import test from 'node:test';
import assert from 'node:assert/strict';
import {clearPersonalHistory} from '../src/personal-history-actions.ts';
import {readCollectionSuccessor} from '../src/query/collection-successor.ts';
import {readUserMutationClockOffset} from '../src/query/user-mutation-clock.ts';
import {refreshOfflineReadingProgress, syncOfflineReadingObservation, type OfflineReadingObservation} from '../src/reading-offline-coordination.ts';

test('history clear keeps the captured revision, intent and cancellation and sends once', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  await clearPersonalHistory({request: async <T>(...args: unknown[]) => {calls.push(args); return {} as T;}}, 'held', 23, signal);
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
test('offline reading refresh preserves exact receipt batch and rejects mismatched bindings', async () => {
  const receipt = {receiptId: 'receipt'} as OfflineReadingObservation['receipt']; const calls: unknown[][] = [];
  const batch = [{receipt, itemId: 'item', sourceId: 'source', sourceRevision: 'a'.repeat(64)}];
  const api = {request: async <T>(...args: unknown[]) => {calls.push(args); return {results: [{receiptId: 'receipt', outcome: 'current', itemId: 'item', sourceId: 'source', sourceRevision: 'a'.repeat(64), progress: null}]} as T;}};
  const result = await refreshOfflineReadingProgress(api, batch, () => true);
  assert.equal(result?.[0]?.outcome, 'current');
  assert.deepEqual(calls, [['/v1/reading/offline/progress/read', 'POST', {receipts: [receipt]}]]);
  await assert.rejects(refreshOfflineReadingProgress(api, [{...batch[0]!, itemId: 'foreign'}], () => true));
  assert.equal(await refreshOfflineReadingProgress({request: async <T>() => ({}) as T}, batch, () => false), undefined);
});
test('offline reading sends the original observation and stops on retired, malformed or unsupported outcomes', async () => {
  const input = {receipt: {receiptId: 'renewed'}, observationReceipt: {receiptId: 'original'}, expectedRevision: 7, observedAt: 1000, startedAt: 500, location: {}} as OfflineReadingObservation;
  const calls: unknown[][] = [];
  const api = {request: async <T>(...args: unknown[]) => {calls.push(args); return {progress: null, saved: false, outcome: 'history_paused'} as T;}};
  const synced = await syncOfflineReadingObservation(api, input, 'source', () => true);
  assert.equal(synced?.outcome, 'history_paused'); assert.equal(synced?.result.progress, null); assert.equal(synced?.result.saved, false);
  assert.deepEqual(calls, [['/v1/reading/offline/progress', 'POST', input]]);
  for (const answer of [{progress: null, saved: false, outcome: 'future'}, {progress: null, saved: false, outcome: 'applied'}, {progress: null, saved: false, outcome: 'history_paused', extra: true}]) {
    assert.equal(await syncOfflineReadingObservation({request: async <T>() => answer as T}, input, 'source', () => true), undefined);
  }
  assert.equal(await syncOfflineReadingObservation({request: async <T>() => ({}) as T}, input, 'source', () => false), undefined);
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
