import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {RememberedServer} from '@core/server-connections.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const origin = 'https://entry.example', serverId = 'srv_' + 's'.repeat(43);
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
function session(family: string, accountId = 'account-a', role = 'account'): LocalSession {
  return {accessToken: 'access-' + family, refreshToken: 'refresh-credential-' + family, sessionFamilyId: family, tokenGeneration: '1',
    installationId: 'i'.repeat(43), deviceId: 'device', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
    viewer: {serverId, authority: 'local', accountId, profileId: 'main', role}};
}
const record = (value: LocalSession): RememberedServer => ({version: '1', logicalOrigin: origin, pin, session: value, routes: {paired: [origin]}});
async function fixture(previous = session('previous')) {
  let raw: AppSessionRecord | undefined, fail = false, supersede = false, cleanupFails = false;
  const events: string[] = [], retired: string[] = [];
  const store = new AppSessionStore({read: async () => raw, change: async update => {if (fail) throw new Error('fixture-write-failure');raw = update(raw);}});
  await store.connectionStorage(serverId, {signIn: 'local', expectedRevision: 0}).change(() => record(previous));
  const next = session('admitted'), system = {id: serverId, name: 'Actual server', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const snapshot = {authority: 'local', serverId, canManage: false, twoStepSetupRequired: true,
    capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'account-a', username: 'actual-user', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Actual profile', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false}]};
  const connection = {pin, dispose() {}, getSnapshot: () => ({phase: 'ready'})};
  const api = new HttpLocalApi(origin, '');api.getRouteConnection = () => connection as never;api.setAccessToken = () => {};
  api.request = (async path => {
    if (path === '/v1/system') return system;
    if (path === '/v1/direct/sign-in') return {...snapshot, session: next};
    throw new Error('Unexpected fixture route');
  }) as HttpLocalApi['request'];
  const entry = new BrowserAccountEntry({store, actions: {} as never,
    environment: () => ({storage: store.connectionStorage(serverId)}), restore: async () => undefined,
    inspect: async () => pin, connect: (async () => ({api, connection})) as never,
    remember: async (_api, granted, environment) => {
      events.push('write-start');await environment.storage.change(() => record(granted));events.push('write-committed');
      if (supersede) await store.connectionStorage(serverId, {signIn: 'local', expectedRevision: (await store.read()).revision}).change(() => record(session('successor')));
    },
    retire: async (_api, captured) => {events.push('retire:' + captured.sessionFamilyId);retired.push(captured.sessionFamilyId);if (cleanupFails) throw new Error('fixture-offline');},
  }, {inspect: async () => system, getSnapshot: () => ({origin}), cancel() {}, dispose() {}} as unknown as ServerSignInProbe);
  await entry.inspect(origin);
  return {entry, store, events, retired, next, failWrite: () => {fail = true;}, supersede: () => {supersede = true;}, cleanupFails: () => {cleanupFails = true;}};
}
test('reauthentication retires only the exact restricted predecessor after committed current admission', async () => {
  const f = await fixture();await f.entry.signIn('actual-user', 'fixture-input');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'admitted');
  assert.deepEqual(f.events, ['write-start', 'write-committed', 'retire:previous']);
  assert.deepEqual(f.retired, ['previous']);assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');f.entry.dispose();
});
test('failed durable admission keeps the predecessor and retires only the uninstalled candidate', async () => {
  const f = await fixture();f.failWrite();await f.entry.signIn('actual-user', 'fixture-input');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'previous');
  assert.deepEqual(f.retired, ['admitted']);assert.equal(f.events.includes('write-committed'), false);f.entry.dispose();
});
test('a selection superseding the committed candidate prevents predecessor cleanup and preserves the successor', async () => {
  const f = await fixture();f.supersede();await f.entry.signIn('actual-user', 'fixture-input');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'successor');
  assert.deepEqual(f.retired, ['admitted']);assert.equal(f.entry.getSnapshot().phase, 'password');f.entry.dispose();
});
test('another account or an existing viewer family is not a restricted predecessor to retire', async () => {
  for (const previous of [session('other-account', 'account-b'), session('viewer', 'account-a', 'member')]) {
    const f = await fixture(previous);await f.entry.signIn('actual-user', 'fixture-input');
    assert.deepEqual(f.retired, []);assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'admitted');f.entry.dispose();
  }
});
test('a reused family is not signed out as its own predecessor', async () => {
  const f = await fixture(session('admitted'));await f.entry.signIn('actual-user', 'fixture-input');
  assert.deepEqual(f.retired, []);f.entry.dispose();
});
test('failed best-effort remote cleanup cannot roll back or sign out the committed new family', async () => {
  const f = await fixture();f.cleanupFails();await f.entry.signIn('actual-user', 'fixture-input');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'admitted');
  assert.deepEqual(f.retired, ['previous']);assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');f.entry.dispose();
});
