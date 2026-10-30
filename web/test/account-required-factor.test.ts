import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const origin = 'https://factor.example', serverId = 'srv_' + 's'.repeat(43);
const password = 'fixture-input';
function fixture() {
  let required = true, passwordChange = false, selected: {connection: {pin: typeof pin; session: LocalSession}; signIn: 'local'} | undefined;
  let holdPath: string | undefined, release!: () => void, pending: Promise<void> | undefined;
  const calls: {path: string; body: unknown}[] = [], admitted: string[] = [];
  let selections = 0, signIns = 0;
  const tokens: string[] = [];
  const system = {id: serverId, name: 'Actual server', signIn: 'local', move: null, version: '1', setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
  const snapshot = () => ({authority: 'local', serverId, canManage: !required,
    capabilities: {manageAccount: !required, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'account-a', username: 'actual-user', primaryProfileId: 'profile-a', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'profile-a', name: 'Actual profile', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false}],
    ...(required ? {twoStepSetupRequired: true} : {})});
  const session = {accessToken: 'account-access', refreshToken: 'account-refresh-credential', sessionFamilyId: 'account-family', tokenGeneration: '1', installationId: 'i'.repeat(43), deviceId: 'device',
    expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId, accountId: 'account-a', profileId: 'profile-a', role: 'account'}} as LocalSession;
  const enrolment = {secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Actual?secret=ABCDEFGHIJKLMNOP', recoveryCodes: ['fixture-code']};
  const connection = {pin, dispose() {}, getSnapshot: () => ({phase: 'ready'})};
  const api = new HttpLocalApi(origin, '');api.getRouteConnection = () => connection as never;api.setAccessToken = value => {tokens.push(value);};
  api.request = (async (path, _method, body) => {
    calls.push({path, body});if (path === holdPath) await pending;
    if (path === '/v1/system') return system;
    if (path === '/v1/direct/sign-in') return {...snapshot(), session: {...session, sessionFamilyId: ++signIns === 1 ? session.sessionFamilyId : 'successor-family'}, ...(passwordChange ? {passwordChangeRequired: true} : {})};
    if (path === '/v1/direct/password') {passwordChange = false;return {};}
    if (path === '/v1/direct') return snapshot();
    if (path === '/v1/direct/two-factor/enrol') return enrolment;
    if (path === '/v1/direct/two-factor/verify') return {enabled: true, pendingEnrolment: false, recoveryCodesRemaining: 1};
    throw new Error('Unexpected fixture route: ' + path);
  }) as HttpLocalApi['request'];
  const entry = new BrowserAccountEntry({
    store: {read: async () => ({version: 1, revision: 1, servers: selected ? [selected] : [], ...(selected ? {selectedServerId: serverId} : {})}), selected: async () => selected, connectionStorage: () => ({read: async () => undefined, change: async () => {}})} as never,
    actions: {switchProfile: async () => {selections++;}, signOutServer: async (captured: LocalSession) => {assert.equal(captured.sessionFamilyId, 'account-family');selected = undefined;}} as never,
    environment: () => ({storage: {read: async () => undefined, change: async () => {}}}),
    restore: async () => ({api, session}), inspect: async () => pin, connect: (async () => ({api, connection})) as never,
    remember: async (_api, granted) => {admitted.push(granted.sessionFamilyId);selected = {connection: {pin, session: granted}, signIn: 'local'};}, retire: async () => {},
  }, {inspect: async () => system, getSnapshot: () => ({origin}), cancel() {}, dispose() {}} as unknown as ServerSignInProbe);
  return {entry, calls, admitted, session, enrolment, tokens, get selections() {return selections;},
    clearRequirement: () => {required = false;}, requirePassword: () => {passwordChange = true;},
    replaceSelection: () => {selected = {connection: {pin, session: {...session, sessionFamilyId: 'successor-family'}}, signIn: 'local'};},
    hold: (path: string) => {holdPath = path;pending = new Promise(resolve => {release = resolve;});}, release: () => release()};
}
async function signIn(f: ReturnType<typeof fixture>) {await f.entry.inspect(origin);await f.entry.signIn('actual-user', password);}
async function dispatched(f: ReturnType<typeof fixture>, path: string) {
  for (let i = 0; i < 100 && !f.calls.some(call => call.path === path); i++) await new Promise(resolve => setImmediate(resolve));
  assert.ok(f.calls.some(call => call.path === path));
}
test('a required factor retains only the restricted account and cannot select a profile, including after restore', async () => {
  const f = fixture();await signIn(f);
  assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');assert.equal(f.entry.getSnapshot().snapshot?.twoStepSetupRequired, true);
  assert.deepEqual(f.admitted, ['account-family']);assert.equal(f.session.viewer.role, 'account');
  await f.entry.chooseProfile('profile-a');assert.equal(f.selections, 0);
  await f.entry.loadProfiles();assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');
  await f.entry.chooseProfile('profile-a');assert.equal(f.selections, 0);f.entry.dispose();
});
test('fresh-password enrollment returns ephemeral actual data without putting it in entry state', async () => {
  const f = fixture();await signIn(f);
  assert.equal(await f.entry.beginRequiredTwoStep(''), undefined);
  const value = await f.entry.beginRequiredTwoStep(password);assert.deepEqual(value, f.enrolment);
  assert.equal(f.calls.at(-1)?.path, '/v1/direct/two-factor/enrol');assert.deepEqual(f.calls.at(-1)?.body, {password});
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /ABCDEFGHIJKLMNOP|fixture-code|fixture-input/);
  assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');f.entry.dispose();
});
test('confirmed setup reads a genuine cleared account snapshot before offering profiles, without issuing a viewer', async () => {
  const f = fixture();await signIn(f);f.clearRequirement();
  assert.equal(await f.entry.confirmRequiredTwoStep(password, '123456'), true);
  assert.deepEqual(f.calls.slice(-2).map(call => call.path), ['/v1/direct/two-factor/verify', '/v1/direct']);
  assert.deepEqual(f.calls.at(-2)?.body, {password, code: '123456'});
  assert.equal(f.entry.getSnapshot().phase, 'profiles');assert.deepEqual(f.admitted, ['account-family']);
  assert.equal(f.session.viewer.role, 'account');assert.equal(f.selections, 0);f.entry.dispose();
});
test('a selected successor refuses setup before dispatching password or factor proof', async () => {
  const f = fixture();await signIn(f);f.replaceSelection();
  assert.equal(await f.entry.beginRequiredTwoStep(password), undefined);
  assert.equal(await f.entry.confirmRequiredTwoStep(password, '123456'), false);
  assert.equal(f.calls.some(call => call.path.includes('/two-factor/')), false);assert.equal(f.entry.getSnapshot().busy, false);f.entry.dispose();
});
test('a late enrollment cannot return its secret or alter entry after cancellation', async () => {
  const f = fixture();await signIn(f);f.hold('/v1/direct/two-factor/enrol');
  const work = f.entry.beginRequiredTwoStep(password);await dispatched(f, '/v1/direct/two-factor/enrol');
  f.entry.cancel();f.release();assert.equal(await work, undefined);assert.equal(f.entry.getSnapshot().phase, 'welcome');f.entry.dispose();
});
test('a verification response cannot bypass a still-required server snapshot', async () => {
  const f = fixture();await signIn(f);
  assert.equal(await f.entry.confirmRequiredTwoStep(password, '123456'), false);
  assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');assert.equal(f.selections, 0);f.entry.dispose();
});
test('password change precedes required setup when the actual sign-in requires both', async () => {
  const f = fixture();f.requirePassword();await signIn(f);
  assert.equal(f.entry.getSnapshot().phase, 'password-change');assert.equal(await f.entry.beginRequiredTwoStep(password), undefined);
  assert.equal(f.calls.some(call => call.path.includes('/two-factor/')), false);f.entry.dispose();
});
test('cancellation during verification cannot read onward or expose a ready profile state', async () => {
  const f = fixture();await signIn(f);f.hold('/v1/direct/two-factor/verify');
  const work = f.entry.confirmRequiredTwoStep(password, '123456');await dispatched(f, '/v1/direct/two-factor/verify');
  f.entry.cancel();f.release();assert.equal(await work, false);assert.equal(f.entry.getSnapshot().phase, 'welcome');
  assert.equal(f.calls.some(call => call.path === '/v1/direct'), false);f.entry.dispose();
});

test('password change clears the retired bearer before ordinary sign-in continues into required setup', async () => {
  const f = fixture();f.requirePassword();await signIn(f);
  await f.entry.changePassword(password, 'new-fixture-input');
  assert.equal(f.entry.getSnapshot().phase, 'password');assert.equal(f.tokens.at(-1), '');
  await f.entry.signIn('actual-user', 'new-fixture-input');
  assert.equal(f.entry.getSnapshot().phase, 'two-step-setup');
  assert.deepEqual(f.admitted, ['account-family', 'successor-family']);f.entry.dispose();
});
