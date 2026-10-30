/** FX-075 toast clearance: the one place that turns explicit caller geometry
 * into the distance a toast keeps from the viewport bottom.
 *
 * Every input is a real value the caller already has: the existing bottomOffset
 * (tab bar or dock height, safe area excluded), the safe-area bottom inset, and -
 * only when something actually occupies the bottom of this screen right now - the
 * top of that obstruction measured from the viewport bottom. Nothing here infers a
 * dock from playback, invents an obstruction, or owns the shell context that would
 * supply one. */

/** The 12 pt breathing space the previous host added below its offset. */
export const TOAST_GAP = 12;

export type ToastClearanceInput = Readonly<{
  /** Height of the tab bar or dock, excluding the safe-area inset. */
  bottomOffset: number;
  /** The safe-area bottom inset; counted exactly once. */
  safeBottom: number;
  /** Occupied obstruction's top measured from the viewport bottom, when one exists. */
  obstructionTop?: number;
}>;

const finite = (value: number | undefined): number => value !== undefined && Number.isFinite(value) ? value : 0;

/** The bottom padding a host needs. Without a real obstruction this is the
 * original bottomOffset + safeBottom + 12; with one it clears whichever is larger,
 * so the safe area is still counted once and no gap is ever smaller than before. */
export function toastClearance({bottomOffset, safeBottom, obstructionTop}: ToastClearanceInput): number {
  const base = finite(bottomOffset) + finite(safeBottom);
  // A non-finite or non-positive obstruction is no obstruction: the fallback stays.
  const obstruction = obstructionTop !== undefined && Number.isFinite(obstructionTop) && obstructionTop > 0 ? obstructionTop : undefined;
  return (obstruction === undefined ? base : Math.max(base, obstruction)) + TOAST_GAP;
}
