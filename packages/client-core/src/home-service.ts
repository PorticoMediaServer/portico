/**
 * Home as one document every app shows the same way: read once, kept while the app runs, and
 * brought up to date in place.
 *
 * - `load()` is the first read and the explicit refresh. Rows already on screen stay there while
 *   it runs; an error with rows on screen keeps the rows.
 * - `revalidate()` is the quiet read the freshness hub asks for when a library, the profile's
 *   watched state or a playback position moved. The answer is merged into what is shown: a row
 *   that did not change is the same object, so its cards do not draw again.
 * - The server sends each row whole, so `more()` has nothing further to read.
 *
 * No React and no timers of its own beyond the read deadline: tested under Node.
 */
import {readHomeLayoutSettings, saveHomeLayoutSettings} from './home-layout-settings.ts';
import type {DeviceClass} from './preferences.ts';
import {fetchHome, readHomeViewRow, type HomeApi, type HomeDocument, type HomeLayout, type HomeLayoutView, type HomeRow} from './home.ts';

/** `deviceClass`: the kind of device this app is, which a settings save names (Customize Home is saved as settings). */
export type HomeServiceOptions = Readonly<{timeoutMs?: number; deviceClass?: DeviceClass}>;

/** One opening of the layout editor; a save it started is not the next opening's. */
export type HomeLayoutWriteLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
const layoutClosed = () => Object.assign(new Error('Home layout opening closed'), {name: 'AbortError'});
export type HomeSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  document?: HomeDocument;
  /** The last failed read. Present it through the app's error words; never show its message. */
  error?: unknown;
  /** Rows whose next page is being read. */
  paging: ReadonlySet<string>;
}>;

/** What asking a row for more came to. */
export type HomePageOutcome = Readonly<{rowId: string; requestedCursor: string | undefined} & (
  | {status: 'applied'; applied: true; firstItemId: string | null}
  | {status: 'failed'; applied: false}
  | {status: 'refused'; applied: false; reason: 'not_ready' | 'row_missing' | 'no_cursor' | 'already_paging' | 'permission_denied'}
  | {status: 'ignored'; applied: false; reason: 'disposed' | 'superseded' | 'aborted' | 'row_changed' | 'viewer_changed' | 'no_progress'}
)>;

const none: ReadonlySet<string> = Object.freeze(new Set<string>());
/** How many deferred Home rows are read at once. */
export const HOME_ROW_READS = 3;
const sameJson = (a: unknown, b: unknown) => a === b || JSON.stringify(a) === JSON.stringify(b);

/** `fresh` with every row and card that did not change taken from `shown`. Returns `shown` when nothing changed. */
export function mergeHomeDocument(shown: HomeDocument, fresh: HomeDocument): HomeDocument {
  const before = new Map(shown.rows.map(row => [row.id, row]));
  let changed = shown.rows.length !== fresh.rows.length;
  const rows = fresh.rows.map((row, index) => {
    const old = before.get(row.id);
    // A saved view's row already read keeps its titles when a re-read sends it deferred again.
    if (row.deferred && old && !old.deferred && sameJson(old.viewRead, row.viewRead)) { if (shown.rows[index] !== old) changed = true; return old; }
    if (old && sameJson(old, row)) { if (shown.rows[index] !== old) changed = true; return old; }
    changed = true;
    if (!old) return row;
    const cards = new Map(old.entries.map(entry => [entry.id, entry]));
    return Object.freeze({...row, entries: Object.freeze(row.entries.map(entry => { const was = cards.get(entry.id); return was && sameJson(was, entry) ? was : entry; }))});
  });
  if (!changed && sameJson(shown.layout, fresh.layout) && sameJson(shown.spotlight, fresh.spotlight)) return shown;
  return Object.freeze({...fresh, rows: Object.freeze(rows)});
}

export class HomeService {
  private state: HomeSnapshot = Object.freeze({phase: 'idle', paging: none});
  private listeners = new Set<() => void>();
  /** Each explicit load starts a new one; an answer for an older one is dropped. */
  private generation = 0;
  private reading?: {controller: AbortController; promise: Promise<void>};
  /** Deferred rows being read, and rows whose read failed while they stayed near (asked again once they leave and return). */
  private rowReads = new Map<string, AbortController>();
  private failedRows = new Set<string>();
  private demanded: ReadonlySet<string> = new Set();
  private disposed = false;
  private readonly api: () => HomeApi;
  private readonly timeoutMs: number;
  private readonly deviceClass: DeviceClass;

  constructor(api: HomeApi | (() => HomeApi), options: HomeServiceOptions = {}) {
    this.api = typeof api === 'function' ? api : () => api;
    this.timeoutMs = options.timeoutMs ?? 15_000;
    this.deviceClass = options.deviceClass ?? 'web';
  }

  getSnapshot = (): HomeSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };

  private publish(patch: Partial<HomeSnapshot>): void {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }

  private async read(signal?: AbortSignal): Promise<HomeDocument> {
    const controller = new AbortController(), abort = () => controller.abort(signal?.reason);
    if (signal?.aborted) abort(); else signal?.addEventListener('abort', abort, {once: true});
    const timer = setTimeout(() => controller.abort(Object.assign(new Error('The server did not answer in time.'), {name: 'TimeoutError', code: 'timeout', retryable: true})), this.timeoutMs);
    try { return await fetchHome(this.api(), controller.signal); }
    catch (error) { throw controller.signal.aborted && !signal?.aborted ? controller.signal.reason : error; }
    finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
  }

  /** The first read, and Try again. With `clear`, what is shown is dropped first. */
  load(options: {clear?: boolean} = {}): Promise<void> {
    if (this.disposed) return Promise.resolve();
    if (this.reading && !options.clear) return this.reading.promise;
    this.reading?.controller.abort();
    this.stopRowReads();
    const mine = ++this.generation, controller = new AbortController();
    this.publish({phase: 'loading', error: undefined, ...(options.clear ? {document: undefined} : {})});
    const promise = this.read(controller.signal).then(document => {
      if (mine !== this.generation) return;
      this.publish({phase: 'ready', error: undefined, document: this.state.document ? mergeHomeDocument(this.state.document, document) : document});
    }, error => {
      if (mine !== this.generation) return;
      this.publish({phase: 'error', error});
    }).finally(() => { if (this.reading?.controller === controller) this.reading = undefined; });
    this.reading = {controller, promise};
    return promise;
  }
  retry = (): Promise<void> => this.load();

  /** The quiet read: what is shown stays until the answer arrives, and a failure changes nothing on screen. */
  async refresh(context?: {signal?: AbortSignal; settle?: () => Promise<void>}): Promise<'adopted' | 'not-answered' | 'superseded'> {
    if (this.disposed) return 'superseded';
    if (!this.state.document) { await this.load(); return this.state.document ? 'adopted' : 'not-answered'; }
    const mine = this.generation;
    let fresh: HomeDocument;
    try { fresh = await this.read(context?.signal); } catch { return this.disposed || mine !== this.generation ? 'superseded' : 'not-answered'; }
    await context?.settle?.();
    if (this.disposed || mine !== this.generation || context?.signal?.aborted || !this.state.document) return 'superseded';
    const merged = mergeHomeDocument(this.state.document, fresh);
    if (merged !== this.state.document || this.state.phase !== 'ready') this.publish({phase: 'ready', error: undefined, document: merged});
    return 'adopted';
  }
  async revalidate(context?: {signal?: AbortSignal; settle?: () => Promise<void>}): Promise<void> { await this.refresh(context); }

  /** The rows near the screen, as the app sees them. A saved view's row the server deferred is read once while it is
   * near (GET /v1/items with the row's own library and query); a row that leaves has its read stopped. A row whose
   * titles come back empty is taken out, as the server leaves out an empty row. One read per row at a time. */
  demand(rowIds: Iterable<string>): void {
    if (this.disposed) return;
    const wanted = this.demanded = new Set(rowIds);
    for (const [id, controller] of this.rowReads) if (!wanted.has(id)) { controller.abort(); this.rowReads.delete(id); }
    for (const id of this.failedRows) if (!wanted.has(id)) this.failedRows.delete(id);
    this.startRowReads();
  }
  /** At most HOME_ROW_READS at once, in Home's order: a saved view sorted at random over a whole library is a slow read,
   * and a fast scroll must not start every deferred row together. Each finished read starts the next demanded one. */
  private startRowReads(): void {
    for (const row of this.state.document?.rows ?? []) {
      if (this.rowReads.size >= HOME_ROW_READS) return;
      if (this.demanded.has(row.id) && row.deferred && row.viewRead && !this.rowReads.has(row.id) && !this.failedRows.has(row.id)) this.readRow(row);
    }
  }
  private stopRowReads(): void { for (const controller of this.rowReads.values()) controller.abort(); this.rowReads.clear(); this.failedRows.clear(); }
  private readRow(row: HomeRow): void {
    const controller = new AbortController(), mine = this.generation, read = row.viewRead!;
    this.rowReads.set(row.id, controller);
    void readHomeViewRow(this.api(), read, controller.signal).then(entries => {
      const document = this.state.document;
      if (controller.signal.aborted || mine !== this.generation || this.disposed || !document) return;
      const at = document.rows.findIndex(each => each.id === row.id && each.deferred && sameJson(each.viewRead, read));
      if (at < 0) return;
      const {deferred: _pending, ...was} = document.rows[at]!;
      const filled: HomeRow = Object.freeze({...was, entries, limit: entries.length});
      const rows = entries.length ? document.rows.map((each, index) => index === at ? filled : each) : document.rows.filter((_, index) => index !== at);
      this.publish({document: Object.freeze({...document, rows: Object.freeze(rows)})});
    }, () => { if (!controller.signal.aborted && mine === this.generation) this.failedRows.add(row.id); })
      .finally(() => { if (this.rowReads.get(row.id) === controller) { this.rowReads.delete(row.id); if (!this.disposed && mine === this.generation) this.startRowReads(); } });
  }

  /** Rows arrive whole, so there is never a further page. */
  async more(rowId: string): Promise<HomePageOutcome> {
    const refused = (reason: 'not_ready' | 'row_missing' | 'no_cursor'): HomePageOutcome => ({rowId, requestedCursor: undefined, status: 'refused', applied: false, reason});
    if (this.disposed) return {rowId, requestedCursor: undefined, status: 'ignored', applied: false, reason: 'disposed'};
    if (!this.state.document) return refused('not_ready');
    return refused(this.state.document.rows.some(row => row.id === rowId) ? 'no_cursor' : 'row_missing');
  }

  /** Customize Home: Home's rows with the profile's saved order and hidden rows (`home-layout-settings.ts`). */
  loadLayout = (signal?: AbortSignal): Promise<HomeLayoutView> => readHomeLayoutSettings(this.api(), this.deviceClass, signal);

  private assertLayoutOpen(lifetime?: HomeLayoutWriteLifetime): void {
    let open = !this.disposed && !lifetime?.signal.aborted;
    try { if (lifetime && !lifetime.isCurrent()) open = false; } catch { open = false; }
    if (!open) throw layoutClosed();
  }
  /** The later save wins; the server keeps no revision of the layout. */
  async saveLayout(rowOrder: readonly string[], hiddenRowIds: readonly string[], _idempotencyKey: () => string, _layoutRevision?: number, lifetime?: HomeLayoutWriteLifetime): Promise<HomeLayout> {
    this.assertLayoutOpen(lifetime);
    const saved = await saveHomeLayoutSettings(this.api(), this.deviceClass, {rowOrder, hiddenRowIds}, lifetime?.signal);
    this.assertLayoutOpen(lifetime);
    await this.refresh({signal: lifetime?.signal});
    return saved;
  }
  async resetLayout(_idempotencyKey: () => string, _layoutRevision?: number, lifetime?: HomeLayoutWriteLifetime): Promise<void> {
    this.assertLayoutOpen(lifetime);
    await saveHomeLayoutSettings(this.api(), this.deviceClass, null, lifetime?.signal);
    this.assertLayoutOpen(lifetime);
    await this.refresh({signal: lifetime?.signal});
  }

  /** Take an entry out of every row at once (a removal the screen has just asked the server for); returns the way to put the rows back. */
  withoutEntry(entryId: string): () => void {
    const before = this.state.document;
    if (this.disposed || !before) return () => {};
    let touched = false;
    const rows = before.rows.map(row => {
      if (!row.entries.some(e => e.id === entryId)) return row;
      touched = true;
      return Object.freeze({...row, entries: Object.freeze(row.entries.filter(e => e.id !== entryId)), ...(row.total !== undefined ? {total: Math.max(0, row.total - 1)} : {})});
    });
    if (!touched) return () => {};
    const after = Object.freeze({...before, rows: Object.freeze(rows)});
    this.publish({document: after});
    return () => {
      if (this.disposed) return;
      if (this.state.document === after) this.publish({document: before});
      else void this.revalidate();
    };
  }

  dispose(): void {
    this.disposed = true;
    this.generation++;
    this.reading?.controller.abort();
    this.stopRowReads();
    this.listeners.clear();
  }
}
