/**
 * Home as one document every app shows the same way: read once, kept while the app runs, and
 * brought up to date in place.
 *
 * - `load()` is the first read and the explicit refresh. Rows already on screen stay there while
 *   it runs; an error with rows on screen keeps the rows.
 * - `revalidate()` is the quiet read the freshness hub asks for when a library, the profile's
 *   watched state or a playback position moved. The answer is merged into what is shown: a row
 *   that did not change is the same object (its cards do not re-render), and the order of rows
 *   follows the server. Membership-fenced rows replace their pages and cursor together; rows
 *   whose server supports reanchoring may retain their bounded tail.
 * - A row's next page that the server refuses as out of date is not the person's problem: the
 *   whole document is read again quietly and the row continues from there.
 * - Saving the layout carries the revision that was read; when the layout moved elsewhere first,
 *   the conflict is surfaced for an explicit Reload layout before saving again.
 *
 * No React and no timers of its own beyond the read deadline: tested under Node.
 */
import {appendHomeRowPage, homeShellFence, fetchHome, fetchHomeLayout, fetchHomeRow, hasFencedHomeMembership, hasHomeRowContent, resetHomeLayout, saveHomeLayout, type HomeApi, type HomeDocument, type HomeLayout, type HomeLayoutView} from './home.ts';
import {isStaleAnswer, mergeRevalidatedList} from './freshness-actions.ts';

/** Manifest facts only; the revision is the exact parsed Home revision. */
export type HomeAuthorityObservation = Readonly<{
  serverId: string; viewerFence: string; privacyFence?: string; revision: HomeDocument['revision'];
  generation: number; quiet: number; at: number;
}>;
export type HomeObservationOptions = Readonly<{
  serverId: string;
  isCurrent(): boolean;
  observe(o: HomeAuthorityObservation): 'accept' | 'retire';
  retire(): void;
  mismatch(): void;
}>;
export type HomeServiceOptions = Readonly<{
  timeoutMs?: number; initialLimit?: number; observation?: HomeObservationOptions;
  /** The least time between two whole-Home refreshes asked for by a stale row page. While a
   * library is being scanned every continuation goes stale within a second; without this the
   * row asks again as soon as Home is brought up to date, and Home reloads twice a second. */
  staleRefreshEveryMs?: number; now?: () => number;
}>;
/** Terminal for this admitted Home owner, including ordinary-error fallback paths. */
export class HomeAuthorityRetired extends Error {
  constructor() { super('Home authority retired'); this.name = 'HomeAuthorityRetired'; }
}
type ManifestBorrower = {
  current(): boolean;
  finish(document?: HomeDocument, error?: unknown): void;
};
type ManifestFlight = {
  serial: number; generation: number; quiet: number; controller: AbortController;
  borrowers: Set<ManifestBorrower>; classifying: boolean; done: Promise<void>;
};

type DisplayOutcome = 'adopted' | 'not-answered' | 'superseded';
type DisplayBorrower = {
  settled: boolean;
  settle(): Promise<void>;
  finish(outcome: DisplayOutcome, error?: unknown): void;
};
type DisplayFlight = {
  document: HomeDocument; generation: number; serial: number; quiet: number;
  controller: AbortController; borrowers: Set<DisplayBorrower>;
};

/** One original layout editor opening; it never becomes the next editor. */
export type HomeLayoutWriteLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
const layoutRetired = () => Object.assign(new Error('Home layout opening retired'), {name: 'AbortError'});
export type HomeSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  document?: HomeDocument;
  /** The last failed read. Present it through the app's error words; never show its message. */
  error?: unknown;
  /** Rows whose next page is being read. */
  paging: ReadonlySet<string>;
}>;

/** A receipt belongs only to this call's row and cursor, never to a quiet refresh. */
export type HomePageOutcome = Readonly<{rowId: string; requestedCursor: string | undefined} & (
  /** Raw entry.id of the first new retained card; null for a page without new cards (including Live rows). */
  | {status: 'applied'; applied: true; firstItemId: string | null}
  | {status: 'failed'; applied: false}
  | {status: 'refused'; applied: false; reason: 'not_ready' | 'row_missing' | 'no_cursor' | 'already_paging' | 'permission_denied'}
  /** The server rejected the continuation and the recovery read failed. */
  | {status: 'stale'; applied: false}
  /** Recovery read succeeded; its entries are not a receipt for the rejected page. */
  | {status: 'revalidated'; applied: false}
  | {status: 'ignored'; applied: false; reason: 'disposed' | 'superseded' | 'aborted' | 'row_changed' | 'viewer_changed' | 'no_progress'}
)>;

const none: ReadonlySet<string> = Object.freeze(new Set<string>());
const sameJson = (a: unknown, b: unknown) => a === b || JSON.stringify(a) === JSON.stringify(b);
/** Backoff before a shelf that failed to hydrate is asked for again; after the last, the row is dropped. */
export const homeRowRetryDelaysMs: readonly number[] = [1_500, 5_000, 15_000];
const permissionDenied=(error:unknown):boolean=>typeof error==='object'&&error!==null&&('status' in error&&[401,403,404].includes(Number(error.status))||'code' in error&&['unauthorized','forbidden','not_found'].includes(String(error.code)));

/** `fresh` merged into `shown`: only reanchorable rows may keep a bounded tail. Returns `shown` when nothing changed. */
export function mergeHomeDocument(shown: HomeDocument, fresh: HomeDocument): HomeDocument {
  if (shown.serverId !== fresh.serverId || shown.viewerFence !== fresh.viewerFence || shown.privacyFence !== fresh.privacyFence) return fresh;
  // Layout owns row order and hidden membership, so an older layout cannot replace either.
  if (fresh.layout.revision < shown.layout.revision) return shown;
  const before = new Map(shown.rows.map(row => [row.id, row]));
  let changed = shown.rows.length !== fresh.rows.length;
  const rows = fresh.rows.flatMap((row, index) => {
    if (row.deferred && row.id === 'for_you') {
      const previous = shown.rows.filter(old => old.family === 'for_you');
      if (previous.length) { changed = true; return previous; }
    }
    const old = before.get(row.id);
    if (!old) { changed = true; return [row]; }
    if (row.deferred) { if (shown.rows[index] !== old) changed = true; return [old]; }
    if(row.live||old.live){
      if(sameJson(old,row)){if(shown.rows[index]!==old)changed=true;return [old];}
      changed=true;return [row];
    }
    // No membership fingerprint is exposed to the client. Even overlapping first
    // pages cannot prove that a retained tail or its signed cursor is still valid.
    const refreshed = mergeRevalidatedList(old.entries, row.entries, e => e.id, {same: sameJson, complete: hasFencedHomeMembership(row.id) || !row.nextCursor});
    const entries = refreshed;
    const keptTail = entries.length > row.entries.length;
    const {entries: _a, revision: _b, nextCursor: _c, hasMore: _d, ...oldRest} = old;
    const {entries: _e, revision: _f, nextCursor: _g, hasMore: _h, ...freshRest} = row;
    const nextCursor = keptTail ? old.nextCursor : row.nextCursor;
    if (entries === old.entries && nextCursor === old.nextCursor && sameJson(oldRest, freshRest)) {
      if (shown.rows[index] !== old) changed = true;
      return [old];
    }
    changed = true;
    return [Object.freeze({...row, entries, nextCursor, hasMore: keptTail ? old.hasMore : row.hasMore})];
  });
  if (!changed && sameJson(shown.layout, fresh.layout) && sameJson(shown.spotlight, fresh.spotlight)) return shown;
  return Object.freeze({...fresh, rows: Object.freeze(rows)});
}

export class HomeService {
  private state: HomeSnapshot = Object.freeze({phase: 'idle', paging: none});
  private listeners = new Set<() => void>();
  private generation = 0;
  /** Quiet reads are numbered too: a settled read older than the newest accepted quiet read is stale. */
  private quiet = 0;
  private reading?: {controller: AbortController; promise: Promise<void>; serial: number};
  private pages = new Map<string, AbortController>();
  private disposed = false;
  private hydrationGeneration = 0;
  private hydrations = new Set<AbortController>();
  /** Rows on screen or about to be; undefined until a caller declares any, when every deferred row loads. */
  private demand?: ReadonlySet<string>;
  /** Deferred rows of the current document that wait for demand, each fetched at most once. */
  private waiting = new Map<string, () => void>();
  private readonly rowRetries = new Set<ReturnType<typeof setTimeout>>();
  /** Shelves dropped after their last retry; the next return to Home reads them again. */
  private readonly droppedRows = new Set<string>();
  /** Failed reads per shelf, kept across quiet re-reads so a retry schedule can't restart forever. */
  private readonly rowFailures = new Map<string, number>();
  private readonly api: () => HomeApi;
  private readonly timeoutMs: number;
  private readonly initialLimit: number | undefined;
  private readonly observation?: HomeObservationOptions;
  private retired = false;
  private baseline?: string;
  private privateReadSerial = 0;
  private manifest?: ManifestFlight;
  private display?: DisplayFlight;

  /** `api` may be a function so a renewed sign-in is picked up without a new service. */
  constructor(api: HomeApi | (() => HomeApi), options: HomeServiceOptions = {}) {
    if (options.initialLimit !== undefined && (!Number.isSafeInteger(options.initialLimit) || options.initialLimit < 1 || options.initialLimit > 60)) throw new RangeError('Home initialLimit must be an integer from 1 to 60');
    this.api = typeof api === 'function' ? api : () => api;
    this.timeoutMs = options.timeoutMs ?? 15_000;
    this.initialLimit = options.initialLimit;
    this.observation = options.observation;
    this.staleRefreshEveryMs = options.staleRefreshEveryMs ?? 5_000;
    this.now = options.now ?? Date.now;
  }
  private readonly staleRefreshEveryMs: number;
  private readonly now: () => number;
  private lastStaleRefresh = -Infinity;

  /** Declares the row ids that are visible or about to be. A deferred row loads when first
   * declared; the rest stay as they are until asked for. `undefined` loads every row again. */
  setRowDemand(ids: readonly string[] | undefined): void {
    this.demand = ids ? new Set(ids) : undefined;
    // oxlint-disable-next-line unicorn/no-useless-spread -- Demand callbacks may change waiting membership.
    for (const [id, start] of [...this.waiting]) if (this.wanted(id)) { this.waiting.delete(id); start(); }
  }
  /** The `for_you` placeholder is replaced by its family's rows; either id asks for it. */
  private wanted(id: string): boolean {
    const demand = this.demand;
    return !demand || demand.has(id) || id === 'for_you' && !!this.state.document?.rows.some(row => row.family === 'for_you' && demand.has(row.id));
  }

  getSnapshot = (): HomeSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(patch: Partial<HomeSnapshot>): void {
    if (!this.ownerCurrent()) return;
    this.state = Object.freeze({...this.state, ...patch});
    if (this.display && this.display.document !== this.state.document) {
      this.display.controller.abort();
      this.display = undefined;
    }
    const listeners = [...this.listeners];
    for (const listener of listeners) { if (this.observation && !this.ownerCurrent()) break; listener(); }
  }

  private ownerCurrent(): boolean {
    if (this.disposed || this.retired) return false;
    try { return !this.observation || this.observation.isCurrent(); } catch { return false; }
  }

  private retireAuthority(): HomeAuthorityRetired {
    if (!this.retired) {
      this.retired = true;
      this.display?.controller.abort();
      this.manifest?.controller.abort();
      for (const controller of this.hydrations) controller.abort();
      this.waiting.clear();
      for (const timer of this.rowRetries) clearTimeout(timer);
      this.rowRetries.clear();
      for (const controller of this.pages.values()) controller.abort();
      this.observation?.retire();
    }
    return new HomeAuthorityRetired();
  }

  /** Current, parsed row proof only. Children have their own Home fence. */
  private rowMismatch(row: HomeDocument['rows'][number], fence: string): boolean {
    if (row.viewerFence === fence && !row.children?.some(child => child.viewerFence !== fence)) return false;
    this.observation?.mismatch();
    return true;
  }

  private advanceQuiet(): number {
    this.display?.controller.abort();
    this.display = undefined;
    return ++this.quiet;
  }

  /** One service-owned network flight; callers own only their borrow, never its controller. */
  private async observedRead(signal: AbortSignal | undefined, witness: () => boolean): Promise<HomeDocument | undefined> {
    const serial = this.privateReadSerial, generation = this.generation;
    const admitted = () => this.ownerCurrent() && !signal?.aborted && serial === this.privateReadSerial && generation === this.generation;
    const current = () => {
      try { return admitted() && witness() && admitted(); }
      catch { return false; }
    };
    if (!current()) return;
    // An acknowledged write/load cannot join a pre-barrier read. Wait for release,
    // then all same-serial waiters converge on exactly one new flight.
    while (this.manifest && (this.manifest.controller.signal.aborted || this.manifest.serial !== serial || this.manifest.generation !== generation)) {
      const previous = this.manifest;
      await new Promise<void>(resolve => {
        const stop = () => resolve();
        signal?.addEventListener('abort', stop, {once: true});
        void previous.done.then(() => { signal?.removeEventListener('abort', stop); resolve(); });
      });
      if (!current()) return;
    }
    if (!current()) return;
    let flight = this.manifest;
    let start = false, release = () => {};
    if (!flight) {
      const done = new Promise<void>(resolve => { release = resolve; });
      flight = {serial, generation, quiet: this.quiet, controller: new AbortController(), borrowers: new Set(), classifying: false, done};
      this.manifest = flight;
      start = true;
    }
    const owned = flight;
    const answer = new Promise<HomeDocument | undefined>((resolve, reject) => {
      const stop = () => { if (!owned.classifying) borrower.finish(); };
      const borrower: ManifestBorrower = {
        current,
        finish: (document, error) => {
          if (!owned.borrowers.delete(borrower)) return;
          signal?.removeEventListener('abort', stop);
          if (!owned.borrowers.size) owned.controller.abort();
          if (error !== undefined) reject(error); else resolve(document);
        },
      };
      owned.borrowers.add(borrower);
      signal?.addEventListener('abort', stop, {once: true});
    });
    if (start) void this.runManifest(owned, release);
    return answer;
  }

  private async runManifest(flight: ManifestFlight, release: () => void): Promise<void> {
    let document: HomeDocument | undefined, error: unknown;
    try {
      document = await this.read(flight.controller.signal);
      if (![...flight.borrowers].some(borrower => borrower.current())) document = undefined;
      if (document) {
        flight.classifying = true;
        const observation = this.observation!;
        if (document.serverId !== observation.serverId) throw this.retireAuthority();
        const verdict = observation.observe(Object.freeze({serverId: document.serverId, viewerFence: document.viewerFence,
          ...(document.privacyFence !== undefined ? {privacyFence: document.privacyFence} : {}),
          revision: document.revision, generation: flight.generation, quiet: flight.quiet, at: Date.now()}));
        // The authority retires on a privacy change only; a library added or removed is a refresh.
        if (verdict === 'retire' || !this.ownerCurrent() || this.baseline !== undefined && homeShellFence(document) !== this.baseline) throw this.retireAuthority();
        if (Array.from(flight.borrowers).some(borrower => borrower.current())) this.baseline ??= homeShellFence(document);
        else document = undefined;
      }
    } catch (caught) { error = caught; }
    finally {
      // Release network ownership before resolving any display borrower (and settle).
      flight.classifying = false;
      if (this.manifest === flight) this.manifest = undefined;
      release();
    }
    for (const borrower of flight.borrowers) {
      if (error instanceof HomeAuthorityRetired) borrower.finish(undefined, error);
      else if (!borrower.current()) borrower.finish();
      else borrower.finish(document, error);
    }
  }

  /** Off-Home authority proof: no snapshot, row, paging or display-ticket changes. */
  async authority(context: {signal?: AbortSignal; isCurrent(): boolean}): Promise<'answered' | 'superseded'> {
    if (!this.observation) return 'superseded';
    const generation = this.generation, serial = this.privateReadSerial;
    const current = () => generation === this.generation && serial === this.privateReadSerial && context.isCurrent();
    const fresh = await this.observedRead(context.signal, current);
    return fresh && this.ownerCurrent() && !context.signal?.aborted && current() ? 'answered' : 'superseded';
  }

  /** Same shown-document callers share adoption, with independently cancellable waits. */
  async refresh(context?: {signal?: AbortSignal; settle?: () => Promise<void>}): Promise<DisplayOutcome> {
    if (!this.ownerCurrent() || context?.signal?.aborted) return 'superseded';
    const document = this.state.document;
    if (this.state.phase !== 'ready' || !document) return 'not-answered';
    // This additive entry has no effect on omitted-option revalidate's old path.
    if (!this.observation) return this.refreshUnobserved(context);
    let flight = this.display, start = false;
    if (!flight || flight.document !== document || !this.displayCurrent(flight)) {
      const quiet = this.advanceQuiet();
      flight = {document, quiet, serial: this.privateReadSerial, generation: this.generation,
        controller: new AbortController(), borrowers: new Set()};
      this.display = flight;
      start = true;
    }
    const owned = flight;
    const answer = new Promise<DisplayOutcome>((resolve, reject) => {
      let settled: Promise<void> | undefined, cancel = () => {};
      const cancelled = new Promise<void>(yes => { cancel = yes; });
      const stop = () => borrower.finish('superseded');
      const borrower: DisplayBorrower = {
        settled: false,
        settle: () => {
          // A cancelled borrow's callback must never start later.
          if (!owned.borrowers.has(borrower) || owned.controller.signal.aborted) return Promise.resolve();
          return settled ??= Promise.race([Promise.resolve().then(() => {
            if (owned.borrowers.has(borrower) && !owned.controller.signal.aborted) return context?.settle?.();
          }), cancelled]);
        },
        finish: (outcome, error) => {
          if (!owned.borrowers.delete(borrower)) return;
          context?.signal?.removeEventListener('abort', stop);
          owned.controller.signal.removeEventListener('abort', cancel);
          cancel();
          if (!owned.borrowers.size) owned.controller.abort();
          if (error !== undefined) reject(error); else resolve(outcome);
        },
      };
      owned.borrowers.add(borrower);
      context?.signal?.addEventListener('abort', stop, {once: true});
      owned.controller.signal.addEventListener('abort', cancel, {once: true});
    });
    if (start) void this.runDisplay(owned);
    return answer;
  }

  private displayCurrent(flight: DisplayFlight): boolean {
    return this.ownerCurrent() && !flight.controller.signal.aborted && flight.generation === this.generation
      && flight.serial === this.privateReadSerial && flight.quiet === this.quiet;
  }

  private async runDisplay(flight: DisplayFlight): Promise<void> {
    const current = () => this.displayCurrent(flight) && this.state.document === flight.document;
    let outcome: DisplayOutcome = 'superseded', error: unknown;
    try {
      const fresh = await this.observedRead(flight.controller.signal, current);
      if (!fresh || !current()) return;
      // A caller joining during settlement shares the same answer, but its own
      // settle must finish too. Cancelling it releases only its wait.
      while (current()) {
        const pending = [...flight.borrowers].filter(borrower => !borrower.settled);
        if (!pending.length) break;
        for (const borrower of pending) borrower.settled = true;
        await Promise.all(pending.map(borrower => borrower.settle()));
      }
      if (!current()) return;
      if (this.state.phase !== 'ready' || !this.state.document) { outcome = 'not-answered'; return; }
      const merged = this.withoutDropped(mergeHomeDocument(this.state.document, fresh));
      if (this.display === flight) this.display = undefined;
      if (merged !== this.state.document) this.publish({document: merged});
      if (!this.displayCurrent(flight) || this.state.document !== merged) return;
      this.hydrateRows(fresh, flight.quiet);
      if (this.displayCurrent(flight)) outcome = 'adopted';
    } catch (caught) {
      if (caught instanceof HomeAuthorityRetired || current()) error = caught;
    } finally {
      if (this.display === flight) this.display = undefined;
      for (const borrower of flight.borrowers) borrower.finish(outcome, error);
    }
  }

  private async refreshUnobserved(context?: {signal?: AbortSignal; settle?: () => Promise<void>}): Promise<DisplayOutcome> {
    const generation = this.generation, quiet = this.advanceQuiet();
    const current = () => this.ownerCurrent() && generation === this.generation && quiet === this.quiet && !context?.signal?.aborted;
    try {
      const fresh = await this.read(context?.signal ?? new AbortController().signal);
      if (!current()) return 'superseded';
      await context?.settle?.();
      if (!current()) return 'superseded';
      if (this.state.phase !== 'ready' || !this.state.document) return 'not-answered';
      const merged = this.withoutDropped(mergeHomeDocument(this.state.document, fresh));
      if (merged !== this.state.document) this.publish({document: merged});
      if (!current()) return 'superseded';
      this.hydrateRows(fresh, quiet);
      return current() ? 'adopted' : 'superseded';
    } catch (error) { if (!current()) return 'superseded'; throw error; }
  }

  private hydrateRows(fresh: HomeDocument, quiet?: number): void {
    if (!this.ownerCurrent()) return;
    const shown = this.state.document;
    if (shown && shown.serverId === fresh.serverId && shown.viewerFence === fresh.viewerFence && fresh.layout.revision < shown.layout.revision) return;
    const epoch = ++this.hydrationGeneration;
    for (const controller of this.hydrations) controller.abort();
    this.hydrations.clear();
    this.waiting.clear();
    for (const timer of this.rowRetries) clearTimeout(timer);
    this.rowRetries.clear();
    const queue: HomeDocument['rows'][number][] = [];
    const mine = this.generation;
    let running = 0;
    const consume = async () => {
      running++;
      try { while (queue.length && this.ownerCurrent() && epoch === this.hydrationGeneration && (quiet === undefined || quiet === this.quiet)) {
        const descriptor = queue.shift()!;
        const controller = new AbortController();
        this.hydrations.add(controller);
        let abort = () => {};
        let timer: ReturnType<typeof setTimeout> | undefined;
        try {
          const row = await Promise.race([
            fetchHomeRow(this.api(), descriptor.id, {limit: this.initialLimit ?? (descriptor.limit || undefined)}, controller.signal),
            new Promise<never>((_, reject) => {
              abort = () => reject(Object.assign(new Error('home_row_cancelled'), {name: 'AbortError'}));
              controller.signal.addEventListener('abort', abort, {once: true});
              timer = setTimeout(() => controller.abort(), this.timeoutMs);
            }),
          ]);
          const shown = this.state.document;
          if (!this.ownerCurrent() || mine !== this.generation || (quiet !== undefined && quiet !== this.quiet) || epoch !== this.hydrationGeneration || controller.signal.aborted || !shown || shown.viewerFence !== fresh.viewerFence || shown.serverId !== fresh.serverId) continue;
          if (this.rowMismatch(row, fresh.viewerFence) || !this.ownerCurrent()) continue;
          const replacements = row.children ?? [row];
          const rows = shown.rows.flatMap(old => old.id === descriptor.id || descriptor.id === 'for_you' && old.family === 'for_you' ? [] : [old]);
          const at = shown.rows.findIndex(old => old.id === descriptor.id || descriptor.id === 'for_you' && old.family === 'for_you');
          rows.splice(Math.max(0, at), 0, ...replacements.filter(next => hasHomeRowContent(next) || next.critical));
          this.publish({document: mergeHomeDocument(shown, Object.freeze({...shown, rows: Object.freeze(rows)})), error: undefined});
          this.rowFailures.delete(descriptor.id);
        } catch (error) {
          if(permissionDenied(error)&&this.ownerCurrent()&&mine===this.generation&&(quiet===undefined||quiet===this.quiet)&&epoch===this.hydrationGeneration&&!controller.signal.aborted)this.removeRow(descriptor.id);
          // A failed or timed-out shelf stays a skeleton, which nothing else would ever retry on this document.
          // Retry it a few times with backoff while this hydration is current, then drop it; a later refresh
          // brings the descriptor back. Other shelves continue meanwhile.
          else if (mine === this.generation && epoch === this.hydrationGeneration && this.ownerCurrent() && (quiet === undefined || quiet === this.quiet) && !(error instanceof HomeAuthorityRetired)) {
            const attempt = (this.rowFailures.get(descriptor.id) ?? 0) + 1;
            this.rowFailures.set(descriptor.id, attempt);
            if (attempt > homeRowRetryDelaysMs.length) { this.droppedRows.add(descriptor.id); this.removeRow(descriptor.id); }
            else {
              const retry = setTimeout(() => {
                this.rowRetries.delete(retry);
                if (this.ownerCurrent() && epoch === this.hydrationGeneration) { queue.push(descriptor); pump(); }
              }, homeRowRetryDelaysMs[attempt - 1]);
              this.rowRetries.add(retry);
            }
          }
        } finally { if (timer) clearTimeout(timer); controller.signal.removeEventListener('abort', abort); this.hydrations.delete(controller); }
      } } finally { running--; }
    };
    const pump = () => { while (running < 2 && queue.length) void consume(); };
    for (const row of fresh.rows) {
      // A shelf dropped after its retries stays away until the viewer comes back to Home (recoverDroppedRows).
      if (!row.deferred || this.droppedRows.has(row.id)) continue;
      if (this.wanted(row.id)) queue.push(row);
      else this.waiting.set(row.id, () => { if (this.ownerCurrent() && epoch === this.hydrationGeneration) { queue.push(row); pump(); } });
    }
    pump();
  }

  /** A quiet re-read brings every descriptor back; a dropped shelf must not return as a skeleton. */
  private withoutDropped(document: HomeDocument): HomeDocument {
    if (!this.droppedRows.size || !document.rows.some(row => this.droppedRows.has(row.id))) return document;
    return Object.freeze({...document, rows: Object.freeze(document.rows.filter(row => !this.droppedRows.has(row.id)))});
  }

  private removeRow(id:string):void {
    const document=this.state.document;
    if(document)this.publish({document:Object.freeze({...document,rows:Object.freeze(document.rows.filter(row=>row.id!==id))})});
  }

  private async read(signal: AbortSignal): Promise<HomeDocument> {
    let timer: ReturnType<typeof setTimeout> | undefined, abort = () => {};
    const controller = new AbortController();
    const stop = () => controller.abort();
    signal.addEventListener('abort', stop, {once: true});
    try {
      if (this.observation && (signal.aborted || !this.ownerCurrent())) throw Object.assign(new Error('home_read_cancelled'), {name: 'AbortError'});
      const cancellation = new Promise<never>((_, reject) => {
        abort = () => reject(Object.assign(new Error('home_read_cancelled'), {name: 'AbortError'}));
        controller.signal.addEventListener('abort', abort, {once: true});
        timer = setTimeout(() => { reject(Object.assign(new Error('home_read_timeout'), {code: 'request_timeout', retryable: true})); controller.abort(); }, this.timeoutMs);
      });
      return await Promise.race([cancellation, fetchHome(this.api(), this.initialLimit, controller.signal)]);
    } finally {
      if (timer) clearTimeout(timer);
      controller.signal.removeEventListener('abort', abort);
      signal.removeEventListener('abort', stop);
    }
  }

  /** The first read, or an explicit refresh. `clear` drops what is shown first (another profile's taste was reset). Concurrent calls share one read. */
  load(options: {clear?: boolean} = {}): Promise<void> {
    if (!this.ownerCurrent()) return Promise.resolve();
    if (this.reading && !options.clear && (!this.observation || this.reading.serial === this.privateReadSerial)) return this.reading.promise;
    this.reading?.controller.abort();
    const mine = ++this.generation, controller = new AbortController();
    if (this.observation) this.privateReadSerial++;
    this.droppedRows.clear(); this.rowFailures.clear();
    // An explicit load/clear invalidates every outstanding quiet read.
    this.advanceQuiet();
    for (const page of this.pages.values()) page.abort();
    this.pages.clear();
    const ticket = this.quiet, serial = this.privateReadSerial;
    const current = () => this.ownerCurrent() && mine === this.generation && (!this.observation || ticket === this.quiet && serial === this.privateReadSerial) && !controller.signal.aborted;
    this.publish({phase: 'loading', error: undefined, paging: none, ...(options.clear ? {document: undefined} : {})});
    const promise = (async () => {
      try {
        const document = this.observation ? await this.observedRead(controller.signal, current) : await this.read(controller.signal);
        if (!document || !current()) return;
        this.publish({phase: 'ready', document: this.state.document ? mergeHomeDocument(this.state.document, document) : document, error: undefined});
        if (current()) this.hydrateRows(document, this.observation ? ticket : undefined);
      } catch (error) {
        if (error instanceof HomeAuthorityRetired) throw error;
        if (!current()) return;
        this.publish({phase: 'error', error});
      } finally {
        if (this.reading?.controller === controller) this.reading = undefined;
      }
    })();
    this.reading = {controller, promise, serial};
    return promise;
  }

  retry = (): Promise<void> => this.load();

  /** The quiet read: merge the server's current Home into what is shown. Rejects when the read failed; nothing changes then. */
  /**
   * Coming back to Home: a shelf dropped after its last retry is read again with the document
   * (there's no row-level error state in the mock-ups, so a failed shelf leaves rather than
   * showing an error; this brings it back). Nothing is read when nothing was dropped.
   */
  async recoverDroppedRows(): Promise<void> {
    if (!this.droppedRows.size || this.disposed || this.state.phase !== 'ready') return;
    this.droppedRows.clear(); this.rowFailures.clear();
    await this.revalidate();
  }

  async revalidate(context?: {signal?: AbortSignal; settle?: () => Promise<void>}): Promise<void> {
    if (this.observation) { await this.refresh(context); return; }
    if (this.disposed || this.state.phase !== 'ready' || !this.state.document) return;
    const mine = this.generation, ticket = this.advanceQuiet(), controller = new AbortController(), stop = () => controller.abort();
    const current = () => !this.disposed && mine === this.generation && ticket === this.quiet && !controller.signal.aborted;
    context?.signal?.addEventListener('abort', stop, {once: true});
    try {
      const fresh = await this.read(controller.signal);
      // A response that was already superseded must not invoke another caller's
      // settle callback or wait on its obsolete completion.
      if (!current()) return;
      await context?.settle?.();
      if (!current() || !this.state.document || this.state.phase !== 'ready') return;
      const merged = this.withoutDropped(mergeHomeDocument(this.state.document, fresh));
      if (merged !== this.state.document) this.publish({document: merged});
      // A read superseded after its settle may not start or own deferred shelves.
      if (current()) this.hydrateRows(fresh, ticket);
    } catch (error) {
      // A late failure from a superseded read is not the current document's failure.
      if (!current()) return;
      throw error;
    } finally { context?.signal?.removeEventListener('abort', stop); }
  }

  /** The next page of one row. Only an applied outcome may fulfil that exact cursor's wait. */
  async more(rowId: string): Promise<HomePageOutcome> {
    const shown = this.state.document;
    const row = shown?.rows.find(r => r.id === rowId);
    const request = {rowId, requestedCursor: row?.nextCursor};
    if (!this.ownerCurrent()) return {...request, status: 'ignored', applied: false, reason: 'disposed'};
    if (this.state.phase !== 'ready') return {...request, status: 'refused', applied: false, reason: 'not_ready'};
    if (!shown || !row) return {...request, status: 'refused', applied: false, reason: 'row_missing'};
    if (!row.nextCursor) return {...request, status: 'refused', applied: false, reason: 'no_cursor'};
    if (this.pages.has(rowId)) return {...request, status: 'refused', applied: false, reason: 'already_paging'};
    const mine = this.generation, controller = new AbortController(), cursor = row.nextCursor;
    const ignored = (reason: Extract<HomePageOutcome, {status: 'ignored'}>['reason']): HomePageOutcome => ({...request, status: 'ignored', applied: false, reason});
    const cancelled = (): HomePageOutcome | undefined => !this.ownerCurrent() ? ignored('disposed') : mine !== this.generation ? ignored('superseded') : controller.signal.aborted ? ignored('aborted') : undefined;
    const opposed = (): HomePageOutcome | undefined => {
      const stopped = cancelled();
      if (stopped) return stopped;
      const current = this.state.document;
      if (!current || current.serverId !== shown.serverId || current.viewerFence !== shown.viewerFence) return ignored('viewer_changed');
      // Even an equal cursor cannot authorize a page against a replaced membership or Live observation.
      if (current.rows.find(r => r.id === rowId) !== row) return ignored('row_changed');
    };
    this.pages.set(rowId, controller);
    this.publish({paging: new Set(this.state.paging).add(rowId)});
    const done = () => {
      if (this.pages.get(rowId) !== controller) return;
      this.pages.delete(rowId);
      if (this.disposed || mine !== this.generation) return;
      const paging = new Set(this.state.paging); paging.delete(rowId);
      this.publish({paging: paging.size ? paging : none});
    };
    let abort = () => {};
    try {
      const stopped = opposed();
      if (stopped) return stopped;
      // Settle on cancellation even when the transport ignores its signal.
      const cancellation = new Promise<never>((_, reject) => {
        abort = () => reject(Object.assign(new Error('home_page_cancelled'), {name: 'AbortError'}));
        controller.signal.addEventListener('abort', abort, {once: true});
      });
      const page = await Promise.race([fetchHomeRow(this.api(), rowId, {cursor,limit:row.limit||undefined,group:row.live?.group||undefined,sourceId:row.seeAll?.type==='guide'?row.seeAll.sourceId:undefined}, controller.signal), cancellation]);
      const outdated = opposed();
      if (outdated) return outdated;
      if ((this.observation ? this.rowMismatch(page, shown.viewerFence) : page.viewerFence !== shown.viewerFence) || !this.ownerCurrent()) return ignored('viewer_changed');
      const next = appendHomeRowPage(row, page);
      const seen = new Set(row.entries.map(entry => entry.id));
      const firstItemId = next.entries.find(entry => !seen.has(entry.id))?.id ?? null;
      if (firstItemId === null && next.nextCursor === cursor && (next.live?.channels.length ?? 0) === (row.live?.channels.length ?? 0)) return ignored('no_progress');
      const document = this.state.document!;
      this.publish({document: Object.freeze({...document, rows: Object.freeze(document.rows.map(r => r.id === rowId ? next : r))})});
      done();
      const superseded = cancelled();
      if (superseded) return superseded;
      if (this.state.document?.rows.find(r => r.id === rowId) !== next) return ignored('row_changed');
      return {...request, status: 'applied', applied: true, firstItemId};
    } catch (error) {
      if (error instanceof HomeAuthorityRetired) throw error;
      const outdated = opposed();
      if (outdated) return outdated;
      done();
      const replaced = opposed();
      if (replaced) return replaced;
      if(permissionDenied(error)){this.removeRow(rowId);return {...request, status: 'refused', applied: false, reason: 'permission_denied'};}
      // Out of date, not broken: bring the whole of Home up to date in place and let the row continue from there.
      if (isStaleAnswer(error)) {
        const document = this.state.document, now = this.now(), recently = now - this.lastStaleRefresh < this.staleRefreshEveryMs;
        // A row refreshed a moment ago stops asking (it keeps its cards) until Home's own quiet read brings a new cursor.
        if ((recently || hasFencedHomeMembership(rowId)) && document && document.rows.find(r => r.id === rowId)?.nextCursor === cursor) {
          this.publish({document: Object.freeze({...document, rows: Object.freeze(document.rows.map(r => r.id === rowId ? Object.freeze({...r, nextCursor: ''}) : r))})});
        }
        if (recently) return cancelled() ?? {...request, status: 'stale', applied: false};
        this.lastStaleRefresh = now;
        try {
          if (this.observation) {
            const outcome = await this.refresh();
            if (outcome !== 'adopted') return cancelled() ?? ignored('superseded');
          } else await this.revalidate();
          return cancelled() ?? {...request, status: 'revalidated', applied: false};
        } catch (error) {
          if (error instanceof HomeAuthorityRetired) throw error;
          return cancelled() ?? {...request, status: 'stale', applied: false};
        }
      }
      // Any other failed page leaves the row as it is; reaching its end again asks again.
      return {...request, status: 'failed', applied: false};
    } finally {
      controller.signal.removeEventListener('abort', abort);
      done();
    }
  }

  loadLayout = (signal?: AbortSignal): Promise<HomeLayoutView> => {
    if (!this.observation) return fetchHomeLayout(this.api(), signal);
    if (!this.ownerCurrent() || signal?.aborted) return Promise.reject(layoutRetired());
    return fetchHomeLayout(this.api(), signal).then(layout => {
      if (!this.ownerCurrent() || signal?.aborted) throw layoutRetired();
      return layout;
    });
  };

  private assertLayoutCurrent(lifetime?: HomeLayoutWriteLifetime): void {
    let live = this.ownerCurrent() && !lifetime?.signal.aborted;
    try {if (lifetime && !lifetime.isCurrent()) live = false;} catch {live = false;}
    if (!live) throw layoutRetired();
  }
  /** A conflicting save is surfaced for Reload layout; it never overwrites another device silently. */
  async saveLayout(rowOrder: readonly string[], hiddenRowIds: readonly string[], idempotencyKey: () => string, expectedRevision?: number, lifetime?: HomeLayoutWriteLifetime): Promise<HomeLayout> {
    this.assertLayoutCurrent(lifetime);
    const revision = expectedRevision ?? this.state.document?.layout.revision;
    if (revision === undefined) throw new Error('Home is not loaded.');
    try {
      const saved = await saveHomeLayout(this.api(), {expectedRevision: revision, rowOrder, hiddenRowIds, idempotencyKey: idempotencyKey()}, lifetime?.signal);
      this.acknowledgeLayout();
      this.assertLayoutCurrent(lifetime);
      await this.afterLayout(lifetime);this.assertLayoutCurrent(lifetime);return saved;
    } catch (error) {if (error instanceof HomeAuthorityRetired) throw error;this.assertLayoutCurrent(lifetime);throw error;}
  }

  async resetLayout(idempotencyKey: () => string, expectedRevision?: number, lifetime?: HomeLayoutWriteLifetime): Promise<void> {
    this.assertLayoutCurrent(lifetime);
    try {
      await resetHomeLayout(this.api(), idempotencyKey(), lifetime?.signal, expectedRevision);
      this.acknowledgeLayout();
      this.assertLayoutCurrent(lifetime);
      await this.afterLayout(lifetime);this.assertLayoutCurrent(lifetime);
    } catch (error) {if (error instanceof HomeAuthorityRetired) throw error;this.assertLayoutCurrent(lifetime);throw error;}
  }

  private acknowledgeLayout(): void {
    if (!this.observation) return;
    // The write was acknowledged even if the editor retired while awaiting it.
    // Its pre-write reads/shelves can no longer represent current Home.
    this.privateReadSerial++;
    this.advanceQuiet();
  }

  /** The rows follow a new arrangement at once, without a loading state when Home is already showing. */
  private async afterLayout(lifetime?: HomeLayoutWriteLifetime): Promise<void> {
    if (this.observation) { await this.afterObservedLayout(lifetime); return; }
    this.advanceQuiet();
    if (lifetime) {
      this.assertLayoutCurrent(lifetime);
      if (this.state.phase === 'ready' && this.state.document) {
        const mine = this.generation;
        try {
          const fresh = await this.read(lifetime.signal);this.assertLayoutCurrent(lifetime);
          if (mine === this.generation && this.state.document) {
            this.advanceQuiet();
            this.publish({document: mergeHomeDocument(this.state.document, fresh)});this.hydrateRows(fresh);
          }
        } catch (error) {if (error instanceof HomeAuthorityRetired) throw error;this.assertLayoutCurrent(lifetime);throw error;}
      }
      // A supplied opening never falls through to load(), whose lifecycle is
      // the persistent Home owner rather than this editor's captured lifetime.
      this.assertLayoutCurrent(lifetime);return;
    }
    if (this.state.phase === 'ready' && this.state.document) {
      const mine = this.generation;
      try {
        const fresh = await this.read(new AbortController().signal);
        if (!this.disposed && mine === this.generation && this.state.document) {
          this.advanceQuiet();
          this.publish({document: mergeHomeDocument(this.state.document, fresh)});this.hydrateRows(fresh);
        }
        return;
      } catch { /* Existing callers keep their ordinary Home load fallback. */ }
    }
    await this.load();
  }

  private async afterObservedLayout(lifetime?: HomeLayoutWriteLifetime): Promise<void> {
    this.assertLayoutCurrent(lifetime);
    const mine = this.generation, ticket = this.quiet, serial = this.privateReadSerial;
    const current = () => this.ownerCurrent() && mine === this.generation && ticket === this.quiet && serial === this.privateReadSerial && !lifetime?.signal.aborted && (!lifetime || lifetime.isCurrent());
    if (this.state.phase === 'ready' && this.state.document) {
      try {
        const fresh = await this.observedRead(lifetime?.signal, current);
        this.assertLayoutCurrent(lifetime);
        if (!fresh || !current()) return;
        this.publish({document: mergeHomeDocument(this.state.document!, fresh)});
        if (current()) this.hydrateRows(fresh, ticket);
        return;
      } catch (error) {
        if (error instanceof HomeAuthorityRetired) throw error;
        this.assertLayoutCurrent(lifetime);
        if (lifetime) throw error;
        if (!current()) return;
        // Ordinary failures retain the persistent owner's existing load fallback.
      }
    }
    if (!lifetime && current()) await this.load();
  }

  /** Take an entry out of every row at once (an optimistic removal the screen has just asked the server for); returns the way to put the rows back. */
  withoutEntry(entryId: string): () => void {
    const before = this.state.document;
    if (!this.ownerCurrent() || !before) return () => {};
    let touched = false;
    const rows = before.rows.map(row => {
      if (!row.entries.some(e => e.id === entryId)) return row;
      touched = true;
      return Object.freeze({...row, entries: Object.freeze(row.entries.filter(e => e.id !== entryId)), total: Math.max(0, row.total - 1)});
    });
    if (!touched) return () => {};
    const after = Object.freeze({...before, rows: Object.freeze(rows)});
    this.publish({document: after});
    return () => {
      if (!this.ownerCurrent()) return;
      if (this.state.document === after) this.publish({document: before});
      else void this.revalidate().catch(error => {
        // Retirement is terminal and already fenced synchronously; no fallback.
        if (error instanceof HomeAuthorityRetired) return;
      });
    };
  }

  dispose(): void {
    this.disposed = true;
    this.manifest?.controller.abort();
    this.advanceQuiet();
    this.hydrationGeneration++;
    for (const controller of this.hydrations) controller.abort();
    this.hydrations.clear();
    for (const timer of this.rowRetries) clearTimeout(timer);
    this.rowRetries.clear();
    this.waiting.clear();
    this.generation++;
    this.reading?.controller.abort();
    for (const page of this.pages.values()) page.abort();
    this.pages.clear();
    this.listeners.clear();
  }
}
