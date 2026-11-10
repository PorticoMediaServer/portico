import test from 'node:test';
import {systemAnswer, signInAnswer} from '../../packages/client-core/test/next-wire.ts';
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
    if (path === '/v1/system') return systemAnswer(system);
    if (path === '/v1/auth/sign-in') return signInAnswer({...snapshot, session: next});
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
test('a reused family is not signed out as its own predecessor', async () => {
  const f = await fixture(session('admitted'));await f.entry.signIn('actual-user', 'fixture-input');
  assert.deepEqual(f.retired, []);f.entry.dispose();
});
