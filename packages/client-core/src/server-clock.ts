/**
 * Product decision "No client-clock requirements" (planning/DECISIONS.md): the app never refuses
 * to work because this device's clock is wrong. An instant a Portico server or the account service
 * issued (an expiry, a horizon, a deadline) is never compared with this device's wall clock.
 *
 * Instead each origin's clock is estimated from its own answers: every response's `Date` header
 * (or a `serverTime` the body carries) gives the offset between that origin's clock and this
 * device's, taken at the midpoint of the exchange. `serverNow(origin)` is this device's clock plus
 * that offset, so a device a year ahead or behind still reads a server's expiry the way the server
 * does. The final word stays with the server: a client still attempts, and renews on the server's
 * own 401 or expired answer.
 *
 * Where nothing has been heard from an origin yet, the most recent offset from any Portico origin
 * stands in (a server and the account service keep real time; it is the device that drifts), and
 * before any answer at all the offset is zero.
 */

type Observation = Readonly<{offset: number; at: number}>;
const clocks = new Map<string, Observation>();
let latest: Observation | undefined;
let persisted: ((offset: number) => void) | undefined;

/** The origin a URL or base URL belongs to, or '' when it has none. */
export function clockOrigin(url: string | URL | undefined): string {
  if (!url) return '';
  try { return new URL(String(url)).origin; } catch { return ''; }
}

/** Parse an HTTP-date (RFC 9110) or an ISO instant into epoch milliseconds. */
export function parseServerTime(value: string | number | null | undefined): number | undefined {
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : undefined;
  if (typeof value !== 'string' || !value.trim()) return undefined;
  const ms = Date.parse(value);
  return Number.isFinite(ms) && ms > 0 ? ms : undefined;
}

/**
 * Record what an origin said its time was. `sentAt` and `receivedAt` are this device's clock at
 * the start and end of the exchange, and `resolutionMs` how finely the time was reported (an HTTP
 * `Date` is whole seconds). The server's true time lies somewhere in that window, so the offset
 * lies in a known range: when zero is inside it the clocks agree and the offset is 0 (no
 * sub-second jitter from a truncated header); otherwise it is the nearest end of the range.
 */
export function observeServerTime(origin: string, serverTime: string | number | null | undefined, sentAt: number, receivedAt: number = Date.now(), resolutionMs = 0): void {
  const server = parseServerTime(serverTime);
  if (server === undefined || !Number.isFinite(sentAt) || !Number.isFinite(receivedAt) || receivedAt < sentAt) return;
  const low = server - receivedAt, high = server + Math.max(0, resolutionMs) - sentAt;
  const offset = low <= 0 && high >= 0 ? 0 : low > 0 ? low : high;
  const observation = Object.freeze({offset: Math.round(offset), at: receivedAt});
  if (origin) clocks.set(origin, observation);
  latest = observation;
  persisted?.(observation.offset);
}

/** Record a response's `Date` header. Headers a browser hides (cross-origin, unexposed) read as null and teach nothing. */
export function observeResponseClock(origin: string, response: Readonly<{headers?: {get(name: string): string | null}}> | undefined, sentAt: number, receivedAt: number = Date.now()): void {
  let date: string | null | undefined;
  try { date = response?.headers?.get('Date'); } catch { date = undefined; }
  observeServerTime(origin, date, sentAt, receivedAt, 1000);
}

/** This device's clock minus the origin's, in milliseconds (0 when nothing is known). */
export function serverClockOffset(origin?: string): number {
  return (origin ? clocks.get(origin) : undefined)?.offset ?? latest?.offset ?? 0;
}

/** The origin's current time as best this device knows it. Use this, never `Date.now()`, against a server-issued instant. */
export function serverNow(origin?: string): number {
  return Date.now() + serverClockOffset(origin);
}

/** A `now()` bound to one origin, for code that takes an injectable clock. */
export function serverClockFor(origin?: string): () => number {
  return () => serverNow(origin);
}

/**
 * Restore the last offset this device learned (for work that must judge a stored instant before
 * any server has answered, such as an offline download), and keep it saved as it changes.
 */
export function restoreServerClock(saved: number | undefined, save?: (offset: number) => void): void {
  if (latest === undefined && typeof saved === 'number' && Number.isFinite(saved)) latest = Object.freeze({offset: Math.round(saved), at: Date.now()});
  persisted = save;
}

/** Tests only. */
export function resetServerClocks(): void { clocks.clear(); latest = undefined; persisted = undefined; }
