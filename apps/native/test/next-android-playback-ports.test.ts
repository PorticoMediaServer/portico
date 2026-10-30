import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import type {NativeExternalPresentationLifetime} from '../src-next/platform/playback/types';
import type {PlaybackFence} from '../../../packages/player-core/src';
import * as capabilityReadiness from '../src-next/platform/playback/capability-readiness.ts';

// Contract source for BLK-PLAY-006: the generic (Android/phone) factory must
// forward the already-installed external-presentation and Picture in Picture
// request ports unchanged, matching the Apple factory. This drives the real
// generic factory with mocked platform modules and a fake runtime; it does not
// run a player, touch native code or claim a device result.

const calls: {name: string; args: unknown[]; self: object}[] = [];
class FakeRuntime {
  queue = {playEntry: () => {}};
  dock = {};
  installSnapshotCapture = () => {};
  isSnapshotSourceCurrent = () => true;
  refreshAuthority = async () => {};
  flush = async () => {};
  getSnapshot = () => ({fence: undefined, lease: undefined});
  subscribe = () => () => {};
  constructor(_options: unknown) {}
  start = () => {};
  startVersion = () => {};
  startOffline = () => {};
  selectionPlayback = () => {};
  cataloguePlayback = () => {};
  change = () => {};
  chooseQuality = () => {};
  setRate = () => {};
  setForcedOnly = () => {};
  subtitleControls = () => ({});
  readSession = () => {};
  prepareExternalPresentation = (...args: unknown[]) => {calls.push({name: 'prepareExternalPresentation', args, self: this});};
  requestPictureInPicture = (...args: unknown[]) => {calls.push({name: 'requestPictureInPicture', args, self: this});};
  onTransition = () => {};
  toggle = () => {};
  replay = () => {};
  retry = () => {};
  setQueuePolicy = () => {};
  editQueue = () => {};
  selectQueuedEntry = () => {};
  seek = () => {};
  skip = () => {};
  previous = () => {};
  next = () => {};
  stop = () => {};
  goLive = () => {};
  setSleepTimer = () => {};
  beginRemote = () => {};
  updateRemote = () => {};
  dismissRemote = () => {};
  continueHere = () => {};
  restoreBookmark = () => {};
  open = () => {};
  dispose = () => {};
  captureVideoSnapshot = () => {};
}

const viewer = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile'};
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'f'.repeat(64)};
const engineModule = {acquire: async () => ({lease: 'lease', instanceId: 'instance'}), command: async () => ({}), release: async () => {}, available: true, getConstants: () => ({pictureInPictureSupported: true})};
const native = {
  Platform: {OS: 'android', isTV: false, Version: '34'},
  NativeModules: {PorticoPlaybackModule: engineModule, PorticoMpvPlaybackModule: engineModule, PorticoRuntime: {playbackRequestId: () => 'request', getConstants: () => ({pictureInPictureSupported: true})}},
  NativeEventEmitter: class {addListener() {return {remove() {}};}},
  AppState: {addEventListener: () => ({remove() {}})},
  UIManager: {hasViewManagerConfig: () => true},
  Dimensions: {get: () => ({width: 390, height: 844})},
  requireNativeComponent: () => 'PorticoPlayer',
  findNodeHandle: () => 1,
};
const modules = {
  react: {...React, default: React},
  'react-native': native,
  '../../../../../packages/client-core/src/playback-v1/options': {CapabilityPublisher: class {publish = async () => {}}},
  '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: () => ({})},
  '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
  './engine-choice': {chooseEngine: () => 'native', parseEngineFeatures: () => ({})},
  './engine-media': {planEngineOf: () => 'native', requiredSubtitleRendering: () => false},
  './capability-document': {deviceCapabilityDocument: () => ({})},
  './NativeListeningAdapter': {NativeListeningV1Adapter: class {}},
  './runtime': {NativePlaybackRuntime: FakeRuntime},
  './scope-http': {PlaybackScopeHttp: class {constructor() {}}},
  './dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false}, './capability-readiness': capabilityReadiness,
  '../video-snapshot': {createVideoSnapshotCapture: () => ({})},
  '../../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported: false})}},
  './types': {},
};

const {createNativeShellPlaybackAdapter} = await screenModule(new URL('../src-next/platform/playback/native.tsx', import.meta.url), modules);
const options = {
  appVersion: 'test', session: () => ({viewer}), authority: () => authority,
  api: {baseUrl: 'https://server.invalid', getRouteConnection: () => ({})},
  settings: () => ({overrides: {}}), subscribeSettings: () => () => {},
};
const adapter = createNativeShellPlaybackAdapter(options as never);
const runtime = adapter.runtime as unknown as FakeRuntime;

test('generic factory forwards the existing external request and Picture in Picture ports', () => {
  assert.strictEqual(adapter.prepareExternalPresentation, runtime.prepareExternalPresentation);
  assert.strictEqual(adapter.requestPictureInPicture, runtime.requestPictureInPicture);
});

test('forwarded request ports dispatch to the exact runtime fence and lifetime', () => {
  calls.length = 0;
  const fence: PlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account',
    profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
  sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
  const lifetime: NativeExternalPresentationLifetime = {signal: new AbortController().signal, isCurrent: () => true};
  void adapter.prepareExternalPresentation!(fence, lifetime);
  void adapter.requestPictureInPicture!(fence, lifetime);
  assert.deepEqual(calls.map(call => call.name), ['prepareExternalPresentation', 'requestPictureInPicture']);
  for (const call of calls) {
    assert.strictEqual(call.self, runtime);
    assert.strictEqual(call.args[0], fence);
    assert.strictEqual(call.args[1], lifetime);
  }
});
