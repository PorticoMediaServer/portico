import test from 'node:test';
import assert from 'node:assert/strict';
import {PersonalSavedService, type SavedDefinition} from '@core/personal-saved.ts';
import {saveViewDraft} from '../src/screens/browse/save-view.ts';

const definition: SavedDefinition = {libraryId: 'movies', pivot: 'movies', query: {variant:'predicate',field: 'playState', operator: 'equals', value: 'unplayed'}, sort: [{field: 'added', direction: 'desc'}], presentation: 'grid'};
const directory = () => ({serverId: 'server', viewerFence: 'saved-only', revision: 1, resources: [], nextCursor: ''});
test('lost create response retries the original operation/definition without reselecting or issuing a duplicate', async () => {
  const writes: unknown[] = [];
  let reads = 0, ids = 0;
  const service = new PersonalSavedService({request: async<T>(_path: string, method = 'GET', body?: unknown): Promise<T> => {
    if (method === 'GET') {reads++;return directory() as T;}
    writes.push(body);
    if (writes.length === 1) throw new TypeError('Connection lost');
    return {serverId: 'server', viewerFence: 'saved-only', receipt: {operationId: (body as {operationId: string}).operationId, resourceId: 'actual-created-view', revision: 1, deleted: false}} as T;
  }}, {serverId: 'server', viewerId: 'viewer'}, async () => `00000000-0000-4000-8000-${String(++ids).padStart(12, '0')}`);
  try {
    await assert.rejects(saveViewDraft(service, definition, '  Unwatched  ', () => true));
    assert.equal(reads, 1);assert.equal(service.getSnapshot().retryPending, true);
    assert.equal(await saveViewDraft(service, definition, 'Unwatched', () => true), 'actual-created-view');
    assert.equal(ids, 1);assert.equal(writes.length, 2);assert.deepEqual(writes[0], writes[1]);
    assert.equal((writes[0] as {name: string}).name, 'Unwatched');
    assert.deepEqual((writes[0] as {definition: SavedDefinition}).definition, definition);
    assert.equal(reads, 2, 'only successful receipt refreshes the directory');
  } finally {service.dispose();}
});
test('a directory read failure keeps the draft unsubmitted and can be retried as a read', async () => {
  let reads = 0, writes = 0;
  const service = new PersonalSavedService({request: async<T>(_path: string, method = 'GET'): Promise<T> => {
    if (method === 'GET') {if (++reads === 1) throw new TypeError('Offline');return directory() as T;}
    writes++;throw new TypeError('Offline');
  }}, {serverId: 'server', viewerId: 'viewer'}, async () => '00000000-0000-4000-8000-000000000001');
  try {
    await assert.rejects(saveViewDraft(service, definition, 'My view', () => true));assert.equal(writes, 0);
    await assert.rejects(saveViewDraft(service, definition, 'My view', () => true));assert.equal(reads, 2);assert.equal(writes, 1);
  } finally {service.dispose();}
});
test('retired dialog cannot turn a late directory read into a create', async () => {
  let current = true, writes = 0;
  const service = new PersonalSavedService({request: async<T>(_path: string, method = 'GET'): Promise<T> => {
    if (method === 'GET') {current = false;return directory() as T;}
    writes++;throw new Error('must not create');
  }}, {serverId: 'server', viewerId: 'viewer'}, async () => '00000000-0000-4000-8000-000000000001');
  try {await assert.rejects(saveViewDraft(service, definition, 'My view', () => current), {name: 'AbortError'});assert.equal(writes, 0);} finally {service.dispose();}
});
