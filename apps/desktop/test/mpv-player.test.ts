import assert from 'node:assert/strict';
import test from 'node:test';
import {MpvPlayer} from '../src/mpv-player.ts';

/** Records the app commands and answers `mpv_observe` with the reading set last. */
function app() {
  const calls: [string, Record<string, unknown> | undefined][] = [];
  let reading = {state: 'playing', positionMs: 0, durationMs: 60_000, rate: 1, volume: 100, muted: false};
  const invoke = async <T,>(command: string, args?: Record<string, unknown>) => {
    calls.push([command, args]);
    return (command === 'mpv_observe' ? reading : undefined) as T;
  };
  return {calls, invoke, set: (next: Partial<typeof reading>) => { reading = {...reading, ...next}; }};
}
const settle = () => new Promise(resolve => setTimeout(resolve, 0));
const source = {url: '/v1/media/g/original', generation: 4, mode: 'direct' as const, startPositionMs: 30_000, subtitles: [], autoplay: true, engine: 'mpv' as const};

test('mpv is handed the server address of the presentation and followed by its readings', async () => {
  const mpv = app(), player = new MpvPlayer(mpv.invoke, path => 'http://server.test' + path);
  const heard: string[] = [];
  player.onEvent(event => heard.push(`${event.type} ${event.observation.state} ${event.observation.positionMs}`));
  player.load(source);
  await settle();
  assert.deepEqual(mpv.calls[0], ['mpv_load', {url: 'http://server.test/v1/media/g/original', startMs: 30_000, autoplay: true}]);
  assert.equal(player.loaded, 4);
  mpv.set({positionMs: 31_000});
  await (player as unknown as {poll(): Promise<void>}).poll();
  assert.deepEqual(heard, ['state playing 31000']);
  player.dispose();
});

test('a closed mpv window reads as paused where it was, and Play opens it again there', async () => {
  const mpv = app(), player = new MpvPlayer(mpv.invoke, path => 'http://server.test' + path);
  player.load(source);
  await settle();
  mpv.set({positionMs: 45_000});
  await (player as unknown as {poll(): Promise<void>}).poll();
  mpv.set({state: 'idle', positionMs: 0});
  await (player as unknown as {poll(): Promise<void>}).poll();
  assert.equal(player.observe().state, 'paused');
  assert.equal(player.observe().positionMs, 45_000);
  player.play();
  await settle(); await settle();
  assert.deepEqual(mpv.calls.at(-1), ['mpv_load', {url: 'http://server.test/v1/media/g/original', startMs: 45_000, autoplay: true}]);
  player.dispose();
});

test('the file\'s own tracks the presentation names are picked once mpv has opened it', async () => {
  const mpv = app(), player = new MpvPlayer(mpv.invoke, path => 'http://server.test' + path);
  player.load({...source, fileTracks: {audio: 2, subtitle: -1}});
  await settle();
  mpv.set({state: 'loading'});
  await (player as unknown as {poll(): Promise<void>}).poll();
  assert.ok(!mpv.calls.some(([command]) => command === 'mpv_tracks'), 'not before mpv has the file open');
  mpv.set({state: 'playing'});
  await (player as unknown as {poll(): Promise<void>}).poll();
  await (player as unknown as {poll(): Promise<void>}).poll();
  assert.deepEqual(mpv.calls.filter(([command]) => command === 'mpv_tracks'), [['mpv_tracks', {audio: 2, subtitle: -1}]]);
  player.dispose();
});
