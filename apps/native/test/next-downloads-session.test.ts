import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeUserMutationStore} from '../src-next/platform/user-mutations.ts';
import {NativeUserMutationSelectionOwner} from '../src-next/platform/user-mutation-selection.ts';
import {userMutationBridge} from './helpers/user-mutation-bridge.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineModule} from './helpers/offline-module.mjs';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {resolve = done;});
  return {promise, resolve};
};
const cancelled = (error: unknown) => (error as {code?: string})?.code === 'cancelled';
type Fixture = ReturnType<typeof fixture>;
let active: Fixture;

function fixture() {
  const viewer = {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'member'};
  const session = {viewer, accessToken: 'fixture-token', sessionFamilyId: 'family', authorizationHorizon: 'horizon', deviceId: 'device'};
  const authority = {...viewer, epoch: 'epoch', viewerFence: 'viewer-fence'};
  const state = {
    current: true, connects: 0, disconnects: 0, starts: 0, stops: 0, disposed: 0, playbackDisposed: 0,
    followed: [] as string[], adopted: [] as unknown[], wants: [] as unknown[], calls: [] as string[],
    requests: [] as AbortSignal[], services: [] as FakeDownloadsService[], errors: [] as unknown[],
    connection: undefined as undefined | {api: any; service: FakeDownloadsService},
    open: async () => {}, request: async (_path: string, _signal?: AbortSignal): Promise<unknown> => ({}),
  };
  const device = {
    getSnapshot: () => ({supported: true, ready: true, entries: []}), subscribe: () => () => {},
    open: () => state.open(), adopt: (owner: unknown) => {state.adopted.push(owner);},
    connect: (api: any, service: FakeDownloadsService) => {state.connects++; state.connection = {api, service};},
    disconnect: () => {state.disconnects++; state.connection = undefined;},
    want: (...args: unknown[]) => {state.wants.push(args);}, wantPublication: async () => {}, display: () => {},
    setSettings: async () => {}, pause: async () => {}, resume: async () => {}, remove: async () => {}, removeOthers: async () => {},
    admission: (bytes: number) => bytes > 100 ? 'Needs space' : '', cancelPending: async (_id: string) => {},
    playable: () => '', playabilityReason: () => null, note: () => '', displayFacts: async () => ({}), localMedia: async () => ({}),
  };
  const context = {session: () => session, authority: () => authority, current: () => state.current,
    api: {baseUrl: 'https://server.test', mediaUrl: (path: string) => path,
      request: (path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
        state.calls.push(path); if (signal) state.requests.push(signal); return state.request(path, signal);
      }},
  } as unknown as NativeShellBootstrapContext;
  return {state, viewer, session, authority, context, device, mutationBridge: userMutationBridge()};
}

class FakeDownloadsService {
  readonly owner: Fixture;
  readonly api: any;
  constructor(api: any) {this.api = api; this.owner = active; active.state.services.push(this);}
  start() {this.owner.state.starts++;}
  stop() {this.owner.state.stops++;}
  followDevice(id: string) {this.owner.state.followed.push(id);}
  dispose() {this.owner.state.disposed++;}
}

// Execute the actual coordinator, custody, lifetime and bootstrap. Only their
// external device/service/platform dependencies are stood in; no source rewrite
// or installed React/native toolchain is needed for these lifetime assertions.
const singleton = new Proxy({}, {
  get: (_target, name) => active.device[name as keyof Fixture['device']],
  ownKeys: () => Reflect.ownKeys(active.device),
  getOwnPropertyDescriptor: () => ({configurable: true, enumerable: true}),
});
const deviceModules: Record<string, Record<string, unknown>> = {
  '../../../../../packages/client-core/src/downloads-service': {DownloadsService: FakeDownloadsService},
  '../../platform/offline-downloads': {offlineDownloads: singleton, createNativeOfflineDownloadsAdapter: (_open: unknown, device: unknown) => device},
  './playback': {createNativeOfflineEntryPlayback: () => {
    const owner = active; return {dispose: () => {owner.state.playbackDisposed++;}};
  }},
};
const noop = () => {};
const subscribe = () => noop;
const preferences = {effective: {'playback.skipBackMs': 10_000}};
const account = {account: {username: 'member', role: 'member'}, profiles: [{id: 'profile', name: 'Profile', art: 'mint', revision: 1, pinRevision: 1}]};
const home = {load: async () => {}, getSnapshot: () => ({document: {serverId: 'server', viewerFence: 'viewer-fence'}}), subscribe, dispose: noop};
const playback = {getSnapshot: () => ({dock: {}}), subscribe, dispose: noop};
const selected = () => ({signIn: 'local', connection: {session: active.session, logicalOrigin: 'https://server.test', pin: {serverId: 'server', fingerprint: 'fingerprint'}}});
const localApi = () => {
  const api = new HttpLocalApi('https://server.test');
  api.request = active.context.api.request;
  return api;
};
const bootModules: Record<string, Record<string, unknown>> = {
  'react-native': {NativeModules: {}, NativeEventEmitter: class {addListener() {return {remove: noop};}}, AppState: {currentState: 'active', addEventListener: () => ({remove: noop})}, Dimensions: {get: () => ({width: 390, height: 844}), addEventListener: () => ({remove: noop})}, Platform: {OS: 'ios', isTV: false}},
  '../screens/settings-live/lifetime': {NativeScreenLifetime: class {}},
  '../screens/settings-live/device-client-report': {sendNativeDeviceReport: noop},
  '../screens/settings-live/together-playback': {bindNativeTogether: subscribe},
  '../../../../packages/client-core/src/audiobook-preferences': {audiobookPreferences: () => ({})},
  '../../../../packages/contracts/src/routes': {routePath: (name: string) => name},
  '../../../../packages/client-core/src/home-service': {HomeService: class {}},
  '../../../../packages/client-core/src/identity-client': {IdentityClient: class {avatars = async () => []; }},
  '../../../../packages/client-core/src/preferences': {currentPreferenceSnapshot: (_previous: unknown, next: unknown) => next},
  '../../../../packages/client-core/src/library-list': {parseLibraryList: (value: unknown) => value},
  '../../../../packages/client-core/src/session-refresh': {LocalSessionRefresher: class {stop = noop; wake = noop;}},
  '../../../../packages/client-core/src/playback-v1/events': {EventsClient: class {onResync = subscribe; on = subscribe; onAny = subscribe;}},
  '../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: (value: unknown) => value},
  '../../../../packages/client-core/src/event-feed': {runEventFeed: subscribe},
  '../../../../packages/client-core/src/artwork': {ambientArtworkSource: noop},
  '../platform/user-mutations': {
    NativeUserMutationStore: class extends NativeUserMutationStore {constructor() {super(active.mutationBridge);}},
    retainNativeUserMutationSelection: (store: NativeUserMutationStore, scope: any, report: (error: unknown) => void) =>
      new NativeUserMutationSelectionOwner(async work => work({...active.viewer, origin: 'https://server.test', familyId: active.session.sessionFamilyId, deviceId: active.session.deviceId}), subscribe).activate(store, scope, report),
  },
  '../platform/session': {restoreNativeConnection: async () => ({api: localApi(), session: active.session}), readNativeSelectedViewer: async () => ({session: active.session, serverUrl: 'https://server.test'}), trackNativeConnection: noop},
  '../platform/installation': {beginNativeInstallation: () => {
    const owner = active;
    return {current: () => owner.state.current, dispose: noop, prepare: async () => ({saved: {viewer: {session: owner.session, serverUrl: 'https://server.test'}}, env: {}, identity: {appVersion: 'test'}})};
  }},
  '../platform/queue-storage': {NativeQueueStore: class {async change(_key: string, change: (value: unknown) => {value: unknown}) {return change({}).value;}}},
  '../platform/device-settings': {bindNativeDeviceSettings: noop, nativeDeviceSettings: () => ({read: noop})},
  '../platform/server-preferences': {bindNativeServerPreferences: noop},
  '../platform/server-display': {rememberNativeServerDisplay: async () => ({name: 'Server'}), readNativeServerDisplays: async () => [{name: 'Server'}]},
  '../platform/app-session-storage': {nativeAppSessions: {selected: async () => selected(), read: async () => undefined}, nativeAppSessionStorage: {change: async () => {}}},
  '../platform/accounts': {nativeAccounts: {}}, '../platform/account-authority': {hostedUrl: 'https://services.invalid'},
  '../platform/sign-in-ended': {nativeSignInEnded: noop, rememberNativeSignInHint: async () => {}, readNativeSignInHint: async () => undefined},
  '../platform/identity-failure': {nativeIdentityFailure: noop, nativeRouteIdentityFailure: noop},
  '../platform/profile-avatars': {profilePictureSource: noop}, '../kit/artwork': {nativeAmbientSource: noop},
  './library-memory': {LastLibraryMemory: class {}}, '../platform/playback': {createNativeShellPlaybackAdapter: () => playback},
  './playback-settings': {nativePlaybackSettings: () => ({})},
  './remembered-playback-settings': {rememberNativePlaybackPreferences: async () => {}},
  './remembered-reading-settings': {rememberNativeReadingPreferences: async () => {}},
  '../../../../packages/client-core/src/lyrics': {LyricsService: class {}},
  '../../../../packages/client-core/src/music-preferences': {musicAudioEffects: noop},
  '../platform/play-on/controller': {NativePlayOn: class {dispose = noop;}},
  '../../../../packages/client-core/src/installation': {installationIdentity: () => ({name: 'Device'})},
  '../screens/settings/runtime-region': {nativeRuntimeRegionText: () => ({refreshDeviceRegion: noop})},
  '../screens/settings/appearance-policy': {nativeAppearanceCardSizePercent: noop, nativeAppearanceMotion: noop, nativeAppearanceShowBackdrops: noop, nativeAppearanceTextSize: noop},
  '../screens/settings/top-shelf-locale': {bindNativeTopShelfLocale: noop},
  '../screens/native-screen-services': {nativeScreenServices: () => ({reportError: (error: unknown) => active.state.errors.push(error)})},
  './routes': {NativeShellNavigation: class {dispose = noop; subscribe = subscribe; getSnapshot = () => ({route: {kind: 'shell', destination: {kind: 'home'}}, revision: 1});}},
  './watch-outlet': {NativeWatchOutlet: class {invalidate = noop; dispose = noop;}},
  './bootstrap-services': {NativeShellBootstrapServices: class {
    home = home; dispose = noop;
    request = async () => ({items: []}); readAccount = async () => account;
    read = async () => ({preferences, unread: 0, hasLiveTV: false});
    readPreferences = async () => preferences; readUnread = async () => 0; readGuideAvailability = async () => false;
  }, nativeShellDirectViewer: noop, nativeShellPermissionDenied: () => false},
  './account-binding': {createNativeShellAccountBinding: () => ({assertCurrent: async () => {}})},
  // Home freshness has its own suites; this shell test only needs it inert.
  './freshness': {NativeReadOrder: class {}, nativeQueuedRefresh: () => async () => {},
    NativeShellFreshness: class {attach = noop; dispose = noop; setActive = noop; setHomeOpen = noop; invalidateHome = noop; loadAuxiliary = noop;}},
  '../platform/home-action-diagnostics': {homeActionDiagnostics: {libraryMenu: noop}},
};
const downloadsModule = await offlineModule(new URL('../src-next/screens/downloads/device.ts', import.meta.url), deviceModules) as typeof import('../src-next/screens/downloads/device');
const {createNativeDownloadsDevice, retainNativeDownloadsDevice} = downloadsModule;
const {restoreNativeShellRuntime} = await offlineModule(new URL('../src-next/shell/bootstrap.ts', import.meta.url), {
  ...bootModules, '../screens/downloads/device': downloadsModule,
}) as typeof import('../src-next/shell/bootstrap');

test('fresh Home owns collection and sync after its first download sheet closes, without visiting Saved', async () => {
  active = fixture(); const f = active;
  const owner = retainNativeDownloadsDevice(f.context, f.device as any);
  await owner.open();
  const sheet = createNativeDownloadsDevice(f.context, f.device as any);
  await sheet.device.open();
  sheet.want([{id: 'preparation', itemId: 'item'}] as any);
  sheet.dispose(); sheet.dispose();
  try {
    assert.equal(f.state.connects, 1); assert.equal(f.state.starts, 1);
    assert.deepEqual(f.state.followed, ['device']); assert.equal(f.state.wants.length, 1);
    assert.equal(f.state.stops, 0); assert.equal(f.state.disconnects, 0);
    assert.throws(() => sheet.device.getSnapshot(), cancelled);
    await f.state.connection!.api.request('/late-ready-preparation');
    await f.state.connection!.api.request('/queued-progress', 'POST', {});
    assert.deepEqual(f.state.calls, ['/late-ready-preparation', '/queued-progress']);
  } finally {owner.dispose();}
  assert.equal(f.state.disconnects, 1); assert.equal(f.state.disposed, 1);
});

test('shell retirement aborts pending transport and retires still-borrowed actions', async () => {
  active = fixture(); const f = active, answer = deferred<unknown>();
  f.state.request = () => answer.promise;
  const owner = retainNativeDownloadsDevice(f.context, f.device as any);
  await owner.open();
  const sheet = createNativeDownloadsDevice(f.context, f.device as any);
  const request = f.state.connection!.api.request('/pending');
  owner.dispose(); owner.dispose();
  assert.equal(f.state.requests[0].aborted, true);
  assert.throws(() => sheet.device.setSettings({}), cancelled);
  assert.throws(() => sheet.device.admission!(200), cancelled);
  await assert.rejects(sheet.device.cancelPending!('preparation'), cancelled);
  answer.resolve({old: true}); await assert.rejects(request, cancelled);
  sheet.dispose();
  assert.equal(f.state.disconnects, 1); assert.equal(f.state.disposed, 1);
  assert.equal(f.state.adopted.at(-1), undefined);
});

test('a late old-shell disposal cannot clear the replacement viewer connection', async () => {
  active = fixture(); const f = active;
  const old = retainNativeDownloadsDevice(f.context, f.device as any); await old.open();
  const oldScreen = createNativeDownloadsDevice(f.context, f.device as any);
  const viewer = {...f.viewer, profileId: 'replacement'};
  const newer = retainNativeDownloadsDevice({...f.context,
    session: () => ({...f.session, viewer}) as any,
    authority: () => ({...f.authority, profileId: viewer.profileId, epoch: 'replacement-epoch'}),
  }, f.device as any); await newer.open();
  old.dispose(); oldScreen.dispose();
  assert.equal(f.state.connects, 2); assert.equal(f.state.disconnects, 0);
  assert.equal(f.state.adopted.at(-1), viewer);
  await f.state.connection!.api.request('/new-viewer');
  newer.dispose(); assert.equal(f.state.disconnects, 1); assert.equal(f.state.disposed, 2);
});

test('a read-only borrower stays offline until the authenticated shell owns the coordinator', async () => {
  active = fixture(); const f = active;
  const reader = createNativeDownloadsDevice(f.context, f.device as any, {readOnly: true});
  await reader.device.open(); assert.equal(f.state.connects, 0); assert.equal(f.state.starts, 0);
  const owner = retainNativeDownloadsDevice(f.context, f.device as any);
  reader.dispose(); assert.equal(f.state.connects, 1); assert.equal(f.state.disconnects, 0);
  owner.dispose(); assert.equal(f.state.disconnects, 1);
});

test('retirement while native state opens cannot reconnect a retired owner', async () => {
  active = fixture(); const f = active, opened = deferred<void>();
  f.state.open = () => opened.promise;
  const owner = retainNativeDownloadsDevice(f.context, f.device as any), opening = owner.open();
  owner.dispose(); opened.resolve();
  await assert.rejects(opening, cancelled);
  assert.equal(f.state.connects, 0); assert.equal(f.state.starts, 0);
});

test('the actual bootstrap starts downloads on Home and retains them through the last screen disposal', async () => {
  active = fixture(); const f = active;
  let context: NativeShellBootstrapContext | undefined;
  const runtime = await restoreNativeShellRuntime(value => {
    context = value;
    return {getState: () => ({destination: {kind: 'home'}}), account: {}, attach: noop, subscribe, dispose: noop,
      reportError: (error: unknown) => {f.state.errors.push(error);}} as any;
  }, {onAuthorityEnded: noop});
  assert.ok(runtime); assert.ok(context);
  try {
    // Native open is asynchronous and intentionally does not delay Home.
    await Promise.resolve(); await Promise.resolve();
    assert.equal(f.state.connects, 1, 'bootstrap starts without rendering Saved or Downloads');
    const sheet = createNativeDownloadsDevice(context);
    await sheet.device.open(); sheet.dispose();
    assert.equal(f.state.disconnects, 0); assert.equal(f.state.stops, 0);
    assert.deepEqual(f.state.errors, []);
  } finally {runtime.dispose();}
  assert.equal(f.state.disconnects, 1); assert.equal(f.state.disposed, 1);
});

test('bootstrap reports an unreadable native state without preventing Home or losing retry', async () => {
  active = fixture(); const f = active, failure = new Error('storage_unreadable'), reported = deferred<void>();
  f.state.open = async () => {throw failure;};
  let context: NativeShellBootstrapContext | undefined;
  const runtime = await restoreNativeShellRuntime(value => {
    context = value;
    return {getState: () => ({destination: {kind: 'home'}}), account: {}, attach: noop, subscribe, dispose: noop,
      reportError: (error: unknown) => {f.state.errors.push(error); reported.resolve();}} as any;
  }, {onAuthorityEnded: noop});
  assert.ok(runtime); assert.ok(context);
  try {
    await reported.promise;
    assert.deepEqual(f.state.errors, [failure]); assert.equal(f.state.connects, 0);
    f.state.open = async () => {};
    const screen = createNativeDownloadsDevice(context);
    await screen.device.open(); screen.dispose();
    assert.equal(f.state.connects, 1); assert.equal(f.state.disconnects, 0);
  } finally {runtime.dispose();}
});
