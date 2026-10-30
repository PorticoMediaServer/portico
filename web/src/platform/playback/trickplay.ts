import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {parseAnalysis} from '@core/media-analysis.ts';
import {parseTrickplay, preferredTrickplaySet, type TrickplaySet, type TrickplayTarget} from '@core/trickplay.ts';
import {playbackFence, sameFence, type PlaybackFence, type QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot, WebPlaybackSource} from './types';

export type PlaybackTrickplaySource = Pick<WebPlaybackSource, 'authority' | 'contentScope' | 'isCurrent'> & Readonly<{
  api: Pick<WebPlaybackSource['api'], 'request'>;
  getSnapshot(): Pick<WebPlaybackRuntimeSnapshot, 'authority' | 'disposed' | 'fence'> & Readonly<{
    offers: Pick<NonNullable<WebPlaybackRuntimeSnapshot['offers']>, 'scope' | 'current'> | null;
  }>;
}>;
export type PlaybackTrickplaySelection = Readonly<{
  set: TrickplaySet;
  fence: QueuePlaybackFence;
  viewerFence: string;
  sourceRevision: string;
  /** The image owner rechecks this before/after authenticated I/O and before displaying a crop. */
  isCurrent(): boolean;
}>;

/** Read the selected source's independent, session-fenced inventory revision before selecting
 * its immutable preview set. The parent owns authenticated image loading and blob lifetime. */
export async function readPlaybackTrickplay(source: PlaybackTrickplaySource, expected: PlaybackFence, target: TrickplayTarget, signal: AbortSignal): Promise<PlaybackTrickplaySelection | null> {
  if (expected.kind !== 'queue') return null;
  const fence = playbackFence(expected), captured = Object.freeze({...target}), scope = Object.freeze({...source.contentScope});
  const initial = source.getSnapshot(), viewerFence = initial.offers?.scope.viewerFence;
  if (!viewerFence) return null;
  const isCurrent = () => {
    const snapshot = source.getSnapshot(), offers = snapshot.offers, current = offers?.current;
    return !signal.aborted && source.isCurrent() && !snapshot.disposed && snapshot.authority === source.authority
      && source.authority.serverId === scope.serverId && source.contentScope.serverId === scope.serverId && source.contentScope.viewerId === scope.viewerId
      && sameFence(snapshot.fence, fence) && captured.itemId === fence.itemId
      && offers?.scope.serverId === scope.serverId && offers.scope.libraryId === captured.libraryId && offers.scope.itemId === captured.itemId
      && offers.scope.viewerFence === viewerFence && current?.sessionId === fence.sessionId && current.generation === fence.generation && current.sourceId === fence.sourceId && !current.preparedVersionId;
  };
  if (!isCurrent()) return null;
  const query = new URLSearchParams({sourceId: fence.sourceId, sessionId: fence.sessionId, generation: String(fence.generation)});
  const analysisRaw = await source.api.request<unknown>(encodedRoutePath('get_items_id_analysis', {id: captured.itemId}) + '?' + query, 'GET', undefined, signal);
  if (!isCurrent()) return null;
  // analysisV1 resolves the actual session/generation; resolveSource reads inventory_objects.revision.
  // This is independent of both the set's captured revision and PlayerSourceOffer.factsRevision.
  const analysis = parseAnalysis(analysisRaw, scope, {...captured, sourceId: fence.sourceId, sessionId: fence.sessionId, generation: fence.generation});
  if (analysis.scope.viewerFence !== viewerFence || analysis.source.needsProbe) return null;
  const raw = await source.api.request<unknown>(encodedRoutePath('get_items_id_trickplay', {id: captured.itemId}), 'GET', undefined, signal);
  if (!isCurrent()) return null;
  const view = parseTrickplay(raw, scope.serverId, captured);
  if (view.scope.viewerFence !== viewerFence) return null;
  // The wire explicitly rounds seconds to milliseconds (apiwire.MarshalDurations). The analysis
  // source keeps microseconds; compare at that same wire boundary, never with an arbitrary margin.
  const duration = Math.round(Number(analysis.source.durationUS) / 1e6 * 1000) / 1000;
  const eligible = view.sets.filter(set => !set.stale && set.sourceId === fence.sourceId && set.sourceRevision === analysis.source.revision && set.durationSeconds === duration);
  const set = preferredTrickplaySet({...view, sets: eligible}, fence.sourceId);
  return set && isCurrent() ? Object.freeze({set, fence, viewerFence, sourceRevision: analysis.source.revision, isCurrent}) : null;
}
