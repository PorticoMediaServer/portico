import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as capabilityReadiness from '../src-next/platform/playback/capability-readiness.ts';

async function factory({tv = false, supported = true, bridge = {localFile() {}}, override, dynamicRange = false, keepCapabilities = false}: {
  tv?: boolean; supported?: boolean; bridge?: Record<string, unknown> | null; override?: object; dynamicRange?: boolean; keepCapabilities?: boolean;
} = {}) {
  let captured: any;
  let onAppState: ((state: string) => void) | undefined;
  const effects = {facts: 0, refresh: 0, flush: 0}, published: unknown[] = [];
  const deviceCalls: string[] = [], starts: unknown[][] = [];
  const device = {
    getSnapshot: () => {deviceCalls.push('snapshot');return {supported};},
    open: () => {deviceCalls.push('open');}, connect: () => {deviceCalls.push('connect');},
    adopt: () => {deviceCalls.push('adopt');}, localMedia: () => {deviceCalls.push('file');},
    recordProgress: () => {deviceCalls.push('journal');},
  };
  const started = Promise.resolve();
  const runtime = {installSnapshotCapture() {}, startOffline: (...args: unknown[]) => {starts.push(args);return started;},
    refreshAuthority: async () => {effects.refresh++;}, flush: async () => {effects.flush++;},
    queue: {playEntry: () => {assert.fail('unexpected queue.playEntry');}}};
  const dynamicRangeMatching = await screenModule(new URL('../src-next/platform/playback/dynamic-range-matching.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {Platform: {OS: 'ios', isTV: tv}, NativeModules: {PorticoPlaybackModule: {dynamicRangeMatching: dynamicRange}}},
  });
  const {createNativeShellPlaybackAdapter} = await screenModule(new URL('../src-next/platform/playback/native.ios.tsx', import.meta.url), {
    react: {default: {}},
    'react-native': {
      NativeModules: {PorticoPlaybackModule: {}, PorticoRuntime: {playbackRequestId: () => 'request'}, PorticoCapabilities: {facts: async () => ({probe: ++effects.facts})}, ...(bridge ? {PorticoDownloads: bridge} : {})},
      NativeEventEmitter: class {addListener() {return {remove() {}};}},
      Platform: {OS: 'ios', isTV: tv, Version: 27}, UIManager: {hasViewManagerConfig: () => true},
      requireNativeComponent: () => 'PorticoPlayer', AppState: {addEventListener: (_: string, listener: (state: string) => void) => {onAppState = listener;return {remove() {onAppState = undefined;}};}},
    },
    '../../../../../packages/client-core/src/playback-v1/options': {CapabilityPublisher: class {async publish(document: unknown) {published.push(document);}}},
    '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: () => ({})},
    '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
    './engine-choice': {parseEngineFeatures: () => ({})},
    './dynamic-range-matching': dynamicRangeMatching, './capability-readiness': capabilityReadiness,
    './capability-document.ios': {deviceCapabilityDocument: (input: {native: unknown}) => ({native: input.native})},
    './NativeListeningAdapter': {},
    './runtime': {NativePlaybackRuntime: class {constructor(options: unknown) {captured = options;return runtime;}}},
    './scope-http': {PlaybackScopeHttp: class {dispose() {}}},
    './subtitle-file-picker.ios': {appleSubtitleFilePicker: undefined},
    '../../../platform/offline-downloads': {offlineDownloads: device},
    '../video-snapshot': {createVideoSnapshotCapture: () => ({})},
  });
  const adapter = createNativeShellPlaybackAdapter({
    appVersion: 'test', api: {}, session: () => ({viewer: {authority: 'local', profileId: 'profile'}}),
    authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'}),
    settings: () => ({engine: 'native'}), subscribeSettings: () => () => {},
    ...(override ? {offlineDevice: override} : {}),
  });
  await captured.capabilitiesReady();
  if (!keepCapabilities) captured.disposeCapabilities();
  return {adapter, captured, device, deviceCalls, runtime, starts, started, effects, published, appState: (state: string) => onAppState?.(state)};
}

test('Apple installs the same existing offline device and exact runtime forwarder without opening, adopting or connecting it', async () => {
  const x = await factory();
  assert.strictEqual(x.captured.offlineDevice, x.device);
  assert.equal(x.captured.nativeLocalSubtitleTiming, true);
  assert.strictEqual(x.adapter.runtime, x.runtime);
  assert.strictEqual(x.adapter.startOffline, x.runtime.startOffline);
  assert.deepEqual(x.deviceCalls, ['snapshot']);
  assert.deepEqual(x.starts, []);
  const entry = {id: 'download', itemId: 'movie'}, lifetime = {signal: new AbortController().signal, isCurrent: () => true};
  assert.strictEqual(x.adapter.startOffline(entry, lifetime), x.started);
  await x.started;
  assert.equal(x.starts.length, 1);
  assert.strictEqual(x.starts[0]![0], entry);
  assert.strictEqual(x.starts[0]![1], lifetime);
  assert.deepEqual(x.deviceCalls, ['snapshot'], 'the factory never resolves a file or writes another journal');
});

test('Apple never advertises offline playback on TV or without its supported installed native file bridge, even with a supplied device', async () => {
  const override = {getSnapshot: () => ({supported: true})};
  for (const options of [{tv: true}, {bridge: null}, {bridge: {}}, {bridge: {localFile: true}}, {supported: false}]) {
    const x = await factory({...options, override});
    assert.equal(x.captured.offlineDevice, undefined);
    assert.equal(x.adapter.startOffline, undefined);
    assert.equal('startOffline' in x.adapter, false);
    assert.deepEqual(x.starts, []);
    assert.ok(x.deviceCalls.every(call => call === 'snapshot'));
  }
});

test('an explicitly supplied offline device is preserved only behind the genuine Apple file bridge', async () => {
  const override = {};
  const x = await factory({override});
  assert.strictEqual(x.captured.offlineDevice, override);
  assert.strictEqual(x.adapter.startOffline, x.runtime.startOffline);
  assert.deepEqual(x.starts, []);
});

test('the Apple factory enables dynamic-range settings only for an implemented tvOS matching port', async () => {
  const tv = await factory({tv: true, dynamicRange: true});
  assert.equal(tv.captured.nativeDynamicRangeMatching, true);
  for (const options of [{tv: true}, {dynamicRange: true}, {}]) {
    const x = await factory(options);
    assert.equal(x.captured.nativeDynamicRangeMatching, undefined);
  }
});

test('Apple foreground probes and publishes current device facts and stops listening when disposed', async () => {
  for (const tv of [false, true]) {
    const x = await factory({tv, keepCapabilities: true});
    try {
      assert.deepEqual(x.published, [{native: {probe: 1}}]);
      x.appState('background');
      await x.captured.capabilitiesReady();
      assert.deepEqual(x.effects, {facts: 1, refresh: 0, flush: 1});
      x.appState('active');
      await x.captured.capabilitiesReady();
      assert.deepEqual(x.effects, {facts: 2, refresh: 1, flush: 1});
      assert.deepEqual(x.published, [{native: {probe: 1}}, {native: {probe: 2}}]);
      x.captured.disposeCapabilities();
      x.appState('active');
      await x.captured.capabilitiesReady();
      assert.deepEqual(x.effects, {facts: 2, refresh: 1, flush: 1});
    } finally {x.captured.disposeCapabilities();}
  }
});
