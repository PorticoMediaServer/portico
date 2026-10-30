import type {HomeRow} from '../../../../../packages/client-core/src/home';

/** E-mobile-29: when Home reads itself again for its live rows. It was re-read every 1.5–2.5 s,
 * unbounded: the timer used the device clock against the server's expiry, a near or past expiry
 * fired at once, and every new document scheduled again.
 * - Time left is measured on the server's own clock (expiresAt − observedAt), never the device's.
 * - Never sooner than 30 s, with up to 5 s of jitter so clients spread out.
 * - A re-read that brings back the same or an already-expired expiry backs off exponentially
 *   (60 s, 120 s ... 10 min), so one expiry is never read twice in a row on the same schedule. */
export const LIVE_REREAD_MIN_MS = 30_000;
export const LIVE_REREAD_MAX_MS = 10 * 60_000;
export const LIVE_REREAD_JITTER_MS = 5_000;

export type LiveRereadMemory = Readonly<{expiry?: string; stalls: number}>;
export type LiveRereadPlan = Readonly<{expiry: string; stalls: number; delayMs: number}>;

export function liveRereadPlan(rows: readonly HomeRow[], memory: LiveRereadMemory, random: () => number = Math.random): LiveRereadPlan | undefined {
  let soonest: {expiry: string; remaining: number} | undefined;
  for (const row of rows) {
    if (!row.live) continue;
    const remaining = Date.parse(row.live.expiresAt) - Date.parse(row.live.observedAt);
    if (!Number.isFinite(remaining)) continue;
    if (!soonest || remaining < soonest.remaining) soonest = {expiry: row.live.expiresAt, remaining};
  }
  if (!soonest) return undefined;
  const stalled = soonest.expiry === memory.expiry || soonest.remaining <= 0;
  const stalls = stalled ? memory.stalls + 1 : 0;
  const base = stalled ? Math.min(LIVE_REREAD_MAX_MS, LIVE_REREAD_MIN_MS * 2 ** stalls) : Math.min(LIVE_REREAD_MAX_MS, Math.max(LIVE_REREAD_MIN_MS, soonest.remaining));
  return Object.freeze({expiry: soonest.expiry, stalls, delayMs: base + Math.floor(Math.max(0, Math.min(1, random())) * LIVE_REREAD_JITTER_MS)});
}
