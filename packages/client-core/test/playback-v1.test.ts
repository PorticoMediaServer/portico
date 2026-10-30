import test from 'node:test';
import assert from 'node:assert/strict';
import {GenerationGate, ServerClock, driftCorrection, timelinePosition, type PlayerObservation} from '../src/playback-v1/index.ts';
import {FakePlaybackServer, fakeResponseError} from '../src/playback-v1/testing/fake-server.ts';

const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(setImmediate); };

/** A controllable clock and timer wheel. */
function clockwork() {
  let now = 1_000_000;
  const timers: {fn: () => void; at: number; id: number}[] = [];
  let next = 0;
  return {
    now: () => now,
    setTimer: (fn: () => void, ms: number) => { const id = ++next; timers.push({fn, at: now + ms, id}); return id; },
    clearTimer: (id: unknown) => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); },
    async advance(ms: number) {
      const end = now + ms;
      for (;;) {
        timers.sort((a, b) => a.at - b.at);
        const t = timers[0];
        if (!t || t.at > end) break;
        timers.shift(); now = t.at; t.fn(); await settle();
      }
      now = end;
      await settle();
    },
    pending: () => timers.length,
  };
}

async function started(server: FakePlaybackServer, body: Record<string, unknown> = {itemId: 'movie-1', startPositionMs: 0, state: 'playing'}, key = 'k1') {
  const r = await server.handle({method: 'POST', path: '/v1/playback/sessions', headers: {'Idempotency-Key': key}, body});
  assert.equal(r.status, 201);
  return r.body as {id: string; revision: string; presentation: {generation: number}};
}


test('fake server: idempotent start, If-Match, generations, idempotent stop and lease expiry (spec §16.1–4)', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const a = await started(server);
  const replay = await started(server);
  assert.equal(replay.id, a.id, 'same key, same body → same session');
  const reused = await server.handle({method: 'POST', path: '/v1/playback/sessions', headers: {'Idempotency-Key': 'k1'}, body: {itemId: 'other'}});
  assert.equal(reused.status, 422, 'idempotency_key_reused (spec §17.1)');
  const stale = await server.handle({method: 'PATCH', path: `/v1/playback/sessions/${a.id}`, headers: {'If-Match': '0'}, body: {state: 'paused'}});
  assert.equal(stale.status, 412);
  assert.ok((stale.body as {error: {current: unknown}}).error.current, '412 carries the current session');
  const paused = await server.handle({method: 'PATCH', path: `/v1/playback/sessions/${a.id}`, headers: {'If-Match': '1'}, body: {state: 'paused'}});
  assert.equal((paused.body as {presentation: {generation: number}}).presentation.generation, 1, 'pausing changes no bytes');
  const track = await server.handle({method: 'PATCH', path: `/v1/playback/sessions/${a.id}`, headers: {'If-Match': '2'}, body: {audio: {trackId: 'a2'}}});
  assert.equal((track.body as {presentation: {generation: number}}).presentation.generation, 2, 'a track change is a new generation');
  assert.equal((await server.handle({method: 'DELETE', path: `/v1/playback/sessions/${a.id}`})).status, 204);
  assert.equal((await server.handle({method: 'DELETE', path: `/v1/playback/sessions/${a.id}`})).status, 204, 'stop is idempotent');
  const b = await started(server, {itemId: 'movie-2'}, 'k2');
  await c.advance(121_000);
  assert.equal((await server.handle({method: 'POST', path: `/v1/playback/sessions/${b.id}/observer`, body: {generation: 1}})).status, 410, 'the lease lapsed');
});

test('clock: best-RTT offset, server time, group position and drift correction (spec §10)', () => {
  let now = 10_000;
  const clock = new ServerClock({now: () => now});
  assert.equal(clock.estimate(), undefined);
  assert.equal(clock.add({t0: 1000, t1: 6010, t2: 6020, t3: 1100}), true);
  assert.equal(clock.add({t0: 2000, t1: 7005, t2: 7006, t3: 2020}), true);
  assert.equal(clock.add({t0: 5, t1: 1, t2: 0, t3: 4}), false, 'impossible samples are ignored');
  const e = clock.estimate()!;
  assert.equal(e.rttMs, 19);
  assert.equal(Math.round(e.offsetMs), 4996);
  assert.equal(Math.round(clock.serverNow()), 14_996);
  assert.equal(timelinePosition({state: 'playing', positionMs: 1000, rate: 1, atServerTimeMs: 14_000}, 14_996), 1996);
  assert.equal(timelinePosition({state: 'paused', positionMs: 1000, rate: 1, atServerTimeMs: 0}, 99_999), 1000);
  assert.deepEqual(driftCorrection(1000, 1020), {kind: 'none'});
  const nudge = driftCorrection(1000, 1200);
  assert.ok(nudge.kind === 'rate' && nudge.rate > 1 && nudge.rate <= 1.05);
  assert.deepEqual(driftCorrection(1000, 2000), {kind: 'seek', positionMs: 2000});
  now += 11 * 60_000;
  assert.equal(clock.estimate(), undefined, 'old samples expire');
  const gate = new GenerationGate();
  assert.equal(gate.advance(3), true);
  assert.equal(gate.advance(3), false);
  assert.equal(gate.accepts(2), false);
});
