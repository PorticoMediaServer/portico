/**
 * One cache of server documents per signed-in viewer on one server (CLIENT-DATA-REVIEW §5).
 *
 * Screens read through keyed queries instead of building a service of their own: the same
 * document is read once however many owners ask for it, is kept after its screen closes, and is
 * shown at once when it is opened again. It is read again only when the server says something it
 * was built from moved (the FreshnessHub's hints), or when this device changed it; never on a
 * timer and never because the person came back to it. A read that is answered while the person is
 * pressing waits for a pause before it changes the screen, and a failed read keeps what is shown.
 *
 * Privacy: the cache belongs to one viewer scope. `dispose()` aborts every request it started,
 * empties the cache, and makes every later answer an error before it can be stored, so nothing one
 * profile read can reach another. The shell disposes it whenever the server, account, profile,
 * role or privacy fence changes, and builds a new one.
 */
import {QueryClient, hashKey, timeoutManager, type DefaultError, type DefaultedQueryObserverOptions, type Query, type QueryFunction, type QueryFunctionContext, type QueryKey, type QueryObserverOptions} from '@tanstack/query-core';
import {finalAnswer, type FreshDocument, type FreshDocumentOptions, type FreshnessDependency, type FreshnessHub, type RevalidateContext} from '../freshness-hub.ts';
import {linkAbortSignals, throwIfAborted} from '../abort-lifetime.ts';
import {cachedLibraries, cachedTitle, writeDependencies, type ConfirmedWrite} from './write-dependencies.ts';

/** What a query was built from: the hub dependencies it moves with. A function is asked again each time a hint arrives. */
export type QueryDependencies = readonly FreshnessDependency[] | ((data: unknown) => readonly FreshnessDependency[]);

declare module '@tanstack/query-core' {
  interface Register {
    queryMeta: {
      dependencies?: QueryDependencies;
      /** For a query whose value is a live object (a grid's controller with its windowed pages): let it go when it
       * leaves the cache, is replaced by another value, or the viewer's cache ends. Called once per value. */
      release?: (value: unknown) => void;
    };
  }
}

export type ViewerQueryScope = Readonly<{serverId: string; viewerId: string}>;
/** Who a cache belongs to, as both shells build it. */
export type ViewerScopeIdentity = Readonly<{serverId: string; authority: string; accountId: string; profileId: string; role: string}>;
/**
 * The scope of one viewer's cache: the server, then the viewer's authority, account, profile and
 * role, then anything else the shell keys itself on (`more`). Owner and admin documents live in the
 * same cache as everything else, so the role is part of the scope: a viewer whose role changes gets
 * a new, empty cache even if nothing else restarted, and nothing read as an owner is shown to a member.
 */
export function viewerQueryScope(viewer: ViewerScopeIdentity, ...more: readonly unknown[]): ViewerQueryScope {
  if (!viewer.serverId || !viewer.authority || !viewer.accountId || !viewer.profileId || !viewer.role) throw new Error('A viewer query scope needs a server, an authority, an account, a profile and a role.');
  return Object.freeze({serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId, viewer.role, ...more])});
}
export type ViewerRequester = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

export type ViewerQueries = Readonly<{
  scope: ViewerQueryScope;
  client: QueryClient;
  /** The viewer's requester: every request is tied to this cache's lifetime and refused after it ends. */
  api: ViewerRequester;
  /** This device changed these: what was built from them is read again (gathered, after a pause in input). */
  moved(dependencies: readonly FreshnessDependency[]): void;
  /**
   * Keep something that is not a cached document current on this viewer's hub (a grid that re-reads
   * its visible pages in place): read again when its dependencies move, on the hub's usual terms.
   * Open it while it is shown; it ends with the viewer's cache.
   */
  keepCurrent(options: FreshDocumentOptions): FreshDocument;
  /** The hub document that keeps a query current, while the query is in the cache. */
  documentFor(queryKey: QueryKey): FreshDocument | undefined;
  isDisposed(): boolean;
  dispose(): void;
}>;

export type ViewerQueriesOptions = Readonly<{
  scope: ViewerQueryScope;
  hub: FreshnessHub;
  api: ViewerRequester;
  /** How long a document nobody shows stays in the cache. */
  gcTimeMs?: number;
  /** The most documents kept that nobody shows; the least recently shown goes first. */
  maxInactive?: number;
  /**
   * Hear the writes the server confirmed on this device (`HttpLocalApi.onConfirmedWrite`): each one
   * moves what it changed (query/write-dependencies.ts), so every page built from it reads again
   * now, whoever made the write. Heard until the cache ends.
   */
  writes?: (listener: (write: ConfirmedWrite) => void) => () => void;
}>;

// The cache's own timers (collecting a document nobody shows) never keep a process alive: Node
// (tests, the probe tool) lets them go at exit; browsers and React Native have no unref and are unchanged.
const later = (callback: () => void, delay: number) => { const timer = setTimeout(callback, delay); (timer as {unref?: () => void}).unref?.(); return timer; };
type Repeating = {timer?: ReturnType<typeof setTimeout>; [Symbol.toPrimitive]: () => number};
timeoutManager.setTimeoutProvider<ReturnType<typeof setTimeout> | Repeating>({
  setTimeout: (callback, delay) => later(callback, delay),
  clearTimeout: timer => { if (timer !== undefined) clearTimeout(timer as ReturnType<typeof setTimeout>); },
  // Nothing here repeats (no refetch interval), but the provider is whole: a chain of single timers.
  setInterval: (callback, delay) => {
    const handle: Repeating = {[Symbol.toPrimitive]: () => 0};
    const next = () => { handle.timer = later(() => { next(); callback(); }, delay); };
    next();
    return handle;
  },
  clearInterval: handle => { const timer = (handle as Repeating | undefined)?.timer; if (timer !== undefined) clearTimeout(timer); },
});

const abortError = (): Error => Object.assign(new Error('This viewer is no longer signed in here.'), {name: 'AbortError'});

class ViewerQueryClient extends QueryClient {
  private readonly wrapped = new WeakMap<QueryFunction, QueryFunction>();
  private readonly wrappers = new WeakSet<QueryFunction>();
  wrap!: (fn: QueryFunction) => QueryFunction;
  /** The viewer's cache has ended: nothing in it may fetch, and nothing is kept. */
  ended = false;
  // Every query function, however a query was started (an observer, fetchQuery, a prefetch),
  // passes through here: the one place a dead viewer's answer is refused and a quiet read waits
  // for the person's input to pause before its answer is stored.
  override defaultQueryOptions<TQueryFnData = unknown, TError = DefaultError, TData = TQueryFnData, TQueryData = TQueryFnData, TQueryKey extends QueryKey = QueryKey, TPageParam = never>(
    options: QueryObserverOptions<TQueryFnData, TError, TData, TQueryData, TQueryKey, TPageParam> | DefaultedQueryObserverOptions<TQueryFnData, TError, TData, TQueryData, TQueryKey>,
  ): DefaultedQueryObserverOptions<TQueryFnData, TError, TData, TQueryData, TQueryKey> {
    const defaulted = super.defaultQueryOptions(options);
    const fn = defaulted.queryFn as QueryFunction | undefined;
    if (typeof fn === 'function' && !this.wrappers.has(fn)) {
      let wrapper = this.wrapped.get(fn);
      if (!wrapper) { wrapper = this.wrap(fn); this.wrapped.set(fn, wrapper); this.wrappers.add(wrapper); }
      (defaulted as {queryFn?: unknown}).queryFn = wrapper;
    }
    // A screen of the ended viewer still unmounting may ask again: it gets an inert, empty query.
    if (this.ended) return {...defaulted, enabled: false, gcTime: 0};
    return defaulted;
  }
}

export function createViewerQueries(options: ViewerQueriesOptions): ViewerQueries {
  const scope = Object.freeze({serverId: options.scope.serverId, viewerId: options.scope.viewerId});
  if (!scope.serverId || !scope.viewerId) throw new Error('A viewer query scope needs a server and a viewer.');
  const hub = options.hub, base = options.api;
  const life = new AbortController();
  let disposed = false;
  const live = () => { if (disposed) throw abortError(); };

  const api: ViewerRequester = Object.freeze({
    async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
      live();
      const linked = linkAbortSignals([life.signal, signal]);
      try {
        throwIfAborted(linked.signal);
        const value = await base.request<T>(path, method, body, linked.signal);
        live(); throwIfAborted(linked.signal);
        return value;
      } finally { linked.dispose(); }
    },
  });

  // Declared first: the client's staleness asks the bridge, which is built from the client.
  let bridge: QueryFreshness | undefined;
  const client: ViewerQueryClient = new ViewerQueryClient({
    defaultOptions: {
      queries: {
        // Staleness is the hub's to say: a query is out of date only while its document owes a read.
        staleTime: (query: Query): number => (bridge?.owes(query) ? 0 : Infinity),
        gcTime: options.gcTimeMs ?? 5 * 60_000,
        // The hub retries failed reads on its own waits and raises one quiet unreachable state.
        retry: false,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
        // Reachability is the route connection's to judge, not the browser's online flag.
        networkMode: 'always',
      },
    },
  });

  const settles = new Map<string, RevalidateContext['settle']>();
  const kept = new Set<FreshDocument>();
  client.wrap = (fn: QueryFunction): QueryFunction => async (context: QueryFunctionContext) => {
    live();
    const value = await fn(context);
    live();
    // No custom key hash is ever set, so a query's hash is its key's.
    const settle = settles.get(hashKey(context.queryKey));
    if (settle) { await settle(); live(); }
    return value;
  };

  const freshness = bridge = new QueryFreshness(client, hub, settles, options.maxInactive ?? 64);
  const unhear = options.writes?.(write => {
    if (disposed) return;
    for (const dependency of writeDependencies(write, {title: itemId => cachedTitle(client, itemId), libraries: () => cachedLibraries(client)})) hub.note(dependency);
  });

  return Object.freeze({
    scope, client, api,
    moved(dependencies: readonly FreshnessDependency[]) { if (!disposed) for (const d of dependencies) hub.note(d); },
    keepCurrent(options: FreshDocumentOptions) {
      const document = hub.document(options);
      if (disposed) { document.dispose(); return document; }
      // An owner that ends its document first leaves the cache's list too.
      const end = document.dispose.bind(document);
      document.dispose = () => { kept.delete(document); end(); };
      kept.add(document);
      return document;
    },
    documentFor(queryKey: QueryKey) { const query = client.getQueryCache().find({queryKey, exact: true}); return query ? freshness.document(query) : undefined; },
    isDisposed: () => disposed,
    dispose() {
      if (disposed) return;
      disposed = true;
      unhear?.();
      client.ended = true;
      life.abort(abortError());
      const all = client.getQueryCache().getAll();
      // Live values (a grid's controller) are let go before anything is reset, so none outlives its viewer.
      for (const query of all) freshness.releaseHeld(query);
      freshness.stop();
      void client.cancelQueries();
      // A screen of this viewer still mounted while the shell changes over is told its document is
      // empty now, so it can never draw it again (removing a query alone would leave its last
      // result), and the query is collected as soon as that screen lets go of it.
      for (const query of all) { query.reset(); query.gcTime = 0; }
      client.clear();
      client.getMutationCache().clear();
      client.unmount();
      settles.clear();
      for (const document of [...kept]) document.dispose();
      kept.clear();
    },
  });
}

/**
 * Keeps every query that declares `meta.dependencies` current through the viewer's FreshnessHub:
 * one hub document per query, open while some screen observes it. The hub decides when to read
 * (hints gathered, input paused, failures backed off); the read is the query's own fetch, so every
 * observer of the same document sees the one answer.
 */
class QueryFreshness {
  private readonly documents = new Map<Query, FreshDocument>();
  /** Queries whose fetch in flight was started by the hub, not by a screen. */
  private readonly hubReads = new WeakSet<Query>();
  private readonly lastShown = new Map<Query, number>();
  /** The live value each releasing query holds, so it is let go exactly once. */
  private readonly held = new Map<Query, unknown>();
  private readonly stopCache: () => void;
  private shown = 0;
  private readonly client: QueryClient;
  private readonly hub: FreshnessHub;
  private readonly settles: Map<string, RevalidateContext['settle']>;
  private readonly maxInactive: number;

  constructor(client: QueryClient, hub: FreshnessHub, settles: Map<string, RevalidateContext['settle']>, maxInactive: number) {
    this.client = client; this.hub = hub; this.settles = settles; this.maxInactive = Math.max(0, maxInactive);
    this.stopCache = client.getQueryCache().subscribe(event => {
      const query = event.query as Query;
      switch (event.type) {
        case 'added': case 'observerAdded': case 'observerRemoved': case 'observerOptionsUpdated': this.sync(query); if (event.type === 'observerRemoved') this.trim(query); break;
        case 'removed': this.releaseHeld(query); this.forget(query); break;
        case 'updated': this.track(query); this.updated(query, event.action.type, event.action.type === 'error' ? event.action.error : undefined); break;
      }
    });
  }

  /** The query's document owes a read: a screen that opens it, or a caller asking for it, reads it again. */
  owes(query: Query): boolean { return this.documents.get(query)?.getState().stale === true; }

  document(query: Query): FreshDocument | undefined { return this.documents.get(query); }

  /** A releasing query's value changed: the one it held is let go. */
  private track(query: Query): void {
    const release = query.meta?.release;
    if (!release) return;
    const before = this.held.get(query), now = query.state.data;
    if (before === now) return;
    if (before !== undefined) { try { release(before); } catch { /* a failed release never stops the cache */ } }
    if (now === undefined) this.held.delete(query); else this.held.set(query, now);
  }
  releaseHeld(query: Query): void {
    const release = query.meta?.release, value = this.held.get(query);
    this.held.delete(query);
    if (release && value !== undefined) { try { release(value); } catch { /* a failed release never stops the cache */ } }
  }

  stop(): void {
    this.stopCache();
    this.held.clear();
    for (const document of this.documents.values()) document.dispose();
    this.documents.clear(); this.lastShown.clear();
  }

  private dependencies(query: Query): readonly FreshnessDependency[] | undefined {
    const declared = query.meta?.dependencies;
    if (!declared) return undefined;
    if (typeof declared !== 'function') return declared;
    try { return declared(query.state.data); } catch { return []; }
  }

  private sync(query: Query): void {
    if (!query.meta?.dependencies) return;
    let document = this.documents.get(query);
    if (!document) {
      const read = (context: RevalidateContext) => this.read(query, context);
      document = this.hub.document({dependencies: () => this.dependencies(query) ?? [], revalidate: read, recover: read, hasContent: () => query.state.data !== undefined});
      this.documents.set(query, document);
    }
    const open = query.isActive();
    if (open) this.lastShown.set(query, ++this.shown);
    document.setOpen(open);
  }

  private updated(query: Query, action: string, error: unknown): void {
    const document = this.documents.get(query);
    if (!document) return;
    if (action === 'fetch' && query.state.fetchStatus === 'fetching' && !this.hubReads.has(query)) document.markCurrent();
    else if (action === 'success') document.check();
    // A first load that failed with nothing to show: the hub tries it again on its own waits.
    else if (action === 'error' && !this.hubReads.has(query) && query.state.data === undefined && !finalAnswer(error) && !isAbort(error)) document.invalidate();
  }

  private async read(query: Query, context: RevalidateContext): Promise<void> {
    throwIfAborted(context.signal);
    if (this.client.getQueryCache().get(query.queryHash) !== query) return;
    this.hubReads.add(query);
    this.settles.set(query.queryHash, context.settle);
    // Stopped (covered, backgrounded): the query goes back to what it showed, with no error.
    const stop = () => { void query.cancel({revert: true, silent: true}); };
    context.signal.addEventListener('abort', stop, {once: true});
    try {
      // A read already in flight may have started before what moved, so its answer cannot be
      // trusted to include it. A first load is let finish (it is what fills the screen); a quiet
      // re-read in flight is replaced. Either way, this read's answer is the one that counts.
      if (query.state.fetchStatus !== 'idle' && query.state.data === undefined) await query.fetch().catch(() => undefined);
      throwIfAborted(context.signal);
      await query.fetch(undefined, {cancelRefetch: true});
    } finally {
      context.signal.removeEventListener('abort', stop);
      this.hubReads.delete(query);
      if (this.settles.get(query.queryHash) === context.settle) this.settles.delete(query.queryHash);
    }
  }

  private forget(query: Query): void {
    this.documents.get(query)?.dispose();
    this.documents.delete(query);
    this.lastShown.delete(query);
  }

  /** Keep at most `maxInactive` documents nobody shows, dropping the least recently shown. */
  private trim(released: Query): void {
    if (released.isActive()) return;
    const cache = this.client.getQueryCache();
    const inactive = cache.getAll().filter(q => !q.isActive() && q.state.fetchStatus !== 'fetching');
    if (inactive.length <= this.maxInactive) return;
    inactive.sort((a, b) => (this.lastShown.get(a) ?? 0) - (this.lastShown.get(b) ?? 0));
    for (const query of inactive.slice(0, inactive.length - this.maxInactive)) cache.remove(query);
  }
}

const isAbort = (error: unknown): boolean => {
  const name = (error as {name?: unknown} | null)?.name;
  return name === 'AbortError' || name === 'CancelledError' || (error as {revert?: unknown} | null)?.revert !== undefined;
};

export type {DefaultError};
