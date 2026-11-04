import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService} from '../src/downloads-service.ts';

// #736: a retried submit resends the identical body under the same operation id; a changed choice needs a new one.
// The item request now takes the caller's id for its intent; without one it still makes a new id per call.
const batch = {batchId: 'b', accepted: 0, items: [], rejected: [], duplicate: false};
function fixture() {
  const sent: any[] = [];
  let n = 0;
  const service = new DownloadsService({request: async <T,>(path: string, method = 'GET', body?: any): Promise<T> => {
    if (path === '/v1/downloads/preparations' && method === 'POST') {sent.push(body); return batch as T;}
    return {items: [], nextCursor: ''} as T;
  }} as any, () => `generated_${++n}`);
  return {service, sent};
}

test('an item request sent with the caller operation id sends exactly that id, and the same body for the same choice', async () => {
  const {service, sent} = fixture();
  await service.request({mediaId: 'film'}, '720p-high', 'held_1');
  await service.request({mediaId: 'film'}, '720p-high', 'held_1');
  assert.deepEqual(sent, [{operationId: 'held_1', mediaId: 'film', quality: '720p-high'}, {operationId: 'held_1', mediaId: 'film', quality: '720p-high'}]);
});

test('without the caller id each item request gets a new one, and a malformed id is refused before anything is sent', async () => {
  const {service, sent} = fixture();
  await service.request({mediaId: 'film'}, 'original');
  await service.request({mediaId: 'film'}, 'original');
  assert.deepEqual(sent.map(body => body.operationId), ['generated_1', 'generated_2']);
  await assert.rejects(service.request({mediaId: 'film'}, 'original', 'not ok!'), /stable operationId/);
  assert.equal(sent.length, 2);
});
