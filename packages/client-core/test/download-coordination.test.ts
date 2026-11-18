import test from 'node:test';
import assert from 'node:assert/strict';
import {readDownloadOptionsResponse} from '../src/query/download-documents.ts';

function fixture(answer: unknown) {
  const calls: unknown[][] = [];
  return {calls, api: {request: async <T>(...args: unknown[]): Promise<T> => {calls.push(args); return answer as T;}}};
}
test('the download options read returns the transport promise itself without parsing or an added rejection boundary', () => {
  const held = new Promise<unknown>(() => {});
  const api = {request: <T>() => held as Promise<T>}; const signal = new AbortController().signal;
  assert.strictEqual(readDownloadOptionsResponse(api, 'book', signal), held);
});
test('the options read encodes the item and version identifiers', async () => {
  const signal = new AbortController().signal;
  const invalid = fixture({});
  assert.deepEqual(await readDownloadOptionsResponse(invalid.api, 'book/one', signal, 'version/one'), {});
  assert.deepEqual(invalid.calls.map(v => v[0]), ['/v1/items/book%2Fone/download-options?versionId=version%2Fone']);
});
