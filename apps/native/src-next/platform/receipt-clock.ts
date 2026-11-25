/**
 * The time a download's receipt expiry is judged by (B-14.21). A receipt carries the server's own
 * `expiresAt`; comparing it with the device's wall clock alone lets a clock turned back keep a
 * download playable offline forever. So the device keeps a floor that only moves forward:
 *
 * - every time it judges a receipt, the floor rises to the wall clock if that is later;
 * - whenever the server answers with a receipt (issued or renewed), the floor is set to the
 *   server's own `issuedAt`: the server's time is the trusted one, and a device clock that was
 *   wrongly far ahead stops holding receipts hostage once it has been online again.
 *
 * The time used is the later of the wall clock and the floor. No React and no native code.
 */
export type ReceiptClock = Readonly<{floorMs: number}>;

/** The time to judge expiry by, and the floor to keep (unchanged when the wall clock is behind it). */
export function receiptTime(clock: ReceiptClock, wallMs: number): Readonly<{nowMs: number; floorMs: number}> {
  const floor = Number.isFinite(clock.floorMs) && clock.floorMs > 0 ? clock.floorMs : 0;
  const wall = Number.isFinite(wallMs) ? wallMs : 0;
  const nowMs = Math.max(floor, wall);
  return {nowMs, floorMs: nowMs};
}

/** The floor after the server answered with receipts issued at these instants (ISO strings). */
export function serverReceiptFloor(clock: ReceiptClock, issuedAt: readonly string[]): number {
  const server = Math.max(0, ...issuedAt.map(text => Date.parse(text)).filter(Number.isFinite));
  return server > 0 ? server : clock.floorMs;
}
