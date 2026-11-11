// The invited person's side on the rebuilt server: what an invitation shows, and joining with it.
import test from 'node:test';
import assert from 'node:assert/strict';
import {setInstallationIdentity} from '../src/installation.ts';
import {acceptServerInvitation, previewServerInvitation} from '../src/invitation-join.ts';
import {redeemMemberResetCode} from '../src/account-reset.ts';
import type {ProfileAPI} from '../src/profile-management.ts';

const INSTALLATION = 'installation_for_invitation_tests_00001';
const later = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();
const session = (over: {profileId?: string; role?: string; refreshToken?: string | null; accessToken?: string} = {}) => ({
  accessToken: over.accessToken ?? 'access_account', expiresAt: later(15), sessionId: 'ses_1', tokenGeneration: '1', sessionExpiresAt: later(60 * 24 * 90),
  deviceId: 'dev_1', installationId: INSTALLATION, ...(over.refreshToken === null ? {} : {refreshToken: over.refreshToken ?? 'refresh_token_0000000001'}),
  viewer: {accountId: 'acct_new', serverId: 'srv', role: over.role ?? 'account', ...(over.profileId ? {profileId: over.profileId} : {})},
});
type Call = {path: string; method: string; body: unknown; token: string};
function server(answers: Record<string, unknown>) {
  const calls: Call[] = [];
  const make = (token: string): ProfileAPI => ({
    request: async <T>(path: string, method = 'GET', body?: unknown) => {
      calls.push({path, method, body, token});
      const answer = answers[method + ' ' + path];
      if (answer === undefined) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
      if (answer instanceof Error) throw answer;
      return answer as T;
    },
    withAccessToken: next => make(next),
  });
  return {api: make(''), calls};
}
test.beforeEach(() => setInstallationIdentity({installationId: INSTALLATION, name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}));
test.after(() => setInstallationIdentity(undefined));

test('a link\'s or a typed code reads the same invitation, which names the server, the role and the libraries shared', async () => {
  const preview = {serverName: 'Home', role: 'member', allLibraries: false, libraries: ['Films', 'Shows'], expiresAt: later(60)};
  const s = server({'GET /v1/auth/invitations/K7QP-2MXD-9HRT-4WNC': preview});
  const read = await previewServerInvitation(s.api, {code: 'K7QP-2MXD-9HRT-4WNC'});
  assert.deepEqual(read, {serverName: 'Home', expiresAt: preview.expiresAt, role: 'member', libraries: [{name: 'Films'}, {name: 'Shows'}], allLibraries: false, signInMethod: 'local'});
  assert.deepEqual(await previewServerInvitation(s.api, {shortCode: 'k7qp2mxd9hrt4wnc'}), read);
  assert.equal(s.calls.length, 2);
  await assert.rejects(previewServerInvitation(s.api, {shortCode: 'K7QP-2MXD'}), {code: 'invitation_incomplete'});
  await assert.rejects(previewServerInvitation(s.api, {code: '../admin'}), {code: 'invitation_incomplete'});
  assert.equal(s.calls.length, 2, 'an incomplete code is never sent');
  await assert.rejects(previewServerInvitation(s.api, {code: 'c'.repeat(43)}), {code: 'not_found'});
  assert.equal(s.calls.at(-1)!.path, '/v1/auth/invitations/' + 'c'.repeat(43), 'a link\'s old long code is sent, so the server can say it no longer works');
});

test('joining makes the account and is the sign-in: the answer is kept, and a one-profile account opens its profile', async () => {
  const s = server({
    'POST /v1/auth/invitations/K7QP-2MXD-9HRT-4WNC/accept': {session: session(), account: {id: 'acct_new', username: 'dana', role: 'member'},
      profiles: [{id: 'main', name: 'Dana', color: 'blue', isPrimary: true, pinRequired: false}]},
    'POST /v1/profiles/main/select': session({profileId: 'main', role: 'member', refreshToken: null, accessToken: 'access_main'}),
  });
  const result = await acceptServerInvitation(s.api, {shortCode: 'K7QP2MXD9HRT4WNC'}, {username: ' dana ', password: 'Long enough 1', profileName: ' Dana '});
  if (result.kind !== 'signed-in') return assert.fail('expected a sign-in');
  assert.equal(result.snapshot.account.id, 'acct_new'); assert.equal(result.session.viewer.profileId, 'main');
  assert.equal(result.session.refreshToken, 'refresh_token_0000000001', 'the sign-in the join answered with is the one kept');
  assert.deepEqual(s.calls[0]!.body, {username: 'dana', password: 'Long enough 1', profileName: 'Dana',
    device: {installationId: INSTALLATION, name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}});
  assert.equal(s.calls[1]!.token, 'access_account');
  const taken = Object.assign(new Error('taken'), {status: 409, code: 'username_taken'});
  await assert.rejects(acceptServerInvitation(server({'POST /v1/auth/invitations/K7QP-2MXD-9HRT-4WNC/accept': taken}).api, {code: 'K7QP-2MXD-9HRT-4WNC'}, {username: 'dana', password: 'x'}), error => error === taken);
});

test('a reset code sets the new password and signs nobody in', async () => {
  const s = server({'POST /v1/auth/password-reset/confirm': undefined});
  (s as {api: ProfileAPI}).api = {request: async <T>(path: string, method?: string, body?: unknown) => {s.calls.push({path, method: method!, body, token: ''}); return undefined as T;}};
  await redeemMemberResetCode(s.api, ' dana ', ' K7QP-2MXD-9HRT ', 'A new password 1');
  assert.deepEqual(s.calls, [{path: '/v1/auth/password-reset/confirm', method: 'POST', body: {username: 'dana', code: 'K7QP-2MXD-9HRT', newPassword: 'A new password 1'}, token: ''}]);
});
