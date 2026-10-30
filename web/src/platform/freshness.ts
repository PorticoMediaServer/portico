import {createContext, createElement, useCallback, useContext, useEffect, useMemo, useRef, useSyncExternalStore, type ReactNode} from 'react';
import {DocumentPool, serviceDocument, type PooledDocument, type QuietService} from '@core/freshness-pool.ts';
import {FreshnessHub, dependsOn, type FreshDocument, type FreshDocumentOptions, type FreshnessDependency, type Reachability} from '@core/freshness-hub.ts';
import type {HttpLocalApi} from '@core/index.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
/**
 * Open screens that stay current, quietly (the shared rules are client-core's `freshness-hub`).
 *
 * One hub and one pool of last-known documents per signed-in viewer on one server. They outlive
 * the screens: a document closed by Back is kept, and shown at once when it is opened again.
 * A different viewer or server gets a new hub and pool, and the old ones are let go with
 * everything they held.
 */
export type FreshnessRuntime = {api: HttpLocalApi; scope: string; hub: FreshnessHub; pool: DocumentPool};
let runtime: FreshnessRuntime | undefined;

export function freshnessFor(api: HttpLocalApi, scope: {viewerId: string; serverId: string}): FreshnessRuntime {
  const key = scope.serverId + '\n' + scope.viewerId;
  if (runtime && runtime.api === api && runtime.scope === key) return runtime;
  const old = runtime;
  const hub = new FreshnessHub();
  runtime = {api, scope: key, hub, pool: new DocumentPool(hub, {max: 32})};
  // Let go after this render: screens still unmounting release their documents first.
  if (old) setTimeout(() => { old.pool.dispose(); old.hub.dispose(); }, 0);
  return runtime;
}


const FreshnessContext = createContext<FreshnessRuntime | undefined>(undefined);
export function useFreshness(): FreshnessRuntime {
  const value = useContext(FreshnessContext);
  if (!value) throw new Error('A viewer freshness provider is required.');
  return value;
}
/** Browser signals only; the owning shell supplies its actual session and feed. */
export function bindBrowserFreshness(runtime: FreshnessRuntime, events: EventsClient | null, page: Document = document, browser: Window = window): () => void {
  const {api, hub} = runtime;
  hub.attach(events);
  const visibility = () => hub.setActive(page.visibilityState !== 'hidden');
  const touch = () => hub.touch();
  const kinds = ['keydown', 'pointerdown', 'pointerup', 'wheel', 'touchmove'] as const;
  const options = {capture: true, passive: true} as const;
  page.addEventListener('visibilitychange', visibility);
  for (const kind of kinds) browser.addEventListener(kind, touch, options);
  const source = {};
  const route = () => { const phase = api.getRouteSnapshot()?.phase; hub.report(source, phase !== 'offline' && phase !== 'probing' && phase !== 'identity_mismatch' && phase !== 'unconfirmed'); };
  visibility(); route();
  const off = api.subscribeRoute(route);
  return () => {
    off(); hub.attach(null); hub.report(source, true);
    page.removeEventListener('visibilitychange', visibility);
    for (const kind of kinds) browser.removeEventListener(kind, touch, options);
  };
}
export function FreshnessProvider({api, viewerId, serverId, events, children}: {api: HttpLocalApi; viewerId: string; serverId: string; events: EventsClient | null; children: ReactNode}) {
  const value = useMemo(() => freshnessFor(api, {viewerId, serverId}), [api, viewerId, serverId]);
  useEffect(() => bindBrowserFreshness(value, events), [value, events]);
  return createElement(FreshnessContext.Provider, {value}, children);
}
/** Whether the server has been out of reach for a while. It clears by itself. */
export function useReachability(): Reachability {
  const {hub} = useFreshness();
  return useSyncExternalStore(hub.subscribeReachability, hub.getReachability);
}

type MakeDocument<S> = () => {service: S; document: FreshDocumentOptions; watch?(document: FreshDocument): () => void};
type Store<T> = {getSnapshot(): T; subscribe(listener: () => void): () => void; dispose?(): void};

/**
 * A document kept in the viewer's pool under `key`: the same service, with what it has loaded,
 * every time the same thing is opened. `make` runs only when the pool has nothing under `key`.
 * The document counts as open while this component is mounted.
 */
export function usePooled<S extends Store<T>, T>(key: string, make: MakeDocument<S>): {service: S; snapshot: T; document: FreshDocument; memo: Record<string, unknown>} {
  const {pool} = useFreshness();
  const factory = useRef(make);
  factory.current = make;
  const entry = useMemo(() => pool.peek<S>(key, () => factory.current()), [pool, key]);
  useEffect(() => {
    let held: PooledDocument<S> | undefined = pool.acquire<S>(key, () => factory.current());
    held.setOpen(true);
    return () => { held?.release(); held = undefined; };
  }, [pool, key]);
  const snapshot = useSyncExternalStore(entry.service.subscribe, entry.service.getSnapshot);
  return {service: entry.service, snapshot, document: entry.document, memo: entry.memo};
}

/**
 * A failure is not shown at once. While a first load that failed is being tried again by itself
 * (the first couple of attempts, a few seconds), the screen goes on showing that it is loading;
 * after that its own error state shows, with Try again, and the attempts continue behind it.
 */
export function useRecovering(document: FreshDocument): boolean {
  const state = useSyncExternalStore(document.subscribe, document.getState);
  return (state.stale || state.revalidating) && state.failures < 2;
}

/** `usePooled` for a client-core service that reads itself again with `revalidate()`. */
export function usePooledService<S extends QuietService & Store<T>, T>(key: string, create: () => S, dependencies: readonly FreshnessDependency[] | (() => readonly FreshnessDependency[]), options?: {hasContent?(service: S): boolean}) {
  const held = usePooled<S, T>(key, () => serviceDocument(create(), dependencies, options));
  const recovering = useRecovering(held.document);
  const shown = held.snapshot as T & {phase?: string; error?: unknown};
  // An error in its first moments, while the load is being tried again: still loading as far as the person is concerned.
  const quiet = recovering && shown.phase === 'error';
  const snapshot = useMemo(() => (quiet ? {...shown, phase: 'loading', error: null} as T : held.snapshot), [quiet, shown, held.snapshot]);
  return {...held, snapshot};
}

/**
 * Keeps something that is not pooled current while this component is mounted: `revalidate` is
 * called when one of `dependencies` moved, on the hub's usual terms (gathered, paused for input,
 * retried quietly). Returns `markCurrent`, for when the screen has just read it itself.
 */
/**
 * A screen kept current by its server hints (`dependencies`) and, where something on it moves
 * with no hint (a download's progress), by a clock every `everyMs` while the tab is in front;
 * `everyMs: 0` keeps no clock. The hub decides when a read actually runs (the screen open, input
 * paused) and retries a failed read by itself; nothing is read while the tab is hidden, and one
 * read catches up when it is shown again.
 */
export function useQuietRefresh(name: string, revalidate: FreshDocumentOptions['revalidate'], hasContent: () => boolean = () => true, options: {dependencies?: readonly FreshnessDependency[]; everyMs?: number} = {}): () => void {
  const {hub} = useFreshness();
  const clock = dependsOn.clock(name);
  const everyMs = options.everyMs ?? 30000;
  const key = (options.dependencies ?? []).join('\n');
  const dependencies = useMemo(() => [clock, ...(key ? key.split('\n') : [])], [clock, key]);
  useEffect(() => {
    if (everyMs <= 0) return;
    const timer = setInterval(() => { if (hub.isActive()) hub.note(clock); }, everyMs);
    return () => clearInterval(timer);
  }, [hub, clock, everyMs]);
  return useFreshDocument(dependencies, revalidate, hasContent);
}

export function useFreshDocument(dependencies: readonly FreshnessDependency[] | (() => readonly FreshnessDependency[]), revalidate: FreshDocumentOptions['revalidate'], hasContent: () => boolean = () => true): () => void {
  const {hub} = useFreshness();
  // A function is asked again each time a hint arrives (a list's titles in view change as it scrolls).
  const dynamic = typeof dependencies === 'function';
  const key = dynamic ? '\u0000dynamic' : dependencies.join('\n');
  const latest = useRef({revalidate, hasContent, dependencies});
  latest.current = {revalidate, hasContent, dependencies};
  const held = useRef<FreshDocument | undefined>(undefined);
  useEffect(() => {
    const document = hub.document({dependencies: dynamic ? () => (latest.current.dependencies as () => readonly FreshnessDependency[])() : key ? key.split('\n') : [], revalidate: context => latest.current.revalidate(context), hasContent: () => latest.current.hasContent()});
    held.current = document;
    document.setOpen(true);
    return () => { document.dispose(); if (held.current === document) held.current = undefined; };
  }, [hub, key, dynamic]);
  return useCallback(() => held.current?.markCurrent(), []);
}
