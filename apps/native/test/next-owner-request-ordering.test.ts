import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller';

function context() {let current = true;return {subscribeAuthority: (_listener: () => void) => () => {}, authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'}), current: () => current, retire: () => {current = false;}};}
function deferred<T>() {let resolve!: (value: T) => void;let reject!: (error: unknown) => void;const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}

for (const id of ['', '.', '..', 'a/b']) test(`source configuration keeps transport rejection priority for ${JSON.stringify(id)}`, async () => {
  const scope = context(), lifetime = new NativeScreenLifetime(scope), answer = deferred<unknown>(), refusal = {code: 'original_transport'};let path = '', settled = false;
  const controller = new NativeLiveOwnerController(lifetime, {request: <T>(value: string) => {path = value;return answer.promise as Promise<T>;}}, () => 'operation');
  const pending = controller.document(id);void pending.then(() => {settled = true;}, () => {settled = true;});
  assert.equal(path, `/v1/admin/live-sources/${encodeURIComponent(id)}/configuration`);
  await Promise.resolve();assert.equal(settled, false);
  scope.retire();answer.reject(refusal);await assert.rejects(pending, error => error === refusal);
  controller.dispose();lifetime.dispose();
});

test('source configuration checks the retired lifetime before parsing malformed success', async () => {
  const scope = context(), lifetime = new NativeScreenLifetime(scope), answer = deferred<unknown>();let parsed = 0;
  const controller = new NativeLiveOwnerController(lifetime, {request: <T>() => answer.promise as Promise<T>}, () => 'operation');
  const pending = controller.document('source');scope.retire();
  answer.resolve({get serverId() {parsed++;throw new Error('parsed');}, get result() {parsed++;throw new Error('parsed');}});
  await assert.rejects(pending);assert.equal(parsed, 0);controller.dispose();lifetime.dispose();
});
