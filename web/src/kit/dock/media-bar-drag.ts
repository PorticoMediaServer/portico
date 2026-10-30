/** The phone dock's sideways drag paint: distance opacity while the finger is down, and
 * an outward deceleration for a committed dismissal. The snap-back keeps the existing
 * PlayerMorph spring; nothing here decides whether a gesture dismisses. */
export const MEDIA_BAR_DISMISS_MS = 200;
export const MEDIA_BAR_SNAP_MS = 500;
export const MEDIA_BAR_REDUCED_MS = 160;

/** Read the paint the browser is actually showing, before cancelling its animation. */
export function paintedX(element: HTMLElement): number {
  const transform = getComputedStyle(element).transform;
  if (!transform || transform === 'none') return 0;
  try { return new DOMMatrixReadOnly(transform).m41; } catch { return 0; }
}
export function paintedOpacity(element: HTMLElement): number {
  const value = parseFloat(getComputedStyle(element).opacity);
  return Number.isFinite(value) ? value : 1;
}

/** The bar fades with the same displacement the finger already owns, never a new clock. */
export function dragOpacity(distancePx: number, width: number): number {
  return Math.max(0, Math.min(1, 1 - Math.abs(distancePx) / Math.max(1, width)));
}

/** Outward offsets decelerate into the edge; the caller keeps its own dismiss target. */
export function dismissOffsets(from: number, target: number, steps = 33): number[] {
  return Array.from({length: steps}, (_, index) => {
    const t = index / (steps - 1), eased = 1 - Math.pow(1 - t, 3);
    return from + (target - from) * eased;
  });
}

/** Opacity frames for the same release; reduced motion keeps only this layer. */
export function releaseOpacity(from: number, to: number, steps = 33): number[] {
  return Array.from({length: steps}, (_, index) => from + (to - from) * index / (steps - 1));
}
