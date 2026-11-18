import assert from 'node:assert/strict';
import test from 'node:test';
import type {PlayerEvent, PlayerSource} from '@core/playback-v1/port.ts';
import {EngineSwitch} from '../src/platform/playback/engine-switch.ts';
import type {PlaybackMediaPort} from '../src/platform/playback/web-playback.ts';

/** A player that records what it was asked and can send an event. */
function fake(name: string, calls: string[]) {
  const listeners = new Set<(event: PlayerEvent) => void>();
  const port = {
    load: (source: PlayerSource) => { calls.push(`${name} load ${source.generation}`); },
    play: () => { calls.push(`${name} play`); }, pause: () => {}, seek: () => {}, setRate: () => {}, selectSidecarSubtitle: () => {},
    observe: () => ({generation: 0, state: 'idle' as const, positionMs: 0, rate: 1}),
    onEvent: (listener: (event: PlayerEvent) => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    onNotice: () => () => {}, loaded: 0, settling: false,
    unload: () => { calls.push(`${name} unload`); }, suspend: () => {}, resume: () => {}, dispose: () => { calls.push(`${name} dispose`); },
    send: (generation: number) => { for (const l of listeners) l({type: 'time', observation: {generation, state: 'playing', positionMs: 1, rate: 1}}); },
  };
  return port as PlaybackMediaPort & {send(generation: number): void};
}
const source = (generation: number, engine?: 'native' | 'mpv'): PlayerSource =>
  ({url: '/v1/media/g/original', generation, mode: 'direct', startPositionMs: 0, subtitles: [], autoplay: true, ...(engine ? {engine} : {})});

test('each presentation plays in the player the server planned it for, and only that player is heard', () => {
  const calls: string[] = [], own = fake('own', calls), mpv = fake('mpv', calls);
  const player = new EngineSwitch(own, mpv), heard: number[] = [];
  player.onEvent(event => heard.push(event.observation.generation));
  player.load(source(1));
  player.load(source(2, 'mpv'));
  player.play();
  own.send(1); mpv.send(2);
  player.load(source(3, 'native'));
  assert.deepEqual(calls, ['own load 1', 'own unload', 'mpv load 2', 'mpv play', 'mpv unload', 'own load 3']);
  assert.deepEqual(heard, [2], 'the unloaded page player is not heard while mpv holds the presentation');
  player.dispose();
  assert.deepEqual(calls.slice(-2), ['own dispose', 'mpv dispose']);
});
