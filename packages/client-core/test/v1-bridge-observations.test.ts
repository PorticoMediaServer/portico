import test from 'node:test';
import assert from 'node:assert/strict';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {PlaybackApiError} from '../src/playback-v1/http.ts';

/** The native service's bridge reports with observations: one claim, then the latest reading once
 * a second. Nothing queues and nothing is resent; the end and Stop keep their reliable paths. */
function setup() {
  let now = 0;
  const samples: {seq: number; state: string; positionMs: number; seekSerial: number; skipped?: unknown}[] = [];
  const deletes: unknown[] = [];
  const claims: unknown[] = [];
  const failures: number[] = [];
  const timers: {at: number; fn: () => void; id: number}[] = [];
  let ids = 0;
  const http = {send: async (r: {method: string; path: string; body?: unknown}) => {
    if (r.path.endsWith('/observer')) { claims.push(r.body); return {status: 200, headers: {}, body: {epoch: 'e'.repeat(22), everyMs: 1000, leaseUntil: '2099-01-01T00:00:00Z'}}; }
    if (r.path.endsWith('/observation')) {
      const status = failures.shift();
      if (status) return {status, headers: {}, body: {error: {code: status === 410 ? 'session_ended' : 'server_busy'}}};
      const b = r.body as typeof samples[number]; samples.push(b);
      return {status: 200, headers: {}, body: {acceptedSeq: b.seq, leaseUntil: '2099-01-01T00:00:00Z'}};
    }
    if (r.method === 'DELETE') { deletes.push(r.body); return {status: 204, headers: {}}; }
    if (r.method === 'GET') return {status: 200, headers: {}, body: {id: 'ps_1', revision: '9', kind: 'vod', role: 'local', state: 'ended', end: {reason: 'stopped'}, presentation: {generation: 1, mode: 'stream', url: '/v1/media/x', startPositionMs: 0, engine: 'native', subtitles: []}, lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 1000}}};
    throw new Error('unexpected ' + r.method + ' ' + r.path);
  }};
  const api = new V1PlaybackApi(http as never, {now: () => now, timers: {setTimer: (fn, ms) => { const id = ++ids; timers.push({at: now + ms, fn, id}); return id; }, clearTimer: id => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); }}});
  const settle = async () => { for (let i = 0; i < 20; i++) await new Promise(r => setImmediate(r)); };
  const advance = async (ms: number) => {
    const end = now + ms;
    for (;;) {
      timers.sort((a, b) => a.at - b.at);
      const next = timers[0];
      if (!next || next.at > end) break;
      timers.shift(); now = next.at; next.fn();
      await settle();
    }
    now = end;
    await settle();
  };
  return {api, samples, deletes, claims, failures, advance, settle, timers};
}

test('steady playback sends the latest reading once a second, after one claim', async () => {
  const {api, samples, claims, advance} = setup();
  for (let s = 0; s <= 10; s++) {
    await api.progressPlayback('ps_1', {generation: 1, sequence: s, positionSeconds: s, state: 'playing'});
    await advance(1000);
  }
  assert.equal(claims.length, 1);
  assert.ok(samples.length >= 9 && samples.length <= 11, `${samples.length} samples in 11 s`);
  assert.ok(samples.every((x, i) => i === 0 || x.seq === samples[i - 1]!.seq + 1), 'sequential, never resent');
  assert.ok(samples.at(-1)!.positionMs >= 9000);
});

test('a jump in position moves the seek serial; a failed sample is replaced, not queued', async () => {
  const {api, samples, failures, advance} = setup();
  await api.progressPlayback('ps_1', {generation: 1, sequence: 1, positionSeconds: 10, state: 'playing'});
  await advance(1000);
  await api.progressPlayback('ps_1', {generation: 1, sequence: 2, positionSeconds: 300, state: 'playing'});
  failures.push(503);
  await advance(1000);
  await advance(1000);
  const last = samples.at(-1)!;
  assert.equal(last.seekSerial, 1);
  assert.equal(last.positionMs, 300_000);
  assert.ok(samples.every((x, i) => i === 0 || x.seq > samples[i - 1]!.seq), 'the failed sample was not sent again');
});

test('the end is confirmed only once the server has it; a refusal is retryable by the caller', async () => {
  const {api, samples, failures, advance} = setup();
  await api.progressPlayback('ps_1', {generation: 1, sequence: 1, positionSeconds: 1, state: 'playing'});
  await advance(1000);
  failures.push(503);
  await assert.rejects(api.progressPlayback('ps_1', {generation: 1, sequence: 2, positionSeconds: 100, state: 'ended'}), (e: unknown) => e instanceof PlaybackApiError && e.code === 'timeline_unconfirmed');
  await api.progressPlayback('ps_1', {generation: 1, sequence: 3, positionSeconds: 100, state: 'ended'});
  assert.equal(samples.at(-1)!.state, 'ended');
});

test('a skip rides the samples until one is accepted; Stop sends the final position and stops sampling', async () => {
  const {api, samples, deletes, advance, timers} = setup();
  await api.progressPlayback('ps_1', {generation: 1, sequence: 1, positionSeconds: 5, state: 'playing'});
  await advance(1000);
  await api.markerSkipped('ps_1', {markerId: 'intro', mode: 'manual', positionMs: 5000});
  await advance(1000);
  assert.deepEqual(samples.at(-1)!.skipped, {markerId: 'intro', mode: 'manual', positionMs: 5000});
  await api.progressPlayback('ps_1', {generation: 1, sequence: 2, positionSeconds: 7, state: 'paused'});
  await api.stopPlayback('ps_1');
  assert.deepEqual(deletes, [{positionMs: 7000}]);
  const sent = samples.length;
  await advance(5000);
  assert.equal(samples.length, sent, 'nothing is sent after Stop');
  assert.equal(timers.length, 0);
});

test('handing a session to a native owner stops this reporter and gives its epoch', async () => {
  const {api, samples, advance} = setup();
  await api.progressPlayback('ps_1', {generation: 1, sequence: 1, positionSeconds: 5, state: 'playing'});
  await advance(1000);
  assert.equal(api.handOverObserver('ps_1'), 'e'.repeat(22));
  const sent = samples.length;
  await api.progressPlayback('ps_1', {generation: 1, sequence: 2, positionSeconds: 6, state: 'playing'});
  await advance(3000);
  assert.equal(samples.length, sent);
});

test('a session the server ended stops sampling and is reported ended', async () => {
  const {api, failures, advance} = setup();
  const ended: string[] = [];
  api.onServerEnded((id, end) => ended.push(id + ':' + end.reason));
  await api.progressPlayback('ps_1', {generation: 1, sequence: 1, positionSeconds: 5, state: 'playing'});
  failures.push(410);
  await advance(1000);
  await advance(1000);
  assert.deepEqual(ended, ['ps_1:stopped']);
});
