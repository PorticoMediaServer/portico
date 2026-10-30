/**
 * Rate-limit retries for guide loads (M25-2): a single `429 Too Many Requests`
 * while loading one guide block must not break that block until Try again.
 * Honour `Retry-After` (seconds or HTTP-date) with bounded backoff and jitter,
 * retrying at most a few times before surfacing the error.
 */

/** True when the failure is a rate limit (HTTP 429). */
export function isRateLimited(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as {status?: unknown}).status === 429;
}

function headerValue(headers: unknown): string | number | undefined {
  if (!headers || typeof headers !== 'object') return undefined;
  const h = headers as Record<string, unknown>;
  for (const key of ['Retry-After', 'retry-after', 'RETRY-AFTER']) {
    const v = h[key];
    if (typeof v === 'string' || typeof v === 'number') return v;
  }
  return undefined;
}

/**
 * Parses a `Retry-After` value (seconds or HTTP-date) to milliseconds.
 * Numbers are seconds. Numeric strings are seconds. Other strings are parsed
 * as HTTP-dates (ms until then, clamped at zero). Returns undefined when the
 * value carries no usable delay. Clamped to at most 30 s so a faulty server
 * cannot stall the guide.
 */
export function parseRetryAfter(value: unknown, now: number = Date.now()): number | undefined {
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value < 0) return undefined;
    return Math.min(value * 1000, 30_000);
  }
  if (typeof value !== 'string' || !value) return undefined;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) return Math.min(Number(trimmed) * 1000, 30_000);
  const time = Date.parse(trimmed);
  if (!Number.isFinite(time)) return undefined;
  return Math.min(Math.max(time - now, 0), 30_000);
}

/** Extracts the server-directed retry delay (ms) from a 429 failure, if any. */
export function retryAfterMs(error: unknown, now: number = Date.now()): number | undefined {
  if (!isRateLimited(error)) return undefined;
  const e = error as {retryAfterSeconds?: unknown; retryAfter?: unknown; headers?: unknown};
  return parseRetryAfter(e.retryAfterSeconds, now)
    ?? parseRetryAfter(e.retryAfter, now)
    ?? parseRetryAfter(headerValue(e.headers), now);
}

/**
 * Delay before the next attempt (ms): the server's `Retry-After` when present
 * (clamped, plus a small jitter so recovering clients spread out), otherwise
 * exponential backoff with jitter (500 ms, 1 s, 2 s … capped at 10 s).
 * `attempt` is 1-based (first retry = 1).
 */
export function rateLimitDelay(error: unknown, attempt: number, random: () => number = Math.random): number {
  const directed = retryAfterMs(error);
  if (directed !== undefined) return Math.min(directed + Math.floor(random() * 250), 30_000);
  const backoff = Math.min(500 * 2 ** Math.max(attempt - 1, 0), 10_000);
  return backoff + Math.floor(random() * 250);
}

/** Maximum rate-limit attempts per block (initial try + retries). */
export const GUIDE_RATE_LIMIT_ATTEMPTS = 3;
