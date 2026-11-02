import type {DetailSnapshot} from '@core/detail.ts';
import type {ShowWorkspaceData} from '@core/show-workspace.ts';

/** A show can name only the episode whose current detail offer it will play. The episode's detail
 * and the show workspace are different server documents with their own viewer fences (an item's
 * fence includes its content restrictions), so they are matched by server, library and item, never
 * by fence: comparing fences hid Play/Resume on every show (W-5). Each document still guards its
 * own fence; the detail binding refuses a stale answer on its own. */
export function showPrimary(data: ShowWorkspaceData, snapshot: Pick<DetailSnapshot, 'phase' | 'data'>) {
  const next = data.nextUp, detail = snapshot.data;
  if (snapshot.phase !== 'ready' || !next?.playback || !detail || next.libraryId !== data.scope.libraryId
    || detail.scope.serverId !== data.scope.serverId || detail.scope.libraryId !== next.libraryId
    || detail.scope.itemId !== next.playback.itemId
    || detail.item.kind !== 'episode' || detail.item.id !== next.playback.itemId) return undefined;
  const action = detail.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
  if (!action?.playback || action.playback.itemId !== next.playback.itemId) return undefined;
  const resume = (detail.item.progressSeconds ?? 0) > 0 && action.playback.startSeconds > 0;
  const length = detail.item.duration ?? 0, progress = length > 0 ? Math.min(1, Math.max(0, (detail.item.progressSeconds ?? 0) / length)) : 0;
  return {next, detail, action, resume, progress};
}
