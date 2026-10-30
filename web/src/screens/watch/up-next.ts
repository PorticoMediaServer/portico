import {playbackPolicyNext, type WebPlaybackPolicyAction} from '../../platform/playback/policy.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {currentWatchPolicy, type WatchPolicyBinding} from './policy';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';

export type WatchNextBinding = Omit<WatchPolicyBinding, 'policyAction'> & Readonly<{
  policyAction(fence: QueuePlaybackFence, action: WebPlaybackPolicyAction): Promise<void>;
}>;

/** A card describes one genuine offered next occurrence on both the painted and live queue.
 * Neither a new offer on the same source nor a changed next item can inherit its old buttons. */
export function watchUpNextOffer(binding: Pick<WatchPolicyBinding, 'getSnapshot' | 'isCurrent'>, shown: WebPlaybackRuntimeSnapshot) {
  const current = currentWatchPolicy(binding, shown), prompt = current?.shown?.prompt, livePrompt = current?.live?.prompt;
  if (!current || current.shown?.attention || current.live?.attention || current.shown?.finished || current.live?.finished
    || prompt?.kind !== 'up-next' || livePrompt?.kind !== 'up-next' || prompt.offer.id !== livePrompt.offer.id
    || prompt.offer.marker?.id !== livePrompt.offer.marker?.id || prompt.sceneAfterCredits !== livePrompt.sceneAfterCredits) return null;
  const live = binding.getSnapshot(), next = playbackPolicyNext(shown, current.fence), nextLive = playbackPolicyNext(live, current.fence);
  if (!next || !nextLive || next.entryId !== nextLive.entryId || next.itemId !== nextLive.itemId || next.queueId !== nextLive.queueId || next.queueRevision !== nextLive.queueRevision) return null;
  const count = prompt.countdown, liveCount = livePrompt.countdown;
  const countdown = count && liveCount && count.automatic === liveCount.automatic && [count.seconds, liveCount.seconds].every(seconds => Number.isFinite(seconds) && seconds >= 0)
    ? {automatic: count.automatic, seconds: Math.min(count.seconds, liveCount.seconds)} : null;
  const total = shown.queueView?.postPlay.countdownSeconds;
  const progress = countdown?.automatic && total && total > 0 ? Math.max(0, Math.min(1, 1 - countdown.seconds / total)) : undefined;
  return {fence: current.fence, prompt, next, countdown, progress};
}

export async function dispatchWatchNext(binding: WatchNextBinding, shown: WebPlaybackRuntimeSnapshot, kind: 'play-now' | 'watch-credits'): Promise<boolean> {
  const current = watchUpNextOffer(binding, shown);
  if (!current) return false;
  await binding.policyAction(current.fence, kind === 'play-now' ? {kind, nextEntryId: current.next.entryId} : {kind, offerId: current.prompt.offer.id});
  return true;
}
