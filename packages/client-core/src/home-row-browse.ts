import type {HomeRowSource, Library} from '../../contracts/src/next/api.ts';
import {BrowseQueryError, parseBrowseCapabilities, parseBrowseFacets, type BrowseNode, type BrowseSortSelection} from './browse.ts';
import {fetchHomeRow} from './home.ts';
import {rememberFilterNames} from './filter-value-names.ts';
import {readLibrary} from './items.ts';
import {itemListFilters, libraryBrowseCapabilities} from './library-browse.ts';
import {groupOrderSort} from './presentation/browse-groups.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';

export const homeRowPivot = 'row-titles';
export const homeRowDefaultSort = [{field: groupOrderSort, direction: 'asc'}] as const;
export const isRecentHomeRow = (rowId: string) => rowId === 'recently_added' || rowId === 'recently_released';
export const hasHomeRowQueue = (rowId: string) => rowId !== 'trending' && !rowId.startsWith('because:') && rowId !== 'on_now';

/** Cross-library recent rows use the options shared by the different library kinds. */
const commonLibrary: Library = {id: '', name: '', kind: '',
  sorts: [{id: 'title', order: 'asc'}, {id: 'added', order: 'desc'}, {id: 'year', order: 'desc'}],
  filters: [{id: 'genre', type: 'tag'}, {id: 'year', type: 'year'}, {id: 'watched', type: 'boolean'},
    {id: 'favorite', type: 'boolean'}, {id: 'inList', type: 'boolean'}]};

export async function readHomeRowCapabilities(api: ServerRequester, rowId: string, libraryId: string | undefined, signal?: AbortSignal) {
  const recent = isRecentHomeRow(rowId);
  const library = recent && libraryId ? await readLibrary(api, libraryId, signal) : commonLibrary;
  const common = parseBrowseCapabilities(libraryBrowseCapabilities(library, homeRowPivot));
  const fields = recent ? common.fields.filter(field => ['genre', 'year', 'playState', 'favorite', 'watchlisted'].includes(field.id))
    .map(field => field.id === 'genre' ? {...field, operators: [...field.operators, 'contains-all' as const]} : field) : [];
  if (recent && !libraryId) fields.push({id: 'library', labelKey: 'browse.field.library', type: 'identity-set', operators: ['in'],
    controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: []});
  const sorts = [{id: groupOrderSort, labelKey: 'next.browse.bestMatch', directions: ['asc'] as const, defaultDirection: 'asc' as const,
    expensive: false, applicableKinds: []}, ...(recent ? common.sorts : [])];
  const pivot = {id: homeRowPivot, labelKey: 'next.browse.bestMatch', browsable: true, entityKinds: [], supportedViews: ['grid', 'list'],
    defaultSort: homeRowDefaultSort, sorts: recent ? sorts.map(sort => sort.id) : [], filters: fields.map(field => field.id), paging: 'keyset' as const};
  return {...common, pivots: [pivot], resolvedPivot: pivot, fields, sorts, quickFilters: recent ? common.quickFilters : []};
}

/** The same scope, filters and order go to the row, its facets and its queue. */
export function homeRowSource(id: string, library: string | undefined, nodes: readonly BrowseNode[], sorts: readonly BrowseSortSelection[]): HomeRowSource {
  const filters = nodes.flatMap(function collect(node): BrowseNode[] {
    if ('all' in node) return node.all.flatMap(collect);
    if (!('field' in node)) throw new BrowseQueryError('query', 'This filter is not available for this row.');
    if (node.field === 'library' && node.operator === 'in' && Array.isArray(node.value) && !library) {
      library = node.value.length === 1 ? String(node.value[0]) : `any:${node.value.join(',')}`;
      return [];
    }
    if (node.field === 'genre' && Array.isArray(node.value)) {
      return [{...node, operator: 'equals', value: `${node.operator === 'contains-all' ? 'all' : 'any'}:${node.value.join(',')}`}];
    }
    return [node];
  });
  const {genre, year, watched, inProgress, favorite, inList, sort, order} = itemListFilters(filters, sorts.filter(each => each.field !== groupOrderSort));
  return {id, library, genre, year, watched, inProgress, favorite, inList, sort, order};
}

export function readHomeRowPage(api: ServerRequester, {id, ...source}: HomeRowSource, page: {at?: number; cursor?: string; limit: number; total?: boolean}, signal: AbortSignal) {
  return fetchHomeRow(api, id, {...source, ...page}, signal);
}

export async function readHomeRowFacet(api: ServerRequester, {id, sort, order, ...filters}: HomeRowSource, facet: string, signal: AbortSignal) {
  return callServer(api, 'GET /v1/facets', {query: {...filters, homeRow: id, facet}, signal});
}

export async function readHomeRowFacetChoices(api: ServerRequester, source: HomeRowSource, field: string, search: string, signal: AbortSignal) {
  const {values} = await readHomeRowFacet(api, source, field, signal);
  const wanted = search.trim().toLocaleLowerCase();
  const matches = values.filter(value => !wanted || (value.label || value.value).toLocaleLowerCase().includes(wanted));
  const choices = parseBrowseFacets({field, values: matches, total: matches.length}, field);
  rememberFilterNames(choices.values);
  return choices;
}

export async function countHomeRow(api: ServerRequester, source: HomeRowSource, signal: AbortSignal) {
  return (await readHomeRowPage(api, source, {limit: 1, total: true}, signal)).total;
}
