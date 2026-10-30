import {readReadingFacet} from '@core/browse-preparation.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {browseContextFacetsPath, type BrowseFacetPage, type BrowseNode} from '@portico/client-core';

/** The two non-browsable reading facets the server declares (Root §2/§3). */
export type ReadingFacetField = 'author' | 'series';
/** The captured shell's own API request, never a second transport. */
export type FacetRequest = ((path: string, method: 'GET', body: undefined, signal: AbortSignal) => Promise<unknown>) & Pick<LibraryContentApi, 'requestRaw'> & Readonly<{isCurrent?: () => boolean}>;
export type FacetPageOptions = Readonly<{
  libraryId: string;
  field: ReadingFacetField;
  /** The reading library's published grid pivot (books or comics). */
  pivot: string;
  query?: BrowseNode;
  q?: string;
  limit?: number;
  offset?: number;
}>;

/**
 * One counted page of a reading facet, through the existing browse facet route. Values keep the
 * server's opaque seriesId (Reading series only) and its own label, up to the accepted 4096
 * characters — nothing is truncated or derived here.
 */
export async function loadFacetPage(request: FacetRequest, options: FacetPageOptions, signal: AbortSignal): Promise<BrowseFacetPage> {
  const limit = options.limit ?? 100, offset = options.offset ?? 0;
  if (!Number.isInteger(limit) || limit < 1 || limit > 200 || !Number.isInteger(offset) || offset < 0) throw new Error('invalid_request');
  const current = () => !signal.aborted && (request.isCurrent?.() ?? true);
  const guard = () => {if (!current()) throw Object.assign(new Error('Reading facet scope retired'), {name: 'AbortError'});};
  guard();
  const path = browseContextFacetsPath(options.libraryId, options.field, options.pivot, options.query, options.q ?? '', limit, offset);
  const page = await readReadingFacet({request: async <T>(target: string, _method?: string, _body?: unknown, input?: AbortSignal) => request(target, 'GET', undefined, input ?? signal) as Promise<T>,
    ...(request.requestRaw ? {requestRaw: request.requestRaw} : {})}, options.libraryId, path, options.field, {signal, isCurrent: current});
  guard();
  if (page.values.length > limit) throw new Error('invalid_document');
  return page;
}

/**
 * An author facet value is the filter: the accepted attribution string, never a person identity
 * and never a parsed Subtitle. The same value the facet lists is what browse receives.
 */
export function authorPredicate(value: string): BrowseNode {
  if (!value) throw new Error('invalid_request');
  return {variant: 'predicate', field: 'author', operator: 'equals', value};
}
