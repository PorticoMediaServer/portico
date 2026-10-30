import {sameFence, UNDO_SECONDS, type PolicyView, type QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';

/** Structural subset of the media owner's action port; no policy execution here. */
export type WatchMarkerAction = Readonly<{kind: 'skip' | 'dismiss' | 'undo'; markerId: string}>;
export type WatchPolicyBinding = Readonly<{
  getSnapshot: () => WebPlaybackRuntimeSnapshot;
  isCurrent: () => boolean;
  policyAction: (fence: QueuePlaybackFence, action: WatchMarkerAction) => Promise<void>;
  reportError: (error: unknown) => void;
}>;

export function currentWatchPolicy(binding: Pick<WatchPolicyBinding, 'getSnapshot' | 'isCurrent'>, shown: WebPlaybackRuntimeSnapshot) {
  const live = binding.getSnapshot(), fence = shown.fence;
  if (!binding.isCurrent() || shown.disposed || live.disposed || fence?.kind !== 'queue' || !sameFence(live.fence, fence)
    || shown.authority !== live.authority || live.dock.remote?.phase === 'playing'
    || shown.presentation?.kind !== 'video' || !shown.presentation.expanded || live.presentation?.kind !== 'video' || !live.presentation.expanded) return null;
  if (!sameFence(shown.presentation.fence, fence) || !sameFence(live.presentation.fence, fence)) return null;
  if (!shown.presentation.controls.includes('seek') || !live.presentation.controls.includes('seek')) return null;
  return {fence, shown: shown.presentation.policy, live: live.presentation.policy};
}

/** The policy owns this lifetime, including expiry while paused. No renderer clock. */
export function watchUndoOffer(binding: WatchPolicyBinding, shown: WebPlaybackRuntimeSnapshot) {
  const current = currentWatchPolicy(binding, shown), policy = current?.shown, live = current?.live;
  if (!policy || !live || policy.attention || live.attention || policy.finished || live.finished
    || !policy.undo || !live.undo || policy.undo.marker.id !== live.undo.marker.id || policy.undo.marker.kind !== live.undo.marker.kind) return null;
  if ([policy.undo.remainingSeconds, live.undo.remainingSeconds].some(value => !Number.isFinite(value) || value <= 0 || value > UNDO_SECONDS)) return null;
  const remainingSeconds = Math.min(policy.undo.remainingSeconds, live.undo.remainingSeconds);
  return {marker: policy.undo.marker, remainingSeconds, fraction: remainingSeconds / UNDO_SECONDS};
}

function offersMarker(policy: PolicyView | null, action: WatchMarkerAction): boolean {
  if (!policy || policy.attention || policy.finished) return false;
  if (action.kind === 'undo') return policy.undo?.marker.id === action.markerId && policy.undo.remainingSeconds > 0;
  if (policy.prompt?.kind === 'skip') return policy.prompt.marker.id === action.markerId;
  // The drawn credits card can skip its real closing marker. Dismiss is still skip-only;
  // Watch credits dismisses the distinct offer through the media owner's offer action.
  const marker = policy.prompt?.kind === 'up-next' ? policy.prompt.offer.marker : undefined;
  return action.kind === 'skip' && marker?.id === action.markerId && (marker.kind === 'credits' || marker.kind === 'outro');
}

/** Marker identity goes to the real policy owner; the UI never supplies a seek position. */
export async function dispatchWatchMarker(binding: WatchPolicyBinding, shown: WebPlaybackRuntimeSnapshot, action: WatchMarkerAction): Promise<boolean> {
  const current = currentWatchPolicy(binding, shown);
  if (!current || !offersMarker(current.shown, action) || !offersMarker(current.live, action)) return false;
  await binding.policyAction(current.fence, action);
  return true;
}
