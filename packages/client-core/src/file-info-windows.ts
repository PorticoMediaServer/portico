import {
  readPreparation,
  type PreparationRequestPort, type PreparationResponse, type PreparationRuntime, type PreparingRead,
} from './read-preparation.ts';

/** Every FileInfo lane window's continuation facts. One opening keeps one `basis` through
 * every continuation; a changed basis is a new opening, never a silently merged page. */
export type FileInfoWindowFacts = Readonly<{
  queryId: string; basis: string; limit: number; returned: number;
  next?: string; previous?: string; reload?: string;
}>;
export type FileInfoWindowRead<Page> = Readonly<{
  page: Page;
  /** The server could not continue the old window and the same opening was read again from
   * its start. Callers drop every resident row of this lane before showing the page. */
  reopened: boolean;
}>;

export class FileInfoWindowError extends Error {
  readonly code: 'invalid_file_info_window' | 'file_info_continuation_unavailable' | 'stale_continuation';
  readonly response?: PreparationResponse;
  constructor(code: FileInfoWindowError['code'], response?: PreparationResponse) {
    super(code);
    this.code = code;this.response = response;
    this.name = 'FileInfoWindowError';
  }
}

export type FileInfoWindowSessionOptions<Read, Page> = Readonly<{
  api: PreparationRequestPort;
  /** The lane's first window for `read`, or the window a signed cursor names. */
  path(read: Read, cursor?: string): string;
  poll(queryId: string): string;
  cancel(queryId: string): string;
  /** Validate the complete envelope and bounded rows of one ready window. */
  parse(body: unknown, read: Read, response: PreparationResponse): Page;
  window(page: Page): FileInfoWindowFacts;
  parsePreparing(body: unknown, response: PreparationResponse): PreparingRead;
  /** The handle of any 200 reply, validated without parsing rows, so late replies are released. */
  readyQueryId(body: unknown, response: PreparationResponse): string | undefined;
  errorFromResponse(response: PreparationResponse): Error;
  isCurrent(): boolean;
  signal?: AbortSignal;
  runtime?: Partial<PreparationRuntime>;
  onPreparing?(pending: PreparingRead): void;
}>;

type Operation = {controller: AbortController; detach(): void; release?: () => void; predecessor?: Operation};
const cancelled = () => Object.assign(new Error('file-info-read-cancelled'), {name: 'AbortError'});

/** One lane of one FileInfo opening: one operation and one held window, never a history.
 * Navigation is explicit, never a cursor-draining loop. The held window's lease is kept only
 * so its signed anchors stay valid; it is released when the next window has been read, on
 * cancel and on retirement. Renderers bound their own resident rows (see FileInfoRows). */
export class FileInfoWindowSession<Read, Page> {
  private readonly options: FileInfoWindowSessionOptions<Read, Page>;
  private operation?: Operation;
  private original?: Read;
  private held?: FileInfoWindowFacts;
  private closed = false;
  private readonly onExternalAbort = () => this.retire();

  constructor(options: FileInfoWindowSessionOptions<Read, Page>) {
    this.options = options;
    options.signal?.addEventListener('abort', this.onExternalAbort, {once: true});
    if (options.signal?.aborted) this.retire();
  }
  private live(): boolean { return !this.closed && !this.options.signal?.aborted && this.options.isCurrent(); }
  private check(): void { if (!this.live()) { this.retire(); throw cancelled(); } }
  get window(): FileInfoWindowFacts | undefined { return this.live() ? this.held : undefined; }
  private clear(): void { const old = this.operation; this.operation = undefined; this.held = undefined; this.dispose(old); }
  private dispose(operation: Operation | undefined): void {
    if (!operation) return;
    const predecessor = operation.predecessor;
    operation.predecessor = undefined;
    operation.detach(); operation.controller.abort(); operation.release?.();
    this.dispose(predecessor);
  }
  private releasePredecessor(operation: Operation): void {
    const predecessor = operation.predecessor;
    operation.predecessor = undefined;
    this.dispose(predecessor);
  }
  /** Drop current interest, rows and anchors; a later open may reuse this session. */
  cancel(): void { this.clear(); }
  /** Retire permanently when the panel, opening, viewer or server goes away. */
  retire(): void {
    this.closed = true;
    this.clear();
    this.original = undefined;
    this.options.signal?.removeEventListener('abort', this.onExternalAbort);
  }
  open(read: Read, signal?: AbortSignal): Promise<FileInfoWindowRead<Page>> {
    this.check();
    this.clear();
    this.original = read;
    return this.start(read, undefined, signal);
  }
  next(signal?: AbortSignal): Promise<FileInfoWindowRead<Page>> { return this.navigate('next', signal); }
  previous(signal?: AbortSignal): Promise<FileInfoWindowRead<Page>> { return this.navigate('previous', signal); }
  reload(signal?: AbortSignal): Promise<FileInfoWindowRead<Page>> { return this.navigate('reload', signal); }
  private navigate(direction: 'next' | 'previous' | 'reload', signal?: AbortSignal): Promise<FileInfoWindowRead<Page>> {
    this.check();
    const token = this.held?.[direction];
    if (!this.original || !token) return Promise.reject(new FileInfoWindowError('file_info_continuation_unavailable'));
    // A continuation token names the old handle: keep that lease until the server has
    // consumed the token, then release it. Deleting it first would force a stale reply.
    const predecessor = this.operation, basis = this.held!.basis;
    this.operation = undefined; this.held = undefined;
    return this.start(this.original, {token, basis, predecessor}, signal);
  }
  private async start(read: Read, continuation: Readonly<{token: string; basis: string; predecessor?: Operation}> | undefined, signal?: AbortSignal): Promise<FileInfoWindowRead<Page>> {
    const predecessor = continuation?.predecessor;
    if (signal?.aborted) { this.dispose(predecessor); throw cancelled(); }
    const controller = new AbortController();
    const operation: Operation = {controller, predecessor, detach: () => signal?.removeEventListener('abort', onAbort)};
    const onAbort = () => { if (this.operation === operation) this.cancel(); else controller.abort(); };
    this.operation = operation;
    signal?.addEventListener('abort', onAbort, {once: true});
    const current = () => this.live() && this.operation === operation && !controller.signal.aborted;
    let reopened = false;
    try {
      const result = await readPreparation<Read, Page>({
        api: this.options.api, descriptor: read,
        paths: {
          initial: value => this.options.path(value, continuation?.token),
          // One stale reply re-reads the same opening from its start: never the old token.
          refresh: value => { reopened = true; this.releasePredecessor(operation); return this.options.path(value); },
          poll: id => this.options.poll(id), cancel: id => this.options.cancel(id),
        },
        parseReady: (body, response) => {
          const page = this.options.parse(body, read, response);
          if (continuation && !reopened && this.options.window(page).basis !== continuation.basis) throw new FileInfoWindowError('stale_continuation', response);
          return page;
        },
        readyQueryId: (body, response) => { const id = this.options.readyQueryId(body, response); this.releasePredecessor(operation); return id; },
        parsePreparing: (body, response) => { const pending = this.options.parsePreparing(body, response); this.releasePredecessor(operation); return pending; },
        retainOnReady: true,
        errorFromResponse: this.options.errorFromResponse,
        signal: controller.signal, isCurrent: current, runtime: this.options.runtime,
        onPreparing: pending => this.options.onPreparing?.(pending),
      });
      if (!current()) { result.release(); throw cancelled(); }
      operation.release = result.release;
      this.held = this.options.window(result.value);
      return Object.freeze({page: result.value, reopened});
    } catch (error) {
      if (this.operation === operation) this.clear();
      else this.dispose(operation);
      if (controller.signal.aborted && !current() && (error as {name?: string} | null)?.name === 'AbortError') throw cancelled();
      throw error;
    }
  }
}

/** A lane's resident rows, appended window by window and bounded. Past the bound the earliest
 * rows are dropped and `earlier` says so: the renderer offers the lane from its start again
 * (FileInfoWindowSession.open with the original read), so nothing is ever unreachable. */
export type FileInfoRows<Row> = Readonly<{rows: readonly Row[]; more: boolean; earlier: boolean}>;
export const FILE_INFO_RESIDENT_ROWS = 200;
export function fileInfoRows<Row>(current: FileInfoRows<Row> | undefined, rows: readonly Row[], window: FileInfoWindowFacts, mode: 'replace' | 'append' | 'prepend', limit = FILE_INFO_RESIDENT_ROWS): FileInfoRows<Row> {
  if (!current || mode === 'replace') return Object.freeze({rows: Object.freeze(rows.slice(0, limit)), more: !!window.next, earlier: !!window.previous});
  if (mode === 'append') {
    const all = [...current.rows, ...rows], dropped = Math.max(0, all.length - limit);
    return Object.freeze({rows: Object.freeze(all.slice(dropped)), more: !!window.next, earlier: current.earlier || dropped > 0});
  }
  const all = [...rows, ...current.rows], dropped = Math.max(0, all.length - limit);
  return Object.freeze({rows: Object.freeze(all.slice(0, all.length - dropped)), more: current.more || dropped > 0, earlier: !!window.previous});
}
