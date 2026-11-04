import test from 'node:test';
import assert from 'node:assert/strict';
import {prepareItemDownload, readDownloadPreparation, readDownloadOptions, readDownloadReceiptKeys, issueDownloadReceipts,
  revalidateDownloadReceipts, revokeRemovedDownloadReceipt, readDownloadRevocations, syncOfflineDownloadPlays, syncDesktopDownloadPlays} from '../src/download-coordination.ts';

function fixture(answer: unknown) {
  const calls: unknown[][] = [];
  return {calls, api: {request: async <T>(...args: unknown[]): Promise<T> => {calls.push(args); return answer as T;}}};
}
test('download admission preserves the intent, version, signal and one-request budget on retry', async () => {
  const {api, calls} = fixture({batchId: 'batch', accepted: 0, items: [], rejected: [], duplicate: false});
  const signal = new AbortController().signal;
  const intent = {operationId: 'held', mediaId: 'book', versionId: 'publication', quality: 'original', localOnly: 'ignored'};
  await prepareItemDownload(api, intent, signal); await prepareItemDownload(api, intent, signal);
  const expected = ['/v1/downloads/preparations', 'POST', {operationId: 'held', mediaId: 'book', versionId: 'publication', quality: 'original'}, signal];
  assert.deepEqual(calls, [expected, expected]);
});
test('a native transport answer settles its attempt before DTO failure; transport failure leaves the attempt held', async () => {
  const {api} = fixture({}); let received = 0;
  const intent = {operationId: 'held', mediaId: 'book', quality: 'original'};
  await assert.rejects(prepareItemDownload(api, intent, undefined, () => received++));
  assert.equal(received, 1);
  const failure = new Error('offline');
  await assert.rejects(prepareItemDownload({request: async () => {throw failure;}}, intent, undefined, () => received++), error => error === failure);
  assert.equal(received, 1);
});
test('receipt coordination preserves batches, stable IDs, scoped transport and cancellation without extra reads', async () => {
  const {api, calls} = fixture({results: []}); const signal = new AbortController().signal;
  await issueDownloadReceipts(api, 'issue', ['a', 'b'], signal);
  await revalidateDownloadReceipts(api, 'renew', ['r1', 'r2'], signal);
  await revokeRemovedDownloadReceipt(api, 'remove', 'r1');
  assert.deepEqual(calls, [
    ['/v1/downloads/receipts', 'POST', {operationId: 'issue', preparationIds: ['a', 'b']}, signal],
    ['/v1/downloads/receipts/revalidate', 'POST', {operationId: 'renew', receiptIds: ['r1', 'r2']}, signal],
    ['/v1/downloads/receipts/revoke', 'POST', {operationId: 'remove', receiptIds: ['r1'], reason: 'removed'}],
  ]);
});
test('discovery keeps opaque cursor encoding and response validation', async () => {
  const signal = new AbortController().signal;
  const keys = fixture({keys: []}); assert.deepEqual(await readDownloadReceiptKeys(keys.api, signal), []);
  assert.deepEqual(keys.calls, [['/v1/downloads/receipt-keys', 'GET', undefined, signal]]);
  const revocations = fixture({items: [], nextCursor: '', asOf: '2026-11-04T00:00:00Z'});
  await readDownloadRevocations(revocations.api, 'opaque/+=', signal);
  assert.deepEqual(revocations.calls, [['/v1/downloads/receipts/revocations?cursor=opaque%2F%2B%3D', 'GET', undefined, signal]]);
  const invalid = fixture({});
  await assert.rejects(readDownloadPreparation(invalid.api, 'prep/one', signal));
  await assert.rejects(readDownloadOptions(invalid.api, 'book/one', signal, 'version/one'));
  assert.deepEqual(invalid.calls.map(v => v[0]), ['/v1/downloads/preparations/prep%2Fone', '/v1/items/book%2Fone/download-options?versionId=version%2Fone']);
});
test('native seconds and desktop milliseconds keep the same progress DTO and observations', async () => {
  const {api, calls} = fixture({applied: 0, conflicts: 0, entries: []});
  const observedAt = '2026-11-04T12:00:00.000Z'; const signal = new AbortController().signal;
  await syncOfflineDownloadPlays(api, 'native', [{playId: 'play', itemId: 'item', positionSeconds: 1.125, ended: false, observedAt}]);
  await syncDesktopDownloadPlays(api, 'desktop', [{playId: 'play', itemId: 'item', positionMs: 1125, ended: false, observedMs: Date.parse(observedAt)}], signal);
  const entries = [{playId: 'play', itemId: 'item', positionMs: 1125, ended: false, observedAt}];
  assert.deepEqual(calls, [['/v1/downloads/progress', 'POST', {operationId: 'native', entries}], ['/v1/downloads/progress', 'POST', {operationId: 'desktop', entries}, signal]]);
});
