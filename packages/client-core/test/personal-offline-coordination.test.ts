import test from 'node:test';
import assert from 'node:assert/strict';
import {clearPersonalHistory} from '../src/personal-history-actions.ts';
import {readCollectionSuccessor} from '../src/query/collection-successor.ts';
import {readUserMutationClockOffset} from '../src/query/user-mutation-clock.ts';
import type {DownloadReceipt} from '../src/downloads.ts';
import type {ReadingLocation} from '../src/reading.ts';
import {refreshOfflineReadingProgressResponse, syncOfflineReadingObservationResponse} from '../src/reading-offline-coordination.ts';

test('history clear keeps the captured revision, intent and cancellation and sends once', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const held = new Promise<void>(() => {});
  assert.strictEqual(clearPersonalHistory({request: <T>(...args: unknown[]) => {calls.push(args); return held as Promise<T>;}}, 'held', 23, signal), held);
  assert.deepEqual(calls, [['/v1/personal-history/actions', 'POST', {operationId: 'held', expectedRevision: 23, action: 'clear-history'}, signal]]);
});
test('collection successor follows server order and its opaque cursor across unavailable members', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const next = {id: 'next', available: true, missing: false, title: 'Next'};
  const api = {request: async <T>(...args: unknown[]) => {calls.push(args); return (calls.length === 1
    ? {items: [{id: 'current', available: true}], nextCursor: 'opaque/+='}
    : {items: [{id: 'unavailable', available: false}, {id: 'missing', available: true, missing: true}, next], nextCursor: ''}) as T;}};
  assert.deepEqual(await readCollectionSuccessor(api, 'collection/one', 'current', signal), next);
  assert.deepEqual(calls, [
    ['/v1/collections/collection%2Fone/items?sort=groupOrder&direction=asc&limit=60', 'GET', undefined, signal],
    ['/v1/collections/collection%2Fone/items?sort=groupOrder&direction=asc&limit=60&cursor=opaque%2F%2B%3D', 'GET', undefined, signal],
  ]);
});
test('collection walk keeps its ten-page budget and cancellation never starts another page', async () => {
  let count = 0; const controller = new AbortController();
  const api = {request: async <T>() => {count++; return {items: [], nextCursor: 'next'} as T;}};
  assert.equal(await readCollectionSuccessor(api, 'collection', 'absent', controller.signal), undefined); assert.equal(count, 10);
  controller.abort(); await assert.rejects(readCollectionSuccessor(api, 'collection', 'absent', controller.signal), {name: 'AbortError'}); assert.equal(count, 10);
});
test('native mutation clock samples once through its captured transport and rejects noninteger time', async () => {
  let count = 0; const serverTimeMs = Date.now() + 5000, before = Date.now();
  const offset = await readUserMutationClockOffset({request: async <T>(path: string) => {count++; assert.equal(path, '/v1/user-mutations/policy'); return {serverTimeMs} as T;}});
  assert.equal(count, 1); assert.ok(offset <= serverTimeMs - before && offset >= serverTimeMs - Date.now());
  await assert.rejects(readUserMutationClockOffset({request: async <T>() => ({serverTimeMs: 1.5}) as T}), /mutation_clock/);
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

test('saved reading-group removal preserves revision, stable command and post-response authority', async () => {
  const {removeSavedReadingGroup} = await import('../src/saved-reading-reference.ts');
  const signal = new AbortController().signal, command = {operationId: 'held', expectedRevision: 2}, calls: unknown[][] = [];
  const api = {request: async <T>(...args: unknown[]) => {calls.push(args); return {serverId: 'server', referenceId: 'reference', operationId: 'held', removed: true, revision: 3} as T;}};
  const result = await removeSavedReadingGroup(api, 'server', 'reference', command, signal, () => {});
  assert.equal(result.revision, 3);
  assert.deepEqual(calls, [['/v1/reading/saved-groups/reference', 'DELETE', command, signal]]);
  const cancelled = new Error('retired');
  await assert.rejects(removeSavedReadingGroup(api, 'server', 'reference', command, signal, () => {throw cancelled;}), error => error === cancelled);
});
