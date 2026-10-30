import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryInventoryService} from '../src/library-inventory.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};
const wire = {serverId: 'server', viewerFence: 'fence'};
const source = (id: string, more: Record<string, unknown> = {}) => ({id, libraryId: 'library', kind: 'local', name: 'Folder ' + id, path: '/media/' + id, resolvedPath: '/media/' + id, classification: 'local', generation: 1, health: 'healthy', enabled: true, followSymlinks: false, identityConfirmed: true, missingGraceSeconds: 0, intervalMs: 0, nextScanAt: '', lastCompleteAt: '', lastProgressAt: '', ...more});
const object = (id: string, more: Record<string, unknown> = {}) => ({id, sourceId: 'a', assetId: 'asset-' + id, itemId: '', title: 'File ' + id, relativePath: id + '.mkv', revision: 'r1', state: 'available', size: 1, modifiedNs: 1, missingSince: '', analysisState: '', analysisError: '', unsupportedReason: '', ...more});

test('a folder or file that cannot be read is left out alone, and a scan step this app does not know is not shown', async () => {
  const service = new LibraryInventoryService({scope, libraryId: 'library', api: {request: async <T>(path: string) => (path.endsWith('inventory-config')
    ? {scope: wire, libraryId: 'library', revision: 1, policy: {tier: 'custom', revision: 3, operations: ['probe', 'divine', 'probe']}, sources: [source('a'), {id: 'broken'}, source('a', {name: 'Again'}), source('b')], future: true}
    : {scope: wire, libraryId: 'library', sourceId: '', revision: 1, nextCursor: '', items: [object('one'), 'nope', object('one'), object('two', {size: -1}), object('three')]}) as T}});
  await service.refresh();
  const state = service.getSnapshot();
  assert.equal(state.error, null);
  assert.deepEqual(state.config!.sources.map(s => [s.id, s.name]), [['a', 'Folder a'], ['b', 'Folder b']]);
  assert.equal(state.config!.policy.tier, 'custom');
  assert.deepEqual(state.config!.policy.operations, ['probe']);
  assert.deepEqual(state.objects!.items.map(i => i.id), ['one', 'three']);
  service.dispose();
});
