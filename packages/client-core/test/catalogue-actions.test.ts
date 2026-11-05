import test from 'node:test';
import assert from 'node:assert/strict';
import {readMetadataResponse, submitMetadataBulk, previewCatalogueDeletion, deleteCatalogueItems, readLibraryMetadataAgentResponse, refreshCatalogueMetadata} from '../src/query/catalogue-actions.ts';

test('catalogue commands preserve the original body, operation identity and transport signal', async () => {
  const calls: unknown[][] = [], raw = {}, signal = new AbortController().signal;
  const api = {async request(...args: unknown[]) {calls.push(args);return raw;}};
  const edit = {operationId: 'same-operation', targets: [{kind: 'item' as const, id: 'item', expectedRevision: 'revision'}], fields: {title: {value: 'Title'}}};
  const deletion = {itemIds: ['item'], operationId: 'delete-operation', expectedRevision: 'delete-revision', confirmation: 'Delete', deleteFiles: false};
  assert.equal(await readMetadataResponse(api, 'item', 'a /?', signal), raw);
  assert.equal(await submitMetadataBulk(api, edit, signal), raw);
  assert.equal(await previewCatalogueDeletion(api, deletion.itemIds, signal), raw);
  assert.equal(await deleteCatalogueItems(api, deletion, signal), raw);
  assert.equal(await readLibraryMetadataAgentResponse(api, 'lib /?', signal), raw);
  assert.equal(await refreshCatalogueMetadata(api, 'movie', 'a /?', signal), raw);
  assert.deepEqual(calls, [
    ['/v1/metadata/item/a%20%2F%3F', 'GET', undefined, signal],
    ['/v1/metadata/bulk', 'POST', edit, signal],
    ['/v1/admin/media/delete/preview', 'POST', {itemIds: deletion.itemIds}, signal],
    ['/v1/admin/media/delete', 'POST', deletion, signal],
    ['/v1/libraries/lib%20%2F%3F/metadata/agent', 'GET', undefined, signal],
    ['/v1/items/a%20%2F%3F/metadata/refresh', 'POST', {}, signal],
  ]);
  assert.equal(calls[1][2], edit);assert.equal(calls[3][2], deletion);
});
test('an unknown write outcome is surfaced once without retrying or replacing its body', async () => {
  const error = new TypeError('connection ended');let count = 0;
  await assert.rejects(deleteCatalogueItems({async request() {count++;throw error;}}, {}), value => value === error);
  assert.equal(count, 1);
});

test('catalogue operations retain transport promise identity and error settlement order', () => {
  const pending = new Promise<unknown>(() => {}), api = {request: () => pending};
  const edit = {operationId: 'operation', targets: [], fields: {}};
  for (const result of [readMetadataResponse(api, 'item', 'item'), submitMetadataBulk(api, edit),
    previewCatalogueDeletion(api, ['item']), deleteCatalogueItems(api, {}),
    readLibraryMetadataAgentResponse(api, 'library'), refreshCatalogueMetadata(api, 'movie', 'item')]) {
    assert.equal(result, pending);
  }
});
