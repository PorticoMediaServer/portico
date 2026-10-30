import test from 'node:test';
import assert from 'node:assert/strict';
import {ObservationReporter, type ObservationSample} from '../src/playback-v1/observation.ts';
import {SessionsClient} from '../src/playback-v1/sessions.ts';
import {PlaybackApiError, type V1Request} from '../src/playback-v1/http.ts';
import type {PlayerObservation} from '../src/playback-v1/port.ts';

/** A hand-driven clock and timer queue: nothing runs until the test moves time. */
function clock() {
  let now = 0;
  const timers: {at: number; fn: () => void; id: number}[] = [];
  let next = 1;
  return {
    now: () => now,
    setTimer: (fn: () => void, ms: number) => { const id = next++; timers.push({at: now + ms, fn, id}); return id; },
    clearTimer: (id: unknown) => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); },
    async advance(ms: number) {
      const until = now + ms;
      for (;;) {
        timers.sort((a, b) => a.at - b.at);
        const due = timers[0];
        if (!due || due.at > until) break;
        timers.shift();
        now = due.at;
        due.fn();
        for (let i = 0; i < 10; i++) await Promise.resolve();
      }
      now = until;
      for (let i = 0; i < 10; i++) await Promise.resolve();
    },
  };
}

const reading = (positionMs: number, state: PlayerObservation['state'] = 'playing'): PlayerObservation => ({generation: 1, state, positionMs, rate: 1});

/** The server's side: one epoch, latest wins, scripted refusals. */
function server() {
  const samples: ObservationSample[] = [];
  const claims: {generation: number; replaces?: string}[] = [];
  let epoch = 0;
  const fail: (Error | undefined)[] = [];
  return {
    samples, claims, fail,
    restart() { epoch++; },
    claim: async (body: {generation: number; replaces?: string}) => { claims.push(body); epoch++; return {epoch: `epoch-${String(epoch).padStart(16, '0')}`, everyMs: 1000, leaseUntil: new Date(Date.now() + 120_000).toISOString()}; },
    send: async (sample: ObservationSample) => {
      const scripted = fail.shift();
      if (scripted) throw scripted;
      if (sample.epoch !== `epoch-${String(epoch).padStart(16, '0')}`) throw new PlaybackApiError(409, 'observation_epoch_changed', 'after_refresh');
      samples.push(sample);
      return {acceptedSeq: sample.seq, leaseUntil: new Date(Date.now() + 120_000).toISOString()};
    },
  };
}

test('one claim, then the latest reading once a second with the device clocks', async () => {
  const c = clock(), s = server();
  const r = new ObservationReporter({claim: s.claim, send: s.send, now: c.now, setTimer: c.setTimer, clearTimer: c.clearTimer});
  r.setGeneration(1);
  r.observe(reading(0));
  await c.advance(0); // the claim answers
  for (let i = 1; i <= 5; i++) { r.observe(reading(i * 1000)); await c.advance(1000); }
  assert.equal(s.claims.length, 1);
  assert.equal(s.samples.length, 5);
  assert.deepEqual(s.samples.map(x => x.seq), [1, 2, 3, 4, 5]);
  const last = s.samples.at(-1)!;
  assert.equal(last.positionMs, 5000);
  assert.ok(last.playClockMs <= last.monotonicMs, 'play clock never exceeds the device clock');
  assert.equal(last.monotonicMs, 5000);
  r.stop();
});

test('paused and buffering time is not play-clock time; a seek moves the serial', async () => {
  const c = clock(), s = server();
  const r = new ObservationReporter({claim: s.claim, send: s.send, now: c.now, setTimer: c.setTimer, clearTimer: c.clearTimer});
  r.setGeneration(1);
  r.observe(reading(0));
  await c.advance(0); // the claim answers
  await c.advance(1000);
  r.observe(reading(1000, 'paused'), 'state');
  await c.advance(3000);
  r.observe(reading(1000, 'buffering'), 'state');
  await c.advance(2000);
  r.observe(reading(30_000), 'seeked');
  await c.advance(1000);
  const last = s.samples.at(-1)!;
  assert.equal(last.playClockMs, 2000, 'one second before the pause, one after the seek');
  assert.equal(last.seekSerial, 1);
  r.stop();
});

test('a failed sample is never resent, and one failure says nothing about the connection', async () => {
  const c = clock(), s = server();
  const health: boolean[] = [];
  const r = new ObservationReporter({claim: s.claim, send: s.send, now: c.now, setTimer: c.setTimer, clearTimer: c.clearTimer, onConnection: h => health.push(h)});
  r.setGeneration(1);
  r.observe(reading(0));
  await c.advance(0); // the claim answers
  s.fail.push(new Error('offline'));
  await c.advance(1000);
  await c.advance(1000);
  assert.deepEqual(s.samples.map(x => x.seq), [2], 'the second tick carries the next seq, not a resend of the first');
  assert.deepEqual(health, []);
  // Sustained: 5 failures over 10 seconds, then recovery on the next success.
  for (let i = 0; i < 11; i++) s.fail.push(new Error('offline'));
  await c.advance(11_000);
  assert.deepEqual(health, [false]);
  await c.advance(1000);
  assert.deepEqual(health, [false, true]);
  r.stop();
});

test('after a server restart the reporter claims once, naming its old epoch', async () => {
  const c = clock(), s = server();
  const r = new ObservationReporter({claim: s.claim, send: s.send, now: c.now, setTimer: c.setTimer, clearTimer: c.clearTimer});
  r.setGeneration(1);
  r.observe(reading(0));
  await c.advance(0); // the claim answers
  await c.advance(2000);
  const old = s.samples[0].epoch;
  s.restart();
  await c.advance(1000); // refused: the epoch changed
  await c.advance(1000); // claims again, then sends
  assert.equal(s.claims.length, 2);
  assert.equal(s.claims[1].replaces, old);
  const after = s.samples.at(-1)!;
  assert.notEqual(after.epoch, old);
  assert.equal(after.seq, 1, 'a new epoch starts its own sequence');
  assert.ok(after.monotonicMs <= 1000, 'and its own clocks');
  r.stop();
});

test('an ended session stops the reporter; a skip rides every sample until one is accepted', async () => {
  const c = clock(), s = server();
  let ended = 0;
  const r = new ObservationReporter({claim: s.claim, send: s.send, now: c.now, setTimer: c.setTimer, clearTimer: c.clearTimer, onEnded: () => ended++});
  r.setGeneration(1);
  r.observe(reading(0));
  await c.advance(0); // the claim answers
  r.skipped({markerId: 'intro', mode: 'manual', positionMs: 0});
  s.fail.push(new Error('offline'));
  await c.advance(1000);
  await c.advance(1000);
  assert.equal(s.samples[0].skipped?.markerId, 'intro', 'the skip survived the failed sample');
  await c.advance(1000);
  assert.equal(s.samples[1].skipped, undefined, 'and is not repeated once accepted');
  s.fail.push(new PlaybackApiError(410, 'session_ended', 'never'));
  await c.advance(1000);
  assert.equal(ended, 1);
  assert.equal(r.snapshot().stopped, true);
});

test('the client sends an observation once, with a short deadline and no resend', async () => {
  const requests: V1Request[] = [];
  const client = new SessionsClient({send: async request => { requests.push(request); return {status: 200, headers: {}, body: {acceptedSeq: 3, leaseUntil: '2026-10-28T12:00:00Z'}}; }});
  const ack = await client.observe('session-1', {epoch: 'e'.repeat(22), seq: 3, generation: 1, state: 'playing', positionMs: 1, rate: 1, monotonicMs: 1, playClockMs: 1, seekSerial: 0});
  assert.equal(ack.acceptedSeq, 3);
  assert.equal(requests[0].method, 'PUT');
  assert.equal(requests[0].singleAttempt, true);
  assert.equal(requests[0].deadlineMs, 1500);
  assert.match(requests[0].path, /\/v1\/playback\/sessions\/session-1\/observation$/);
});
