/**
 * Rate-limit retries for guide loads (M25-2): a single `429 Too Many Requests`
 * while loading one guide block must not break that block until Try again.
 * Honour `Retry-After` (seconds or HTTP-date) with bounded backoff and jitter,
 * retrying at most a few times before surfacing the error.
 */
import {backoffDelay, parseRetryAfter as parseHeaderRetryAfter, type Backoff} from '../timer.ts';

const rateLimitBackoff: Backoff = {baseMs: 500, maxMs: 10_000, jitter: 'none'};

/** True when the failure is a rate limit (HTTP 429). */
export function isRateLimited(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as {status?: unknown}).status === 429;
}

function headerValue(headers: unknown): string | number | undefined {
  if (!headers || typeof headers !== 'object') return undefined;
  for (const [key, v] of Object.entries(headers)) {
    if (key.toLowerCase() === 'retry-after' && (typeof v === 'string' || typeof v === 'number')) return v;
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
  const ms = parseHeaderRetryAfter(trimmed, now);
  if (ms !== undefined) return Math.min(ms, 30_000);
  // The helper reports no floor for zero, a past date or a number past safe integers; here the first two mean "no wait".
  if (/^\d+$/.test(trimmed)) return /^0+$/.test(trimmed) ? 0 : 30_000;
  return /[a-z]/i.test(trimmed) && Number.isFinite(Date.parse(trimmed)) ? 0 : undefined;
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
  return backoffDelay(rateLimitBackoff, attempt) + Math.floor(random() * 250);
}

/** Maximum rate-limit attempts per block (initial try + retries). */
export const GUIDE_RATE_LIMIT_ATTEMPTS = 3;
