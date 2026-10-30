/**
 * Platform-neutral artwork scheduling policy (lifted from the web's
 * `ui/artwork-store.ts`, WEB-SYS-01 / APL-SYS-11), so web and Apple load
 * artwork the same way. The platform supplies the loader (web: fetch with a
 * bearer → object URL; Apple: a native prefetch or a URI with headers) and,
 * optionally, how to release a value when it's evicted.
 *
 * Policy:
 * - Cost follows the viewport: a caller withdraws interest by aborting its
 *   signal; queued work nobody waits for is dropped before it starts.
 * - Visual order: waiting work starts lowest `order` first (the caller derives
 *   it from screen position, e.g. row × 1000 + column). `reorder` (or a
 *   re-`load`) moves a key cheaply when the viewport scrolls. Starts are
 *   deferred to a microtask, so everything one render pass asks for is ordered
 *   as a batch before the first request goes out.
 * - Rows fill left to right: with a `group` (the row id), once a group has
 *   started, its remaining queued items go ahead of groups that haven't.
 * - Slot limits: `concurrency` normal requests, plus `priorityExtra` slots
 *   only high-priority work (the hero backdrop) may use; high priority is a
 *   separate lane that always goes first.
 * - A retryable failure is retried after the server's Retry-After or the
 *   backoff (5 s, 15 s, 45 s by default), whichever is longer. A retry wait
 *   never holds a slot (no head-of-line blocking). Callers keep waiting on the
 *   same promise, so a card simply fills in later.
 * - After retries run out the key is remembered as failed for a cooldown.
 * - Settled values are cached, bounded by `maxEntries` (least recently used
 *   released first). PERF-22: callers that know value sizes pass `sizeOf` +
 *   `maxBytes` for a byte budget instead of (or in addition to) the count cap;
 *   large images (backdrops, trickplay sheets) live in a separate small LRU
 *   (`isLargeKey` / `maxLargeEntries`) outside the byte budget; `isPinned`
 *   keeps mounted values out of eviction entirely.
 * - `peek()` counts as use (Map delete + re-set); insertion order is the LRU,
 *   so no sort on insert.
 * - `beginScreen` measures time-to-first-image and time-to-all-visible for a
 *   screen load (`stats().screen`, `onScreenSettled`), for dev-build logging.
 *
 * Costs: load/reorder/start are O(log queued); nothing walks the cache except
 * eviction, which stops at the first entries it can release.
 */
import {MinHeap} from './heap.ts';

export type ArtworkPriority = 'high' | 'normal';

export type ArtworkLoader<V> = (key: string, signal: AbortSignal) => Promise<V>;

/** How to treat a loader failure. `retryAfterSeconds` is the server's Retry-After, when given. */
export type ArtworkFailure = Readonly<{retryable: boolean; retryAfterSeconds?: number}>;

/** One screen load, from `beginScreen` on. Times are milliseconds since `beginScreen`. */
export type ArtworkScreenStats = Readonly<{
  label: string;
  /** Distinct requests since the screen began (cache hits included). */
  requested: number;
  cached: number;
  failed: number;
  /** Requests still waiting. */
  pending: number;
  /** When the first image was available to a caller (a cache hit counts). */
  firstImageMs?: number;
  /** When every request so far had settled (loaded or failed) for the first time. */
  allVisibleMs?: number;
}>;

export type ArtworkSchedulerOptions<V> = Readonly<{
  load: ArtworkLoader<V>;
  /** Classify a failure. Default: aborts are final, everything else is retryable. */
  classify?: (error: unknown) => ArtworkFailure;
  /** Called when a cached value is evicted or the scheduler is disposed (e.g. URL.revokeObjectURL). */
  release?: (value: V) => void;
  concurrency?: number;
  priorityExtra?: number;
  maxEntries?: number;
  /** PERF-22: byte budget for the normal pool (default unbounded). Needs `sizeOf`. */
  maxBytes?: number;
  /** PERF-22: encoded bytes per settled value (default 0). */
  sizeOf?: (value: V) => number;
  /** PERF-22: a pinned value is never evicted (e.g. still mounted on screen). */
  isPinned?: (value: V) => boolean;
  /** PERF-22: keys for the separate large-image LRU (backdrops, trickplay sheets). */
  isLargeKey?: (key: string) => boolean;
  /** PERF-22: settled large images kept (default 4). */
  maxLargeEntries?: number;
  backoffMs?: readonly number[];
  failedCooldownMs?: number;
  /** Per-attempt timeout; a slow thumbnail is better retried than waited on. */
  timeoutMs?: number;
  /** Called once per screen, when all its visible artwork has settled. */
  onScreenSettled?: (stats: ArtworkScreenStats) => void;
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
}>;

export type ArtworkRequestOptions = Readonly<{
  signal?: AbortSignal;
  priority?: ArtworkPriority;
  /** Screen position; lower starts first. Unordered work starts after ordered work, first come first served. */
  order?: number;
  /** A row (or other run) to complete together once started. */
  group?: string;
  /** PERF-22: explicit large-pool membership (overrides `isLargeKey`). */
  large?: boolean;
}>;

export class ArtworkUnavailableError extends Error {
  constructor() { super('Artwork unavailable.'); this.name = 'ArtworkUnavailableError'; }
}

function abortError(message = 'Aborted'): Error { const e = new Error(message); e.name = 'AbortError'; return e; }
const isAbort = (e: unknown) => !!e && typeof e === 'object' && (e as {name?: unknown}).name === 'AbortError';

const UNORDERED = Number.MAX_SAFE_INTEGER;

type Waiter<V> = {resolve: (value: V) => void; reject: (e: unknown) => void};
type Entry<V> = {
  value?: V;
  hasValue: boolean;
  /** Encoded bytes (via `sizeOf`), counted while settled in the normal pool. */
  size: number;
  /** The separate large-image LRU (outside the byte budget). */
  large: boolean;
  waiters: Set<Waiter<V>>;
  attempts: number;
  queued: boolean;
  timer?: unknown;
  inFlight?: AbortController;
  failedAt?: number;
  priority: boolean;
  order: number;
  seq: number;
  group?: string;
  /** Identifies this entry's live heap node; older nodes are stale and skipped. */
  token: number;
};
/** rank 0 = a started group's item; 1 = everything else. */
type Node = {key: string; token: number; rank: number; order: number; seq: number};
type Group = {started: boolean; queued: Set<string>; inFlight: number};
type Screen = {label: string; startedAt: number; requested: number; cached: number; failed: number; pending: Set<string>; firstImageAt?: number; allVisibleAt?: number};

const nodeBefore = (a: Node, b: Node) => a.rank !== b.rank ? a.rank < b.rank : a.order !== b.order ? a.order < b.order : a.seq < b.seq;

export class ArtworkScheduler<V> {
  private entries = new Map<string, Entry<V>>();
  private high = new MinHeap<Node>(nodeBefore);
  private normal = new MinHeap<Node>(nodeBefore);
  private groups = new Map<string, Group>();
  private queuedCount = 0;
  private active = 0;
  private nextToken = 0;
  private nextSeq = 0;
  private flushScheduled = false;
  private disposed = false;
  private screen?: Screen;
  private listeners = new Set<() => void>();
  private loadFn: ArtworkLoader<V>;
  private classify: (error: unknown) => ArtworkFailure;
  private release: (value: V) => void;
  private onScreenSettled?: (stats: ArtworkScreenStats) => void;
  private concurrency: number;
  private priorityExtra: number;
  private maxEntries: number;
  private maxBytes: number;
  private sizeOf: (value: V) => number;
  private isPinned: ((value: V) => boolean) | undefined;
  private isLargeKey: ((key: string) => boolean) | undefined;
  private maxLargeEntries: number;
  /** Encoded bytes currently held by the normal pool. */
  private byteTotal = 0;
  private backoffMs: readonly number[];
  private failedCooldownMs: number;
  private timeoutMs: number;
  private now: () => number;
  private setTimer: (fn: () => void, ms: number) => unknown;
  private clearTimer: (timer: unknown) => void;

  constructor(options: ArtworkSchedulerOptions<V>) {
    this.loadFn = options.load;
    this.classify = options.classify ?? (e => ({retryable: !isAbort(e)}));
    this.release = options.release ?? (() => {});
    this.onScreenSettled = options.onScreenSettled;
    this.concurrency = Math.max(1, options.concurrency ?? 6);
    this.priorityExtra = Math.max(0, options.priorityExtra ?? 2);
    this.maxEntries = Math.max(1, options.maxEntries ?? 600);
    this.maxBytes = Math.max(1, options.maxBytes ?? Number.MAX_SAFE_INTEGER);
    this.sizeOf = options.sizeOf ?? (() => 0);
    this.isPinned = options.isPinned;
    this.isLargeKey = options.isLargeKey;
    this.maxLargeEntries = Math.max(1, options.maxLargeEntries ?? 4);
    this.backoffMs = options.backoffMs ?? [5000, 15000, 45000];
    this.failedCooldownMs = options.failedCooldownMs ?? 60000;
    this.timeoutMs = options.timeoutMs ?? 8000;
    this.now = options.now ?? Date.now;
    this.setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
  }

  /** The cached value, if settled. Counts as use (refreshes recency). */
  peek(key: string): V | undefined {
    const entry = this.entries.get(key);
    if (!entry?.hasValue) return undefined;
    this.touch(key, entry);
    return entry.value;
  }

  /** Called whenever any entry settles, so a mounted image can re-read `peek`. */
  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  /** Diagnostics: counts only, plus the current screen's timings. */
  stats(): {entries: number; queued: number; active: number; screen?: ArtworkScreenStats; bytes: number; large: number} {
    let large = 0;
    for (const e of this.entries.values()) if (e.hasValue && e.large) large++;
    return {entries: this.entries.size, queued: this.queuedCount, active: this.active, screen: this.screenStats(), bytes: this.byteTotal, large};
  }

  /** Start measuring a screen load (call on navigation, before its cards ask for artwork). */
  beginScreen(label = 'screen'): void {
    this.screen = {label, startedAt: this.now(), requested: 0, cached: 0, failed: 0, pending: new Set()};
  }

  load(key: string, options: ArtworkRequestOptions = {}): Promise<V> {
    if (this.disposed) return Promise.reject(abortError('Artwork scheduler disposed'));
    const {signal} = options;
    if (signal?.aborted) return Promise.reject(abortError());
    let entry = this.entries.get(key);
    if (entry?.hasValue) {
      this.touch(key, entry);
      if (this.screen) { this.screen.requested++; this.screen.cached++; this.screen.firstImageAt ??= this.now(); this.scheduleFlush(); }
      return Promise.resolve(entry.value as V);
    }
    if (entry?.failedAt !== undefined && !entry.waiters.size && !entry.queued && !entry.inFlight && entry.timer === undefined) {
      if (this.now() - entry.failedAt < this.failedCooldownMs) return Promise.reject(new ArtworkUnavailableError());
      this.entries.delete(key);
      entry = undefined;
    }
    if (!entry) {
      entry = {hasValue: false, size: 0, large: options.large ?? this.isLargeKey?.(key) ?? false, waiters: new Set(), attempts: 0, queued: false, priority: false, order: UNORDERED, seq: 0, token: 0};
      this.entries.set(key, entry);
    }
    const owned = entry;
    if (this.screen && !this.screen.pending.has(key)) { this.screen.requested++; this.screen.pending.add(key); }
    return new Promise<V>((resolve, reject) => {
      const abort = () => {
        if (!owned.waiters.delete(waiter)) return;
        waiter.reject(abortError());
        this.abandonIfUnwanted(key, owned);
      };
      const waiter: Waiter<V> = {
        resolve: value => {signal?.removeEventListener('abort', abort);resolve(value);},
        reject: error => {signal?.removeEventListener('abort', abort);reject(error);},
      };
      owned.waiters.add(waiter);
      signal?.addEventListener('abort', abort, {once: true});
      const promote = options.priority === 'high' && !owned.priority;
      if (promote) owned.priority = true;
      const moved = this.place(key, owned, options.order, options.group);
      if (!owned.queued && !owned.inFlight && owned.timer === undefined) this.enqueue(key, owned);
      else if (owned.queued && (moved || promote)) { this.pushNode(key, owned); this.scheduleFlush(); }
    });
  }

  /** The viewport moved: give a waiting key a new screen position (and optionally row). O(log queued). */
  reorder(key: string, order: number, group?: string): void {
    const entry = this.entries.get(key);
    if (!entry || this.disposed) return;
    if (this.place(key, entry, order, group) && entry.queued) { this.pushNode(key, entry); this.scheduleFlush(); }
  }

  /** Apply a new order/group; true when either changed. */
  private place(key: string, entry: Entry<V>, order: number | undefined, group: string | undefined): boolean {
    let changed = false;
    if (order !== undefined && Number.isFinite(order) && order !== entry.order) { entry.order = order; changed = true; }
    if (group !== undefined && group !== entry.group) {
      const old = entry.group !== undefined ? this.groups.get(entry.group) : undefined;
      if (old) { old.queued.delete(key); if (entry.inFlight) old.inFlight--; this.dropGroupIfIdle(entry.group!); }
      entry.group = group;
      if (entry.queued || entry.inFlight) {
        const next = this.groupFor(group);
        if (entry.queued) next.queued.add(key);
        if (entry.inFlight) next.inFlight++;
      }
      changed = true;
    }
    return changed;
  }

  private groupFor(name: string): Group {
    let g = this.groups.get(name);
    if (!g) { g = {started: false, queued: new Set(), inFlight: 0}; this.groups.set(name, g); }
    return g;
  }

  private dropGroupIfIdle(name: string) {
    const g = this.groups.get(name);
    if (g && !g.queued.size && !g.inFlight) this.groups.delete(name);
  }

  /** Nobody waits for queued work any more: drop it before it costs a request. */
  private abandonIfUnwanted(key: string, entry: Entry<V>) {
    if (entry.waiters.size || entry.hasValue) return;
    if (entry.timer !== undefined) { this.clearTimer(entry.timer); entry.timer = undefined; }
    if (entry.queued) this.dequeue(key, entry);
    // A request already on the wire is left to finish: its bytes are useful if the card comes back.
    if (!entry.inFlight) this.entries.delete(key);
    if (this.screen?.pending.delete(key)) this.scheduleFlush();
  }

  private enqueue(key: string, entry: Entry<V>) {
    entry.queued = true;
    this.queuedCount++;
    if (!entry.seq) entry.seq = ++this.nextSeq;
    if (entry.group !== undefined) this.groupFor(entry.group).queued.add(key);
    this.pushNode(key, entry);
    this.scheduleFlush();
  }

  private dequeue(key: string, entry: Entry<V>) {
    entry.queued = false;
    entry.token = 0;
    this.queuedCount--;
    if (entry.group !== undefined) { this.groups.get(entry.group)?.queued.delete(key); this.dropGroupIfIdle(entry.group); }
  }

  /** Insert a fresh node for a queued entry; any older node for it becomes stale. */
  private pushNode(key: string, entry: Entry<V>) {
    entry.token = ++this.nextToken;
    const rank = entry.group !== undefined && this.groups.get(entry.group)?.started ? 0 : 1;
    (entry.priority ? this.high : this.normal).push({key, token: entry.token, rank, order: entry.order, seq: entry.seq});
    // Reorders leave stale nodes behind; compact when they outnumber the live ones.
    if (this.high.size + this.normal.size > 2 * this.queuedCount + 256) {
      const live = (n: Node) => { const e = this.entries.get(n.key); return !!e && e.queued && e.token === n.token; };
      this.high.retain(live);
      this.normal.retain(live);
    }
  }

  private headOf(heap: MinHeap<Node>): Node | undefined {
    for (;;) {
      const node = heap.peek();
      if (!node) return undefined;
      const e = this.entries.get(node.key);
      if (e && e.queued && e.token === node.token && (heap === this.high) === e.priority) return node;
      heap.pop();
    }
  }

  private scheduleFlush() {
    if (this.flushScheduled || this.disposed) return;
    this.flushScheduled = true;
    queueMicrotask(() => { this.flushScheduled = false; this.pump(); this.checkScreen(); });
  }

  private pump() {
    while (!this.disposed) {
      const high = this.headOf(this.high);
      if (high && this.active < this.concurrency + this.priorityExtra) { this.high.pop(); this.begin(high.key); continue; }
      const normal = this.headOf(this.normal);
      if (normal && this.active < this.concurrency) { this.normal.pop(); this.begin(normal.key); continue; }
      return;
    }
  }

  private begin(key: string) {
    const entry = this.entries.get(key)!;
    this.dequeue(key, entry);
    this.start(key, entry);
  }

  private start(key: string, entry: Entry<V>) {
    if (this.disposed) return;
    if (!entry.waiters.size) { if (!entry.hasValue) this.entries.delete(key); return; }
    this.active++;
    entry.attempts++;
    const controller = new AbortController();
    entry.inFlight = controller;
    if (entry.group !== undefined) {
      const g = this.groupFor(entry.group);
      g.inFlight++;
      if (!g.started) {
        // The row has begun: its other waiting items now go ahead of rows that haven't.
        g.started = true;
        for (const member of g.queued) { const e = this.entries.get(member); if (e?.queued) this.pushNode(member, e); }
      }
    }
    let timedOut = false;
    const timeout = this.setTimer(() => { timedOut = true; controller.abort(); }, this.timeoutMs);
    let request: Promise<V>;
    try { request = this.loadFn(key, controller.signal); } catch (e) { request = Promise.reject(e); }
    request.then(value => {
      if (this.disposed) { this.release(value); return; }
      // The attempt is over before settling, so a trim below sees completed
      // requests as evictable (back-to-back completions otherwise pin each other).
      this.finishAttempt(entry);
      entry.value = value; entry.hasValue = true; entry.failedAt = undefined;
      entry.size = entry.large ? 0 : Math.max(0, this.sizeOf(value));
      const waiters = [...entry.waiters];
      entry.waiters.clear();
      for (const w of waiters) w.resolve(value);
      // If nobody was waiting any more, the bytes are still worth keeping for a revisit.
      if (!entry.large) this.byteTotal += entry.size;
      this.touch(key, entry);
      if (this.screen?.pending.delete(key)) this.screen.firstImageAt ??= this.now();
      this.trim();
      this.notify();
    }, error => {
      if (this.disposed) return;
      this.finishAttempt(entry);
      // Nobody waits for this picture any more: the caller that wanted it has gone, so the
      // failure says nothing about the key. A loader that can only settle through a mounted
      // view - the native image does - always ends here once its card unmounts mid-load and
      // the attempt times out. A cooldown would then make the next mount reject without
      // asking the server, which is a blank card that a reload cannot explain.
      if (!entry.waiters.size) {
        if (this.entries.get(key) === entry) this.entries.delete(key);
        if (this.screen?.pending.delete(key)) this.scheduleFlush();
        this.notify();
        return;
      }
      const failure: ArtworkFailure = timedOut ? {retryable: true} : this.classify(error);
      if (failure.retryable && entry.attempts <= this.backoffMs.length && entry.waiters.size) {
        const backoff = this.backoffMs[entry.attempts - 1] ?? 0;
        const serverAsked = failure.retryAfterSeconds !== undefined ? failure.retryAfterSeconds * 1000 : 0;
        entry.timer = this.setTimer(() => {
          entry.timer = undefined;
          if (this.disposed) return;
          if (!entry.waiters.size) { this.entries.delete(key); if (this.screen?.pending.delete(key)) this.scheduleFlush(); return; }
          this.enqueue(key, entry);
        }, Math.max(backoff, serverAsked));
        return;
      }
      entry.failedAt = this.now();
      const waiters = [...entry.waiters];
      entry.waiters.clear();
      for (const w of waiters) w.reject(error);
      if (this.screen?.pending.delete(key)) this.screen.failed++;
      this.notify();
    }).finally(() => {
      this.clearTimer(timeout);
      // Normally cleared in the settle handlers above, so a trim there sees
      // the attempt as finished; this covers the disposed path, which returns early.
      this.finishAttempt(entry);
      this.active--;
      this.scheduleFlush();
    });
  }

  /** An attempt finished (settled, failed or disposed): no longer in flight. Idempotent. */
  private finishAttempt(entry: Entry<V>) {
    if (entry.inFlight === undefined) return;
    entry.inFlight = undefined;
    if (entry.group !== undefined) {
      const g = this.groups.get(entry.group);
      if (g) g.inFlight--;
      this.dropGroupIfIdle(entry.group);
    }
  }

  private notify() { for (const l of [...this.listeners]) l(); }

  /** Most recently used last: Map iteration order is the LRU order. */
  private touch(key: string, entry: Entry<V>) {
    this.entries.delete(key);
    this.entries.set(key, entry);
  }

  private trim() {
    let normal = 0;
    let large = 0;
    for (const e of this.entries.values()) if (e.hasValue) { if (e.large) large++; else normal++; }
    if (large <= this.maxLargeEntries && this.byteTotal <= this.maxBytes && normal <= this.maxEntries) return;
    for (const [key, e] of this.entries) {
      if (large <= this.maxLargeEntries && this.byteTotal <= this.maxBytes && normal <= this.maxEntries) break;
      if (e.waiters.size || e.queued || e.inFlight || e.timer !== undefined) continue;
      if (!e.hasValue) {
        if (e.failedAt === undefined) continue;
        this.entries.delete(key);
        continue;
      }
      // A mounted URL is never revoked; it becomes evictable when its count drops to 0.
      if (this.isPinned?.(e.value as V)) continue;
      if (e.large) {
        if (large <= this.maxLargeEntries) continue;
        large--;
      } else {
        if (this.byteTotal <= this.maxBytes && normal <= this.maxEntries) continue;
        normal--;
        this.byteTotal = Math.max(0, this.byteTotal - e.size);
      }
      this.release(e.value as V);
      this.entries.delete(key);
    }
  }

  private screenStats(): ArtworkScreenStats | undefined {
    const s = this.screen;
    if (!s) return undefined;
    return {
      label: s.label, requested: s.requested, cached: s.cached, failed: s.failed, pending: s.pending.size,
      firstImageMs: s.firstImageAt !== undefined ? s.firstImageAt - s.startedAt : undefined,
      allVisibleMs: s.allVisibleAt !== undefined ? s.allVisibleAt - s.startedAt : undefined,
    };
  }

  private checkScreen() {
    const s = this.screen;
    if (!s || s.allVisibleAt !== undefined || !s.requested || s.pending.size) return;
    s.allVisibleAt = this.now();
    this.onScreenSettled?.(this.screenStats()!);
  }

  /** Ends the scheduler for good. Safe to call more than once. */
  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    const waiting: Waiter<V>[] = [];
    for (const e of this.entries.values()) {
      if (e.timer !== undefined) this.clearTimer(e.timer);
      e.inFlight?.abort();
      if (e.hasValue) this.release(e.value as V);
      waiting.push(...e.waiters);
      e.waiters.clear();
    }
    this.high.clear();
    this.normal.clear();
    this.groups.clear();
    this.queuedCount = 0;
    this.byteTotal = 0;
    this.entries.clear();
    this.listeners.clear();
    // Deferred so teardown doesn't re-enter a caller's rejection handler while dispose is on the stack.
    if (waiting.length) queueMicrotask(() => { for (const w of waiting) w.reject(abortError('Artwork scheduler disposed')); });
  }
}
