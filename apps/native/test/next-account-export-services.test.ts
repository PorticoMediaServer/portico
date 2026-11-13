import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {porticoAccountForCall, porticoApiError} from '../../../packages/client-core/src/portico-services-native.ts';
import {linkNativeAbortSignals} from '../../../packages/client-core/src/native-abort-signals.ts';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {ApiError} from '../../../packages/client-core/src/http.ts';

test('account export needs fresh proof, keeps its direct file on this opening, and never replays a lost answer', async () => {
  const scope = {accountId: 'account', familyId: 'family'}, origin = 'https://services.example.invalid';
  const session = {account: {id: scope.accountId}, familyId: scope.familyId, accessToken: 'fixture-token'};
  const document = {format: 'portico.account-export.v2', account: {id: scope.accountId, displayName: 'Own name'}, sessions: []};
  const calls: any[] = [];
  let reply = async () => new Response(JSON.stringify({error: {code: 'password_wrong'}}), {status: 403});
  const fetcher = (async (url: string, init: RequestInit) => {
    assert.equal(url, origin + '/v1/account/export'); assert.equal(init.method, 'POST');
    assert.equal(new Headers(init.headers).get('Authorization'), 'Bearer fixture-token');
    assert.equal(init.cache, 'no-store'); assert.equal(init.credentials, 'omit'); assert.ok(init.signal);
    calls.push(JSON.parse(String(init.body))); return reply();
  }) as typeof fetch;
  const {NativeAccountSecurity} = await screenModule(new URL('../src-next/platform/account-security.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {}},
    '../../../../packages/client-core/src/portico-services-native': {porticoAccountForCall, porticoApiError},
    '../../../../packages/client-core/src/native-abort-signals': {linkNativeAbortSignals},
    '../../../../packages/client-core/src': {ApiError},
    '../../../../packages/client-core/src/account-notifications': {},
    '../../../../packages/client-core/src/hosted-security': {},
    '../../../../packages/client-core/src/provider-security-proof': {},
    '../../../../packages/client-core/src/presentation/errors': {presentError},
    './account-work': {accountExpectation: () => scope, nativeAccountWork: async (_: unknown, work: any, guard: any) => {
      assert(guard.isCurrent()); return work(session);
    }}, './account-authority': {hostedUrl: origin}, './server-connections': {nativeNetworkFetch: fetcher},
  });
  const owner = {accountCredentials: () => ({getSnapshot: () => ({session})}), accountClient: () => {throw new Error('retired proof/job API');}};
  const security = {hasPassword: true, mfaEnabled: true};
  const service = new NativeAccountSecurity(owner, scope, security, () => true);
  service.open('export'); assert.equal(calls.length, 0);
  await service.exportAccount(''); await service.exportAccount('typed-now'); assert.equal(calls.length, 0);
  await service.exportAccount('wrong', '111111');
  assert.equal(service.getSnapshot().step, 'form'); assert.ok(service.getSnapshot().failure);
  reply = async () => new Response(JSON.stringify({error: {code: 'slow_down'}}), {status: 429, headers: {'Retry-After': '3600'}});
  await service.exportAccount('typed-now', '222222'); assert.match(service.getSnapshot().failure, /3,600/);
  reply = async () => {throw new TypeError('connection lost after sending');};
  await service.exportAccount('typed-now', '333333');
  assert.equal(calls.length, 3, 'a failed download was sent once, never replayed');
  assert.equal(service.getSnapshot().locked, false, 'a new code can be typed after a lost answer');
  reply = async () => new Response(JSON.stringify(document));
  await service.exportAccount('typed-now', '444444');
  const ready = service.getSnapshot();
  assert.equal(ready.step, 'export'); assert.equal(ready.busy, false); assert.equal(ready.exportJob, undefined);
  assert.equal(ready.exportFile.filename, 'portico-account.json'); assert.equal(ready.exportFile.contentType, 'application/json');
  assert.deepEqual(JSON.parse(ready.exportFile.contents), document);
  assert.deepEqual(calls, [
    {currentPassword: 'wrong', twoStepCode: '111111'}, {currentPassword: 'typed-now', twoStepCode: '222222'},
    {currentPassword: 'typed-now', twoStepCode: '333333'}, {currentPassword: 'typed-now', twoStepCode: '444444'},
  ]);
  await service.exportAccount('typed-now', '555555'); assert.equal(calls.length, 4, 'ready does not request another export');
  service.close(); assert.equal(service.getSnapshot().exportFile, undefined);
  reply = async () => new Response(JSON.stringify({...document, account: {id: 'another-account'}}));
  service.open('export'); await service.exportAccount('typed-now', '555555');
  assert.equal(service.getSnapshot().exportFile, undefined); assert.ok(service.getSnapshot().failure);

  let finish!: (answer: Response) => void;
  const pending = new Promise<Response>(resolve => {finish = resolve;});
  reply = () => pending;
  const old = service.exportAccount('typed-now', '666666');
  for (let i = 0; i < 10; i++) await Promise.resolve();
  assert.equal(service.getSnapshot().busy, true);
  service.close(); service.open('password'); finish(new Response(JSON.stringify(document))); await old;
  assert.equal(service.getSnapshot().kind, 'password'); assert.equal(service.getSnapshot().exportFile, undefined);
  service.dispose();
  const providerOnly = new NativeAccountSecurity(owner, scope, {...security, hasPassword: false}, () => true);
  const before = calls.length; providerOnly.open('export'); await providerOnly.exportAccount('typed-now', '777777');
  assert.equal(calls.length, before, 'a provider sign-in is not the password required for export');
  providerOnly.dispose();
});
