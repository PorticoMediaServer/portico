// @fabric-react
import test, {after} from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, createReadingHost, connectReadingHost} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {routeBase64} from '../../../packages/client-core/src/route-identity';
import {nativeReadingSettingsSection} from '../src-next/screens/settings/reading-preferences';
import {closeReadingResource, createReadingSessionOwner} from '../src-next/screens/read/reading-session-owner';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import * as React from 'react';

// The native bundle supplies this compile-time flag; exercise its production path.
const previousDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__');
Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: false});
after(() => {if (previousDev) Object.defineProperty(globalThis, '__DEV__', previousDev); else Reflect.deleteProperty(globalThis, '__DEV__');});

const rows = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows);
const fields = rows.map(row => ({key: row.key!, type: typeof row.control.default === 'boolean' ? 'boolean' : typeof row.control.default === 'number' ? 'integer' : 'string',
  default: row.control.default, scopes: ['profile-server'], group: 'reading', labelKey: `preferences.${row.key}`,
  ...(row.control.options ? {allowedValues: row.control.options.map(option => option.value)} : {})}));
const preferences = parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'fixture', registry: {revision: 'fixture', fields},
  documents: [{scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {}},
    {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}}],
  effective: Object.fromEntries(fields.map(field => [field.key, field.default])),
  effectiveSource: Object.fromEntries(fields.map(field => [field.key, 'default'])), clampedFields: []});

for (const activity of [{initial: true, current: true}, {initial: false, current: false}, {initial: true, current: false}, {initial: false, current: true}])
test(`the original session keeps activity ${activity.initial}->${activity.current} and forwards retirement`, async t => {
  let current = true, input: ((message: string) => void) | undefined;
  const authority = new Set<() => void>(), calls: string[] = [], packets: Record<string, unknown>[] = [];
  const revision = 'a'.repeat(64), api = new HttpLocalApi('http://127.0.0.1:39141', 'reader-fixture-token', async (_url, init = {}) => {
    const method = init.method ?? 'GET'; calls.push(method);
    const data = method === 'POST' ? {sessionId: 'session', itemId: 'item', sourceId: 'source', revision, size: 100, format: 'pdf', metadata: {title: 'Stored title'}} : {progress: null};
    return method === 'DELETE' ? new Response(null, {status: 204}) : new Response(JSON.stringify(data), {headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
  });
  const life = new AbortController();
  const client = api.reading({viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}, isCurrent: () => current, signal: life.signal});
  const owner = createReadingSessionOwner({current: () => current, subscribe: listener => {authority.add(listener); return () => {authority.delete(listener);};},
    acquire: async () => ({session: await client.open({itemId: 'item', sourceId: 'source'}), release: () => client.dispose()})});
  await owner.ensure(); const ticket = owner.attach();
  const host = createReadingHost({session: ticket.session, channel: routeBase64(new Uint8Array(24).fill(7)), preferences: () => preferences,
    initialProgress: ticket.initialProgress, current: ticket.current, send: message => {packets.push(JSON.parse(message)); input?.(message);}, onBack() {}, onSettings() {}});
  ticket.onRetire(() => {host.dispose(); ticket.release();});
  host.setActive(activity.initial); host.setActive(activity.current);
  const connection = connectReadingHost({send: message => host.receive(message), subscribe(listener) {input = listener; return () => {input = undefined;};}});
  t.after(() => {connection.dispose(); void owner.dispose(); life.abort();}); const guest = await connection.ready;
  let observedActive: boolean | undefined; const offActive = guest.onActive(value => {observedActive = value;}); t.after(offActive);
  assert.equal(observedActive, activity.current);
  assert.equal(guest.session.signal.aborted, false); const before = calls.length;
  current = false; for (const listener of [...authority]) listener();
  assert.equal(guest.session.signal.aborted, true, 'retirement is delivered to the captured guest');
  assert.equal(packets.filter(packet => packet.kind === 'retired').length, 1);
  const count = packets.length; host.confirm(); assert.equal(packets.length, count);
  assert.equal(calls.length, before, 'retirement does not borrow another viewer');
});

for (const authorityRetired of [false, true])
test(`original ReadingClient close uses only the original authority (retired=${authorityRetired})`, async () => {
  let current = true; const calls: string[] = [], listeners = new Set<() => void>(), clientLife = new AbortController();
  const revision = 'a'.repeat(64), api = new HttpLocalApi('http://127.0.0.1:39141', 'reader-fixture-token', async (_url, init = {}) => {
    const method = init.method ?? 'GET'; calls.push(method);
    const data = method === 'POST' ? {sessionId: 'session', itemId: 'item', sourceId: 'source', revision, size: 100, format: 'pdf', metadata: {title: 'Stored title'}} : {progress: null};
    return method === 'DELETE' ? new Response(null, {status: 204}) : new Response(JSON.stringify(data), {headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
  });
  const client = api.reading({viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}, isCurrent: () => current, signal: clientLife.signal});
  const owner = createReadingSessionOwner({current: () => current, subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    acquire: async () => {const original = await client.open({itemId: 'item', sourceId: 'source'});
      return {session: {manifest: original.manifest, signal: original.signal, headPDF: original.headPDF, readRange: original.readRange,
        readAsset: original.readAsset, page: original.page, progress: original.progress, saveProgress: original.saveProgress,
        close: async closeSignal => {if (current) await original.close(closeSignal);}}, release: () => {clientLife.abort(); client.dispose();}};}});
  await owner.ensure();
  if (authorityRetired) {current = false; for (const listener of [...listeners]) listener();}
  await owner.dispose();
  assert.equal(calls.filter(method => method === 'DELETE').length, authorityRetired ? 0 : 1);
});

test('removing a retained route closes its original session while the same authority remains current', async () => {
  let authority = true, retained = true; const calls: string[] = [], listeners = new Set<() => void>(), clientLife = new AbortController();
  const revision = 'a'.repeat(64), api = new HttpLocalApi('http://127.0.0.1:39141', 'reader-fixture-token', async (_url, init = {}) => {
    const method = init.method ?? 'GET'; calls.push(method);
    const data = method === 'POST' ? {sessionId: 'session', itemId: 'item', sourceId: 'source', revision, size: 100, format: 'pdf', metadata: {title: 'Stored title'}} : {progress: null};
    return method === 'DELETE' ? new Response(null, {status: 204}) : new Response(JSON.stringify(data), {headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
  });
  const client = api.reading({viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}, isCurrent: () => authority, signal: clientLife.signal});
  const owner = createReadingSessionOwner({current: () => authority && retained, subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    acquire: async () => {const original = await client.open({itemId: 'item', sourceId: 'source'});
      return {session: {manifest: original.manifest, signal: original.signal, headPDF: original.headPDF, readRange: original.readRange,
        readAsset: original.readAsset, page: original.page, progress: original.progress, saveProgress: original.saveProgress,
        close: async closeSignal => {if (authority) await original.close(closeSignal);}}, release: () => {clientLife.abort(); client.dispose();}};}});
  await owner.ensure(); retained = false; for (const listener of [...listeners]) listener(); await owner.dispose();
  assert.equal(calls.filter(method => method === 'DELETE').length, 1);
  authority = false;
});


for (const removal of ['route', 'authority'] as const)
test(`actual live host with its presentation binding fences original close on ${removal} retirement`, async () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'original', viewerFence: 'a'.repeat(64)};
  const viewer = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'} as const;
  let valid = true; const listeners = new Set<() => void>(), calls: string[] = [];
  const revision = 'a'.repeat(64), navigation = new NativeShellNavigation(authority, () => authority);
  const route = {kind: 'read', itemId: 'item', sourceId: 'source'} as const;
  const api = new HttpLocalApi('http://127.0.0.1:39141', 'reader-fixture-token', async (url, init = {}) => {
    const method = init.method ?? 'GET'; calls.push(method);
    const data = method === 'POST' ? {sessionId: 'session', itemId: 'item', sourceId: 'source', revision, size: 100, format: 'pdf', metadata: {title: 'Stored title'}}
      : String(url).endsWith('/versions') ? {chosenSourceId: 'source', versions: [{sourceId: 'source', inventoryAvailable: true, size: 100, format: 'pdf'}]} : {progress: null};
    return method === 'DELETE' ? new Response(null, {status: 204}) : new Response(JSON.stringify(data), {headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
  });
  const context = {api, navigation, authority: () => authority, current: () => valid,
    session: () => ({viewer, sessionFamilyId: 'family'}),
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  navigation.open(route, authority);
  const presentation = navigation.presentationStack().at(-1)!;
  const binding = navigation.bindPresentation(presentation, {current: context.current, subscribe: context.subscribeAuthority}); assert.ok(binding);
  const {ReadNativeHost} = await screenModule(new URL('../src-next/screens/read/ReadNativeHost.tsx', import.meta.url), {...stackSlotFixture, 
    react: {...React, default: React}, 'react-native': {View: 'View', AppState: {addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47})},
    '../../../../../packages/client-core/src': {preferenceValue: () => undefined, ReadingError: class extends Error {}},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../downloads/device': {createNativeDownloadsDevice: () => {throw new Error('unexpected offline device');}},
    '../../../platform/offline-downloads': {offlineDownloads: {}},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', StateView: 'StateView', SkeletonBlock: 'SkeletonBlock'},
    '../../kit/controls/Button': {Button: 'Button'}, '../../kit/theme': {useTheme: () => ({colors: {ink: '#000'}})},
    './NativeReadingGuestHost': {NativeReadingGuestHost: 'NativeReadingGuestHost'},
    './reading-session-owner': {createReadingSessionOwner, closeReadingResource},
    './annotation-journal': {NativeAnnotationJournal: class {unbind() {}}, nativeAnnotationIdentity: () => ({}), nativeAnnotationLeaseOf: () => undefined},
    './annotation-session': {nativeAnnotationBridge: (value: any) => value.session},
    './annotation-sync': {NativeAnnotationSync: class {dispose() {} flush() {return Promise.resolve();}}},
    './annotation-authoring': {NATIVE_OFFLINE_ANNOTATION_AUTHORING: false},
  });
  let tree: unknown; const source = {getSnapshot: () => preferences, subscribe: () => () => {}};
  const host = createFabricHookHost(() => {tree = ReadNativeHost({context, target: route, binding, active: true,
    preferences: source, onBack() {}, onOpenSettings() {}});});
  try {
    await act(async () => {await host.mount(); for (let i = 0; i < 4; i++) await new Promise<void>(resolve => setImmediate(resolve));});
    assert.equal((tree as {type?: unknown} | undefined)?.type, 'NativeReadingGuestHost', 'the original accepted session reached the ready guest outlet');
    assert.equal(calls.filter(method => method === 'POST').length, 1);
    if (removal === 'route') navigation.back(authority);
    else {valid = false; for (const listener of [...listeners]) listener();}
    await act(async () => {for (let i = 0; i < 3; i++) await new Promise<void>(resolve => setImmediate(resolve));});
    assert.equal(calls.filter(method => method === 'DELETE').length, removal === 'route' ? 1 : 0);
  } finally {await host.unmount(); binding!.dispose(); navigation.dispose();}
});
