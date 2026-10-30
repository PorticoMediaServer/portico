import type {MessageId, MessageValues} from '../../i18n/src/index.ts';

/**
 * A live viewer whose tuner a recording is about to take (B-11.27). This happens only when the
 * owner's DVR setting is "Stop watching" (`liveTunerConflict: stop-watching`); by default live
 * viewing keeps its tuner and the recording waits. The server puts the moment the tuner goes in
 * the channel session (`linear.reservationRequiredAtMs`); every player shows it the same way:
 *
 * - more than a minute away: a quiet line naming the time ("later");
 * - within the last minute: a countdown in seconds, with what happens next;
 * - at the moment itself, or when the server reports the tuner gone (`tuner_reservation_changed`),
 *   `PlaybackService` stops the channel itself and ends with `ended.reason` `tuner_reserved`, which
 *   players show as "Live TV stopped" with no retry loop.
 *
 * The deadline is set by the server's clock and moved onto this device's clock when the session
 * arrives (`parseChannelSession`, with the server's `nowMs`), so a device clock that is out does
 * not shift the countdown. The server's own stop is still what is authoritative.
 */
export const TUNER_RESERVED = 'tuner_reserved';
export const COUNTDOWN_MS = 60_000;

export type TunerHandover = Readonly<{kind: 'later'; atMs: number}> | Readonly<{kind: 'countdown'; secondsLeft: number}>;

export function tunerHandover(reservationRequiredAtMs: number | undefined, nowMs: number): TunerHandover | undefined {
  if (reservationRequiredAtMs === undefined || !Number.isFinite(reservationRequiredAtMs)) return undefined;
  const left = reservationRequiredAtMs - nowMs;
  if (left > COUNTDOWN_MS) return Object.freeze({kind: 'later' as const, atMs: reservationRequiredAtMs});
  return Object.freeze({kind: 'countdown' as const, secondsLeft: Math.max(0, Math.ceil(left / 1000))});
}

/** Whether the channel's tuner has gone to a recording: the deadline has passed or the server says so. */
export function tunerHandedOver(media: Readonly<{reservationRequiredAtMs?: number; errorCode?: string}> | undefined, nowMs: number): boolean {
  if (!media) return false;
  return media.errorCode === 'tuner_reservation_changed' || (media.reservationRequiredAtMs !== undefined && media.reservationRequiredAtMs <= nowMs);
}

/** The banner's two lines: what is happening, and what happens next. */
export function tunerHandoverLines(h: TunerHandover, t: (id: MessageId, values?: MessageValues) => string, time: (ms: number) => string): readonly [string, string] {
  return h.kind === 'later'
    ? [t('live.handover.later', {time: time(h.atMs)}), t('live.handover.next')]
    : [t('live.handover.countdown', {seconds: h.secondsLeft}), t('live.handover.next')];
}
