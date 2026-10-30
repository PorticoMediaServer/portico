import {randomId} from '../random-id.ts';
import {resendableRefusal, retryWriterCapacity} from '../writer-capacity.ts';
/**
 * The HTTP seam for playback v1. Modules speak in `V1Request`/`V1Response` so the same code runs
 * against the real server (an adapter over `HttpLocalApi`/`fetch`, phase 2 wiring) and
 * `FakePlaybackServer`. Non-2xx responses become `PlaybackApiError` with the style guide's error
 * fields (§4.6): `code`, `retry` class, `current` (on 412) and `Retry-After`.
 */
export type V1Request = Readonly<{method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'; path: string; headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal;
  /** Give up after this long (default: the transport's own deadline). */
  deadlineMs?: number;
  /** Send once: no token renewal and resend on a 401, no other retry. A playback observation
   * is replaced by the next one a second later, so resending an old one only adds load. */
  singleAttempt?: boolean}>;
export type V1Response = Readonly<{status: number; headers: Readonly<Record<string, string>>; body?: unknown}>;
export interface V1Http {
  acknowledgeUserMutation?(intentId: string): Promise<void>;
  /** Resolves with any HTTP response; rejects only when there is no response (offline, aborted). */
  send(request: V1Request): Promise<V1Response>;
}

export type RetryClass = 'never' | 'same_request' | 'after_refresh' | 'after_reauth';

export class PlaybackApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retry: RetryClass;
  readonly retryAfterMs?: number;
  /** The resource's current representation, when the server sends it (412, some 409s). */
  readonly current?: unknown;
  /** The server's machine reason, when it gives one: for `connection_too_slow`, the widest fixed
   * quality the connection carries ("720p"). */
  readonly reason?: string;
  get streamLimit(): Readonly<{scope: 'account'; limit: number; playing: number}> | undefined {
   const v=this.current as Record<string,unknown>|undefined;
   return this.code==='stream_limit_reached'&&v?.scope==='account'&&Number.isSafeInteger(v.limit)&&Number(v.limit)>0&&Number.isSafeInteger(v.playing)&&Number(v.playing)>=0?Object.freeze({scope:'account',limit:v.limit as number,playing:v.playing as number}):undefined;
  }
  constructor(status: number, code: string, retry: RetryClass, current?: unknown, retryAfterMs?: number, reason?: string) {
    super(code);
    this.name = 'PlaybackApiError';
    this.status = status;
    this.code = code;
    this.retry = retry;
    this.current = current;
    this.retryAfterMs = retryAfterMs;
    if (reason) this.reason = reason;
  }
}

/** Case-insensitive header read. */
export function header(r: V1Response, name: string): string | undefined {
  const want = name.toLowerCase();
  for (const [k, v] of Object.entries(r.headers)) if (k.toLowerCase() === want) return v;
  return undefined;
}

function retryAfter(r: V1Response, body: {retryAfterMs?: unknown} | undefined): number | undefined {
  const h = header(r, 'Retry-After')?.trim();
  if (h && /^\d+$/.test(h)) return Number(h) * 1000;
  // The other legal form (RFC 9110) is an HTTP-date; a proxy in front of the server may answer with one.
  const at = h ? Date.parse(h) : NaN;
  if (Number.isFinite(at) && at > Date.now()) return Math.ceil((at - Date.now()) / 1000) * 1000;
  return typeof body?.retryAfterMs === 'number' && Number.isSafeInteger(body.retryAfterMs) && body.retryAfterMs > 0 ? body.retryAfterMs : undefined;
}

const defaultRetry = (status: number): RetryClass => (status === 412 || status === 409 ? 'after_refresh' : status === 401 ? 'after_reauth' : status >= 500 || status === 429 ? 'same_request' : 'never');

/**
 * Send and require one of `ok` statuses (default 2xx); anything else throws `PlaybackApiError`. A
 * change the writer refused before it began (`writer_capacity`) is resent after its Retry-After,
 * with the same Idempotency-Key, rather than dropped (`writer-capacity.ts`).
 */
export function call(http: V1Http, request: V1Request, ok?: readonly number[]): Promise<V1Response> {
  return retryWriterCapacity(
    () => callOnce(http, request, ok),
    error => error instanceof PlaybackApiError && resendableRefusal(error.code) && error.retry === 'same_request' ? {retryAfterMs: error.retryAfterMs} : undefined,
    request.signal,
  );
}

/** `call` without the writer-capacity resend: for requests that must never be sent twice. */
export async function callOnce(http: V1Http, request: V1Request, ok?: readonly number[]): Promise<V1Response> {
  const r = await http.send(request);
  const good = ok ? ok.includes(r.status) : r.status >= 200 && r.status < 300;
  if (good) return r;
  const e = (r.body as {error?: {code?: unknown; retry?: unknown; current?: unknown; retryAfterMs?: unknown; reason?: unknown}} | undefined)?.error;
  const code = typeof e?.code === 'string' ? e.code : r.status === 404 ? 'not_found' : 'request_failed';
  const retry = (['never', 'same_request', 'after_refresh', 'after_reauth'] as const).includes(e?.retry as RetryClass) ? e!.retry as RetryClass : defaultRetry(r.status);
  const reason = typeof e?.reason === 'string' && e.reason.length <= 128 ? e.reason : undefined;
  throw new PlaybackApiError(r.status, code, retry, e?.current, retryAfter(r, e), reason);
}

/** A fresh `Idempotency-Key` (16–128 chars, `[A-Za-z0-9_-]`, style guide §4.7.3). */
export function idempotencyKey(random: () => string = () => randomId()): string {
  return random().replace(/[^A-Za-z0-9_-]/g, '').slice(0, 128).padEnd(16, '0');
}

export const enc = encodeURIComponent;
