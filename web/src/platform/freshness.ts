import {createContext, createElement, useCallback, useContext, useEffect, useMemo, useRef, useSyncExternalStore, type ReactNode} from 'react';
import {createViewerQueries, type ViewerQueries} from '@core/query/viewer-queries.ts';
import {createQueryHooks, type ViewerQueryOptions} from '@core/query/react.ts';
import type {QueryKey, QueryObserverResult} from '@tanstack/query-core';
import {FreshnessHub, dependsOn, type FreshDocument, type FreshDocumentOptions, type FreshnessDependency, type Reachability} from '@core/freshness-hub.ts';
import type {HttpLocalApi} from '@core/index.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
/**
 * Open screens that stay current, quietly (the shared rules are client-core's `freshness-hub`).
 *
 * One hub and one cache of server documents (`queries`, client-core's query/viewer-queries) per
 * signed-in viewer on one server. They outlive the screens: a document closed by Back is kept, and
 * shown at once when it is opened again. A different viewer or server gets a new hub and cache;
 * the old cache is emptied at once, with every request it started aborted, so nothing one viewer
 * read reaches the next.
 */
export type FreshnessRuntime = {api: HttpLocalApi; scope: string; hub: FreshnessHub; queries: ViewerQueries; pages: PageMemory};
let runtime: FreshnessRuntime | undefined;
/** The page a route last showed, in the order it was last shown. */
const PAGE_MEMORY_MAX = 24;

/**
 * Last-known pages, kept beside the viewer (not the screen) so a route that is opened
 * again — browser Back and Forward, a cover from the player, a card tapped twice — draws
 * the page it had at once and reads it again in place. Only the screen's own service
 * adopts a page: it is shown as that service's state, so every command keeps working.
 */
export type PageMemory = {
  remember<T>(key: string, page: T): void;
  recall<T>(key: string): T | undefined;
  forget(key: string): void;
};
function pageMemory(): PageMemory {
  const held = new Map<string, unknown>();
  return {
    remember(key, page) {held.delete(key); held.set(key, page); for (const oldest of held.keys()) {if (held.size <= PAGE_MEMORY_MAX) break; held.delete(oldest);}},
    recall: <T,>(key: string) => held.get(key) as T | undefined,
    forget: key => {held.delete(key);},
  };
}

export function freshnessFor(api: HttpLocalApi, scope: {viewerId: string; serverId: string}): FreshnessRuntime {
  const key = scope.serverId + '\n' + scope.viewerId;
  if (runtime && runtime.api === api && runtime.scope === key && !runtime.queries.isDisposed()) return runtime;
  const old = runtime;
  const hub = new FreshnessHub();
  runtime = {api, scope: key, hub, queries: createViewerQueries({scope, hub, api}), pages: pageMemory()};
  if (old) {
    // The old viewer's documents go now; nothing may be shown or stored from them again.
    old.queries.dispose();
    // The hub after this render: screens still unmounting release their documents first.
    setTimeout(() => old.hub.dispose(), 0);
  }
  return runtime;
}

/** Ends a viewer's runtime (the shell's session ended or restarted): its cache is emptied at once. */
export function disposeFreshness(value: FreshnessRuntime): void {
  value.queries.dispose();
  value.hub.dispose();
  if (runtime === value) runtime = undefined;
}


const FreshnessContext = createContext<FreshnessRuntime | undefined>(undefined);
export function useFreshness(): FreshnessRuntime {
  const value = useContext(FreshnessContext);
  if (!value) throw new Error('A viewer freshness provider is required.');
  return value;
}
/** This viewer's cache of server documents. */
export function useViewerQueries(): ViewerQueries {
  return useFreshness().queries;
}
const hooks = createQueryHooks({useMemo, useEffect, useCallback, useSyncExternalStore});
/**
 * The document under `options.queryKey`, from this viewer's cache: read once however many screens
 * ask, kept after the screen closes, and read again in place when the server says it moved.
 */
export function useQuery<TData, TKey extends QueryKey = QueryKey>(options: ViewerQueryOptions<TData, TKey>): QueryObserverResult<TData, Error> {
  return hooks.useViewerQuery(useFreshness().queries, options);
}
/** The viewer's last-known pages, for a route that shows what it had when it is opened again. */
export function usePageMemory(): PageMemory {
  return useFreshness().pages;
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

/**
 * A failure is not shown at once. While a first load that failed is being tried again by itself
 * (the first couple of attempts, a few seconds), the screen goes on showing that it is loading;
 * after that its own error state shows, with Try again, and the attempts continue behind it.
 */
export function useRecovering(document: FreshDocument): boolean {
  const state = useSyncExternalStore(document.subscribe, document.getState);
  return (state.stale || state.revalidating) && state.failures < 2;
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
