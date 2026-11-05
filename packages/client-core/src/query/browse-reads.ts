import {readReadingFacet} from '../browse-preparation.ts';
import {browseCapabilitiesPath, browsePath, browseContextFacetsPath, browseCountsPath, browseCountsRequest,
  type BrowseNode, type BrowseRequestBody} from '../browse.ts';

export interface BrowseReadApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
/** Forward the original transport promise. Callers retain their exact joins,
 * lifetime guards and DTO validation order, without another settlement step. */
export function readBrowseCapabilitiesResponse(api: BrowseReadApi, libraryId: string, pivot: string, signal?: AbortSignal) {
  return api.request(browseCapabilitiesPath(libraryId, pivot), 'GET', undefined, signal);
}
export function readImmediateBrowsePageResponse(api: BrowseReadApi, libraryId: string, body: BrowseRequestBody, signal?: AbortSignal) {
  return api.request(browsePath(libraryId), 'POST', body, signal);
}
export function readBrowseCountsResponse(api: BrowseReadApi, libraryId: string, pivot: string,
  queries: readonly Readonly<{id: string; query: BrowseNode | null}>[], signal?: AbortSignal) {
  return api.request(browseCountsPath(libraryId), 'POST', browseCountsRequest(pivot, queries), signal);
}
export function readBrowseFacetResponse(api: BrowseReadApi, libraryId: string, field: string, pivot: string,
  query: BrowseNode | undefined, search: string, limit: number, signal?: AbortSignal) {
  return api.request(browseContextFacetsPath(libraryId, field, pivot, query, search, limit), 'GET', undefined, signal);
}
/** Retain the existing Reading preparation owner and its original promise. */
export function readReadingBrowseFacet(api: import('../library-content.ts').LibraryContentApi, libraryId: string,
  field: string, pivot: string, query: BrowseNode | undefined, search: string, limit: number,
  lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {
  return readReadingFacet(api, libraryId, browseContextFacetsPath(libraryId, field, pivot, query, search, limit), field, lifetime);
}
