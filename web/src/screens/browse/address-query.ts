import {completeBrowseNode, parseBrowseNode, type BrowseNode, type BrowseSortSelection} from '@core/browse.ts';
import {browseConjunction} from './route-search';

/**
 * A browse grid's toolbar filters and order are the page's own state, so its address carries
 * them: Back from a title, a reload and a copied link land on the same filtered, sorted grid
 * (LB-1). The library grid and a Home row's See all share this one vocabulary and one rule for
 * which side wins when the address and the grid disagree.
 */
export type AddressedQuery = Readonly<{predicates: readonly BrowseNode[]; sorts: readonly BrowseSortSelection[]}>;
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

export type AddressDecision = 'agreed' | 'publish' | 'adopt';
/**
 * Which side moved. An address that moved while the grid still held the last agreed query is a
 * real navigation (Back/Forward or a link) and is adopted into the grid. A grid that moved while
 * the address still held it is the toolbar, published on the address. The page's own replace
 * echo arrives with both equal and does nothing, so it never rebuilds the grid; an older echo
 * arriving after a newer one is republished rather than mistaken for a navigation.
 */
export class AddressContinuity {
  private settled: string | undefined;
  private lastRoute: string | undefined;
  private written = new Set<string>();
  reconcile(routeKey: string, controllerKey: string, genuineArrival: boolean): AddressDecision {
    // The first agreement is the address the grid was built from.
    this.settled ??= routeKey;
    this.lastRoute ??= routeKey;
    const moved = routeKey !== this.lastRoute;
    this.lastRoute = routeKey;
    if (routeKey === controllerKey) {this.settled = routeKey; return 'agreed';}
    if (controllerKey !== this.settled) {
      if (routeKey !== this.settled && (!this.written.has(routeKey) || moved && genuineArrival)) return 'adopt';
      if (this.written.size > 7) for (const key of this.written) {this.written.delete(key); break;}
      this.written.add(controllerKey);
      return 'publish';
    }
    return 'adopt';
  }
}
