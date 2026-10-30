import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as capabilityReadiness from '../src-next/platform/playback/capability-readiness.ts';

test('Apple retained view receives only the runtime request counter and exact binding, including source retirement', async () => {
  const h = hooks();
  const binding = {lease: 'lease', intentId: 3, sessionId: 'session', generation: 7, itemId: 'item'};
  let state: any = {lease: '', fence: undefined}, captured: any;
  const commands: unknown[] = [], hosts: string[] = [];
  const runtime = {
    getSnapshot: () => state, subscribe: () => () => {}, installSnapshotCapture() {}, attachSurface: () => () => {},
    setSubtitleDisplay() {}, setSurfaceTag() {},
    queue: {playEntry: () => {assert.fail('unexpected queue.playEntry');}},
    prepareExternalPresentation: async (...args: unknown[]) => {commands.push(['prepare', ...args]);return binding;},
    requestPictureInPicture: async (...args: unknown[]) => {commands.push(['request', ...args]);return false;},
  };
  const {createNativeShellPlaybackAdapter} = await screenModule(new URL('../src-next/platform/playback/native.ios.tsx', import.meta.url), {
    react: h.react,
    'react-native': {
      NativeModules: {PorticoPlaybackModule: {}, PorticoRuntime: {playbackRequestId: () => 'request', getConstants: () => ({pictureInPictureSupported: true})}},
      NativeEventEmitter: class {addListener() {return {remove() {}};}},
      Platform: {OS: 'ios', isTV: false, Version: 27}, UIManager: {hasViewManagerConfig: () => true},
      requireNativeComponent: (name: string) => {hosts.push(name);return name;}, findNodeHandle: () => 17,
      AppState: {addEventListener: () => ({remove() {}})},
    },
    '../../../../../packages/client-core/src/playback-v1/options': {CapabilityPublisher: class {async publish() {}}},
    '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: () => ({})},
    '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
    './engine-choice': {parseEngineFeatures: () => ({})},
    './dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false}, './capability-readiness': capabilityReadiness,
    './capability-document.ios': {deviceCapabilityDocument: () => ({})},
    './NativeListeningAdapter': {},
    './runtime': {NativePlaybackRuntime: class {constructor(options: unknown) {captured = options;return runtime;}}},
    './scope-http': {PlaybackScopeHttp: class {dispose() {}}},
    './subtitle-file-picker.ios': {appleSubtitleFilePicker: undefined},
    '../../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported: false})}},
    '../video-snapshot': {createVideoSnapshotCapture: () => ({})},
  });
  const adapter = createNativeShellPlaybackAdapter({
    appVersion: 'test', api: {}, session: () => ({viewer: {authority: 'local', profileId: 'profile'}}),
    authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'}),
    settings: () => ({engine: 'native'}), subscribeSettings: () => () => {},
  });
  assert.equal(captured.pictureInPictureControls, true, 'the platform reports support, so the retained-view port is installed');
  const props = {expandedBounds: {x: 0, y: 0, width: 800, height: 450}, docked: false, subtitlesVisible: true, surfaceActive: true};
  const render = () => h.render(() => adapter.Surface(props));
  const idle = render();
  assert.equal(idle.props.pictureInPictureRequest, 0);assert.equal(idle.props.pictureInPictureBinding, null);
  state = {lease: 'lease', fence: {}, pictureInPicture: {request: 1, binding}};
  const requested = render();
  assert.equal(requested.type, idle.type);assert.equal(requested.props.pictureInPictureRequest, 1);
  assert.strictEqual(requested.props.pictureInPictureBinding, binding);
  state = {lease: '', fence: undefined, pictureInPicture: {request: 1}};
  const retired = render();
  assert.equal(retired.type, idle.type);assert.equal(retired.props.pictureInPictureRequest, 1);
  assert.equal(retired.props.pictureInPictureBinding, null, 'clear binding without resetting/issuing another request');
  assert.deepEqual(hosts, ['PorticoPlayer']);assert.deepEqual(commands, [], 'rendering props never starts a native command');
  const fence = {sessionId: 'caller'}, lifetime = {signal: new AbortController().signal, isCurrent: () => true};
  assert.strictEqual(await adapter.prepareExternalPresentation(fence, lifetime), binding);
  assert.equal(await adapter.requestPictureInPicture(fence, lifetime), false);
  assert.strictEqual(commands[0] && (commands[0] as unknown[])[1], fence);
  assert.strictEqual(commands[1] && (commands[1] as unknown[])[2], lifetime);
  captured.disposeCapabilities();
});
