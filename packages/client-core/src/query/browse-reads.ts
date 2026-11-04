import {readReadingFacet} from '../browse-preparation.ts';
import {
  browseCapabilitiesPath, browsePath, browseContextFacetsPath, browseCountsPath, browseCountsRequest,
  parseBrowseCapabilities, parseBrowseResult, parseBrowseFacets, parseBrowseCounts,
  type BrowseNode, type BrowseRequestBody,
} from '../browse.ts';

/** Domain reads use the caller's existing transport and lifetime. These operations
 * add no cache, retry, timeout or preparation policy of their own. */
export interface BrowseReadApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
export async function readBrowseCapabilities(api: BrowseReadApi, libraryId: string, pivot: string, signal?: AbortSignal) {
  return parseBrowseCapabilities(await api.request(browseCapabilitiesPath(libraryId, pivot), 'GET', undefined, signal));
}
export async function readImmediateBrowsePage(api: BrowseReadApi, libraryId: string, body: BrowseRequestBody, signal?: AbortSignal) {
  return parseBrowseResult(await api.request(browsePath(libraryId), 'POST', body, signal));
}
export async function readBrowseCounts(api: BrowseReadApi, libraryId: string, pivot: string,
  queries: readonly Readonly<{id: string; query: BrowseNode | null}>[], signal?: AbortSignal) {
  const raw = await api.request(browseCountsPath(libraryId), 'POST', browseCountsRequest(pivot, queries), signal);
  return parseBrowseCounts(raw, queries.map(query => query.id));
}
export async function readBrowseFacet(api: BrowseReadApi, libraryId: string, field: string, pivot: string,
  query: BrowseNode | undefined, search: string, limit: number, signal?: AbortSignal) {
  const path = browseContextFacetsPath(libraryId, field, pivot, query, search, limit);
  return parseBrowseFacets(await api.request(path, 'GET', undefined, signal), field);
}

/** Reading facets retain their existing preparation owner and currentness checks. */
export async function readContextualBrowseFacet(api: import('../library-content.ts').LibraryContentApi,
  library: Readonly<{id: string; kind: string}>, field: string, pivot: string, query: BrowseNode | undefined,
  search: string, lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {
  if (library.kind === 'book' || library.kind === 'comic') {
    const path = browseContextFacetsPath(library.id, field, pivot, query, search, 200);
    return readReadingFacet(api, library.id, path, field, lifetime);
  }
  return readBrowseFacet(api, library.id, field, pivot, query, search, 200, lifetime.signal);
}
