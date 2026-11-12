import test from 'node:test';
import assert from 'node:assert/strict';
import {PorticoServicesAccountClient, PorticoServicesCredentialApi, emailedCode} from '../src/portico-services-native.ts';
import {MFARequiredError} from '../src/hosted-security.ts';
import {ApiError, CredentialSessionService, type CredentialRecord} from '../src/index.ts';
import {presentError} from '../src/presentation/errors.ts';

// The native apps' sign-in at the new Portico Services, against a stand-in for it: what is sent,
// and what the apps' sign-in store is given back.
const origin = 'https://services.test';
const later = () => new Date(Date.now() + 12 * 3600000).toISOString();
type Call = {route: string; body?: any; headers: Record<string, string>; credentials?: string};
function services(routes: Record<string, (body: any, headers: Record<string, string>) => [number, unknown?]>) {
  const calls: Call[] = [];
  const fetcher = (async (url: string, init: RequestInit) => {
    const route = `${init.method} ${String(url).slice(origin.length)}`, headers = init.headers as Record<string, string>;
    const body = init.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({route, body, headers, credentials: init.credentials});
    const [status, answer] = routes[route]?.(body, headers) ?? [404, {error: {code: 'not_found'}}];
    return {ok: status < 300, status, headers: {get: () => null}, text: async () => answer === undefined ? '' : JSON.stringify(answer)};
  }) as unknown as typeof fetch;
  return {calls, fetcher};
}
const device = {name: 'Portico on Android', kind: 'phone'};
const account = {id: 'acct_1', username: 'ada', displayName: 'Ada', email: 'ada@example.test'};
const refusal = (code: string) => ({error: {code, message: 'words from Services'}});

test('a password sign-in gives the apps a session with both tokens, the account and 90 days', async () => {
  const s = services({
    'POST /v1/sessions': body => body.login === 'ada' && body.password === 'right' ? [200, {tokens: {accessToken: 'pat_1', refreshToken: 'prt_1', expiresAt: later()}}] : [401, refusal('credentials')],
    'GET /v1/account': (_, headers) => headers.Authorization === 'Bearer pat_1' ? [200, account] : [401, refusal('signed_out')],
  });
  const client = new PorticoServicesAccountClient(origin, device, s.fetcher, () => 'family-1');
  const session = await client.login('ada', 'right');
  assert.deepEqual(s.calls[0].body, {login: 'ada', password: 'right', device});
  assert.equal(s.calls[0].headers['Portico-Refresh'], undefined, 'an app keeps its tokens: no cookie');
  assert.equal(s.calls[0].credentials, 'omit');
  assert.equal(session.refreshToken, 'prt_1');
  assert.deepEqual(session.account, {id: 'acct_1', username: 'ada', displayName: 'Ada'});
  assert.equal(session.familyId, 'family-1');
  assert.ok(Math.abs(Date.parse(session.refreshExpiresAt) - Date.now() - 90 * 86400000) < 60000);

  await assert.rejects(client.login('ada', 'wrong'), (error: unknown) => {
    assert.ok(error instanceof ApiError);
    assert.equal(presentError(error, 'sign-in').category, 'invalid-input', 'a wrong password reads as "check what you typed"');
    return true;
  });
});

test('two-step: the sign-in asks for the code, and the code finishes it', async () => {
  const s = services({
    'POST /v1/sessions': () => [200, {twoStep: {challengeId: 'chl_1'}}],
    'POST /v1/sessions/two-step': body => body.challengeId === 'chl_1' && body.code === '123456' ? [200, {tokens: {accessToken: 'pat_2', refreshToken: 'prt_2', expiresAt: later()}}] : [403, refusal('two_step_wrong')],
    'GET /v1/account': () => [200, account],
  });
  const client = new PorticoServicesAccountClient(origin, device, s.fetcher);
  const challenge = await client.login('ada', 'right').then(() => assert.fail('signed in without the code'), (error: unknown) => {
    assert.ok(error instanceof MFARequiredError); return error.challenge;
  });
  await assert.rejects(client.mfa(challenge, '000000'), (error: unknown) => error instanceof ApiError && presentError(error, 'sign-in').category === 'invalid-input');
  assert.equal((await client.mfa(challenge, ' 123456 ')).accessToken, 'pat_2');
});

test('sign-up sends the code to the address, a taken username says so, and the code signs in the account this device signed up', async () => {
  const s = services({
    'POST /v1/accounts': body => body.username === 'taken' ? [409, refusal('username_taken')] : [202, {accountId: 'acct_new', email: body.email}],
    'POST /v1/accounts/confirm': body => body.accountId === 'acct_new' && body.code === 'ABCD-EFGH' ? [200, {tokens: {accessToken: 'pat_3', refreshToken: 'prt_3', expiresAt: later()}}] : [422, refusal('code_wrong')],
    'POST /v1/accounts/confirm/resend': () => [202],
    'GET /v1/account': () => [200, {...account, id: 'acct_new'}],
  });
  const client = new PorticoServicesAccountClient(origin, device, s.fetcher);
  await assert.rejects(client.register({username: 'taken', displayName: '', password: 'long enough', contactAddress: 'ada@example.test'}),
    (error: unknown) => error instanceof ApiError && error.status === 409 && error.code === 'username_unavailable');
  const registration = await client.register({username: 'ada', displayName: 'Ada', password: 'long enough', contactAddress: 'ada@example.test'});
  assert.equal(s.calls.at(-1)!.body.email, 'ada@example.test');
  assert.equal(registration.registrationId, 'acct_new');
  assert.equal(registration.status, 'contact_pending');
  assert.equal((await client.resendRegistration(registration)).registrationId, 'acct_new');
  const session = await client.verifyRegistration(registration, 'abcd efgh');
  assert.equal(s.calls.find(call => call.route === 'POST /v1/accounts/confirm')!.body.code, 'ABCD-EFGH', 'the code as Services writes it');
  assert.equal(session.account.id, 'acct_new');
  assert.equal(emailedCode('abcdefgh'), 'ABCD-EFGH');
});

test('the sign-in options offer email sign-up and help only when Services has email, and no Google or Apple yet', async () => {
  const on = services({'GET /v1/system': () => [200, {capabilities: ['email', 'google', 'apple']}]});
  const options = await new PorticoServicesAccountClient(origin, device, on.fetcher).options();
  assert.equal(options.registration, true); assert.equal(options.recovery, true); assert.deepEqual(options.providers, []);
  const off = services({'GET /v1/system': () => [200, {capabilities: []}]});
  assert.equal((await new PorticoServicesAccountClient(origin, device, off.fetcher).options()).registration, false);
});

test('the apps\' sign-in store renews at Portico Services and keeps the account and sign-in it renews', async () => {
  let refresh = 'prt_a';
  const s = services({
    'POST /v1/sessions/refresh': body => body.refreshToken === refresh ? [200, {accessToken: 'pat_b', refreshToken: (refresh = 'prt_b'), expiresAt: later()}] : [401, refusal('signed_out')],
    'DELETE /v1/sessions/current': (_, headers) => headers.Authorization ? [204] : [401, refusal('signed_out')],
  });
  let kept: CredentialRecord | undefined;
  const store = {load: async () => structuredClone(kept) ?? null, save: async (value: CredentialRecord) => { kept = structuredClone(value); }};
  const service = new CredentialSessionService(new PorticoServicesCredentialApi(origin, s.fetcher), store as never, async () => crypto.randomUUID());
  await service.authenticateIfSignedOut(async () => ({accessToken: 'pat_a', refreshToken: 'prt_a', expiresAt: new Date(Date.now() - 1000).toISOString(),
    refreshExpiresAt: new Date(Date.now() + 90 * 86400000).toISOString(), familyId: 'family-1', account: {id: 'acct_1', username: 'ada', displayName: 'Ada'}}));
  const renewed = await service.accessSession();
  assert.deepEqual(s.calls.map(call => call.route), ['POST /v1/sessions/refresh']);
  assert.equal(renewed.accessToken, 'pat_b');
  assert.equal(renewed.familyId, 'family-1');
  assert.equal(renewed.account.id, 'acct_1');
  assert.equal(kept?.active?.authority, 'prt_b', 'the rotated refresh token is the one kept');
});

test('ending a kept sign-in renews it once and signs it out, and one Services already ended is ended', async () => {
  const s = services({
    'POST /v1/sessions/refresh': body => body.refreshToken === 'prt_live' ? [200, {accessToken: 'pat_end', refreshToken: 'prt_next', expiresAt: later()}] : [401, refusal('signed_out')],
    'DELETE /v1/sessions/current': (_, headers) => headers.Authorization === 'Bearer pat_end' ? [204] : [401, refusal('signed_out')],
  });
  const api = new PorticoServicesCredentialApi(origin, s.fetcher);
  await api.revoke('prt_live');
  assert.deepEqual(s.calls.map(call => call.route), ['POST /v1/sessions/refresh', 'DELETE /v1/sessions/current']);
  await api.revoke('prt_gone');
  assert.equal(s.calls.at(-1)!.route, 'POST /v1/sessions/refresh', 'nothing more to end');
});
