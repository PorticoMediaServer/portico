import {entriesFromItems, readFacet, readItems, readLibrary, type ItemListQuery} from '../items.ts';
import {childrenQuery, genreTitlesCapabilities, genreTitlesPivot, libraryBrowseCapabilities, libraryItemQuery} from '../library-browse.ts';
import type {ContentEntry} from '../library-content.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import type {ServerRequester} from './server-api.ts';
import {readRebuiltReadingFacet} from './reading-facets-rebuilt.ts';
import {routePath} from '../../../contracts/src/routes.ts';
import type {GuideRoute} from '../channel-guide.ts';
import {acrossLibraries, browseCapabilitiesPath, browsePath, browseContextFacetsPath, browseCountsPath, browseCountsRequest, parseBrowseFacets,
  type BrowseNode, type BrowseRequestBody} from '../browse.ts';

export interface BrowseReadApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
/** What a library's tab can be filtered and ordered by, for `parseBrowseCapabilities`. A list across libraries still asks the route it always did. */
export async function readBrowseCapabilitiesResponse(api: BrowseReadApi, libraryId: string, pivot: string, signal?: AbortSignal) {
  // A genre's titles across every library: one list of films and shows, sorted by title, date added or year.
  if (libraryId === acrossLibraries && pivot === genreTitlesPivot) return genreTitlesCapabilities();
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
  // A library's authors are its people by that role, not a counted facet of the list.
  if (field === 'author') return readRebuiltReadingFacet(api as ServerRequester, {libraryId, field, q: search, limit}, signal);
  // The counted filter itself is left out, so its other choices keep their counts.
  const others = conditions(query).flatMap(node => 'all' in node ? node.all : [node]).filter(node => !('field' in node) || node.field !== field);
  const facet = await readFacet(api as ServerRequester, field, libraryItemQuery({id: libraryId}, pivot, others, []), signal);
  const wanted = search.trim().toLocaleLowerCase();
  // A tag is chosen by its name, which the list accepts as the filter.
  const values = facet.values.map(value => ({value: field === 'genre' ? value.label || value.value : value.value, label: value.label || value.value, count: value.count}))
    .filter(value => !wanted || value.label.toLocaleLowerCase().includes(wanted));
  return {field, total: values.length, values: values.slice(0, limit)};
}
/** A Reading library's filter choices: its authors and series from the rebuilt server's people and
 * series lists, any other filter from the library's own counted choices. */
export async function readReadingBrowseFacet(api: import('../library-content.ts').LibraryContentApi, libraryId: string,
  field: string, pivot: string, query: BrowseNode | undefined, search: string, limit: number,
  lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {
  const page = field === 'author' || field === 'series'
    ? await readRebuiltReadingFacet(api as ServerRequester, {libraryId, field, q: search, limit}, lifetime.signal)
    : parseBrowseFacets(await readBrowseFacetResponse(api as BrowseReadApi, libraryId, field, pivot, query, search, limit, lifetime.signal), field);
  if (!lifetime.isCurrent()) throw Object.assign(new Error('The list closed.'), {name: 'AbortError'});
  return page;
}

type MusicPage = Readonly<{entries: readonly ContentEntry[]; nextCursor: string; total?: number}>;
type MusicPageRequest = Readonly<{cursor?: string; limit: number}>;
async function readMusicPage(api: BrowseReadApi, libraryId: string, query: ItemListQuery, page: MusicPageRequest, signal?: AbortSignal): Promise<MusicPage> {
  const answer = await readItems(api as ServerRequester, query, {cursor: page.cursor || undefined, limit: page.limit, total: true}, signal);
  if (!answer || !Array.isArray(answer.items)) throw new Error(unreadableServerResponse);
  return {entries: entriesFromItems(answer.items, libraryId), nextCursor: answer.nextCursor ?? '', ...(typeof answer.total === 'number' ? {total: answer.total} : {})};
}
/** One page of an album's tracks in the album's own order, with the album's track count: the album page's list and its whole-album actions. */
export const readAlbumTrackPage = (api: BrowseReadApi, libraryId: string, albumId: string, page: MusicPageRequest, signal?: AbortSignal): Promise<MusicPage> =>
  readMusicPage(api, libraryId, childrenQuery(albumId), page, signal);
/** One page of an artist's albums, newest first: the artist page's releases. */
export const readArtistReleasePage = (api: BrowseReadApi, libraryId: string, artistId: string, page: MusicPageRequest, signal?: AbortSignal): Promise<MusicPage> =>
  readMusicPage(api, libraryId, {parent: artistId, kind: 'album', sort: 'year', order: 'desc'}, page, signal);
/** One page of every song crediting an artist: `popular` is the server's most played (only songs someone sharing their activity played), `title` is all of them by name. */
export const readArtistSongPage = (api: BrowseReadApi, libraryId: string, artistId: string, order: 'popular' | 'title', page: MusicPageRequest, signal?: AbortSignal): Promise<MusicPage> =>
  readMusicPage(api, libraryId, {artist: artistId, kind: 'track', sort: order, ...(order === 'title' ? {order: 'asc' as const} : {})}, page, signal);

/** One named channel in the existing selected-source guide window. */
export function readSelectedChannelGuideResponse(api: BrowseReadApi,
  route: Pick<GuideRoute, 'kind' | 'sourceId' | 'timezone' | 'search' | 'start' | 'end' | 'includeServerHidden'>,
  channelId: string, includeServerHidden: boolean, signal: AbortSignal) {
  const params = new URLSearchParams({...route, includeServerHidden: String(includeServerHidden), channels: channelId, limit: '1'});
  const path = routePath('get_guide') + '?' + params;
  return api.request(path, 'GET', undefined, signal);
}
