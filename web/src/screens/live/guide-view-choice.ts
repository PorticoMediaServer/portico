/** A stored or explicit choice wins; first use follows the current orientation. */
export function guideViewChoice(explicit: 'guide' | 'list' | undefined, remembered: 'guide' | 'list' | undefined, width: number, height: number): 'guide' | 'list' {
  return explicit ?? remembered ?? (height > width ? 'list' : 'guide');
}
