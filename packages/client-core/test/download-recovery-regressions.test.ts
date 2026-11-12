import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService, downloadCopyId} from '../src/downloads-service.ts';
import {request} from './helpers/downloads-next.ts';

test('exact recovery reads one complete request once for all its requested titles and never grows the on-screen list', async t => {
  let reads = 0;
  const service = new DownloadsService({async request<T>(): Promise<T> {reads++; return request('dl_one', ['a', 'b', 'c']) as T;}});
  t.after(() => service.dispose()); const before = service.getSnapshot();
  const found = await service.lookupPreparations(['a', 'c', 'a'].map(id => downloadCopyId('dl_one', id)));
  assert.deepEqual(found.map(p => p.itemId), ['a', 'c']); assert.equal(reads, 1); assert.equal(service.getSnapshot(), before);
  await assert.rejects(service.lookupPreparations(['not a copy'])); assert.equal(reads, 1);
});

test('a failed initial background read is retried while the service is started', async t => {
  let reads = 0;
  const service = new DownloadsService({async request<T>(path: string): Promise<T> {
    if (path.includes('/usage')) return {copiesBytes: 0} as T;
    if (++reads === 1) throw Object.assign(new Error('busy'), {status: 503});
    return {items: [request()]} as T;
  }}, () => 'intent', 5); t.after(() => service.dispose());
  service.start();
  for (let i = 0; i < 50 && service.getSnapshot().phase !== 'ready'; i++) await new Promise(resolve => setTimeout(resolve, 5));
  assert.equal(service.getSnapshot().phase, 'ready'); assert.equal(reads, 2); assert.equal(service.getSnapshot().failure, undefined);
});
