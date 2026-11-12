import {readRebuiltReadingFacet} from './reading-facets-rebuilt.ts';
import type {LibraryContentApi} from '../library-content.ts';
import type {BrowseFacetPage, BrowseNode} from '../browse.ts';

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

/** One page of a Read library's authors or series, from the rebuilt server's people and series
 * lists, through the captured shell's own request. */
export async function loadReadingFacetPage(request: FacetRequest, options: FacetPageOptions, signal: AbortSignal): Promise<BrowseFacetPage> {
  const limit = options.limit ?? 100, offset = options.offset ?? 0;
  if (!Number.isInteger(limit) || limit < 1 || limit > 200 || !Number.isInteger(offset) || offset < 0) throw new Error('invalid_request');
  const current = () => !signal.aborted && (request.isCurrent?.() ?? true);
  const guard = () => {if (!current()) throw Object.assign(new Error('Reading facet scope retired'), {name: 'AbortError'});};
  guard();
  const page = await readRebuiltReadingFacet({request: async <T>(target: string, _method?: string, _body?: unknown, input?: AbortSignal) => request(target, 'GET', undefined, input ?? signal) as Promise<T>},
    {libraryId: options.libraryId, field: options.field, q: options.q, limit, offset}, signal);
  guard();
  return page;
}
