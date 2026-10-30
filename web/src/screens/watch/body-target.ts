import {sameFence} from '../../../../packages/player-core/src/index.ts';
import type {WatchBodyTarget} from '../detail';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';

export function watchBodyTarget(snapshot: WebPlaybackRuntimeSnapshot | undefined): WatchBodyTarget | undefined {
  const fence = snapshot?.fence, presentation = snapshot?.presentation, session = snapshot?.session, offers = snapshot?.offers;
  if (snapshot?.disposed || fence?.kind !== 'queue' || presentation?.subject.kind !== 'video' || !sameFence(presentation.fence, fence)
    || session?.id !== fence.sessionId || session.itemId !== fence.itemId || session.presentation.generation !== fence.generation
    || !offers || offers.scope.serverId !== snapshot.authority.serverId || offers.scope.serverId !== fence.playback.scope.serverId
    || offers.scope.itemId !== fence.itemId || offers.current?.sessionId !== fence.sessionId
    || offers.current.generation !== fence.generation || offers.current.sourceId !== fence.sourceId) return undefined;
  const item = presentation.subject.item, kind = item.kind;
  if (item.id !== fence.itemId || kind !== 'movie' && kind !== 'episode' && kind !== 'extra') return undefined;
  return Object.freeze({libraryId: offers.scope.libraryId, itemId: item.id, kind, ...(item.show?.id ? {showId: item.show.id} : {})});
}
export const watchBodyKey = (target: WatchBodyTarget) => JSON.stringify([target.libraryId, target.itemId, target.kind]);

