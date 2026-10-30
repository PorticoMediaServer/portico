import assert from 'node:assert/strict';
import test from 'node:test';
import {MetadataLyricsService} from '../src/metadata-lyrics.ts';

const scope = {serverId: 'server', viewerId: 'owner'}, target = {kind: 'item' as const, id: 'song', libraryId: 'library'};
function view() {
  const source = {id: 'asset', version: 'b'.repeat(64), startMs: 0, durationMs: 120000, sourceDurationMs: 120000, local: true};
  const resource = {id: 'lyrics', revision: 1, scope: 'library', canManage: true, deleted: false, language: 'en', offsetMs: 0, format: 'text', digest: 'c'.repeat(64), provenance: {origin: 'upload', providerId: '', label: 'Library lyrics', rights: ''}, createdAt: '2026-10-25T00:00:00Z'};
  return {scope: {serverId: scope.serverId, libraryId: target.libraryId, itemId: target.id, viewerFence: 'a'.repeat(64), sessionId: '', sessionGeneration: 0}, sources: [source], source, resources: [resource], selection: {revision: 0, resource: null}, canShare: true, providers: {lrclib: false, configured: false, revision: 1}};
}

test('owner choice sends exact current source and global CAS, without a playback session', async () => {
  const writes: {method?: string; body?: unknown}[] = [];
  const service = new MetadataLyricsService({scope, target, current: () => true, api: {async request<T>(_path: string, method?: string, body?: unknown) {
    if (method !== 'GET') writes.push({method, body});
    return view() as T;
  }}});
  await service.load();
  const data = service.getSnapshot().data!;
  await service.choose(data.resources[0]!, data);
  assert.deepEqual(writes, [{method: 'PUT', body: {scope: 'library', sourceId: 'asset', sourceVersion: 'b'.repeat(64), sessionId: '', expectedSelectionRevision: 0, resourceId: 'lyrics', revision: 1}}]);
  service.dispose();
});

test('old view callbacks and a synchronously retired saving grant send no lyrics write', async () => {
  let current = true, writes = 0;
  const service = new MetadataLyricsService({scope, target, current: () => current, api: {async request<T>(_path: string, method?: string) {if (method !== 'GET') writes++; return view() as T;}}});
  await service.load();
  const old = service.getSnapshot().data!;
  await service.load();
  await service.choose(old.resources[0]!, old);
  assert.equal(writes, 0);
  const data = service.getSnapshot().data!;
  const off = service.subscribe(() => {if (service.getSnapshot().phase === 'saving') current = false;});
  await service.choose(data.resources[0]!, data);
  assert.equal(writes, 0);
  off(); service.dispose();
});
