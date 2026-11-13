import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService, downloadCopyId, downloadCopyReference} from '../src/downloads-service.ts';
import {menu, request, server} from './helpers/downloads-next.ts';

test('the rebuilt quality menu offers its real sizes, preparation and room through the existing model', async t => {
  const s = server(), service = new DownloadsService(s.api); t.after(() => service.dispose());
  const result = await service.options('itm_film');
  assert.deepEqual(result.options.map(o => [o.quality, o.estimatedBytes, o.estimated, o.requiresPreparation]), menu.items.map(o => [o.quality, o.sizeBytes, o.estimated, o.prepared]));
  assert.equal(result.storage.remainingBytes, 1000); assert.equal(result.source.bytes, 100);
  assert.equal(s.calls[0]!.path, '/v1/items/itm_film/download-options');
});

test('the server default is the first choice for title and container sheets', async t => {
  const service = new DownloadsService({request: async <T>() => ({...menu, default: '720p-low'}) as T});
  t.after(() => service.dispose());
  const title = await service.options('itm_film');
  const container = await service.containerOptions({id: 'itm_film', kind: 'show'}, 'device', {episodes: 'all'});
  assert.deepEqual(title.options.map(o => o.quality), ['720p-low', 'original']);
  assert.deepEqual(container.options.map(o => o.quality), ['720p-low', 'original']);
  assert.equal(title.source.bytes, 100, 'the original size still describes the source');
});

test('list, request, file, pause and remove use the rebuilt routes without an obsolete background request', async t => {
  const s = server(), service = new DownloadsService(s.api, () => 'intent', 5000, 'profile'); t.after(() => service.dispose());
  assert.equal((await service.qualities()).some(q => q.quality === 'original'), true);
  assert.deepEqual(s.calls, [], 'the shared fixed ladder needs no retired settings route');
  service.followDevice('device'); await service.refreshRequests();
  assert.equal(service.getSnapshot().items[0]?.profileId, 'profile');
  const batch = await service.request({mediaId: 'itm_film'}, 'original', 'pressed');
  const copy = batch.items[0]!;
  assert.equal(copy.downloadId, 'dl_one'); assert.equal(copy.artifact.sha256, '', 'a server hash is not invented for an original');
  assert.deepEqual(downloadCopyReference(copy.id), {downloadId: 'dl_one', itemId: 'itm_film'});
  assert.deepEqual(s.calls.find(c => c.method === 'POST')?.body, {itemId: 'itm_film', quality: 'original', versionId: undefined, operationId: 'pressed'});
  const grant = await service.grant(copy); assert.equal(grant.artifact.bytes, 100); assert.equal(grant.artifact.sha256, '');
  await service.act(copy, 'pause'); assert.equal(service.getSnapshot().items[0]?.state, 'paused');
  await service.act(copy, 'remove'); assert.deepEqual(service.getSnapshot().items, []);
  assert.ok(s.calls.some(c => c.path === '/v1/downloads/dl_one/items/itm_film' && c.method === 'DELETE'));
  assert.ok(s.calls.every(c => !c.path.includes('/requests') && !c.path.includes('/preparations')));
});

test('the complete server list keeps long containers and distinguishes the same title in two requests', async t => {
  const names = Array.from({length: 350}, (_, i) => 'itm_' + i), s = server([request('dl_one', names), request('dl_two', ['itm_0'])]);
  const service = new DownloadsService(s.api); t.after(() => service.dispose()); service.followDevice('device');
  await service.refresh();
  assert.equal(service.getSnapshot().items.length, 351); assert.equal(service.getSnapshot().requests[0]?.request.items.length, 350);
  assert.equal(new Set(service.getSnapshot().items.map(p => p.id)).size, 351);
  const found = await service.lookupPreparations([downloadCopyId('dl_one', 'itm_349'), downloadCopyId('dl_two', 'itm_0')]);
  assert.deepEqual(found.map(p => p.itemId), ['itm_349', 'itm_0']);
});

test('a failed refresh retains downloaded rows and a later successful read clears the error', async t => {
  const s = server(), service = new DownloadsService(s.api); t.after(() => service.dispose());
  await service.refresh(); const items = service.getSnapshot().items;
  s.fail(Object.assign(new Error('busy'), {status: 503})); await service.refresh();
  assert.equal(service.getSnapshot().items, items); assert.equal(service.getSnapshot().phase, 'ready'); assert.ok(service.getSnapshot().error);
  s.fail(); s.set([]); await service.refresh(); assert.deepEqual(service.getSnapshot().items, []); assert.equal(service.getSnapshot().error, undefined);
});
