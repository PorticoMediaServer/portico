/** A bounded viewport over one original cursor query. Cursors are opaque and
 * enter this class only as the producer's offered next cursor or a bookmark. */
export type DashboardCursorPage<T, M> = Readonly<{items: readonly T[]; nextCursor?: string; metadata: M}>;
export type DashboardCursorChunk<T> = Readonly<{cursor: string; items: readonly T[]}>;
export type DashboardCursorSnapshot<T, M> = Readonly<{
  scope: object; generation: number; chunks: readonly DashboardCursorChunk<T>[]; items: readonly T[];
  bookmarks: readonly string[]; nextCursor: string; metadata?: M;
  pending: boolean; error?: unknown; refreshRequired: boolean;
}>;
type Flight<T, M> = {generation: number; expected: DashboardCursorSnapshot<T, M>; cursor: string; abort: AbortController; promise: Promise<void>; reset: boolean; admission?: () => boolean};

export class DashboardCursorWindow<T, M> {
  private generation = 0;
  private disposed = false;
  private flight?: Flight<T, M>;
  private focused?: {id: string; token: object};
  private owned = new Map<object, string>();
  private held?: {flight: Flight<T, M>; commit: () => void};
  private seen = new Set<string>();
  private checkpoint?: string;
  private power = 1;
  private distance = 0;
  private agedOut = false;
  private issued = new WeakSet<object>();
  private value: DashboardCursorSnapshot<T, M> = {scope: {}, generation: 0, chunks: [], items: [], bookmarks: [''], nextCursor: '', pending: false, refreshRequired: false};
  constructor(private readonly read: (cursor: string, signal: AbortSignal) => Promise<DashboardCursorPage<T, M>>,
    private readonly key: (item: T) => string, private readonly current: () => boolean,
    private readonly changed: (value: DashboardCursorSnapshot<T, M>) => void,
    private readonly sameRow?: (first: T, second: T) => boolean) {}
  snapshot = (): DashboardCursorSnapshot<T, M> => this.value;
  refreshing = (): boolean => !!this.flight?.reset && this.alive(this.flight);
  /** Adopt an already-read producer page as the first page of a fresh query. */
  seed(cursor: string, page: DashboardCursorPage<T, M>): void {
    if (this.disposed || !this.current() || this.disposed || this.flight || this.value.chunks.length) return;
    if (cursor) this.seen.add(cursor);
    this.restartCycle(page.nextCursor);
    this.publish({...this.value, chunks: [{cursor, items: page.items}], items: page.items, bookmarks: cursor ? ['', cursor] : [''], nextCursor: page.nextCursor ?? '', metadata: page.metadata, pending: false, refreshRequired: !!cursor && page.nextCursor === cursor});
  }
  private publish(value: DashboardCursorSnapshot<T, M>): void {this.value = Object.freeze(value); this.issued.add(this.value); this.changed(this.value);}
  isWitness(expected: DashboardCursorSnapshot<T, M>): boolean {return this.issued.has(expected) && expected.scope === this.value.scope && expected.generation === this.generation;}
  private alive(flight: Flight<T, M>): boolean {
    const own = () => !this.disposed && this.flight === flight && flight.generation === this.generation && !flight.abort.signal.aborted;
    return own() && this.current() && own() && (!flight.admission || flight.admission()) && own();
  }
  /** Called by the actual focus binding, not by a guessed row index. */
  focus(id?: string): object | undefined {const token = id === undefined ? undefined : {}; this.focused = id === undefined ? undefined : {id, token: token!}; if (this.held && this.alive(this.held.flight)) this.held.commit(); return token;}
  focusToken(id: string): object | undefined {return this.focused?.id === id ? this.focused.token : undefined;}
  blur(id: string, token: object): void {if (this.focused?.id === id && this.focused.token === token) this.focus(undefined);}
  pin(id: string): object {const token = {}; this.owned.set(token, id); return token;}
  unpin(token: object): void {if (!this.owned.delete(token)) return; if (this.held && this.alive(this.held.flight)) this.held.commit();}
  refresh(): Promise<void> {
    if (this.disposed || !this.current() || this.disposed || this.value.refreshRequired) return Promise.resolve();
    const active = this.flight; if (active?.reset && this.alive(active)) return active.promise;
    const predecessor = this.flight, old = this.value, start = old.chunks[0]?.cursor ?? '';
    this.flight = undefined; this.held = undefined; this.seen.clear(); this.agedOut = false; this.restartCycle();
    const generation = ++this.generation; this.issued = new WeakSet();
    predecessor?.abort.abort();
    if (this.disposed || generation !== this.generation || this.flight || !this.current() || generation !== this.generation || this.flight) return this.pendingFlight();
    const publication = {...old, generation, bookmarks: [''], nextCursor: '', pending: false, error: undefined, refreshRequired: false};
    this.value = Object.freeze(publication); this.issued.add(this.value);
    return this.request(this.value, start, false, true, true);
  }
  /** Explicit user recovery starts the same query at its genuine first page. */
  restart(expected: DashboardCursorSnapshot<T, M>): Promise<void> {
    if (this.disposed || !this.current() || this.disposed || this.value !== expected || !expected.refreshRequired || this.flight) return Promise.resolve();
    this.generation++; this.issued = new WeakSet(); this.seen.clear(); this.agedOut = false; this.restartCycle();
    const publication = {...expected, generation: this.generation, bookmarks: [''], nextCursor: '', pending: false, error: undefined, refreshRequired: false};
    this.value = Object.freeze(publication); this.issued.add(this.value);
    return this.request(this.value, '', false, true, false);
  }
  next(expected: DashboardCursorSnapshot<T, M>, admission?: () => boolean): Promise<void> {
    if (admission && !admission()) return Promise.resolve();
    const active = this.coalesced(expected); if (active) return active;
    if (expected !== this.value || !expected.nextCursor || expected.refreshRequired) return Promise.resolve();
    return this.request(expected, expected.nextCursor, false, false, false, admission);
  }
  previous(expected: DashboardCursorSnapshot<T, M>, admission?: () => boolean): Promise<void> {
    if (admission && !admission()) return Promise.resolve();
    const active = this.coalesced(expected); if (active) return active;
    if (expected !== this.value || expected.refreshRequired || !expected.chunks.length) return Promise.resolve();
    const first = expected.chunks[0].cursor, index = expected.bookmarks.indexOf(first);
    if (!first) return Promise.resolve();
    if (index < 0 || index <= 1 && this.agedOut) return this.request(expected, '', false, true, false, admission);
    const cursor = index > 0 ? expected.bookmarks[index - 1] : '';
    return this.request(expected, cursor, true, false, false, admission);
  }
  retry(expected: DashboardCursorSnapshot<T, M>, admission?: () => boolean): Promise<void> {
    if (admission && !admission()) return Promise.resolve();
    const active = this.coalesced(expected); if (active) return active;
    if (expected !== this.value || expected.error === undefined || expected.refreshRequired) return Promise.resolve();
    const cursor = this.retryCursor, previous = this.retryPrevious;
    return this.request(expected, cursor, previous, this.retryReset, this.retryRehydrate, admission);
  }
  /** Brent's constant-memory checkpoint covers a forward producer chain. */
  private restartCycle(cursor?: string): void {this.checkpoint = cursor || undefined; this.power = 1; this.distance = 0;}
  private cycle(cursor: string): boolean {
    if (!cursor) return false;
    if (this.checkpoint === undefined) {this.checkpoint = cursor; return false;}
    if (!Number.isSafeInteger(this.distance + 1)) return true;
    this.distance++;
    if (cursor === this.checkpoint) return true;
    if (this.distance === this.power) {
      if (this.power > Math.floor(Number.MAX_SAFE_INTEGER / 2)) return true;
      this.checkpoint = cursor; this.power *= 2; this.distance = 0;
    }
    return false;
  }
  private closedCursor(error: unknown, cursor: string): boolean {
    if (error instanceof Error && error.message === 'dashboard_refresh_required') return true;
    const code = error && typeof error === 'object' && 'code' in error ? (error as {code?: unknown}).code : undefined;
    return code === 'invalid_cursor' || code === 'stale_continuation' || code === 'revision_mismatch' || !!cursor && (code === 'invalid_request' || code === 'invalid_console_request');
  }
  private coalesced(expected: DashboardCursorSnapshot<T, M>): Promise<void> | undefined {
    const flight = this.flight;
    if (!flight || !(expected === flight.expected || expected === this.value && expected.pending && expected.generation === flight.generation)) return undefined;
    if (!this.alive(flight)) {this.retireRefused(flight); return undefined;}
    return flight.promise;
  }
  private retireRefused(flight: Flight<T, M>): void {
    if (this.flight !== flight || flight.generation !== this.generation) return;
    this.flight = undefined; this.held = undefined;
    this.value = Object.freeze({...this.value, pending: false}); this.issued.add(this.value);
    flight.abort.abort();
  }
  private pendingFlight(): Promise<void> {const active = this.flight; return active ? active.promise : Promise.resolve();}
  private retryReset = false;
  private retryRehydrate = false;
  private retryCursor = '';
  private retryPrevious = false;
  private request(expected: DashboardCursorSnapshot<T, M>, cursor: string, previous: boolean, reset: boolean, rehydrate = false, admission?: () => boolean): Promise<void> {
    if (this.disposed || !this.current() || admission && !admission() || this.disposed || expected !== this.value || this.flight) return Promise.resolve();
    const abort = new AbortController();
    let settle!: () => void; const promise = new Promise<void>(resolve => {settle = resolve;});
    const flight: Flight<T, M> = {generation: this.generation, expected, cursor, abort, promise, reset, admission};
    this.flight = flight; this.retryCursor = cursor; this.retryPrevious = previous; this.retryReset = reset; this.retryRehydrate = rehydrate;
    this.publish({...expected, pending: true, error: undefined});
    if (!this.alive(flight)) {this.retireRefused(flight); settle(); return promise;}
    let requestedCursor = cursor;
    void (async () => {
      try {
        if (!this.alive(flight)) return;
        if (reset && rehydrate && expected.chunks.length) {
          const pages: {cursor: string; page: DashboardCursorPage<T, M>}[] = [], used = new Set<string>();
          let next = cursor;
          for (let i = 0; i < Math.min(3, expected.chunks.length); i++) {
            if (!this.alive(flight) || used.has(next)) {if (this.alive(flight)) {this.flight = undefined; this.publish({...this.value, pending: false, refreshRequired: true});} return;}
            used.add(next); requestedCursor = next; const page = await this.read(next, abort.signal); if (!this.alive(flight)) return;
            pages.push({cursor: next, page}); if (!page.nextCursor) break; next = page.nextCursor;
          }
          if (pages.at(-1)?.page.nextCursor && used.has(pages.at(-1)!.page.nextCursor!)) {this.flight = undefined; this.publish({...this.value, pending: false, refreshRequired: true}); return;}
          this.acceptRefresh(flight, pages); return;
        }
        const page = await this.read(cursor, abort.signal);
        if (!this.alive(flight)) return;
        const expectedSuccessor = previous ? this.value.chunks[0]?.cursor : undefined;
        const retained = new Map(this.value.items.map(row => [this.key(row), row]));
        const conflict = this.sameRow && page.items.some(row => {const prior = retained.get(this.key(row)); return prior !== undefined && !this.sameRow!(prior, row);});
        if (!this.alive(flight)) return;
        if (conflict || previous && expectedSuccessor && page.nextCursor !== expectedSuccessor || !reset && !previous && page.nextCursor && (page.nextCursor === cursor || this.seen.has(page.nextCursor) || this.cycle(page.nextCursor))) {
          this.flight = undefined; this.publish({...this.value, pending: false, refreshRequired: true}); return;
        }
        this.accept(flight, page, previous, reset);
      } catch (error) {
        if (this.alive(flight)) {const closed = this.closedCursor(error, requestedCursor); if (!this.alive(flight)) return; this.flight = undefined; if (closed) this.publish({...this.value, pending: false, refreshRequired: true}); else this.publish({...this.value, pending: false, error: error ?? new Error('dashboard_page_failed')});}
      }
    })().finally(() => {if (this.flight === flight && !this.alive(flight)) this.retireRefused(flight); settle();});
    return promise;
  }
  private accept(flight: Flight<T, M>, page: DashboardCursorPage<T, M>, previous: boolean, reset: boolean): void {
    if (!this.alive(flight)) return;
    const old = this.value, candidate = {cursor: flight.cursor, items: page.items};
    const pinned = new Set(this.owned.values()); if (this.focused) pinned.add(this.focused.id);
    if (reset && old.items.some(row => pinned.has(this.key(row)) && !page.items.some(next => this.key(next) === this.key(row)))) {if (!this.alive(flight)) return; this.held = {flight, commit: () => this.accept(flight, page, previous, reset)}; return;}
    const chunks = reset ? [candidate] : previous ? [candidate, ...old.chunks.filter(chunk => chunk.cursor !== flight.cursor)] : [...old.chunks.filter(chunk => chunk.cursor !== flight.cursor), candidate];
    const evicted = chunks.length > 3 ? previous ? chunks[chunks.length - 1] : chunks[0] : undefined;
    if (evicted && evicted.items.some(row => pinned.has(this.key(row)))) {if (!this.alive(flight)) return; this.held = {flight, commit: () => this.accept(flight, page, previous, reset)}; return;}
    if (!this.alive(flight)) return;
    if (evicted) previous ? chunks.pop() : chunks.shift();
    const ids = new Set<string>(), items: T[] = [];
    for (const chunk of chunks) for (const row of chunk.items) {const id = this.key(row); if (!ids.has(id)) {ids.add(id); items.push(row);}}
    if (!this.alive(flight)) return;
    if (reset) {this.seen.clear(); this.agedOut = false; this.restartCycle(page.nextCursor);}
    let bookmarks = reset ? [''] : [...old.bookmarks];
    if (!previous && flight.cursor && !bookmarks.includes(flight.cursor)) bookmarks.push(flight.cursor);
    if (bookmarks.length > 64) {this.agedOut = true; bookmarks = ['', ...bookmarks.slice(-63)];}
    if (previous) {this.seen = new Set(chunks.map(chunk => chunk.cursor).filter(Boolean)); this.restartCycle(evicted?.cursor ?? old.nextCursor);}
    else if (flight.cursor) {this.seen.add(flight.cursor); if (this.seen.size > 63) this.seen.delete(this.seen.values().next().value!);}
    this.held = undefined; this.flight = undefined;
    this.publish({...old, chunks, items, bookmarks, nextCursor: previous ? evicted?.cursor ?? old.nextCursor : page.nextCursor ?? '', metadata: page.metadata, pending: false, error: undefined, refreshRequired: false});
  }
  private acceptRefresh(flight: Flight<T, M>, pages: readonly {cursor: string; page: DashboardCursorPage<T, M>}[]): void {
    if (!this.alive(flight) || !pages.length) return;
    const chunks = pages.map(({cursor, page}) => ({cursor, items: page.items})), ids = new Map<string, T>(), items: T[] = [];
    for (const chunk of chunks) for (const row of chunk.items) {
      const key = this.key(row), prior = ids.get(key);
      if (prior !== undefined && this.sameRow && !this.sameRow(prior, row)) {if (!this.alive(flight)) return; this.flight = undefined; this.publish({...this.value, pending: false, refreshRequired: true}); return;}
      if (prior === undefined) {ids.set(key, row); items.push(row);}
    }
    const pinned = new Set(this.owned.values()); if (this.focused) pinned.add(this.focused.id);
    if (this.value.items.some(row => pinned.has(this.key(row)) && !ids.has(this.key(row)))) {if (!this.alive(flight)) return; this.held = {flight, commit: () => this.acceptRefresh(flight, pages)}; return;}
    if (!this.alive(flight)) return;
    const last = pages[pages.length - 1].page, bookmarks = ['', ...pages.map(page => page.cursor).filter(Boolean)];
    this.seen = new Set(bookmarks.filter(Boolean)); this.agedOut = pages[0].cursor !== ''; this.restartCycle(last.nextCursor);
    this.held = undefined; this.flight = undefined;
    this.publish({...this.value, chunks, items, bookmarks, nextCursor: last.nextCursor ?? '', metadata: last.metadata, pending: false, error: undefined, refreshRequired: false});
  }
  /** Retire the current viewport read without discarding its resident pages. */
  cancelPending(): void {
    const flight = this.flight; if (!flight) return;
    this.flight = undefined; this.held = undefined;
    this.publish({...this.value, pending: false, error: flight.expected.error});
    flight.abort.abort();
  }
  dispose(): void {if (this.disposed) return; this.disposed = true; this.generation++; const predecessor = this.flight; this.flight = undefined; this.held = undefined; this.owned.clear(); this.issued = new WeakSet(); predecessor?.abort.abort();}
}
