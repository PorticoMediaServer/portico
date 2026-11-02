import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativePlaybackFailureReason} from '../../platform/playback/playback-failure';

/** states 10 and 11: the body names what happened, then where the place is saved. An unknown
 * cause keeps the neutral sentence. */
export function failureBody(reason: NativePlaybackFailureReason | undefined, position: string): string {
  const t = defaultI18n.t;
  return reason === 'connection' ? t('next.states.droppedBody', {position}) : reason === 'stream-ended' ? t('next.states.streamEndedBody', {position})
    : reason === 'unsupported' ? t('next.states.unsupportedBody', {position}) : t('next.dock.native.stoppedAt', {position});
}
/** Details' "What happened": the same classes, never one sentence for all of them. */
export function failureWhatHappened(reason: NativePlaybackFailureReason | undefined, fallback: string): string {
  const t = defaultI18n.t;
  return reason === 'connection' ? t('web.player.error.unreachable') : reason === 'stream-ended' ? t('web.player.error.streamGone')
    : reason === 'unsupported' ? t('queuePlayer.unsupported') : fallback;
}
