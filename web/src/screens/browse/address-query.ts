import {completeBrowseNode, parseBrowseNode, type BrowseNode, type BrowseSortSelection} from '@core/browse.ts';
import {browseConjunction} from './route-search';
import type {BrowseQuery} from './controller';

/**
 * A browse grid's toolbar filters and order are the page's own state, so its address carries
 * them: Back from a title, a reload and a copied link land on the same filtered, sorted grid
 * (LB-1). The library grid and a Home row's See all share this one vocabulary. The address is
 * the source: the toolbar writes it, and the grid follows it.
 */
export type AddressedQuery = BrowseQuery;
export type AddressedSearch = Readonly<{query?: BrowseNode; sort?: readonly BrowseSortSelection[]}>;

export const appliedSorts = (sorts: readonly BrowseSortSelection[]): BrowseSortSelection[] => sorts.map(({field, direction, seed}) =>
  ({field, direction, ...(seed === undefined ? {} : {seed})}));
function sameSorts(a: readonly BrowseSortSelection[], b: readonly BrowseSortSelection[]): boolean {
  return a.length === b.length && a.every((value, index) => value.field === b[index]?.field && value.direction === b[index]?.direction && value.seed === b[index]?.seed);
}
/** The address's query, with an absent order read as the page's default one. */
export function addressedQuery(search: AddressedSearch, defaults: readonly BrowseSortSelection[]): AddressedQuery {
  return {predicates: search.query ? browseConjunction(search.query) : [], sorts: appliedSorts(search.sort?.length ? search.sort : defaults)};
}
/** One comparable key for both sides: every predicate in the controller's own normal form. */
export function addressedQueryKey(query: AddressedQuery): string {
  return JSON.stringify({predicates: query.predicates.map(node => parseBrowseNode(completeBrowseNode(node))), sorts: appliedSorts(query.sorts)});
}
/** The address that carries a query: no `query` when unfiltered, no `sort` when it is the default. */
export function addressedSearch(query: AddressedQuery, defaults: readonly BrowseSortSelection[]): {query?: BrowseNode; sort?: BrowseSortSelection[]} {
  const predicates = query.predicates;
  return {...(predicates.length ? {query: predicates.length === 1 ? predicates[0] : {variant: 'group', all: [...predicates]} as BrowseNode} : {}),
    ...(sameSorts(query.sorts, defaults) ? {} : {sort: appliedSorts(query.sorts)})};
}

/** A library address without the toolbar's own state. Changing a filter or the order keeps the
 *  same screen, lease and controller; any other change to the address is another screen. */
export function libraryAddressKey(location: Readonly<{pathname: string; search: unknown}>): string {
  const search = location.search && typeof location.search === 'object' ? location.search as Record<string, unknown> : {};
  const rest = Object.keys(search).filter(key => key !== 'query' && key !== 'sort' && search[key] !== undefined).sort().map(key => [key, search[key]]);
  return JSON.stringify([location.pathname, rest]);
}

/** The query part of a grid's cache key: the address's filters in the controller's normal form,
 *  its order as written (none: the opening order), and the tab's own opening order, so the same
 *  address read twice is one grid and two tabs on one pivot are two. */
export function addressCacheKey(search: AddressedSearch, opens?: readonly BrowseSortSelection[]): string {
  return JSON.stringify({predicates: search.query ? browseConjunction(search.query).map(node => parseBrowseNode(completeBrowseNode(node))) : [],
    sort: search.sort?.length ? appliedSorts(search.sort) : null, ...(opens?.length ? {opens: appliedSorts(opens)} : {})});
}
