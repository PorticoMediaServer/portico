import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {porticoAccount, ServicesError} from '../../../packages/client-core/src/portico-account.ts';
import {heldFor, hostedSessionFrom, porticoApiError} from '../../../packages/client-core/src/portico-services-native.ts';
import {ApiError, type HostedSession} from '../../../packages/client-core/src/http.ts';
import {routeBase64, webRouteCrypto} from '../../../packages/client-core/src/route-identity.ts';
import {SetupCodeService} from '../../../packages/client-core/src/setup-code.ts';
const origin = 'https://services.example.test', id = '12345678-1234-4234-8234-123456789abc';
const tokens = {accessToken: 'pat_fixture', refreshToken: 'prt_fixture', expiresAt: new Date(Date.now() + 3600000).toISOString()};
async function fixture() {
  let api: any, lost = false, status = 202, answer: unknown = {state: 'waiting', pollAfterMs: 9000}, accountFails = false;
  const records = new Map<string, unknown>(), calls: {path: string; body: any; headers: Headers}[] = [];
  class Store {async change(key: string, update: (raw: unknown) => any) {
    const next = update(structuredClone(records.get(key) ?? null)); records.set(key, structuredClone(next.record)); return next.value;
  }}
  const fetcher = (async (url: string, init: RequestInit) => {
    const path = url.slice(origin.length), body = init.body ? JSON.parse(String(init.body)) : undefined, headers = new Headers(init.headers);
    calls.push({path, body, headers}); assert.equal(init.credentials, 'omit');
    if (path === '/v1/tv-codes') {
      assert.ok(records.size, 'secret saved before create'); if (lost) {lost = false; throw new Error('lost reply');}
      return Response.json({id: 'tvc_fixture', code: 'ABCD-EFGH', verifyUrl: origin + '/tv', verifyUrlWithCode: origin + '/tv#code=ABCD-EFGH',
        expiresAt: new Date(Date.now() + 600000).toISOString(), pollAfterMs: 5000}, {status: 201});
    }
    if (path === '/v1/tv-codes/tvc_fixture/redeem') return Response.json(answer, {status});
    if (path === '/v1/account') return Response.json(accountFails ? {error: {code: 'busy'}} : {id: 'acct_fixture', username: 'ada', displayName: 'Ada'}, {status: accountFails ? 503 : 200});
    if (path === '/v1/sessions/current') return new Response(null, {status: 204});
    throw new Error('unexpected request ' + path);
  }) as typeof fetch;
  const {createNativePorticoTVSignIn} = await screenModule(new URL('../src-next/platform/tv-sign-in.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoRuntime: {newRequestId: async () => id.toUpperCase()}}, Platform: {OS: 'ios'}},
    '../../../../packages/client-core/src/query/connection-operations.ts': {}, '../../../../packages/client-core/src': {},
    '../../../../packages/client-core/src/setup-code': {SetupCodeService: class {
      constructor(public authority: string, supplied: any, public storage: any, public requestId: any) {api = supplied;}
    }},
    '../../../../packages/client-core/src/local-session': {}, '../../../../packages/client-core/src/installation': {},
    '../../../../packages/client-core/src/portico-account': {porticoAccount, ServicesError},
    '../../../../packages/client-core/src/portico-services-native': {heldFor, hostedSessionFrom, porticoApiError},
    '../../../../packages/client-core/src/http': {ApiError}, '../../../../packages/client-core/src/route-identity': {routeBase64},
    './queue-storage': {NativeQueueStore: Store}, './server-connections': {nativeNetworkFetch: fetcher, nativeRouteCrypto: webRouteCrypto},
    './account-authority': {hostedUrl: origin},
  });
  const service = createNativePorticoTVSignIn();
  return {api, service, calls, records, lose: () => {lost = true;}, reply: (code: number, value: unknown) => {status = code; answer = value;}, failAccount: () => {accountFails = true;}};
}

test('TV create retries a lost answer with the saved secret/key and shows the deployed /tv QR', async () => {
  const f = await fixture(), signal = new AbortController().signal;
  assert.equal(await f.service.requestId(), id); f.lose(); await assert.rejects(f.api.create(id, signal));
  const a = await f.api.create(id, signal);
  assert.deepEqual(f.calls[0].body, f.calls[1].body); assert.equal(f.calls[1].headers.get('Idempotency-Key'), id);
  assert.deepEqual(f.calls[1].body.device, {name: 'Portico on Apple TV', kind: 'tv'});
  assert.equal(f.calls[1].body.secretHash, routeBase64(await webRouteCrypto.sha256(new TextEncoder().encode(a.deviceCode))));
  await f.api.create(id, signal); assert.deepEqual(f.calls[1].body, f.calls[2].body);
  let journal: any;
  const real = new SetupCodeService(f.service.authority, {create: async () => a, poll: f.api.poll},
    {change: async update => journal = update(journal)}, async () => id);
  await real.start(); await (real as any).step(); real.pause();
  assert.equal(real.getSnapshot().userCode, 'ABCD-EFGH');
  assert.equal(real.getSnapshot().verificationUriComplete, origin + '/tv#code=ABCD-EFGH'); assert.equal('deviceCode' in real.getSnapshot(), false);
});

test('TV waiting, denial and expiry map to existing activation states', async () => {
  const f = await fixture(), signal = new AbortController().signal, authorization = await f.api.create(id, signal), entry = {requestId: id, authorization};
  await assert.rejects(f.api.poll(entry, signal, async () => {}), (e: ApiError) => e.code === 'authorization_pending' && e.retryAfterSeconds === 9);
  for (const [wire, code] of [['tv_code_denied', 'access_denied'], ['tv_code_gone', 'expired_token']]) {
    f.reply(410, {error: {code: wire}}); await assert.rejects(f.api.poll(entry, signal, async () => {}), (e: ApiError) => e.code === code);
  }
  assert.deepEqual(f.calls[1].body, {secret: authorization.deviceCode});
});

test('TV checkpoints its grant; cancellation is local and an abandoned grant is signed out', async () => {
  const f = await fixture(), signal = new AbortController().signal, authorization = await f.api.create(id, signal), entry = {requestId: id, authorization};
  f.reply(200, {state: 'approved', tokens}); let saved: unknown;
  const session: HostedSession = await f.api.poll(entry, signal, async (grant: unknown) => {saved = grant;});
  assert.equal(session, saved); assert.equal(session.account.id, 'acct_fixture'); assert.equal(session.refreshToken, tokens.refreshToken);
  assert.equal(f.api.cancel, undefined);
  f.api.abandoned({...entry, grant: session}); await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.calls.at(-1)?.path, '/v1/sessions/current'); assert.equal(f.calls.at(-1)?.headers.get('Authorization'), 'Bearer pat_fixture');
  assert.ok([...f.records.values()].every(value => value === null));
});

test('TV ends a consumed sign-in if its account cannot be read', async () => {
  const f = await fixture(), signal = new AbortController().signal, authorization = await f.api.create(id, signal);
  f.reply(200, {state: 'approved', tokens}); f.failAccount();
  await assert.rejects(f.api.poll({requestId: id, authorization}, signal, async () => {}), (e: ApiError) => e.code === 'invalid_grant');
  assert.equal(f.calls.at(-1)?.path, '/v1/sessions/current');
});
