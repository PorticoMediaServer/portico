import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '@core/library-content.ts';
import {skippedFiles} from '../src/screens/settings/libraries/skipped-files.ts';
const page = {jobId: 'job', count: 2, hasMore: true, nextSequence: 4, items: [{sequence: 4, path: 'TV/file.mkv', category: 'damaged', reason: 'file_truncated'}]};
const api = (value: unknown, inspect?: (path: string, signal?: AbortSignal) => void) => ({request: async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {inspect?.(path, signal);return value as T;}} as LibraryContentApi);
test('skipped files use a bounded page and preserve the caller abort lifetime', async () => {
  const life = new AbortController();
  const result = await skippedFiles(api(page, (path, signal) => {assert.equal(path, '/v1/admin/libraries/library/scans/job/issues?after=0&limit=40');assert.equal(signal, life.signal);}), 'library', 'job', 0, life.signal);
  assert.deepEqual(result, {items: [{sequence: 4, path: 'TV/file.mkv', reason: 'scan.issue.file_truncated'}], count: 2, hasMore: true, next: 4});
});
test('another scan, stalled pagination and non-playability categories are refused', async () => {
  for (const value of [{...page, jobId: 'other'}, {...page, nextSequence: 0}, {...page, items: [{...page.items[0], category: 'unmatched'}]}]) {
    await assert.rejects(skippedFiles(api(value), 'library', 'job', 0, new AbortController().signal));
  }
});
test('unknown reasons use known category copy and never render arbitrary server message keys', async () => {
  const result = await skippedFiles(api({...page, items: [{...page.items[0], category: 'not-media', reason: 'new_reason', messageKey: 'not.a.catalogue.key'}]}), 'library', 'job', 0, new AbortController().signal);
  assert.equal(result.items[0].reason, 'scan.issue.not_media');
});

test('prototype property names cannot escape the known category and reason catalogue', async () => {
  for (const key of ['__proto__', 'constructor', 'toString']) {
    await assert.rejects(skippedFiles(api({...page, items: [{...page.items[0], category: key}]}), 'library', 'job', 0, new AbortController().signal));
    const result = await skippedFiles(api({...page, items: [{...page.items[0], reason: key}]}), 'library', 'job', 0, new AbortController().signal);
    assert.equal(result.items[0].reason, 'scan.issue.damaged');
  }
});
