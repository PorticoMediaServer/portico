import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineFixture, viewer, owner, settle, digest, title} from './helpers/offline-fixture.mjs';
import {downloadCopyId, downloadPreparation} from '../../../packages/client-core/src/downloads-service.ts';
import {downloaded, request} from '../../../packages/client-core/test/helpers/downloads-next.ts';

test('a server-next original arrives, plays with no server, expires after its permission and keeps an offline removal', async t => {
  const f = await offlineFixture(); t.after(() => f.offline.dispose());
  const id = downloadCopyId('dl_one', 'itm_film'), p = downloadPreparation(request(), request().items![0]!);
  const service = Object.assign(f.service, {downloaded: async () => downloaded,
    grant: async () => ({url: downloaded.files[0]!.url, artifact: {ref: downloaded.versionId, sha256: '', bytes: 100, fileName: 'media.mp4', container: 'mp4', contentType: ''}})});
  f.handlers.set('/v1/downloads/sync', () => ({playableUntil: new Date(Date.now() + 30 * 86400000).toISOString(), playableForMs: 30 * 86400000, fetch: [], remove: []}));
  await f.offline.open(); f.offline.connect(f.api, service as any, viewer); await settle();
  await f.offline.fetch(p);
  assert.equal(f.started[0]?.sha256, '', 'no server digest was fabricated');
  assert.equal(f.started[0]?.sourceId, downloaded.versionId);
  assert.equal(JSON.parse(f.started[0]!.meta).downloadId, 'dl_one');
  const native = f.entries.get(id)!; Object.assign(native, {state: 'ready', bytesDone: 100, sha256: digest}); f.emit(native); await settle();
  const entry = f.offline.getSnapshot().entries[0]!;
  assert.equal(f.offline.playabilityReason(entry), null); assert.equal(entry.receipt, undefined);
  assert.equal(f.offline.playabilityReason(entry, {...viewer, profileId: 'someone_else'}), 'other_profile');
  f.offline.disconnect();
  const before = f.calls.length, media = await f.offline.localMedia(entry);
  assert.ok(media.fileUrl.startsWith('file:')); assert.equal(f.calls.length, before, 'offline playback makes no request');
  await f.offline.remove(entry); assert.deepEqual(f.offline.getSnapshot().entries, []);
  const kept = JSON.parse(f.disk.state); assert.deepEqual(kept.removed[owner][id], {downloadId: 'dl_one', itemId: 'itm_film'});
  f.handlers.set('/v1/downloads/dl_one/items/itm_film', () => undefined);
  f.offline.connect(f.api, service as any, viewer); await settle();
  assert.ok(f.calls.some(c => c.path === '/v1/downloads/dl_one/items/itm_film' && c.method === 'DELETE'));
  assert.equal(JSON.parse(f.disk.state).removed[owner][id], undefined);
  assert.deepEqual(f.offline.getSnapshot().entries, []);
  const expired = await offlineFixture({state: JSON.stringify({...kept, permissions: {[owner]: 1}, removed: {}})}); t.after(() => expired.offline.dispose());
  expired.seed(id, 'itm_film', {sha256: digest, meta: {downloadId: 'dl_one', owner, title, bundle: 4}});
  expired.offline.adopt(viewer); await expired.offline.open();
  assert.equal(expired.offline.playabilityReason(expired.offline.getSnapshot().entries[0]!), 'needs_check_in');
});


test('sync sends only the signed-in profile titles on a shared phone', async t => {
  const f = await offlineFixture(); t.after(() => f.offline.dispose());
  const other = {...viewer, profileId: 'other_profile'};
  const otherOwner = JSON.stringify([other.authority, other.accountId, other.profileId, other.serverId]);
  f.seed('mine', 'itm_mine', {sha256: digest, meta: {downloadId: 'dl_mine', owner, title, bundle: 4}});
  f.seed('theirs', 'itm_theirs', {sha256: digest, meta: {downloadId: 'dl_theirs', owner: otherOwner, title, bundle: 4}});
  const service = Object.assign(f.service, {downloaded: async () => downloaded});
  f.handlers.set('/v1/downloads/sync', body => {
    assert.deepEqual(body.holds, [{downloadId: 'dl_mine', itemId: 'itm_mine'}]);
    return {playableUntil: new Date(Date.now() + 30 * 86400000).toISOString(), playableForMs: 30 * 86400000,
      fetch: [], remove: [{downloadId: 'dl_mine', itemId: 'itm_mine', reason: 'removed'}]};
  });
  await f.offline.open(); f.offline.connect(f.api, service as any, viewer); await settle();
  assert.ok(f.calls.some(c => c.path === '/v1/downloads/sync'));
  assert.deepEqual(f.removed, ['mine']);
  assert.ok(f.entries.has('theirs'), 'the other profile download is preserved');
});


test('multipart files transfer as one title and keep usable captions for the later file', async t => {
  const f = await offlineFixture(); t.after(() => f.offline.dispose());
  const asked = {...request(), quality: '720p-low'}, id = downloadCopyId(asked.id, 'itm_film');
  const doc = {...downloaded, quality: asked.quality, item: {...downloaded.item, durationMs: 2000,
    versions: [{id: downloaded.versionId, durationMs: 2000, parts: [{offsetMs: 0}, {offsetMs: 1000}]}]},
    files: [1, 2].map(n => ({url: `https://server.invalid/part-${n}`, sizeBytes: 100, sha256: digest, container: 'mp4'})),
    subtitles: [1, 2].map(part => ({id: `sub-${part}`, part, language: 'en', title: 'English', format: 'vtt', default: true, forced: false, sdh: false, source: 'embedded', url: `https://server.invalid/caption-${part}`}))};
  const files = doc.files.map((file, n) => ({url: file.url, artifact: {ref: downloaded.versionId, sha256: digest, bytes: 100, fileName: `part-${n + 1}.mp4`, container: 'mp4', contentType: ''}, offsetSeconds: n, durationSeconds: 1, startSeconds: 0}));
  const service = Object.assign(f.service, {downloaded: async () => doc,
    grant: async () => ({url: files[0]!.url, artifact: {...files[0]!.artifact, bytes: 200, sha256: ''}, files})});
  const start = f.native.start;
  f.native.start = async request => {const entry = await start(request); entry.dir = `file:///downloads/${request.id}/`; f.emit(entry); return entry;};
  const extras = new Map<string, string>();
  f.native.fetchExtra = async (_id, name) => {extras.set(name, 'WEBVTT\n\n00:00:00.000 --> 00:00:00.900\nCaption\n'); return 'saved';};
  f.native.readExtra = async (_id, name) => extras.get(name) ?? '';
  f.native.writeExtra = async (_id, name, value) => {extras.set(name, value);};
  f.handlers.set('/v1/downloads/sync', () => ({playableUntil: new Date(Date.now() + 30 * 86400000).toISOString(), playableForMs: 30 * 86400000, fetch: [], remove: []}));
  await f.offline.open(); f.offline.connect(f.api, service as any, viewer); await settle();
  await f.offline.fetch(downloadPreparation(asked, asked.items![0]!)); await settle();
  assert.equal(f.started.length, 1); assert.equal(f.started[0]?.bytes, 200);
  assert.equal('endSeconds' in f.started[0]!.files[0], false, 'optional clocks never cross the Apple bridge as null');
  assert.deepEqual(f.started[0]?.files?.map((file: {offsetSeconds?: number}) => file.offsetSeconds), [0, 1]);
  const native = f.entries.get(id)!; Object.assign(native, {state: 'ready', bytesDone: 200, sha256: digest}); f.emit(native); await settle();
  const entry = f.offline.getSnapshot().entries[0]!, bundle = await f.offline.extras(entry);
  f.native.localFile = async () => ({url: 'file:///downloads/first.mp4', durationSeconds: 1.5,
    parts: [{url: 'file:///downloads/first.mp4', offsetSeconds: 0, durationSeconds: .5, sourceOffsetSeconds: 0, startSeconds: 0},
      {url: 'file:///downloads/second.mp4', offsetSeconds: .5, durationSeconds: 1, sourceOffsetSeconds: 1, startSeconds: 0}]});
  const local = await f.offline.localMedia(entry);
  assert.equal(local.durationSeconds, 1.5);
  assert.equal(local.extras?.bundle?.chapters[0]?.endSeconds, 1.5, 'chapter end follows the measured converted files');
  assert.equal(bundle.subtitles?.length, 1);
  assert.ok(extras.get('sub-sub-1-1.vtt')?.includes('00:00:01.000 --> 00:00:01.900'));
  const cached = extras.get('bundle.json')!;
  assert.equal(cached.includes('https://server.invalid'), false, 'finished metadata keeps local cache paths');
});
