import assert from 'node:assert/strict';
import {test} from 'node:test';
import {HostedAccountClient} from '../src/hosted-security.ts';
import {providerRecentProof, reconfirmWithProvider, type ProviderProofPorts} from '../src/provider-security-proof.ts';

const origin = 'https://accounts.example.test', scope = {accountId: 'acct_A', familyId: 'family_A'};
const code = 'C'.repeat(43), intent = {purpose: 'password_set_or_change' as const, target: '', operationId: 'operation-0000000001'};
type Call = {path: string; method: string; body: unknown; token?: string; completion?: string};

function fake(overrides: Record<string, unknown> = {}) {
  const calls: Call[] = [];
  const answers: Record<string, unknown> = {
    'POST /v1/identity/google/start': {transactionId: 'oid_A', expiresAt: '2030-01-01T00:00:00Z', mode: 'reauthenticate', authorizationUrl: 'https://accounts.google.com/o/oauth2/auth?x=1'},
    'POST /v1/identity/apple/start': {transactionId: 'oid_B', expiresAt: '2030-01-01T00:00:00Z', mode: 'reauthenticate', nativeNonce: 'nonce_B'},
    'GET /v1/identity/transactions/oid_A': {transactionId: 'oid_A', provider: 'google', mode: 'reauthenticate', status: 'verified', expiresAt: '2030-01-01T00:00:00Z', contactVerified: true, needsUsername: false},
    'GET /v1/identity/transactions/oid_B': {transactionId: 'oid_B', provider: 'apple', mode: 'reauthenticate', status: 'verified', expiresAt: '2030-01-01T00:00:00Z', contactVerified: true, needsUsername: false},
    'POST /v1/identity/transactions/oid_B/native-apple': {transactionId: 'oid_B', provider: 'apple', mode: 'reauthenticate', status: 'verified', expiresAt: '2030-01-01T00:00:00Z', contactVerified: true, needsUsername: false},
    'POST /v1/identity/transactions/oid_A/complete': {recentProof: {...scope, proof: 'proof_' + 'P'.repeat(40), expiresAt: '2030-01-01T00:00:00Z'}},
    ...overrides,
  };
  const client = new HostedAccountClient(origin, 'native', async (path, method, body, _signal, token, completion) => {
    calls.push({path, method, body, token, completion});
    const key = method + ' ' + path;
    if (!(key in answers)) throw new Error('unexpected ' + key);
    return answers[key];
  }, 'b'.repeat(43));
  return {client, calls};
}
const ports = (returned: string, opened: string[] = []): ProviderProofPorts => ({
  newRequestId: async () => 'request-0000000000000001',
  openBrowser: async (url, transactionId) => {opened.push(url + '|' + transactionId); return returned;},
});

test('a browser provider re-confirmation asks for a completion code and carries it to the proof', async () => {
  const {client, calls} = fake(), opened: string[] = [];
  const pending = await reconfirmWithProvider(client, {provider: 'google', intent, nativeApple: false, accountOrigin: origin}, 'token_A',
    ports(`${origin}/app/account-return#tx=oid_A&code=${code}`, opened));
  assert.deepEqual(opened, ['https://accounts.google.com/o/oauth2/auth?x=1|oid_A']);
  const start = calls[0]!.body as Record<string, unknown>;
  assert.equal(start.mode, 'reauthenticate'); assert.equal(start.completion, 'code'); assert.equal(start.sessionMode, 'native');
  assert.deepEqual(start.intent, intent); assert.equal(calls[0]!.token, 'token_A');
  assert.equal(calls[1]!.completion, code, 'the transaction is read with its own completion code');
  assert(Object.isFrozen(pending));
  const proof = await providerRecentProof(client, pending, '123456', scope, 'token_A');
  assert.equal(proof.proof, 'proof_' + 'P'.repeat(40));
  assert.deepEqual(calls[2]!.body, {factor: '123456', completionCode: code});
  assert(!JSON.stringify(calls).includes('password":'), 'no password crosses this flow');
});

test('native Apple needs no browser and no completion code', async () => {
  const {client, calls} = fake();
  let nonce: string | undefined;
  const pending = await reconfirmWithProvider(client, {provider: 'apple', intent, nativeApple: true, accountOrigin: origin}, 'token_A',
    {...ports('unused'), openBrowser: async () => assert.fail('no browser'), signInApple: async value => {nonce = value; return {idToken: 'id-token'};}});
  assert.equal(nonce, 'nonce_B');
  assert.equal('completion' in (calls[0]!.body as object), false);
  assert.equal(pending.completionCode, undefined);
  assert.deepEqual(calls[1]!.body, {idToken: 'id-token', displayName: ''});
});

for (const nativeApple of [false, true]) {
  test(`the selected identity is captured before awaiting a request ID (native Apple ${nativeApple})`, async () => {
    const {client, calls} = fake();
    const expectedIdentity = {id: 'selected-link', revision: '9223372036854775807'};
    const pending = await reconfirmWithProvider(client, {provider: nativeApple ? 'apple' : 'google', intent, nativeApple, accountOrigin: origin, expectedIdentity}, 'token_A', {
      ...ports(`${origin}/app/account-return#tx=oid_A&code=${code}`),
      newRequestId: async () => {expectedIdentity.id = 'different-link'; expectedIdentity.revision = '1'; return 'request-0000000000000001';},
      signInApple: async () => ({idToken: 'fixture-token'}),
    });
    assert.equal(pending.intent.operationId, intent.operationId);
    assert.deepEqual((calls[0]!.body as {expectedIdentity: unknown}).expectedIdentity, {id: 'selected-link', revision: '9223372036854775807'});
    assert.equal(calls[0]!.token, 'token_A');
  });
}

test('a wrong, foreign or unconfirmed return proves nothing', async () => {
  for (const returned of [`${origin}/app/account-return#tx=oid_Z&code=${code}`, `https://evil.example/app/account-return#tx=oid_A&code=${code}`,
    `${origin}/app/account-return#tx=oid_A`, `${origin}/app/account-return#tx=oid_A&code=${code}&proof=x`]) {
    const {client, calls} = fake();
    await assert.rejects(reconfirmWithProvider(client, {provider: 'google', intent, nativeApple: false, accountOrigin: origin}, 'token_A', ports(returned)));
    assert.equal(calls.some(call => call.path.endsWith('/complete')), false);
  }
  for (const view of [{status: 'failed'}, {mode: 'login'}, {provider: 'apple'}]) {
    const {client} = fake({'GET /v1/identity/transactions/oid_A': {transactionId: 'oid_A', provider: 'google', mode: 'reauthenticate', status: 'verified', expiresAt: '2030-01-01T00:00:00Z', contactVerified: true, needsUsername: false, ...view}});
    await assert.rejects(reconfirmWithProvider(client, {provider: 'google', intent, nativeApple: false, accountOrigin: origin}, 'token_A', ports(`${origin}/app/account-return#tx=oid_A&code=${code}`)), String(Object.keys(view)));
  }
});

test('only credential changes on an app session use it, and only a proof finishes it', async () => {
  const {client} = fake();
  for (const purpose of ['provider_link', 'account_deletion_preview', 'server_transfer'] as const)
    await assert.rejects(reconfirmWithProvider(client, {provider: 'google', intent: {...intent, purpose}, nativeApple: false, accountOrigin: origin}, 'token_A', ports('x')));
  await assert.rejects(reconfirmWithProvider(client, {provider: 'google', intent, nativeApple: true, accountOrigin: origin}, 'token_A', ports('x')), 'native Apple for Google');
  const browser = new HostedAccountClient(origin, 'browser', async () => assert.fail('no request'));
  await assert.rejects(reconfirmWithProvider(browser, {provider: 'google', intent, nativeApple: false, accountOrigin: origin}, 'token_A', ports('x')));
  const linked = fake({'POST /v1/identity/transactions/oid_A/complete': {securityResult: {...scope, status: 'complete'}}});
  await assert.rejects(providerRecentProof(linked.client, {transactionId: 'oid_A', provider: 'google', intent, completionCode: code}, '', scope, 'token_A'));
  const other = fake({'POST /v1/identity/transactions/oid_A/complete': {recentProof: {accountId: 'acct_B', familyId: 'family_A', proof: 'proof_' + 'P'.repeat(40), expiresAt: '2030-01-01T00:00:00Z'}}});
  await assert.rejects(providerRecentProof(other.client, {transactionId: 'oid_A', provider: 'google', intent, completionCode: code}, '', scope, 'token_A'), 'a proof for another account');
  await assert.rejects(providerRecentProof(fake().client, {transactionId: 'oid_A', provider: 'google', intent, completionCode: 'short'}, '', scope, 'token_A'));
});

test('a native browser sign-in completion sends its completion code', async () => {
  const {client, calls} = fake({'POST /v1/identity/transactions/oid_A/complete': {accessToken: 'a'.repeat(43)}});
  await client.complete('oid_A', {factor: '', completionCode: code}).catch(() => undefined);
  assert.equal((calls[0]!.body as {completionCode?: string}).completionCode, code);
});
