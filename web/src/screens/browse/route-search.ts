import {parseBrowseNode, type BrowseNode, type BrowseSortSelection} from '@core/browse.ts';
import type {ContentView} from '@core/library-content.ts';

const views: readonly ContentView[] = ['discover', 'browse', 'collections', 'categories', 'show', 'season', 'artist', 'album', 'book', 'collection', 'releases', 'songs', 'authors', 'series', 'author', 'book_series', 'disc'];
function invalid(): never {throw new Error('invalid-library-route');}
function text(value: unknown, max = 256): string | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== 'string' || !value || value.length > max || /[\x00-\x1f\x7f]/.test(value)) invalid();
  return value;
}
export type LibraryScreenSearch = Readonly<{view?: ContentView; entityId?: string; category?: string; pivot?: string; section?: 'mixes'; unmatched?: boolean; query?: BrowseNode; sort?: readonly BrowseSortSelection[]; q?: string}>;
export type HomeRowScreenSearch = Readonly<{query?: BrowseNode; sort?: readonly BrowseSortSelection[]}>;
/** A See all page owns only the toolbar's scoped filters and order; its row and
 * vocabulary come from the route's own row, never from the address. */
export function parseHomeRowScreenSearch(raw: Record<string, unknown>): HomeRowScreenSearch {
  return queryAndSort(raw);
}
/** A saved or shared view may arrive as one top-level conjunction. The toolbar chips and the
 * empty state name each condition, so the controller keeps those leaves apart; `{all: [...]}`
 * and the same conditions in the implicit conjunction build the identical query. */
export function browseConjunction(node: BrowseNode): readonly BrowseNode[] {
  return 'all' in node && node.all.length > 0 ? node.all : [node];
}
/** Both a library page and a Home row's See all own the toolbar's scoped filters and order. */
function queryAndSort(raw: Record<string, unknown>): {query?: BrowseNode; sort?: BrowseSortSelection[]} {
  const query = raw.query === undefined ? undefined : parseBrowseNode(raw.query);
  let sort: BrowseSortSelection[] | undefined;
  if (raw.sort !== undefined) {
    if (!Array.isArray(raw.sort) || raw.sort.length < 1 || raw.sort.length > 3) invalid();
    sort = raw.sort.map(row => {
      if (!row || typeof row !== 'object' || Array.isArray(row)) invalid();
      const field = text(row.field, 64);
      if (!field || row.direction !== 'asc' && row.direction !== 'desc' || row.seed !== undefined && (!Number.isSafeInteger(row.seed) || row.seed < 0)) invalid();
      return {field, direction: row.direction, ...(row.seed === undefined ? {} : {seed: row.seed})};
    });
  }
  return {...(query ? {query} : {}), ...(sort ? {sort} : {})};
}
/** A malformed owned predicate must not silently broaden the destination. */
export function parseLibraryScreenSearch(raw: Record<string, unknown>): LibraryScreenSearch {
  const view = raw.view === undefined ? undefined : views.find(view => view === raw.view);
  if (raw.view !== undefined && !view) invalid();
  const entityId = text(raw.entityId), category = text(raw.category), pivot = text(raw.pivot, 64), q = text(raw.q, 512);
  const {query, sort} = queryAndSort(raw);
  const unmatched = raw.unmatched === true || undefined;
  const section = raw.section === 'mixes' && view === 'discover' ? 'mixes' as const : undefined;
  return {...(view ? {view} : {}), ...(entityId ? {entityId} : {}), ...(category ? {category} : {}), ...(pivot ? {pivot} : {}), ...(unmatched ? {unmatched: true} : {}),
    ...(section ? {section} : {}), ...(query ? {query} : {}), ...(sort ? {sort} : {}), ...(q ? {q} : {})};
}
