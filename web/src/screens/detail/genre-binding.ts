import {browseCapabilitiesPath, parseBrowseCapabilities, validateBrowseQuery, type BrowseNode} from '@core/browse.ts';
import type {DetailApi} from '@core/detail.ts';
import type {ScreenContext} from './context';

/** Uses the library's actual published vocabulary; the parent serializes the destination. */
export async function openGenre(context: ScreenContext, api: DetailApi, libraryId: string, genre: string, current: () => void, signal: AbortSignal) {
  current(); signal.throwIfAborted();
  if (!context.effects.openBrowse) throw new DOMException('Browse destination unavailable', 'AbortError');
  const capabilities = parseBrowseCapabilities(await api.request(browseCapabilitiesPath(libraryId, ''), 'GET', undefined, signal));
  current(); signal.throwIfAborted();
  if (capabilities.library.id !== libraryId) throw new DOMException('Browse vocabulary changed', 'AbortError');
  const query: BrowseNode = {field: 'genre', operator: 'equals', value: genre};
  validateBrowseQuery(capabilities, query);
  // The parent resolves this library's genuine default view/pivot and route.
  context.effects.openBrowse({libraryId, query});
}
