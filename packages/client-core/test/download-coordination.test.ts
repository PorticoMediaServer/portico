import test from 'node:test';
import assert from 'node:assert/strict';
import {grantPreparedDownloadResponse, readDownloadPreparationResponse} from '../src/download-coordination.ts';
import {readDownloadOptionsResponse} from '../src/query/download-documents.ts';

function fixture(answer: unknown) {
  const calls: unknown[][] = [];
  return {calls, api: {request: async <T>(...args: unknown[]): Promise<T> => {calls.push(args); return answer as T;}}};
}
test('named operations return the transport promise itself without parsing or an added rejection boundary', () => {
  const held = new Promise<unknown>(() => {});
  const api = {request: <T>() => held as Promise<T>}; const signal = new AbortController().signal;
  const operations = [
    () => grantPreparedDownloadResponse(api, 'prep', 'held', signal),
    () => readDownloadPreparationResponse(api, 'prep', signal),
    () => readDownloadOptionsResponse(api, 'book', signal),
  ];
  for (const operation of operations) assert.strictEqual(operation(), held);
});
test('preparation and options reads encode their identifiers', async () => {
  const signal = new AbortController().signal;
  const invalid = fixture({});
  assert.deepEqual(await readDownloadPreparationResponse(invalid.api, 'prep/one', signal), {});
  assert.deepEqual(await readDownloadOptionsResponse(invalid.api, 'book/one', signal, 'version/one'), {});
  assert.deepEqual(invalid.calls.map(v => v[0]), ['/v1/downloads/preparations/prep%2Fone', '/v1/items/book%2Fone/download-options?versionId=version%2Fone']);
});
