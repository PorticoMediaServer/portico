import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import {createNativeOfflineEntryPlayback} from '../src-next/screens/downloads/playback.ts';
import * as readEligibility from '../src-next/screens/read/eligibility';
import * as readPlatform from '../src-next/screens/read/platform';
import {claimNativeDownloadsDevice} from '../src-next/shell/downloads-device-custody';
import {sameAuthority} from '../src-next/shell/model';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('the ordinary Downloads route registers its own real-data renderer and exact eleven copy bindings', async () => {
  const host = hooks(); let kind: string | undefined, factory: any, retired = 0;
  const device = {getSnapshot: () => ({entries: []})};
  const view = await screenModule(new URL('../src-next/screens/downloads/view.tsx', import.meta.url), {
    react: host.react, './DownloadsScreen': {DownloadsScreen: 'Downloads'},
    './device': {createNativeDownloadsDevice: () => ({device, service: undefined, dispose: () => {retired++;}})},
    '../../shell/model': {sameAuthority}, '../../shell/routes': {nativeEntryRoute: (entry: unknown) => ({kind: 'title', entry})},
    '../read/eligibility': readEligibility, '../read/platform': readPlatform,
  });
  const {nativeDownloadsCopy} = view;
  await screenModule(new URL('../src-next/screens/downloads/register.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View', useWindowDimensions: () => ({width: 390, height: 844})}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 34})},
    '../../shell/route-registry': {nativeShellRoutes: {registerRoute: (key: string, create: any) => {kind = key; factory = create;}}},
    '../../shell/model': {phoneBarHeight: () => 84, shellLayout: () => ({tabs: true})}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'},
    './view': view,
  });
  assert.equal(kind, 'downloads');
  assert.equal(Object.keys(nativeDownloadsCopy).length, 11);
  assert.equal(defaultI18n.t(nativeDownloadsCopy.emptyBody), "Open a title and choose Download. It's kept here to watch without a connection.");
  assert.equal(defaultI18n.t(nativeDownloadsCopy.storageLimitHelp), 'The most space downloads may use on this phone');
  let backs = 0;
  const renderer = factory({current: () => true, authority: () => ({}), navigation: {back: () => {backs++;}}});
  const route = renderer.render({kind: 'downloads'}, {});
  const screen = route.props.children[0];
  assert.equal(screen.props.device, device);
  screen.props.back(); assert.equal(backs, 1);
  const tree = host.render(() => route.type(route.props));
  assert.equal(tree.props.style.paddingBottom, 84, 'the retained Saved navigation remains visible below the actual route');
  assert.equal(tree.props.style.backgroundColor, undefined, 'the standard backdrop, not flat ink, paints the page');
  assert.equal(tree.props.children[0].type, 'StandardBackdrop', 'the route covers the page it is drawn over with the standard backdrop');
  renderer.dispose(); assert.equal(retired, 1);
});

test('Downloads storage authority retires old actions, requests and disconnects without touching a newer viewer', async () => {
  const host = hooks(), connections: any[] = [], followed: string[] = []; let disposed = 0, disconnects = 0, starts = 0, stops = 0;
  const deviceSnapshot = {entries: [], supported: true};
  const device = {
    connect: (...args: any[]) => {connections.push(args);}, disconnect: () => {disconnects++;}, adopt() {},
    getSnapshot: () => deviceSnapshot, subscribe: () => () => {}, open: async () => {}, setSettings: async () => {},
    pause: async () => {}, resume: async () => {}, remove: async () => {}, removeOthers: async () => {},
    displayFacts: async () => ({}), localMedia: async () => ({}), playable: () => '', playabilityReason: () => null, note: () => '',
  };
  const {createNativeDownloadsDevice} = await screenModule(new URL('../src-next/screens/downloads/device.ts', import.meta.url), {
    react: host.react,
    '../../../../../packages/client-core/src/downloads-service': {DownloadsService: class {start() {starts++;} stop() {stops++;} followDevice(id: string) {followed.push(id);} dispose() {disposed++;}}},
    '../destinations/lifetime': {nativeDestinationLifetime},
    './playback': {createNativeOfflineEntryPlayback},
    '../../shell/downloads-device-custody': {claimNativeDownloadsDevice},
    '../../platform/offline-downloads': {offlineDownloads: device, createNativeOfflineDownloadsAdapter: (_: undefined, actual: unknown) => actual},
  });
  const viewer = {serverId: 'server', accountId: 'account', profileId: 'first', authority: 'local'};
  const authority = {...viewer, epoch: 'epoch', viewerFence: 'home-fence'};
  let current = true, finish: (value: unknown) => void = () => {};
  const context = {session: () => ({viewer, deviceId: 'device-1'}), authority: () => authority, current: () => current,
    api: {baseUrl: 'https://server.test', mediaUrl: (path: string) => path, request: () => new Promise(resolve => {finish = resolve;})}};
  const first = createNativeDownloadsDevice(context);
  await first.device.open();
  assert.equal(connections[0][2], viewer);
  assert.equal(starts, 1, 'the connected service reads its list');
  assert.deepEqual(followed, ['device-1'], 'whole-container requests are found again on reconnect');
  assert.equal(stops, 0);
  assert.equal(first.device.openOffline, undefined);
  const retainedSaved = createNativeDownloadsDevice(context);
  assert.equal(retainedSaved.device.getSnapshot(), first.device.getSnapshot(), 'Saved and Downloads read the same authority-local native snapshot');
  await retainedSaved.device.open();
  assert.equal(connections.length, 1, 'a route round trip cannot create a second native connection');
  first.dispose(); first.dispose();
  assert.equal(disconnects, 0, 'closing one borrower leaves the retained Saved hub connected');
  assert.throws(() => first.device.setSettings({wifiOnly: false}), {code: 'cancelled'}, 'a disposed route cannot mutate through another retained borrower');
  assert.deepEqual(retainedSaved.device.getSnapshot().entries, []);
  const request = connections[0][0].request('/actual');
  current = false; finish({private: 'old'});
  await assert.rejects(request, {code: 'cancelled'});
  assert.throws(() => first.device.getSnapshot(), {code: 'cancelled'});
  assert.throws(() => first.device.setSettings({wifiOnly: false}), {code: 'cancelled'});
  const second = createNativeDownloadsDevice({...context, current: () => true});
  await second.device.open();
  retainedSaved.dispose(); assert.equal(disconnects, 0, 'old disposal leaves the newer singleton connection intact');
  second.dispose(); assert.equal(disconnects, 1); assert.equal(disposed, 2);
});

test('Saved and Downloads leases expose the same installed offline command without another connection owner', async () => {
  const host = hooks(), listeners = new Set<() => void>(); let connections = 0, disconnects = 0;
  const entry = {id: 'download', itemId: 'item', owner: 'owner', state: 'ready'};
  const snapshot = {entries: [entry], supported: true, ready: true};
  const calls: {entry: unknown; lifetime: {signal: AbortSignal; isCurrent: () => boolean}}[] = [];
  const source = {getSnapshot: () => snapshot, subscribe: () => () => {}, open: async () => {}, adopt() {},
    connect: () => {connections++;}, disconnect: () => {disconnects++;}, playable: () => ''};
  const {createNativeDownloadsDevice} = await screenModule(new URL('../src-next/screens/downloads/device.ts', import.meta.url), {
    react: host.react,
    '../../../../../packages/client-core/src/downloads-service': {DownloadsService: class {start() {} stop() {} followDevice() {} dispose() {}}},
    '../destinations/lifetime': {nativeDestinationLifetime}, './playback': {createNativeOfflineEntryPlayback},
    '../../shell/downloads-device-custody': {claimNativeDownloadsDevice},
    '../../platform/offline-downloads': {offlineDownloads: source, createNativeOfflineDownloadsAdapter: (_: undefined, actual: unknown) => actual},
  });
  const viewer = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
  let route: object = {kind: 'downloads'}, revision = 1;
  const context = {current: () => true, session: () => ({viewer}), authority: () => ({...viewer, epoch: 'epoch'}),
    api: {baseUrl: 'https://server.test', mediaUrl: (path: string) => path},
    playback: {startOffline: async (entry: unknown, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) => {calls.push({entry, lifetime});}},
    navigation: {getSnapshot: () => ({route, revision}), subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}},
    subscribeAuthority: () => () => {},
  };
  const downloads = createNativeDownloadsDevice(context), saved = createNativeDownloadsDevice(context);
  try {
    await downloads.device.open(); await saved.device.open(); assert.equal(connections, 1);
    await downloads.device.openOffline(entry); assert.equal(calls[0].entry, entry);
    assert.equal(calls[0].lifetime.isCurrent(), true);
    downloads.dispose(); assert.equal(disconnects, 0); assert.equal(calls[0].lifetime.signal.aborted, false);
    route = {kind: 'shell', destination: {kind: 'saved'}}; revision++;
    for (const listener of [...listeners]) listener();
    await saved.device.openOffline(entry); assert.equal(calls[1].entry, entry); assert.equal(connections, 1);
  } finally {downloads.dispose(); saved.dispose();}
  assert.equal(disconnects, 1); assert.equal(listeners.size, 0);
});
