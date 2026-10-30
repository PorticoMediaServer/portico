import {sameFence, type QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {playbackPolicyNext, type WebPlaybackPolicyAction, type WebPlaybackPolicyNext} from '../../platform/playback/policy.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {fenceKey} from '../../shell/playback-model';

/** The post-play action port plus the two ordinary commands the drawn dialog offers.
 * Nothing here executes policy; the runtime still admits every command against its
 * own current fence. */
export type WatchStillWatchingBinding = Readonly<{
  getSnapshot: () => WebPlaybackRuntimeSnapshot;
  isCurrent: () => boolean;
  policyAction: (fence: QueuePlaybackFence, action: WebPlaybackPolicyAction) => Promise<void>;
  replay: (fence: QueuePlaybackFence) => Promise<void>;
  stop: (fence: QueuePlaybackFence) => Promise<void>;
  reportError: (error: unknown) => void;
}>;

export type WatchStillWatchingOffer = Readonly<{
  fence: QueuePlaybackFence;
  /** Everything the dialog is drawn for: source/authority fence, queue revision and
   * occurrence, and whether the ordinary Replay command is really offered. */
  key: string;
  next: WebPlaybackPolicyNext;
  replayOffered: boolean;
}>;

/** phone-player f6e. The server's own passout check holds the end of this exact
 * occurrence; the dialog may only speak for that occurrence while both the painted
 * and live snapshots still describe it. */
export function watchStillWatching(binding: WatchStillWatchingBinding, shown: WebPlaybackRuntimeSnapshot): WatchStillWatchingOffer | null {
  const live = binding.getSnapshot();
  if (!binding.isCurrent() || shown.disposed || live.disposed) return null;
  const fence = shown.fence;
  if (!fence || fence.kind !== 'queue' || !live.fence || live.fence.kind !== 'queue' || !sameFence(live.fence, fence)) return null;
  if (shown.authority !== live.authority) return null;
  const painted = shown.presentation, current = live.presentation;
  if (painted?.kind !== 'video' || !painted.expanded || current?.kind !== 'video' || !current.expanded) return null;
  if (!sameFence(painted.fence, fence) || !sameFence(current.fence, fence)) return null;
  if (painted.policy?.attention !== 'still-watching' || current.policy?.attention !== 'still-watching') return null;
  const next = playbackPolicyNext(shown, fence), liveNext = playbackPolicyNext(live, fence);
  if (!next || !liveNext || next.entryId !== liveNext.entryId || next.itemId !== liveNext.itemId
    || next.queueId !== liveNext.queueId || next.queueRevision !== liveNext.queueRevision) return null;
  // f6e draws Replay as an ordinary command; it is shown only while both admissions
  // really carry it, never as a placeholder for a command the runtime would refuse.
  const replayOffered = painted.controls.includes('replay') && current.controls.includes('replay');
  return Object.freeze({fence, next, replayOffered, key: `${fenceKey(fence)}\u0000${next.queueRevision}\u0000${next.entryId}\u0000${replayOffered ? 'replay' : ''}`});
}
