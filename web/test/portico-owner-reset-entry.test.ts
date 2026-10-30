import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const origin = 'https://owner-file.example', serverId = 'srv_' + 's'.repeat(43);
function fixture(signIn: 'local' | 'portico' = 'portico') {
  let raw: AppSessionRecord | undefined, gate: Promise<void> | undefined, release = () => {};
  const calls: {path: string; body?: unknown}[] = [], retired: string[] = [], publicTokens: string[] = [];
  const pin = Object.freeze({serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)});
  const route = {pin, logicalOrigin: origin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const system = {id: serverId, name: 'Original server', version: '1', signIn, move: null, setupRequired: false, hostedConfigured: signIn === 'portico', hostedAttached: signIn === 'portico', apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const response = {
    variant: 'owner-session', authority: 'local', serverId, canManage: true, resetFileOwnerSession: true,
    capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
    account: {id: 'owner', username: 'actual_owner', role: 'owner', hostedAccountId: 'actual-hosted-link', primaryProfileId: 'main', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Actual owner', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: true}],
    session: {accessToken: 'access-only-owner', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-01T00:00:00Z', sessionFamilyId: 'file-family', tokenGeneration: '1', deviceId: 'device', installationId: 'installation',
      serverIdentity: {publicKey: pin.publicKey, fingerprint: pin.fingerprint}, viewer: {authority: 'local', serverId, accountId: 'owner', profileId: 'main', role: 'owner'}},
  };
  const store = new AppSessionStore({read: async () => raw, change: async (change, check) => {check?.(); raw = change(raw); check?.();}});
  const api = new HttpLocalApi(origin, ''); api.getRouteConnection = () => route as never; api.setAccessToken = value => {publicTokens.push(value);};
  api.request = (async (path, _method, body) => {
    calls.push({path, body});
    if (path === '/v1/system') return system;
    if (path === '/v1/auth/owner-reset/request') return {systemKind: 'linux', dataDirectory: '/fixture/state', fileDirectory: '/fixture/state', fileName: 'reset-code.txt', retryAfterSeconds: 1};
    if (path === '/v1/auth/owner-reset/verify') {await gate; return response;}
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
test('Portico file proof admits only the real access-only owner main profile without Hosted credentials or local password', async () => {
  const f = fixture(); await f.entry.inspect(origin);
  assert.ok(await f.entry.requestPorticoOwnerResetFile()); assert.deepEqual(f.calls.at(-1)?.body, {});
  assert.equal(await f.entry.verifyPorticoOwnerResetFile('file-proof'), true);
  assert.equal(f.entry.getSnapshot().ownerResetSessionReady, true);
  const selected = await f.store.selected(); assert.equal(selected?.resetFileOwner, true); assert.equal(selected?.connection.hosted, undefined);
  assert.equal(selected?.connection.session.refreshToken, undefined); assert.deepEqual(f.publicTokens, []);
  assert.equal(await f.entry.completePorticoOwnerReset(() => true), true);
  assert.equal(await f.entry.completePorticoOwnerReset(() => false), false);
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system', '/v1/auth/owner-reset/request', '/v1/auth/owner-reset/verify']);
  assert.deepEqual(f.retired, []); f.entry.dispose();
});
test('Local sign-in cannot enter the Portico owner-file purpose', async () => {
  const f = fixture('local'); await f.entry.inspect(origin);
  assert.equal(await f.entry.requestPorticoOwnerResetFile(), undefined); assert.equal(await f.entry.verifyPorticoOwnerResetFile('file-proof'), false);
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system']); f.entry.dispose();
});
test('late Portico proof after cancellation retires its original family without admission', async () => {
  const f = fixture(); await f.entry.inspect(origin); f.hold();
  const work = f.entry.verifyPorticoOwnerResetFile('file-proof'); await settle(); f.entry.cancelPorticoOwnerResetFile(); f.release();
  assert.equal(await work, false); assert.equal(await f.store.selected(), undefined); assert.deepEqual(f.retired, ['file-family']);
  assert.equal(f.entry.getSnapshot().phase, 'portico'); f.entry.dispose();
});
test('a successor saved during file proof survives the refused original admission and cleanup', async () => {
  const f = fixture(); await f.entry.inspect(origin); f.hold();
  const work = f.entry.verifyPorticoOwnerResetFile('file-proof'); await settle(); f.successor(); f.release();
  assert.equal(await work, false); assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'successor-family'); assert.deepEqual(f.retired, ['file-family']); f.entry.dispose();
});
test('a changed protected selection cannot use the retained done-page continuation', async () => {
  const f = fixture(); await f.entry.inspect(origin); assert.equal(await f.entry.verifyPorticoOwnerResetFile('file-proof'), true);
  f.successor(); assert.equal(await f.entry.completePorticoOwnerReset(() => true), false); f.entry.dispose();
});
test('a different server identity key is refused before protected admission', async () => {
  const f = fixture(); await f.entry.inspect(origin); f.response.session.serverIdentity.publicKey = 'x'.repeat(43);
  assert.equal(await f.entry.verifyPorticoOwnerResetFile('file-proof'), false); assert.equal(await f.store.selected(), undefined); assert.deepEqual(f.retired, ['file-family']); f.entry.dispose();
});
