import test, {after} from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, connectReadingHost, createReadingHost, preferenceValue, ReadingError} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {routeBase64} from '../../../packages/client-core/src/route-identity';
import {defaultI18n} from '../../../packages/i18n/src';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {readRouteBack} from '../src-next/screens/read/read-route-back';
import {nativeReadingSettingsSection} from '../src-next/screens/settings/reading-preferences';
import {appleReaderEntry, readerAssetEntry, readerDelivery, readerNavigationAllowed} from '../src-next/screens/read/asset-origin';
import {closeReadingResource, createReadingSessionOwner} from '../src-next/screens/read/reading-session-owner';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {fabricHost, React} from './helpers/fabric-host.mjs';

const priorDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__');
Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: false});
after(() => {if (priorDev) Object.defineProperty(globalThis, '__DEV__', priorDev); else Reflect.deleteProperty(globalThis, '__DEV__');});

/** Real Fabric reconciliation and production route/host/guest/bridge. WebView,
 * server transport and native assets are controlled boundaries, not a device. */
for (const revokeEvicted of [false, true])
test(`actual retained Read components cap guest residency and restore original sessions (evicted revoked=${revokeEvicted})`, async t => {
  let finishRange: ((value: Response) => void) | undefined, rangeCancelled: Promise<boolean> | undefined, cleanupRequested = false;
  let cleanupFabric = async () => {}, cleanupRenderer = () => {}, cleanupNavigation = () => {};
  const revision = 'a'.repeat(64);
  const releaseHeld = () => {cleanupRequested = true; const release = finishRange; finishRange = undefined;
    release?.(new Response(new Uint8Array([7]), {status: 206, headers: {'Content-Type': 'application/pdf', 'Content-Length': '1', 'Content-Range': 'bytes 0-0/100', ETag: `"${revision}"`}}));};
  const boundedOutcome = async () => {if (!rangeCancelled) return undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {return await Promise.race([rangeCancelled, new Promise<never>((_, reject) => {timer = setTimeout(() => reject(new Error('range cancellation did not settle')), 2000);})]);}
    finally {if (timer) clearTimeout(timer);}};
  // Register before setup/await: unmount the original guest, retire its owners,
  // release the controlled response, then bounded-join the observed outcome.
  t.after(async () => {try {await cleanupFabric();} finally {try {cleanupRenderer();} finally {try {cleanupNavigation();} finally {releaseHeld(); await boundedOutcome();}}}});
  const fabric = await fabricHost(), react = {...React, default: React}; cleanupFabric = () => fabric.unmount();
  const calls: {method: string; item?: string; kind?: string}[] = [], originals = new Map<string, any>(), views = new Set<any>(), allViews: any[] = [];
  let peak = 0, authorityCurrent = true;
  const authorityListeners = new Set<() => void>();
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'a'.repeat(64)};
  const viewer = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'} as const;
  const navigation = new NativeShellNavigation(authority, () => authority); cleanupNavigation = () => navigation.dispose();
  const fields = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows).map(row => ({
    key: row.key!, type: typeof row.control.default === 'boolean' ? 'boolean' : typeof row.control.default === 'number' ? 'integer' : 'string',
    default: row.control.default, scopes: ['profile-server'], group: 'reading', labelKey: `preferences.${row.key}`,
    ...(row.control.options ? {allowedValues: row.control.options.map(option => option.value)} : {}),
  }));
  const snapshot = parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'reading-fabric', registry: {revision: 'reading-fabric', fields},
    documents: [{scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {}},
      {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}}],
    effective: Object.fromEntries(fields.map(field => [field.key, field.default])), effectiveSource: Object.fromEntries(fields.map(field => [field.key, 'default'])), clampedFields: []});
  const preferences = {getSnapshot: () => snapshot, subscribe: () => () => {}};
  const api = new HttpLocalApi('http://127.0.0.1:39141', 'reader-fixture-token', async (url, init = {}) => {
    const method = init.method ?? 'GET', path = new URL(String(url)).pathname;
    if (path.endsWith('/reading/versions')) {
      const item = path.split('/')[3]!; calls.push({method, item, kind: 'versions'});
      return new Response(JSON.stringify({chosenSourceId: `source-${item.slice(5)}`, versions: [{sourceId: `source-${item.slice(5)}`, inventoryAvailable: true, size: 100, format: 'pdf'}]}), {headers: {'Content-Type': 'application/json'}});
    }
    if (method === 'POST') {
      const body = JSON.parse(String(init.body)), item = body.itemId, index = Number(item.slice(5)); calls.push({method, item});
      const manifest = {sessionId: `session-${index}`, itemId: item, sourceId: body.sourceId, revision, size: 100, format: 'pdf', metadata: {title: item}};
      return new Response(JSON.stringify(manifest), {headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
    }
    const match = path.match(/\/sessions\/session-(\d+)/), index = Number(match?.[1]); calls.push({method, item: `item-${index}`});
    if (path.endsWith('/source')) return new Promise<Response>(resolve => {finishRange = resolve; if (cleanupRequested) releaseHeld();});
    if (method === 'DELETE') return new Response(null, {status: 204});
    return new Response(JSON.stringify({progress: {sourceId: `source-${index}`, sourceRevision: revision,
      location: {format: 'pdf', pageIndex: index, progression: 0}, revision: 1, observedAt: 1, stale: false}}),
      {headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
  });
  const context: any = {navigation, authority: () => authority, current: () => authorityCurrent,
    session: () => ({viewer, sessionFamilyId: 'family'}), subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
    api: {reading: (scope: any) => {const client = api.reading(scope); return {versions: client.versions.bind(client), dispose: client.dispose.bind(client),
      open: async (request: any, signal: AbortSignal) => {const session = await client.open(request, signal); originals.set(request.itemId, session); return session;}};}}};
  const WebView = React.forwardRef((props: any, ref: any) => {
    const record = React.useRef({props, input: undefined as undefined | ((message: string) => void), guest: undefined as any, active: false, connected: undefined as any, live: false}).current;
    record.props = props;
    React.useImperativeHandle(ref, () => ({injectJavaScript(script: string) {
      const prefix = 'window.PorticoReader&&window.PorticoReader.receive(', suffix = ');true;';
      assert.ok(script.startsWith(prefix) && script.endsWith(suffix)); record.input?.(JSON.parse(script.slice(prefix.length, -suffix.length)));
    }}), []);
    React.useLayoutEffect(() => {
      record.live = true; views.add(record); allViews.push(record); peak = Math.max(peak, views.size);
      const connection = connectReadingHost({send: message => record.props.onMessage({nativeEvent: {url: appleReaderEntry, data: message}}),
        subscribe: listener => {record.input = listener; return () => {record.input = undefined;};}});
      record.connected = connection; void connection.ready.then(guest => {record.guest = guest; guest.onActive(value => {record.active = value;});}, () => {});
      return () => {record.live = false; views.delete(record); connection.dispose();};
    }, []);
    return React.createElement('RCTWebView', {...props, testID: 'reading-webview'});
  });
  const {ReadingGuestView, createReadingGuestBridge} = await screenModule(new URL('../src-next/screens/read/ReadingGuestView.tsx', import.meta.url), {
    react, 'react-native-webview': {default: WebView}, '../../../../../packages/client-core/src': {createReadingHost},
    '../../../../../packages/client-core/src/route-identity': {routeBase64}, '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24).fill(7)}},
    '../../platform/reading-highlights-save': {readingHighlightsSavePort: () => async () => 'cancelled'}, './asset-origin': {readerNavigationAllowed, readerDelivery},
    './startup-diagnostics': {readerStartupDiagnostic: () => undefined, readerStartupProbe: ''},
  });
  const native = {View: 'RCTView', AppState: {addEventListener: () => ({remove() {}})}, NativeModules: {PorticoReaderAssets: {bundle: async () => ({url: appleReaderEntry})}},
    useWindowDimensions: () => ({width: 390, height: 844})};
  const {NativeReadingGuestHost} = await screenModule(new URL('../src-next/screens/read/NativeReadingGuestHost.tsx', import.meta.url), {...stackSlotFixture, 
    react, 'react-native': native, 'react-native-webview': {default: WebView}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/states': {LoadingPaint: ({children}: any) => children, SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../../kit/controls/Button': {Button: 'Button'}, '../../kit/theme': {useTheme: () => ({colors: {ink: '#000'}})},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, './asset-origin': {readerAssetEntry}, './ReadingGuestView': {ReadingGuestView, createReadingGuestBridge},
  });
  const {ReadNativeHost} = await screenModule(new URL('../src-next/screens/read/ReadNativeHost.tsx', import.meta.url), {...stackSlotFixture, 
    react, 'react-native': native, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47})},
    '../../../../../packages/client-core/src': {preferenceValue, ReadingError}, '../../../../../packages/i18n/src': {defaultI18n},
    '../downloads/device': {createNativeDownloadsDevice: () => {throw new Error('unexpected offline device');}}, '../../../platform/offline-downloads': {offlineDownloads: {}},
    '../../kit/states': {LoadingPaint: ({children}: any) => children, SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../../kit/controls/Button': {Button: 'Button'}, '../../kit/theme': {useTheme: () => ({colors: {ink: '#000'}})},
    './NativeReadingGuestHost': {NativeReadingGuestHost}, './reading-session-owner': {createReadingSessionOwner, closeReadingResource},
    './annotation-journal': {NativeAnnotationJournal: class {unbind() {}}, nativeAnnotationIdentity: () => ({}), nativeAnnotationLeaseOf: () => undefined},
    './annotation-session': {nativeAnnotationBridge: (value: any) => value.session}, './annotation-sync': {NativeAnnotationSync: class {dispose() {} flush() {return Promise.resolve();}}},
    './annotation-authoring': {NATIVE_OFFLINE_ANNOTATION_AUTHORING: false},
  });
  const {createNativeReadRenderer} = await screenModule(new URL('../src-next/screens/read/ReadRoute.tsx', import.meta.url), {
    react, 'react-native': {Platform: {isTV: false}}, './host-component': {nativeReadHost: () => ReadNativeHost}, './read-route-back': {readRouteBack},
  });
  const renderer = createNativeReadRenderer(context, preferences, () => {}); cleanupRenderer = () => renderer.dispose();
  function Root() {React.useSyncExternalStore(navigation.subscribe, navigation.getSnapshot, navigation.getSnapshot); return renderer.renderRetained();}
  const settle = async () => {await fabric.settle(); await fabric.settle();};
  {
    await fabric.render(React.createElement(Root));
    for (let index = 0; index < 20; index++) {
      if (index) {assert.equal(navigation.open({kind: 'search-reading', query: `book ${index}`}, authority), true); await settle();}
      assert.equal(navigation.open({kind: 'read', itemId: `item-${index}`, sourceId: `source-${index}`}, authority), true); await settle();
      assert.equal(calls.filter(call => call.method === 'POST').length, index + 1);
      assert.ok(views.size <= 2); assert.ok([...views].filter(view => view.active).length <= 1);
      assert.ok([...views].some(view => view.guest?.session.manifest.itemId === `item-${index}`), 'actual current guest is ready');
      if (index === 0) {const guest = [...views].find(view => view.active)!.guest;
        rangeCancelled = guest.session.readRange({start: 0, endExclusive: 1}).then(() => false, () => true); await settle(); assert.ok(finishRange);}
      if (index === 2) {assert.equal(await boundedOutcome(), true, 'eviction cancels the old binary transfer');
        releaseHeld(); await settle();}
    }
    assert.ok(peak <= 2, `at most two committed WebView boundaries, observed ${peak}`);
    assert.equal(calls.filter(call => call.kind === 'versions').length, 20);
    const stale = allViews.find(view => !view.live && view.guest?.session.manifest.itemId === 'item-0'); assert.ok(stale);
    if (revokeEvicted) {const original = originals.get('item-1'); assert.ok(original); original.retire(); await settle();}
    for (let index = 19; index >= 0; index--) {
      const view = [...views].find(value => value.active);
      if (revokeEvicted && index === 1) {assert.equal(view, undefined, 'revoked original has no active guest');
        const flatten = (nodes: any[]): any[] => nodes.flatMap(node => [node, ...flatten(node.children ?? [])]);
        assert.ok(flatten(fabric.root).some(node => node.type === 'StateView'), 'revoked original exposes the unavailable state');}
      else {assert.ok(view); assert.equal(view.guest.session.manifest.itemId, `item-${index}`); assert.equal(view.guest.session.manifest.sourceId, `source-${index}`);
        assert.equal(view.guest.session.manifest.revision, revision); assert.equal(view.guest.initialProgress.progress.location.pageIndex, index);}
      assert.equal(calls.filter(call => call.method === 'POST').length, 20, 'Back never opens a replacement session');
      assert.equal(calls.filter(call => call.kind === 'versions').length, 20, 'Back never refreshes the source');
      if (index === 0) {stale.props.onError(); stale.props.onMessage({nativeEvent: {url: appleReaderEntry, data: 'stale'}}); await settle();
        assert.equal([...views].find(value => value.active)?.guest.session.manifest.itemId, 'item-0');}
      if (index) {assert.equal(navigation.back(authority), true); await settle(); assert.equal(navigation.back(authority), true); await settle();}
    }
    assert.equal(calls.filter(call => call.method === 'DELETE').length, revokeEvicted ? 18 : 19, 'each still-current removed original was closed once');
    const before = calls.length; authorityCurrent = false; for (const listener of [...authorityListeners]) listener(); await settle();
    assert.equal(calls.length, before, 'authority retirement causes no new IO');
  }
});
