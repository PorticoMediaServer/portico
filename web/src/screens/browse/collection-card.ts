import type {ContentEntry} from '@core/library-content.ts';

/**
 * The exact watched tally a collection card may draw. Absence stays absent (never zero), a
 * lower-bound total is never an exact denominator, and a tally larger than its container is
 * inconsistent, so it is left out rather than shown as an impossible fraction.
 */
export function collectionWatchedCount(entry: Pick<ContentEntry, 'watchedCount' | 'count' | 'countIsLowerBound'>): number | undefined {
  const {watchedCount, count} = entry;
  if (watchedCount === undefined || count === undefined || count <= 0 || entry.countIsLowerBound === true) return undefined;
  return watchedCount <= count ? watchedCount : undefined;
}

/** The tile's bottom bar: only a real watched share of an exact, positive total. */
export function collectionWatchedProgress(entry: Pick<ContentEntry, 'watchedCount' | 'count' | 'countIsLowerBound'>): number | undefined {
  const watched = collectionWatchedCount(entry), count = entry.count;
  return watched === undefined || watched <= 0 || count === undefined ? undefined : watched / count;
}
