import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence, sameScope} from '../../../packages/player-core/src/identity.ts';
import {PlayerSessionChoices} from '../../../packages/player-core/src/session-choices.ts';
import {NativeVideoPlaybackPolicy, nativeVideoPolicyMarkers, nativeVideoPolicyPostPlay} from '../src-next/platform/playback/video-policy.ts';

// SUP-007: the real runtime method that follows the AirPlay route. The external-presentation
// preparation it calls is the PiP one, already covered on its own; here it is a recorded port.
const runtimeModule = screenModule(new URL('../src-next/platform/playback/runtime.ts', import.meta.url), {
  react: {default: {}}, 'react-native': {}, './start-failure': {}, './capacity-retry': {}, './listening-controls': {},
  '../../../../../packages/client-core/src/index': {}, '../../../../../packages/client-core/src/detail': {},
  '../../../../../packages/client-core/src/media-item': {}, '../../../../../packages/contracts/src/routes': {},
  '../../../../../packages/client-core/src/playback-v1/sessions': {}, '../../../../../packages/client-core/src/playback-v1/local-http': {},
  '../../../../../packages/client-core/src/playback-v1/http': {}, '../../../../../packages/client-core/src/playback-v1/options': {},
  '../../../../../packages/client-core/src/playback-v1/v1-queue-player': {}, '../../../../../packages/client-core/src/playback-v1/queue': {},
  '../../../../../packages/client-core/src/playback-v1/channel-control': {}, '../../../../../packages/client-core/src/playback/linear-protocol': {},
  '../../../../../packages/client-core/src/guide/directory': {}, '../../../../../packages/client-core/src/guide/legacy-source': {},
  '../../../../../packages/client-core/src/playback-v1/types': {}, '../../../../../packages/client-core/src/playback-v1/quality': {},
  '../../../../../packages/client-core/src/subtitles': {}, '../../../../../packages/client-core/src/subtitle-originals': {}, './forced-image': {},
  '../../../../../packages/client-core/src/presentation/service-text': {},
  '../../../../../packages/player-core/src': {playbackFence, sameFence, sameScope, PlayerSessionChoices},
  './NativePlaybackCoordinator': {}, './external-presentation': {}, './native-playback': {}, './scope-http': {},
  './preparing-owner': {}, './start-presentation': {}, './offline-playback': {}, './caption-style': {},
  './subtitle-style-choices': {}, './start-intent': {}, './video-policy': {NativeVideoPlaybackPolicy, nativeVideoPolicyMarkers, nativeVideoPolicyPostPlay},
  './subtitle-controls': {}, './subtitle-file-upload': {}, './engine-media': {}, './playback-failure': {}, './playback-recovery': {},
  './audio-track-selection': {}, './offline-tracks': {},
  './native-listening-kinds': {}, './selection': {}, './video-exit-permission': {},
});

const scope = {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local' as const};
const fenceFor = (sessionId: string) => playbackFence({kind: 'queue', playback: {scope, run: 1}, sessionId, itemId: 'episode', sourceId: 'version', entryId: 'entry', generation: 1});

async function fixture() {
  const {NativePlaybackRuntime} = await runtimeModule;
  const runtime = Object.create(NativePlaybackRuntime.prototype);
  const calls: {fence: unknown; lifetime: {signal: AbortSignal; isCurrent: () => boolean}; settle: (binding: unknown) => void; fail: (error: unknown) => void}[] = [];
  let sessionId = 'session';
  Object.assign(runtime, {
    airPlayActive: false, snapshot: {fence: fenceFor('session')},
    currentSession: () => ({id: sessionId}),
    prepareExternalPresentation: (fence: unknown, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) =>
      new Promise((settle, fail) => {calls.push({fence, lifetime, settle, fail});}),
  });
  const tick = () => new Promise(resolve => setImmediate(resolve));
  return {runtime, calls, tick, moveTo: (next: string) => {sessionId = next; runtime.snapshot = {fence: fenceFor(next)};}};
}

test('AirPlay video prepares burned-in captions once per session and again for the next item', async () => {
  const f = await fixture();
  f.runtime.followAirPlay(true);
  assert.equal(f.calls.length, 1); assert.ok(sameFence(f.calls[0]!.fence as never, fenceFor('session')));
  f.runtime.followAirPlay(); f.runtime.followAirPlay(true);
  assert.equal(f.calls.length, 1, 'one preparation at a time');
  f.calls[0]!.settle({sessionId: 'session'}); await f.tick();
  f.runtime.followAirPlay();
  assert.equal(f.calls.length, 1, 'a prepared session is not prepared again');
  f.moveTo('next'); f.runtime.followAirPlay();
  assert.equal(f.calls.length, 2); assert.ok(sameFence(f.calls[1]!.fence as never, fenceFor('next')));
});

test('a preparation that did not take is tried again on the next tick, and ending AirPlay retires it', async () => {
  const f = await fixture();
  f.runtime.followAirPlay(true);
  f.calls[0]!.settle(undefined); await f.tick();
  f.runtime.followAirPlay();
  assert.equal(f.calls.length, 2);
  const pending = f.calls[1]!;
  f.runtime.followAirPlay(false);
  assert.equal(pending.lifetime.signal.aborted, true); assert.equal(pending.lifetime.isCurrent(), false);
  f.runtime.followAirPlay();
  assert.equal(f.calls.length, 2, 'nothing is prepared without AirPlay');
});

test('only queue video is prepared, and a failed preparation is reported, not thrown', async t => {
  const f = await fixture(), warnings: unknown[][] = [];
  const global = globalThis as {__DEV__?: boolean}, dev = global.__DEV__;
  global.__DEV__ = true; t.after(() => {global.__DEV__ = dev;});
  t.mock.method(console, 'warn', (...args: unknown[]) => {warnings.push(args);});
  f.runtime.snapshot = {fence: undefined};
  f.runtime.followAirPlay(true);
  assert.equal(f.calls.length, 0);
  f.runtime.snapshot = {fence: fenceFor('session')};
  f.runtime.followAirPlay();
  f.calls[0]!.fail(new Error('replan refused')); await f.tick();
  assert.equal(warnings.length, 1); assert.equal(warnings[0]![0], '[airplay-captions]');
  f.runtime.followAirPlay();
  assert.equal(f.calls.length, 2, 'the next tick tries again');
});
