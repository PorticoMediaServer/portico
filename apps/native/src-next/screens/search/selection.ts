import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {createNativeSelection} from '../selection/native-selection';
import type {NativeSearchDestination} from './model';

/** Search keeps its own query/generation context and existing explicit-selection UX. */
export function createNativeSearchSelection(context: NativeShellBootstrapContext, source: NativeSearchDestination,
  activation: NativeDestinationActivationPort) {
  return createNativeSelection(context, {
    subscribe: listener => {const stops = [source.subscribe(listener), activation.subscribe(listener)]; return () => stops.forEach(stop => stop());},
    current: () => activation.getSnapshot().visible,
    getSnapshot: () => {
      const snapshot = source.getSnapshot(), page = snapshot.search.data, viewer = context.session()?.viewer;
      const viewId = JSON.stringify(['native-search', snapshot.search.generation, page?.query]);
      const captured: SelectionContext | undefined = snapshot.search.phase === 'ready' && page && viewer &&
        (viewer.authority === 'local' || viewer.authority === 'hosted') && viewId.length <= 4096
        ? {scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId,
          viewId, privacyFence: page.scope.viewerFence}, revision: {catalogRevision: String(page.revision.catalog), viewerRevision: String(page.revision.viewer)}} : undefined;
      return {context: captured, groups: snapshot.groups};
    },
  }, {area: 'search', selectAll: true});
}
