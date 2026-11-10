import {readFacet, readItems, readLibrary} from '../items.ts';
import {libraryBrowseCapabilities, libraryItemQuery} from '../library-browse.ts';
import type {ServerRequester} from './server-api.ts';
import {encodedRoutePath, routePath} from '../../../contracts/src/routes.ts';
import type {GuideRoute} from '../channel-guide.ts';
import {acrossLibraries, browseCapabilitiesPath, browsePath, browseContextFacetsPath, browseCountsPath, browseCountsRequest, parseBrowseFacets,
  type BrowseNode, type BrowseRequestBody} from '../browse.ts';

export interface BrowseReadApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
/** What a library's tab can be filtered and ordered by, for `parseBrowseCapabilities`. A list across libraries still asks the route it always did. */
export async function readBrowseCapabilitiesResponse(api: BrowseReadApi, libraryId: string, pivot: string, signal?: AbortSignal) {
  if (libraryId === acrossLibraries) return api.request(browseCapabilitiesPath(libraryId, pivot), 'GET', undefined, signal);
  return libraryBrowseCapabilities(await readLibrary(api as ServerRequester, libraryId, signal), pivot);
}
export function readImmediateBrowsePageResponse(api: BrowseReadApi, libraryId: string, body: BrowseRequestBody, signal?: AbortSignal) {
  return api.request(browsePath(libraryId), 'POST', body, signal);
}
const conditions = (query: BrowseNode | null | undefined): readonly BrowseNode[] => query ? [query] : [];
/** How many titles each of a few candidate filters would leave: the empty state's "loosen one filter" choices. */
export async function readBrowseCountsResponse(api: BrowseReadApi, libraryId: string, pivot: string,
  queries: readonly Readonly<{id: string; query: BrowseNode | null}>[], signal?: AbortSignal) {
  if (libraryId === acrossLibraries) return api.request(browseCountsPath(libraryId), 'POST', browseCountsRequest(pivot, queries), signal);
  const counts = await Promise.all(queries.map(async each => {
    const page = await readItems(api as ServerRequester, libraryItemQuery({id: libraryId}, pivot, conditions(each.query), []), {limit: 1, total: true}, signal);
    return {id: each.id, total: page.total ?? page.items.length, totalIsLowerBound: false, revision: 'counted'};
  }));
  return {counts};
}
/** The choices of one filter, counted under the other filters in use, for `parseBrowseFacets`. `search` narrows them by name. */
export async function readBrowseFacetResponse(api: BrowseReadApi, libraryId: string, field: string, pivot: string,
  query: BrowseNode | undefined, search: string, limit: number, signal?: AbortSignal) {
  if (libraryId === acrossLibraries) return api.request(browseContextFacetsPath(libraryId, field, pivot, query, search, limit), 'GET', undefined, signal);
  // The counted filter itself is left out, so its other choices keep their counts.
  const others = conditions(query).flatMap(node => 'all' in node ? node.all : [node]).filter(node => !('field' in node) || node.field !== field);
  const facet = await readFacet(api as ServerRequester, field, libraryItemQuery({id: libraryId}, pivot, others, []), signal);
  const wanted = search.trim().toLocaleLowerCase();
  // A tag is chosen by its name, which the list accepts as the filter.
  const values = facet.values.map(value => ({value: field === 'genre' ? value.label || value.value : value.value, label: value.label || value.value, count: value.count}))
    .filter(value => !wanted || value.label.toLocaleLowerCase().includes(wanted));
  return {field, total: values.length, values: values.slice(0, limit)};
}
/** A Reading library's author and series lists, read once. */
export async function readReadingBrowseFacet(api: import('../library-content.ts').LibraryContentApi, libraryId: string,
  field: string, pivot: string, query: BrowseNode | undefined, search: string, limit: number,
  lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {
  const page = parseBrowseFacets(await api.request(browseContextFacetsPath(libraryId, field, pivot, query, search, limit), 'GET', undefined, lifetime.signal), field);
  if (!lifetime.isCurrent()) throw Object.assign(new Error('The list closed.'), {name: 'AbortError'});
  return page;
}

/** Capture the original artist release address before its pager is constructed. */
export function createArtistAlbumPageRead(api: BrowseReadApi, artistId: string) {
  const path = encodedRoutePath('get_artists_id_albums', {id: encodeURIComponent(artistId)}) + '?limit=100';
  return (cursor: string | undefined, signal: AbortSignal) => api.request(path + (cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''), 'GET', undefined, signal) as Promise<{albums?: unknown; nextCursor?: unknown}>;
}

/** One named channel in the existing selected-source guide window. */
export function readSelectedChannelGuideResponse(api: BrowseReadApi,
  route: Pick<GuideRoute, 'kind' | 'sourceId' | 'timezone' | 'search' | 'start' | 'end' | 'includeServerHidden'>,
  channelId: string, includeServerHidden: boolean, signal: AbortSignal) {
  const params = new URLSearchParams({...route, includeServerHidden: String(includeServerHidden), channels: channelId, limit: '1'});
  const path = routePath('get_guide') + '?' + params;
  return api.request(path, 'GET', undefined, signal);
}
