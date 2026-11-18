import assert from 'node:assert/strict';
import test from 'node:test';
import {servicesFetch, signInWithCode} from '../src/portico-code.ts';

type Sent = Readonly<{method: string; path: string; headers: [string, string][]; body: string | null}>;
/** Portico Services behind the app's `services_request`: a code, one "waiting", then approval. */
function services() {
  const sent: Sent[] = [];
  let redeems = 0;
  const invoke = async <T,>(command: string, args?: Record<string, unknown>) => {
    assert.equal(command, 'services_request');
    const request = (args as {request: Sent}).request; sent.push(request);
    const json = (status: number, value: unknown) => ({status, headers: [['content-type', 'application/json']], body: JSON.stringify(value)}) as T;
    if (request.path === '/v1/tv-codes') return json(201, {id: 'tvc_1', code: 'ABCD-EFGH', verifyUrl: 'https://web.getportico.tv/tv', verifyUrlWithCode: 'https://web.getportico.tv/tv#code=ABCD-EFGH', expiresAt: '2099-01-01T00:00:00Z', pollAfterMs: 1});
    if (request.path === '/v1/tv-codes/tvc_1/redeem') return json(200, ++redeems < 2 ? {state: 'waiting', pollAfterMs: 1} : {state: 'approved', tokens: {accessToken: 'access-1', refreshToken: 'refresh-1', expiresAt: '2099-01-01T00:00:00Z'}});
    if (request.path === '/v1/account') return json(200, {id: 'acc_1', username: 'justin', displayName: 'Justin'});
    if (request.path === '/v1/servers/srv_1/identity') return json(200, {identity: {note: 'for srv_1'}});
    return json(404, {error: {code: 'not_found'}});
  };
  return {sent, invoke};
}

test('a code approved in the browser signs the Portico Account in, and its note goes to the server asked', async () => {
  const fake = services(), opened: string[] = [];
  const access = await signInWithCode(fake.invoke, url => opened.push(url), {name: 'Portico on Linux', kind: 'desktop'}, new AbortController().signal);
  assert.deepEqual(opened, ['https://web.getportico.tv/tv#code=ABCD-EFGH'], 'the browser opens on the code, filled in');
  assert.equal(access.accountId, 'acc_1');
  const create = JSON.parse(fake.sent[0]!.body!) as {device: unknown; secretHash: string};
  assert.deepEqual(create.device, {name: 'Portico on Linux', kind: 'desktop'});
  const redeem = JSON.parse(fake.sent[1]!.body!) as {secret: string};
  const hash = Buffer.from(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(redeem.secret))).toString('base64url');
  assert.equal(hash, create.secretHash, 'only the hash of the secret was sent with the code');
  assert.deepEqual(await access.identity('srv_1', 'challenge-1'), {identity: {note: 'for srv_1'}});
  assert.ok(fake.sent.at(-1)!.headers.some(([name, value]) => name === 'authorization' && value === 'Bearer access-1'));
});

test('only Portico Services is reached through the app', async () => {
  await assert.rejects(servicesFetch(services().invoke)('https://evil.example/v1/account'), TypeError);
});
