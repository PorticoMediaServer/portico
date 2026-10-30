import {playbackFailurePermanent, playbackUserMessage} from '../playback-messages.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

/**
 * What the error card says when playback stops.
 *
 * Two kinds, told apart honestly. A failure trying again can fix (the server stopped answering,
 * the stream was ended, the player could not load) keeps "Try again" as the way forward. A title
 * that cannot be played here at all (no format this browser plays and nothing the server may
 * convert it to, or the viewer may not play it) says that in one plain sentence, and what would
 * change it; "Try again" is not offered as if it might work.
 */
export type PlaybackFailure = Readonly<{permanent: boolean; title: MessageId; body?: MessageId; text?: string}>;

const bodies: Readonly<Record<string, MessageId>> = {
  stream_gone: 'web.player.error.streamGone',
  server_unreachable: 'web.player.error.unreachable',
  engine_unavailable: 'web.player.error.engineUnavailable',
  streaming_unsupported: 'web.player.error.noStreaming',
  cannot_play_here: 'web.player.error.cannotPlay',
  unsupported_media: 'web.player.error.cannotPlay',
  'This title can’t be played on this device.': 'web.player.error.cannotPlay',
  transcode_not_allowed: 'web.player.error.conversionOff',
  transcoding_disabled: 'web.player.error.conversionOff',
  'This title needs converting, and converting is turned off for this account.': 'web.player.error.conversionOff',
  'This item isn’t available.': 'web.player.error.notAvailable',
  not_found: 'web.player.error.notAvailable',
  client_update_required: 'web.player.error.updateRequired',
  'This account is already playing as many streams as it is allowed.': 'web.player.error.streamLimit',
};

export function playbackFailure(error: string | undefined): PlaybackFailure {
  const permanent = playbackFailurePermanent(error);
  const body = error ? bodies[error] : undefined;
  return Object.freeze({
    permanent,
    title: permanent ? 'web.player.error.cannotPlayTitle' : 'web.player.error.stoppedTitle',
    ...(body ? {body} : {text: playbackUserMessage(error)}),
  });
}
