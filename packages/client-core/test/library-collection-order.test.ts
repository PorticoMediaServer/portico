import test from 'node:test';
import assert from 'node:assert/strict';
import {moveCollectionTitle} from '../src/library-collection-order.ts';

test('a library collection move reads the revision, sends the move, and is sent once more when the collection changed meanwhile', async () => {
  const calls: {path: string; method: string; body?: any}[] = [];
  let revision = 4, refuse = 1;
  const request = async (path: string, method: 'GET' | 'PUT', body?: unknown): Promise<any> => {
    calls.push({path, method, body});
    if (method === 'GET') return {id: 'c1', revision};
    if (refuse-- > 0) { revision++; throw Object.assign(new Error('changed'), {code: 'revision_mismatch'}); }
    return {id: 'c1', revision: revision + 1};
  };
  assert.equal(await moveCollectionTitle(request, 'c1', 'i3', ''), 6);
  assert.deepEqual(calls.map(c => `${c.method} ${c.path}`), ['GET /v1/collections/c1', 'PUT /v1/collections/c1/order', 'GET /v1/collections/c1', 'PUT /v1/collections/c1/order']);
  assert.deepEqual(calls[1]!.body, {expectedRevision: 4, itemIds: ['i3'], afterItemId: ''});
  assert.equal(calls[3]!.body.expectedRevision, 5);
  // Any other refusal is the caller's to show.
  const denied = async (_path: string, method: 'GET' | 'PUT'): Promise<any> => { if (method === 'GET') return {revision: 1}; throw Object.assign(new Error('no'), {code: 'not_permitted'}); };
  await assert.rejects(moveCollectionTitle(denied, 'c1', 'i3', 'i1'), (e: any) => e.code === 'not_permitted');
});
