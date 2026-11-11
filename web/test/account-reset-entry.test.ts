import test from 'node:test';
import {systemAnswer, signInAnswer} from '../../packages/client-core/test/next-wire.ts';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const serverId = 'srv_' + 's'.repeat(43), origin = 'https://reset.example';
function fixture(kind: 'local' | 'portico' = 'local') {
  let selected: unknown;
  let revision = 4, resetRole = 'member', wrongServer = false, twoStep = false, expiryMs: number | undefined;
  let securityGate: Promise<void> | undefined, passwordGate: Promise<void> | undefined, releaseSecurity: (() => void) | undefined, releasePassword: (() => void) | undefined;
  const hints = new Set<() => void>();
  let verifyGate: Promise<void> | undefined, releaseVerify: (() => void) | undefined;
  const calls: {path: string; body?: unknown}[] = [], retired: string[] = [], installed: string[] = [], admitted: string[] = [];
  const system = {id: serverId, name: 'Actual server', version: '1', signIn: kind, move: null, setupRequired: false, hostedConfigured: kind === 'portico', hostedAttached: kind === 'portico', apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const connection = {pin: {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)}, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const grant = (restricted: boolean) => {
    const id = wrongServer && restricted ? 'different-server' : serverId;
    return {authority: 'local', serverId: id, canManage: true,
      capabilities: {manageAccount: true, managePeople: resetRole === 'owner', manageServer: resetRole === 'owner', manageSignIn: resetRole === 'owner'},
      account: {id: 'actual-account', username: 'actual_user', role: resetRole, primaryProfileId: 'main', revision: 1, disabled: false, allowedLibraries: []},
      profiles: [{id: 'main', name: 'Actual person', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}],
      ...(restricted ? {passwordChangeRequired: true} : {}),
      session: {accessToken: restricted ? 'restricted-access' : 'ordinary-access', ...(restricted ? {} : {refreshToken: 'ordinary-refresh-credential'}), sessionFamilyId: restricted ? 'restricted-family' : 'ordinary-family', tokenGeneration: '1', installationId: 'i'.repeat(43), deviceId: 'device',
        expiresAt: restricted && expiryMs !== undefined ? new Date(Date.now() + expiryMs).toISOString() : '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId: id, accountId: 'actual-account', profileId: 'main', role: restricted ? 'account' : resetRole}}};
  };
  const api = new HttpLocalApi(origin, ''); api.getRouteConnection = () => connection as never; api.setAccessToken = value => {installed.push(value);};
  api.request = (async (path, _method, body) => {
    calls.push({path, body});
    if (path === '/v1/system') return systemAnswer(system);
    if (path === '/v1/auth/owner-reset/request') return {fileName: 'reset-code.txt', retryAfterSeconds: 1};
    if (path === '/v1/auth/owner-reset/verify' || path === '/v1/direct/password/reset') {if (verifyGate) await verifyGate; return {variant: 'password-change', ...grant(true)};}
    if (path === '/v1/auth/sign-in') return signInAnswer(twoStep ? {challenge: {token: 'actual-factor-challenge', expiresAt: '2099-01-01T00:00:00Z'}} : grant(false));
    if (path === '/v1/auth/two-factor') return signInAnswer(grant(false));
    if (path === '/v1/auth/move/code') return {username: 'actual_user', signIn: kind, expiresAt: new Date(Date.now() + 3600000).toISOString()};
    if (path === '/v1/auth/move/local') {if (verifyGate) await verifyGate; return grant(false);}
    throw new Error('Unexpected public route: ' + path);
  }) as HttpLocalApi['request'];
  const temporary = {request: async (path: string, _method: string, body?: unknown) => {
    calls.push({path: 'restricted:' + path, body});
    if (path === '/v1/direct/two-factor') {await securityGate;return {enabled: twoStep, pendingEnrolment: false, recoveryCodesRemaining: twoStep ? 5 : 0};}
    if (path === '/v1/direct/password') {await passwordGate;return;}
    throw new Error('Unexpected restricted route: ' + path);
  }} as HttpLocalApi;
  const entry = new BrowserAccountEntry({store: {selected: async () => selected, read: async () => ({version: 1, revision, servers: []}), connectionStorage: (_id: string, value: {expectedRevision: number}) => {assert.equal(value.expectedRevision, 4); return {read: async () => undefined, change: async () => {}};}} as never,
    actions: {} as never, environment: () => ({storage: {read: async () => undefined, change: async () => {}}}), restore: async () => undefined,
    inspect: async () => connection.pin, connect: (async () => ({api, connection})) as never,
    temporary: (_api, session) => {assert.equal(session.sessionFamilyId, 'restricted-family'); return temporary;},
    observeSelection: changed => {hints.add(changed);return () => {hints.delete(changed);};},
    remember: async (_api, session) => {admitted.push(session.sessionFamilyId);selected = {signIn: kind, connection: {pin: connection.pin, session}};}, retire: async (_api, session) => {retired.push(session.sessionFamilyId); calls.push({path: 'retire:' + session.sessionFamilyId});},
  }, {inspect: async () => system, getSnapshot: () => ({origin}), cancel: () => {}, dispose: () => {}} as unknown as ServerSignInProbe);
  return {entry, calls, retired, installed, admitted, owner: () => {resetRole = 'owner';}, wrongServer: () => {wrongServer = true;}, factor: () => {twoStep = true;}, revise: () => {revision++;for (const changed of hints) changed();},
    expireIn: (ms: number) => {expiryMs = ms;}, replacePin: () => {connection.pin.publicKey = 'changed-key';},
    holdSecurity: () => {securityGate = new Promise(resolve => {releaseSecurity = resolve;});}, releaseSecurity: () => releaseSecurity?.(),
    holdPassword: () => {passwordGate = new Promise(resolve => {releasePassword = resolve;});}, releasePassword: () => releasePassword?.(),
    hold: () => {verifyGate = new Promise(resolve => {releaseVerify = resolve;});}, release: () => releaseVerify?.()};
}
test('owner reset location is only the file name and wait, never a path, code readback or admission', async () => {
  const f = fixture(); await f.entry.inspect(origin); const location = await f.entry.requestLocalResetFile('actual_user');
  assert.deepEqual(location, {fileName: 'reset-code.txt', retryAfterSeconds: 1});
  assert.equal('code' in location!, false); assert.deepEqual(f.admitted, []); assert.deepEqual(f.installed, []); f.entry.dispose();
});
test('Portico kind cannot dispatch the Local reset-file or password flow', async () => {
  const f = fixture('portico'); await f.entry.inspect(origin); await f.entry.requestLocalResetFile('actual_user');
  await f.entry.resetLocalPassword({kind: 'member', username: 'actual_user', code: 'one-use-code'}, 'ordinary-new-password');
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system']); assert.deepEqual(f.admitted, []); f.entry.dispose();
});
test('a server selection changed after move preview refuses redemption before any code/password dispatch', async () => {
  const f = fixture(); await f.entry.inspect(origin); await f.entry.previewMoveCode('m'.repeat(43)); f.revise();
  await f.entry.redeemLocalMove('chosen_user', 'ordinary-new-password');
  assert.equal(f.calls.some(call => call.path === '/v1/auth/move/local'), false); assert.deepEqual(f.admitted, []); f.entry.dispose();
});
test('a Portico move-code preview cannot enter Local password redemption or expose its code in state', async () => {
  const f = fixture('portico'); await f.entry.inspect(origin); await f.entry.previewMoveCode('m'.repeat(43));
  assert.equal(f.entry.getSnapshot().phase, 'move-portico'); assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /mmmmmmmm/);
  await f.entry.redeemLocalMove('chosen_user', 'ordinary-new-password');
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system', '/v1/auth/move/code']); assert.deepEqual(f.admitted, []); f.entry.dispose();
});

const settle = () => new Promise(resolve => setImmediate(resolve));
