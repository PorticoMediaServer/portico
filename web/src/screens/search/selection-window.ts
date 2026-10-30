export type SearchShelfRange = Readonly<{count: number; first: number; last: number}>;

/** Only an observed shelf viewport is a registration window. The count witnesses
 * the measured item array; a successor layout must report its own range. */
export function displayedSearchEntries<T>(items: readonly T[], range: SearchShelfRange | undefined, intersecting: boolean): readonly T[] {
  if (!intersecting || !range || range.count !== items.length || range.first < 0 || range.last < range.first) return [];
  return items.slice(range.first, Math.min(items.length, range.last + 1));
}
