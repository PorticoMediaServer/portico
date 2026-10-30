import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import type {NativePlaybackReadLifetime} from '../src-next/platform/playback/types';
import type {PlaybackFence} from '../../../packages/player-core/src';
import type {V1QueuedEntrySelection, V1QueueEditIntent, V1QueuePolicyIntent} from '../../../packages/client-core/src/playback-v1/v1-queue-player';
import * as capabilityReadiness from '../src-next/platform/playback/capability-readiness.ts';

// Contract source for BLK-PLAY-001: the Apple factory must forward the five
// already-installed runtime commands unchanged. This drives the real native.ios
// factory with mocked platform modules and a fake runtime; it does not run a
// player, touch native code or claim a device result.

const calls: {name: string; args: unknown[]; self: object}[] = [];
class FakeRuntime {
  queue = {playEntry: function (this: object, ...args: unknown[]) {calls.push({name: 'queuePlay', args, self: this});}};
  dock = {};
  installSnapshotCapture = () => {};
  isSnapshotSourceCurrent = () => true;
  refreshAuthority = async () => {};
  flush = async () => {};
  getSnapshot = () => ({fence: undefined, lease: undefined});
  subscribe = () => () => {};
  constructor(public options: unknown) {}
  start = () => {};
  startVersion = () => {};
  selectionPlayback = () => {};
  cataloguePlayback = () => {};
  change = () => {};
  chooseQuality = () => {};
  setRate = () => {};
  setForcedOnly = (...args: unknown[]) => {calls.push({name: 'setForcedOnly', args, self: this}); return true;};
  subtitleControls = () => ({});
  readSession = () => {};
  prepareExternalPresentation = () => {};
  requestPictureInPicture = () => {};
  onTransition = () => {};
  toggle = () => {};
  replay = () => {};
  retry = (...args: unknown[]) => {calls.push({name: 'retry', args, self: this});};
  setQueuePolicy = (...args: unknown[]) => {calls.push({name: 'setQueuePolicy', args, self: this});};
  editQueue = (...args: unknown[]) => {calls.push({name: 'editQueue', args, self: this});};
  selectQueuedEntry = (...args: unknown[]) => {calls.push({name: 'selectQueuedEntry', args, self: this});};
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
const nativeModules = {
  PorticoPlaybackModule: {acquire: async () => ({lease: 'lease', instanceId: 'instance'}), command: async () => ({}), release: async () => {}},
  PorticoRuntime: {playbackRequestId: () => 'request', getConstants: () => ({pictureInPictureSupported: false})},
};
const native = {
  Platform: {OS: 'ios', isTV: false, Version: '17.0'},
  NativeModules: nativeModules,
  NativeEventEmitter: class {addListener() {return {remove() {}};}},
  AppState: {addEventListener: () => ({remove() {}})},
  UIManager: {hasViewManagerConfig: () => true},
  requireNativeComponent: () => 'PorticoPlayer',
  findNodeHandle: () => 1,
};
const theme = {colors: {ink: '#000'}, platform: 'phone'};
const modules = {
  react: {...React, default: React},
  'react-native': native,
  '../../../../../packages/client-core/src/playback-v1/options': {CapabilityPublisher: class {publish = async () => {}}},
  '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: () => ({})},
  '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
  './engine-choice': {parseEngineFeatures: () => ({})},
  './dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false}, './capability-readiness': capabilityReadiness,
  './capability-document.ios': {deviceCapabilityDocument: () => ({})},
  './NativeListeningAdapter': {NativeListeningV1Adapter: class {}},
  './runtime': {NativePlaybackRuntime: FakeRuntime},
  './scope-http': {PlaybackScopeHttp: class {constructor() {}}},
  './subtitle-file-picker.ios': {appleSubtitleFilePicker: () => {}},
  '../../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported: false})}},
  '../video-snapshot': {createVideoSnapshotCapture: () => ({})},
  './types': {},
  '../../kit/theme': {useTheme: () => theme},
};

const {createNativeShellPlaybackAdapter} = await screenModule(new URL('../src-next/platform/playback/native.ios.tsx', import.meta.url), modules);
const options = {
  appVersion: 'test', session: () => ({viewer}), authority: () => authority,
  api: {baseUrl: 'https://server.invalid', getRouteConnection: () => ({})},
  settings: () => ({overrides: {}}), subscribeSettings: () => () => {},
};
const adapter = createNativeShellPlaybackAdapter(options as never);
const runtime = adapter.runtime as unknown as FakeRuntime;

test('Apple factory forwards the five existing runtime ports without substitution', () => {
  assert.strictEqual(adapter.retry, runtime.retry);
  assert.strictEqual(adapter.selectQueuedEntry, runtime.selectQueuedEntry);
  assert.strictEqual(adapter.setQueuePolicy, runtime.setQueuePolicy);
  assert.strictEqual(adapter.editQueue, runtime.editQueue);
  assert.strictEqual(adapter.setForcedOnly, runtime.setForcedOnly);
  assert.notStrictEqual(adapter.queuePlay, runtime.queue.playEntry);
});

test('forwarded commands dispatch to the exact runtime with original arguments', () => {
  calls.length = 0;
  const fence: PlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account',
    profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
  sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
  const lifetime: NativePlaybackReadLifetime = {signal: new AbortController().signal, isCurrent: () => true};
  const selection: V1QueuedEntrySelection = {queueId: 'queue', expectedRevision: '7', entryId: 'entry'};
  const editRequest = {queueId: 'queue', expectedRevision: '7', intent: {action: 'remove', entryId: 'entry'} as V1QueueEditIntent};
  const policyRequest = {queueId: 'queue', expectedRevision: '7', intent: {action: 'repeat', repeat: 'all'} as V1QueuePolicyIntent};
  adapter.retry!(fence, lifetime);
  adapter.selectQueuedEntry!(fence, selection, lifetime);
  adapter.setQueuePolicy!(fence, policyRequest, lifetime);
  adapter.editQueue!(fence, editRequest, lifetime);
  assert.deepEqual(calls.map(call => call.name), ['retry', 'selectQueuedEntry', 'setQueuePolicy', 'editQueue']);
  for (const call of calls) assert.strictEqual(call.self, runtime);
  assert.strictEqual(calls[0]!.args[0], fence);
  assert.strictEqual(calls[0]!.args[1], lifetime);
  assert.strictEqual(calls[1]!.args[0], fence);
  assert.strictEqual(calls[1]!.args[1], selection);
  assert.strictEqual(calls[1]!.args[2], lifetime);
  assert.strictEqual(calls[2]!.args[0], fence);
  assert.strictEqual(calls[2]!.args[1], policyRequest);
  assert.strictEqual(calls[2]!.args[2], lifetime);
  assert.strictEqual(calls[3]!.args[0], fence);
  assert.strictEqual(calls[3]!.args[1], editRequest);
  assert.strictEqual(calls[3]!.args[2], lifetime);
});

test('queuePlay keeps the queue receiver and forwards the exact three strings', () => {
  calls.length = 0;
  adapter.queuePlay!('queue', 'entry', '7');
  assert.equal(calls.length, 1);
  assert.equal(calls[0]?.name, 'queuePlay');
  assert.strictEqual(calls[0]?.self, runtime.queue);
  assert.deepEqual(calls[0]?.args, ['queue', 'entry', '7']);
});

test('setForcedOnly forwards the original fence and boolean to the exact runtime', () => {
  calls.length = 0;
  const fence: PlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account',
    profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
  sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
  const result = adapter.setForcedOnly!(fence, true);
  assert.equal(result, true);
  assert.equal(calls.length, 1);
  assert.equal(calls[0]?.name, 'setForcedOnly');
  assert.strictEqual(calls[0]?.self, runtime);
  assert.strictEqual(calls[0]?.args[0], fence);
  assert.strictEqual(calls[0]?.args[1], true);
});
