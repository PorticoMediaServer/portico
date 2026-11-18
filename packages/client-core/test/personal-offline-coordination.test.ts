import test from 'node:test';
import assert from 'node:assert/strict';
import {clearPersonalHistory} from '../src/personal-history-actions.ts';
import {readCollectionSuccessor} from '../src/query/collection-successor.ts';
import type {DownloadReceipt} from '../src/downloads.ts';
import type {ReadingLocation} from '../src/reading.ts';
import {refreshOfflineReadingProgressResponse, syncOfflineReadingObservationResponse} from '../src/reading-offline-coordination.ts';

test('history clear uses the rebuilt route once with captured cancellation', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const held = new Promise<void>(() => {});
  assert.strictEqual(clearPersonalHistory({request: <T>(...args: unknown[]) => {calls.push(args); return held as Promise<T>;}}, 'held', 23, signal), held);
  assert.deepEqual(calls, [['/v1/history', 'DELETE', undefined, signal]]);
});
test('collection successor follows the collection page\'s order and cursor, skipping titles that cannot play', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const next = {id: 'next', available: true, title: 'Next'};
  const api = {request: async <T>(...args: unknown[]) => {calls.push(args); return (calls.length === 1
    ? {items: [{id: 'current', available: true}], nextCursor: 'opaque/+='}
    : {items: [{id: 'unavailable', available: false}, next], nextCursor: ''}) as T;}};
  assert.deepEqual(await readCollectionSuccessor(api, 'collection/one', 'current', signal), next);
  assert.deepEqual(calls, [
    ['/v1/items?collection=collection%2Fone&limit=60', 'GET', undefined, signal],
    ['/v1/items?collection=collection%2Fone&limit=60&cursor=opaque%2F%2B%3D', 'GET', undefined, signal],
  ]);
});
test('collection walk keeps its ten-page budget and cancellation never starts another page', async () => {
  let count = 0; const controller = new AbortController();
  const api = {request: async <T>() => {count++; return {items: [], nextCursor: 'next'} as T;}};
  assert.equal(await readCollectionSuccessor(api, 'collection', 'absent', controller.signal), undefined); assert.equal(count, 10);
  controller.abort(); await assert.rejects(readCollectionSuccessor(api, 'collection', 'absent', controller.signal), {name: 'AbortError'}); assert.equal(count, 10);
});
test('offline reading operations preserve payload and return the original transport promise', async () => {
  const calls: unknown[][] = [], held = new Promise<unknown>(() => {});
  const api = {request: <T>(...args: unknown[]) => {calls.push(args); return held as Promise<T>;}};
  const receipt = {receiptId: 'renewed'} as DownloadReceipt, observationReceipt = {receiptId: 'original'} as DownloadReceipt;
  const location = {} as ReadingLocation;
  assert.strictEqual(refreshOfflineReadingProgressResponse(api, [receipt]), held);
  assert.strictEqual(syncOfflineReadingObservationResponse(api, receipt, observationReceipt, 7, 1000, 500, location), held);
  assert.deepEqual(calls, [
    ['/v1/reading/offline/progress/read', 'POST', {receipts: [receipt]}],
    ['/v1/reading/offline/progress', 'POST', {receipt, observationReceipt, expectedRevision: 7, observedAt: 1000, startedAt: 500, location}],
  ]);
});
