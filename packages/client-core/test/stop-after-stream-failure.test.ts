// What a person loses without the fix: the stream fails mid-film, they close the player, and
// Continue Watching now says 0:00 (or drops the film), because the stop carried the failed
// element's position (0) instead of where the picture last was.
import assert from 'node:assert/strict';
import test from 'node:test';
import type {V1Http, V1Request, V1Response} from '../src/playback-v1/http.ts';
import type {MediaPlayer, PlayerEvent, PlayerObservation, PlayerSource} from '../src/playback-v1/port.ts';
import {PlaybackSessionController} from '../src/playback-v1/sessions.ts';

const session = (id: string) => ({id, kind: 'vod', state: 'playing', itemId: 'itm_film', versionId: 'med_film',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 1, mode: 'stream', url: `/v1/media/${id}/master.m3u8`, startPositionMs: 0, engine: 'native', route: 'converted', quality: {mode: 'original'}, subtitles: [], decision: {}}});
const json = (status: number, body?: unknown): V1Response => ({status, headers: {}, ...(body === undefined ? {} : {body})});

test('closing the player after the stream failed saves where the picture last was, not 0:00', async () => {
  const sent: V1Request[] = [];
  const http: V1Http = {async send(r) { sent.push(r); return r.method === 'POST' ? json(201, session('pbs_1')) : r.method === 'PUT' ? json(200, {reportEveryMs: 10_000, generation: 1}) : json(204); }};
  const listeners = new Set<(e: PlayerEvent) => void>();
  let reading: PlayerObservation = {generation: 0, state: 'idle', positionMs: 0, rate: 1};
  const emit = (e: PlayerEvent) => { reading = e.observation; for (const l of listeners) l(e); };
  const player: MediaPlayer = {load(s: PlayerSource) { reading = {generation: s.generation, state: 'playing', positionMs: s.startPositionMs, rate: 1}; },
    play() {}, pause() {}, seek() {}, setRate() {}, selectSidecarSubtitle() {}, observe: () => reading,
    onEvent(l) { listeners.add(l); return () => listeners.delete(l); }, dispose() {}};
  const controller = new PlaybackSessionController({http, player});
  await controller.start({itemId: 'itm_film'}, {state: 'playing'});
  emit({type: 'time', observation: {generation: 1, state: 'playing', positionMs: 1_800_000, rate: 1}});
  // The stream dies: the web element is torn down and reports 0 with its error.
  emit({type: 'error', observation: {generation: 1, state: 'error', positionMs: 0, rate: 1}, code: 'network_error'});
  await controller.stop();
  const stop = sent.find(r => r.method === 'DELETE');
  assert.ok(stop, 'the session was stopped');
  assert.equal((stop!.body as {positionMs?: number} | undefined)?.positionMs, 1_800_000);
  controller.dispose();
});
