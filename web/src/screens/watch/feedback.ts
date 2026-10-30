import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import type {WatchBindings} from './types';
import type {FeedbackTarget} from '../account/feedback';

/** Ruling35: keep the actual item and original full session fence. The Feedback domain
 * decides canSubmit/categories and whether it may include diagnostics, never this renderer. */
export function watchFeedbackTarget(binding: WatchBindings, shown: WebPlaybackRuntimeSnapshot) {
  const fence = shown.fence, session = shown.session, presentation = shown.presentation;
  if (shown.disposed || fence?.kind !== 'queue' || !session || !presentation || !sameFence(presentation.fence, fence)
    || session.id !== fence.sessionId || session.presentation.generation !== fence.generation || session.itemId !== fence.itemId) return undefined;
  const identity = playbackIdentity(presentation.subject, 'full', binding.environment);
  if (!identity.title) return undefined;
  const isCurrent = () => {
    const live = binding.runtime.getSnapshot();
    return !live.disposed && live.authority === shown.authority && sameFence(live.fence, fence)
      && live.session?.id === session.id && live.session.itemId === fence.itemId && live.session.presentation.generation === fence.generation;
  };
  return isCurrent() ? Object.freeze({item: Object.freeze({id: fence.itemId, title: identity.title}),
    playback: Object.freeze({fence, isCurrent, subscribe: binding.runtime.subscribe})}) : undefined;
}

/** Recheck both the domain permission and ORIGINAL rendered source on the gesture. */
export function dispatchWatchFeedback(target: FeedbackTarget | undefined, port: Readonly<{available(): boolean; open(target: FeedbackTarget): void}>): boolean {
  if (!target?.playback || !port.available() || !target.playback.isCurrent()) return false;
  port.open(target);return true;
}
