// Sign-in, the account's profiles and its devices against the new server's routes.
import test from 'node:test';
import assert from 'node:assert/strict';
import {withUnknownFields} from './unknown-fields.ts';
import {setInstallationIdentity} from '../src/installation.ts';
import {ManagedProfilesClient, completeDirectSignInChallenge, completeOwnerSetup, directSignIn, directSignInContinuation, parseDirectSnapshot, parseManagedProfiles, selectDirectProfile, type ProfileAPI} from '../src/profile-management.ts';
import {IdentityClient} from '../src/identity-client.ts';
import {parseDevices} from '../src/identity-devices.ts';

const INSTALLATION = 'installation_for_sign_in_tests_0000001';
const later = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();
const profile = (id: string, over: Record<string, unknown> = {}) => ({id, name: id, color: 'blue', isPrimary: id === 'main', pinRequired: false, ...over});
const account = (role = 'owner') => ({id: 'acct', username: 'sam', role});
const session = (over: {profileId?: string; role?: string; refreshToken?: string | null; accessToken?: string} = {}) => ({
  accessToken: over.accessToken ?? 'access_account', expiresAt: later(15), sessionId: 'ses_1', tokenGeneration: '1', sessionExpiresAt: later(60 * 24 * 90),
  deviceId: 'dev_1', installationId: INSTALLATION, ...(over.refreshToken === null ? {} : {refreshToken: over.refreshToken ?? 'refresh_token_0000000001'}),
  viewer: {accountId: 'acct', serverId: 'srv', role: over.role ?? 'account', ...(over.profileId ? {profileId: over.profileId} : {})},
});
type Call = {path: string; method: string; body: unknown; token: string};
/** A server that answers from a table and records what it was asked, and with whose token. */
function server(answers: Record<string, unknown | ((call: Call) => unknown)>) {
  const calls: Call[] = [];
  const make = (token: string): ProfileAPI => ({
    request: async <T>(path: string, method = 'GET', body?: unknown) => {
      const call = {path, method, body, token};
      calls.push(call);
      const answer = answers[method + ' ' + path];
      if (answer === undefined) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
      return (typeof answer === 'function' ? answer(call) : answer) as T;
    },
    withAccessToken: next => make(next),
  });
  return {api: make(''), calls};
}
test.beforeEach(() => setInstallationIdentity({installationId: INSTALLATION, name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}));
test.after(() => setInstallationIdentity(undefined));

test('signing in to an account with several profiles asks who is watching: nothing is chosen for the person', async () => {
  const s = server({'POST /v1/auth/sign-in': withUnknownFields({session: session(), account: account(), profiles: [profile('main'), profile('kid', {pinRequired: true})]})});
  const result = await directSignIn(s.api, 'sam', 'a password');
  assert.equal(result.kind, 'signed-in');
  if (result.kind !== 'signed-in') return;
  assert.equal(directSignInContinuation(result), 'profile-selection');
  assert.equal(result.session.viewer.role, 'account'); assert.equal(result.session.viewer.profileId, '');
  assert.deepEqual(result.snapshot.capabilities, {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false});
  assert.deepEqual(result.snapshot.profiles.map(p => [p.id, p.primary, p.pinRequired]), [['main', true, false], ['kid', false, true]]);
  assert.equal(s.calls.length, 1);
  assert.deepEqual(s.calls[0]!.body, {username: 'sam', password: 'a password', device: {installationId: INSTALLATION, name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}});
});

test('an account\'s only profile opens by itself when it has no PIN, with the sign-in just given, and keeps its refresh token', async () => {
  const s = server({
    'POST /v1/auth/sign-in': {session: session(), account: account(), profiles: [profile('main')]},
    'POST /v1/profiles/main/select': session({profileId: 'main', role: 'owner', refreshToken: null, accessToken: 'access_main'}),
  });
  const result = await directSignIn(s.api, 'sam', 'a password');
  if (result.kind !== 'signed-in') return assert.fail('expected a sign-in');
  assert.equal(directSignInContinuation(result), 'viewer');
  assert.equal(result.session.accessToken, 'access_main'); assert.equal(result.session.viewer.profileId, 'main'); assert.equal(result.session.viewer.role, 'owner');
  assert.equal(result.session.refreshToken, 'refresh_token_0000000001', 'choosing a profile answers without a refresh token: the one from sign-in is still the right one');
  assert.deepEqual(result.snapshot.capabilities, {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true});
  assert.equal(s.calls[1]!.token, 'access_account', 'the profile is chosen with the new sign-in, not whatever the transport held before');
  // A PIN on the only profile still asks for it.
  const guarded = server({'POST /v1/auth/sign-in': {session: session(), account: account(), profiles: [profile('main', {pinRequired: true})]}});
  const asked = await directSignIn(guarded.api, 'sam', 'a password');
  assert.equal(directSignInContinuation(asked), 'profile-selection'); assert.equal(guarded.calls.length, 1);
});

test('two-step sign-in answers a challenge, finished with a code', async () => {
  const s = server({
    'POST /v1/auth/sign-in': {challenge: {token: 'challenge_token', expiresAt: later(5)}},
    'POST /v1/auth/two-factor': {session: session(), account: account('member'), profiles: [profile('main'), profile('kid')]},
  });
  const first = await directSignIn(s.api, 'sam', 'a password');
  assert.equal(directSignInContinuation(first), 'challenge');
  if (first.kind !== 'challenge') return;
  await assert.rejects(completeDirectSignInChallenge(s.api, first.token, '   '), {code: 'code_required'});
  const done = await completeDirectSignInChallenge(s.api, first.token, ' 123456 ');
  assert.equal(done.kind, 'signed-in');
  assert.deepEqual(s.calls.at(-1)!.body, {challenge: 'challenge_token', code: '123456'});
});

test('creating the owner sends the setup code with the owner\'s details and opens the main profile', async () => {
  const s = server({
    'POST /v1/setup/owner': {session: session(), account: account(), profiles: [profile('main')]},
    'POST /v1/profiles/main/select': session({profileId: 'main', role: 'owner', refreshToken: null}),
  });
  const result = await completeOwnerSetup(s.api, {setupCode: 'ABCD-EFGH', username: 'sam', password: 'a password'});
  assert.equal(result.kind === 'signed-in' && result.session.viewer.role, 'owner');
  assert.deepEqual(s.calls[0]!.body, {setupCode: 'ABCD-EFGH', username: 'sam', password: 'a password', device: {installationId: INSTALLATION, name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}});
});

test('only the main profile manages anything, and how much follows the account\'s role', () => {
  const read = (role: string, canManageAccount: boolean) => parseDirectSnapshot({me: {account: account(role === 'account' ? 'member' : role), role, canManageAccount}, profiles: {items: [profile('main'), profile('kid')]}}, {serverId: 'srv'}).capabilities;
  assert.deepEqual(read('owner', true), {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true});
  assert.deepEqual(read('admin', true), {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: false});
  assert.deepEqual(read('member', true), {manageAccount: true, managePeople: false, manageServer: false, manageSignIn: false});
  // The owner's other profiles are members, and a sign-in still at Who's watching manages nothing.
  assert.deepEqual(read('member', false), {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false});
  assert.deepEqual(read('account', false), {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false});
  assert.throws(() => parseDirectSnapshot({me: {account: account(), role: 'owner', canManageAccount: true}, profiles: {items: [profile('main')]}}, {serverId: 'srv', accountId: 'someone-else'}), {code: 'invalid_response'});
});

test('profiles: a colour this app does not know reads as blue; an unreadable profile refuses sign-in and is left out of a list shown', () => {
  assert.equal(parseManagedProfiles([profile('main', {color: 'ultraviolet'})])[0]!.art, 'blue');
  const rows = [profile('main'), {id: 'broken', name: 'No flags', color: 'blue'}];
  assert.throws(() => parseManagedProfiles(rows), {code: 'invalid_response'});
  assert.deepEqual(parseManagedProfiles(rows, true).map(p => p.id), ['main']);
  assert.throws(() => parseManagedProfiles([profile('a'), profile('b')]), {code: 'invalid_response'}, 'no main profile');
  assert.throws(() => parseManagedProfiles([profile('main'), profile('main')]), {code: 'invalid_response'}, 'the same profile twice');
  assert.throws(() => parseManagedProfiles(Array.from({length: 9}, (_, n) => profile(n ? 'p' + n : 'main'))), {code: 'invalid_response'}, 'more than an account may hold');
  assert.deepEqual(parseManagedProfiles(withUnknownFields([profile('main')]) as unknown[]).map(p => p.id), ['main']);
});

test('choosing a profile sends its PIN only when one was typed, and the answer must be for that profile', async () => {
  const scope = {authority: 'local' as const, accountId: 'acct', serverId: 'srv'};
  const s = server({'POST /v1/profiles/kid/select': session({profileId: 'kid', role: 'member', refreshToken: null})});
  await assert.rejects(selectDirectProfile(s.api, scope, 'kid', {pin: '12a4'}), {code: 'pin_format'});
  assert.equal(s.calls.length, 0, 'a malformed PIN is refused before anything is sent');
  const chosen = await selectDirectProfile(s.api, scope, 'kid', {pin: '4821', remember: true});
  assert.equal(chosen.session.viewer.profileId, 'kid'); assert.equal(chosen.session.refreshToken, undefined);
  assert.deepEqual(s.calls[0]!.body, {pin: '4821'});
  await selectDirectProfile(s.api, scope, 'kid', {});
  assert.deepEqual(s.calls[1]!.body, {});
  const other = server({'POST /v1/profiles/kid/select': session({profileId: 'main', role: 'owner', refreshToken: null})});
  await assert.rejects(selectDirectProfile(other.api, scope, 'kid', {}), /does not match/);
});

test('profile edits: the later save wins; an empty PIN removes it; a deleted profile is gone when the server answers', async () => {
  const s = server({'GET /v1/profiles': {items: [profile('main'), profile('kid')]}, 'POST /v1/profiles': profile('new'), 'PATCH /v1/profiles/kid': profile('kid'),
    'PUT /v1/profiles/kid/pin': profile('kid', {pinRequired: true}), 'DELETE /v1/profiles/kid/pin': undefined as never, 'DELETE /v1/profiles/kid': undefined as never});
  // An absent body in the table means "no content".
  const api: ProfileAPI = {request: async <T>(path: string, method = 'GET', body?: unknown) => method === 'DELETE' ? (s.calls.push({path, method, body, token: ''}), undefined as T) : s.api.request<T>(path, method, body)};
  const client = new ManagedProfilesClient(api, {authority: 'local', accountId: 'acct', serverId: 'srv'});
  const kid = (await client.list()).find(p => p.id === 'kid')!;
  await client.create(' New ', 'coral');
  await client.edit(kid, {name: ' Kiddo ', art: 'mint'});
  await client.edit(kid, {pin: '4821'});
  await client.edit(kid, {pin: ''});
  await assert.rejects(client.edit(kid, {pin: '12'}), /four numeric digits/);
  const states: string[] = [];
  const removed = await client.delete(await client.previewDelete(kid), {onStatus: status => states.push(status.state)});
  assert.equal(removed.state, 'removed'); assert.deepEqual(states, ['removed']);
  assert.deepEqual(s.calls.slice(1).map(call => [call.method, call.path, call.body]), [
    ['POST', '/v1/profiles', {name: 'New', color: 'coral'}],
    ['PATCH', '/v1/profiles/kid', {name: 'Kiddo', color: 'mint'}],
    ['PUT', '/v1/profiles/kid/pin', {pin: '4821'}],
    ['DELETE', '/v1/profiles/kid/pin', undefined],
    ['DELETE', '/v1/profiles/kid', undefined],
  ]);
  await assert.rejects(client.previewDelete((await client.list()).find(p => p.primary)!), /primary profile/);
});

test('signed-in devices say home or away and never an address; a device this app cannot read is left out', () => {
  const device = (id: string, over: Record<string, unknown> = {}) => ({id, name: 'Living room TV', platform: 'tvos', createdAt: later(-600), lastSeenAt: later(-5), network: 'home', signedIn: true, current: false, ...over});
  const devices = parseDevices(withUnknownFields({items: [device('dev_1', {current: true}), device('dev_2', {network: 'away', signedIn: false}), {id: 'dev_3'}]}));
  assert.deepEqual(devices.map(d => [d.id, d.network, d.sessions, d.current, d.ip]), [['dev_1', 'home', 1, true, ''], ['dev_2', 'away', 0, false, '']]);
  assert.throws(() => parseDevices({items: [device('dev_1', {current: true}), device('dev_2', {current: true})]}), {code: 'invalid_device_document'});
  assert.throws(() => parseDevices({items: [device('dev_1'), device('dev_1')]}), {code: 'invalid_device_document'});
});

test('devices and sign-ins: rename, sign a device out, sign out everywhere else, change the password', async () => {
  const s = server({'PATCH /v1/devices/dev_2': {id: 'dev_2', name: 'Den', createdAt: later(-600), lastSeenAt: later(-5), signedIn: true, current: false},
    'DELETE /v1/devices/dev_2': null, 'DELETE /v1/me/sessions': null, 'DELETE /v1/me/sessions/ses_9': null, 'POST /v1/auth/password': null,
    'GET /v1/me/sessions': {items: [{id: 'ses_1', deviceId: 'dev_1', deviceName: 'Laptop', createdAt: later(-600), lastUsedAt: later(-1), expiresAt: later(600), network: 'away', current: true},
      {id: 'ses_9', deviceId: 'dev_2', deviceName: 'Den', createdAt: later(-900), lastUsedAt: later(-800), expiresAt: later(100), endedAt: later(-700), endReason: 'ended_by_user', current: false}]}});
  const identity = new IdentityClient(s.api);
  assert.equal((await identity.editDevice('dev_2', {name: 'Den'})).name, 'Den');
  await identity.signOutDevice('dev_2'); await identity.signOutEverywhere(); await identity.endSession('ses_9');
  await identity.changePassword('old password', 'new password', '123456');
  assert.deepEqual((await identity.sessions()).map(row => [row.id, row.current, row.network, row.endReason]), [['ses_1', true, 'away', undefined], ['ses_9', false, undefined, 'ended_by_user']]);
  assert.deepEqual(s.calls.slice(0, 5).map(call => [call.method, call.path, call.body]), [
    ['PATCH', '/v1/devices/dev_2', {name: 'Den'}], ['DELETE', '/v1/devices/dev_2', undefined], ['DELETE', '/v1/me/sessions', undefined], ['DELETE', '/v1/me/sessions/ses_9', undefined],
    ['POST', '/v1/auth/password', {currentPassword: 'old password', newPassword: 'new password', code: '123456'}]]);
});
