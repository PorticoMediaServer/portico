import {bindNativeServerPreferences} from '../src-next/platform/server-preferences';
import {PreferencesReader} from '../../../packages/client-core/src/preferences-reader';
import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {NativeShellNavigation, type NativeShellRoute} from '../src-next/shell/routes';
import {nativeReadingSettingsPreferences, nativeReadingSettingsSection} from '../src-next/screens/settings/reading-preferences';
import {openNativeReadingSettings} from '../src-next/screens/settings/reading-entry';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {fabricHost, React} from './helpers/fabric-host.mjs';

type HostNode = {tag: number; type: unknown; props: Record<string, any>; children: HostNode[]};
// The retained scene runs on the installed Fabric renderer, so React owns keyed
// identity, commit order, layout-effect cleanup and unmount. Native hosts and
// WebView pixels remain controlled.
async function mountRetained(element: any) {
  const host = await fabricHost();
  let current = element, scene: any = null;
  // The scene the retained renderer offers, committed by React itself.
  const Scene = () => {scene = current ? current.type(current.props) : null; return scene;};
  const render = async (next: any) => {current = next; await host.render(React.createElement(Scene));};
  await render(element);
  /** The committed native guests the retained scene shows, in order. */
  const rows = () => host.root.filter(node => node.type === 'TrustedReadHost') as HostNode[];
  /** The keys the scene offers for those guests. */
  const keys = () => (scene?.props?.children ? (Array.isArray(scene.props.children) ? scene.props.children : [scene.props.children]) : [])
    .filter((value: any) => value?.type === 'TrustedReadHost').map((value: any) => value.key);
  return {host, rows, keys, update: render, act: async (work: () => void) => {work(); await host.settle();}, unmount: () => host.unmount()};
}

function authorityContext() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'a'.repeat(64)};
  const fields = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows).map(row => ({
    key: row.key!, type: typeof row.control.default === 'boolean' ? 'boolean' : typeof row.control.default === 'number' ? 'integer' : 'string',
    default: row.control.default, scopes: ['profile-server'], group: 'reading', labelKey: `preferences.${row.key}`,
    ...(row.control.options ? {allowedValues: row.control.options.map(option => option.value)} : {}),
  }));
  const snapshot = parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'read-unit', registry: {revision: 'read-unit', fields},
    documents: [{scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {}},
      {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}}],
    effective: Object.fromEntries(fields.map(field => [field.key, field.default])), effectiveSource: Object.fromEntries(fields.map(field => [field.key, 'default'])), clampedFields: []});
  let current = true, requests = 0;
  const authorityListeners = new Set<() => void>(), preferenceListeners = new Set<() => void>();
  const navigation = new NativeShellNavigation(authority, () => authority);
  const context = {navigation, authority: () => authority, current: () => current, preferences: () => snapshot,
    api: new HttpLocalApi('https://read-registration.test', 'synthetic-token', async () => {requests++; throw new Error('registration_must_not_fetch');}),
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
    subscribePreferences: (listener: () => void) => {preferenceListeners.add(listener); return () => {preferenceListeners.delete(listener);};},
  };
  const reader = new PreferencesReader(context.api, {serverId: authority.serverId, viewerId: authority.profileId});
  bindNativeServerPreferences(context, {read: deviceClass => reader.preferences(deviceClass),
    apply: (scope, deviceClass, revision, values) => reader.applyPreferences(scope, deviceClass, revision, values), accept: next => next});
  return {context, snapshot, get requests() {return requests;}, close() {current = false; reader.dispose(); for (const listener of [...authorityListeners]) listener();}};
}

test('Read registration retains the original occurrence across Reading Settings history', async () => {
  const f = authorityContext(); let registered: string | undefined, factory: any;
  const readingDestinations: string[] = [];
  const {createNativeReadRenderer} = await screenModule(new URL('../src-next/screens/read/ReadRoute.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}}, './host-component': {nativeReadHost: () => 'TrustedReadHost'},
    './read-route-back': {readRouteBack: (await import('../src-next/screens/read/read-route-back')).readRouteBack},
  });
  await screenModule(new URL('../src-next/screens/read/register.tsx', import.meta.url), {
    react: {...React, default: React}, '../../shell/route-registry': {nativeShellRoutes: {registerRoute: (kind: string, create: any) => {registered = kind; factory = create;}}},
    '../settings/ReadingSettingsRoute': {nativeReadingSettingsPreferences, openNativeReadingSettings}, './ReadRoute': {createNativeReadRenderer}, './read-destinations': await screenModule(new URL('../src-next/screens/read/read-destinations.tsx', import.meta.url), {
      react: {...React, default: React}, '../../shell/route-registry': {nativeShellRoutes: {registerRoute: (kind: string) => readingDestinations.push(kind)}},
      '../destinations/lifetime': await import('../src-next/screens/destinations/lifetime'),
      '../settings/ReadingSettingsRoute': {openNativeReadingSettings}, './platform': await import('../src-next/screens/read/platform'),
      './read-book-model': await import('../src-next/screens/read/read-book-model'), './read-periodical-model': await import('../src-next/screens/read/read-periodical-model'),
      './read-series-model': await import('../src-next/screens/read/read-series-model'), './ReadBookScreen': {ReadBookScreen: 'ReadBookScreen'},
      './ReadPeriodicalScreen': {ReadPeriodicalScreen: 'ReadPeriodicalScreen'}, './ReadSeriesScreen': {ReadSeriesScreen: 'ReadSeriesScreen'}}),
  });
  assert.equal(registered, 'read'); assert.equal(f.requests, 0);
  assert.deepEqual(readingDestinations, ['read-book', 'read-series', 'read-periodical'], 'the real side-effect namespace registers its own destinations');
  const source = nativeReadingSettingsPreferences(f.context), renderer = factory(f.context);
  const library: NativeShellRoute = {kind: 'shell', destination: {kind: 'library', libraryId: 'actual-library'}};
  const read: NativeShellRoute = {kind: 'read', itemId: 'actual-item', sourceId: 'actual-source'};
  f.context.navigation.open(library, f.context.authority()); f.context.navigation.open(read, f.context.authority());
  renderer.render(read, {}); let element = renderer.renderRetained({});
  const residence = await mountRetained(element);
  const child = () => residence.rows()[0]!, firstKey = residence.keys()[0];
  const first = child(); assert.equal(first.props.target, read); assert.equal(first.props.active, true);
  assert.equal(first.props.preferences.getSnapshot(), source.getPreferenceSnapshot());
  first.props.onOpenSettings(); await residence.update(renderer.renderRetained({}));
  assert.equal(child().props.active, false); assert.equal(child().props.target, read); assert.equal(child().tag, first.tag);
  assert.deepEqual(residence.keys(), [firstKey], 'the original occurrence keeps its own committed key and host');
  assert.equal(f.context.navigation.back(f.context.authority()), true); await residence.update(renderer.renderRetained({}));
  assert.equal(child().props.active, true); child().props.onBack(); assert.equal(f.context.navigation.getSnapshot().route, library);
  assert.equal(renderer.renderRetained({}), null); await residence.unmount(); renderer.dispose();
  assert.equal(source.current(), true); assert.equal(f.requests, 0); f.close(); assert.equal(source.current(), false);
});

test('the actual Read renderer refuses creation on TV before mounting a WebView', async () => {
  const host = hooks();
  const {createNativeReadRenderer} = await screenModule(new URL('../src-next/screens/read/ReadRoute.tsx', import.meta.url), {
    react: host.react, 'react-native': {Platform: {isTV: true}}, './host-component': {nativeReadHost: () => {throw new Error('TV must not resolve a WebView host');}}, './read-route-back': {readRouteBack: () => () => {}},
  });
  assert.throws(() => createNativeReadRenderer({}, {}, () => {}), /native_read_unavailable/);
});

test('the actual lazy host module is safe to evaluate on TV without evaluating a WebView package', async () => {
  const host = hooks();
  const {nativeReadHost} = await screenModule(new URL('../src-next/screens/read/host-component.ts', import.meta.url), {
    react: host.react, 'react-native': {Platform: {isTV: true}},
  });
  assert.throws(nativeReadHost, /native_read_unavailable/);
});

test('twenty visited Reading occurrences project at most two guest slots while preserving original routes', async () => {
  const f = authorityContext();
  const {createNativeReadRenderer} = await screenModule(new URL('../src-next/screens/read/ReadRoute.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}}, './host-component': {nativeReadHost: () => 'TrustedReadHost'},
    './read-route-back': {readRouteBack: (await import('../src-next/screens/read/read-route-back')).readRouteBack},
  });
  const renderer = createNativeReadRenderer(f.context, {getSnapshot: () => f.snapshot, subscribe: () => () => {}}, () => {});
  const original: NativeShellRoute[] = [];
  let element: any, residence: Awaited<ReturnType<typeof mountRetained>> | undefined;
  const children = () => residence!.rows();
  for (let index = 0; index < 20; index++) {
    if (index) {f.context.navigation.open({kind: 'search-reading', query: `book ${index}`}, f.context.authority()); renderer.renderRetained();}
    const route: NativeShellRoute = {kind: 'read', itemId: `item-${index}`, sourceId: `source-${index}`}; original.push(route);
    f.context.navigation.open(route, f.context.authority()); renderer.render(route); element = renderer.renderRetained();
    if (!residence) residence = await mountRetained(element);
    else await residence.update(element);
    const rows = children(); assert.equal(rows.length, index + 1);
    assert.ok(rows.filter((row: any) => row.props.resident).length <= 2);
    assert.equal(rows.filter((row: any) => row.props.active).length, 1);
    assert.equal(rows.at(-1)!.props.target, route);
  }
  for (let index = 19; index >= 0; index--) {
    assert.equal(f.context.navigation.getSnapshot().route, original[index]);
    if (index) {assert.equal(f.context.navigation.back(f.context.authority()), true); assert.equal(f.context.navigation.back(f.context.authority()), true);}
  }
  await residence?.unmount(); renderer.dispose(); f.close();
});

test('a third guest waits for the exact evicted incarnation teardown', async () => {
  const f = authorityContext();
  const {createNativeReadRenderer} = await screenModule(new URL('../src-next/screens/read/ReadRoute.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}}, './host-component': {nativeReadHost: () => 'TrustedReadHost'},
    './read-route-back': {readRouteBack: (await import('../src-next/screens/read/read-route-back')).readRouteBack},
  });
  const renderer = createNativeReadRenderer(f.context, {getSnapshot: () => f.snapshot, subscribe: () => () => {}}, () => {});
  const routes = [0, 1, 2].map(index => ({kind: 'read', itemId: `item-${index}`, sourceId: `source-${index}`} as const));
  let element: any;
  f.context.navigation.open(routes[0]!, f.context.authority()); renderer.render(routes[0]!); element = renderer.renderRetained();
  const residence = await mountRetained(element);
  const rows = () => residence.rows();
  const first = rows()[0]!, firstTicket = {}; first.props.onGuestState(true, firstTicket);
  f.context.navigation.open({kind: 'search-reading', query: 'second'}, f.context.authority()); renderer.renderRetained();
  f.context.navigation.open(routes[1]!, f.context.authority()); renderer.render(routes[1]!); await residence.update(renderer.renderRetained());
  const second = rows()[1]!, secondTicket = {}; second.props.onGuestState(true, secondTicket);
  f.context.navigation.open({kind: 'search-reading', query: 'third'}, f.context.authority()); renderer.renderRetained();
  f.context.navigation.open(routes[2]!, f.context.authority()); renderer.render(routes[2]!); await residence.update(renderer.renderRetained());
  assert.equal(rows()[0]!.props.resident, false); assert.equal(rows()[2]!.props.resident, false, 'successor waits for the original guest teardown');
  await residence.act(() => first.props.onGuestState(false, {})); assert.equal(rows()[2]!.props.resident, false, 'a different ticket cannot acknowledge eviction');
  await residence.act(() => first.props.onGuestState(false, firstTicket)); assert.equal(rows()[2]!.props.resident, true);
  assert.ok(rows().filter((row: any) => row.props.resident).length <= 2);
  await residence.unmount(); renderer.dispose(); f.close();
});

test('repeated openings of the same route object retain distinct presentation owners', async () => {
  const f = authorityContext();
  const {createNativeReadRenderer} = await screenModule(new URL('../src-next/screens/read/ReadRoute.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}}, './host-component': {nativeReadHost: () => 'TrustedReadHost'},
    './read-route-back': {readRouteBack: (await import('../src-next/screens/read/read-route-back')).readRouteBack},
  });
  const renderer = createNativeReadRenderer(f.context, {getSnapshot: () => f.snapshot, subscribe: () => () => {}}, () => {});
  const route: NativeShellRoute = {kind: 'read', itemId: 'same-item', sourceId: 'same-source'};
  f.context.navigation.open(route, f.context.authority()); renderer.render(route); let element = renderer.renderRetained();
  const residence = await mountRetained(element);
  const rows = () => residence.rows();
  const originalKey = residence.keys()[0];
  f.context.navigation.open({kind: 'search-reading', query: 'same'}, f.context.authority()); renderer.renderRetained();
  f.context.navigation.open(route, f.context.authority()); renderer.render(route); await residence.update(renderer.renderRetained());
  assert.equal(rows().length, 2); assert.equal(rows()[0]!.props.target, route); assert.equal(rows()[1]!.props.target, route);
  assert.equal(residence.keys()[0], originalKey); assert.notEqual(residence.keys()[1], originalKey);
  assert.equal(rows().filter(row => row.props.active).length, 1); assert.equal(rows()[1]!.props.active, true);
  f.context.navigation.back(f.context.authority()); f.context.navigation.back(f.context.authority());
  await residence.update(renderer.renderRetained());
  assert.equal(rows().length, 1); assert.equal(residence.keys()[0], originalKey); assert.equal(rows()[0]!.props.active, true);
  await residence.unmount(); renderer.dispose(); f.close();
});
