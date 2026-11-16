import {withGuardedRaw} from '../../../packages/client-core/src/home-selection/list-destinations.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller';

function context() {let current = true;return {subscribeAuthority: (_listener: () => void) => () => {}, authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'}), current: () => current, retire: () => {current = false;}};}
function deferred<T>() {let resolve!: (value: T) => void;let reject!: (error: unknown) => void;const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}

for (const id of ['.', '..', 'a/b']) test(`source configuration keeps transport rejection priority for ${JSON.stringify(id)}`, async () => {
  const scope = context(), lifetime = new NativeScreenLifetime(scope), answer = deferred<unknown>(), refusal = {code: 'original_transport'};let path = '', settled = false;
  const api = {request: async <T>(): Promise<T> => {throw new Error('unexpected read');}, requestRaw: (value: string) => {path = value;return answer.promise as any;}};
  const controller = new NativeLiveOwnerController(lifetime, withGuardedRaw(api, api, () => lifetime.assert()), () => 'operation');
  const pending = controller.sourceSettings(id);void pending.then(() => {settled = true;}, () => {settled = true;});
  assert.equal(path, `/v1/admin/live/sources/${encodeURIComponent(id)}`);
  await Promise.resolve();assert.equal(settled, false);
  scope.retire();answer.reject(refusal);await assert.rejects(pending, error => error === refusal);
  controller.dispose();lifetime.dispose();
});

test('source configuration checks the retired lifetime before parsing malformed success', async () => {
  const scope = context(), lifetime = new NativeScreenLifetime(scope), answer = deferred<unknown>();let parsed = 0;
  const api = {request: async <T>(): Promise<T> => {throw new Error('unexpected read');}, requestRaw: () => answer.promise as any};
  const controller = new NativeLiveOwnerController(lifetime, withGuardedRaw(api, api, () => lifetime.assert()), () => 'operation');
  const pending = controller.sourceSettings('source');scope.retire();
  answer.resolve({status: 200, headers: {etag: '"1"'}, get body() {parsed++;throw new Error('parsed');}});
  await assert.rejects(pending);assert.equal(parsed, 0);controller.dispose();lifetime.dispose();
});

test('a source settings read with no id dispatches no request', async () => {
  const scope=context(),lifetime=new NativeScreenLifetime(scope);let requests=0;
  const controller=new NativeLiveOwnerController(lifetime,{request:async<T>():Promise<T>=>{requests++;throw new Error('unexpected');},requestRaw:async()=>{requests++;throw new Error('unexpected');}},()=> 'operation');
  await assert.rejects(controller.sourceSettings(''));assert.equal(requests,0);controller.dispose();lifetime.dispose();
});
