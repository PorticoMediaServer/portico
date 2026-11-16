import test from 'node:test';
import assert from 'node:assert/strict';
import {deleteSurfaceFor, EntryDeleteProducer} from '../src/shell/entry-delete.ts';
import {entryActions} from '../src/shell/entry-actions.ts';

const target = {libraryId: 'library', itemId: 'item', kind: 'movie' as const, title: 'Film'};
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
// The rebuilt server: one title's preview and delete are its own routes (definition 03 rules 63 to 69).
const previewAnswer = (deleteRefusal?: string) => ({title: 'Film', titles: 1, files: 1, bytes: 2048, listEntries: 2, otherPeople: 1, alsoRemoves: [], ...(deleteRefusal ? {deleteRefusal} : {})});
const deleteAnswer = () => ({removed: true, filesDeleted: 1, bytes: 2048, kept: [], alsoRemoved: []});
type Call = {path: string; method?: string; body?: unknown};
function harness(options: {preview?: unknown; remove?: (attempt: number) => unknown; current?: () => boolean} = {}) {
  const calls: Call[] = []; let deletes = 0;
  const ports = {
    serverId: 'server', viewerId: 'viewer', isCurrent: options.current ?? (() => true), requestId: async () => 'op-1',
    api: {request: async (path: string, method?: string, body?: unknown) => {
      calls.push({path, method, body});
      if (path === '/v1/admin/items/item/deletion') return options.preview ?? previewAnswer();
      if (path === '/v1/admin/items/item?confirm=Film' && method === 'DELETE') return options.remove ? options.remove(deletes++) : deleteAnswer();
      throw new Error('unexpected path ' + path);
    }},
  };
  const producer = new EntryDeleteProducer();
  return {calls, producer, ports, deletes: () => deletes};
}
const deleteCalls = (calls: Call[]) => calls.filter(call => call.method === 'DELETE');

test('one title is previewed on its own route and confirmed by typing its name; files always go', async () => {
  const h = harness();
  h.producer.open(target, h.ports); await tick();
  const {phase, preview} = h.producer.getSnapshot();
  assert.equal(phase, 'ready');
  assert.deepEqual([preview?.confirmation, preview?.confirmationKind, preview?.totalFiles, preview?.totalBytesText, preview?.listEntries, preview?.otherPeople], ['Film', 'title', 1, '2.0 KB', 2, 1]);
  h.producer.setConfirmation('Film'); await h.producer.confirm();
  assert.deepEqual(deleteCalls(h.calls).map(call => call.path), ['/v1/admin/items/item?confirm=Film']);
  assert.deepEqual([h.producer.getSnapshot().phase, h.producer.getSnapshot().result?.removed, h.producer.getSnapshot().result?.filesRetained], ['result', 1, false]);
});

test('a replaced dialog keeps its own state and a late answer is ignored', async () => {
  let finish!: () => void; const pending = new Promise<void>(resolve => {finish = resolve;});
  const h = harness({remove: () => pending.then(() => deleteAnswer())});
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('Film');
  const first = h.producer.confirm(); await tick();
  h.producer.open({...target, itemId: 'next'}, h.ports); await tick();
  finish(); await first;
  const state = h.producer.getSnapshot();
  assert.notEqual(state.phase, 'result');
  assert.equal(state.result, undefined);
});

test('an uncertain delete is tried again for the same title', async () => {
  const h = harness({remove: attempt => { if (attempt === 0) throw new Error('network'); return deleteAnswer(); }});
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('Film');
  await h.producer.confirm();
  assert.equal(h.producer.getSnapshot().phase, 'uncertain');
  await h.producer.retry();
  assert.deepEqual(deleteCalls(h.calls).map(call => call.path), ['/v1/admin/items/item?confirm=Film', '/v1/admin/items/item?confirm=Film']);
  assert.equal(h.producer.getSnapshot().phase, 'result');
});

test('a title that cannot be deleted says why and sends no delete; a wrong name sends none either', async () => {
  const blocked = harness({preview: previewAnswer('Portico can\'t delete files on remote storage')});
  blocked.producer.open(target, blocked.ports); await tick();
  assert.equal(blocked.producer.getSnapshot().phase, 'blocked');
  assert.equal(blocked.producer.getSnapshot().preview?.refusal, 'Portico can\'t delete files on remote storage');
  blocked.producer.setConfirmation('Film'); await blocked.producer.confirm();
  assert.equal(deleteCalls(blocked.calls).length, 0);
  const mismatch = harness();
  mismatch.producer.open(target, mismatch.ports); await tick(); mismatch.producer.setConfirmation('WRONG');
  await mismatch.producer.confirm();
  assert.equal(deleteCalls(mismatch.calls).length, 0);
  assert.equal(mismatch.producer.getSnapshot().errorCode, 'delete_not_ready');
});

test('two confirms send one delete', async () => {
  const h = harness();
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('Film');
  await Promise.all([h.producer.confirm(), h.producer.confirm()]);
  assert.equal(deleteCalls(h.calls).length, 1);
});

test('a retired scope opens no surface and sends no preview or delete', async () => {
  const retired = harness({current: () => false});
  entryActions.openDelete(target, null, retired.ports);
  assert.equal(entryActions.getSnapshot(), null);
  retired.producer.open(target, retired.ports); await tick();
  assert.equal(retired.producer.getSnapshot().phase, 'idle');
  assert.equal(retired.calls.length, 0);
});

test('the uncertain phase maps to the approved retry surface', () => {
  assert.equal(deleteSurfaceFor('uncertain'), 'uncertain');
  assert.equal(deleteSurfaceFor('ready'), 'dialog');
  assert.equal(deleteSurfaceFor('idle'), 'none');
});
