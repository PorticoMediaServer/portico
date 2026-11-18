import test from 'node:test';
import assert from 'node:assert/strict';
import {readCatalogueDeletion, deleteCatalogueTitles, readLibraryMetadataAgentResponse, refreshCatalogueMetadata} from '../src/query/catalogue-actions.ts';

test('catalogue commands preserve the original body, operation identity and transport signal', async () => {
  const calls: unknown[][] = [], raw = {}, signal = new AbortController().signal;
  const api = {async request(...args: unknown[]) {calls.push(args);return raw;}};
  assert.equal(await readLibraryMetadataAgentResponse(api, 'lib /?', signal), raw);
  assert.equal(await refreshCatalogueMetadata(api, 'movie', 'a /?', signal), raw);
  assert.deepEqual(calls, [
    ['/v1/admin/libraries/lib%20%2F%3F', 'GET', undefined, signal],
    ['/v1/admin/items/a%20%2F%3F/metadata/refresh', 'POST', {}, signal],
  ]);
});
test('an unknown delete outcome is surfaced once, never retried by itself', async () => {
  const error = new TypeError('connection ended');let count = 0;
  await assert.rejects(deleteCatalogueTitles({async request() {count++;throw error;}}, [{itemId: 'a', libraryId: 'l', kind: 'movie'}, {itemId: 'b', libraryId: 'l', kind: 'movie'}]), value => value === error);
  assert.equal(count, 1);
});

test('a library with deleting off is a preview that says why and offers no delete', async () => {
  const refused = Object.assign(new Error('Deleting files is turned off for this library.'), {code: 'deleting_not_allowed', status: 403});
  const preview = await readCatalogueDeletion({async request() {throw refused;}}, [{itemId: 'a', libraryId: 'l', kind: 'movie'}, {itemId: 'b', libraryId: 'l', kind: 'movie'}]);
  assert.deepEqual([preview.refusal, preview.blocked, preview.targets.every(target => !target.allowMediaDeletion)], ['Deleting files is turned off for this library.', ['l'], true]);
});

test('catalogue operations retain transport promise identity and error settlement order', () => {
  const pending = new Promise<unknown>(() => {}), api = {request: () => pending};
  for (const result of [readLibraryMetadataAgentResponse(api, 'library'), refreshCatalogueMetadata(api, 'movie', 'item')]) {
    assert.equal(result, pending);
  }
});

test('confirmed deletion carries the title or selection count to the server', async () => {
 const paths: string[] = [], signal = new AbortController().signal;
 const api = {request: async (path: string, _method?: string, _body?: unknown, sentSignal?: AbortSignal) => {
  paths.push(path);assert.equal(sentSignal, signal);return {removed: paths.length === 1 ? true : 2, kept: [], alsoRemoved: []};
 }};
 const a = {itemId: 'a', libraryId: 'l', kind: 'movie', title: 'Film & Part 2?'};
 await deleteCatalogueTitles(api, [a], signal);
 await deleteCatalogueTitles(api, [a, {...a, itemId: 'b'}], signal);
 assert.deepEqual(paths, ['/v1/admin/items/a?confirm=Film+%26+Part+2%3F', '/v1/admin/items/delete?confirm=2']);
});
