import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as continuationQueue from '../src-next/platform/continuation-queue.ts';
import {PorticoServicesAccountClient} from '../../../packages/client-core/src/portico-services-native.ts';
import {ApiError} from '../../../packages/client-core/src/http.ts';
import {routeBase64, webRouteCrypto} from '../../../packages/client-core/src/route-identity.ts';
import {CodedError} from '../../../packages/client-core/src/server-messages.ts';
import {MFARequiredError, parseAccountReturn, parseNativeAccountContinuation} from '../../../packages/client-core/src/hosted-security.ts';
import {linkNativeAbortSignals} from '../../../packages/client-core/src/native-abort-signals.ts';

const origin = 'https://accounts.example.test', secret = 's'.repeat(43), code = 'c'.repeat(43);
const tokens = {accessToken: 'pat_fixture', refreshToken: 'prt_fixture', expiresAt: new Date(Date.now() + 3600000).toISOString()};
type Call = {path: string; body: any; headers: Headers; credentials?: RequestCredentials};
async function fixture(nativeApple = true, completion: (body: any) => [number, unknown] = () => [200, {tokens}], provider: 'apple' | 'google' = 'apple') {
  const authorizeUrl = provider === 'apple' ? 'https://appleid.apple.com/auth/authorize' : 'https://accounts.google.com/o/oauth2/v2/auth';
  const calls: Call[] = [], saves: string[] = []; let sheets = 0, browsers = 0, adopted = 0, live = true, bindings = 0;
  let browserResult = `portico://account-return?transactionId=sso_fixture&code=${code}`;
  let duringSheet = () => {}, duringBrowser = () => {}, accountUnavailable = false;
  const fetcher = (async (url: string, init: RequestInit) => {
    assert.ok(url.startsWith(origin + '/v1/'), 'only the injected stand-in receives requests');
    const path = new URL(url).pathname, body = init.body ? JSON.parse(String(init.body)) : undefined;
    const headers = new Headers(init.headers); calls.push({path, body, headers, credentials: init.credentials});
    let status = 200, answer: unknown;
    if (path === '/v1/sessions/provider') answer = body.kind === 'app'
      ? {id: 'sso_fixture', nonce: 'nonce-from-services', expiresAt: new Date(Date.now() + 600000).toISOString()}
      : {id: 'sso_fixture', authorizeUrl, expiresAt: new Date(Date.now() + 600000).toISOString()};
    else if (path === '/v1/sessions/apple' || path === '/v1/sessions/provider/complete') [status, answer] = completion(body);
    else if (path === '/v1/sessions/two-step') [status, answer] = body.code === '123456' ? [200, {tokens}] : [403, {error: {code: 'two_step_wrong'}}];
    else if (path === '/v1/account') [status, answer] = accountUnavailable ? [503, {error: {code: 'busy'}}] : [200, {id: 'acct_fixture', username: 'ada', displayName: 'Ada'}];
    else if (path === '/v1/sessions/current') {status = 204; answer = undefined;}
    else assert.fail('unexpected Services route ' + path);
    return new Response(JSON.stringify(answer), {status});
  }) as typeof fetch;
  const client = new PorticoServicesAccountClient(origin, {name: 'Portico test phone', kind: 'phone'}, fetcher, () => 'family_fixture');
  // Capabilities have their own wire test (PR1); this fixture supplies the offered-method view model.
  client.options = async () => ({password: true, registration: true, recovery: true, accountSecurity: false, verifiedContactRequired: true,
    providers: [{id: provider, enabled: true, native: false, nativeClientIds: []}]});
  const bridge = {canSignInApple: nativeApple, canOpenBrowser: true,
    loadContinuation: async () => null, saveContinuation: async (_origin: string, value: string) => {saves.push(value);},
    newBinding: async () => ++bindings === 1 ? 'b'.repeat(43) : secret, cancel() {},
    signInApple: async (nonce: string) => {sheets++; assert.equal(nonce, 'nonce-from-services'); duringSheet(); return {idToken: 'apple-identity-token', displayName: 'Ada Lovelace'};},
    openBrowser: async (url: string, expected: string) => {browsers++; assert.equal(url, authorizeUrl); assert.equal(expected, 'sso_fixture'); duringBrowser(); return browserResult;},
  };
  const nativeAccounts = {onCancel: () => () => {}, porticoClient: () => client, cancel() {},
    authenticatePorticoAccount: async (work: (signal: AbortSignal) => Promise<unknown>, current: () => boolean) => {
      assert.ok(current()); const result = await work(new AbortController().signal); assert.ok(current()); adopted++; return result;
    }};
  const {NativePorticoSignIn} = await screenModule(new URL('../src-next/platform/portico-sign-in.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoAccountAuth: bridge,
      PorticoNetwork: {sha256: async (input: string) => routeBase64(await webRouteCrypto.sha256(Uint8Array.from(atob(input.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0))))}}},
    '../../../../packages/client-core/src/hosted-security': {MFARequiredError, parseAccountReturn, parseNativeAccountContinuation},
    '../../../../packages/client-core/src/server-messages': {CodedError}, '../../../../packages/client-core/src': {ApiError, routeBase64},
    './accounts': {nativeAccounts}, './abort-signals': {linkNativeAbortSignals}, './account-authority': {hostedUrl: origin}, './continuation-queue': continuationQueue,
  });
  const source = new NativePorticoSignIn(() => live); await source.prepare();
  return {source, calls, saves, sheets: () => sheets, browsers: () => browsers, adopted: () => adopted,
    failAccount: () => {accountUnavailable = true;},
    setBrowser: (value: string) => {browserResult = value;}, onSheet: (fn: () => void) => {duringSheet = fn;}, onBrowser: (fn: () => void) => {duringBrowser = fn;}, retire: () => {live = false;}};
}

test('the Gallery entry points sign in with native Apple through rebuilt Services and adopt once', async () => {
  const f = await fixture();
  const tx = await f.source.provider('apple', () => true);
  assert.equal(tx.status, 'verified'); assert.equal(tx.needsUsername, false);
  const session = await f.source.finishProvider();
  assert.equal(session.account.id, 'acct_fixture'); assert.equal(session.refreshToken, 'prt_fixture');
  assert.equal(f.sheets(), 1); assert.equal(f.browsers(), 0); assert.equal(f.adopted(), 1);
  assert.deepEqual(f.calls.map(c => c.path), ['/v1/sessions/provider', '/v1/sessions/apple', '/v1/account']);
  assert.deepEqual(f.calls[0].body, {provider: 'apple', kind: 'app'});
  assert.deepEqual(f.calls[1].body, {id: 'sso_fixture', identityToken: 'apple-identity-token', device: {name: 'Portico test phone', kind: 'phone'}, displayName: 'Ada Lovelace'});
  assert.match(f.calls[1].headers.get('Idempotency-Key')!, /^[a-f0-9-]{36}$/);
  assert.equal(f.calls[1].headers.has('Portico-Refresh'), false); assert.equal(f.calls[1].credentials, 'omit');
  assert.ok(f.saves.every(value => !value.includes('apple-identity-token') && !value.includes('pat_fixture') && !value.includes('prt_fixture')));
  f.source.dispose();
});

test('first-use Apple asks for a username, retains the opening on a taken name and completes without another sheet', async () => {
  const f = await fixture(true, body => !body.username ? [422, {error: {code: 'username_needed'}}]
    : body.username === 'taken' ? [409, {error: {code: 'username_taken'}}] : [200, {tokens}]);
  const tx = await f.source.provider('apple');
  assert.equal(tx.needsUsername, true); assert.equal(tx.contactVerified, true); assert.equal(tx.suggestedName, 'Ada Lovelace');
  assert.equal(f.adopted(), 0);
  await assert.rejects(f.source.finishProvider('taken', 'Ada'), {code: 'username_unavailable'});
  await f.source.finishProvider('ada', 'Ada');
  assert.equal(f.adopted(), 1); assert.equal(f.sheets(), 1);
  const completions = f.calls.filter(c => c.path === '/v1/sessions/apple');
  assert.deepEqual(completions.map(c => c.body.username), [undefined, 'taken', 'ada']);
  assert.equal(new Set(completions.map(c => c.headers.get('Idempotency-Key'))).size, 3);
  f.source.dispose();
});

test('Apple two-step stays in the existing provider view model and a wrong code can be corrected', async () => {
  const f = await fixture(true, () => [200, {twoStep: {challengeId: 'tsc_fixture'}}]);
  const tx = await f.source.provider('apple');
  assert.equal(tx.mfaRequired, true); assert.equal(f.adopted(), 0);
  await assert.rejects(f.source.finishProvider(undefined, undefined, '000000'), {code: 'two_step_wrong'});
  await f.source.finishProvider(undefined, undefined, '123456');
  assert.equal(f.adopted(), 1); assert.equal(f.sheets(), 1);
  assert.deepEqual(f.calls.filter(c => c.path.endsWith('/two-step')).map(c => c.body),
    [{challengeId: 'tsc_fixture', code: '000000'}, {challengeId: 'tsc_fixture', code: '123456'}]);
  f.source.dispose();
});

test('Apple browser fallback binds the return to its flow and spends the code with the private secret', async () => {
  const f = await fixture(false);
  await f.source.provider('apple'); await f.source.finishProvider();
  assert.equal(f.browsers(), 1); assert.equal(f.sheets(), 0); assert.equal(f.adopted(), 1);
  assert.equal(f.calls[0].body.secretHash, routeBase64(await webRouteCrypto.sha256(new TextEncoder().encode(secret))));
  assert.deepEqual(f.calls[1].body, {id: 'sso_fixture', code, secret, device: {name: 'Portico test phone', kind: 'phone'}});
  assert.equal(f.calls[1].path, '/v1/sessions/provider/complete');
  assert.ok(f.saves.every(value => !value.includes(secret)));
  assert.ok(f.saves.every(value => !value.includes('completionCode') && !value.includes('identityToken')));
  f.source.dispose();
});

test('a wrong or incomplete provider return and a retired Apple sheet never complete or adopt', async () => {
  for (const returned of [`portico://account-return?transactionId=other&code=${code}`, 'portico://account-return?transactionId=sso_fixture']) {
    const f = await fixture(false); f.setBrowser(returned);
    await assert.rejects(f.source.provider('apple')); assert.equal(f.calls.length, 1); assert.equal(f.adopted(), 0); f.source.dispose();
  }
  const f = await fixture(); f.onSheet(() => f.source.dispose());
  await assert.rejects(f.source.provider('apple')); assert.equal(f.calls.length, 1); assert.equal(f.adopted(), 0);
});

test('an account conflict is not merged by email and an unoffered provider never starts', async () => {
  const f = await fixture(true, () => [409, {error: {code: 'account_exists'}}]);
  await assert.rejects(f.source.provider('apple'), {code: 'account_exists'}); assert.equal(f.adopted(), 0);
  const before = f.calls.length; await assert.rejects(f.source.provider('google'), {code: 'unavailable'});
  assert.equal(f.calls.length, before); f.source.dispose();
});

test('a provider grant whose account cannot be read is ended instead of left without an owner', async () => {
  const f = await fixture(); f.failAccount();
  await assert.rejects(f.source.provider('apple'), {code: 'busy'});
  assert.equal(f.adopted(), 0);
  const ended = f.calls.find(call => call.path === '/v1/sessions/current');
  assert.equal(ended?.headers.get('Authorization'), 'Bearer pat_fixture');
  f.source.dispose();
});


test('Google uses the browser even on an Apple-capable device and completes through the Gallery entry points', async () => {
  const f = await fixture(true, () => [200, {tokens}], 'google');
  const tx = await f.source.provider('google'); await f.source.finishProvider();
  assert.equal(tx.provider, 'google'); assert.equal(tx.status, 'verified');
  assert.equal(f.sheets(), 0); assert.equal(f.browsers(), 1); assert.equal(f.adopted(), 1);
  assert.equal(f.calls[0].body.provider, 'google'); assert.equal(f.calls[0].body.kind, undefined);
  assert.equal(f.calls[0].body.secretHash, routeBase64(await webRouteCrypto.sha256(new TextEncoder().encode(secret))));
  assert.equal(f.calls[1].path, '/v1/sessions/provider/complete');
  assert.equal(f.calls[1].body.code, code); assert.equal(f.calls[1].body.secret, secret);
  assert.ok(f.calls[1].headers.get('Idempotency-Key')); f.source.dispose();
});

test('Google first-use username and two-step replies use the existing forms', async () => {
  for (const step of ['username', 'two-step'] as const) {
    const f = await fixture(true, body => step === 'username'
      ? body.username ? [200, {tokens}] : [422, {error: {code: 'username_needed'}}]
      : [200, {twoStep: {challengeId: 'tsc_fixture'}}], 'google');
    const tx = await f.source.provider('google'); assert.equal(f.adopted(), 0);
    if (step === 'username') {assert.equal(tx.needsUsername, true); await f.source.finishProvider('ada', 'Ada');}
    else {assert.equal(tx.mfaRequired, true); await f.source.finishProvider(undefined, undefined, '123456');}
    assert.equal(f.adopted(), 1); assert.equal(f.browsers(), 1); f.source.dispose();
  }
});

test('Google rejects an unrelated return or an opening retired while its browser was out', async () => {
  for (const retired of [false, true]) {
    const f = await fixture(false, () => [200, {tokens}], 'google');
    if (retired) f.onBrowser(f.retire);
    else f.setBrowser(`portico://account-return?transactionId=other&code=${code}`);
    await assert.rejects(f.source.provider('google'));
    assert.equal(f.calls.length, 1); assert.equal(f.adopted(), 0); f.source.dispose();
  }
});
