/**
 * Whether downloads may still play without reaching the server: the 30 days the server gives at
 * every contact (`POST /v1/downloads/sync`), judged on the device by a clock that cannot be
 * turned back.
 *
 * The time used is the server's, as the device last learned it: at a contact the device keeps
 * how far its own clock is from the server's, and from then on reads its clock plus that
 * difference. A clock wrongly set ahead or behind before the contact therefore does not matter.
 * Turning the clock back afterwards does not give more time either: the device also keeps the
 * latest time it has ever judged by (the floor), and time never goes below it. The device keeps
 * this one small record in its own storage and saves it after each judgement that moved it.
 */
import type {OfflinePermission} from './device-downloads.ts';

/** `offsetMs` is the server's clock minus this device's at the last contact; `floorMs` is the
 * latest server time judged by. Both are in the server's time. */
export type OfflineClock = Readonly<{offsetMs: number; floorMs: number}>;
export const freshOfflineClock: OfflineClock = Object.freeze({offsetMs: 0, floorMs: 0});

const finite = (value: unknown): number => typeof value === 'number' && Number.isFinite(value) ? value : 0;

/** The clock after a contact: the server's time then was `playableUntil` less `playableForMs`. */
export function clockAtContact(clock: OfflineClock, permission: OfflinePermission): OfflineClock {
  const serverNow = Date.parse(permission.playableUntil) - permission.playableForMs;
  if (!Number.isFinite(serverNow)) return clock;
  // The server's time is the trusted one: a floor raised by a device clock that was wrongly far
  // ahead stops holding downloads back once the device has been online again.
  return Object.freeze({offsetMs: serverNow - permission.receivedAt, floorMs: serverNow});
}

/** The server's time now, as this device judges it, and the clock to keep. */
export function offlineNow(clock: OfflineClock, wallMs: number): Readonly<{nowMs: number; clock: OfflineClock}> {
  const floorMs = finite(clock.floorMs), nowMs = Math.max(floorMs, finite(wallMs) + finite(clock.offsetMs));
  return {nowMs, clock: nowMs === floorMs ? clock : Object.freeze({offsetMs: finite(clock.offsetMs), floorMs: nowMs})};
}

/** Whether downloads under this permission may play now; without one they may not. */
export function mayPlayOffline(permission: OfflinePermission | undefined, clock: OfflineClock, wallMs: number): Readonly<{playable: boolean; clock: OfflineClock}> {
  const judged = offlineNow(clock, wallMs);
  const until = permission ? Date.parse(permission.playableUntil) : NaN;
  return {playable: Number.isFinite(until) && judged.nowMs < until, clock: judged.clock};
}
