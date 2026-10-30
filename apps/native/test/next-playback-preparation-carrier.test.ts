import test from 'node:test';
import assert from 'node:assert/strict';
import {NativePlayback} from '../src-next/platform/playback/native-playback';
import type {StartPreparation, Timers} from '../../../packages/client-core/src/playback-v1/sessions';
import type {V1Http, V1Request} from '../../../packages/client-core/src/playback-v1/http';

// Native bridge seam only: a synthetic valid retry then terminal refusal never
// creates, installs or loads a Session. This is not producer enablement.
const timers: Timers = {now: () => 0, setTimer: callback => {queueMicrotask(callback); return 1;}, clearTimer() {}};
test('queue host forwards original sixth-context preparation notice before any Session exists', async () => {
  const requests: V1Request[] = [], notices: StartPreparation[] = [];
  const http: V1Http = {async send(request) {
    requests.push(request);
    return requests.length === 1 ? {status: 202, headers: {}, body: {retryAfterMs: 1}}
      : {status: 409, headers: {}, body: {error: {code: 'request_expired', message: 'Original operation expired.'}}};
  }};
  const path = new NativePlayback({http, timers});
  const life = new AbortController(); let captures = 0;
  try {
    await assert.rejects(path.transport.start({itemId: 'item'}, {state: 'playing'}, 'original', 10, life.signal,
      {isCurrent: () => !life.signal.aborted, onPreparing: (_wait, notice) => {notices.push(notice);}, capturePreparing: () => {captures++; return undefined;}}));
    assert.equal(requests.length, 2); assert.equal(requests[0]!.body, requests[1]!.body);
    assert.equal(requests[0]!.signal, life.signal); assert.equal(requests[1]!.signal, life.signal);
    assert.deepEqual(notices, [{messageId: 'player.preparing.slowStart', retryAfterMs: 250}], 'the canonical notice retains the pacing floor');
    assert.equal(captures, 0, 'retry-only has no resource receipt');
    assert.equal(path.controller.getSnapshot().session, undefined);
  } finally {path.dispose();}
});

test('original sixth-context retirement prevents the first native start dispatch', async () => {
  let sends = 0, notices = 0;
  const path = new NativePlayback({http: {async send() {sends++; throw new Error('must not send');}}, timers});
  try {
    await assert.rejects(path.transport.start({itemId: 'item'}, {}, 'original', 10, undefined,
      {isCurrent: () => false, onPreparing: () => {notices++;}}));
    assert.equal(sends, 0); assert.equal(notices, 0); assert.equal(path.controller.getSnapshot().session, undefined);
  } finally {path.dispose();}
});
