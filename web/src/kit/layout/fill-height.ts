/**
 * The height a self-scrolling grid takes to fill the screen below its own top edge, stopping where
 * the shell's fixed bottom chrome begins. The phone tab bar (and the mini player dock above it)
 * float over the page, and the shell reserves their height as its own bottom padding, so a grid
 * sized to `innerHeight - top` would end underneath them and its last row could never scroll clear.
 * The shell marks itself with `data-shell-root`; outside it (a gallery, a test) nothing is reserved.
 */
const MINIMUM = 280;

/** The shell element whose bottom padding is the fixed bottom chrome's clearance, if `element` is inside one. */
export function shellRootOf(element: Element): HTMLElement | null {
  return element.closest<HTMLElement>('[data-shell-root]');
}

/** Pixels at the viewport's bottom that fixed shell chrome covers (tab bar plus its safe area, and the dock). */
export function shellBottomClearance(element: Element): number {
  const root = shellRootOf(element);
  if (!root) return 0;
  const padding = Number.parseFloat(getComputedStyle(root).paddingBottom);
  return Number.isFinite(padding) ? padding : 0;
}

export function fillHeightBelow(element: HTMLElement, minimum = MINIMUM): number {
  return Math.max(minimum, window.innerHeight - element.getBoundingClientRect().top - shellBottomClearance(element));
}

/** Calls `onChange` when the clearance can change without `element` itself resizing (the dock appearing or the navigation switching). */
export function observeShellClearance(element: Element, onChange: () => void): () => void {
  const root = shellRootOf(element);
  if (!root || typeof ResizeObserver === 'undefined') return () => {};
  const observer = new ResizeObserver(onChange);
  observer.observe(root, {box: 'border-box'});
  return () => observer.disconnect();
}
