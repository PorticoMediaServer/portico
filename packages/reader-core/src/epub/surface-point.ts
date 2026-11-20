/** iOS's back gesture starts within this many points of the screen's left edge. The paginator leaves such a touch alone. */
export const READER_EDGE = 20;

/**
 * Where a pointer in a chapter document sits across the reader's own box on screen, 0 at its left edge and 1 at its
 * right. A chapter iframe can be as wide as all its pages and scrolled under the reader (WebKit), so the event's clientX
 * and the iframe's innerWidth are not the screen's: the iframe's own on-screen left brings the point back to the screen.
 */
export function surfacePoint(clientX: number, frameLeft: number, hostLeft: number, hostWidth: number): Readonly<{x: number; fraction: number}> {
  const x = clientX + frameLeft - hostLeft;
  return {x, fraction: hostWidth > 0 ? Math.max(0, Math.min(1, x / hostWidth)) : 0};
}
