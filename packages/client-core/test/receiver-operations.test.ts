import assert from 'node:assert/strict';
import test from 'node:test';
import {readReceiverItem, readTogetherPlaybackItem, readCastConfiguration, receiverLinkOperations, openTogetherStream} from '../src/query/receiver.ts';

test('receiver operations preserve paths, method defaults, normalized bodies and caller cancellation', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const api = {async request<T>(...args: unknown[]): Promise<T> {
    calls.push(args);
    return {requestId: 'request', userCode: 'ABCD', deviceName: 'TV', platform: 'tvos'} as T;
  }};
  await readReceiverItem(api, 'item/a', 'GET', signal);
  await readCastConfiguration(api);
  const links = receiverLinkOperations(api);
  assert.deepEqual(await links.preview(' abcd ', {signal}), {requestId: 'request', userCode: 'ABCD', deviceName: 'TV', platform: 'tvos'});
  await links.decide('request', ' abcd ', 'approve', {signal});
  assert.deepEqual(calls, [
    ['/v1/items/item%2Fa', 'GET', undefined, signal],
    ['/v1/cast/configuration'],
    ['/v1/quick-connect/review', 'POST', {userCode: 'ABCD'}, signal],
    ['/v1/quick-connect/decision', 'POST', {requestId: 'request', userCode: 'ABCD', decision: 'approve'}, signal],
  ]);
});

test('Together stream preserves the core path, event cursor, captured bearer and signal', async () => {
  const signal = new AbortController().signal, calls: unknown[][] = [], response = new Response();
  const api = {baseUrl: 'https://server.test', routeFetch: async (...args: Parameters<typeof fetch>) => {calls.push(args);return response;}};
  assert.equal(await openTogetherStream(api, 'captured', '/v1/groups/room/events', 'cursor', signal), response);
  assert.deepEqual(calls, [['https://server.test/v1/groups/room/events', {signal, headers: {Accept: 'text/event-stream', Authorization: 'Bearer captured', 'Last-Event-ID': 'cursor'}}]]);
  assert.throws(() => openTogetherStream(api, 'captured', 'https://other.test/v1/groups/room/events', '', signal), /invalid_group_stream/);
  assert.equal(calls.length, 1);
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
