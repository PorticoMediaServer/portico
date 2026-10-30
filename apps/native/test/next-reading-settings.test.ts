import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src';
import {PreferencesReader} from '../../../packages/client-core/src/preferences-reader';
import {bindNativeServerPreferences} from '../src-next/platform/server-preferences';
import {currentPreferenceSnapshot, parsePreferenceSnapshot, type PreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {nativeReadingPreferencesAvailable, nativeReadingSettingsPreferences, nativeReadingSettingsSection} from '../src-next/screens/settings/reading-preferences';
import type {ShellAuthority} from '../src-next/shell/model';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {openNativeReadingSettings} from '../src-next/screens/settings/reading-entry';
import {rememberNativeReadingPreferences, readRememberedReadingPreferences, rememberedReadingHistoryPaused} from '../src-next/shell/remembered-reading-settings';
import {NativeModules} from './react-native-stub.mjs';

const allRows = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows);
const fields = allRows.map(row => {
  const value = row.control.default;
  assert.ok(typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean');
  return {key: row.key!, type: typeof value === 'boolean' ? 'boolean' : typeof value === 'number' ? 'integer' : 'string', default: value,
    ...(row.control.options ? {allowedValues: row.control.options.map(option => option.value)} : {}),
    scopes: ['profile-server'], group: 'reading', labelKey: `preferences.${row.key}`};
});
function snapshot(revision = 3, overrides: Record<string, unknown> = {}): PreferenceSnapshot {
  return parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'reading-v1', registry: {revision: 'reading-v1', fields},
    documents: [{scope: 'profile-server', revision, activeRevision: revision, digest: 'a'.repeat(64), values: overrides},
      {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}}],
    effective: Object.fromEntries(fields.map(field => [field.key, overrides[field.key] ?? field.default])),
    effectiveSource: Object.fromEntries(fields.map(field => [field.key, field.key in overrides ? 'profile-server' : 'default'])), clampedFields: []});
}
async function fixture() {
  let preferences = snapshot(), valid = true;
  const authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'a'.repeat(64)};
  const authorityListeners = new Set<() => void>(), preferenceListeners = new Set<() => void>();
  const requests: {method: string; body?: any; signal?: AbortSignal | null}[] = [];
  let nextReply: ((method: string, body: any, signal?: AbortSignal | null) => Promise<unknown>) | undefined;
  const envelope = (data: PreferenceSnapshot, fence = 'a'.repeat(64)) => ({scope: {serverId: authority.serverId, viewerFence: fence}, data});
  const api = new HttpLocalApi('https://reading-settings.test', 'synthetic-account-token', async (_input, init) => {
    const method = init?.method ?? 'GET', body = init?.body ? JSON.parse(String(init.body)) : undefined;
    requests.push({method, body, signal: init?.signal});
    const reply = nextReply ? await nextReply(method, body, init?.signal) : envelope(preferences);
    return new Response(JSON.stringify(reply), {status: 200, headers: {'content-type': 'application/json'}});
  });
  const owner = {api, authority: () => authority, current: () => valid, preferences: () => preferences,
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
    subscribePreferences: (listener: () => void) => {preferenceListeners.add(listener); return () => {preferenceListeners.delete(listener);};}};
  const reader = new PreferencesReader(api, {serverId: authority.serverId, viewerId: authority.profileId});
  preferences = await reader.preferences('mobile'); requests.length = 0;
  bindNativeServerPreferences(owner, {read: deviceClass => reader.preferences(deviceClass), apply: (scope, deviceClass, revision, values) => reader.applyPreferences(scope, deviceClass, revision, values),
    accept: next => {preferences = currentPreferenceSnapshot(preferences, next); for (const listener of [...preferenceListeners]) listener(); return preferences;}});
  return {owner, requests, envelope,
    reply: (next: typeof nextReply) => {nextReply = next;},
    publish: (next: PreferenceSnapshot) => {preferences = next; for (const listener of [...preferenceListeners]) listener();},
    close: () => {valid = false; reader.dispose(); for (const listener of [...authorityListeners]) listener();},
    listeners: () => ({authority: authorityListeners.size, preferences: preferenceListeners.size})};
}

test('retained Read and Settings borrow one source, publishing canonical changes only after real CAS acknowledgement', async () => {
  const f = await fixture(), source = nativeReadingSettingsPreferences(f.owner);
  try {
    assert.equal(nativeReadingSettingsPreferences(f.owner), source);
    const before = source.getSnapshot();
    const confirmed = source.getPreferenceSnapshot();
    assert.equal(confirmed, source.getState().preferences, 'Core receives the same confirmed snapshot without reconstruction');
    assert.equal(source.getSnapshot(), before, 'external-store projection is stable');
    let finish!: (reply: unknown) => void;
    f.reply(async () => new Promise(resolve => {finish = resolve;}));
    const pending = source.patch({textSize: 6});
    assert.equal(source.getSnapshot(), before, 'no optimistic preference reaches the retained reader');
    assert.equal(source.getPreferenceSnapshot(), confirmed);
    assert.equal(source.getState().busy, true);
    assert.equal(f.requests[0]!.method, 'PATCH');
    assert.equal(f.requests[0]!.body.scope, 'profile-server');
    assert.equal(f.requests[0]!.body.deviceClass, 'mobile');
    assert.equal(f.requests[0]!.body.expectedRevision, 3);
    assert.deepEqual(f.requests[0]!.body.values, {'reading.textSize': 6});
    assert.equal(typeof f.requests[0]!.body.idempotencyKey, 'string');
    finish(f.envelope(snapshot(4, {'reading.textSize': 6})));
    assert.equal((await pending).textSize, 6);
    assert.equal(source.getPreferenceSnapshot(), source.getState().preferences);
    assert.equal(source.getPreferenceSnapshot().documents[0]!.revision, 4);
    assert.equal(f.owner.preferences(), source.getPreferenceSnapshot(), 'the root and retained reader receive the exact same confirmed acknowledgement');
    assert.equal(nativeReadingSettingsPreferences(f.owner).getSnapshot().textSize, 6, 'returning through Settings needs no second reader or SSE wait');
    f.publish(snapshot(5, {'reading.textSize': 7}));
    assert.equal(source.getSnapshot().textSize, 7);
    assert.equal(source.getState().preferences.documents[0]!.revision, 5);
    f.reply(async () => f.envelope(snapshot(6)));
    await source.patch({textSize: null});
    assert.equal(f.requests[1]!.body.expectedRevision, 5);
    assert.deepEqual(f.requests[1]!.body.values, {'reading.textSize': null}, 'Reset clears only the actual profile override');
  } finally {f.close();}
  assert.deepEqual(f.listeners(), {authority: 0, preferences: 0});
});

test('Reading settings reject unknown values and disabled dependent row writes without network mutations', async () => {
  const f = await fixture(), source = nativeReadingSettingsPreferences(f.owner);
  try {
    await assert.rejects(source.setRow('reading.font', 'invented-font'));
    await assert.rejects(source.setRow('not-a-row', 'anything'));
    f.publish(snapshot(4, {'reading.twoPages': false}));
    const dependent = allRows.find(row => row.id === 'comics.firstPageAlone')!;
    assert.equal(source.rowEnabled(dependent), false);
    await assert.rejects(source.setRow(dependent.id, true));
    assert.equal(f.requests.length, 0);
  } finally {f.close();}
});

test('a changed viewer fence refuses the preference acknowledgement and preserves the current projection', async () => {
  const f = await fixture(), source = nativeReadingSettingsPreferences(f.owner);
  try {
    await source.refresh();
    const before = source.getSnapshot();
    f.reply(async () => f.envelope(snapshot(4, {'reading.textSize': 6}), 'c'.repeat(64)));
    await assert.rejects(source.patch({textSize: 6}));
    assert.equal(source.getSnapshot(), before);
    assert.notEqual(source.getState().error, undefined);
  } finally {f.close();}
});

test('authority retirement aborts the real outstanding request and withholds late preferences', async () => {
  const f = await fixture(), source = nativeReadingSettingsPreferences(f.owner);
  let finish!: (reply: unknown) => void;
  f.reply(async () => new Promise(resolve => {finish = resolve;}));
  const pending = source.patch({textSize: 6});
  f.close();
  assert.equal(f.requests[0]!.signal?.aborted, true);
  finish(f.envelope(snapshot(4, {'reading.textSize': 6})));
  await assert.rejects(pending);
  assert.throws(source.getSnapshot);
  assert.deepEqual(f.listeners(), {authority: 0, preferences: 0});
});

test('Reading settings eligibility uses actual published preferences and never admits a TV or a partial registry', () => {
  const actual = snapshot();
  assert.equal(nativeReadingPreferencesAvailable(actual), true);
  assert.equal(nativeReadingPreferencesAvailable({...actual, deviceClass: 'television'}), false);
  assert.equal(nativeReadingPreferencesAvailable({...actual, documents: []}), false);
  assert.equal(nativeReadingPreferencesAvailable({...actual, registry: {...actual.registry,
    fields: actual.registry.fields.map(field => ({...field, scopes: ['profile-device-class']}))}}), false);
  assert.equal(nativeReadingPreferencesAvailable({...actual, effective: {}, registry: {...actual.registry, fields: []}}), false);
});

test('the Reading settings entry preserves the exact publication and library history instead of opening General', async () => {
  const f = await fixture(), authority = f.owner.authority(), navigation = new NativeShellNavigation(authority, f.owner.authority);
  const publication = {kind: 'read' as const, itemId: 'unit-publication', sourceId: 'unit-source'};
  try {
    navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: 'unit-library'}}, authority);
    navigation.open(publication, authority);
    assert.equal(openNativeReadingSettings({...f.owner, navigation}), true);
    assert.deepEqual(navigation.getSnapshot().route, {kind: 'settings-page', scope: 'account', page: 'reading'});
    assert.ok(navigation.retainedRoutes().includes(publication));
    assert.equal(navigation.back(authority), true);
    assert.equal(navigation.getSnapshot().route, publication, 'Back preserves actual route identity for the same retained controller');
    assert.deepEqual(navigation.getSnapshot().destination, {kind: 'library', libraryId: 'unit-library'});
    const before = navigation.getSnapshot();
    assert.equal(openNativeReadingSettings({...f.owner, navigation, preferences: () => ({...snapshot(), documents: []})}), false);
    assert.equal(navigation.getSnapshot(), before, 'an unavailable page never falls back to General or mutates history');
    f.close();
    assert.equal(openNativeReadingSettings({...f.owner, navigation}), false);
    assert.equal(navigation.getSnapshot(), before);
  } finally {f.close(); navigation.dispose();}
});


test('remembered Reading settings retain the confirmed snapshot only for the original owner', async () => {
  const runtime = NativeModules.PorticoRuntime as Record<string, any>;
  const oldRead = runtime.loadQueueRecord, oldWrite = runtime.saveQueueRecord;
  let record: string | null = null, current = true, family = 'family';
  runtime.loadQueueRecord = async (key: string) => {assert.equal(key, 'remembered-reading-settings:v1'); return record;};
  runtime.saveQueueRecord = async (key: string, value: string) => {assert.equal(key, 'remembered-reading-settings:v1'); record = value;};
  const viewer = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile'} as const;
  const owner = {...viewer, sessionFamilyId: 'family'};
  const context = {session: () => ({viewer, sessionFamilyId: family}), authority: () => viewer, current: () => current} as unknown as Parameters<typeof rememberNativeReadingPreferences>[0];
  const identity = {owner, current: () => current} as unknown as Parameters<typeof readRememberedReadingPreferences>[0];
  const actual = snapshot();
  try {
    assert.equal(rememberedReadingHistoryPaused(actual), true, 'missing history permission withholds writes');
    assert.equal(rememberedReadingHistoryPaused({...actual, effective: {...actual.effective, 'privacy.pauseWatchHistory': false}}), false);
    assert.equal(rememberedReadingHistoryPaused({...actual, effective: {...actual.effective, 'privacy.pauseWatchHistory': 'unknown'}}), true);
    assert.equal(await readRememberedReadingPreferences(identity, () => true, 'mobile'), undefined);
    await rememberNativeReadingPreferences(context, actual);
    assert.deepEqual(JSON.parse(record!).snapshot, actual);
    assert.deepEqual(await readRememberedReadingPreferences(identity, () => true, 'mobile'), actual);
    assert.equal(await readRememberedReadingPreferences(identity, () => true, 'tablet'), undefined);
    const other = {...identity, owner: {...owner, sessionFamilyId: 'other'}};
    assert.equal(await readRememberedReadingPreferences(other, () => true, 'mobile'), undefined);
    const damaged = JSON.parse(record!); damaged.snapshot.registry.fields = []; record = JSON.stringify(damaged);
    assert.equal(await readRememberedReadingPreferences(identity, () => true, 'mobile'), undefined);
    family = 'other'; current = false;
    await assert.rejects(rememberNativeReadingPreferences(context, actual));
    await assert.rejects(readRememberedReadingPreferences(identity, () => true, 'mobile'));
  } finally {runtime.loadQueueRecord = oldRead; runtime.saveQueueRecord = oldWrite;}
});
