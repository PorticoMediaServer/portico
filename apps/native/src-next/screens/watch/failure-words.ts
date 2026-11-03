import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativePlaybackFailureReason} from '../../platform/playback/playback-failure';
import {sessionEndMessage} from '../../../../../packages/client-core/src/session-end';
import {TUNER_RESERVED} from '../../../../../packages/client-core/src/live-handover';

/** Why the server ended this playback, when it did (a session's `end`). */
export type NativePlaybackEnd = Readonly<{reason: string; message?: string}>;
/** states 10 and 11's heading. A recording that took the tuner reads as Live TV's handover does
 * on every player (W2-A9); anything else stopped is "Playback stopped". */
export function failureTitle(ended: NativePlaybackEnd | undefined): string {
  return defaultI18n.t(ended?.reason === TUNER_RESERVED ? 'live.handover.stoppedTitle' : 'next.states.playbackStopped');
}

/** states 10 and 11: the body names what happened (or why the server ended it), then where the place is saved. An unknown
 * cause keeps the neutral sentence. */
export function failureBody(reason: NativePlaybackFailureReason | undefined, position: string, ended?: NativePlaybackEnd): string {
  const t = defaultI18n.t;
  // The server ended it: its reason, in the words every client uses (sessionEndMessage, W2-A11).
  if (ended) return sessionEndMessage(ended);
  return reason === 'connection' ? t('next.states.droppedBody', {position}) : reason === 'stream-ended' ? t('next.states.streamEndedBody', {position})
    : reason === 'unsupported' ? t('next.states.unsupportedBody', {position}) : t('next.dock.native.stoppedAt', {position});
}
/** Details' "What happened": the same classes, never one sentence for all of them. */
export function failureWhatHappened(reason: NativePlaybackFailureReason | undefined, fallback: string): string {
  const t = defaultI18n.t;
  return reason === 'connection' ? t('web.player.error.unreachable') : reason === 'stream-ended' ? t('web.player.error.streamGone')
    : reason === 'unsupported' ? t('queuePlayer.unsupported') : fallback;
}
