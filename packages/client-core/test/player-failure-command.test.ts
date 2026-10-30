import test from 'node:test';
import assert from 'node:assert/strict';
import {playerFailureCommand, playerFailureDetail, playerFailurePositionMs, type PlayerFailureCommandPorts} from '../src/player-failure-command.ts';
import {PlaybackApiError} from '../src/playback-v1/http.ts';

type Session = Readonly<{id: string; generation: number}>;

/** Stands in for one client's controller, remembering and notification. */
function ports(options: {current?: string; change?: (failure: {code: string; detail: string}, positionMs: number) => Promise<Session | undefined>} = {}) {
  const events: string[] = [];
  const calls: {sessionId?: string; code: string; detail: string; positionMs: number}[] = [];
  const value: PlayerFailureCommandPorts<Session, string> & {calls: typeof calls; events: string[]} = {
    calls, events,
    current: () => options.current,
    change: async (failure, positionMs) => {
      calls.push({sessionId: options.current, code: failure.code, detail: failure.detail, positionMs});
      events.push('change');
      return options.change ? options.change(failure, positionMs) : {id: 's1', generation: 2};
    },
    retain: session => { events.push('retain'); return session; },
    changed: session => { events.push(`changed:${session.generation}`); },
    playback: session => { events.push('playback'); return `${session.id}@${session.generation}`; },
  };
  return value;
}

test('a noncurrent session is not commanded at all', async () => {
  const p = ports({current: 'other'});
  assert.deepEqual(await playerFailureCommand(p, {sessionId: 's1', code: 'decode_error', detail: 'x', positionSeconds: 4}), {escalates: false});
  assert.deepEqual(p.calls, []);
  assert.deepEqual(p.events, []);
});

test('the re-planned session is retained, notified and mapped in that order', async () => {
  const p = ports({current: 's1'});
  assert.deepEqual(await playerFailureCommand(p, {sessionId: 's1', code: 'decode_error', detail: 'MEDIA_ERR_DECODE', positionSeconds: 37.4}), {escalates: true, session: 's1@2'});
  assert.deepEqual(p.calls, [{sessionId: 's1', code: 'decode_error', detail: 'MEDIA_ERR_DECODE', positionMs: 37400}]);
  assert.deepEqual(p.events, ['change', 'retain', 'changed:2', 'playback']);
});

test('control characters become spaces and the detail is cut at 500', () => {
  assert.equal(playerFailureDetail('a\u0000b\nc\rd\u007f'), 'a b c d ');
  assert.equal(playerFailureDetail('x'.repeat(600)), 'x'.repeat(500));
  assert.equal(playerFailureDetail(''), '');
  assert.equal(playerFailureDetail('\u001f'), ' ');
});

test('the position is rounded milliseconds and never negative', () => {
  assert.equal(playerFailurePositionMs(37.4), 37400);
  assert.equal(playerFailurePositionMs(0.0004), 0);
  assert.equal(playerFailurePositionMs(-12.7), 0);
  assert.equal(playerFailurePositionMs(-0), 0);
  // A non-finite reading is the controller's to refuse; the adapter neither invents nor hides it.
  assert.equal(Number.isNaN(playerFailurePositionMs(Number.NaN)), true);
  assert.equal(playerFailurePositionMs(Number.POSITIVE_INFINITY), Number.POSITIVE_INFINITY);
});

test('a controller that no longer holds the session is a refusal, not a failure', async () => {
  const p = ports({current: 's1', change: async () => undefined});
  assert.deepEqual(await playerFailureCommand(p, {sessionId: 's1', code: 'engine_error', detail: '', positionSeconds: 1}), {escalates: false});
  assert.deepEqual(p.events, ['change']);
});

test('unsupported media and a gone session stay refused; every other error is transient', async () => {
  const failure = (error: Error) => ports({current: 's1', change: async () => { throw error; }});
  for (const error of [new PlaybackApiError(422, 'unsupported_media', 'never'), new PlaybackApiError(404, 'not_found', 'never'), new PlaybackApiError(410, 'session_ended', 'never')]) {
    assert.deepEqual(await playerFailureCommand(failure(error), {sessionId: 's1', code: 'source_not_supported', detail: '', positionSeconds: 0}), {escalates: false});
  }
  for (const error of [new PlaybackApiError(409, 'revision_mismatch', 'never'), new PlaybackApiError(503, 'unavailable', 'backoff'), new Error('nothing'), new DOMException('Playback change retired', 'AbortError')]) {
    assert.equal(await playerFailureCommand(failure(error), {sessionId: 's1', code: 'decode_error', detail: '', positionSeconds: 0}), null);
  }
});

test('a retention or notification failure is transient, never an escalation', async () => {
  const p = ports({current: 's1'});
  const failing = {...p, retain: () => { throw new DOMException('Original preparation cleanup is settling', 'AbortError'); }};
  assert.equal(await playerFailureCommand(failing, {sessionId: 's1', code: 'decode_error', detail: '', positionSeconds: 0}), null);
  assert.deepEqual(p.events, ['change']);
});
