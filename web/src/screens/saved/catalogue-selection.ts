import type {BrowseSelectionScope} from '@core/selection-model/index.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {SavedEntry} from '@core/saved.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback/runtime';
import type {ScreenContext} from '../detail/context';
import {createCatalogueDocumentSelection, type CatalogueDocumentSource} from '../browse/document-selection';
import type {SavedRuntime, SavedScreenRoute} from './runtime';
import {sameSavedRoute, savedRouteKey} from './route';

export function savedCatalogueEntry(entry: SavedEntry): ContentEntry | undefined {
  if (entry.kind === 'playlist' || entry.kind === 'reading_group') return undefined;
  const media = entry.kind === 'playlist_entry' ? entry.hidden ? undefined : entry.media : entry;
  // A radio station is a seed, not an item: it is never a saved selection member.
  return media?.kind === 'radio' ? undefined : media;
}
/** Only current, server-projected catalogue members participate. Playlist
 * occurrence removal and resource administration keep their separate owners. */
export function createSavedCatalogueSelection(context: ScreenContext, runtime: SavedRuntime, route: SavedScreenRoute,
  principal: Omit<BrowseSelectionScope, 'viewId'>, metadataFence: () => string | undefined, isRouteCurrent: () => boolean, playback?: WebShellPlaybackRuntime) {
  let previous: readonly ContentEntry[] = [], membershipEpoch = 0;
  const membership = (entries: readonly ContentEntry[]) => {
    if (entries.length !== previous.length || entries.some((entry, index) => entry !== previous[index])) {
      previous = entries;membershipEpoch++;
    }
    return membershipEpoch;
  };
  const unique = (entries: readonly ContentEntry[]) => {
    const seen = new Set<string>();
    return entries.filter(entry => {const key = JSON.stringify([entry.libraryId, entry.kind, entry.id]);if (seen.has(key)) return false;seen.add(key);return true;});
  };
  const services = [runtime.saved, runtime.posters, runtime.squares];
  const source: CatalogueDocumentSource = {
    subscribe: listener => {const offs = [...services.map(service => service.subscribe(listener)), runtime.personal.subscribe(listener)];return () => {for (const off of offs) off();};},
    getSnapshot: () => {
      if (!context.isCurrent() || !isRouteCurrent() || !runtime.operations.current) return null;
      if (route.view === 'resource') {
        const state = runtime.personal.getSnapshot();
        if (!sameSavedRoute(state.route, route) || state.loading || state.error || !state.resource || !state.viewerFence) return null;
        const entries = state.entries.flatMap(entry => !entry.hidden && entry.media ? [entry.media] : []);
        return {key: JSON.stringify([savedRouteKey(route), state.viewerFence, state.revision, state.cursor, membership(entries)]),
          revision: JSON.stringify([state.viewerFence, state.revision]), entries: unique(entries)};
      }
      if (route.view !== 'watchlist' && route.view !== 'favorites' && route.view !== 'playlist') return null;
      const selected = route.view === 'playlist' ? [runtime.saved] : [runtime.posters, runtime.squares];
      const states = selected.map(service => service.getSnapshot());
      if (states.some((state, index) => state.phase !== 'ready' || !state.projection || !sameSavedRoute(state.route, route.view === 'playlist' ? route : {...route, shape: index === 0 ? 'posters' : 'squares'}))) return null;
      const projections = states.map(state => state.projection!);
      if (projections.some(value => value.scope.viewerFence !== projections[0].scope.viewerFence)) return null;
      const revision = JSON.stringify(projections.map(value => [value.scope, value.revision, value.playlistRevision]));
      const entries = projections.flatMap(value => value.sections.flatMap(section => section.entries.flatMap(entry => {const media = savedCatalogueEntry(entry);return media ? [media] : [];})));
      return {key: JSON.stringify([savedRouteKey(route), revision, states.map(state => [state.generation, state.pagination.cursor]), membership(entries)]), revision, entries: unique(entries)};
    },
  };
  const selection = createCatalogueDocumentSelection(context, source, principal, metadataFence, playback);
  return Object.assign(selection, {
    member: (entry: ContentEntry) => {
      const document = source.getSnapshot();
      if (!document || !previous.includes(entry)) return;
      return document.entries.find(candidate => candidate.id === entry.id && candidate.kind === entry.kind && candidate.libraryId === entry.libraryId);
    },
    position: (entry: ContentEntry) => source.getSnapshot()?.entries.indexOf(entry) ?? -1});
}

export type SavedCatalogueSelection = ReturnType<typeof createSavedCatalogueSelection>;
