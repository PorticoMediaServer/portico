import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SavedProjection, SavedRoute} from '../../../../../packages/client-core/src/saved';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {catalogueSelectionSource, createNativeSelection, type NativeCatalogueSelectionSnapshot, type NativeSelection} from '../selection/native-selection';

/** The two authorized Saved projections and their change signal: everything this selection
 * reads from a destination. The phone hub is one source; the TV Saved destination is adapted
 * into the same shape, so both screens share one selection owner with no second store.
 * The projections are the canonical Saved mirror's own; this owner copies neither their
 * sections nor their entry union, so the parser stays the one authority for both screens. */
export type NativeSavedSelectionSource = Readonly<{
  getSnapshot: () => Readonly<{phase: string;
    myList: Readonly<{projection?: SavedProjection | null; route?: SavedRoute | null}>;
    favorites: Readonly<{projection?: SavedProjection | null; route?: SavedRoute | null}>;
    myListSquares?: Readonly<{projection?: SavedProjection | null; route?: SavedRoute | null}>}>;
  subscribe: (listener: () => void) => () => void;
}>;

/** Adapt only arrived Saved projections, including the separately paged square
 * shelf. No fetched page is promoted to whole-query membership. */
export function createNativeSavedSelection(context: NativeShellBootstrapContext, destination: NativeSavedSelectionSource,
  activation: NativeDestinationActivationPort): NativeSelection {
  const source = {
    subscribe: destination.subscribe,
    getSnapshot: (): NativeCatalogueSelectionSnapshot => {
      const snapshot = destination.getSnapshot(), viewer = context.session()?.viewer, authority = viewer?.authority;
      const lists = [snapshot.myList, snapshot.myListSquares, snapshot.favorites].flatMap((list, index) => list?.projection
        ? [{id: String(index), list, projection: list.projection}] : []);
      const first = lists[0]?.projection;
      const key = JSON.stringify(lists.map(({id, list, projection}) => [id, list.route, projection.scope.viewerFence, projection.revision, projection.query]));
      const admitted = snapshot.phase === 'ready' && first && viewer && (authority === 'local' || authority === 'hosted');
      return {key, witness: snapshot.myList, context: admitted ? {
        scope: {serverId: viewer.serverId, authority, accountId: viewer.accountId, profileId: viewer.profileId,
          viewId: JSON.stringify(['native-saved', snapshot.myList.route, first.scope.entityId]), privacyFence: first.scope.viewerFence},
        revision: {catalogRevision: String(first.revision.catalog), viewerRevision: String(first.revision.viewer)},
      } : null, sections: lists.flatMap(({id, projection}) => projection.sections.map(section => ({id: `${id}:${section.id}`,
        entries: section.entries.filter((entry): entry is ContentEntry => entry.kind !== 'playlist' && entry.kind !== 'playlist_entry' && entry.kind !== 'reading_group')})))};
    },
  };
  return createNativeSelection(context, catalogueSelectionSource(source, activation), {area: 'saved', aggregates: true});
}

/** Pushed and inline Saved pages keep the canonical source's own revision domain.
 * PersonalSaved's server resource/history revisions are not compared with
 * Detail/catalogue revisions. Appending a page in that revision keeps selection. */
export function createNativeSavedViewSelection(context: NativeShellBootstrapContext,
  destination: import('./view-model').NativeSavedViewDestination, activation: NativeDestinationActivationPort) {
  return createNativeSelection(context, catalogueSelectionSource({
    subscribe: destination.subscribe,
    getSnapshot: (): NativeCatalogueSelectionSnapshot => {
      const snapshot = destination.getSnapshot(), viewer = context.session()?.viewer;
      const projection = snapshot.saved?.projection, personal = snapshot.personal;
      const fence = projection?.scope.viewerFence ?? personal?.viewerFence;
      const admitted = !snapshot.error && !snapshot.saved?.error && !personal?.error && !!fence
        && (snapshot.phase === 'ready' || snapshot.phase === 'loading' && !!(projection || personal?.resource || personal?.entries.length));
      const revision = projection ? {catalogRevision: String(projection.revision.catalog), viewerRevision: String(projection.revision.viewer)}
        : {catalogRevision: JSON.stringify(['saved-resource', personal?.resource?.revision ?? personal?.revision]), viewerRevision: JSON.stringify(['saved', personal?.revision])};
      const entries = (projection?.sections ?? []).map(section => ({id: section.id, entries: section.entries.flatMap(entry =>
        entry.kind === 'playlist_entry' ? entry.hidden ? [] : [entry.media]
          : entry.kind === 'playlist' || entry.kind === 'reading_group' ? [] : [entry])}));
      if (personal) entries.push({id: 'personal', entries: personal.entries.flatMap(entry => !entry.hidden && entry.media ? [entry.media] : [])});
      return {key: JSON.stringify(['native-saved-view', snapshot.route, projection?.query]), witness: snapshot.saved ?? personal,
        context: admitted && viewer && (viewer.authority === 'local' || viewer.authority === 'hosted') ? {
          scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId,
            viewId: JSON.stringify(['native-saved-view', snapshot.route]), privacyFence: fence!}, revision,
        } : null, sections: entries};
    },
  }, activation), {area: 'saved', aggregates: true});
}
