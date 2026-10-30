// @fabric-react
import test, {after} from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {appleReaderEntry, readerAssetEntry, readerNavigationAllowed, readerDelivery} from '../src-next/screens/read/asset-origin';
import {defaultI18n} from '../../../packages/i18n/src';
import {NativeShellNavigation, type NativeShellRoute} from '../src-next/shell/routes';
import type {ShellAuthority} from '../src-next/shell/model';
import {readRouteBack} from '../src-next/screens/read/read-route-back';
import {closeReadingResource, createReadingSessionOwner} from '../src-next/screens/read/reading-session-owner';

// BLK-MOB-001: the native host replaces the whole guest with Retry when a Read
// fails, so these vectors exercise the actual unmodified ReadNativeHost render
// and its supplied onBack. Hooks and effects run through the dependency-free
// driver; layout, safe-area pixels and WebView lifecycle remain platform
// evidence. Vectors are sources only for the native reviewer.

const previousDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__');
Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: false});
after(() => {if (previousDev) Object.defineProperty(globalThis, '__DEV__', previousDev); else Reflect.deleteProperty(globalThis, '__DEV__');});

type Node = {type: unknown; props: Record<string, any>};
const flatten = (style: any): Record<string, any> => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
const childrenOf = (node: Node): unknown[] => {
  const children = node.props?.children;
  return Array.isArray(children) ? children.flat(Infinity) : children === undefined || children === null ? [] : [children];
};
function* nodes(value: unknown): Generator<Node> {
  if (Array.isArray(value)) {for (const item of value) yield* nodes(item); return;}
  if (!value || typeof value !== 'object' || !('props' in value)) return;
  const node = value as Node; yield node;
  for (const child of childrenOf(node)) yield* nodes(child);
}
const find = (tree: unknown, match: (node: Node) => boolean): Node | undefined => {
  for (const node of nodes(tree)) if (match(node)) return node;
  return undefined;
};
const byTestID = (tree: unknown, testID: string) => find(tree, node => node.props?.testID === testID);
const byType = (tree: unknown, type: string) => find(tree, node => node.type === type);

type Failure = 'none' | 'pending' | 'versions' | 'open' | 'progress' | 'assets';
type Fixture = Readonly<{at: Failure; width?: number}>;

async function harness(fixture: Fixture) {
  let hostCalls = 0, current = true, versionCalls = 0, at: Failure = fixture.at;
  const disposals: string[] = [], listeners = new Set<() => void>();
  const received: string[] = [];
  const reader = {dispose: () => disposals.push('host'), confirm() {}, setActive() {}, setCrease() {}, setCreaseRef() {}, receive(message: string) {received.push(message);}};
  const session = {manifest: {format: 'pdf', revision: 'r'.repeat(64)}, signal: new AbortController().signal,
    progress: async () => {if (at === 'progress') throw new Error('progress_rejected'); return {progress: null};},
    close: async () => {disposals.push('session');}};
  const client = {
    versions: () => {versionCalls++; return at === 'pending' ? new Promise(() => {}) : at === 'versions' ? Promise.reject(new Error('versions_rejected')) : Promise.resolve({chosenSourceId: 'source', versions: [{sourceId: 'source', inventoryAvailable: true}]});},
    open: async () => {if (at === 'open') throw new Error('open_rejected'); return session;},
    dispose() {},
  };
  const context = {
    api: {reading: () => client},
    current: () => current,
    session: () => ({sessionFamilyId: 'family', viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'owner'}}),
    subscribeAuthority(listener: () => void) {listeners.add(listener); return () => {listeners.delete(listener);};},
    navigation: {getSnapshot: () => ({route: {kind: 'read', itemId: 'item', sourceId: 'source'}}), retainedRoutes: () => [], allows: () => true, open: () => true, back() {}},
    authority: () => ({kind: 'owner'}),
  };
  const width = fixture.width ?? 390;
  // Leaf hosts are stable string types: this driver never renders a mocked
  // component, it only traverses the elements ReadNativeHost returns.
  const {ReadingGuestView} = await screenModule(new URL('../src-next/screens/read/ReadingGuestView.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native-webview': {default: 'WebView'},
    '../../../../../packages/client-core/src': {createReadingHost: () => reader},
    '../../../../../packages/client-core/src/route-identity': {routeBase64: () => 'route'},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24)}},
    '../../platform/reading-highlights-save': {readingHighlightsSavePort: () => ({})},
    './asset-origin': {readerNavigationAllowed, readerDelivery},
    './startup-diagnostics': {readerStartupDiagnostic: () => undefined, readerStartupProbe: ''},
  });
  const {NativeReadingGuestHost} = await screenModule(new URL('../src-next/screens/read/NativeReadingGuestHost.tsx', import.meta.url), {...stackSlotFixture, 
    react: {...React, default: React}, 'react-native': {View: 'View', NativeModules: {PorticoReaderAssets: {bundle: async () => at === 'assets' ? {} : {url: appleReaderEntry}}}, useWindowDimensions: () => ({width, height: 844})},
    'react-native-webview': {default: 'WebView'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/states': {LoadingPaint: 'LoadingPaint', StateView: 'StateView', SkeletonBlock: 'SkeletonBlock'},
    '../../kit/controls/Button': {Button: 'Button'}, '../../kit/theme': {useTheme: () => ({colors: {ink: '#000'}})},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, './asset-origin': {readerAssetEntry},
    './ReadingGuestView': {ReadingGuestView: 'ReadingGuestView', createReadingGuestBridge: () => {hostCalls++; return {...reader};}},
  });
  const {ReadNativeHost} = await screenModule(new URL('../src-next/screens/read/ReadNativeHost.tsx', import.meta.url), {...stackSlotFixture, 
    react: {...React, default: React},
    'react-native': {
      AppState: {addEventListener: () => ({remove() {}})}, View: 'View',
      NativeModules: {PorticoReaderAssets: {bundle: async () => at === 'assets' ? {} : {url: appleReaderEntry}}},
      useWindowDimensions: () => ({width, height: width === 390 ? 844 : 1024}),
    },
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    'react-native-webview': {default: 'WebView'},
    '../../../../../packages/client-core/src': {
      createReadingHost: () => {hostCalls++; return reader;}, preferenceValue: () => undefined,
      ReadingError: class extends Error {},
    },
    '../../../../../packages/client-core/src/route-identity': {routeBase64: () => 'route'},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24).fill(1)}},
    '../../platform/reading-highlights-save': {readingHighlightsSavePort: () => ({})},
    '../downloads/device': {createNativeDownloadsDevice: () => ({dispose() {}, device: {open: async () => {}}})},
    '../../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({entries: []}), publicationSource: () => ({sourceId: 'source'}), readingSession: async () => session}},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', StateView: 'StateView', SkeletonBlock: 'SkeletonBlock'},
    '../../kit/controls/Button': {Button: 'Button'},
    '../../kit/theme': {useTheme: () => ({colors: {ink: '#000'}})},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})},
    './NativeReadingGuestHost': {NativeReadingGuestHost: 'NativeReadingGuestHost'},
    './reading-session-owner': {createReadingSessionOwner, closeReadingResource},
    './ReadingGuestView': {ReadingGuestView: 'ReadingGuestView', createReadingGuestBridge: () => {hostCalls++; return reader;}},
    './asset-origin': {readerAssetEntry, readerNavigationAllowed, readerDelivery},
    './startup-diagnostics': {readerBridgeDiagnostic: () => undefined, readerStartupDiagnostic: () => undefined, readerStartupProbe: ''},
    './annotation-journal': {NativeAnnotationJournal: class {bind() {} unbind() {} dispose() {}}, nativeAnnotationIdentity: () => ({}), nativeAnnotationLeaseOf: () => ({})},
    './annotation-session': {nativeAnnotationBridge: (value: any) => value.session},
    './annotation-sync': {NativeAnnotationSync: class {flush() {return Promise.resolve();} subscribe() {return () => {};} getSnapshot() {return {};} dispose() {}}},
    './annotation-authoring': {NATIVE_OFFLINE_ANNOTATION_AUTHORING: false},
  });
  return {ReadNativeHost, NativeReadingGuestHost, ReadingGuestView, context, reader, received, get hostCalls() {return hostCalls;}, get versionCalls() {return versionCalls;}, setFailureAt(value: Failure) {at = value;}, setCurrent(value: boolean) {current = value; for (const listener of [...listeners]) listener();}, disposals};
}

function mount(component: (props: any) => unknown, props: any) {
  let tree: unknown;
  const host = createFabricHookHost(() => {tree = component(props);});
  return {host, get tree() {return tree;}, async update(next: any) {props = next; await host.rerender();}};
}

const propsFor = (context: any, overrides: Record<string, unknown> = {}) => ({
  context, target: {itemId: 'item', sourceId: 'source'}, active: true,
  preferences: {getSnapshot: () => ({effective: {}, effectiveSource: {}, fields: []}), subscribe: () => () => {}},
  onBack() {}, onOpenSettings() {}, ...overrides,
});

async function settle() {
  await act(async () => {await new Promise<void>(resolve => setImmediate(resolve));});
}

test('initial loading draws the supplied native Back outside the guest and no duplicate Retry', async () => {
  const fixture = await harness({at: 'pending'});
  let backs = 0;
  const view = mount(fixture.ReadNativeHost, propsFor(fixture.context, {onBack: () => {backs++;}}));
  await view.host.mount();
  assert.ok(byType(view.tree, 'LoadingPaint'), 'the unopened reader keeps the loading paint');
  assert.equal(byType(view.tree, 'StateView'), undefined, 'loading is not the failure StateView');
  const back = byTestID(view.tree, 'native-read-host-back');
  assert.ok(back, 'the loading/no-guest state must render the native Back affordance');
  assert.equal(back!.type, 'Button');
  assert.equal(back!.props.icon, 'i-left');
  assert.equal(back!.props.accessibilityLabel, defaultI18n.t('action.back'));
  assert.equal(flatten(back!.props.style).height, 38, 'phone Back uses the authored 38px control');
  const wrapper = find(view.tree, node => node.props?.pointerEvents === 'box-none');
  assert.ok(wrapper, 'the Back sits in its own safe-area overlay');
  assert.equal(flatten(wrapper!.props.style).top, 55, 'safe-area top (47) plus the authored 8px offset');
  assert.equal(flatten(wrapper!.props.style).left, 12, 'authored leading gutter');
  back!.props.onPress();
  assert.equal(backs, 1, 'active Back presses the supplied callback once');
  await view.update(propsFor(fixture.context, {active: false, onBack: () => {backs++;}}));
  byTestID(view.tree, 'native-read-host-back')!.props.onPress();
  assert.equal(backs, 1, 'inactive render never calls the supplied callback');
  await view.host.unmount();
});

for (const at of ['versions', 'open', 'progress', 'assets'] as const)
test(`a ${at} failure keeps Retry and the native Back reachable`, async () => {
  const fixture = await harness({at});
  let backs = 0;
  const view = mount(fixture.ReadNativeHost, propsFor(fixture.context, {onBack: () => {backs++;}}));
  await act(async () => {await view.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  let display = view;
  if (at === 'assets') {const child = byType(view.tree, 'NativeReadingGuestHost'); assert.ok(child);
    display = mount(fixture.NativeReadingGuestHost, child!.props); await act(async () => {await display.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});}
  const state = byType(display.tree, 'StateView');
  assert.ok(state, `the ${at} rejection keeps the existing StateView`);
  assert.equal(state!.props.primaryAction.id, 'retry', 'Retry stays the existing primary action');
  assert.equal(state!.props.primaryAction.label, defaultI18n.t('next.states.tryAgain'));
  assert.equal(state!.props.primaryAction.disabled, at === 'progress', 'only a pre-session failure can open another session');
  const back = byTestID(display.tree, 'native-read-host-back');
  assert.ok(back, `the ${at} failure keeps a native Back outside the guest`);
  back!.props.onPress();
  assert.equal(backs, 1, 'the live failure Back calls the supplied callback once');
  assert.equal(byType(view.tree, 'WebView'), undefined, `no guest is retained after a ${at} failure`);
  if (display !== view) await display.host.unmount();
  await view.host.unmount();
});

test('Retry repeats the same target and leaves Back reachable', async () => {
  const fixture = await harness({at: 'versions'});
  const view = mount(fixture.ReadNativeHost, propsFor(fixture.context));
  await act(async () => {await view.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const state = byType(view.tree, 'StateView');
  assert.ok(state);
  assert.equal(fixture.versionCalls, 1);
  await act(async () => {
    state!.props.primaryAction.onPress();
    await new Promise<void>(resolve => setImmediate(resolve));
  });
  assert.equal(fixture.versionCalls, 2, 'Retry repeats the same target versions step');
  assert.ok(byTestID(view.tree, 'native-read-host-back'), 'Back stays reachable after Retry');
  await view.host.unmount();
});

test('a healthy guest renders no duplicate native Back and a WebView failure projects Back with Retry', async () => {
  const fixture = await harness({at: 'none'});
  let backs = 0;
  const view = mount(fixture.ReadNativeHost, propsFor(fixture.context, {onBack: () => {backs++;}}));
  await act(async () => {await view.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const child = byType(view.tree, 'NativeReadingGuestHost'); assert.ok(child);
  const display = mount(fixture.NativeReadingGuestHost, child!.props);
  await act(async () => {await display.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const boundary = byType(display.tree, 'ReadingGuestView');
  const guest = boundary && fixture.ReadingGuestView(boundary.props) as any;
  assert.ok(guest, 'the opened reader hands the document to the guest');
  assert.equal(fixture.hostCalls > 0, true, 'the healthy path creates the reading host');
  assert.equal(byTestID(display.tree, 'native-read-host-back'), undefined, 'the healthy guest already owns the only Back');
  guest!.props.onError();
  await settle();
  assert.ok(byType(display.tree, 'StateView'), 'WebView onError projects the existing failure state');
  assert.equal(byType(display.tree, 'StateView')!.props.primaryAction.id, 'retry');
  const back = byTestID(display.tree, 'native-read-host-back');
  assert.ok(back, 'the projected failure gets the native Back');
  back!.props.onPress();
  assert.equal(backs, 1);
  assert.equal(fixture.hostCalls, 1, 'a failure does not silently open a second reader host');
  await display.host.unmount(); await view.host.unmount();
});

for (const method of ['onContentProcessDidTerminate', 'onRenderProcessGone'] as const)
test(`a ${method} projection keeps Retry and the native Back reachable with no duplicate over the guest`, async () => {
  const fixture = await harness({at: 'none'});
  let backs = 0;
  const view = mount(fixture.ReadNativeHost, propsFor(fixture.context, {onBack: () => {backs++;}}));
  await act(async () => {await view.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const child = byType(view.tree, 'NativeReadingGuestHost'); assert.ok(child);
  const display = mount(fixture.NativeReadingGuestHost, child!.props);
  await act(async () => {await display.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const boundary = byType(display.tree, 'ReadingGuestView');
  const guest = boundary && fixture.ReadingGuestView(boundary.props) as any;
  assert.ok(guest, 'the healthy guest is present before termination');
  assert.equal(byTestID(display.tree, 'native-read-host-back'), undefined, 'no native Back while the guest is healthy');
  guest!.props[method]();
  await settle();
  assert.ok(byType(display.tree, 'StateView'), `${method} projects the failure state`);
  assert.equal(byType(display.tree, 'StateView')!.props.primaryAction.id, 'retry', 'Retry remains reachable');
  const back = byTestID(display.tree, 'native-read-host-back');
  assert.ok(back, `${method} gets the native Back`);
  back!.props.onPress();
  assert.equal(backs, 1, 'the projected Back presses the supplied callback once');
  assert.equal(fixture.hostCalls, 1, 'termination does not open a second reader host');
  await display.host.unmount(); await view.host.unmount();
});

test('tablet width uses the authored 44px Back control', async () => {
  const fixture = await harness({at: 'pending', width: 834});
  const view = mount(fixture.ReadNativeHost, propsFor(fixture.context));
  await view.host.mount();
  const back = byTestID(view.tree, 'native-read-host-back');
  assert.ok(back);
  assert.equal(flatten(back!.props.style).height, 44, 'tablet-read.html .rtop .back is 44px');
  await view.host.unmount();
});

test('an old native Back callback cannot borrow the guard from a reopened retained reader', async () => {
  const fixture = await harness({at: 'pending'});
  const authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'original', viewerFence: 'viewer'};
  const navigation = new NativeShellNavigation(authority, () => authority);
  const target: Extract<NativeShellRoute, {kind: 'read'}> = {kind: 'read', itemId: 'item', sourceId: 'source'};
  const context = {...fixture.context, navigation};
  navigation.open(target, authority);
  const original = navigation.getSnapshot();
  const firstProps = propsFor(context, {target, onBack: readRouteBack(navigation, original, target, () => true)});
  const view = mount(fixture.ReadNativeHost, firstProps);
  try {
    await view.host.mount();
    const oldPress = byTestID(view.tree, 'native-read-host-back')!.props.onPress;
    navigation.open({kind: 'licenses'}, authority);
    await view.update({...firstProps, active: false});
    oldPress(); assert.equal(navigation.getSnapshot().route.kind, 'licenses');
    navigation.back(authority);
    const reopened = navigation.getSnapshot();
    assert.strictEqual(reopened.route, target); assert.notStrictEqual(reopened, original);
    await view.update({...firstProps, active: true, onBack: readRouteBack(navigation, reopened, target, () => true)});
    const freshPress = byTestID(view.tree, 'native-read-host-back')!.props.onPress;
    oldPress(); assert.strictEqual(navigation.getSnapshot(), reopened, 'old render cannot select the new callback through the host ref');
    freshPress(); const after = navigation.getSnapshot(); assert.equal(after.route.kind, 'shell');
    freshPress(); assert.strictEqual(navigation.getSnapshot(), after, 'fresh admitted render spends Back once');
  } finally {await view.host.unmount(); navigation.dispose();}
});

test('the extracted guest view rejects stale and foreign messages and forwards each native failure', async () => {
  const {ReadingGuestView} = await screenModule(new URL('../src-next/screens/read/ReadingGuestView.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native-webview': {default: 'WebView'},
    '../../../../../packages/client-core/src': {createReadingHost: () => {}},
    '../../../../../packages/client-core/src/route-identity': {routeBase64: () => 'route'},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24)}},
    '../../platform/reading-highlights-save': {readingHighlightsSavePort: () => ({})},
    './asset-origin': {readerNavigationAllowed, readerDelivery},
    './startup-diagnostics': {readerStartupDiagnostic: () => undefined, readerStartupProbe: ''},
  });
  const received: string[] = [], observed: string[] = [];
  const reader = {receive: (message: string) => received.push(message)}, replacement = {receive() {}};
  let current: unknown = reader, failures = 0;
  const view = ReadingGuestView({url: appleReaderEntry, view: {current: null}, reader,
    currentReader: () => current, backgroundColor: '#000', observeBridge: (_: string, message: string) => observed.push(message),
    onFailure: () => {failures++;}}) as Node;
  const message = (url: string, data: string) => view.props.onMessage({nativeEvent: {url, data}});
  message(appleReaderEntry, 'current');
  message('https://untrusted.example/', 'foreign');
  current = replacement; message(appleReaderEntry, 'stale');
  assert.deepEqual(received, ['current']); assert.deepEqual(observed, ['current']);
  view.props.onError(); view.props.onContentProcessDidTerminate(); view.props.onRenderProcessGone();
  assert.equal(failures, 3);
});

test('evicting and remounting a guest retains the original live session', async () => {
  const fixture = await harness({at: 'none'});
  const live = mount(fixture.ReadNativeHost, propsFor(fixture.context));
  await act(async () => {await live.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const child = byType(live.tree, 'NativeReadingGuestHost'); assert.ok(child);
  const first = mount(fixture.NativeReadingGuestHost, child!.props);
  await act(async () => {await first.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  await first.host.unmount();
  assert.equal(fixture.disposals.filter(value => value === 'session').length, 0);
  const second = mount(fixture.NativeReadingGuestHost, child!.props);
  await act(async () => {await second.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  assert.equal(fixture.versionCalls, 1, 'guest remount does not admit a new session');
  await second.host.unmount(); await live.host.unmount(); await settle();
  assert.equal(fixture.disposals.filter(value => value === 'session').length, 1);
});


test('an old guest error and message cannot affect a retried guest at the same asset URL', async () => {
  const fixture = await harness({at: 'none'}), live = mount(fixture.ReadNativeHost, propsFor(fixture.context));
  await act(async () => {await live.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const child = byType(live.tree, 'NativeReadingGuestHost'); assert.ok(child);
  const guest = mount(fixture.NativeReadingGuestHost, child!.props);
  try {
    await act(async () => {await guest.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
    const firstBoundary = byType(guest.tree, 'ReadingGuestView'); assert.ok(firstBoundary);
    const oldView = fixture.ReadingGuestView(firstBoundary!.props) as any;
    await act(() => oldView.props.onError());
    const error = byType(guest.tree, 'StateView'); assert.ok(error);
    await act(async () => {error!.props.primaryAction.onPress(); await new Promise<void>(resolve => setImmediate(resolve));});
    const nextBoundary = byType(guest.tree, 'ReadingGuestView'); assert.ok(nextBoundary);
    assert.notEqual(nextBoundary!.props.reader, firstBoundary!.props.reader);
    const nextView = fixture.ReadingGuestView(nextBoundary!.props) as any;
    await act(() => {oldView.props.onError(); oldView.props.onMessage({nativeEvent: {url: appleReaderEntry, data: 'stale'}});});
    assert.equal(byType(guest.tree, 'StateView'), undefined);
    assert.deepEqual(fixture.received, []);
    nextView.props.onMessage({nativeEvent: {url: appleReaderEntry, data: 'current'}});
    assert.deepEqual(fixture.received, ['current']); assert.equal(fixture.versionCalls, 1);
  } finally {await guest.host.unmount(); await live.host.unmount();}
});


test('a spent acquisition Retry cannot replace the successfully accepted original successor', async () => {
  const fixture = await harness({at: 'versions'}), view = mount(fixture.ReadNativeHost, propsFor(fixture.context));
  try {
    await act(async () => {await view.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
    const failed = byType(view.tree, 'StateView'); assert.ok(failed);
    const oldRetry = failed!.props.primaryAction.onPress;
    fixture.setFailureAt('none');
    await act(async () => {oldRetry(); await new Promise<void>(resolve => setImmediate(resolve));});
    const accepted = byType(view.tree, 'NativeReadingGuestHost'); assert.ok(accepted);
    const originalOwner = accepted!.props.owner;
    assert.equal(fixture.versionCalls, 2); assert.equal(fixture.disposals.filter(value => value === 'session').length, 0);
    await act(async () => {oldRetry(); await new Promise<void>(resolve => setImmediate(resolve));});
    assert.equal(byType(view.tree, 'NativeReadingGuestHost')!.props.owner, originalOwner);
    assert.equal(fixture.versionCalls, 2); assert.equal(fixture.disposals.filter(value => value === 'session').length, 0);
  } finally {await view.host.unmount();}
});

test('a spent guest Retry cannot replace a healthy guest or its original session', async () => {
  const fixture = await harness({at: 'none'}), live = mount(fixture.ReadNativeHost, propsFor(fixture.context));
  await act(async () => {await live.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
  const child = byType(live.tree, 'NativeReadingGuestHost'); assert.ok(child);
  const guest = mount(fixture.NativeReadingGuestHost, child!.props);
  try {
    await act(async () => {await guest.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
    const first = byType(guest.tree, 'ReadingGuestView'); assert.ok(first);
    await act(() => fixture.ReadingGuestView(first!.props).props.onError());
    const failed = byType(guest.tree, 'StateView'); assert.ok(failed);
    const oldRetry = failed!.props.primaryAction.onPress;
    await act(async () => {oldRetry(); await new Promise<void>(resolve => setImmediate(resolve));});
    const healthy = byType(guest.tree, 'ReadingGuestView'); assert.ok(healthy);
    const originalReader = healthy!.props.reader;
    assert.equal(fixture.hostCalls, 2); assert.equal(fixture.versionCalls, 1);
    await act(async () => {oldRetry(); await new Promise<void>(resolve => setImmediate(resolve));});
    assert.equal(byType(guest.tree, 'ReadingGuestView')!.props.reader, originalReader);
    assert.equal(fixture.hostCalls, 2); assert.equal(fixture.versionCalls, 1);
    assert.equal(fixture.disposals.filter(value => value === 'session').length, 0);
  } finally {await guest.host.unmount(); await live.host.unmount();}
});

test('a covered acquisition failure cannot Retry until its same original render is active', async () => {
  const fixture = await harness({at: 'versions'}), initial = propsFor(fixture.context);
  const view = mount(fixture.ReadNativeHost, initial);
  try {
    await act(async () => {await view.host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
    const retry = byType(view.tree, 'StateView')!.props.primaryAction.onPress;
    fixture.setFailureAt('none'); await act(() => view.update({...initial, active: false}));
    await act(async () => {retry(); await new Promise<void>(resolve => setImmediate(resolve));});
    assert.equal(fixture.versionCalls, 1);
    await act(() => view.update(initial));
    await act(async () => {retry(); await new Promise<void>(resolve => setImmediate(resolve));});
    assert.equal(fixture.versionCalls, 2); assert.ok(byType(view.tree, 'NativeReadingGuestHost'));
  } finally {await view.host.unmount();}
});
