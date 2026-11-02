import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {createNativeSelection, type NativeSelection, type NativeSelectionGroup, type NativeSelectionSource} from '../selection/native-selection';
import type {NativeLibrarySnapshot} from './model';

/**
 * The Discover tab's own multi-select (DESIGN: every grid, row, list and search
 * result can select several items). One owner per library page, over exactly the
 * shelves it draws, with the discover page's own viewer fence and revisions.
 */
export function discoverSelectionSource(source: Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => NativeLibrarySnapshot}>, current: () => boolean,
  viewer: () => Readonly<{serverId: string; authority: 'local' | 'hosted' | string; accountId: string; profileId: string}> | undefined): NativeSelectionSource {
  return {
    subscribe: source.subscribe,
    // A page that has left the route never enters selection.
    current: () => current(),
    getSnapshot: () => {
      const state = source.getSnapshot(), discover = state.discover, projection = discover.projection, account = viewer();
      const groups: NativeSelectionGroup[] = discover.appended
        .filter(section => section.entries.length)
        .map(section => ({id: `discover:${section.id}`, items: section.entries.filter((entry): entry is ContentEntry => !!entry.id)}));
      const viewId = JSON.stringify(['native-library-discover', state.library.id, state.tab.id, projection?.revision.catalog ?? 0, projection?.revision.viewer ?? 0, state.queryRevision]);
      // Only a loaded discover page from the signed-in viewer supplies a context; until then
      // there is nothing Select could truthfully join.
      const admitted = current() && state.phase === 'ready' && discover.phase === 'ready' && !!projection && !!account
        && (account.authority === 'local' || account.authority === 'hosted') && viewId.length <= 4096;
      const context: SelectionContext | undefined = admitted
        ? {scope: {serverId: account.serverId, authority: account.authority as 'local' | 'hosted', accountId: account.accountId, profileId: account.profileId,
          viewId, privacyFence: projection.scope.viewerFence}, revision: {catalogRevision: String(projection.revision.catalog), viewerRevision: String(projection.revision.viewer)}}
        : undefined;
      return {key: viewId, context, groups};
    },
  };
}

/** One owner for the library's Discover tab, retired with the library page. */
export function createNativeDiscoverSelection(context: NativeShellBootstrapContext, source: Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => NativeLibrarySnapshot}>, current: () => boolean): NativeSelection {
  return createNativeSelection(context, discoverSelectionSource(source, current, () => context.session()?.viewer), {area: 'library', aggregates: true});
}
