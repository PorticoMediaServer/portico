import test from 'node:test';
import assert from 'node:assert/strict';
import {readCreditPage, readCreditPageResponse} from '../src/detail.ts';

test('credit response operation preserves original promise, escaped cursor, bounds and signal', () => {
  const pending = new Promise<never>(() => {}), calls: unknown[][] = [], signal = new AbortController().signal;
  const api = {request<T>(...args: unknown[]) {calls.push(args);return pending as Promise<T>;}};
  assert.equal(readCreditPageResponse(api, 'item /?', 'cast', {limit: 40, cursor: 'opaque/+='}, signal), pending);
  assert.deepEqual(calls, [['/v1/items/item%20%2F%3F/credits?group=cast&limit=40&cursor=opaque%2F%2B%3D', 'GET', undefined, signal]]);
});
test('existing parsed credit reader retains its default page size and DTO', async () => {
  const calls: unknown[][] = [], raw = {revision: {catalog: 2, viewer: 3}, group: 'crew', total: 0, credits: []};
  const api = {request<T>(...args: unknown[]) {calls.push(args);return Promise.resolve(raw as T);}};
  assert.deepEqual(await readCreditPage(api, 'item', 'crew'), raw);
  assert.deepEqual(calls, [['/v1/items/item/credits?group=crew&limit=37', 'GET', undefined, undefined]]);
});
