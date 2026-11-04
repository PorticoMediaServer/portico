import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry} from '../../../../../packages/client-core/src/search';
import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {createNativeSelection, type NativeSelection, type NativeSelectionGroup, type NativeSelectionSource} from '../selection/native-selection';
import type {NativeTitleController} from './title-controller';

/**
 * The title page's own multi-select (DESIGN: every grid, row, list and search result
 * can select several items on every platform). It covers exactly what the page draws:
 * the episodes of the show on screen and each More like this row. Only a loaded page
 * supplies a context; an unreadable or retired one offers no Select rather than a pick
 * that cannot be retried.
 */
export function createNativeTitleSelection(context: NativeShellBootstrapContext, controller: NativeTitleController, key: string): NativeSelection {
  const source: NativeSelectionSource = {
    subscribe: listener => controller.subscribe(listener),
    // A retained page that has left the route, or an authority that moved, never enters selection.
    current: () => context.current() && !controller.getSnapshot().retired,
    getSnapshot: () => {
      const state = controller.getSnapshot(), workspace = state.workspace, data = workspace?.data, detail = state.detail;
      const scope = data?.scope ?? detail?.data?.scope, revision = data?.revision ?? detail?.data?.revision;
      const viewer = context.session()?.viewer;
      const groups: NativeSelectionGroup[] = [];
      const episodes = data?.episodes.sections.flatMap(section => section.entries).filter((entry): entry is ContentEntry => entry.kind === 'episode') ?? [];
      if (episodes.length) groups.push({id: 'title-episodes', items: episodes});
      for (const row of [...(detail?.data?.related?.rows ?? []), ...(state.showRows ?? [])]) if (row.entries.length) groups.push({id: `title-related:${row.id}`, items: row.entries});
      const viewId = JSON.stringify(['native-title', key, data?.show.id ?? '', revision?.catalog ?? 0, revision?.viewer ?? 0]);
      // The page's own viewer fence and revisions are the selection's only context, and the
      // selection bar's own reads re-check them; a page still loading has none.
      const captured: SelectionContext | undefined = scope && revision && viewer && workspace?.phase === 'ready'
        && (viewer.authority === 'local' || viewer.authority === 'hosted') && viewId.length <= 4096
        ? {scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId,
          viewId, privacyFence: scope.viewerFence}, revision: {catalogRevision: String(revision.catalog), viewerRevision: String(revision.viewer)}} : undefined;
      return {key: viewId, context: captured, groups};
    },
  };
  return createNativeSelection(context, source, {area: 'show', selectAll: true});
}

/** The card's own selection entry for the menu the row's hold opened. */
export function titleCardSelection(selection: NativeSelection | undefined, entry: SearchEntry) {
  return selection?.item(entry);
}
