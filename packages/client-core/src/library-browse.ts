/**
 * A library tab's list on the server: what the library can be browsed by, in the vocabulary the
 * filter and sort controls already draw, and the request for one applied filter and order.
 *
 * The library says which lists, sorts and filters it has (`GET /v1/libraries/{id}`); nothing is
 * assumed that it does not offer. A filter the screen can build but the server cannot answer is
 * refused in plain words before anything is asked.
 */
import type {Library as WireLibrary} from '../../contracts/src/next/api.ts';
import {acrossLibraries, BrowseQueryError, type BrowseNode, type BrowseSortSelection} from './browse.ts';
import {itemKindOf, libraryKind, type ItemListQuery} from './items.ts';
import {serverPath} from './query/server-api.ts';
import {routePattern} from '../../contracts/src/routes.ts';

/** Each tab's name in the app, the kind of item it lists and its label. */
const pivots: readonly Readonly<{id: string; kind: string; labelKey: string; views: readonly string[]; /** The kinds the list asks for, when more than the tab's own. */ lists?: string}>[] = [
  {id: 'movies', kind: 'movie', labelKey: 'library.movies', views: ['grid', 'list']},
  {id: 'shows', kind: 'show', labelKey: 'library.shows', views: ['grid', 'list']},
  {id: 'episodes', kind: 'episode', labelKey: 'library.episodes', views: ['list', 'grid']},
  {id: 'artists', kind: 'artist', labelKey: 'library.artists', views: ['grid', 'list']},
  {id: 'albums', kind: 'album', labelKey: 'library.albums', views: ['grid', 'list']},
  {id: 'songs', kind: 'track', labelKey: 'library.songs', views: ['list']},
  {id: 'books', kind: 'audiobook', labelKey: 'library.books', views: ['grid', 'list']},
  // An audiobook library's Series tab: each series a card, opening its volumes.
  {id: 'series', kind: 'series', labelKey: 'library.tab.series', views: ['grid', 'list']},
  // An audiobook library's Authors tab is its people list (GET /v1/people), not an item list: by name, with no filters.
  {id: 'authors', kind: 'author', labelKey: 'library.tab.authors', views: ['grid', 'list']},
  {id: 'books', kind: 'book', labelKey: 'library.tab.books', views: ['grid', 'list']},
  // A comic library's tab lists its comics with their series, each series opening its volumes.
  {id: 'comics', kind: 'comic', labelKey: 'library.tab.volumesAndIssues', views: ['grid', 'list'], lists: 'comic,series'},
  {id: 'collections', kind: 'collection', labelKey: 'library.tab.collections', views: ['grid', 'list']},
];
/** The server's sort names and the app's. */
const sortIds: Readonly<Record<string, string>> = {title: 'title', added: 'added', year: 'year', release: 'releaseDate', latest: 'latestAired'};
const wireSort = (field: string): string | undefined => Object.entries(sortIds).find(([, id]) => id === field)?.[0];
/** The server's filters as the controls know them. */
const fields: Readonly<Record<string, Readonly<{id: string; facet?: string} & Record<string, unknown>>>> = {
  genre: {id: 'genre', labelKey: 'browse.field.genre', type: 'identity-set', operators: ['in', 'contains-any', 'equals'], controlHint: 'facet-multi-select', complexity: 'quick', facet: 'genre'},
  year: {id: 'year', labelKey: 'browse.field.year', type: 'number', operators: ['equals', 'between', 'at-least', 'at-most'], controlHint: 'number-range', complexity: 'quick'},
  watched: {id: 'playState', labelKey: 'browse.field.playState', type: 'enum', operators: ['equals'], controlHint: 'select', complexity: 'quick', allowedValues: ['unplayed', 'played']},
  favorite: {id: 'favorite', labelKey: 'browse.field.favorite', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick'},
  inList: {id: 'watchlisted', labelKey: 'browse.field.watchlisted', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick'},
  resolution: {id: 'resolution', labelKey: 'browse.field.resolution', type: 'enum', operators: ['equals', 'in'], controlHint: 'facet-multi-select', complexity: 'standard', facet: 'resolution'},
  // A book's or audiobook's authors, ticked from the library's own Authors list; several can be.
  author: {id: 'author', labelKey: 'browse.field.author', type: 'identity-set', operators: ['in', 'contains-any', 'equals'], controlHint: 'facet-multi-select', complexity: 'quick', facet: 'author'},
};
const quick: readonly Readonly<{id: string; labelKey: string; field: string; value: string | boolean}>[] = [
  {id: 'unwatched', labelKey: 'filter.unwatched', field: 'playState', value: 'unplayed'},
  {id: 'watched', labelKey: 'filter.watched', field: 'playState', value: 'played'},
  {id: 'favorites', labelKey: 'filter.favorites', field: 'favorite', value: true},
  {id: 'watchlist', labelKey: 'filter.watchlist', field: 'watchlisted', value: true},
];
/** The largest page the server serves; the lists ask for less. */
const limits = {maximumDepth: 5, maximumClauses: 40, maximumBytes: 16_384, maximumSorts: 1, defaultLimit: 60, maximumLimit: 200, cursorTtlMs: 0};

/** The library offers the tab: its views list the kind. Authors and Series are an audiobook library's tabs (a
 * reading library's come from its own reading tabs); Authors is its people list, not one of its views. */
const offers = (library: WireLibrary, pivot: (typeof pivots)[number]): boolean =>
  pivot.id === 'authors' ? library.kind === 'audiobooks'
    : (pivot.id !== 'series' || library.kind === 'audiobooks') && !!library.views?.some(view => view.kind === pivot.kind);
const pivotFor = (library: WireLibrary, pivotId: string) => pivots.find(pivot => pivot.id === pivotId && offers(library, pivot));

/** What this library's tab can be filtered and ordered by, as `parseBrowseCapabilities` reads it. */
export function libraryBrowseCapabilities(library: WireLibrary, pivotId: string): unknown {
  const offered = pivots.filter(pivot => offers(library, pivot));
  // `latest` is a show's newest episode (the Recently aired tab); music's newest track has no name in the app yet.
  const sorts = (library.sorts ?? []).filter(sort => sortIds[sort.id] && (sort.id !== 'latest' || ['shows', 'anime'].includes(library.kind))).map(sort => ({id: sortIds[sort.id]!, labelKey: 'sort.' + sortIds[sort.id], directions: ['asc', 'desc'], defaultDirection: sort.order === 'desc' ? 'desc' : 'asc', applicableKinds: []}));
  const filters = (library.filters ?? []).flatMap(filter => {
    const known = fields[filter.id];
    if (!known) return [];
    const {facet, ...field} = known;
    return [{...field, cost: 'indexed', applicableKinds: [], ...(filter.values?.length ? {allowedValues: filter.values} : {}), ...(facet ? {facetSource: {endpoint: serverPath('GET /v1/facets'), field: facet}} : {})}];
  });
  const shaped = offered.map(pivot => {
    // The people list is in name order only, with no filters.
    const own = pivot.id === 'authors' ? sorts.filter(sort => sort.id === 'title') : sorts, lead = own[0];
    return {id: pivot.id, labelKey: pivot.labelKey, entityKinds: [pivot.kind === 'track' ? 'song' : pivot.kind], supportedViews: pivot.views, browsable: true,
      defaultSort: lead ? [{field: lead.id, direction: pivot.id === 'authors' ? 'asc' : lead.defaultDirection}] : [], sorts: own.map(sort => sort.id),
      filters: pivot.id === 'authors' ? [] : filters.map(filter => filter.id), paging: 'keyset'};
  });
  // A Books or Comics library's Authors and Series tabs: its people by the author role and its
  // series, each opening the titles it holds (the read-web frames). Comics lead with Series.
  const reading = library.kind === 'books' || library.kind === 'comics';
  const list = (id: string, field: string) => ({id, labelKey: 'library.tab.' + id, entityKinds: [field], supportedViews: ['grid', 'list'], browsable: false,
    defaultSort: [], sorts: [], filters: [], facetSource: {endpoint: routePattern('get_library_facets'), field}});
  const authors = reading && filters.some(filter => filter.id === 'author') ? [list('authors', 'author')] : [];
  const series = reading && library.views?.some(view => view.kind === 'series') ? [list('series', 'series')] : [];
  const all = library.kind === 'comics' ? [...series, ...shaped, ...authors] : [...shaped, ...authors, ...series];
  return {library: {id: library.id, name: library.name, kind: libraryKind(library.kind), defaultView: ''}, pivots: all, resolvedPivot: shaped.find(pivot => pivot.id === pivotId) ?? null,
    fields: filters, sorts, quickFilters: quick.filter(each => filters.some(filter => filter.id === each.field)).map(each => ({id: each.id, labelKey: each.labelKey, query: {variant: 'predicate', field: each.field, operator: 'equals', value: each.value}})),
    queryLimits: limits};
}

const unsupported = (): never => { throw new BrowseQueryError('query', 'This filter is not available yet.'); };
const one = (value: unknown): string | number | boolean => {
  const values = Array.isArray(value) ? value : [value];
  if (values.length !== 1 || values[0] === null || values[0] === undefined) unsupported();
  return values[0] as string | number | boolean;
};

/** A genre's titles across every library (Search's genre page): the films and shows carrying it,
 * `GET /v1/items?tag={genre}&kind=movie,show`. The genre is the page's one condition. */
export const genreTitlesPivot = 'video-titles';
const genreTitleSorts = ['title', 'added', 'year'] as const;
export function genreTitlesCapabilities(): unknown {
  const sorts = genreTitleSorts.map(id => ({id: sortIds[id]!, labelKey: 'sort.' + sortIds[id], directions: ['asc', 'desc'], defaultDirection: id === 'title' ? 'asc' : 'desc', applicableKinds: []}));
  const {facet: _facet, ...genre} = fields.genre!;
  const pivot = {id: genreTitlesPivot, labelKey: 'library.tab.movies', entityKinds: ['movie', 'show'], supportedViews: ['grid', 'list'], browsable: true,
    defaultSort: [{field: 'title', direction: 'asc'}], sorts: sorts.map(sort => sort.id), filters: ['genre'], paging: 'keyset'};
  return {library: {id: acrossLibraries, name: '', kind: 'mixed', defaultView: ''}, pivots: [pivot], resolvedPivot: pivot,
    fields: [{...genre, cost: 'indexed', applicableKinds: []}], sorts, quickFilters: [], queryLimits: limits};
}
function genreTitlesQuery(nodes: readonly BrowseNode[], sorts: readonly BrowseSortSelection[]): ItemListQuery {
  const query: Record<string, unknown> = {kind: 'movie,show'};
  const add = (node: BrowseNode): void => {
    if ('all' in node) { node.all.forEach(add); return; }
    if ('field' in node && node.field === 'genre' && ['equals', 'in', 'contains-any'].includes(node.operator) && query.tag === undefined) query.tag = String(one(node.value));
    else unsupported();
  };
  nodes.forEach(add);
  if (!query.tag) throw new BrowseQueryError('query', 'Choose a genre.');
  const sort = sorts[0], name = sort && wireSort(sort.field);
  if (sorts.length > 1 || sort && !name) throw new BrowseQueryError('sort', 'That sort is not available here.');
  if (sort) { query.sort = name; query.order = sort.direction; }
  return query as ItemListQuery;
}

/** One or several authors' ids as the list's author condition: any of them, or (contains-all) all of them. */
const authors = (operator: string, value: unknown): string => {
  const ids = Array.isArray(value) ? value : [value];
  if (!ids.length || ids.some(id => typeof id !== 'string' || !id || id.includes(','))) unsupported();
  return ids.length === 1 && operator !== 'contains-all' ? ids[0] as string : (operator === 'contains-all' ? 'all:' : 'any:') + ids.join(',');
};

/** The server's list for a tab with these filters and this order. The filters all apply together; anything else is refused. */
export function libraryItemQuery(library: Readonly<{id: string; views?: WireLibrary['views']}>, pivotId: string, nodes: readonly BrowseNode[], sorts: readonly BrowseSortSelection[]): ItemListQuery {
  if (library.id === acrossLibraries && pivotId === genreTitlesPivot) return genreTitlesQuery(nodes, sorts);
  const candidates = pivots.filter(each => each.id === pivotId && (!library.views || library.views.some(view => view.kind === each.kind)));
  const pivot = candidates[0];
  if (!pivot) throw new BrowseQueryError('pivot', 'That view is not available in this library.');
  // Books is an audiobook library's tab and a book library's. Without the library's lists, both
  // kinds are asked for: each library holds only its own, so the list is that library's books.
  const query: Record<string, unknown> = {library: library.id, kind: candidates.length > 1 ? candidates.map(each => each.lists ?? each.kind).join(',') : pivot.lists ?? pivot.kind};
  const add = (node: BrowseNode): void => {
    if ('all' in node) { node.all.forEach(add); return; }
    if (!('field' in node)) { unsupported(); return; }
    const {field, operator, value} = node;
    if (field === 'genre' && ['in', 'contains-any', 'equals', 'contains-all'].includes(operator) && query.genre === undefined) query.genre = String(one(value));
    else if (field === 'resolution' && ['in', 'equals'].includes(operator) && query.resolution === undefined) query.resolution = String(one(value));
    // A book's authors (people), one or several from the Authors list or the filter's own control:
    // the server takes them as one condition, "any:" or "all:" of their ids.
    else if (field === 'author' && ['in', 'contains-any', 'equals', 'contains-all'].includes(operator) && query.author === undefined) query.author = authors(operator, value);
    // A series, from the Series list or a link to it: the server's series list takes one.
    else if (field === 'series' && ['in', 'contains-any', 'equals'].includes(operator) && query.series === undefined) query.series = String(one(value));
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

/**
 * A saved view's stored list read back as the browse controls draw it: the tab, the filters and the order that
 * `libraryItemQuery` turned into that list (definition area 6 rule 24: a saved view is a saved sort and filter on one
 * library). `undefined` when the list holds something the controls cannot draw, so it is not shown as something else.
 */
export function libraryQueryControls(query: string): Readonly<{pivot: string; nodes: readonly BrowseNode[]; sorts: readonly BrowseSortSelection[]}> | undefined {
  const params = new URLSearchParams(query), nodes: BrowseNode[] = [];
  const kind = params.get('kind') ?? '';
  const pivot = kind === 'audiobook,book' ? 'books' : pivots.find(each => (each.lists ?? each.kind) === kind)?.id;
  if (!pivot) return undefined;
  let sorts: BrowseSortSelection[] = [];
  for (const [name, value] of params) {
    if (name === 'kind' || name === 'order') continue;
    if (name === 'genre') nodes.push({variant: 'predicate', field: 'genre', operator: 'equals', value});
    else if (name === 'resolution') nodes.push({variant: 'predicate', field: 'resolution', operator: 'equals', value});
    else if (name === 'watched' && (value === 'true' || value === 'false')) nodes.push({variant: 'predicate', field: 'playState', operator: 'equals', value: value === 'true' ? 'played' : 'unplayed'});
    else if ((name === 'favorite' || name === 'inList') && (value === 'true' || value === 'false')) nodes.push({variant: 'predicate', field: name === 'favorite' ? 'favorite' : 'watchlisted', operator: 'equals', value: value === 'true'});
    else if (name === 'year') {
      const [from, to] = value.split('..').map(Number);
      if (!value.includes('..') && Number.isInteger(from)) nodes.push({variant: 'predicate', field: 'year', operator: 'equals', value: from!});
      else if (to === 9999 && Number.isInteger(from)) nodes.push({variant: 'predicate', field: 'year', operator: 'at-least', value: from!});
      else if (from === 1 && Number.isInteger(to)) nodes.push({variant: 'predicate', field: 'year', operator: 'at-most', value: to!});
      else if (Number.isInteger(from) && Number.isInteger(to)) nodes.push({variant: 'predicate', field: 'year', operator: 'between', value: [from!, to!]});
      else return undefined;
    } else if (name === 'sort' && sortIds[value]) sorts = [{field: sortIds[value]!, direction: params.get('order') === 'desc' ? 'desc' : 'asc'}];
    else return undefined;
  }
  return {pivot, nodes, sorts};
}
