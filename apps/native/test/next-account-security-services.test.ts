import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {porticoAccountForCall, porticoApiError, readPorticoAccountSecurity} from '../../../packages/client-core/src/portico-services-native.ts';
import {parseSecurityResult} from '../../../packages/client-core/src/hosted-security.ts';
import {linkNativeAbortSignals} from '../../../packages/client-core/src/native-abort-signals.ts';
import {ApiError} from '../../../packages/client-core/src/http.ts';

test('native Password and Two-step use current Services routes and inline proof', async () => {
  const scope = {accountId: 'account', familyId: 'family'}, origin = 'https://services.example.invalid';
  const session = {account: {id: scope.accountId}, familyId: scope.familyId, accessToken: 'pat_fixture'};
  const credentials = {getSnapshot: () => ({session}), subscribe: () => () => {}};
  const codes = Array.from({length: 10}, (_, i) => 'fixture-recovery-' + i);
  const calls: {route: string; body: any; token: string | null}[] = [];
  const fetcher = (async (url: string, init: RequestInit) => {
    const route = `${init.method} ${url.slice(origin.length)}`, body = init.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({route, body, token: new Headers(init.headers).get('Authorization')});
    const replies: Record<string, unknown> = {
      'GET /v1/account': {id: scope.accountId, email: 'fixture@example.invalid', hasPassword: true, providers: [], twoStep: false},
      'GET /v1/account/two-step': {on: false, recoveryCodesLeft: 0},
      'POST /v1/account/two-step': {secret: 'fixture-secret', qrAddress: 'otpauth://totp/Fixture?secret=fixture'},
      'POST /v1/account/two-step/confirm': {recoveryCodes: codes},
      'POST /v1/account/two-step/recovery-codes': {recoveryCodes: codes},
    };
    if (route in replies) return new Response(JSON.stringify(replies[route]), {status: 200});
    if (route === 'POST /v1/account/password' || route === 'DELETE /v1/account/two-step') return new Response(null, {status: 204});
    throw new Error('unexpected route ' + route);
  }) as typeof fetch;
  const guard = async (_owner: unknown, work: (issued: typeof session) => Promise<unknown>, opening: any) => {
    assert.equal(opening.expected.accountId, scope.accountId); assert.equal(opening.expected.familyId, scope.familyId);
    assert(opening.isCurrent()); const result = await work(session); assert(opening.isCurrent()); return result;
  };
  const modules = {
    react: {default: {}},
    '../../../../packages/client-core/src/account-notifications': {},
    '../../../../packages/client-core/src/portico-servers': {},
    '../../../../packages/client-core/src/provider-security-proof': {},
    'react-native': {NativeModules: {}}, '../../../../packages/client-core/src': {ApiError},
    '../../../../packages/client-core/src/portico-services-native': {porticoAccountForCall, porticoApiError, readPorticoAccountSecurity},
    '../../../../packages/client-core/src/hosted-security': {parseSecurityResult},
    '../../../../packages/client-core/src/native-abort-signals': {linkNativeAbortSignals},
    '../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({body: error.message})},
    './account-work': {accountExpectation: () => scope, nativeAccountRead: guard, nativeAccountWork: guard},
    './account-authority': {hostedUrl: origin}, './server-connections': {nativeNetworkFetch: fetcher},
    '../../../../packages/client-core/src/query/hosted-account-transport': {createNativeAccountWorkspaceRequest: () => () => {throw new Error('legacy security request');}},
    './account-security': {NativeAccountSecurity: class {}},
  };
  const owner = {accountCredentials: () => credentials, accountClient: () => {throw new Error('legacy proof client');}};
  const {NativeAccountWorkspace} = await screenModule(new URL('../src-next/platform/account-workspace.ts', import.meta.url), modules);
  const workspace = new NativeAccountWorkspace(owner, scope, () => true);
  await workspace.open('security'); assert.equal(workspace.getSnapshot().phase, 'ready');
  assert.equal(workspace.getSnapshot().security.hasPassword, true); assert.equal(workspace.getSnapshot().security.mfaEnabled, false);
  const {NativeAccountSecurity} = await screenModule(new URL('../src-next/platform/account-security.ts', import.meta.url), modules);
  const security = new NativeAccountSecurity(owner, scope, workspace.getSnapshot().security, () => true);
  const input = {password: 'fixture-password', factor: '123456', newPassword: 'fixture-replacement'};
  security.open('password'); await security.submit(input); assert.equal(security.getSnapshot().step, 'done');
  security.open('mfa-on'); await security.submit(input); assert.equal(security.getSnapshot().step, 'mfa');
  assert.equal(security.getSnapshot().result.secret, 'fixture-secret');
  await security.submit({code: '654321'}); assert.equal(security.getSnapshot().step, 'codes');
  assert.deepEqual(security.getSnapshot().result.recoveryCodes, codes);
  security.close(); assert.equal(security.getSnapshot().result, undefined);
  security.open('mfa-off'); await security.submit(input); assert.equal(security.getSnapshot().step, 'done');
  security.open('codes'); await security.submit(input); assert.equal(security.getSnapshot().step, 'codes');
  assert.deepEqual(calls.map(call => call.route), ['GET /v1/account', 'GET /v1/account/two-step', 'POST /v1/account/password',
    'POST /v1/account/two-step', 'POST /v1/account/two-step/confirm', 'DELETE /v1/account/two-step', 'POST /v1/account/two-step/recovery-codes']);
  assert.deepEqual(calls[2].body, {currentPassword: input.password, twoStepCode: input.factor, newPassword: input.newPassword});
  for (const index of [3, 5, 6]) assert.deepEqual(calls[index].body, {currentPassword: input.password, twoStepCode: input.factor});
  assert.deepEqual(calls[4].body, {code: '654321'}); assert.ok(calls.every(call => call.token === 'Bearer pat_fixture'));
  security.dispose(); workspace.dispose();
});
