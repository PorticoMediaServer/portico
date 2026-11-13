import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {deletePorticoAccount} from '../../../packages/client-core/src/portico-services-native.ts';
import {linkNativeAbortSignals} from '../../../packages/client-core/src/native-abort-signals.ts';
import {ApiError} from '../../../packages/client-core/src/http.ts';

test('deletion requires freshly supplied proof, uses Services and signs out only the successful current opening', async () => {
  const scope = {accountId: 'account', familyId: 'family'}, origin = 'https://services.example.invalid';
  const session = {account: {id: 'account'}, familyId: 'family', accessToken: 'fixture-token'};
  const calls: unknown[] = [], signouts: unknown[] = [];
  let failure: string | undefined = 'password_wrong', active = true, release: (() => void) | undefined;
  const fetcher = (async (url: string, init: RequestInit) => {
    assert.equal(url, origin + '/v1/account/delete'); assert.equal(init.method, 'POST');
    assert.equal(new Headers(init.headers).get('Authorization'), 'Bearer fixture-token');
    assert(init.signal); calls.push(JSON.parse(String(init.body)));
    if (failure) return new Response(JSON.stringify({error: {code: failure}}), {status: failure === 'owns_server' ? 409 : 403});
    if (release) await new Promise<void>(resolve => {release = resolve;});
    return new Response(null, {status: 204});
  }) as typeof fetch;
  const {NativeAccountSecurity} = await screenModule(new URL('../src-next/platform/account-security.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {}},
    '../../../../packages/client-core/src/portico-services-native': {deletePorticoAccount},
    '../../../../packages/client-core/src/native-abort-signals': {linkNativeAbortSignals},
    '../../../../packages/client-core/src/query/hosted-account-transport': {},
    '../../../../packages/client-core/src': {ApiError},
    '../../../../packages/client-core/src/account-notifications': {},
    '../../../../packages/client-core/src/hosted-security': {},
    '../../../../packages/client-core/src/provider-security-proof': {},
    '../../../../packages/client-core/src/presentation/errors': {presentError: (e: Error) => ({body: e.message})},
    './account-work': {accountExpectation: () => scope, nativeAccountWork: async (_: unknown, work: any, guard: any) => {
      assert.deepEqual(guard.expected, scope); assert(guard.isCurrent()); const answer = await work(session);
      if (!guard.isCurrent()) throw new Error('retired'); return answer;
    }}, './account-password-reset': {openNativeAccountPasswordReset: async () => {}}, './account-authority': {hostedUrl: origin}, './server-connections': {nativeNetworkFetch: fetcher},
  });
  const owner = {accountCredentials: () => ({getSnapshot: () => ({session})}),
    signOutPorticoAccount: async (which: unknown) => {signouts.push(which);},
    accountClient: () => {throw new Error('retired deletion API');}};
  const service = new NativeAccountSecurity(owner, scope, {hasPassword: true, mfaEnabled: true}, () => active);
  const providerOnly = new NativeAccountSecurity(owner, scope, {hasPassword: false, mfaEnabled: false}, () => active);
  providerOnly.open('delete'); await providerOnly.deleteAccount('not-an-account-password');
  assert.equal(calls.length, 0); providerOnly.dispose();
  service.open('delete'); await service.submit({confirmation: 'fixture@example.invalid'});
  await service.deleteAccount('', '123456'); await service.deleteAccount('typed-now');
  assert.equal(calls.length, 0, 'neither an email confirmation nor incomplete proof sends deletion');
  for (failure of ['password_wrong', 'two_step_wrong', 'owns_server']) {
    await service.deleteAccount('typed-now', '123456'); assert(service.getSnapshot().failure);
    assert.equal(signouts.length, 0, 'a refusal never signs out');
  }
  await service.submit(); assert.equal(calls.length, 3, 'failed proof is never reused');
  failure = undefined; await service.deleteAccount('typed-again', '654321');
  assert.deepEqual(calls[3], {currentPassword: 'typed-again', twoStepCode: '654321'});
  assert.deepEqual(signouts, [scope]);
  // A late deletion answer belongs to the opening that sent it, never its successor.
  service.close(); service.open('delete'); release = () => {};
  const pending = service.deleteAccount('typed-now', '999999');
  for (let i = 0; i < 20; i++) await Promise.resolve();
  await service.deleteAccount('duplicate', '111111'); assert.equal(calls.length, 5);
  active = false; release!(); await pending; assert.equal(signouts.length, 1);
  service.dispose();
});
