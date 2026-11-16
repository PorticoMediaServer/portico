import test from 'node:test';
import assert from 'node:assert/strict';
import {readMetadataResponse, submitMetadataBulk, readCatalogueDeletion, deleteCatalogueTitles, readLibraryMetadataAgentResponse, refreshCatalogueMetadata} from '../src/query/catalogue-actions.ts';

test('catalogue commands preserve the original body, operation identity and transport signal', async () => {
  const calls: unknown[][] = [], raw = {}, signal = new AbortController().signal;
  const api = {async request(...args: unknown[]) {calls.push(args);return raw;}};
  const edit = {operationId: 'same-operation', targets: [{kind: 'item' as const, id: 'item', expectedRevision: 'revision'}], fields: {title: {value: 'Title'}}};
  assert.equal(await readMetadataResponse(api, 'item', 'a /?', signal), raw);
  assert.equal(await submitMetadataBulk(api, edit, signal), raw);
  assert.equal(await readLibraryMetadataAgentResponse(api, 'lib /?', signal), raw);
  assert.equal(await refreshCatalogueMetadata(api, 'movie', 'a /?', signal), raw);
  assert.deepEqual(calls, [
    ['/v1/metadata/item/a%20%2F%3F', 'GET', undefined, signal],
    ['/v1/metadata/bulk', 'POST', edit, signal],
    ['/v1/admin/libraries/lib%20%2F%3F', 'GET', undefined, signal],
    ['/v1/items/a%20%2F%3F/metadata/refresh', 'POST', {}, signal],
  ]);
  assert.equal(calls[1][2], edit);
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
  const edit = {operationId: 'operation', targets: [], fields: {}};
  for (const result of [readMetadataResponse(api, 'item', 'item'), submitMetadataBulk(api, edit),
    readLibraryMetadataAgentResponse(api, 'library'), refreshCatalogueMetadata(api, 'movie', 'item')]) {
    assert.equal(result, pending);
  }
});
