import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService, downloadCopyId, downloadCopyReference, downloadedBundle, downloadPreparation} from '../src/downloads-service.ts';
import {downloaded, menu, request, server} from './helpers/downloads-next.ts';

test('the rebuilt quality menu offers its real sizes and preparation through the existing model, with no room on the server', async t => {
  const s = server(), service = new DownloadsService(s.api); t.after(() => service.dispose());
  const result = await service.options('itm_film');
  assert.deepEqual(result.options.map(o => [o.quality, o.estimatedBytes, o.estimated, o.requiresPreparation]), menu.items.map(o => [o.quality, o.sizeBytes, o.estimated, o.prepared]));
  assert.equal(result.storage.remainingBytes, null); assert.equal(result.source.bytes, 100);
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


test('multipart grants keep the version packet clock and every file identity', async t => {
  const parts = [0, 1010, 2020].map(offsetMs => ({size: 100, streams: [], durationMs: 999, offsetMs}));
  const document = {...downloaded, item: {...downloaded.item, versions: [{id: downloaded.versionId, durationMs: 3030, parts}]},
    files: [1, 2, 3].map(n => ({url: `/part-${n}`, sizeBytes: 100, container: 'mp3'}))};
  const service = new DownloadsService({request: async <T>() => document as T}); t.after(() => service.dispose());
  const grant = await service.grant({id: downloadCopyId('dl_one', 'itm_film')});
  assert.equal(grant.artifact.bytes, 300);
  assert.deepEqual(grant.files?.map(f => [f.url, f.artifact.fileName, f.offsetSeconds, f.durationSeconds]), [
    ['/part-1', 'part-0001.mp3', 0, 1.01], ['/part-2', 'part-0002.mp3', 1.01, 1.01], ['/part-3', 'part-0003.mp3', 2.02, 1.01],
  ]);
  assert.ok(grant.files?.every(f => f.artifact.sha256 === '' && f.artifact.bytes === 100));
});


test('converted multipart captions remain one language choice on the whole-title clock', () => {
  const version = {id: downloaded.versionId, parts: [{offsetMs: 0}, {offsetMs: 1010}]};
  const track = {id: 'first', part: 1, format: 'vtt', language: 'en', title: 'English', default: true, forced: false, sdh: false, source: 'embedded', url: '/first.vtt'};
  const bundle = downloadedBundle('copy', {...downloaded, quality: '720p-low', item: {...downloaded.item, versions: [version]},
    subtitles: [track, {...track, id: 'second', part: 2, url: '/second.vtt'}]} as any);
  assert.equal(bundle.subtitles.length, 1);
  assert.deepEqual(bundle.subtitles[0]?.partFiles, [{path: '/first.vtt', offsetSeconds: 0}, {path: '/second.vtt', offsetSeconds: 1.01}]);
});


test('rebuilt audiobooks bind to the existing offline audiobook player kind', () => {
  const bundle = downloadedBundle('copy', {...downloaded, item: {...downloaded.item, kind: 'audiobook'}});
  assert.equal(bundle.title.kind, 'audiobook_file');
  const asked = request(), title = {...asked.items![0]!, kind: 'audiobook'};
  assert.equal(downloadPreparation(asked, title).title.kind, 'audiobook_file');
});


test('converted cue ranges are not clipped a second time by the offline player', async t => {
  const document = {...downloaded, quality: '96', item: {...downloaded.item, versions: [{id: downloaded.versionId, durationMs: 2000,
    parts: [{offsetMs: 0, durationMs: 1000, startMs: 5000, endMs: 6000}, {offsetMs: 1000, durationMs: 1000, startMs: 6000, endMs: 7000}]}]},
    files: [1, 2].map(n => ({url: `/part-${n}`, sizeBytes: 100, sha256: 'a'.repeat(64), container: 'mp3'}))};
  const service = new DownloadsService({request: async <T>() => document as T}); t.after(() => service.dispose());
  const grant = await service.grant({id: downloadCopyId('dl_one', 'itm_film')});
  assert.ok(grant.files?.every(file => file.startSeconds === undefined && file.endSeconds === undefined));
});

test('an unchanged ready file survives refresh while its Save grant is pending, but changed files retire it', async t => {
 const s = server(), send = s.api.request.bind(s.api);let release!: () => void;
 s.api.request = async <T>(path: string, method?: string, body?: unknown) => {
  if (path === '/v1/downloads/dl_one/items/itm_film') await new Promise<void>(resolve => {release = resolve;});
  return send<T>(path, method, body);
 };
 const service = new DownloadsService(s.api);t.after(() => service.dispose());
 await service.refresh();const held = service.getSnapshot().items[0]!;
 const grant = service.grant(held);
 s.set([structuredClone(request())]);await service.refresh();release();await grant;
 assert.equal(service.getSnapshot().items.includes(held), true, 'a recreated server page alone must not cancel Save file');
 s.set([{...request(), quality: '720p-low'}]);await service.refresh();assert.equal(service.getSnapshot().items.includes(held), false);
 s.set([]);await service.refresh();s.set([request()]);await service.refresh();assert.equal(service.getSnapshot().items.includes(held), false, 'removal and reappearance is a new lifetime');
});


test('received files are terminal even in a paused request: remove only, no grant or preparation poll', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const d = request(); d.paused = true; d.items![0]!.state = 'received';
  const s = server([d]), service = new DownloadsService(s.api); t.after(() => service.dispose());
  service.start(); await service.refresh();
  const copy = service.getSnapshot().items[0]!;
  assert.equal(copy.state, 'received'); assert.deepEqual(copy.actions, ['remove']);
  await assert.rejects(service.grant(copy));
  assert.equal(s.calls.some(c => c.path.includes('/items/')), false);
  const reads = s.calls.length;
  t.mock.timers.tick(15000); await Promise.resolve();
  assert.equal(s.calls.length, reads, 'a received copy never waits for preparation');
  await service.act(copy, 'remove'); assert.deepEqual(service.getSnapshot().items, []);
});
