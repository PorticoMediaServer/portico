import test from 'node:test';
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
    if (path === '/v1/system') return system;
    if (path === '/v1/auth/owner-reset/request') return {systemKind: 'linux', dataDirectory: '/fixture/state', fileDirectory: '/fixture/state', fileName: 'reset-code.txt', retryAfterSeconds: 1};
    if (path === '/v1/auth/owner-reset/verify' || path === '/v1/direct/password/reset') {if (verifyGate) await verifyGate; return {variant: 'password-change', ...grant(true)};}
    if (path === '/v1/direct/sign-in') return twoStep ? {challenge: {token: 'actual-factor-challenge', expiresAt: '2099-01-01T00:00:00Z'}} : grant(false);
    if (path === '/v1/auth/two-factor/challenge') return grant(false);
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
test('member reset spends its restricted grant only on password change, retires it, then ordinarily signs in', async () => {
  const f = fixture(); await f.entry.inspect(origin); await f.entry.resetLocalPassword({kind: 'member', username: 'actual_user', code: 'one-use-code'}, 'ordinary-new-password', '123456');
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system', '/v1/direct/password/reset', 'restricted:/v1/direct/password', 'retire:restricted-family', '/v1/direct/sign-in']);
  assert.deepEqual(f.calls[2].body, {currentPassword: '', newPassword: 'ordinary-new-password', code: '123456'});
  assert.deepEqual(f.installed, ['ordinary-access']); assert.deepEqual(f.admitted, ['ordinary-family']); assert.deepEqual(f.retired, ['restricted-family']); f.entry.dispose();
});
test('owner reset location is an actual public path, never a code readback or admission', async () => {
  const f = fixture(); await f.entry.inspect(origin); const location = await f.entry.requestLocalResetFile('actual_user');
  assert.deepEqual(location, {systemKind: 'linux', dataDirectory: '/fixture/state', fileDirectory: '/fixture/state', fileName: 'reset-code.txt', retryAfterSeconds: 1});
  assert.equal('code' in location!, false); assert.deepEqual(f.admitted, []); assert.deepEqual(f.installed, []); f.entry.dispose();
});
test('Local owner code proof checks the owner role and leaves ordinary admission to sign-in', async () => {
  const f = fixture(); f.owner(); await f.entry.inspect(origin);
  await f.entry.resetLocalPassword({kind: 'owner', code: 'private-file-code', turnOffTwoStep: true}, 'ordinary-new-password');
  assert.deepEqual(f.calls[1].body, {code: 'private-file-code', turnOffTwoStep: false});
  assert.deepEqual(f.calls.find(call => call.path === 'restricted:/v1/direct/password')?.body, {currentPassword: '', newPassword: 'ordinary-new-password', code: '', turnOffTwoStep: true}); assert.deepEqual(f.admitted, ['ordinary-family']); f.entry.dispose();
});
test('Portico kind cannot dispatch the Local reset-file or password flow', async () => {
  const f = fixture('portico'); await f.entry.inspect(origin); await f.entry.requestLocalResetFile('actual_user');
  await f.entry.resetLocalPassword({kind: 'member', username: 'actual_user', code: 'one-use-code'}, 'ordinary-new-password');
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system']); assert.deepEqual(f.admitted, []); f.entry.dispose();
});
test('a restricted grant arriving after leaving recovery is retired without changing password or admitting', async () => {
  const f = fixture(); await f.entry.inspect(origin); f.hold(); const work = f.entry.resetLocalPassword({kind: 'member', username: 'actual_user', code: 'one-use-code'}, 'ordinary-new-password');
  await new Promise(resolve => setImmediate(resolve)); f.entry.cancel(); f.release(); await work;
  assert.deepEqual(f.retired, ['restricted-family']); assert.equal(f.calls.some(call => call.path.startsWith('restricted:')), false); assert.deepEqual(f.admitted, []); f.entry.dispose();
});
test('factor completion preserves the password step selection revision and refuses a successor before sending its proof', async () => {
  const f = fixture(); f.factor(); await f.entry.inspect(origin); await f.entry.signIn('actual_user', 'ordinary-password');
  assert.equal(f.entry.getSnapshot().phase, 'challenge'); f.revise(); await f.entry.completeChallenge('123456');
  assert.equal(f.calls.some(call => call.path === '/v1/auth/two-factor/challenge'), false); assert.deepEqual(f.admitted, []); f.entry.dispose();
});
test('member move previews a code in memory, then ordinarily admits only its captured Local server grant', async () => {
  const f = fixture(); await f.entry.inspect(origin); await f.entry.previewMoveCode('m'.repeat(43));
  assert.equal(f.entry.getSnapshot().phase, 'move-local'); assert.equal(f.entry.getSnapshot().movePreview?.username, 'actual_user');
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /mmmmmmmm/); assert.deepEqual(f.admitted, []);
  await f.entry.redeemLocalMove('chosen_user', 'ordinary-new-password');
  assert.deepEqual(f.calls.at(-1), {path: '/v1/auth/move/local', body: {code: 'm'.repeat(43), username: 'chosen_user', password: 'ordinary-new-password'}});
  assert.deepEqual(f.admitted, ['ordinary-family']); assert.equal(f.entry.getSnapshot().phase, 'ready'); f.entry.dispose();
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

test('staged owner reset exposes actual security facts and finishes once without re-proving or admission', async () => {
  const f = fixture();f.owner();f.factor();await f.entry.inspect(origin);
  assert.deepEqual(await f.entry.verifyLocalOwnerReset('private-file-code'), {username: 'actual_user', twoStepEnabled: true});
  assert.equal(f.entry.getSnapshot().phase, 'owner-reset-password');
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /private-file-code|restricted-access|restricted-family/);
  assert.deepEqual(f.admitted, []);assert.deepEqual(f.installed, []);
  assert.equal(await f.entry.verifyLocalOwnerReset('private-file-code'), undefined, 'the spent code is never replayed');
  assert.equal(await f.entry.finishLocalOwnerResetPassword('ordinary-new-password', true), true);
  assert.equal(await f.entry.finishLocalOwnerResetPassword('ordinary-new-password', true), false);
  assert.deepEqual(f.calls.map(call => call.path), ['/v1/system', '/v1/auth/owner-reset/verify', 'restricted:/v1/direct/two-factor', 'restricted:/v1/direct/password', 'retire:restricted-family']);
  assert.equal(f.entry.getSnapshot().phase, 'password');assert.equal(f.entry.getSnapshot().ownerReset, undefined);
  assert.deepEqual(f.retired, ['restricted-family']);assert.deepEqual(f.admitted, []);f.entry.dispose();
});

test('preserving owner two-step requires ordinary sign-in MFA after the restricted password write', async () => {
  const f = fixture();f.owner();f.factor();await f.entry.inspect(origin);
  await f.entry.verifyLocalOwnerReset('private-file-code');
  assert.equal(await f.entry.finishLocalOwnerResetPassword('ordinary-new-password', false), true);
  assert.deepEqual(f.calls.find(call => call.path === 'restricted:/v1/direct/password')?.body, {currentPassword: '', newPassword: 'ordinary-new-password', code: '', turnOffTwoStep: false});
  await f.entry.signIn('actual_user', 'ordinary-new-password');
  assert.equal(f.entry.getSnapshot().phase, 'challenge');assert.deepEqual(f.admitted, []);f.entry.dispose();
});

test('owner recovery refuses a different server or non-owner before its security read or password write', async () => {
  for (const mismatch of ['role', 'server'] as const) {
    const f = fixture();if (mismatch === 'server') {f.owner();f.wrongServer();}await f.entry.inspect(origin);
    assert.equal(await f.entry.verifyLocalOwnerReset('private-file-code'), undefined);
    assert.equal(f.calls.some(call => call.path.startsWith('restricted:')), false);
    assert.deepEqual(f.retired, ['restricted-family']);assert.deepEqual(f.admitted, []);f.entry.dispose();
  }
});

test('cancelling during owner proof or security read retires the exact temporary family once', async () => {
  for (const step of ['proof', 'security'] as const) {
    const f = fixture();f.owner();await f.entry.inspect(origin);
    if (step === 'proof') f.hold();else f.holdSecurity();
    const work = f.entry.verifyLocalOwnerReset('private-file-code');await settle();
    f.entry.cancelLocalOwnerReset();f.release();f.releaseSecurity();
    assert.equal(await work, undefined);assert.deepEqual(f.retired, ['restricted-family']);
    assert.equal(f.entry.getSnapshot().phase, 'password');assert.equal(f.entry.getSnapshot().ownerReset, undefined);
    assert.equal(f.calls.some(call => call.path === 'restricted:/v1/direct/password'), false);assert.deepEqual(f.admitted, []);f.entry.dispose();
  }
});

test('idle selection replacement clears owner recovery facts and prevents any password dispatch', async () => {
  const f = fixture();f.owner();await f.entry.inspect(origin);await f.entry.verifyLocalOwnerReset('private-file-code');
  let cleared = 0;f.entry.subscribeAuthorityRetired(() => {cleared++;});
  f.revise();await settle();assert.equal(cleared, 1);assert.equal(f.entry.getSnapshot().ownerReset, undefined);
  assert.equal(await f.entry.finishLocalOwnerResetPassword('ordinary-new-password', false), false);
  assert.equal(f.calls.some(call => call.path === 'restricted:/v1/direct/password'), false);assert.deepEqual(f.retired, ['restricted-family']);f.entry.dispose();
});

test('owner recovery expiry retires the captured grant and clears its displayed facts', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: new Date('2026-10-24T00:00:00Z')});
  const f = fixture();f.owner();f.expireIn(1000);await f.entry.inspect(origin);await f.entry.verifyLocalOwnerReset('private-file-code');
  t.mock.timers.tick(1001);await settle();
  assert.equal(f.entry.getSnapshot().ownerReset, undefined);assert.equal(f.entry.getSnapshot().phase, 'password');assert.deepEqual(f.retired, ['restricted-family']);f.entry.dispose();
});

test('changed route identity refuses the owner password write and a late write cannot admit after cancellation', async () => {
  const changed = fixture();changed.owner();await changed.entry.inspect(origin);await changed.entry.verifyLocalOwnerReset('private-file-code');changed.replacePin();
  assert.equal(await changed.entry.finishLocalOwnerResetPassword('ordinary-new-password', true), false);
  assert.equal(changed.calls.some(call => call.path === 'restricted:/v1/direct/password'), false);assert.deepEqual(changed.admitted, []);changed.entry.dispose();
  const f = fixture();f.owner();await f.entry.inspect(origin);await f.entry.verifyLocalOwnerReset('private-file-code');f.holdPassword();
  const finishing = f.entry.finishLocalOwnerResetPassword('ordinary-new-password', true);await settle();
  f.entry.cancelLocalOwnerReset();f.releasePassword();assert.equal(await finishing, false);
  assert.deepEqual(f.retired, ['restricted-family']);assert.deepEqual(f.admitted, []);assert.equal(f.entry.getSnapshot().phase, 'password');f.entry.dispose();
});
