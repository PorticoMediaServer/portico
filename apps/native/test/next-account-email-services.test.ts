import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {porticoAccountEmail} from '../../../packages/client-core/src/portico-services-native.ts';
import {linkNativeAbortSignals} from '../../../packages/client-core/src/native-abort-signals.ts';
import {ApiError} from '../../../packages/client-core/src/http.ts';

test('email change sends inline proof and confirms the captured address, retaining it after a wrong code', async () => {
  const scope = {accountId: 'account', familyId: 'family'}, origin = 'https://services.example.invalid';
  const session = {account: {id: 'account'}, familyId: 'family', accessToken: 'fixture-token'};
  const calls: {path: string; body: any}[] = [];
  let wrong = true, firstRefused = true, active = true;
  const fetcher = (async (url: string, init: RequestInit) => {
    assert.equal(new Headers(init.headers).get('Authorization'), url.endsWith('/accounts/confirm') ? null : 'Bearer fixture-token');
    assert.equal(init.method, 'POST'); const body = JSON.parse(String(init.body));
    calls.push({path: url.slice(origin.length), body});
    if (url.endsWith('/account/email') && firstRefused) {
      firstRefused = false; return new Response(JSON.stringify({error: {code: 'password_wrong'}}), {status: 403});
    }
    if (url.endsWith('/accounts/confirm') && wrong) {
      wrong = false; return new Response(JSON.stringify({error: {code: 'code_wrong'}}), {status: 422});
    }
    return new Response(JSON.stringify({email: 'next@example.invalid'}), {status: 200});
  }) as typeof fetch;
  const {NativeAccountSecurity} = await screenModule(new URL('../src-next/platform/account-security.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {}},
    '../../../../packages/client-core/src/portico-services-native': {porticoAccountEmail},
    '../../../../packages/client-core/src/native-abort-signals': {linkNativeAbortSignals},
    '../../../../packages/client-core/src/query/hosted-account-transport': {},
    '../../../../packages/client-core/src/server-clock': {},
    '../../../../packages/client-core/src': {ApiError},
    '../../../../packages/client-core/src/account-notifications': {},
    '../../../../packages/client-core/src/hosted-security': {},
    '../../../../packages/client-core/src/provider-security-proof': {},
    '../../../../packages/client-core/src/presentation/errors': {presentError: (e: Error) => ({body: e.message})},
    './account-work': {accountExpectation: () => scope, nativeAccountWork: async (_: unknown, work: any, guard: any) => {
      assert(guard.isCurrent()); const answer = await work(session); assert(guard.isCurrent()); return answer;
    }}, './account-authority': {hostedUrl: origin}, './server-connections': {nativeNetworkFetch: fetcher},
  });
  const owner = {accountCredentials: () => ({getSnapshot: () => ({session})}), accountClient: () => {throw new Error('retired proof API');}};
  const service = new NativeAccountSecurity(owner, scope, {hasPassword: true}, () => active);
  service.open('email'); await service.submit({email: 'refused@example.invalid', password: 'wrong'});
  assert.equal(service.getSnapshot().step, 'form'); assert(service.getSnapshot().failure);
  const input = {email: 'Next@example.invalid', password: 'typed-now', factor: '123456'};
  const sent = service.submit(input); input.email = 'edited@example.invalid'; await sent;
  assert.equal(service.getSnapshot().step, 'email');
  await service.submit({email: 'wrong@example.invalid', code: 'abcd1234'});
  assert.equal(service.getSnapshot().step, 'email'); assert(service.getSnapshot().failure);
  await service.submit({code: 'abcd5678'}); assert.equal(service.getSnapshot().step, 'done');
  assert.deepEqual(calls, [
    {path: '/v1/account/email', body: {currentPassword: 'wrong', twoStepCode: '', newEmail: 'refused@example.invalid'}},
    {path: '/v1/account/email', body: {currentPassword: 'typed-now', twoStepCode: '123456', newEmail: 'next@example.invalid'}},
    {path: '/v1/accounts/confirm', body: {email: 'next@example.invalid', code: 'ABCD-1234'}},
    {path: '/v1/accounts/confirm', body: {email: 'next@example.invalid', code: 'ABCD-5678'}},
  ]);
  active = false; await service.submit({code: 'abcd9999'}); assert.equal(calls.length, 4);
  service.dispose();
});
