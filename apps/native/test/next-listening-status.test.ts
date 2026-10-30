import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {parseNativeListeningV1Event as parseNext} from '../src-next/platform/playback/NativeListeningAdapter.ts';
const session = {id: 'session', revision: '1', kind: 'audio', role: 'local', state: 'playing', itemId: 'item', queue: {queueId: 'queue', entryId: 'entry'}, lease: {reportEveryMs: 10000}, presentation: {generation: 2, mode: 'direct', url: '/v1/media/audio', startPositionMs: 0, subtitles: [], decision: {}}};
const raw = {owner: 'owner', sequence: 1, intent: 'playing', rate: 1, positionSeconds: 3, observed: true, ready: true, seeking: false, ended: false, error: null, terminal: false,
  route: 'Speaker', commandRevision: 2, deadlines: {sleepAtMs: null, passoutAtMs: null}, lastSeq: 4, durationSeconds: 200, queue: null, session,
  binding: {lease: 'lease', intentId: 1, sessionId: 'session', generation: 2, itemId: 'item'}};
for (const [name, parse] of [['next', parseNext]] as const) {
  test(`${name} native listening drops diagnostics and unknown bridge fields without changing current session facts`, () => {
    const diagnostic = 'private diagnostic /local/file?token=secret';
    const event = parse({...raw, error: diagnostic, rateError: diagnostic, notice: diagnostic, noticeCode: diagnostic, extra: diagnostic, eqApplied: true, levelingApplied: false}, 'owner', 'session');
    assert.equal(event.error, defaultI18n.t('playback.message.internal_error'));
    assert.equal(event.rateError, defaultI18n.t('playback.message.operation_unavailable'));
    assert.equal(JSON.stringify(event).includes(diagnostic), false);
    assert.equal('notice' in event, false); assert.equal('noticeCode' in event, false); assert.equal('extra' in event, false);
    assert.equal(event.session.presentation.generation, 2); assert.equal(event.lastSeq, 4);
    assert.equal(event.eqApplied, true); assert.equal(event.levelingApplied, false);
  });
  test(`${name} known recovery states use canonical copy and keep producer binding/generation checks`, () => {
    assert.equal(parse({...raw, error: 'playback_reconnecting'}, 'owner', 'session').error, defaultI18n.t('web.player.reconnecting'));
    assert.equal(parse({...raw, error: 'playback_confirmation_expired'}, 'owner', 'session').error, defaultI18n.t('playback.message.lease_expired'));
    assert.throws(() => parse({...raw, binding: {...raw.binding, generation: 1}}, 'owner', 'session'), /binding/);
    assert.throws(() => parse({...raw, eqApplied: 'yes'}, 'owner', 'session'), /render status/);
  });
}
