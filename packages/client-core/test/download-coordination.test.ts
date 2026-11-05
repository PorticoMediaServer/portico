import test from 'node:test';
import assert from 'node:assert/strict';
import {prepareItemDownloadResponse, readDownloadPreparationResponse, readDownloadOptionsResponse, readDownloadReceiptKeysResponse, issueDownloadReceiptsResponse,
  revalidateDownloadReceiptsResponse, revokeRemovedDownloadReceipt, readDownloadRevocationsResponse, syncOfflineDownloadPlaysResponse, syncDesktopDownloadPlaysResponse} from '../src/download-coordination.ts';

function fixture(answer: unknown) {
  const calls: unknown[][] = [];
  return {calls, api: {request: async <T>(...args: unknown[]): Promise<T> => {calls.push(args); return answer as T;}}};
}
test('download admission preserves the intent, version, signal and one-request budget on retry', async () => {
  const {api, calls} = fixture({batchId: 'batch', accepted: 0, items: [], rejected: [], duplicate: false});
  const signal = new AbortController().signal;
  const intent = {operationId: 'held', mediaId: 'book', versionId: 'publication', quality: 'original', localOnly: 'ignored'};
  await prepareItemDownloadResponse(api, intent, signal); await prepareItemDownloadResponse(api, intent, signal);
  const expected = ['/v1/downloads/preparations', 'POST', {operationId: 'held', mediaId: 'book', versionId: 'publication', quality: 'original'}, signal];
  assert.deepEqual(calls, [expected, expected]);
});
test('named operations return the transport promise itself without parsing or an added rejection boundary', () => {
  const held = new Promise<unknown>(() => {});
  const api = {request: <T>() => held as Promise<T>}; const signal = new AbortController().signal;
  const operations = [
    () => prepareItemDownloadResponse(api, {operationId: 'held', mediaId: 'book', quality: 'original'}, signal),
    () => readDownloadPreparationResponse(api, 'prep', signal), () => readDownloadOptionsResponse(api, 'book', signal),
    () => readDownloadReceiptKeysResponse(api, signal), () => issueDownloadReceiptsResponse(api, 'issue', ['p'], signal),
    () => revalidateDownloadReceiptsResponse(api, 'renew', ['r']), () => revokeRemovedDownloadReceipt(api, 'remove', 'r'),
    () => readDownloadRevocationsResponse(api, '', signal), () => syncOfflineDownloadPlaysResponse(api, 'plays', []),
    () => syncDesktopDownloadPlaysResponse(api, 'plays', [], signal),
  ];
  for (const operation of operations) assert.strictEqual(operation(), held);
});
test('receipt coordination preserves batches, stable IDs, scoped transport and cancellation without extra reads', async () => {
  const {api, calls} = fixture({results: []}); const signal = new AbortController().signal;
  await issueDownloadReceiptsResponse(api, 'issue', ['a', 'b'], signal);
  await revalidateDownloadReceiptsResponse(api, 'renew', ['r1', 'r2']);
  await revokeRemovedDownloadReceipt(api, 'remove', 'r1');
  assert.deepEqual(calls, [
    ['/v1/downloads/receipts', 'POST', {operationId: 'issue', preparationIds: ['a', 'b']}, signal],
    ['/v1/downloads/receipts/revalidate', 'POST', {operationId: 'renew', receiptIds: ['r1', 'r2']}],
    ['/v1/downloads/receipts/revoke', 'POST', {operationId: 'remove', receiptIds: ['r1'], reason: 'removed'}],
  ]);
});
test('discovery keeps opaque cursor encoding and response validation', async () => {
  const signal = new AbortController().signal;
  const keys = fixture({keys: []}); assert.deepEqual(await readDownloadReceiptKeysResponse(keys.api, signal), {keys: []});
  assert.deepEqual(keys.calls, [['/v1/downloads/receipt-keys', 'GET', undefined, signal]]);
  const revocations = fixture({items: [], nextCursor: '', asOf: '2026-11-04T00:00:00Z'});
  await readDownloadRevocationsResponse(revocations.api, 'opaque/+=', signal);
  assert.deepEqual(revocations.calls, [['/v1/downloads/receipts/revocations?cursor=opaque%2F%2B%3D', 'GET', undefined, signal]]);
  const invalid = fixture({});
  assert.deepEqual(await readDownloadPreparationResponse(invalid.api, 'prep/one', signal), {});
  assert.deepEqual(await readDownloadOptionsResponse(invalid.api, 'book/one', signal, 'version/one'), {});
  assert.deepEqual(invalid.calls.map(v => v[0]), ['/v1/downloads/preparations/prep%2Fone', '/v1/items/book%2Fone/download-options?versionId=version%2Fone']);
});
test('native seconds and desktop milliseconds keep the same progress DTO and observations', async () => {
  const {api, calls} = fixture({applied: 0, conflicts: 0, entries: []});
  const observedAt = '2026-11-04T12:00:00.000Z'; const signal = new AbortController().signal;
  await syncOfflineDownloadPlaysResponse(api, 'native', [{playId: 'play', itemId: 'item', positionSeconds: 1.125, ended: false, observedAt}]);
  await syncDesktopDownloadPlaysResponse(api, 'desktop', [{playId: 'play', itemId: 'item', positionMs: 1125, ended: false, observedMs: Date.parse(observedAt)}], signal);
  const entries = [{playId: 'play', itemId: 'item', positionMs: 1125, ended: false, observedAt}];
  assert.deepEqual(calls, [['/v1/downloads/progress', 'POST', {operationId: 'native', entries}], ['/v1/downloads/progress', 'POST', {operationId: 'desktop', entries}, signal]]);
});

test('offline progress body validation remains synchronous before dispatch, matching the original request argument', () => {
  let sent = false;
  const api = {request: <T>() => {sent = true; return Promise.resolve({} as T);}};
  assert.throws(() => syncOfflineDownloadPlaysResponse(api, 'held', [{playId: 'p', itemId: 'i', positionSeconds: NaN, observedAt: '2026-11-04T00:00:00Z'}]));
  assert.equal(sent, false);
});
