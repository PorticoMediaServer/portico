import test from 'node:test';
import assert from 'node:assert/strict';
import {deleteSurfaceFor, EntryDeleteProducer} from '../src/shell/entry-delete.ts';
import {entryActions} from '../src/shell/entry-actions.ts';

const target = {libraryId: 'library', itemId: 'item', kind: 'movie' as const, title: 'Film'};
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
const previewAnswer = (itemId = 'item', blocked: string[] = []) => ({protocolVersion: '1.0', serverId: 'server', result: {
  revision: 1, confirmation: 'DELETE', confirmationKind: 'title', totalBytes: 0, totalFiles: 0, totalBytesText: '0 B', blocked,
  targets: [{itemId, title: 'Film', kind: 'movie', libraryId: 'library', libraryName: 'Movies', allowMediaDeletion: true, titles: 1, bytes: 0, files: [], dependents: []}],
}});
const deleteAnswer = (itemId = 'item') => ({protocolVersion: '1.0', serverId: 'server', result: {
  revision: 2, removed: 1, failed: 0, bytesDeleted: 0, filesRetained: false,
  receipts: [{itemId, title: 'Film', removed: true, filesDeleted: 0, filesKept: 0, bytes: 0, code: ''}],
}});
type Call = {path: string; body?: unknown};
function harness(options: {preview?: unknown; remove?: (attempt: number) => unknown; current?: () => boolean; requestId?: () => Promise<string>} = {}) {
  const calls: Call[] = []; let deletes = 0;
  const ports = {
    serverId: 'server', viewerId: 'viewer', isCurrent: options.current ?? (() => true),
    requestId: options.requestId ?? (async () => 'op-1'),
    api: {request: async (path: string, _method?: string, body?: unknown) => {
      calls.push({path, body});
      if (path.endsWith('/delete/preview')) return options.preview ?? previewAnswer();
      if (path.endsWith('/delete')) return options.remove ? options.remove(deletes++) : deleteAnswer();
      throw new Error('unexpected path ' + path);
    }},
  };
  const producer = new EntryDeleteProducer();
  return {calls, producer, ports, deletes: () => deletes};
}
const deleteCalls = (calls: Call[]) => calls.filter(call => call.path.endsWith('/delete'));

test('a retired dialog cannot delete while the operation id is pending', async () => {
  let release!: (value: string) => void; const pending = new Promise<string>(resolve => {release = resolve;});
  const h = harness({requestId: () => pending});
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('DELETE');
  const confirming = h.producer.confirm(); h.producer.close(); release('op-1'); await confirming;
  assert.equal(deleteCalls(h.calls).length, 0);
});

test('a replaced dialog keeps its own state and a late answer is ignored', async () => {
  let finish!: () => void; const pending = new Promise<void>(resolve => {finish = resolve;});
  const h = harness({remove: () => pending.then(() => deleteAnswer())});
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('DELETE');
  const first = h.producer.confirm(); await tick();
  h.producer.open({...target, itemId: 'next'}, h.ports); await tick();
  finish(); await first;
  const state = h.producer.getSnapshot();
  assert.notEqual(state.phase, 'result');
  assert.equal(state.result, undefined);
});

test('an uncertain delete retries with the same immutable body and operation id', async () => {
  const h = harness({remove: attempt => { if (attempt === 0) throw new Error('network'); return deleteAnswer(); }});
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('DELETE');
  await h.producer.confirm();
  assert.equal(h.producer.getSnapshot().phase, 'uncertain');
  await h.producer.retry();
  const sent = deleteCalls(h.calls).map(call => call.body);
  assert.equal(sent.length, 2);
  assert.deepEqual(sent[0], sent[1]);
  assert.equal((sent[0] as {operationId: string}).operationId, 'op-1');
  assert.equal(h.producer.getSnapshot().phase, 'result');
});

test('a receipt for another title is never applied', async () => {
  const h = harness({remove: () => deleteAnswer('other')});
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('DELETE');
  await h.producer.confirm();
  assert.equal(h.producer.getSnapshot().phase, 'uncertain');
  assert.equal(h.producer.getSnapshot().result, undefined);
});

test('blocked and mismatched confirmations send no delete', async () => {
  const blocked = harness({preview: previewAnswer('item', ['library'])});
  blocked.producer.open(target, blocked.ports); await tick();
  assert.equal(blocked.producer.getSnapshot().phase, 'blocked');
  blocked.producer.setConfirmation('DELETE'); await blocked.producer.confirm();
  assert.equal(deleteCalls(blocked.calls).length, 0);
  const mismatch = harness();
  mismatch.producer.open(target, mismatch.ports); await tick(); mismatch.producer.setConfirmation('WRONG');
  await mismatch.producer.confirm();
  assert.equal(deleteCalls(mismatch.calls).length, 0);
  assert.equal(mismatch.producer.getSnapshot().errorCode, 'delete_not_ready');
});

test('a preview for another title is rejected without a ready dialog', async () => {
  const h = harness({preview: previewAnswer('other')});
  h.producer.open(target, h.ports); await tick();
  assert.equal(h.producer.getSnapshot().phase, 'error');
  assert.equal(h.producer.getSnapshot().preview, undefined);
});

test('two confirms while the operation id is pending allocate one body and send once', async () => {
  let release!: (value: string) => void; const pending = new Promise<string>(resolve => {release = resolve;});
  const h = harness({requestId: () => pending});
  h.producer.open(target, h.ports); await tick(); h.producer.setConfirmation('DELETE');
  const first = h.producer.confirm(), second = h.producer.confirm();
  assert.equal(h.producer.getSnapshot().phase, 'confirming');
  release('op-1'); await Promise.all([first, second]);
  const sent = deleteCalls(h.calls);
  assert.equal(sent.length, 1);
  assert.equal((sent[0]!.body as {operationId: string}).operationId, 'op-1');
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
