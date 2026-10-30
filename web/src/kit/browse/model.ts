import type {BrowseSortCapability, BrowseSortSelection} from '@core/browse';
import type {FacetSelection} from './types';

export function nextFacetSelection(included: boolean, excluded: boolean, cycleExclusion: boolean): FacetSelection {
  return included ? cycleExclusion ? 'exclude' : 'clear' : excluded ? 'clear' : 'include';
}
export function nextBrowseSort(sorts: readonly BrowseSortCapability[], current: BrowseSortSelection, field: string): BrowseSortSelection | undefined {
  const sort = sorts.find(candidate => candidate.id === field);
  if (!sort) return undefined;
  if (sort.id === current.field && (sort.seeded || sort.id === 'random')) return undefined;
  const flipped = current.direction === 'asc' ? 'desc' : 'asc';
  return {field: sort.id, direction: sort.id === current.field && sort.directions.includes(flipped) ? flipped : sort.defaultDirection};
}
export type TrackWindow = Readonly<{start: number; end: number}>;
/** The caller retains revision-pinned pages. This only reports a bounded viewport. */
export function trackWindow(scrollTop: number, height: number, rowHeight: number, total: number, overscan = 4): TrackWindow {
  if (![scrollTop, height, rowHeight, total, overscan].every(Number.isFinite) || height < 0 || rowHeight <= 0 || total < 0 || overscan < 0) throw new Error('Invalid track viewport');
  const count = Math.floor(total), extra = Math.floor(overscan), top = Math.max(0, scrollTop);
  return {start: Math.min(count, Math.max(0, Math.floor(top / rowHeight) - extra)), end: Math.min(count, Math.ceil((top + height) / rowHeight) + extra)};
}
