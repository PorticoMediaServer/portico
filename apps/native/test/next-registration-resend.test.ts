import test from 'node:test';
import assert from 'node:assert/strict';
import {CodedError} from '../../../packages/client-core/src/server-messages';
import * as continuationQueue from '../src-next/platform/continuation-queue';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
class APIError extends Error {readonly status: number; readonly code: string; constructor(status: number, code: string) {super(code); this.status = status; this.code = code;}}
const live = {isCurrent: () => true};

/** Real native adapter and continuation owner; only the native storage and hosted
 * client's IO are supplied. No request is made to a running service. */
async function fixture(context: import('node:test').TestContext) {
  const issued = {kind: 'registration', registrationId: 'original-registration', status: 'contact_pending', revision: '1',
    maskedAddress: 'a***@example.test', expiresAt: '2099-01-01T00:10:00Z', contactVerified: false};
  const requests: {id: string; requestId: string; signal: AbortSignal}[] = [], verified: unknown[] = [];
  let nextID: () => Promise<string> = async () => 'request-id';
  let response: () => Promise<any> = async () => ({...issued, revision: '2', expiresAt: '2099-01-01T00:20:00Z'});
  const api = {options: async () => ({providers: []}), register: async () => issued,
    resendRegistration: async (id: string, requestId: string, signal: AbortSignal) => {requests.push({id, requestId, signal}); return response();},
    verifyRegistration: async (id: string, input: unknown) => {verified.push({id, input}); return {};}};
  const bridge = {loadContinuation: async () => null, newBinding: async () => 'a'.repeat(43), saveContinuation: async () => {}, newRequestId: () => nextID(), cancel() {}};
  const nativeAccounts = {onCancel: () => () => {}, accountClient: () => api,
    authenticatePorticoAccount: async (work: (signal: AbortSignal) => Promise<unknown>, current: () => boolean) => {
      assert.equal(current(), true); const result = await work(new AbortController().signal); assert.equal(current(), true); return result;
    }};
  const {NativePorticoSignIn} = await screenModule(new URL('../src-next/platform/portico-sign-in.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoAccountAuth: bridge}},
    '../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../packages/client-core/src/server-messages': {CodedError}, '../../../../packages/client-core/src': {ApiError: APIError},
    '../../../../packages/contracts/src/hosted/routes': {}, './accounts': {nativeAccounts}, './abort-signals': {},
    './account-authority': {hostedUrl: 'https://example.test'}, './continuation-queue': continuationQueue,
  });
  const source = new NativePorticoSignIn(); context.after(() => source.dispose());
  await source.prepare(); const original = await source.register('person', 'Name', 'password', 'a@example.test');
  return {source, original, requests, verified, nextID: (work: typeof nextID) => {nextID = work;}, response: (work: typeof response) => {response = work;}};
}

test('resend keeps the exact issued registration, adopts the refreshed revision and retires old controls', async context => {
  const f = await fixture(context), fresh = await f.source.resendRegistration(f.original, live);
  assert.equal(f.requests.length, 1); assert.equal(f.requests[0].id, f.original.registrationId);
  assert.equal(fresh.revision, '2'); assert.equal(Object.isFrozen(fresh), true);
  await assert.rejects(f.source.resendRegistration(f.original, live), {code: 'cancelled'});
  await assert.rejects(f.source.verifyRegistration('123456', f.original, live), /native_registration_missing/);
  assert.equal(f.requests.length, 1);
  await f.source.verifyRegistration('654321', fresh, live);
  assert.deepEqual(f.verified, [{id: f.original.registrationId, input: {code: '654321'}}]);
});

test('resend cannot send after its caller retires while native request identity is pending', async context => {
  const f = await fixture(context), id = deferred<string>(); let current = true;
  f.nextID(() => id.promise);
  const pending = f.source.resendRegistration(f.original, {isCurrent: () => current});
  current = false; id.resolve('late-request');
  await assert.rejects(pending, {code: 'cancelled'}); assert.equal(f.requests.length, 0);
});

test('late resend response cannot replace a registration after its form retires', async context => {
  const f = await fixture(context), response = deferred<any>(), arrived = deferred<void>(); let current = true;
  f.response(() => {arrived.resolve(); return response.promise;});
  const pending = f.source.resendRegistration(f.original, {isCurrent: () => current});
  await arrived.promise; current = false; response.resolve({...f.original, revision: '2'});
  await assert.rejects(pending, {code: 'cancelled'});
  await f.source.verifyRegistration('123456', f.original, live);
  assert.equal(f.verified.length, 1, 'the original remains the adapter-owned registration');
});

test('resend rejects a different registration or address instead of adopting it', async context => {
  for (const change of [{registrationId: 'another-registration'}, {maskedAddress: 'b***@example.test'}, {status: 'completed'}]) {
    const f = await fixture(context); f.response(async () => ({...f.original, ...change}));
    await assert.rejects(f.source.resendRegistration(f.original, live), {code: 'account_changed'});
    await f.source.verifyRegistration('123456', f.original, live); assert.equal(f.verified.length, 1);
  }
});

test('disposing the original source aborts its resend and propagates request failures', async context => {
  const f = await fixture(context), response = deferred<any>(), arrived = deferred<void>();
  f.response(() => {arrived.resolve(); return response.promise;});
  const pending = f.source.resendRegistration(f.original, live); await arrived.promise;
  f.source.dispose(); assert.equal(f.requests[0].signal.aborted, true);
  response.resolve({...f.original, revision: '2'}); await assert.rejects(pending, {name: 'AbortError'});
  const next = await fixture(context), failure = new Error('mail refused'); next.response(async () => {throw failure;});
  await assert.rejects(next.source.resendRegistration(next.original, live), error => error === failure);
  assert.equal(next.requests.length, 1, 'a failure is not automatically resent');
});
