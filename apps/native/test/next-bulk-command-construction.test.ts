import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeBulkMedia} from '../src-next/screens/destinations/bulk-media.ts';
import type {CatalogueSelectionOptions} from '../../../packages/client-core/src/catalogue-selection.ts';

const entries = ['a', 'b'].map(id => ({kind: 'movie' as const, id, libraryId: 'library', title: id}));
const schema = ['studio', 'tags', 'genres'].map(field => ({field, label: field, group: 'general', type: field === 'studio' ? 'text' : 'list', bulk: true}));
const changes = {studio: {value: 'New Studio'}, tags: {add: [' Noir '], remove: ['Old']}, genres: {add: ['Drama'], remove: []}};
type Call = {path: string; method?: string; body: any; signal?: AbortSignal};
function fixture(options: {failMetadataOnce?: boolean; partialMetadataOnce?: boolean; failDeleteOnce?: boolean; retireDuringId?: boolean} = {}) {
  const calls: Call[] = [], life = new AbortController(); let ids = 0, previewReads = 0, metadataWrites = 0, deleteWrites = 0;
  let bulk!: NativeBulkMedia;
  const api = {async request<T>(path: string, method?: string, body?: any, signal?: AbortSignal): Promise<T> {
    calls.push({path, method, body, signal});
    if (path === '/v1/metadata/bulk/preview') {
      previewReads++;
      const count = body.targets.length;
      return {variant: 'ready', serverId: 'server', viewerFence: 'a'.repeat(64), contentKind: 'movie', kind: 'item', total: count,
        schema, singleItemFields: [], excludedFields: [], fields: Object.fromEntries(schema.map(spec => [spec.field,
          {state: 'shared', distinctCount: 1, value: 'Old', values: [{value: 'Old', count}], entries: [], locked: 0, truncated: false}])),
        targets: body.targets.map((target: any) => ({kind: target.kind, id: target.id, libraryId: target.libraryId, expectedRevision: (previewReads === 1 ? 'b' : 'e').repeat(64)}))} as T;
    }
    if (path === '/v1/metadata/bulk') {
      metadataWrites++;
      if (options.failMetadataOnce && metadataWrites === 1) throw new Error('unknown metadata outcome');
      const results = body.targets.map((target: any, index: number) => options.partialMetadataOnce && metadataWrites === 1 && index === 0
        ? {kind: target.kind, id: target.id, ok: false, code: 'metadata_conflict', message: 'Changed'}
        : {kind: target.kind, id: target.id, ok: true, revision: 'c'.repeat(64)});
      return {operationId: body.operationId, results, updated: results.filter((row: any) => row.ok).length, failed: results.filter((row: any) => !row.ok).length} as T;
    }
    if (path === '/v1/admin/media/delete/preview') return {protocolVersion: '1.0', serverId: 'server', result: {
      revision: 7, confirmation: '2', confirmationKind: 'count', totalBytes: 0, totalFiles: 0, totalBytesText: '0 B', blocked: [],
      targets: entries.map(entry => ({itemId: entry.id, title: entry.title, kind: entry.kind, libraryId: entry.libraryId, libraryName: 'Library',
        allowMediaDeletion: true, titles: 1, bytes: 0, files: [], dependents: []}))}} as T;
    if (path === '/v1/admin/media/delete') {
      deleteWrites++;
      if (options.failDeleteOnce && deleteWrites === 1) throw new Error('unknown delete outcome');
      return {protocolVersion: '1.0', serverId: 'server', result: {revision: 8, removed: 2, failed: 0, bytesDeleted: 0, filesRetained: true,
        receipts: entries.map(entry => ({itemId: entry.id, title: entry.title, removed: true, filesDeleted: 0, filesKept: 0, bytes: 0}))}} as T;
    }
    throw new Error(`unexpected ${method} ${path}`);
  }};
  bulk = new NativeBulkMedia({api, scope: {serverId: 'server', viewerId: 'owner'}, signal: life.signal, isCurrent: () => true,
    requestId: async () => {ids++; if (options.retireDuringId) bulk.close(); return `operation-${ids}`;}} as unknown as CatalogueSelectionOptions, entries);
  return {bulk, calls, ids: () => ids, writes: () => calls.filter(call => call.path === '/v1/metadata/bulk'), deletions: () => calls.filter(call => call.path === '/v1/admin/media/delete')};
}

test('native metadata retains the exact core-built body and operation ID after an unknown outcome', async () => {
  const f = fixture({failMetadataOnce: true});
  try {
    await f.bulk.openEditor(); assert.equal(f.bulk.getSnapshot().phase, 'ready');
    f.bulk.setChanges(changes); f.bulk.setLockEdited(false);
    await f.bulk.save(); assert.equal(f.bulk.getSnapshot().phase, 'uncertain');
    f.bulk.setChanges({studio: {value: 'must not replace retained command'}});
    await f.bulk.save(); assert.equal(f.bulk.getSnapshot().phase, 'result');
    const writes = f.writes(); assert.equal(writes.length, 2); assert.equal(f.ids(), 1);
    assert.equal(writes[0].body, writes[1].body);
    assert.deepEqual(writes[0].body, {fields: {studio: {value: 'New Studio'}}, lists: {tags: {add: [' Noir '], remove: ['Old']}},
      genres: {add: ['Drama'], remove: []}, lockEdited: false, operationId: 'operation-1',
      targets: entries.map(entry => ({kind: 'item', id: entry.id, expectedRevision: 'b'.repeat(64)}))});
    assert.ok(writes.every(call => call.method === 'POST' && !call.signal!.aborted));
  } finally {f.bulk.dispose();}
});

test('native failed-leaf retry refreshes only failed revisions and preserves its captured changes', async () => {
  const f = fixture({partialMetadataOnce: true});
  try {
    await f.bulk.openEditor(); f.bulk.setChanges(changes); await f.bulk.save();
    await f.bulk.retry(['a']);
    const writes = f.writes(); assert.equal(writes.length, 2); assert.equal(f.ids(), 2);
    assert.deepEqual(writes[1].body.targets, [{kind: 'item', id: 'a', expectedRevision: 'e'.repeat(64)}]);
    assert.deepEqual(writes[1].body.fields, writes[0].body.fields); assert.deepEqual(writes[1].body.lists, writes[0].body.lists);
    assert.equal(writes[1].body.operationId, 'operation-2');
    const reads = f.calls.filter(call => call.path === '/v1/metadata/bulk/preview');
    assert.equal(reads.length, 2); assert.deepEqual(reads[1].body.targets.map((target: any) => target.id), ['a']);
  } finally {f.bulk.dispose();}
});

test('native deletion retains its original confirmation, file choice, revision and ID across uncertainty', async () => {
  const f = fixture({failDeleteOnce: true});
  try {
    await f.bulk.openDelete(); f.bulk.setDeletion({confirmation: ' 2 ', removeFiles: false});
    await f.bulk.deleteNow(); assert.equal(f.bulk.getSnapshot().deletion?.phase, 'uncertain');
    f.bulk.setDeletion({confirmation: '2', removeFiles: true});
    await f.bulk.deleteNow(); assert.equal(f.bulk.getSnapshot().deletion?.phase, 'result');
    const writes = f.deletions(); assert.equal(writes.length, 2); assert.equal(f.ids(), 1);
    assert.equal(writes[0].body, writes[1].body); assert.equal(Object.isFrozen(writes[0].body), true);
    assert.deepEqual(writes[0].body, {itemIds: ['a', 'b'], expectedRevision: 7, confirmation: ' 2 ', deleteFiles: false, operationId: 'operation-1'});
    assert.deepEqual(f.calls.find(call => call.path.endsWith('/delete/preview'))!.body, {itemIds: ['a', 'b']});
  } finally {f.bulk.dispose();}
});

test('native no-change saves mint no IDs and retirement while minting refuses the pending write', async () => {
  const empty = fixture();
  try {await empty.bulk.openEditor(); await empty.bulk.save(); assert.equal(empty.ids(), 0); assert.equal(empty.writes().length, 0);}
  finally {empty.bulk.dispose();}
  const retired = fixture({retireDuringId: true});
  try {
    await retired.bulk.openEditor(); retired.bulk.setChanges(changes); await retired.bulk.save();
    assert.equal(retired.ids(), 1); assert.equal(retired.writes().length, 0); assert.equal(retired.bulk.getSnapshot().phase, 'idle');
  } finally {retired.bulk.dispose();}
});
