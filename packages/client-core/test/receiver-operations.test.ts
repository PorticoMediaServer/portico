import assert from 'node:assert/strict';
import test from 'node:test';
import {readReceiverItem, readTogetherPlaybackItem, readCastConfiguration, receiverLinkOperations} from '../src/query/receiver.ts';

test('receiver operations preserve paths, method defaults, normalized bodies and caller cancellation', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const api = {async request<T>(...args: unknown[]): Promise<T> {
    calls.push(args);
    return {device: {name: 'TV', platform: 'tvos', appVersion: '1'}, expiresAt: '2099-01-01T00:00:00Z'} as T;
  }};
  await readReceiverItem(api, 'item/a', 'GET', signal);
  await readCastConfiguration(api);
  const links = receiverLinkOperations(api);
  assert.deepEqual(await links.preview(' 2345abcd ', {signal}), {requestId: '2345-ABCD', userCode: '2345-ABCD', deviceName: 'TV', platform: 'tvos'});
  await links.decide('request', ' 2345abcd ', 'approve', {signal});
  assert.deepEqual(calls, [
    ['/v1/items/item%2Fa', 'GET', undefined, signal],
    ['/v1/cast/config'],
    ['/v1/auth/codes/2345-ABCD', 'GET', undefined, signal],
    ['/v1/auth/codes/2345-ABCD/approve', 'POST', {deny: false}, signal],
  ]);
});

test('native Together raw reads retain pre-transport route validation and the original promise', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  let resolve!: (value: unknown) => void, parsed = false;
  const response = new Promise<unknown>(done => {resolve = done;});
  const api = {request<T>(...args: unknown[]) {calls.push(args);return response as Promise<T>;}};
  for (const id of ['', '.', '..']) assert.throws(() => readTogetherPlaybackItem(api, id, signal), /route parameter/);
  assert.equal(calls.length, 0);
  const read = readTogetherPlaybackItem(api, 'item/a', signal);
  assert.equal(read, response);
  assert.deepEqual(calls, [['/v1/items/item%2Fa', 'GET', undefined, signal]]);
  const raw = {get id() {parsed = true;return 'item/a';}};
  resolve(raw);assert.equal(await read, raw);assert.equal(parsed, false);
});
