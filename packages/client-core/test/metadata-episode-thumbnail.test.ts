import assert from 'node:assert/strict';
import test from 'node:test';
import {MetadataRepairService} from '../src/metadata-repair.ts';

const scope = {serverId: 'server', viewerId: 'owner'}, target = {kind: 'item' as const, id: 'episode', libraryId: 'library'};
function document(available = true) {
  return {serverId: scope.serverId, viewerFence: 'a'.repeat(64), target: {kind: 'item', id: target.id}, libraryId: target.libraryId, revision: 'b'.repeat(64),
    ...(available ? {canMakeEpisodeThumbnail: true} : {}), schema: [], artworkRoles: ['still', 'thumbnail'],
    snapshot: {fields: {}, relationshipLocks: {}, relationships: [], artwork: []}, candidates: [], history: [], artwork: {candidates: [], jobs: []}, cascades: []};
}

test('explicit episode picture carries current CAS and requires the offered capability', async () => {
  for (const available of [false, true]) {
    const writes: unknown[] = [];
    const service = new MetadataRepairService({scope, api: {async request<T>(_path: string, method?: string, body?: unknown) {
      if (method === 'POST') writes.push(body);
      return document(available) as T;
    }}});
    await service.load(target);
    const data = service.getSnapshot().data!;
    if (available) {
      assert.equal(await service.command({action: 'make_episode_thumbnail'}, data), true);
      assert.deepEqual(writes, [{action: 'make_episode_thumbnail', expectedRevision: data.revision}]);
    } else {
      await assert.rejects(service.command({action: 'make_episode_thumbnail'}, data));
      assert.deepEqual(writes, []);
    }
    service.dispose();
  }
});

test('replaced repair data and synchronous saving retirement cannot generate a picture', async () => {
  let writes = 0;
  const service = new MetadataRepairService({scope, api: {async request<T>(_path: string, method?: string) {
    if (method === 'POST') writes++;
    return document() as T;
  }}});
  await service.load(target);
  const old = service.getSnapshot().data!;
  await service.load(target);
  await assert.rejects(service.command({action: 'make_episode_thumbnail'}, old));
  const data = service.getSnapshot().data!;
  const off = service.subscribe(() => {if (service.getSnapshot().phase === 'saving') service.cancel();});
  assert.equal(await service.command({action: 'make_episode_thumbnail'}, data), false);
  assert.equal(writes, 0);
  off(); service.dispose();
});
