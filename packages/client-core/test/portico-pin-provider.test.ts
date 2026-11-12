import test from 'node:test';
import assert from 'node:assert/strict';
import {providerPINIdentity} from '../src/portico-pin-provider.ts';
import {porticoAccount, type Tokens} from '../src/portico-account.ts';
import {webRouteCrypto} from '../src/route-identity.ts';

const origin = 'https://accounts.example.test', pin = {serverId: 'srv_den', challenge: 'the-server-challenge-for-this-PIN'};
function fixture() {
  const sent: {path: string; body: any}[] = []; let adopted = 0;
  const tokens: Tokens = {accessToken: 'held-account', expiresAt: new Date(Date.now() + 3600000).toISOString()};
  const client = porticoAccount({current: () => tokens, keep: () => {adopted++;}, forget: () => {}}, origin, {fetcher: (async (url: string, init: RequestInit) => {
    const body = JSON.parse(String(init.body)); sent.push({path: new URL(url).pathname, body});
    const answer = url.endsWith('/provider') ? {id: 'sso_ada', authorizeUrl: 'https://accounts.google.com/authorize', nonce: 'new-apple-nonce'}
      : {identity: {payload: 'note'}, tokens: {accessToken: 'must-never-be-adopted'}};
    return new Response(JSON.stringify(answer), {status: 200});
  }) as typeof fetch});
  return {client, sent, adopted: () => adopted};
}

test('PIN provider browser proof starts a bound flow, checks its return and spends only its code/private secret; never adopts tokens', async () => {
  const f = fixture();
  await providerPINIdentity(f.client, 'google', pin, '123456', {accountOrigin: origin, current: () => true,
    signal: new AbortController().signal, crypto: webRouteCrypto, browser: async (url, flow) => {
      assert.equal(url, 'https://accounts.google.com/authorize'); assert.equal(flow, 'sso_ada');
      return 'portico://account-return?transactionId=sso_ada&code=' + 'c'.repeat(43);
    }});
  assert.equal(f.sent.length, 2);
  assert.equal(f.sent[0].path, '/v1/sessions/provider'); assert.deepEqual(f.sent[0].body.pin, pin);
  assert.equal(f.sent[0].body.provider, 'google'); assert.equal(f.sent[0].body.secretHash.length, 43);
  assert.equal(f.sent[1].path, '/v1/sessions/provider/complete');
  assert.equal(f.sent[1].body.id, 'sso_ada'); assert.equal(f.sent[1].body.code, 'c'.repeat(43));
  assert.equal(f.sent[1].body.secret.length, 43); assert.equal(f.sent[1].body.twoStepCode, '123456');
  assert.equal(f.adopted(), 0);
});

test('native Apple PIN proof uses this flow nonce and two-step, without minting an account session', async () => {
  const f = fixture();
  let providerFinished = false;
  await providerPINIdentity(f.client, 'apple', pin, async () => {assert.equal(providerFinished, true); return '234567';}, {accountOrigin: origin, current: () => true,
    signal: new AbortController().signal, crypto: webRouteCrypto, apple: async nonce => {
      assert.equal(nonce, 'new-apple-nonce'); providerFinished = true; return {idToken: 'a-new-provider-signed-token'};
    }});
  assert.deepEqual(f.sent.map(call => call.body), [{provider: 'apple', kind: 'app', pin},
    {id: 'sso_ada', identityToken: 'a-new-provider-signed-token', twoStepCode: '234567'}]);
  assert.equal(f.sent[1].path, '/v1/sessions/apple'); assert.equal(f.adopted(), 0);
});

test('an unrelated return, account change or disposal never spends a provider proof on a PIN request', async () => {
  for (const failure of ['different-return', 'changed', 'disposed'] as const) {
    const f = fixture(), controller = new AbortController(); let current = true;
    await assert.rejects(providerPINIdentity(f.client, 'google', pin, '', {accountOrigin: origin, current: () => current,
      signal: controller.signal, crypto: webRouteCrypto, browser: async () => {
        if (failure === 'changed') current = false;
        if (failure === 'disposed') controller.abort();
        return 'portico://account-return?transactionId=someone-else&code=' + 'c'.repeat(43);
      }}));
    assert.equal(f.sent.length, 1, failure); assert.equal(f.adopted(), 0);
  }
});
