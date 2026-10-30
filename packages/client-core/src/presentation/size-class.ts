/** Layout class in current window CSS pixels (web) or logical points/dp (native). */
export type SizeClass = 'compact' | 'medium' | 'expanded';

/**
 * Classify the live window width, never a device name or physical screen size.
 * Before a valid measurement exists, negative and non-finite widths use compact.
 * Zero is a valid compact startup width. Fractional widths keep exact thresholds.
 */
export function sizeClassForWidth(width: number): SizeClass {
  if (!Number.isFinite(width) || width < 0 || width < 600) return 'compact';
  return width < 1024 ? 'medium' : 'expanded';
}
