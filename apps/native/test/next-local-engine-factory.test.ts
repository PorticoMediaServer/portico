import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as engineChoice from '../src-next/platform/playback/engine-choice.ts';
import * as engineMedia from '../src-next/platform/playback/engine-media.ts';
import * as capabilityReadiness from '../src-next/platform/playback/capability-readiness.ts';

async function factory({available = true, setting = 'automatic', container, subtitleRendering, previousSession}: {available?: boolean; setting?: string; container?: string; subtitleRendering?: string; previousSession?: object} = {}) {
  let captured: any;
  const containerSessions: unknown[] = [];
  const runtime = {installSnapshotCapture() {}, getSnapshot: () => ({}),
    path: {session: () => undefined}, queue: {currentSession: () => previousSession, playEntry() {}},
    coordinator: {getEngine: () => 'native'}, localContainer: (session: unknown) => {containerSessions.push(session);return container;},
    localSubtitleRendering: () => subtitleRendering};
  const {createNativeShellPlaybackAdapter} = await screenModule(new URL('../src-next/platform/playback/native.tsx', import.meta.url), {
    react: {default: {}},
    'react-native': {
      NativeModules: {PorticoPlaybackModule: {}, PorticoMpvPlaybackModule: {available}, PorticoRuntime: {playbackRequestId: () => 'request'}},
      NativeEventEmitter: class {addListener() {return {remove() {}};}},
      Platform: {OS: 'android', isTV: false, Version: 35, constants: {}}, UIManager: {hasViewManagerConfig: () => true},
      Dimensions: {get: () => ({width: 390, height: 844})}, AppState: {addEventListener: () => ({remove() {}})},
    },
    '../../../../../packages/client-core/src/playback-v1/options': {CapabilityPublisher: class {async publish() {}}},
    '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: () => ({})},
    '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
    './engine-choice': engineChoice, './engine-media': engineMedia,
    './capability-document': {deviceCapabilityDocument: () => ({})}, './NativeListeningAdapter': {},
    './runtime': {NativePlaybackRuntime: class {constructor(options: unknown) {captured = options;return runtime;}}},
    './scope-http': {PlaybackScopeHttp: class {dispose() {}}}, './dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false}, './capability-readiness': capabilityReadiness,
    '../../../platform/offline-downloads': {offlineDownloads: {}}, '../video-snapshot': {createVideoSnapshotCapture: () => ({})},
  });
  createNativeShellPlaybackAdapter({appVersion: 'test', api: {}, session: () => ({viewer: {authority: 'local', profileId: 'profile'}}),
    authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'}),
    settings: () => ({engine: setting}), subscribeSettings: () => () => {}});
  await Promise.resolve();
  captured.disposeCapabilities();
  return {choose: captured.engines.choose, containerSessions};
}

const local = (streamUrl: string) => ({id: 'local:copy:1', mode: 'local', generation: 1, streamUrl, duration: 100, resumeSeconds: 0});

test('the active Android factory automatically chooses mpv for an AVI download', async () => {
  const f = await factory(), session = local('file:///downloads/film.avi');
  assert.equal(f.choose({session}), 'mpv');
  assert.deepEqual(f.containerSessions, [session]);
  assert.equal(f.choose({session: local('file:///downloads/film.mp4')}), 'native');
});

test('the factory uses admitted download container metadata when the local path has no extension', async () => {
  const f = await factory({container: 'avi'}), session = local('file:///downloads/copy/media');
  assert.equal(f.choose({session}), 'mpv');
  assert.strictEqual(f.containerSessions[0], session);
});

test('a preceding online audio session cannot force a local video into the native engine', async () => {
  const f = await factory({previousSession: {id: 'old-audio', kind: 'audio', presentation: {generation: 1}}});
  assert.equal(f.choose({session: local('file:///downloads/film.avi')}), 'mpv');
});

test('explicit native choice, unavailable mpv and nonlocal sources retain their original selection', async () => {
  const native = await factory({setting: 'native'}), absent = await factory({available: false}), normal = await factory({container: 'avi'});
  assert.equal(native.choose({session: local('file:///downloads/film.avi')}), 'native');
  assert.equal(absent.choose({session: local('file:///downloads/film.avi')}), 'native');
  assert.equal(normal.choose({session: {...local('https://server.invalid/film.avi'), mode: 'direct'}}), 'native');
  assert.deepEqual(normal.containerSessions, [], 'download facts are never applied to an online source');
});

test('saved rich subtitles request mpv through the same local engine choice', async () => {
  const automatic = await factory({subtitleRendering: 'ass'}), pinned = await factory({setting: 'native', subtitleRendering: 'ass'});
  assert.equal(automatic.choose({session: local('file:///downloads/film.mp4')}), 'mpv');
  assert.equal(pinned.choose({session: local('file:///downloads/film.mp4')}), 'native');
  assert.equal(automatic.choose({session: {...local('https://server.invalid/film.mp4'), mode: 'direct'}}), 'native', 'a saved track cannot select an engine for an online session');
});
