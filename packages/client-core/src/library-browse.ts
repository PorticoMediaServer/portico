/**
 * A library tab's list on the server: what the library can be browsed by, in the vocabulary the
 * filter and sort controls already draw, and the request for one applied filter and order.
 *
 * The library says which lists, sorts and filters it has (`GET /v1/libraries/{id}`); nothing is
 * assumed that it does not offer. A filter the screen can build but the server cannot answer is
 * refused in plain words before anything is asked.
 */
import type {Library as WireLibrary} from '../../contracts/src/next/api.ts';
import {BrowseQueryError, type BrowseNode, type BrowseSortSelection} from './browse.ts';
import {itemKindOf, libraryKind, type ItemListQuery} from './items.ts';

/** Each tab's name in the app, the kind of item it lists and its label. */
const pivots: readonly Readonly<{id: string; kind: string; labelKey: string; views: readonly string[]}>[] = [
  {id: 'movies', kind: 'movie', labelKey: 'library.movies', views: ['grid', 'list']},
  {id: 'shows', kind: 'show', labelKey: 'library.shows', views: ['grid', 'list']},
  {id: 'episodes', kind: 'episode', labelKey: 'library.episodes', views: ['list', 'grid']},
  {id: 'artists', kind: 'artist', labelKey: 'library.artists', views: ['grid', 'list']},
  {id: 'albums', kind: 'album', labelKey: 'library.albums', views: ['grid', 'list']},
  {id: 'songs', kind: 'track', labelKey: 'library.songs', views: ['list']},
  {id: 'books', kind: 'audiobook', labelKey: 'library.books', views: ['grid', 'list']},
  {id: 'books', kind: 'book', labelKey: 'library.tab.books', views: ['grid', 'list']},
  {id: 'comics', kind: 'comic', labelKey: 'library.tab.volumesAndIssues', views: ['grid', 'list']},
];
/** The server's sort names and the app's. */
const sortIds: Readonly<Record<string, string>> = {title: 'title', added: 'added', year: 'year', release: 'releaseDate'};
const wireSort = (field: string): string | undefined => Object.entries(sortIds).find(([, id]) => id === field)?.[0];
/** The server's filters as the controls know them. */
const fields: Readonly<Record<string, Readonly<{id: string; facet?: string} & Record<string, unknown>>>> = {
  genre: {id: 'genre', labelKey: 'browse.field.genre', type: 'identity-set', operators: ['in', 'contains-any', 'equals'], controlHint: 'facet-multi-select', complexity: 'quick', facet: 'genre'},
  year: {id: 'year', labelKey: 'browse.field.year', type: 'number', operators: ['equals', 'between', 'at-least', 'at-most'], controlHint: 'number-range', complexity: 'quick'},
  watched: {id: 'playState', labelKey: 'browse.field.playState', type: 'enum', operators: ['equals'], controlHint: 'select', complexity: 'quick', allowedValues: ['unplayed', 'played']},
  favorite: {id: 'favorite', labelKey: 'browse.field.favorite', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick'},
  inList: {id: 'watchlisted', labelKey: 'browse.field.watchlisted', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick'},
  resolution: {id: 'resolution', labelKey: 'browse.field.resolution', type: 'enum', operators: ['equals', 'in'], controlHint: 'facet-multi-select', complexity: 'standard', facet: 'resolution'},
};
const quick: readonly Readonly<{id: string; labelKey: string; field: string; value: string | boolean}>[] = [
  {id: 'unwatched', labelKey: 'filter.unwatched', field: 'playState', value: 'unplayed'},
  {id: 'watched', labelKey: 'filter.watched', field: 'playState', value: 'played'},
  {id: 'favorites', labelKey: 'filter.favorites', field: 'favorite', value: true},
  {id: 'watchlist', labelKey: 'filter.watchlist', field: 'watchlisted', value: true},
];
/** The largest page the server serves; the lists ask for less. */
const limits = {maximumDepth: 5, maximumClauses: 40, maximumBytes: 16_384, maximumSorts: 1, defaultLimit: 60, maximumLimit: 200, cursorTtlMs: 0};

const pivotFor = (library: WireLibrary, pivotId: string) => pivots.find(pivot => pivot.id === pivotId && library.views?.some(view => view.kind === pivot.kind));

/** What this library's tab can be filtered and ordered by, as `parseBrowseCapabilities` reads it. */
export function libraryBrowseCapabilities(library: WireLibrary, pivotId: string): unknown {
  const offered = pivots.filter(pivot => library.views?.some(view => view.kind === pivot.kind));
  const sorts = (library.sorts ?? []).filter(sort => sortIds[sort.id]).map(sort => ({id: sortIds[sort.id]!, labelKey: 'sort.' + sortIds[sort.id], directions: ['asc', 'desc'], defaultDirection: sort.order === 'desc' ? 'desc' : 'asc', applicableKinds: []}));
  const filters = (library.filters ?? []).flatMap(filter => {
    const known = fields[filter.id];
    if (!known) return [];
    const {facet, ...field} = known;
    return [{...field, cost: 'indexed', applicableKinds: [], ...(filter.values?.length ? {allowedValues: filter.values} : {}), ...(facet ? {facetSource: {endpoint: '/v1/facets', field: facet}} : {})}];
  });
  const first = sorts[0];
  const shaped = offered.map(pivot => ({id: pivot.id, labelKey: pivot.labelKey, entityKinds: [pivot.kind === 'track' ? 'song' : pivot.kind], supportedViews: pivot.views, browsable: true,
    defaultSort: first ? [{field: first.id, direction: first.defaultDirection}] : [], sorts: sorts.map(sort => sort.id), filters: filters.map(filter => filter.id), paging: 'keyset'}));
  return {library: {id: library.id, name: library.name, kind: libraryKind(library.kind), defaultView: ''}, pivots: shaped, resolvedPivot: shaped.find(pivot => pivot.id === pivotId) ?? null,
    fields: filters, sorts, quickFilters: quick.filter(each => filters.some(filter => filter.id === each.field)).map(each => ({id: each.id, labelKey: each.labelKey, query: {variant: 'predicate', field: each.field, operator: 'equals', value: each.value}})),
    queryLimits: limits};
}

const unsupported = (): never => { throw new BrowseQueryError('query', 'This filter is not available yet.'); };
const one = (value: unknown): string | number | boolean => {
  const values = Array.isArray(value) ? value : [value];
  if (values.length !== 1 || values[0] === null || values[0] === undefined) unsupported();
  return values[0] as string | number | boolean;
};

/** The server's list for a tab with these filters and this order. The filters all apply together; anything else is refused. */
export function libraryItemQuery(library: Readonly<{id: string; views?: WireLibrary['views']}>, pivotId: string, nodes: readonly BrowseNode[], sorts: readonly BrowseSortSelection[]): ItemListQuery {
  const pivot = pivots.find(each => each.id === pivotId && (!library.views || library.views.some(view => view.kind === each.kind)));
  if (!pivot) throw new BrowseQueryError('pivot', 'That view is not available in this library.');
  const query: Record<string, unknown> = {library: library.id, kind: pivot.kind};
  const add = (node: BrowseNode): void => {
    if ('all' in node) { node.all.forEach(add); return; }
    if (!('field' in node)) { unsupported(); return; }
    const {field, operator, value} = node;
    if (field === 'genre' && ['in', 'contains-any', 'equals', 'contains-all'].includes(operator) && query.genre === undefined) query.genre = String(one(value));
    else if (field === 'resolution' && ['in', 'equals'].includes(operator) && query.resolution === undefined) query.resolution = String(one(value));
    else if (field === 'playState' && operator === 'equals' && (value === 'played' || value === 'unplayed')) query.watched = value === 'played';
    else if (field === 'favorite' && operator === 'equals' && typeof value === 'boolean') query.favorite = value;
    else if (field === 'watchlisted' && operator === 'equals' && typeof value === 'boolean') query.inList = value;
    else if (field === 'year' && query.year === undefined) {
      const range = Array.isArray(value) ? value : [value];
      if (operator === 'equals' && typeof range[0] === 'number') query.year = String(range[0]);
      else if (operator === 'between' && typeof range[0] === 'number' && typeof range[1] === 'number') query.year = `${range[0]}..${range[1]}`;
      else if (operator === 'at-least' && typeof range[0] === 'number') query.year = `${range[0]}..9999`;
      else if (operator === 'at-most' && typeof range[0] === 'number') query.year = `1..${range[0]}`;
      else unsupported();
    } else unsupported();
  };
  nodes.forEach(add);
  const sort = sorts[0], name = sort && wireSort(sort.field);
  if (sorts.length > 1 || sort && !name) throw new BrowseQueryError('sort', 'That sort is not available here.');
  if (sort) { query.sort = name; query.order = sort.direction; }
  return query as ItemListQuery;
}

/** A holder's own list (a show's seasons, a season's episodes, an album's tracks), in its own order. */
export const childrenQuery = (parent: string, kind?: string): ItemListQuery => ({parent, ...(kind ? {kind: itemKindOf(kind)} : {}), sort: 'order'});
export {pivotFor as libraryPivot};
