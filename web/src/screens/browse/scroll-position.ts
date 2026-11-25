import type {BrowseNode, BrowseSortSelection} from '@core/browse.ts';
import type {BrowseSource} from './controller';

const MAX_SCROLL_POSITIONS = 64;
const scrollPositions = new Map<string, number>();

export function browseScrollKey(
  source: BrowseSource,
  predicates: readonly BrowseNode[],
  sort: BrowseSortSelection | undefined,
  view: 'grid' | 'list',
): string {
  return JSON.stringify([
    source.libraryId,
    source.pivot,
    source.homeRowId ?? null,
    source.base ?? null,
    predicates,
    sort ?? null,
    view,
  ]);
}

export function getBrowseScrollIndex(key: string): number | undefined {
  const value = scrollPositions.get(key);
  if (value === undefined) return undefined;
  scrollPositions.delete(key);
  scrollPositions.set(key, value);
  return value;
}

export function setBrowseScrollIndex(key: string, index: number): void {
  if (!Number.isSafeInteger(index) || index < 0) return;
  if (scrollPositions.has(key)) scrollPositions.delete(key);
  else if (scrollPositions.size >= MAX_SCROLL_POSITIONS) {
    const oldest = scrollPositions.keys().next().value;
    if (oldest !== undefined) scrollPositions.delete(oldest);
  }
  scrollPositions.set(key, index);
}

export function clearBrowseScrollPositions(): void {
  scrollPositions.clear();
}
