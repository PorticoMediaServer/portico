import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryInventoryService} from '../src/library-inventory.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};
const wire = {serverId: 'server', viewerFence: 'fence'};
const source = (id: string, more: Record<string, unknown> = {}) => ({id, libraryId: 'library', kind: 'local', name: 'Folder ' + id, path: '/media/' + id, resolvedPath: '/media/' + id, classification: 'local', generation: 1, health: 'healthy', enabled: true, followSymlinks: false, identityConfirmed: true, missingGraceSeconds: 0, intervalMs: 0, nextScanAt: '', lastCompleteAt: '', lastProgressAt: '', ...more});
const object = (id: string, more: Record<string, unknown> = {}) => ({id, sourceId: 'a', assetId: 'asset-' + id, itemId: '', title: 'File ' + id, relativePath: id + '.mkv', revision: 'r1', state: 'available', size: 1, modifiedNs: 1, missingSince: '', analysisState: '', analysisError: '', unsupportedReason: '', ...more});

test('a folder or file that cannot be read is left out alone, and a scan step this app does not know is kept, each once', async () => {
  const service = new LibraryInventoryService({scope, libraryId: 'library', api: {request: async <T>(path: string) => (path.endsWith('inventory-config')
    ? {scope: wire, libraryId: 'library', revision: 1, policy: {tier: 'custom', revision: 3, operations: ['probe', 'divine', 'probe']}, sources: [source('a'), {id: 'broken'}, source('a', {name: 'Again'}), source('b')], future: true}
    : {scope: wire, libraryId: 'library', sourceId: '', revision: 1, nextCursor: '', items: [object('one'), 'nope', object('one'), object('two', {size: -1}), object('three')]}) as T}});
  await service.refresh();
  const state = service.getSnapshot();
  assert.equal(state.error, null);
  assert.deepEqual(state.config!.sources.map(s => [s.id, s.name]), [['a', 'Folder a'], ['b', 'Folder b']]);
  assert.equal(state.config!.policy.tier, 'custom');
  assert.deepEqual(state.config!.policy.operations, ['probe', 'divine'], 'a newer server\'s operation survives (#757)');
  assert.deepEqual(state.objects!.items.map(i => i.id), ['one', 'three']);
  service.dispose();
});

// #757: a policy read and written back must not lose an operation this client has no name for (a read-modify-write
// would otherwise drop it from the library). Known ids include the server's playback_frames, sonic and episode_thumbnail.
test('a scan policy holding operations this client does not know round-trips unchanged through setPolicy', async () => {
  const puts: any[] = [];
  const operations = ['probe', 'playback_frames', 'sonic', 'episode_thumbnail', 'some_future_step'];
  const service = new LibraryInventoryService({scope, libraryId: 'library', api: {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    if (method === 'PUT') {puts.push(body); return {scope: wire, libraryId: 'library', revision: 2, policy: {tier: 'custom', revision: 4, operations}, sources: [source('a')]} as T;}
    return (path.endsWith('inventory-config')
      ? {scope: wire, libraryId: 'library', revision: 1, policy: {tier: 'custom', revision: 3, operations: [...operations, 'Bad Id!']}, sources: [source('a')]}
      : {scope: wire, libraryId: 'library', sourceId: '', revision: 1, nextCursor: '', items: []}) as T;
  }}});
  await service.refresh();
  const policy = service.getSnapshot().config!.policy;
  assert.deepEqual(policy.operations, operations, 'every well-formed id, in the server order; only the malformed one is left out');
  await service.setPolicy({tier: policy.tier, operations: policy.operations, expectedRevision: policy.revision}).catch(() => {});
  assert.equal(puts.length, 1);
  assert.deepEqual(puts[0].operations, operations, 'written back exactly as read');
  assert.throws(() => service.setPolicy({tier: 'custom', operations: ['probe', 'Bad Id!'], expectedRevision: 3}), /Invalid inventory/);
  service.dispose();
});
