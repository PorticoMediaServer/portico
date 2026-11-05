import {encodedRoutePath} from '../../../contracts/src/routes.ts';
import {parseAnalysis} from '../media-analysis.ts';
import {preferredTrickplaySet, readItemTrickplay, type TrickplaySet, type TrickplayTarget} from '../trickplay.ts';
import {playbackFence, sameFence, type PlaybackFence, type QueuePlaybackFence} from '../../../player-core/src/index.ts';
import type {ContentScope} from '../library-content.ts';
import type {PlayerOffersData} from '../player-offers.ts';

type PlaybackAuthority = Readonly<{serverId: string}>;
type PlaybackApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
type PlaybackOffers = Pick<PlayerOffersData, 'scope' | 'current'>;

export type PlaybackTrickplaySource = Readonly<{authority: PlaybackAuthority; contentScope: ContentScope; isCurrent(): boolean}> & Readonly<{
  api: PlaybackApi;
  getSnapshot(): Readonly<{authority: PlaybackAuthority; disposed: boolean; fence?: PlaybackFence}> & Readonly<{
    offers: PlaybackOffers | null;
  }>;
}>;
export type PlaybackTrickplaySelection = Readonly<{
  set: TrickplaySet;
  fence: QueuePlaybackFence;
  sourceRevision: string;
  /** The image owner rechecks this before/after authenticated I/O and before displaying a crop. */
  isCurrent(): boolean;
}>;

/** Read the selected source's independent, session-fenced inventory revision before selecting
 * its immutable preview set. The parent owns authenticated image loading and blob lifetime. */
export async function readPlaybackTrickplay(source: PlaybackTrickplaySource, expected: PlaybackFence, target: TrickplayTarget, signal: AbortSignal): Promise<PlaybackTrickplaySelection | null> {
  if (expected.kind !== 'queue') return null;
  const fence = playbackFence(expected), captured = Object.freeze({...target}), scope = Object.freeze({...source.contentScope});
  const initial = source.getSnapshot();
  if (!initial.offers) return null;
  const isCurrent = () => {
    const snapshot = source.getSnapshot(), offers = snapshot.offers, current = offers?.current;
    return !signal.aborted && source.isCurrent() && !snapshot.disposed && snapshot.authority === source.authority
      && source.authority.serverId === scope.serverId && source.contentScope.serverId === scope.serverId && source.contentScope.viewerId === scope.viewerId
      && sameFence(snapshot.fence, fence) && captured.itemId === fence.itemId
      && offers?.scope.serverId === scope.serverId && offers.scope.libraryId === captured.libraryId && offers.scope.itemId === captured.itemId
      && current?.sessionId === fence.sessionId && current.generation === fence.generation && current.sourceId === fence.sourceId && !current.preparedVersionId;
  };
  if (!isCurrent()) return null;
  const query = new URLSearchParams({sourceId: fence.sourceId, sessionId: fence.sessionId, generation: String(fence.generation)});
  const analysisRaw = await source.api.request<unknown>(encodedRoutePath('get_items_id_analysis', {id: captured.itemId}) + '?' + query, 'GET', undefined, signal);
  if (!isCurrent()) return null;
  // analysisV1 resolves the actual session/generation; resolveSource reads inventory_objects.revision.
  // This is independent of both the set's captured revision and PlayerSourceOffer.factsRevision.
  const analysis = parseAnalysis(analysisRaw, scope, {...captured, sourceId: fence.sourceId, sessionId: fence.sessionId, generation: fence.generation});
  // Step 7: a moved viewer fence on these reads is a newer answer; session, source and generation still bind them.
  if (analysis.source.needsProbe) return null;
  const view = await readItemTrickplay((path, method, body, requestSignal) => source.api.request(path, method, body, requestSignal), scope.serverId, captured, signal);
  if (!isCurrent()) return null;
  // The wire explicitly rounds seconds to milliseconds (apiwire.MarshalDurations). The analysis
  // source keeps microseconds; compare at that same wire boundary, never with an arbitrary margin.
  const duration = Math.round(Number(analysis.source.durationUS) / 1e6 * 1000) / 1000;
  const eligible = view.sets.filter(set => !set.stale && set.sourceId === fence.sourceId && set.sourceRevision === analysis.source.revision && set.durationSeconds === duration);
  const set = preferredTrickplaySet({...view, sets: eligible}, fence.sourceId);
  return set && isCurrent() ? Object.freeze({set, fence, sourceRevision: analysis.source.revision, isCurrent}) : null;
}
