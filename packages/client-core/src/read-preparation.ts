/** In-memory read preparation only: no result cache, cursor history or mutation retries. */
export type PreparationResponse = Readonly<{
  status: number;
  headers: Readonly<Record<string, string>>;
  body?: unknown;
}>;

/** Structurally compatible with HttpLocalApi's existing raw auth/deadline transport. */
export interface PreparationRequestPort {
  requestRaw(path: string, method: string, options?: Readonly<{
    signal?: AbortSignal;
    singleAttempt?: boolean;
  }>): Promise<PreparationResponse>;
}

export type PreparingRead = Readonly<{queryId: string; retryAfterMs: number}>;
export type PreparationIntermediateReady<Value> = Readonly<{pending: PreparingRead; value: Value}>;
export type PreparationRuntime = Readonly<{
  now(): number;
  random(): number;
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(timer: unknown): void;
}>;

export type ReadPreparationOptions<Descriptor, Ready, Intermediate = never> = Readonly<{
  api: PreparationRequestPort;
  /** Capture an immutable descriptor; refresh must re-anchor without stale row tokens. */
  descriptor: Descriptor;
  paths: Readonly<{
    initial(descriptor: Descriptor): string;
    refresh(descriptor: Descriptor): string;
    poll(queryId: string, descriptor: Descriptor): string;
    cancel(queryId: string, descriptor: Descriptor): string;
  }>;
  parseReady(body: unknown, response: PreparationResponse): Ready;
  /** Optional domain-specific 200 intermediate union member. Undefined leaves the ordinary
   * ready path unchanged. Validate its complete envelope and bounded rows before returning.
   * The pending handle remains stable through 202/intermediate/final-ready responses. */
  parseIntermediateReady?(body: unknown, response: PreparationResponse): PreparationIntermediateReady<Intermediate> | undefined;
  /** Transient synchronous publication after scope/handle/path/retry validation. The helper
   * drops intermediate value/raw references before waiting and continues to final ready. */
  onIntermediateReady?(value: Intermediate, response: PreparationResponse): void;
  /** Validate/extract a handle from the domain's raw 200 union, including intermediate/late replies.
   * Required when ready owns a handle even without a preceding 202. No ready rows need be
   * parsed for cleanup. Undefined means no additional handle. */
  readyQueryId?(body: unknown, response: PreparationResponse): string | undefined;
  /** Default false releases a known handle on ready. Continuation sessions must opt in
   * and own the returned release() lease, retaining at most current + neighbor pages. */
  retainOnReady?: boolean;
  /** Required domain-specific envelope validation, including kind/state where published. */
  parsePreparing(body: unknown, response: PreparationResponse): PreparingRead;
  /** Preserve existing typed errors; all terminal statuses, including 429/503, come here. */
  errorFromResponse(response: PreparationResponse): Error;
  /** Abort on retirement/supersession for prompt cancellation, including during a request. */
  signal?: AbortSignal;
  /** Additional publication guard, checked at every asynchronous and callback boundary. */
  isCurrent(): boolean;
  onPreparing?(pending: PreparingRead, response: PreparationResponse): void;
  runtime?: Partial<PreparationRuntime>;
}>;

export class PreparationProtocolError extends Error {
  readonly code: 'invalid_path' | 'invalid_preparing' | 'invalid_ready_handle' | 'unexpected_location' | 'invalid_retry_after';
  readonly response?: PreparationResponse;
  constructor(code: PreparationProtocolError['code'], response?: PreparationResponse) {
    super(code);
    this.code = code;this.response = response;
    this.name = 'PreparationProtocolError';
  }
}

const TIMER_MAX_MS = 2_147_483_647;
const BASE_BACKOFF_MS = 250, MAX_BACKOFF_MS = 30_000;
const runtimeDefaults: PreparationRuntime = {
  now: () => Date.now(), random: () => Math.random(),
  setTimeout: (callback, delay) => setTimeout(callback, delay),
  clearTimeout: timer => clearTimeout(timer as ReturnType<typeof setTimeout>),
};

function header(response: PreparationResponse, name: string): string | undefined {
  for (const key of Object.keys(response.headers)) {
    if (key.toLowerCase() === name) return response.headers[key];
  }
  return undefined;
}

/** Only caller-generated server-relative paths; URL normalization must not change them. */
function checkedPath(path: string, response?: PreparationResponse): string {
  if (typeof path !== 'string' || !path.startsWith('/') || path.startsWith('//') || /[\\\s#\x00-\x1f\x7f]/.test(path)) {
    throw new PreparationProtocolError('invalid_path', response);
  }
  const parsed = new URL(path, 'https://read-preparation.invalid');
  if (parsed.origin !== 'https://read-preparation.invalid' || parsed.pathname + parsed.search !== path) {
    throw new PreparationProtocolError('invalid_path', response);
  }
  return path;
}

function validQueryId(queryId: unknown): queryId is string {
  return typeof queryId === 'string' && /^[A-Za-z0-9._~-]{1,512}$/.test(queryId) && queryId !== '.' && queryId !== '..';
}

function checkedPending(pending: PreparingRead | undefined, response: PreparationResponse): PreparingRead {
  if (!pending || !validQueryId(pending.queryId)
      || !Number.isSafeInteger(pending.retryAfterMs) || pending.retryAfterMs < 0) {
    throw new PreparationProtocolError('invalid_preparing', response);
  }
  return {queryId: pending.queryId, retryAfterMs: pending.retryAfterMs};
}

function retryFloor(response: PreparationResponse, now: number): number {
  const value = header(response, 'retry-after')?.trim();
  if (!value) return 0;
  if (/^\d+$/.test(value)) {
    const ms = Number(value) * 1000;
    // An unrepresentable floor must fail closed, never become a short local retry.
    if (!Number.isSafeInteger(ms)) throw new PreparationProtocolError('invalid_retry_after', response);
    return ms;
  }
  // Avoid Date.parse treating malformed numeric delay strings (e.g. "1.5") as dates.
  const at = /^[A-Za-z]{3,9}(?:,|\s)/.test(value) ? Date.parse(value) : NaN;
  return Number.isFinite(at) ? Math.max(0, Math.ceil(at - now)) : 0;
}

type Handle = {queryId: string; cancelled: boolean};

/**
 * Read one page. 200 is ready unless the optional intermediate adapter recognizes it;
 * intermediates and 202 schedule one poll at a time. One 409 re-anchors
 * the original descriptor, then any further refusal is surfaced through errorFromResponse.
 * Ready values/raw metadata are returned directly, never retained in a session/cache. A
 * continuation owner opts into retainOnReady and releases its lease on page retirement.
 */
export async function readPreparation<Descriptor, Ready, Intermediate = never>(
  options: ReadPreparationOptions<Descriptor, Ready, Intermediate>,
): Promise<Readonly<{value: Ready; response: PreparationResponse; release(): void}>> {
  const {api, descriptor, paths} = options;
  const supplied = options.runtime;
  const runtime: PreparationRuntime = {
    now: supplied?.now?.bind(supplied) ?? runtimeDefaults.now,
    random: supplied?.random?.bind(supplied) ?? runtimeDefaults.random,
    setTimeout: supplied?.setTimeout?.bind(supplied) ?? runtimeDefaults.setTimeout,
    clearTimeout: supplied?.clearTimeout?.bind(supplied) ?? runtimeDefaults.clearTimeout,
  };
  const controller = new AbortController();
  let handle: Handle | undefined;
  let keepAbortListener = false;
  const abortError = new Error('Read preparation aborted.');
  abortError.name = 'AbortError';

  function release(owned: Handle | undefined): void {
    if (!owned || owned.cancelled) return;
    owned.cancelled = true;
    // Cancellation is independent of the aborted read, best effort and never an outbox entry.
    // No publication/path-current check here: retirement is precisely when release is needed.
    try {
      const path = checkedPath(paths.cancel(owned.queryId, descriptor));
      void api.requestRaw(path, 'DELETE', {singleAttempt: true}).catch(() => {});
    } catch { /* Even a synchronous transport/path failure cannot mask the read outcome. */ }
  }

  function abort(): void {
    if (!controller.signal.aborted) {
      controller.abort();
    }
    if (keepAbortListener) releaseLease();
    else release(handle);
  }

  function releaseLease(): void {
    keepAbortListener = false;
    options.signal?.removeEventListener('abort', abort);
    const owned = handle;
    handle = undefined;
    release(owned);
  }

  function current(): void {
    if (options.signal?.aborted || controller.signal.aborted || !options.isCurrent()) {
      abort();
      throw abortError;
    }
  }

  /** Detach each seam's listener: racing a single never-settled abort promise would retain
   * one reaction per poll/chunk for the entire operation. There is only one live seam. */
  function interruptible<T>(work: Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const detach = () => controller.signal.removeEventListener('abort', onAbort);
      const onAbort = () => { detach(); reject(abortError); };
      controller.signal.addEventListener('abort', onAbort, {once: true});
      work.then(value => { detach(); resolve(value); }, error => { detach(); reject(error); });
      // Work may synchronously abort before this listener was installed.
      if (controller.signal.aborted) onAbort();
    });
  }

  /** A retired initial request may still reveal a private handle; only validate/release it. */
  function releaseLate(response: PreparationResponse, requestedHandle: Handle | undefined): void {
    try {
      const queryId = response.status === 202
        ? checkedPending(options.parsePreparing(response.body, response), response).queryId
        : response.status === 200 ? lateReadyId(response) : undefined;
      if (!validQueryId(queryId)) return;
      // A poll cannot replace its handle. Never use response Location for cleanup either.
      if (requestedHandle && requestedHandle.queryId !== queryId) return;
      const owned = requestedHandle ?? (handle?.queryId === queryId ? handle : {queryId, cancelled: false});
      release(owned);
    } catch { /* Unvalidated envelopes cannot authorize any cancellation path. */ }
  }

  function lateReadyId(response: PreparationResponse): string | undefined {
    // A minimal domain extractor avoids parsing/copying intermediate rows during cleanup.
    const explicit = options.readyQueryId?.(response.body, response);
    if (explicit !== undefined) return explicit;
    const intermediate = options.parseIntermediateReady?.(response.body, response);
    return intermediate === undefined ? undefined : checkedPending(intermediate?.pending, response).queryId;
  }

  async function request(path: string): Promise<PreparationResponse> {
    current();
    const requestedHandle = handle;
    const flight = api.requestRaw(checkedPath(path), 'GET', {signal: controller.signal});
    // Observe a late answer even if race() has already returned on abort. No ready parsing.
    const observed = flight.then(response => {
      if (controller.signal.aborted || options.signal?.aborted || !options.isCurrent()) {
        abort();
        releaseLate(response, requestedHandle);
        throw abortError;
      }
      return response;
    });
    const response = await interruptible(observed);
    // Also cover retirement between the response microtask and this continuation.
    try { current(); } catch (error) { releaseLate(response, requestedHandle); throw error; }
    return response;
  }

  async function wait(delayMs: number): Promise<void> {
    let remaining = delayMs;
    while (remaining > 0) {
      current();
      const started = runtime.now();
      const chunk = Math.min(TIMER_MAX_MS, remaining);
      let timer: unknown;
      const elapsed = new Promise<void>(resolve => { timer = runtime.setTimeout(resolve, chunk); });
      try { await interruptible(elapsed); }
      finally { runtime.clearTimeout(timer); }
      current();
      // Recheck the clock rather than trusting an early timer; never truncate a server floor.
      // A forward wall-clock jump cannot count as having waited future timer chunks.
      remaining -= Math.min(chunk, Math.max(0, runtime.now() - started));
    }
  }

  options.signal?.addEventListener('abort', abort, {once: true});
  try {
    current();
    let path = checkedPath(paths.initial(descriptor));
    current();
    let refreshed = false, attempt = 0;
    while (true) {
      let response: PreparationResponse | undefined = await request(path);
      try { current(); } catch (error) { releaseLate(response, handle); throw error; }
      if (response.status === 200) {
        // Capture a validated minimal handle before full row parsing can fail. The extractor
        // covers the domain's whole 200 union, so malformed rows cannot strand its interest.
        const readyId = options.readyQueryId?.(response.body, response);
        if (readyId !== undefined) {
          if (!validQueryId(readyId) || (handle && handle.queryId !== readyId)) {
            throw new PreparationProtocolError('invalid_ready_handle', response);
          }
          handle ??= {queryId: readyId, cancelled: false};
        }
        current();
      }
      let intermediate = response.status === 200 ? options.parseIntermediateReady?.(response.body, response) : undefined;
      if (response.status === 200 && intermediate === undefined) {
        const value = options.parseReady(response.body, response);
        current();
        if (options.retainOnReady && handle) keepAbortListener = true;
        else {
          release(handle);
          current();
          handle = undefined;
        }
        return {value, response, release: releaseLease};
      }
      if (response.status === 409 && !refreshed) {
        release(handle);
        handle = undefined;
        current();
        refreshed = true;
        attempt = 0;
        path = checkedPath(paths.refresh(descriptor), response);
        current();
        continue;
      }
      if (response.status !== 202 && intermediate === undefined) {
        const error = options.errorFromResponse(response);
        current();
        throw error;
      }
      const pending = checkedPending(intermediate !== undefined ? intermediate?.pending : options.parsePreparing(response.body, response), response);
      if (handle && handle.queryId !== pending.queryId) throw new PreparationProtocolError('invalid_preparing', response);
      handle ??= {queryId: pending.queryId, cancelled: false};
      current();
      path = checkedPath(paths.poll(pending.queryId, descriptor), response);
      current();
      const location = header(response, 'location');
      if (location !== undefined && location !== path) throw new PreparationProtocolError('unexpected_location', response);
      const local = Math.min(MAX_BACKOFF_MS, BASE_BACKOFF_MS * 2 ** Math.min(attempt++, 16));
      const floor = Math.max(pending.retryAfterMs, retryFloor(response, runtime.now()), local);
      const random = runtime.random();
      if (!Number.isFinite(random) || random < 0 || random >= 1) throw new RangeError('Invalid preparation random value.');
      const delayMs = floor + 1 + Math.floor(random * Math.max(1, local / 4));
      if (!Number.isSafeInteger(delayMs)) throw new PreparationProtocolError('invalid_retry_after', response);
      current();
      if (intermediate !== undefined) options.onIntermediateReady?.(intermediate.value, response);
      else options.onPreparing?.(pending, response);
      current();
      // In particular, a legal oversized fallback row must not live across this wait.
      intermediate = undefined;
      response = undefined;
      await wait(delayMs);
    }
  } catch (error) {
    release(handle);
    if (options.signal?.aborted || controller.signal.aborted || !options.isCurrent()) {
      abort();
      throw abortError;
    }
    throw error;
  } finally {
    if (!keepAbortListener) options.signal?.removeEventListener('abort', abort);
  }
}
