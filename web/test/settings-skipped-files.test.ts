import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '@core/library-content.ts';
import {skippedFiles} from '../src/screens/settings/libraries/skipped-files.ts';
const page = {nextCursor: '40', items: [{path: 'TV/file.mkv', reason: 'damaged', message: 'This file is damaged.'},
  {path: 'TV/link.strm', reason: 'strm_unavailable', message: 'The stream cannot be reached.'}]};
const api = (value: unknown, inspect?: (path: string, signal?: AbortSignal) => void) => ({request: async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {inspect?.(path, signal);return value as T;}} as LibraryContentApi);
test('skipped files read the library\'s skipped list and keep the caller\'s abort lifetime', async () => {
  const life = new AbortController(), paths: string[] = [];
  const result = await skippedFiles(api(page, (path, signal) => {paths.push(path);assert.equal(signal, life.signal);}), 'library', '', life.signal);
  await skippedFiles(api({items: []}, path => paths.push(path)), 'library', '40', life.signal);
  assert.deepEqual(paths, ['/v1/admin/libraries/library/skipped', '/v1/admin/libraries/library/skipped?cursor=40']);
  assert.deepEqual(result, {items: [{path: 'TV/file.mkv', reason: 'scan.issue.damaged', message: 'This file is damaged.'},
    {path: 'TV/link.strm', message: 'The stream cannot be reached.'}], hasMore: true, next: '40'});
});
test('the last page has no more', async () => {
  assert.deepEqual(await skippedFiles(api({items: []}), 'library', '', new AbortController().signal), {items: [], hasMore: false, next: ''});
});
test('prototype property names are not catalogue reasons', async () => {
  for (const reason of ['__proto__', 'constructor', 'toString']) {
    const result = await skippedFiles(api({items: [{path: 'a.mkv', reason, message: 'm'}]}), 'library', '', new AbortController().signal);
    assert.equal(result.items[0].reason, undefined);
  }
});
