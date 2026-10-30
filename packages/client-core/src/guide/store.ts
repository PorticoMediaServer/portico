/**
 * The windowed guide (Spec — Channels and Guide §8.2, §9.4): O(visible) in both axes.
 *
 * The guide is tiles of channel pages (50 channels) × time blocks (3 h, UTC-aligned). The screen
 * reports its viewport (rows and span); the store keeps that plus one page and one block of margin,
 * fetches what is missing with at most `maxInFlight` requests (the visible part first), aborts
 * requests that left the interest, and evicts the least recently used pages and tiles beyond the
 * caps. A 1,000-channel, 14-day guide costs what is on screen.
 *
 * `invalidate()` (a `live.guide` event) refetches the tiles in view while the old ones stay on
 * screen; `reset()` (a filter or sort change, a new channel set) starts over.
 */
import {BLOCK_MS, blocksCovering, floorTo} from './time.ts';
import {normalizePrograms} from './layout.ts';
import {GUIDE_RATE_LIMIT_ATTEMPTS, isRateLimited, rateLimitDelay} from './rate-limit.ts';
import type {GuideChannelRow, GuideDataSource, GuideProgram} from './types.ts';

export type GuideViewport = Readonly<{firstRow: number; lastRow: number; start: number; end: number}>;

export type GuideStoreOptions = Readonly<{
  source: GuideDataSource;
  channelPageSize?: number;
  blockMs?: number;
  maxChannelPages?: number;
  maxTiles?: number;
  maxInFlight?: number;
  marginPages?: number;
  marginBlocks?: number;
  retryMs?: readonly number[];
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
}>;

export type GuideStoreSnapshot = Readonly<{version: number; total?: number; channelsError: boolean}>;

export type RowPrograms = Readonly<{
  programs: readonly GuideProgram[];
  /** Every block of the requested span is loaded (possibly from before an invalidation). */
  complete: boolean;
  /** A block of the span failed (show the "Couldn't load this part" band). */
  failed: boolean;
}>;

type Page = {status: 'loading' | 'ready' | 'error'; items: readonly GuideChannelRow[]; controller?: AbortController; used: number; attempts: number; timer?: unknown};
type Tile = {status: 'loading' | 'ready' | 'error'; stale: boolean; data?: Readonly<Record<string, readonly GuideProgram[]>>; controller?: AbortController; used: number; attempts: number; timer?: unknown; generation: number};
type Task = {kind: 'page'; page: number} | {kind: 'tile'; page: number; block: number};

export class GuideWindowStore {
  private o: Required<Omit<GuideStoreOptions, 'setTimer' | 'clearTimer'>>;
  private setTimer: (fn: () => void, ms: number) => unknown;
  private clearTimer: (t: unknown) => void;
  private pages = new Map<number, Page>();
  private tiles = new Map<string, Tile>();
  private total?: number;
  private channelsError = false;
  private viewport?: GuideViewport;
  private interestPages = new Set<number>();
  private interestTiles = new Set<string>();
  private queue: Task[] = [];
  private inFlightCount = 0;
  private clock = 0;
  private generation = 0;
  private version = 0;
  private snapshot: GuideStoreSnapshot = Object.freeze({version: 0, channelsError: false});
  private listeners = new Set<() => void>();
  private pageListeners = new Map<number, Set<() => void>>();
  private disposed = false;
  /** Fast 429 retries per channel page (attempts survive the placeholder delete). */
  private rateLimitPages = new Map<number, number>();

  constructor(options: GuideStoreOptions) {
    this.o = {
      source: options.source, channelPageSize: options.channelPageSize ?? 50, blockMs: options.blockMs ?? BLOCK_MS,
      maxChannelPages: options.maxChannelPages ?? 6, maxTiles: options.maxTiles ?? 24, maxInFlight: options.maxInFlight ?? 4,
      marginPages: options.marginPages ?? 1, marginBlocks: options.marginBlocks ?? 1, retryMs: options.retryMs ?? [5_000, 15_000, 45_000],
    };
    this.setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
  }

  // ── Reading ─────────────────────────────────────────────────────
  getSnapshot = (): GuideStoreSnapshot => this.snapshot;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };
  /** Rows re-render only when their own channel page or its tiles change. */
  subscribePage(page: number, fn: () => void): () => void {
    let set = this.pageListeners.get(page);
    if (!set) this.pageListeners.set(page, set = new Set());
    set.add(fn);
    return () => { set!.delete(fn); };
  }
  pageOf = (row: number) => Math.floor(row / this.o.channelPageSize);

  channelAt(row: number): GuideChannelRow | undefined {
    const page = this.pages.get(this.pageOf(row));
    if (!page || page.status !== 'ready') return undefined;
    page.used = ++this.clock;
    return page.items[row - this.pageOf(row) * this.o.channelPageSize];
  }

  /** Normalized programs of a row overlapping [start, end), merged from resident tiles and de-duplicated. */
  programsFor(row: number, start: number, end: number): RowPrograms {
    const channel = this.channelAt(row);
    if (!channel) return {programs: [], complete: false, failed: false};
    const page = this.pageOf(row);
    const merged: GuideProgram[] = [];
    let complete = true, failed = false;
    for (const block of blocksCovering(start, end, this.o.blockMs)) {
      const tile = this.tiles.get(`${page}:${block}`);
      if (!tile?.data) { complete = false; if (tile?.status === 'error') failed = true; continue; }
      tile.used = ++this.clock;
      for (const p of tile.data[channel.id] ?? []) if (p.end > start && p.start < end) merged.push(p);
    }
    return {programs: normalizePrograms(merged), complete, failed};
  }

  /** For tests and diagnostics. */
  resident(): Readonly<{channelPages: number; tiles: number; programs: number; inFlight: number; queued: number}> {
    let programs = 0;
    for (const t of this.tiles.values()) if (t.data) for (const list of Object.values(t.data)) programs += list.length;
    return {channelPages: this.pages.size, tiles: this.tiles.size, programs, inFlight: this.inFlightCount, queued: this.queue.length};
  }

  // ── Viewport ────────────────────────────────────────────────────
  setViewport(v: GuideViewport): void {
    if (this.disposed) return;
    this.viewport = v;
    this.plan();
  }

  private plan() {
    if (this.disposed) return;
    const v = this.viewport;
    if (!v) return;
    const ps = this.o.channelPageSize;
    const lastPage = this.total === undefined ? Infinity : Math.max(0, Math.ceil(this.total / ps) - 1);
    const visiblePages: number[] = [];
    for (let p = this.pageOf(Math.max(0, v.firstRow)); p <= Math.min(this.pageOf(Math.max(v.firstRow, v.lastRow)), lastPage); p++) visiblePages.push(p);
    const pages = new Set<number>();
    for (let p = (visiblePages[0] ?? 0) - this.o.marginPages; p <= (visiblePages.at(-1) ?? 0) + this.o.marginPages; p++) if (p >= 0 && p <= lastPage) pages.add(p);
    const visibleBlocks = blocksCovering(v.start, v.end, this.o.blockMs);
    const blocks = blocksCovering(v.start - this.o.marginBlocks * this.o.blockMs, v.end + this.o.marginBlocks * this.o.blockMs, this.o.blockMs);
    this.interestPages = pages;
    this.interestTiles = new Set([...pages].flatMap(p => blocks.map(b => `${p}:${b}`)));
    // Abort and drop work that left the interest.
    this.queue = this.queue.filter(t => (t.kind === 'page' ? pages.has(t.page) : this.interestTiles.has(`${t.page}:${t.block}`)));
    for (const [p, page] of this.pages) if (!pages.has(p) && page.status === 'loading') { page.controller?.abort(); this.pages.delete(p); }
    for (const [k, tile] of this.tiles) if (!this.interestTiles.has(k) && tile.status === 'loading') { tile.controller?.abort(); this.tiles.delete(k); }
    // Enqueue what is missing: visible first, then margin.
    const rank = (page: number, block?: number) => (visiblePages.includes(page) && (block === undefined || visibleBlocks.includes(block)) ? 0 : 1);
    const wanted: (Task & {rank: number})[] = [];
    for (const p of pages) {
      const page = this.pages.get(p);
      if (!page) { if (!this.queued({kind: 'page', page: p})) wanted.push({kind: 'page', page: p, rank: rank(p) - 0.5}); continue; }
      page.used = ++this.clock;
      if (page.status !== 'ready') continue;
      for (const b of blocks) {
        const tile = this.tiles.get(`${p}:${b}`);
        if (tile) { tile.used = ++this.clock; if (!tile.stale || tile.status === 'loading') continue; }
        if (!this.queued({kind: 'tile', page: p, block: b})) wanted.push({kind: 'tile', page: p, block: b, rank: rank(p, b)});
      }
    }
    wanted.sort((a, b) => a.rank - b.rank);
    this.queue.unshift(...wanted.map(({rank: _r, ...t}) => t as Task));
    this.queue.sort((a, b) => this.priority(a, visiblePages, visibleBlocks) - this.priority(b, visiblePages, visibleBlocks));
    this.evict();
    this.pump();
  }

  private priority(t: Task, visiblePages: readonly number[], visibleBlocks: readonly number[]): number {
    const pageVisible = visiblePages.includes(t.page);
    if (t.kind === 'page') return pageVisible ? 0 : 2;
    return pageVisible && visibleBlocks.includes(t.block) ? 1 : 3;
  }

  private queued(t: Task): boolean {
    return this.queue.some(q => q.kind === t.kind && q.page === t.page && (t.kind === 'page' || (q as {block: number}).block === t.block));
  }

  private evict() {
    const lru = <T extends {used: number; status: string}>(map: Map<string | number, T>, keep: Set<string | number>, cap: number, abort: (v: T) => void) => {
      if (map.size <= cap) return;
      const candidates = [...map.entries()].filter(([k]) => !keep.has(k)).sort((a, b) => a[1].used - b[1].used);
      for (const [k, v] of candidates) { if (map.size <= cap) break; abort(v); map.delete(k); }
    };
    lru(this.pages as Map<string | number, Page>, this.interestPages as Set<string | number>, this.o.maxChannelPages, p => { p.controller?.abort(); if (p.timer !== undefined) this.clearTimer(p.timer); });
    // Tiles of evicted pages are meaningless (their channel ids may be gone).
    for (const [k, t] of this.tiles) if (!this.pages.has(Number(k.split(':')[0]))) { t.controller?.abort(); if (t.timer !== undefined) this.clearTimer(t.timer); this.tiles.delete(k); }
    lru(this.tiles as Map<string | number, Tile>, this.interestTiles as Set<string | number>, this.o.maxTiles, t => { t.controller?.abort(); if (t.timer !== undefined) this.clearTimer(t.timer); });
  }

  // ── Fetching ────────────────────────────────────────────────────
  private pump() {
    while (!this.disposed && this.inFlightCount < this.o.maxInFlight && this.queue.length) {
      const task = this.queue.shift()!;
      if (task.kind === 'page') this.fetchPage(task.page);
      else this.fetchTile(task.page, task.block);
    }
  }

  private fetchPage(p: number) {
    const existing = this.pages.get(p);
    if (existing?.status === 'loading' || existing?.status === 'ready') return;
    const controller = new AbortController();
    const page: Page = {status: 'loading', items: [], controller, used: ++this.clock, attempts: existing?.attempts ?? 0};
    this.pages.set(p, page);
    this.evict();
    this.inFlightCount++;
    const generation = this.generation;
    this.o.source.channels(p * this.o.channelPageSize, this.o.channelPageSize, controller.signal).then(answer => {
      if (controller.signal.aborted || this.pages.get(p) !== page) return;
      page.status = 'ready'; page.items = answer.items; page.controller = undefined; page.attempts = 0;
      this.rateLimitPages.delete(p);
      this.total = answer.total;
      this.channelsError = false;
      this.changed(p);
      this.plan(); // tiles for this page can go now
    }, (error) => {
      if (controller.signal.aborted || this.pages.get(p) !== page) return;
      if ((error as {code?:string}|null)?.code==='guide_refresh_required') { this.reset(); return; }
      // M25-2: a 429 retries fast (honouring Retry-After) without marking the
      // page failed; only after a few attempts does it surface as an error.
      const seen = this.rateLimitPages.get(p) ?? 0;
      if (isRateLimited(error) && seen < GUIDE_RATE_LIMIT_ATTEMPTS - 1 && generation === this.generation) {
        this.rateLimitPages.set(p, seen + 1);
        page.controller = undefined;
        page.timer = this.setTimer(() => {
          page.timer = undefined;
          if (this.disposed || this.pages.get(p) !== page) return;
          this.pages.delete(p);
          this.plan();
        }, rateLimitDelay(error, seen + 1));
        return;
      }
      this.rateLimitPages.delete(p);
      page.status = 'error'; page.controller = undefined; page.attempts++;
      this.channelsError = true;
      this.changed(p);
      this.retryLater(page, () => { if (this.pages.get(p) === page) { this.pages.delete(p); this.plan(); } });
    }).finally(() => { this.inFlightCount--; this.pump(); });
  }

  private fetchTile(p: number, block: number) {
    const page = this.pages.get(p);
    const key = `${p}:${block}`;
    const existing = this.tiles.get(key);
    if (!page || page.status !== 'ready' || (existing && (existing.status === 'loading' || (existing.status === 'ready' && !existing.stale)))) return;
    const controller = new AbortController();
    const tile: Tile = {status: 'loading', stale: existing?.stale ?? false, data: existing?.data, controller, used: ++this.clock, attempts: existing?.attempts ?? 0, generation: this.generation};
    this.tiles.set(key, tile);
    this.evict();
    this.inFlightCount++;
    const ids = page.items.map(c => c.id);
    this.o.source.programs(ids, block, block + this.o.blockMs, controller.signal).then(data => {
      if (controller.signal.aborted || this.tiles.get(key) !== tile) return;
      tile.status = 'ready'; tile.stale = tile.generation !== this.generation; tile.data = data; tile.controller = undefined; tile.attempts = 0;
      this.changed(p);
    }, (error) => {
      if (controller.signal.aborted || this.tiles.get(key) !== tile) return;
      // M25-2: a 429 retries fast (honouring Retry-After) while the tile stays
      // loading (old programs stay on screen); only after a few attempts does
      // it surface as the failed band.
      if (isRateLimited(error) && tile.attempts < GUIDE_RATE_LIMIT_ATTEMPTS - 1) {
        tile.controller = undefined;
        tile.attempts++;
        tile.timer = this.setTimer(() => {
          tile.timer = undefined;
          if (this.disposed || this.tiles.get(key) !== tile) return;
          tile.status = 'ready';
          tile.stale = true;
          this.plan();
        }, rateLimitDelay(error, tile.attempts));
        return;
      }
      tile.status = 'error'; tile.controller = undefined; tile.attempts++;
      this.changed(p);
      this.retryLater(tile, () => { if (this.tiles.get(key) === tile) { tile.stale = true; this.plan(); } });
    }).finally(() => { this.inFlightCount--; this.pump(); });
  }

  private retryLater(entry: {attempts: number; timer?: unknown}, run: () => void) {
    const delays = this.o.retryMs;
    if (entry.attempts > delays.length) return; // stays failed until `retry()`
    entry.timer = this.setTimer(() => { entry.timer = undefined; if (!this.disposed) run(); }, delays[Math.min(entry.attempts - 1, delays.length - 1)]!);
  }

  /** Try failed pages and tiles in view again now. */
  retry(): void {
    for (const [p, page] of this.pages) if (page.status === 'error') { if (page.timer !== undefined) this.clearTimer(page.timer); this.pages.delete(p); }
    for (const t of this.tiles.values()) if (t.status === 'error') { if (t.timer !== undefined) this.clearTimer(t.timer); t.attempts = 0; t.stale = true; }
    this.plan();
  }

  /** A new guide generation (`live.guide` event): refetch programs in view, keeping old ones on screen meanwhile. */
  invalidate(): void {
    this.generation++;
    for (const t of this.tiles.values()) { t.stale = true; if (t.status === 'loading') { t.controller?.abort(); t.status = t.data ? 'ready' : 'error'; } }
    for (const [k, t] of this.tiles) if (!t.data) this.tiles.delete(k);
    this.plan();
  }

  /** A different channel set (filter, sort, source change): start over. */
  reset(source?: GuideDataSource): void {
    for (const p of this.pages.values()) { p.controller?.abort(); if (p.timer !== undefined) this.clearTimer(p.timer); }
    for (const t of this.tiles.values()) { t.controller?.abort(); if (t.timer !== undefined) this.clearTimer(t.timer); }
    this.pages.clear(); this.tiles.clear(); this.queue = [];
    this.rateLimitPages.clear();
    this.total = undefined; this.channelsError = false;
    this.generation++;
    if (source) this.o = {...this.o, source};
    this.o.source.reset?.();
    this.changed();
    this.plan();
  }

  dispose(): void {
    this.disposed = true;
    this.listeners.clear();
    this.pageListeners.clear();
    this.reset();
  }

  private changed(page?: number) {
    this.version++;
    this.snapshot = Object.freeze({version: this.version, ...(this.total !== undefined ? {total: this.total} : {}), channelsError: this.channelsError});
    if (page !== undefined) for (const l of [...(this.pageListeners.get(page) ?? [])]) l();
    else for (const set of this.pageListeners.values()) for (const l of [...set]) l();
    for (const l of [...this.listeners]) l();
  }
}

/** The block containing an instant (for callers that key things by tile). */
export const blockOf = (ms: number, blockMs = BLOCK_MS) => floorTo(ms, blockMs);
