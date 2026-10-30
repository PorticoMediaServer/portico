/**
 * A windowed, sparse collection: the client-side half of the scale rule
 * (5–10M item libraries; client cost is O(visible), never O(library)).
 *
 * API-agnostic: it never calls the server. The caller supplies
 * `fetchPage(start, count, signal)`; the store decides which pages to hold.
 *
 * - Pages are sparse, keyed by page index. Nothing is ever sized by `total`:
 *   no array, map or loop is O(total). `total` (known from the first page)
 *   only sizes scrollbars and placeholders.
 * - `ensureRange(first, last)` declares the visible range. The store keeps the
 *   visible pages, prefetches one page either side (two in the direction of
 *   travel), dedupes requests already in flight, aborts requests for pages
 *   that left interest, and evicts least-recently-used pages beyond
 *   `maxResidentPages` (never a visible one, nor the pinned one).
 * - `pin(index)` keeps one item's page in memory whatever the budget: the
 *   focused card, so a focus that outlives its scroll position (a covered
 *   page, an interrupted row scroll, Back or Undo) always finds its item.
 * - `positionIndex` (letter → start offset) supports letter jumps and random access.
 * - `invalidate()` (a new filter or sort) starts a fresh generation but keeps
 *   showing the old one until the new generation's first visible page has
 *   arrived, then swaps atomically: no flash to empty.
 * - Subscriptions are per page, for React `useSyncExternalStore`, plus one
 *   coarse subscription for total, status and generation.
 * - Unloaded indices read as placeholders with stable keys `placeholder:<i>`.
 */
export type PageResult<T> = Readonly<{
  items: readonly T[];
  /** Total items in the collection (all pages). */
  total: number;
  /** `total` is a lower bound: the list goes on past it (a broad filter the server counts only up to
   * a threshold). The collection's total then only grows, page by page, until a page knows the end. */
  totalIsLowerBound?: boolean;
  /** Letter (or any bucket label) → first offset, when the server provides it. */
  positionIndex?: Readonly<Record<string, number>>;
}>;

export type FetchPage<T> = (start: number, count: number, signal: AbortSignal) => Promise<PageResult<T>>;

export type WindowedCollectionOptions<T> = Readonly<{
  fetchPage: FetchPage<T>;
  /** Items per request. */
  pageSize: number;
  /** Most pages kept in memory; resident items ≤ maxResidentPages × pageSize (visible pages excepted). */
  maxResidentPages: number;
  /** Stable identity of an item (used for list keys). */
  keyOf: (item: T) => string;
  /** Pages prefetched either side of the visible range (default 1; one more in the direction of travel). */
  prefetchPages?: number;
  /** Parallel page requests (default 3). */
  maxConcurrent?: number;
  /** A failed page is retried by ensureRange after this long (default 5 s). */
  retryAfterMs?: number;
  now?: () => number;
}>;

export type Slot<T> =
  | Readonly<{kind: 'item'; index: number; key: string; item: T}>
  | Readonly<{kind: 'placeholder'; index: number; key: string}>;

export type CollectionStatus = 'idle' | 'loading' | 'ready' | 'error';

export type CollectionSnapshot = Readonly<{
  /** Increments on every invalidate that has swapped in. */
  generation: number;
  /** Unknown until the first page of the generation has arrived. */
  total: number | undefined;
  /** The total is a lower bound and grows as pages arrive ("10,000+"). */
  totalIsLowerBound: boolean;
  status: CollectionStatus;
  /** The last page failure, when the visible range couldn't load. */
  error: unknown;
  positionIndex: Readonly<Record<string, number>> | undefined;
  /** A new generation is loading behind the one on screen. */
  refreshing: boolean;
}>;

export type WindowedCollection<T> = {
  readonly pageSize: number;
  getSnapshot(): CollectionSnapshot;
  subscribe(listener: () => void): () => void;
  /** Notified only when this page changes (loaded, failed, evicted, or the generation swapped). */
  subscribePage(page: number, listener: () => void): () => void;
  /** A value that changes whenever the page changes (for useSyncExternalStore). */
  getPageVersion(page: number): string;
  pageOf(index: number): number;
  itemAt(index: number): T | undefined;
  keyAt(index: number): string;
  slotAt(index: number): Slot<T>;
  /** The visible range, inclusive. Call on every scroll/layout change; cheap when nothing changed. */
  ensureRange(first: number, last: number): void;
  /** Keep this index's page in memory (loading it if needed) until the pin moves or is cleared with
   * `undefined`. One pin at a time; a new filter or sort (`invalidate`) clears it. */
  pin(index: number | undefined): void;
  /** Offset for a letter from the positionIndex, if known. */
  indexOfLetter(letter: string): number | undefined;
  /** Start a new generation (filter/sort change). The old one stays on screen until the new first page arrives. */
  invalidate(fetchPage?: FetchPage<T>): void;
  /**
   * Read what is on screen again, quietly: the same list, as the server has it now. Nothing
   * changes until every visible page of the new reading has arrived; then it replaces the old one
   * in a single step, at the same scroll position. A page that fails leaves the list exactly as
   * it was and rejects, so the caller can try again later. `hold` is awaited just before the
   * swap (the person may be mid-press). Resolves at once when nothing has been shown yet.
   */
  revalidate(options?: {fetchPage?: FetchPage<T>; hold?: () => Promise<void>}): Promise<void>;
  /** Retry failed pages in the current interest now. */
  retry(): void;
  /** Pages and items held in memory (for tests and diagnostics). */
  resident(): {pages: number; items: number};
  /** The items of the generation on screen that are held in memory (at most maxResidentPages × pageSize). */
  residentItems(): readonly T[];
  dispose(): void;
};

type PageState<T> =
  | {status: 'loading'; controller: AbortController; version: number}
  | {status: 'ready'; items: readonly T[]; version: number; used: number}
  | {status: 'error'; error: unknown; at: number; version: number};

type Generation<T> = {
  id: number;
  fetchPage: FetchPage<T>;
  pages: Map<number, PageState<T>>;
  total: number | undefined;
  totalIsLowerBound: boolean;
  positionIndex: Record<string, number> | undefined;
  lastError: unknown;
  /** The page whose arrival makes this generation ready to show (pending generations only). */
  gate: number;
  /** A quiet reading of what is on screen: it swaps in only when whole, and is dropped when a page fails. */
  quiet?: {resolve: () => void; reject: (error: unknown) => void; hold?: () => Promise<void>; holding?: boolean};
};

const abortError = () => { const e = new Error('Aborted'); e.name = 'AbortError'; return e; };

/** The longest a server's "try again later" is honoured for a list page; beyond it the viewer's own Try again decides. */
const MAX_SERVER_WAIT_MS = 10 * 60 * 1000;
/** How long the server asked callers to wait when it refused a page (its `Retry-After`), in milliseconds; 0 when it did not say. */
function serverWait(error: unknown): number {
  const e = error as {retryAfterMs?: unknown; retryAfterSeconds?: unknown} | null;
  const ms = typeof e?.retryAfterMs === 'number' ? e.retryAfterMs : typeof e?.retryAfterSeconds === 'number' ? e.retryAfterSeconds * 1000 : 0;
  return Number.isFinite(ms) && ms > 0 ? Math.min(ms, MAX_SERVER_WAIT_MS) : 0;
}

export function createWindowedCollection<T>(options: WindowedCollectionOptions<T>): WindowedCollection<T> {
  const pageSize = Math.max(1, Math.floor(options.pageSize));
  const maxResident = Math.max(1, Math.floor(options.maxResidentPages));
  const prefetch = Math.max(0, Math.floor(options.prefetchPages ?? 1));
  const maxConcurrent = Math.max(1, Math.floor(options.maxConcurrent ?? 3));
  const retryAfterMs = options.retryAfterMs ?? 5000;
  const now = options.now ?? Date.now;
  const keyOf = options.keyOf;

  let nextId = 0;
  let current: Generation<T> = {id: nextId++, fetchPage: options.fetchPage, pages: new Map(), total: undefined, totalIsLowerBound: false, positionIndex: undefined, lastError: undefined, gate: 0};
  let pending: Generation<T> | undefined;
  let visibleFirst = -1, visibleLast = -1, lastFirst = -1, direction = 0;
  let pinned: number | undefined;
  let tick = 0, disposed = false;
  let shownGeneration = 0;
  // While the server has asked for a pause, scrolling does not start new page requests: the
  // pause outlives the failed page that reported it (a failure out of view is forgotten).
  let waitUntil = 0;
  const pageListeners = new Map<number, Set<() => void>>();
  const listeners = new Set<() => void>();
  let snapshot: CollectionSnapshot = Object.freeze({generation: 0, total: undefined, totalIsLowerBound: false, status: 'idle', error: undefined, positionIndex: undefined, refreshing: false});

  const inFlight = (g: Generation<T>) => { let n = 0; for (const p of g.pages.values()) if (p.status === 'loading') n++; return n; };
  const pageCount = (g: Generation<T>) => (g.total === undefined ? undefined : Math.ceil(g.total / pageSize));
  const exactEmpty = (g: Generation<T>) => g.total === 0 && !g.totalIsLowerBound;

  function notifyPage(page: number) { const set = pageListeners.get(page); if (set) for (const l of [...set]) l(); }
  function notifyAllPages() { for (const [, set] of pageListeners) for (const l of [...set]) l(); }
  function publish() {
    const pages = visibleFirst < 0 ? [] : [...range(pageOfIndex(visibleFirst), pageOfIndex(visibleLast))].filter(p => { const c = pageCount(current); return c === undefined || p < c; });
    const failed = pages.find(p => current.pages.get(p)?.status === 'error');
    const status: CollectionStatus = failed !== undefined ? 'error'
      : current.total === undefined ? (inFlight(current) > 0 ? 'loading' : 'idle')
      : pages.some(p => current.pages.get(p)?.status !== 'ready') ? 'loading' : 'ready';
    const failure = failed !== undefined ? (current.pages.get(failed) as {error: unknown}).error : undefined;
    const next = {generation: shownGeneration, total: current.total, totalIsLowerBound: current.totalIsLowerBound, status, error: failure, positionIndex: current.positionIndex, refreshing: !!pending && !pending.quiet};
    const prev = snapshot;
    if (prev.generation === next.generation && prev.total === next.total && prev.totalIsLowerBound === next.totalIsLowerBound && prev.status === next.status && prev.error === next.error && prev.positionIndex === next.positionIndex && prev.refreshing === next.refreshing) return;
    snapshot = Object.freeze(next);
    for (const l of [...listeners]) l();
  }
  function* range(a: number, b: number) { for (let i = a; i <= b; i++) yield i; }
  function pageOfIndex(index: number) { return Math.max(0, Math.floor(index / pageSize)); }

  /** Pages wanted for the current visible range, visible first (centre-out), then prefetch in travel order. */
  function interest(g: Generation<T>): {visible: number[]; wanted: number[]} {
    if (visibleFirst < 0 || exactEmpty(g)) return {visible: [], wanted: []};
    const count = pageCount(g);
    const clamp = (p: number) => (count === undefined ? p : Math.min(p, Math.max(0, count - 1)));
    const firstPage = clamp(pageOfIndex(visibleFirst)), lastPage = clamp(pageOfIndex(visibleLast));
    const visible = [...range(firstPage, lastPage)];
    const ahead = prefetch + (direction > 0 ? 1 : 0), behind = prefetch + (direction < 0 ? 1 : 0);
    const extra: number[] = [];
    // Before the first answer the page count is unknown: prefetch nothing past the visible pages.
    const forward = count === undefined ? [] : [...range(lastPage + 1, lastPage + ahead)].filter(p => p < count);
    const backward = [...range(Math.max(0, firstPage - behind), firstPage - 1)].reverse();
    if (direction < 0) extra.push(...backward, ...forward); else extra.push(...forward, ...backward);
    const held = pinnedPage(g);
    if (held !== undefined && !visible.includes(held) && !extra.includes(held)) extra.unshift(held);
    return {visible, wanted: [...visible, ...extra]};
  }

  /** The pinned item's page, while it is a page of this generation. */
  function pinnedPage(g: Generation<T>): number | undefined {
    if (pinned === undefined || exactEmpty(g)) return undefined;
    const page = pageOfIndex(pinned), count = pageCount(g);
    return count === undefined || page < count ? page : undefined;
  }

  function evict(g: Generation<T>, keep: ReadonlySet<number>, pinned: ReadonlySet<number>) {
    let ready = 0;
    for (const p of g.pages.values()) if (p.status === 'ready') ready++;
    if (ready <= maxResident) return;
    // Least recently used first; pages outside interest before wanted-but-not-visible ones; never visible.
    const candidates = [...g.pages].filter(([page, p]) => p.status === 'ready' && !pinned.has(page)) as [number, Extract<PageState<T>, {status: 'ready'}>][];
    candidates.sort((a, b) => (keep.has(a[0]) === keep.has(b[0]) ? a[1].used - b[1].used : keep.has(a[0]) ? 1 : -1));
    for (const [page] of candidates) {
      if (ready <= maxResident) break;
      g.pages.delete(page); ready--;
      if (g === current) notifyPage(page);
    }
  }

  function load(g: Generation<T>, page: number) {
    const controller = new AbortController();
    const version = (g.pages.get(page)?.version ?? 0) + 1;
    g.pages.set(page, {status: 'loading', controller, version});
    const start = page * pageSize;
    let request: Promise<PageResult<T>>;
    try { request = g.fetchPage(start, pageSize, controller.signal); } catch (e) { request = Promise.reject(e); }
    request.then(result => {
      if (disposed || controller.signal.aborted || g.pages.get(page)?.status !== 'loading') return;
      if (!result || !Array.isArray(result.items) || !Number.isFinite(result.total) || result.total < 0) throw new Error('invalid_page');
      if (result.totalIsLowerBound) {
        // An open-ended total only grows; a total some page already knows exactly is kept.
        if (g.total === undefined || g.totalIsLowerBound) { g.total = Math.max(g.total ?? 0, Math.floor(result.total)); g.totalIsLowerBound = true; }
      } else {
        g.total = Math.floor(result.total);
        g.totalIsLowerBound = false;
      }
      if (result.positionIndex) g.positionIndex = {...(g.positionIndex ?? {}), ...result.positionIndex};
      g.pages.set(page, {status: 'ready', items: result.items.slice(0, pageSize), version: version + 1, used: ++tick});
      settled(g, page);
    }).catch(error => {
      if (disposed || controller.signal.aborted || g.pages.get(page)?.status !== 'loading') return;
      g.lastError = error;
      const asked = serverWait(error);
      if (asked) waitUntil = Math.max(waitUntil, now() + asked);
      g.pages.set(page, {status: 'error', error, at: now(), version: version + 1});
      settled(g, page);
    });
  }

  function settled(g: Generation<T>, page: number) {
    // A pending generation swaps in once the page the viewer is looking at has an answer (items or a failure to show).
    if (g === pending && g.quiet) { quietSettled(g); }
    else if (g === pending) { const gate = g.pages.get(g.gate)?.status; if (gate === 'ready' || gate === 'error') swap(); }
    if (g === current) notifyPage(page);
    pump();
    publish();
  }

  function dropPending() {
    if (!pending) return;
    for (const p of pending.pages.values()) if (p.status === 'loading') p.controller.abort();
    pending = undefined;
  }

  /** A quiet reading replaces the list only when every page in view has arrived; one failure drops it. */
  function quietSettled(g: Generation<T>) {
    const quiet = g.quiet!;
    const {visible} = interest(g);
    const pages = exactEmpty(g) ? [] : visible.length ? visible : [g.gate];
    const failed = pages.find(p => g.pages.get(p)?.status === 'error');
    if (failed !== undefined) {
      const error = (g.pages.get(failed) as {error: unknown}).error;
      dropPending();
      quiet.reject(error);
      return;
    }
    if (pages.some(p => g.pages.get(p)?.status !== 'ready') || quiet.holding) return;
    const finish = () => {
      if (disposed || pending !== g) return;
      // The view may have moved while waiting: only swap once what is in view now is in hand.
      const now = interest(g).visible;
      if (now.some(p => g.pages.get(p)?.status !== 'ready')) { quiet.holding = false; pump(); return; }
      swap();
      publish();
      quiet.resolve();
    };
    if (!quiet.hold) { finish(); return; }
    quiet.holding = true;
    void quiet.hold().then(finish, finish);
  }

  function swap() {
    const old = current;
    for (const p of old.pages.values()) if (p.status === 'loading') p.controller.abort();
    current = pending!;
    pending = undefined;
    shownGeneration++;
    notifyAllPages();
    pump();
  }

  /** Start wanted pages up to the concurrency limit; abort what left interest; evict beyond the budget. */
  function pump() {
    if (disposed) return;
    for (const g of pending ? [pending, current] : [current]) {
      const {visible, wanted} = interest(g);
      const wantedSet = new Set(wanted), visibleSet = new Set(visible);
      for (const [page, p] of g.pages) {
        if (wantedSet.has(page)) continue;
        if (p.status === 'loading') { p.controller.abort(); g.pages.delete(page); if (g === current) notifyPage(page); }
        else if (p.status === 'error') g.pages.delete(page); // a failure out of view is forgotten; it's retried if it comes back
      }
      // The old generation only keeps what it has while a new one loads; it starts nothing new.
      // (Behind a quiet reading the list on screen is still the list: it goes on loading as the person scrolls.)
      const held = pinnedPage(g), kept = held === undefined ? visibleSet : new Set([...visibleSet, held]);
      if (g === current && pending && !pending.quiet) { evict(g, wantedSet, kept); continue; }
      let active = inFlight(g);
      for (const page of wanted) {
        if (active >= maxConcurrent) break;
        const state = g.pages.get(page);
        if (state?.status === 'ready') { state.used = ++tick; continue; }
        if (state?.status === 'loading') continue;
        if (state?.status === 'error' && now() - state.at < retryAfterMs) continue;
        if (now() < waitUntil) continue;
        const count = pageCount(g);
        if (count !== undefined && page >= count) continue;
        load(g, page); active++;
      }
      evict(g, wantedSet, kept);
    }
  }

  function readyItems(page: number): readonly T[] | undefined {
    const p = current.pages.get(page);
    return p && p.status === 'ready' ? p.items : undefined;
  }

  function itemAt(index: number): T | undefined {
    if (!Number.isInteger(index) || index < 0) return undefined;
    return readyItems(pageOfIndex(index))?.[index % pageSize];
  }

  return {
    pageSize,
    getSnapshot: () => snapshot,
    subscribe(listener) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    subscribePage(page, listener) {
      let set = pageListeners.get(page);
      if (!set) { set = new Set(); pageListeners.set(page, set); }
      set.add(listener);
      return () => { set!.delete(listener); if (!set!.size) pageListeners.delete(page); };
    },
    getPageVersion(page) { return `${shownGeneration}:${current.pages.get(page)?.version ?? 0}`; },
    pageOf: pageOfIndex,
    itemAt,
    keyAt: index => { const item = itemAt(index); return item === undefined ? `placeholder:${index}` : keyOf(item); },
    slotAt: index => { const item = itemAt(index); return item === undefined ? {kind: 'placeholder', index, key: `placeholder:${index}`} : {kind: 'item', index, key: keyOf(item), item}; },
    ensureRange(first, last) {
      if (disposed) return;
      const a = Math.max(0, Math.floor(Math.min(first, last))), b = Math.max(a, Math.floor(Math.max(first, last)));
      if (a === visibleFirst && b === visibleLast) { pump(); return; }
      direction = lastFirst < 0 ? 0 : Math.sign(a - lastFirst);
      lastFirst = a; visibleFirst = a; visibleLast = b;
      if (pending) pending.gate = pageOfIndex(a);
      pump();
      publish();
    },
    pin(index) {
      if (disposed) return;
      const next = index === undefined || !Number.isSafeInteger(index) || index < 0 ? undefined : index;
      if (next === pinned) return;
      pinned = next;
      pump();
    },
    indexOfLetter(letter) {
      const index = current.positionIndex;
      if (!index) return undefined;
      if (Object.prototype.hasOwnProperty.call(index, letter)) return index[letter];
      const upper = letter.toUpperCase();
      return Object.prototype.hasOwnProperty.call(index, upper) ? index[upper] : undefined;
    },
    revalidate(options = {}) {
      if (disposed || current.total === undefined || visibleFirst < 0) return Promise.resolve();
      // A filter or sort change already on its way is newer than this reading.
      if (pending && !pending.quiet) return Promise.resolve();
      if (pending?.quiet) { const superseded = pending.quiet; dropPending(); superseded.resolve(); }
      return new Promise<void>((resolve, reject) => {
        pending = {id: nextId++, fetchPage: options.fetchPage ?? current.fetchPage, pages: new Map(), total: undefined, totalIsLowerBound: false, positionIndex: undefined, lastError: undefined, gate: pageOfIndex(visibleFirst), quiet: {resolve, reject, hold: options.hold}};
        pump();
      });
    },
    invalidate(fetchPage) {
      if (disposed) return;
      pinned = undefined;
      if (pending?.quiet) pending.quiet.resolve();
      if (pending) for (const p of pending.pages.values()) if (p.status === 'loading') p.controller.abort();
      pending = {id: nextId++, fetchPage: fetchPage ?? current.fetchPage, pages: new Map(), total: undefined, totalIsLowerBound: false, positionIndex: undefined, lastError: undefined, gate: visibleFirst < 0 ? 0 : pageOfIndex(visibleFirst)};
      // Nothing on screen yet: there is nothing to keep, so show the new generation's loading state at once.
      if (current.pages.size === 0) { current = pending; pending = undefined; shownGeneration++; notifyAllPages(); }
      pump();
      publish();
    },
    retry() {
      // The viewer asked: their Try again is not held back by the automatic pause.
      waitUntil = 0;
      for (const g of pending ? [pending, current] : [current]) for (const [page, p] of g.pages) if (p.status === 'error') g.pages.delete(page);
      pump();
      publish();
    },
    residentItems() {
      const out: T[] = [];
      for (const p of current.pages.values()) if (p.status === 'ready') out.push(...p.items);
      return out;
    },
    resident() {
      let pages = 0, items = 0;
      for (const g of pending ? [current, pending] : [current]) for (const p of g.pages.values()) if (p.status === 'ready') { pages++; items += p.items.length; }
      return {pages, items};
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      pending?.quiet?.resolve();
      for (const g of pending ? [current, pending] : [current]) for (const p of g.pages.values()) if (p.status === 'loading') p.controller.abort();
      current.pages.clear(); pending = undefined;
      listeners.clear(); pageListeners.clear();
    },
  };
}

export {abortError as collectionAbortError};
