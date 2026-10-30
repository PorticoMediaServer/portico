/** Full deadlines for the opt-in interest path only; legacy retry caps stay separate. */
export const positiveDeadline = (value: unknown): number => typeof value === 'number' && value > 0
  ? Math.min(value, Number.MAX_VALUE) : 0;
export const addDeadline = (at: number, wait: number): number => Math.min(Number.MAX_VALUE, at + wait);
export function serverDeadline(error: unknown, now: number): number {
  const e = error as {retryAfterMs?: unknown; retryAfterSeconds?: unknown} | null;
  const wait = Math.max(positiveDeadline(e?.retryAfterMs), positiveDeadline(typeof e?.retryAfterSeconds === 'number' ? e.retryAfterSeconds * 1000 : 0));
  return wait ? addDeadline(now, wait) : 0;
}
