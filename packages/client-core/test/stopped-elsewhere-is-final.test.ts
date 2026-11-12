// What a person loses without this behaviour: they stop a film on the TV from their phone and the
// TV starts it again by itself, because the restart-after-server-restart path mistook the stop.
import assert from 'node:assert/strict';
import test from 'node:test';
import type {V1Http, V1Request, V1Response} from '../src/playback-v1/http.ts';
import type {MediaPlayer, PlayerEvent, PlayerObservation, PlayerSource} from '../src/playback-v1/port.ts';
import {PlaybackSessionController} from '../src/playback-v1/sessions.ts';

const live = {id: 'pbs_1', kind: 'vod', state: 'playing', itemId: 'itm_film', versionId: 'med_film',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 1000},
  presentation: {generation: 1, mode: 'direct', url: '/v1/media/g/original', startPositionMs: 0, engine: 'native', route: 'original', quality: {mode: 'original'}, subtitles: [], decision: {}}};
const json = (status: number, body?: unknown): V1Response => ({status, headers: {}, ...(body === undefined ? {} : {body})});

test('a session stopped from another device ends here and is not started again', async () => {
  const sent: V1Request[] = [];
  let stopped = false;
  const http: V1Http = {async send(r) {
    sent.push(r);
    if (r.method === 'POST') return json(201, live);
    if (r.method === 'PUT') return stopped ? json(410, {error: {code: 'session_ended', retry: false}}) : json(200, {reportEveryMs: 1000, generation: 1});
    if (r.method === 'GET') return json(200, {...live, state: 'ended', end: {reason: 'stopped_elsewhere'}});
    return json(204);
  }};
  const listeners = new Set<(e: PlayerEvent) => void>();
  let reading: PlayerObservation = {generation: 0, state: 'idle', positionMs: 0, rate: 1};
  let paused = false;
  const player: MediaPlayer = {load(s: PlayerSource) { reading = {generation: s.generation, state: 'playing', positionMs: s.startPositionMs, rate: 1}; },
    play() {}, pause() { paused = true; }, seek() {}, setRate() {}, selectSidecarSubtitle() {}, observe: () => reading,
    onEvent(l) { listeners.add(l); return () => listeners.delete(l); }, dispose() {}};
  const timers = {setTimer: (run: () => void, ms: number) => setTimeout(run, Math.min(ms, 5)), clearTimer: (t: unknown) => clearTimeout(t as ReturnType<typeof setTimeout>)};
  const controller = new PlaybackSessionController({http, player, timers});
  await controller.start({itemId: 'itm_film'}, {state: 'playing'});
  for (const l of listeners) l({type: 'time', observation: {generation: 1, state: 'playing', positionMs: 60_000, rate: 1}});
  stopped = true;
  await controller.flush();
  for (let n = 0; n < 40 && controller.getSnapshot().phase !== 'ended'; n++) await new Promise(r => setTimeout(r, 5));
  assert.equal(controller.getSnapshot().phase, 'ended');
  assert.equal(controller.getSnapshot().ended?.reason, 'stopped_elsewhere');
  assert.ok(paused, 'the picture stops');
  assert.equal(sent.filter(r => r.method === 'POST').length, 1, 'nothing was started again');
  controller.dispose();
});
