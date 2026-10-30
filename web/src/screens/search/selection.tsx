import type {SearchService, SearchGroupResult, SearchEntry} from '@core/search.ts';
import type {BrowseSelectionScope} from '@core/selection-model/index.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback/runtime';
import type {ScreenContext} from '../detail/context';
import {createCatalogueDocumentSelection, type CatalogueDocumentSource} from '../browse/document-selection';
const searchableSelectionKinds = new Set(['movie', 'show', 'season', 'episode', 'album', 'song']);

/** Search is explicit; its own fence/revision never becomes Browse query authority. */
export function createSearchSelection(context: ScreenContext, service: Pick<SearchService, 'getSnapshot' | 'subscribe'>, principal: Omit<BrowseSelectionScope, 'viewId'>,
  metadataFence: () => string | undefined, runtime?: WebShellPlaybackRuntime) {
  const source: CatalogueDocumentSource = {subscribe: service.subscribe, getSnapshot: () => {
    const snapshot = service.getSnapshot(), data = snapshot.data, query = snapshot.query;
    if (!context.isCurrent() || !data || !query || !['ready', 'loading'].includes(snapshot.phase)
      || query.q !== data.query.q || (query.group ?? '') !== data.query.group) return null;
    return {key: JSON.stringify([data.scope.serverId, data.scope.viewerFence, data.revision, query.q, query.group, query.groups, query.libraryIds, query.sort, query.direction]),
      revision: JSON.stringify([data.scope.viewerFence, data.revision]), entries: snapshot.groups.flatMap(group => group.items.flatMap(entry => 'libraryId' in entry && searchableSelectionKinds.has(entry.kind) ? [entry] : []))};
  }};
  const selection = createCatalogueDocumentSelection(context, source, principal, metadataFence, runtime);
  const members = (group: SearchGroupResult, shown: readonly SearchEntry[]) => {
    if (!service.getSnapshot().groups.includes(group) || !shown.length || shown.some(entry => !group.items.includes(entry))) return;
    const entries = shown.flatMap(entry => 'libraryId' in entry && searchableSelectionKinds.has(entry.kind) ? [entry] : []);
    return entries.length === shown.length && selection.canSelectEntries(entries) ? entries : undefined;
  };
  return Object.assign(selection, {
    canSelectGroup: (group: SearchGroupResult, shown: readonly SearchEntry[]) => !!members(group, shown),
    selectGroup: (group: SearchGroupResult, shown: readonly SearchEntry[]) => {const entries = members(group, shown);if (!entries) throw new DOMException('Search group changed', 'AbortError');selection.selectEntries(entries);},
  });
}
export type SearchSelection = ReturnType<typeof createSearchSelection>;
