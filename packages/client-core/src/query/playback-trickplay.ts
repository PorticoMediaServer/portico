import {readScrubPreviews, type TrickplaySet, type TrickplayTarget} from '../trickplay.ts';
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

/** The scrub previews of the version this session plays. The parent owns authenticated image
 * loading and blob lifetime, and asks only when the server offers previews. */
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
  const set = await readScrubPreviews((path, method, body, requestSignal) => source.api.request(path, method, body, requestSignal), captured.itemId, fence.sourceId, signal);
  return set && isCurrent() ? Object.freeze({set, fence, sourceRevision: set.revision, isCurrent}) : null;
}
