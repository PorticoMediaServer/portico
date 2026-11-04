import {fetchHomeRow, type HomeApi} from '../home.ts';
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
export function readBrowseCapabilities(api: BrowseReadApi, libraryId: string, pivot: string, signal: AbortSignal | undefined, beforeParse: () => boolean): Promise<ReturnType<typeof parseBrowseCapabilities> | undefined>;
export function readBrowseCapabilities(api: BrowseReadApi, libraryId: string, pivot: string, signal?: AbortSignal, beforeParse?: () => void): Promise<ReturnType<typeof parseBrowseCapabilities>>;
export async function readBrowseCapabilities(api: BrowseReadApi, libraryId: string, pivot: string, signal?: AbortSignal, beforeParse?: () => boolean | void) {
  const raw = await api.request(browseCapabilitiesPath(libraryId, pivot), 'GET', undefined, signal);
  if (beforeParse?.() === false) return;
  return parseBrowseCapabilities(raw);
}
export async function readImmediateBrowsePage(api: BrowseReadApi, libraryId: string, body: BrowseRequestBody, signal?: AbortSignal) {
  return parseBrowseResult(await api.request(browsePath(libraryId), 'POST', body, signal));
}
export function readBrowseCounts(api: BrowseReadApi, libraryId: string, pivot: string,
  queries: readonly Readonly<{id: string; query: BrowseNode | null}>[], signal: AbortSignal | undefined, beforeParse: () => boolean): Promise<ReturnType<typeof parseBrowseCounts> | undefined>;
export function readBrowseCounts(api: BrowseReadApi, libraryId: string, pivot: string,
  queries: readonly Readonly<{id: string; query: BrowseNode | null}>[], signal?: AbortSignal, beforeParse?: () => void): Promise<ReturnType<typeof parseBrowseCounts>>;
export async function readBrowseCounts(api: BrowseReadApi, libraryId: string, pivot: string,
  queries: readonly Readonly<{id: string; query: BrowseNode | null}>[], signal?: AbortSignal, beforeParse?: () => boolean | void) {
  const raw = await api.request(browseCountsPath(libraryId), 'POST', browseCountsRequest(pivot, queries), signal);
  if (beforeParse?.() === false) return;
  return parseBrowseCounts(raw, queries.map(query => query.id));
}
export function readBrowseFacet(api: BrowseReadApi, libraryId: string, field: string, pivot: string,
  query: BrowseNode | undefined, search: string, limit: number, signal: AbortSignal | undefined, beforeParse: () => boolean): Promise<ReturnType<typeof parseBrowseFacets> | undefined>;
export function readBrowseFacet(api: BrowseReadApi, libraryId: string, field: string, pivot: string,
  query: BrowseNode | undefined, search: string, limit: number, signal?: AbortSignal, beforeParse?: () => void): Promise<ReturnType<typeof parseBrowseFacets>>;
export async function readBrowseFacet(api: BrowseReadApi, libraryId: string, field: string, pivot: string,
  query: BrowseNode | undefined, search: string, limit: number, signal?: AbortSignal, beforeParse?: () => boolean | void) {
  const raw = await api.request(browseContextFacetsPath(libraryId, field, pivot, query, search, limit), 'GET', undefined, signal);
  if (beforeParse?.() === false) return;
  return parseBrowseFacets(raw, field);
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

/** Home originally joined the two raw requests before admitting the row and
 * parsing its capabilities. A malformed vocabulary cannot pre-empt a row error. */
export async function readHomeRowBrowseBootstrap(capabilityApi: BrowseReadApi, rowApi: HomeApi,
  rowId: string, pivot: string, signal: AbortSignal,
  lifetime: Readonly<{isCurrent: () => boolean; rowMismatch: () => Error}>) {
  const [raw, row] = await Promise.all([
    capabilityApi.request(browseCapabilitiesPath('*', pivot), 'GET', undefined, signal),
    fetchHomeRow(rowApi, rowId, {limit: 60}, signal),
  ]);
  if (!lifetime.isCurrent()) return;
  if (row.id !== rowId) throw lifetime.rowMismatch();
  return {row, capabilities: parseBrowseCapabilities(raw)};
}
