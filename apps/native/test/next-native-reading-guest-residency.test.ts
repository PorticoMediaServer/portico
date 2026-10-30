// @fabric-react
import test, {after} from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {connectReadingHost, createReadingHost} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {nativeReadingSettingsSection} from '../src-next/screens/settings/reading-preferences';
import {routeBase64} from '../../../packages/client-core/src/route-identity';
import {appleReaderEntry, readerAssetEntry, readerNavigationAllowed, readerDelivery} from '../src-next/screens/read/asset-origin';
import {createReadingSessionOwner} from '../src-next/screens/read/reading-session-owner';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {readRouteBack} from '../src-next/screens/read/read-route-back';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';

const previousDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__');
Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: false});
after(() => {if (previousDev) Object.defineProperty(globalThis, '__DEV__', previousDev); else Reflect.deleteProperty(globalThis, '__DEV__');});

type Node = {type: unknown; props: Record<string, any>};
/** `type` is the element type itself: the real guest component is a function, while stubbed kit elements are their tag string. */
function find(value: unknown, type: unknown): Node | undefined {
  if (Array.isArray(value)) {for (const child of value) {const found = find(child, type); if (found) return found;} return;}
  if (!value || typeof value !== 'object' || !('props' in value)) return;
  const node = value as Node; return node.type === type ? node : find(node.props.children, type);
}

/** Exercises the real logical owner, bridge, guest component and WebView
 * element and actual route projection with in-memory session ports. This is
 * not a device or a signed-publication enrollment test. */
test('twenty original sessions can evict and restore real guest components without reopening', async () => {
  const {ReadingGuestView, createReadingGuestBridge} = await screenModule(new URL('../src-next/screens/read/ReadingGuestView.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native-webview': {default: 'WebView'},
    '../../../../../packages/client-core/src': {createReadingHost}, '../../../../../packages/client-core/src/route-identity': {routeBase64},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24).fill(7)}},
    '../../platform/reading-highlights-save': {readingHighlightsSavePort: () => async () => 'cancelled'},
    './asset-origin': {readerNavigationAllowed, readerDelivery}, './startup-diagnostics': {readerStartupDiagnostic: () => undefined, readerStartupProbe: ''},
  });
  const {NativeReadingGuestHost} = await screenModule(new URL('../src-next/screens/read/NativeReadingGuestHost.tsx', import.meta.url), {...stackSlotFixture, 
    react: {...React, default: React}, 'react-native': {View: 'View', NativeModules: {PorticoReaderAssets: {bundle: async () => ({url: appleReaderEntry})}}, useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-webview': {default: 'WebView'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', StateView: 'StateView', SkeletonBlock: 'SkeletonBlock'}, '../../kit/controls/Button': {Button: 'Button'},
    '../../kit/theme': {useTheme: () => ({colors: {ink: '#000'}})}, '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})},
    './asset-origin': {readerAssetEntry}, './ReadingGuestView': {ReadingGuestView, createReadingGuestBridge},
  });
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'a'.repeat(64)};
  const navigation = new NativeShellNavigation(authority, () => authority);
  const context = {navigation, authority: () => authority, current: () => true, subscribeAuthority: () => () => {}};
  const {createNativeReadRenderer} = await screenModule(new URL('../src-next/screens/read/ReadRoute.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}}, './host-component': {nativeReadHost: () => 'LogicalReadHost'},
    './read-route-back': {readRouteBack},
  });
  let opens = 0, closes = 0, resolveLate: ((value: Uint8Array) => void) | undefined;
  let pendingOutcome: Promise<boolean> | undefined; const sessions: any[] = [], progresses: any[] = [], owners: Awaited<ReturnType<typeof createReadingSessionOwner>>[] = [];
  const mounted = new Map<number, {host: Awaited<ReturnType<typeof createFabricHookHost>>; tree: () => unknown; view: Node; connection: Awaited<ReturnType<typeof connectReadingHost>>; guest: Awaited<ReturnType<typeof connectReadingHost>['ready']>; setActive: (value: boolean) => void}>();
  const evicted = new Map<number, Node>();
  const rowsSpec = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows);
  const fields = rowsSpec.map(row => ({key: row.key!, type: typeof row.control.default === 'boolean' ? 'boolean' : typeof row.control.default === 'number' ? 'integer' : 'string',
    default: row.control.default, scopes: ['profile-server'], group: 'reading', labelKey: `preferences.${row.key}`,
    ...(row.control.options ? {allowedValues: row.control.options.map(option => option.value)} : {})}));
  const snapshot = parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'read-unit', registry: {revision: 'read-unit', fields},
    documents: [{scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {}},
      {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}}],
    effective: Object.fromEntries(fields.map(field => [field.key, field.default])), effectiveSource: Object.fromEntries(fields.map(field => [field.key, 'default'])), clampedFields: []});
  const preferences = {getSnapshot: () => snapshot, subscribe: () => () => {}};
  const renderer = createNativeReadRenderer(context, preferences, () => {});
  let residenceElement: any, residenceTree: any, residence: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  const rows = () => {const children = residenceTree.props.children; return (Array.isArray(children) ? children : [children]).filter((value: any) => value?.type === 'LogicalReadHost');};
  const mount = async (index: number, active: boolean, onGuestState: (present: boolean, incarnation: object) => void) => {
    let tree: unknown, isActive = active; const host = createFabricHookHost(() => {tree = NativeReadingGuestHost({owner: owners[index], active: isActive, preferences, onBack() {}, onSettings() {}, onGuestState});});
    await act(async () => {await host.mount(); await new Promise<void>(resolve => setImmediate(resolve));});
    const boundary = find(tree, ReadingGuestView); assert.ok(boundary);
    const webview = ReadingGuestView(boundary!.props); assert.equal(webview.type, 'WebView');
    let input: ((message: string) => void) | undefined;
    boundary!.props.view.current = {injectJavaScript(script: string) {
      const prefix = 'window.PorticoReader&&window.PorticoReader.receive(', suffix = ');true;';
      assert.ok(script.startsWith(prefix) && script.endsWith(suffix));
      input?.(JSON.parse(script.slice(prefix.length, -suffix.length)));
    }};
    const connection = connectReadingHost({send: message => webview.props.onMessage({nativeEvent: {url: appleReaderEntry, data: message}}),
      subscribe: listener => {input = listener; return () => {input = undefined;};}});
    const guest = await connection.ready;
    assert.equal(guest.session.manifest.itemId, `item-${index}`);
    mounted.set(index, {host, tree: () => tree, view: webview, connection, guest, setActive: async value => {isActive = value; await host.rerender();}}); return webview;
  };
  const sync = async () => {
    residenceElement = renderer.renderRetained(); assert.ok(residenceElement);
    if (!residence) {residence = createFabricHookHost(() => {residenceTree = residenceElement.type(residenceElement.props);}); await act(() => residence!.mount());}
    else await act(() => residence!.rerender());
    await act(async () => {for (const [index, item] of [...mounted]) {
      const row = rows().find((value: any) => value.props.target.itemId === `item-${index}`);
      if (!row?.props.resident) {evicted.set(index, item.view); await item.host.unmount(); assert.equal(item.guest.session.signal.aborted, true); item.connection.dispose(); mounted.delete(index);}
    }});
    for (const row of rows()) if (row.props.resident) {
      const index = Number(row.props.target.itemId.slice(5)), existing = mounted.get(index);
      if (existing) await act(() => existing.setActive(row.props.active));
      else await mount(index, row.props.active, row.props.onGuestState);
    }
    assert.ok(mounted.size <= 2);
    assert.equal([...mounted.values()].filter(item => (item.tree() as Node).props.pointerEvents === 'auto').length, navigation.getSnapshot().route.kind === 'read' ? 1 : 0);
  };
  try {
    for (let index = 0; index < 20; index++) {
      const signal = new AbortController(), progress = {progress: {sourceId: `source-${index}`, sourceRevision: 'a'.repeat(64),
        location: {format: 'pdf', pageIndex: index, progression: 0}, revision: 1, observedAt: 1, stale: false}}, session = {manifest: {sessionId: `session-${index}`, itemId: `item-${index}`, sourceId: `source-${index}`, revision: 'a'.repeat(64), format: 'pdf', size: 100, metadata: {title: `Item ${index}`}}, signal: signal.signal,
        progress: async () => progress, close: async () => {closes++;}, headPDF: async () => ({}), readRange: async () => index === 0 ? new Promise<Uint8Array>(resolve => {resolveLate = resolve;}) : new Uint8Array([1]),
        readAsset: async () => new Uint8Array(), page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
      sessions.push(session); progresses.push(progress);
      if (index) {assert.equal(navigation.open({kind: 'search-reading', query: `book ${index}`}, authority), true); await sync();}
      const route = {kind: 'read', itemId: `item-${index}`, sourceId: `source-${index}`} as const;
      assert.equal(navigation.open(route, authority), true); renderer.render(route);
      const rendered = renderer.renderRetained();
      const binding = rendered.props.entries.find((entry: any) => entry.route === route).binding;
      const owner = createReadingSessionOwner({current: binding.retainedCurrent, subscribe: binding.subscribe,
        acquire: async () => {opens++; return {session: session as any, release() {}};}});
      owners.push(owner); await owner.ensure(); await sync();
      if (index === 0) {pendingOutcome = mounted.get(0)!.guest.session.readRange({start: 0, endExclusive: 1}).then(() => false, () => true);
        await act(async () => {await new Promise<void>(resolve => setImmediate(resolve));}); assert.ok(resolveLate);}
      if (index === 2) {assert.equal(await pendingOutcome, true, 'eviction cancels the original binary request');
        resolveLate?.(new Uint8Array([7])); await act(async () => {await new Promise<void>(resolve => setImmediate(resolve));});}
      assert.equal(opens, index + 1); assert.equal(closes, 0);
    }
    for (let index = 19; index >= 0; index--) {
      assert.equal(navigation.getSnapshot().route.kind, 'read');
      assert.equal((navigation.getSnapshot().route as {itemId: string}).itemId, `item-${index}`);
      assert.equal(owners[index]!.snapshot().manifest, sessions[index].manifest);
      assert.equal(owners[index]!.snapshot().manifest?.sourceId, `source-${index}`);
      assert.equal(owners[index]!.snapshot().confirmedProgress, progresses[index]);
      /** The guest receives the frozen, null-prototype decoded copy the owner confirmed, so compare the data, not the prototype chain. */
      assert.deepEqual(structuredClone(mounted.get(index)?.guest.initialProgress), progresses[index]); assert.equal(opens, 20); assert.equal(closes, 19 - index);
      const old = evicted.get(index), current = mounted.get(index);
      if (old && current) {old.props.onError(); old.props.onMessage({nativeEvent: {url: appleReaderEntry, data: 'stale'}});
        assert.equal(find(current.tree(), 'StateView'), undefined, 'evicted callbacks cannot fail the restored guest');}
      if (index) {assert.equal(navigation.back(authority), true); await sync(); assert.equal(navigation.back(authority), true); await sync();}
    }
  } finally {
    for (const item of mounted.values()) {await item.host.unmount(); item.connection.dispose();} await residence?.unmount(); renderer.dispose(); navigation.dispose();
    await Promise.all(owners.map(owner => owner.dispose()));
  }
  assert.equal(closes, 20);
});
