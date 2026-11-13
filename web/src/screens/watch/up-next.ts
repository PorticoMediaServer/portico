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
  // At the end of an episode the next one starts after the usual countdown (07 rule 32), and the card says when
  // (phone-player f6): the policy's end countdown, on both the painted and the live snapshot.
  const ending = currentWatchPolicy(binding, shown, true);
  if (ending?.shown?.finished && ending.live?.finished) return watchEndOffer(binding, shown, ending);
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

function watchEndOffer(binding: Pick<WatchPolicyBinding, 'getSnapshot' | 'isCurrent'>, shown: WebPlaybackRuntimeSnapshot, current: NonNullable<ReturnType<typeof currentWatchPolicy>>) {
  const end = current.shown?.endCountdown, liveEnd = current.live?.endCountdown;
  if (!end || !liveEnd || current.shown?.attention || current.live?.attention) return null;
  const live = binding.getSnapshot(), next = playbackPolicyNext(shown, current.fence), nextLive = playbackPolicyNext(live, current.fence);
  if (!next || !nextLive || next.entryId !== nextLive.entryId || next.itemId !== nextLive.itemId || next.queueId !== nextLive.queueId || next.queueRevision !== nextLive.queueRevision) return null;
  if (![end.seconds, liveEnd.seconds].every(seconds => Number.isFinite(seconds) && seconds >= 0)) return null;
  const countdown = {automatic: true as const, seconds: Math.min(end.seconds, liveEnd.seconds)};
  const total = shown.queueView?.postPlay.countdownSeconds;
  const progress = total && total > 0 ? Math.max(0, Math.min(1, 1 - countdown.seconds / total)) : undefined;
  return {fence: current.fence, prompt: null, next, countdown, progress};
}

export async function dispatchWatchNext(binding: WatchNextBinding, shown: WebPlaybackRuntimeSnapshot, kind: 'play-now' | 'watch-credits' | 'not-now'): Promise<boolean> {
  const current = watchUpNextOffer(binding, shown);
  if (!current) return false;
  if (kind === 'play-now') await binding.policyAction(current.fence, {kind, nextEntryId: current.next.entryId});
  else if (kind === 'not-now') { if (current.prompt) return false; await binding.policyAction(current.fence, {kind: 'cancel-autoplay'}); }
  else { if (!current.prompt) return false; await binding.policyAction(current.fence, {kind, offerId: current.prompt.offer.id}); }
  return true;
}
