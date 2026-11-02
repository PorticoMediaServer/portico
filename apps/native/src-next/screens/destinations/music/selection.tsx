import React from 'react';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {SelectionContext} from '../../../../../../packages/client-core/src/selection-model';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {createNativeSelection, type NativeSelection, type NativeSelectionGroup, type NativeSelectionSource} from '../../selection/native-selection';
import type {NativeTVMusicController} from './model';

/** The album and artist row cards (releases, other releases, similar) draw from this
 * page's own projection: its viewer fence and catalog/viewer revisions. */
export function musicRowSelectionSource(controller: NativeTVMusicController, current: () => boolean,
  viewer: () => Readonly<{serverId: string; authority: string; accountId: string; profileId: string}> | undefined, routeKey: string): NativeSelectionSource {
  return {
    subscribe: controller.subscribe,
    current: () => current(),
    getSnapshot: () => {
      const state = controller.getSnapshot(), projection = controller.selectionProjection(), account = viewer();
      const groups: NativeSelectionGroup[] = [
        {id: 'releases', items: state.albums.flatMap(album => {const entry = controller.releaseEntry(album); return entry ? [entry] : [];})},
        {id: 'similar', items: state.similar.filter(entry => controller.similarCurrent(state, entry))},
      ].filter(group => group.items.length);
      const viewId = JSON.stringify(['native-music-rows', routeKey, projection?.revision.catalog ?? 0, projection?.revision.viewer ?? 0]);
      // Only a loaded page from the signed-in viewer supplies a context; until then there is
      // nothing Select could truthfully join.
      const admitted = current() && !!projection && !!account && (account.authority === 'local' || account.authority === 'hosted') && viewId.length <= 4096;
      const context: SelectionContext | undefined = admitted
        ? {scope: {serverId: account.serverId, authority: account.authority as 'local' | 'hosted', accountId: account.accountId, profileId: account.profileId,
          viewId, privacyFence: projection.scope.viewerFence}, revision: {catalogRevision: String(projection.revision.catalog), viewerRevision: String(projection.revision.viewer)}}
        : undefined;
      return {key: viewId, context, groups};
    },
  };
}

/** Top songs and All songs are different Browse queries. Each retains its own
 * opaque revision; beginning a selection in the other list ends the first. The
 * page's album and artist rows share one more owner, so Select starts there too. */
export function createNativeMusicSelection(context: NativeShellBootstrapContext, controller: NativeTVMusicController,
  routeKey: string, current: () => boolean): NativeSelection {
  const groups = ['songs', 'all-songs'] as const;
  const ports = groups.map(group => createNativeSelection(context, {
    subscribe: controller.subscribe, current,
    getSnapshot: () => {
      const own = controller.selectionPage(group), state = controller.getSnapshot(), viewer = context.session()?.viewer;
      return {context: own && viewer && (viewer.authority === 'local' || viewer.authority === 'hosted') ? {
        scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId,
          viewId: JSON.stringify(['native-music', routeKey, own.generation, group, own.query])}, revision: {pageRevision: own.revision}} : undefined,
        groups: [{id: group, status: 'success' as const, offset: own?.offset ?? 0,
          items: (group === 'songs' ? state.songs : state.allSongs.songs).filter(controller.contains)}]};
    },
  }, {area: 'search', preserveEvicted: true}));
  const rows = createNativeSelection(context, musicRowSelectionSource(controller, current, () => context.session()?.viewer, routeKey), {area: 'search', aggregates: true});
  const listeners = new Set<() => void>(); let revision = 0;
  const stops = [...ports, rows].map(port => port.subscribe(() => {revision++; for (const listener of [...listeners]) listener();}));
  // Ending the page's selection ends every owner it drew from (tracks and row cards).
  const clear = () => {let any = false; for (const port of [...ports, rows]) any = port.clear() || any; return any;};
  const rowOnly = (entry: import('../../../../../../packages/client-core/src/search').SearchEntry, group?: string) => {
    const own = rows.item(entry, group === 'releases' || group === 'similar' ? group : undefined);
    if (!own) return undefined;
    const pick = () => {
      if (!current() || !('libraryId' in entry)) return;
      if (ports.every(port => port.clear())) own.onEnter();
    };
    return {...own, onEnter: pick, onToggle: pick};
  };
  return Object.freeze({
    register: () => () => {}, clear,
    item: (entry, group) => {
      const index = groups.findIndex(value => value === group);
      if (index < 0) return rowOnly(entry, group);
      const item = ports[index]!.item(entry, group), own = controller.selectionPage(groups[index]!);
      if (!item || !own) return undefined;
      const pick = () => {
        const live = controller.selectionPage(groups[index]!);
        if (!current() || !('libraryId' in entry) || !controller.contains(entry) || !live || live.generation !== own.generation || live.query !== own.query || live.revision !== own.revision) return;
        if (ports[1 - index]!.clear()) item.onEnter();
      };
      return {...item, onEnter: pick, onToggle: pick};
    },
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => revision,
    render: artwork => <>{ports.map((port, index) => <React.Fragment key={groups[index]}>{port.render(artwork)}</React.Fragment>)}{rows.render(artwork)}</>,
    dispose: () => {stops.forEach(stop => stop()); ports.forEach(port => port.dispose()); rows.dispose(); listeners.clear();},
  });
}
