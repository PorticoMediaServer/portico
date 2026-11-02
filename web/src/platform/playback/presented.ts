/** A playback start failure the player already shows (J-17). The start still rejects for its
 * caller, but the shell's toast stays quiet: the reason is drawn in the player, once. */
const presented = new WeakSet<object>();
export function markPresentedInPlayer<T>(error: T): T {
  if (error && typeof error === 'object') presented.add(error);
  return error;
}
export function presentedInPlayer(error: unknown): boolean {
  return !!error && typeof error === 'object' && presented.has(error);
}
