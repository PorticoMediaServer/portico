import test from 'node:test';
import assert from 'node:assert/strict';
import {createSelectionEditor} from '../src/screens/browse/selection-editor.ts';
import {LibraryDeleteProducer} from '../src/screens/browse/selection-delete.ts';

const tick = () => new Promise(resolve => setImmediate(resolve));
const draft = {fields: {contentRating: 'G'}, lists: {tags: {add: [' X '], remove: []}, genres: {add: ['Drama'], remove: []}}, lockEdited: false};
function editing(count: number, options: {loseTail?: boolean; retireOnSecondId?: boolean} = {}) {
  const events: string[] = [], writes: any[] = []; let ids = 0, live = true;
  const targets = Array.from({length: count}, (_, index) => ({kind: 'item', id: `m${index}`, libraryId: 'library', expectedRevision: 'b'.repeat(64)}));
  const picks = targets.map(target => ({key: target.id, item: {kind: 'movie', id: target.id, libraryId: target.libraryId, capabilities: {edit: true}}}));
  const operation = {target: {mode: 'explicit', context: {}, items: picks, displayItems: picks}} as never;
  const preview = {variant: 'ready', serverId: 'server', viewerFence: 'a'.repeat(64), contentKind: 'movie', kind: 'item', total: count,
    schema: ['contentRating', 'tags', 'genres'].map(field => ({field, label: field, group: 'general', type: field === 'contentRating' ? 'text' : 'list', bulk: true})),
    singleItemFields: [], excludedFields: [], fields: Object.fromEntries(['contentRating', 'tags', 'genres'].map(field => [field,
      {state: 'shared', distinctCount: 1, value: 'Old', values: [{value: 'Old', count}], entries: [], locked: 0, truncated: false}])), targets};
  const api = {async request(path: string, method?: string, body?: any) {
    if (path.endsWith('/preview')) return preview;
    assert.equal(path, '/v1/metadata/bulk'); assert.equal(method, 'POST'); writes.push(body); events.push(`write-${body.operationId}`);
    if (options.loseTail && writes.length === 2) throw new Error('lost tail response');
    return {operationId: body.operationId, results: body.targets.map((target: any) => ({kind: target.kind, id: target.id, ok: true, revision: 'c'.repeat(64)})), updated: body.targets.length, failed: 0};
  }};
  const editor = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => {
    ids++; events.push(`id-${ids}`); if (options.retireOnSecondId && ids === 2) live = false; return `operation-${ids}`;
  }, isCurrent: () => live, serverId: 'server'});
  return {editor, operation, targets, writes, events, ids: () => ids};
}

test('web bulk keeps command bytes and sequential IDs across 200+1 targets and an uncertain tail', async () => {
  const f = editing(201, {loseTail: true}), preview = await f.editor.preview(f.operation);
  const first = await Promise.allSettled([f.editor.save(f.operation, preview, draft), f.editor.save(f.operation, preview, draft)]);
  assert.ok(first.every(result => result.status === 'rejected'));
  assert.deepEqual(f.events, ['id-1', 'id-2', 'write-operation-1', 'write-operation-2']);
  assert.equal(f.ids(), 2); assert.equal(f.writes.length, 2);
  for (let index = 0; index < 2; index++) {
    const expected = {operationId: `operation-${index + 1}`, targets: f.targets.slice(index * 200, (index + 1) * 200).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision})),
      fields: {contentRating: {value: 'G'}}, lists: {tags: {add: [' X '], remove: []}}, genres: {add: ['Drama'], remove: []}, lockEdited: false};
    assert.equal(JSON.stringify(f.writes[index]), JSON.stringify(expected));
  }
  const receipts = await f.editor.save(f.operation, preview, draft);
  assert.deepEqual(receipts.map(receipt => receipt.updated), [200, 1]);
  assert.equal(f.ids(), 2); assert.equal(f.writes.length, 3); assert.equal(f.writes[1], f.writes[2]);
});

test('web bulk retirement during ID allocation sends no body', async () => {
  const f = editing(201, {retireOnSecondId: true}), preview = await f.editor.preview(f.operation);
  await assert.rejects(f.editor.save(f.operation, preview, draft), /cancelled/);
  assert.equal(f.ids(), 2); assert.equal(f.writes.length, 0);
});

function deletion(requestId?: () => Promise<string>) {
  const targets = ['first', 'second'].map(itemId => ({itemId, libraryId: 'library', kind: 'movie' as const}));
  const writes: any[] = []; let ids = 0;
  const producer = new LibraryDeleteProducer();
  const ports = {serverId: 'server', viewerId: 'viewer', isCurrent: () => true,
    requestId: async () => {ids++; return requestId ? requestId() : 'delete-1';},
    api: {async request(path: string, method?: string, body?: any) {
      assert.equal(method, 'POST');
      if (path.endsWith('/preview')) return {protocolVersion: '1.0', serverId: 'server', result: {
        revision: 17, confirmation: '2', confirmationKind: 'count', totalBytes: 0, totalFiles: 0, totalBytesText: '0 B', blocked: [],
        targets: [...targets].reverse().map(target => ({...target, title: target.itemId, libraryName: 'Movies', allowMediaDeletion: true, titles: 1, bytes: 0, files: [], dependents: []})),
      }};
      assert.equal(path, '/v1/admin/media/delete'); writes.push(body);
      if (writes.length === 1) throw new Error('unknown deletion outcome');
      return {protocolVersion: '1.0', serverId: 'server', result: {revision: 18, removed: 2, failed: 0, bytesDeleted: 0, filesRetained: true,
        receipts: targets.map(target => ({itemId: target.itemId, title: target.itemId, removed: true, filesDeleted: 0, filesKept: 0, bytes: 0}))}};
    }}};
  producer.open(targets, ports as never);
  return {producer, writes, ids: () => ids};
}

test('web deletion keeps selected order, frozen body and original ID after a lost response', async () => {
  const f = deletion();
  try {
    await tick(); assert.equal(f.producer.getSnapshot().phase, 'ready');
    f.producer.setConfirmation(' 2 '); await f.producer.confirm(); assert.equal(f.writes.length, 0); assert.equal(f.ids(), 0);
    f.producer.setConfirmation('2'); f.producer.setRemoveFiles(false); await f.producer.confirm();
    assert.equal(f.producer.getSnapshot().phase, 'uncertain'); assert.equal(f.writes.length, 1);
    assert.equal(JSON.stringify(f.writes[0]), JSON.stringify({itemIds: ['first', 'second'], expectedRevision: 17, confirmation: '2', deleteFiles: false, operationId: 'delete-1'}));
    assert.equal(Object.isFrozen(f.writes[0]), true); assert.equal(Object.isFrozen(f.writes[0].itemIds), true);
    await f.producer.retry(); assert.equal(f.producer.getSnapshot().phase, 'result');
    assert.equal(f.ids(), 1); assert.equal(f.writes.length, 2); assert.equal(f.writes[0], f.writes[1]);
  } finally {f.producer.close();}
});

test('web deletion double confirm mints once and retirement while minting dispatches nothing', async () => {
  let release!: (id: string) => void;
  const pending = new Promise<string>(resolve => {release = resolve;}), f = deletion(() => pending);
  try {
    await tick(); f.producer.setConfirmation('2');
    const first = f.producer.confirm(), second = f.producer.confirm();
    assert.equal(f.ids(), 1); f.producer.close(); release('delete-1'); await Promise.all([first, second]);
    assert.equal(f.writes.length, 0); assert.equal(f.producer.getSnapshot().phase, 'idle');
  } finally {f.producer.close();}
});
