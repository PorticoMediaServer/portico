import type {MessageId} from '../../../../../packages/i18n/src';
import type {NativeListeningStatus} from '../../../../../packages/client-core/src/playback/native-listening';

/** Native diagnostic payloads are not display text. Keep only installed status
 * facts and translate known machine states through the existing catalogue. */
export function nativeListeningStatus(raw: Record<string, any>, text: (key: MessageId) => string): NativeListeningStatus {
  const error = !raw.error ? null : raw.error === 'playback_reconnecting' ? text('web.player.reconnecting')
    : raw.error === 'playback_confirmation_expired' || raw.error === 'confirmation_expired' ? text('playback.message.lease_expired')
      : raw.error === 'audio_session_resume_failed' ? text('listening.paused') : text('playback.message.internal_error');
  const binding = raw.binding === null ? null : Object.freeze({lease: raw.binding.lease, intentId: raw.binding.intentId,
    sessionId: raw.binding.sessionId, generation: raw.binding.generation, itemId: raw.binding.itemId});
  return Object.freeze({owner: raw.owner, sequence: raw.sequence, intent: raw.intent, rate: raw.rate,
    positionSeconds: raw.positionSeconds, observed: raw.observed, ready: raw.ready, seeking: raw.seeking, ended: raw.ended,
    terminal: raw.terminal, error, route: raw.route, commandRevision: raw.commandRevision, binding,
    deadlines: Object.freeze({sleepAtMs: raw.deadlines.sleepAtMs, passoutAtMs: raw.deadlines.passoutAtMs}),
    ...(raw.effectiveRate !== undefined ? {effectiveRate: raw.effectiveRate} : {}),
    ...(raw.supportedRates !== undefined ? {supportedRates: Object.freeze([...raw.supportedRates])} : {}),
    ...(raw.rateWaiting !== undefined ? {rateWaiting: raw.rateWaiting} : {}),
    ...(raw.rateError !== undefined ? {rateError: raw.rateError ? text('playback.message.operation_unavailable') : ''} : {}),
    ...(raw.levelingApplied === true && raw.levelingGainDb !== undefined ? {levelingGainDb: raw.levelingGainDb} : {}),
    ...Object.fromEntries(['rendering', 'eqApplied', 'levelingApplied', 'timerStopped', 'expiredTimerRevision', 'policyRevision'].filter(key => raw[key] !== undefined).map(key => [key, raw[key]])),
  });
}
