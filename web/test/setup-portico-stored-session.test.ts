import test from 'node:test';
import assert from 'node:assert/strict';
import {accountAnswer, accountPath, systemAnswer} from '../../packages/client-core/test/next-wire.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {RememberedServer} from '@core/server-connections.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

// What first-run setup with a Portico Account keeps (the server's own session, filed as a Portico sign-in under the
// approving account, with no route key), read back the way the app does at its next start.
const origin = 'http://203.0.113.10:32500', serverId = 'srv_' + 's'.repeat(43), PORTICO = {origin: 'https://web.getportico.example', accountId: 'pacct_owner'};
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
const session: LocalSession = {accessToken: 'access-owner', refreshToken: 'refresh-credential-owner', sessionFamilyId: 'owner-family', tokenGeneration: '1',
  installationId: 'i'.repeat(43), deviceId: 'device', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
  viewer: {serverId, authority: 'local', accountId: 'acct', profileId: 'main', role: 'owner'}};
const settle = () => new Promise(resolve => setImmediate(resolve));
const owner = {authority: 'local', serverId, canManage: true, twoStepSetupRequired: false,
  capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
  account: {id: 'acct', username: 'sam', primaryProfileId: 'main', role: 'owner', revision: 1, disabled: false, allowedLibraries: []},
  profiles: [{id: 'main', name: 'Sam', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}]};

async function opened(stored: 'portico' | 'local', server: 'portico' | 'local') {
  let raw: AppSessionRecord | undefined;
  const store = new AppSessionStore({read: async () => raw, change: async update => {raw = update(raw);}});
  const remembered: RememberedServer = {version: '1', logicalOrigin: origin, pin, session, routes: {paired: [origin]}, ...(stored === 'portico' ? {hosted: PORTICO} : {})};
  await store.connectionStorage(serverId, {signIn: stored, ...(stored === 'portico' ? {hostedAccount: PORTICO} : {}), expectedRevision: 0}).change(() => remembered);
  const connection = {pin, dispose() {}, getSnapshot: () => ({phase: 'ready'})};
  const api = new HttpLocalApi(origin, '');api.getRouteConnection = () => connection as never;api.setAccessToken = () => {};
  api.request = (async (path: string) => {
    if (path === '/v1/system') return systemAnswer({id: serverId, name: 'Den', signIn: server});
    if (accountPath(path)) return accountAnswer(path, owner);
    throw new Error('Unexpected fixture route ' + path);
  }) as HttpLocalApi['request'];
  const entry = new BrowserAccountEntry({store, actions: {} as never, environment: () => ({storage: store.connectionStorage(serverId)}),
    restore: async captured => ({api, session: captured!}), inspect: async () => pin, connect: (async () => ({api, connection})) as never, remember: async () => {},
    observeSelection: () => () => {}});
  await entry.loadProfiles();
  for (let i = 0; i < 200 && (entry.getSnapshot() as {busy?: boolean}).busy; i++) await settle();
  const snapshot = entry.getSnapshot();
  entry.dispose();
  return {snapshot, record: raw};
}

test('the owner\'s sign-in kept by setup with a Portico Account opens again at the next start: it is a Portico sign-in on a Portico server', async () => {
  const {snapshot, record} = await opened('portico', 'portico');
  const opened_ = snapshot as {phase: string; error?: {message?: string}; server?: {signIn?: string}};
  assert.equal(opened_.error, undefined, String(opened_.error?.message));
  assert.deepEqual([opened_.phase, opened_.server?.signIn], ['profiles', 'portico']);
  assert.deepEqual(record?.servers[0]?.connection.hosted, PORTICO, 'the label is kept as it was stored: no route key is invented');
});

test('a local sign-in kept for a server that has since moved to Portico Accounts is not opened', async () => {
  const {snapshot} = await opened('local', 'portico');
  const refused = snapshot as {error?: {message?: string}; server?: unknown};
  assert.match(String(refused.error?.message), /sign-in changed/);
  assert.equal(refused.server, undefined, 'the server is not opened with it');
});
