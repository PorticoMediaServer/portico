import test from 'node:test';
import {setInstallationIdentity} from '@core/installation.ts';
import {signInAnswer,systemAnswer} from '../../packages/client-core/test/next-wire.ts';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

test.beforeEach(() => setInstallationIdentity({installationId: 'installation-' + 'i'.repeat(32), name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}));
test.after(() => setInstallationIdentity(undefined));

const origin = 'https://owner-file.example', serverId = 'srv_' + 's'.repeat(43);
function fixture(signIn: 'local' | 'portico' = 'portico') {
  let raw: AppSessionRecord | undefined, gate: Promise<void> | undefined, release = () => {};
  const calls: {path: string; body?: unknown}[] = [], retired: string[] = [], publicTokens: string[] = [];
  const pin = Object.freeze({serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)});
  const route = {pin, logicalOrigin: origin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const system = {id: serverId, name: 'Original server', version: '1', signIn, move: null, setupRequired: false, hostedConfigured: signIn === 'portico', hostedAttached: signIn === 'portico', apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const response = {
    authority: 'local', serverId, canManage: true,
    capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
    account: {id: 'owner', username: 'actual_owner', role: 'owner', hostedAccountId: 'actual-hosted-link', primaryProfileId: 'main', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Actual owner', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: true}],
    session: {accessToken: 'owner-access', refreshToken: 'owner-refresh-token-long-enough', refreshExpiresAt: '2099-01-01T00:00:00Z', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-01T00:00:00Z', sessionFamilyId: 'file-family', tokenGeneration: '1', deviceId: 'device', installationId: 'installation-' + 'i'.repeat(32),
      serverIdentity: {publicKey: pin.publicKey, fingerprint: pin.fingerprint}, viewer: {authority: 'local', serverId, accountId: 'owner', profileId: 'main', role: 'owner'}},
  };
  const store = new AppSessionStore({read: async () => raw, change: async (change, check) => {check?.(); raw = change(raw); check?.();}});
  const api = new HttpLocalApi(origin, ''); api.getRouteConnection = () => route as never; api.setAccessToken = value => {publicTokens.push(value);};
  api.request = (async (path, _method, body) => {
    calls.push({path, body});
    if (path === '/v1/system') return systemAnswer(system);
    if (path === '/v1/auth/owner-reset') return {fileName: 'reset-code.txt', retryAfterMs: 60000};
    if (path === '/v1/auth/owner-reset/session') {await gate; return signInAnswer(response);}
    throw new Error('Unexpected request ' + path);
  }) as HttpLocalApi['request'];
  const privateAPI = new HttpLocalApi(origin, response.session.accessToken); privateAPI.getRouteConnection = () => route as never;
  const entry = new BrowserAccountEntry({store, actions: {} as never, environment: () => ({storage: {read: async () => undefined, change: async () => {}}}), restore: async () => undefined,
    inspect: async () => pin, connect: (async () => ({api, connection: route})) as never, temporary: () => privateAPI,
    remember: async (_api, session, env, hosted, current) => {
      assert.equal(hosted, undefined); assert.equal(current?.(), true);
      await env.storage.change(() => ({version: '1', logicalOrigin: origin, pin, session, routes: {paired: [origin]}}));
    }, retire: async (_api, session) => {retired.push(session.sessionFamilyId);},
  }, {inspect: async () => system, getSnapshot: () => ({origin}), cancel: () => {}, dispose: () => {}} as unknown as ServerSignInProbe);
  return {entry, calls, retired, publicTokens, store, response, hold: () => {gate = new Promise(resolve => {release = resolve;});}, release: () => release(),
    successor: () => {raw = {version: 1, revision: 7, selectedServerId: serverId, servers: [{signIn: 'local', connection: {version: '1', logicalOrigin: origin, pin, session: {...response.session, sessionFamilyId: 'successor-family'}, routes: {paired: [origin]}}}]};}};
}
const settle = () => new Promise(resolve => setImmediate(resolve));
test('Local sign-in cannot enter the Portico owner-file purpose', async () => {
  const f = fixture('local'); await f.entry.inspect(origin);
  assert.equal(await f.entry.requestPorticoOwnerResetFile(), undefined); assert.equal(await f.entry.verifyPorticoOwnerResetFile('file-proof'), false);
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system']); f.entry.dispose();
});


test('the reset file admits the owner main profile with ordinary session renewal and no Hosted account', async () => {
  const f = fixture();
  try {
    await f.entry.inspect(origin); await f.entry.requestPorticoOwnerResetFile();
    assert.equal(await f.entry.verifyPorticoOwnerResetFile('file-proof'), true);
    assert.equal(f.entry.getSnapshot().phase, 'ready'); assert.equal(f.entry.getSnapshot().ownerResetSessionReady, true);
    const saved = await f.store.selected();
    assert.equal(saved?.connection.session.refreshToken, f.response.session.refreshToken);
    assert.equal(saved?.connection.session.viewer.profileId, 'main'); assert.equal(saved?.connection.hosted, undefined);
    await f.store.connectionStorage(serverId).change(saved => ({...saved!, session: {...saved!.session, accessToken: 'renewed-owner-access', tokenGeneration: '2'}}));
    assert.equal(await f.entry.completePorticoOwnerReset(() => true), true);
    assert.deepEqual(f.calls.map(call => call.path), ['/v1/system', '/v1/auth/owner-reset', '/v1/auth/owner-reset/session']);
    assert.deepEqual(Object.keys(f.calls[2].body as object), ['code', 'device']);
    assert.equal('code' in f.entry.getSnapshot(), false); assert.deepEqual(f.publicTokens, []);
  } finally {f.entry.dispose();}
});

test('a late owner response cannot replace a successor sign-in', async () => {
  const f = fixture();
  try {
    await f.entry.inspect(origin);f.hold();const work = f.entry.verifyPorticoOwnerResetFile('file-proof');
    await settle();f.successor();f.release();assert.equal(await work, false);
    assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'successor-family');
    assert.deepEqual(f.retired, ['file-family']);
  } finally {f.entry.dispose();}
});

test('a reset response without the existing owner main profile is refused before storage', async () => {
  const f = fixture();f.response.session.viewer.profileId = 'other';
  try {
    await f.entry.inspect(origin);assert.equal(await f.entry.verifyPorticoOwnerResetFile('file-proof'), false);
    assert.equal(await f.store.selected(), undefined);
  } finally {f.entry.dispose();}
});
