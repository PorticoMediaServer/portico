import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeLayoutRowView, HomeLayoutView} from '../src/home.ts';
import {HomeLayoutEditor, type HomeLayoutEditorService} from '../src/home-layout-editor.ts';

const row = (id: string, patch: Partial<HomeLayoutRowView> = {}): HomeLayoutRowView => ({id, title: id, kind: 'titles', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false, ...patch});
const view = (revision: number): HomeLayoutView => ({revision, rowOrder: ['continue', 'fixed', 'recommended', 'view:existing'], hiddenRowIds: ['recommended', 'unreadable-row'],
  rows: [row('continue', {required: true, hideable: false}), row('fixed', {reorderable: false}), row('recommended', {hidden: true}), row('view:existing')]});
const HIDDEN = ['recommended', 'unreadable-row'];

type Deferred<T> = {promise: Promise<T>; resolve: (value: T) => void; reject: (error: unknown) => void};
function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((accept, refuse) => {resolve = accept;reject = refuse;});
  return {promise, resolve, reject};
}
type Write = {kind: 'save' | 'reset'; rowOrder?: readonly string[]; hiddenRowIds?: readonly string[]; requestId: string; revision?: number};
/** Every service method is recorded; reads and writes stay unresolved until the test says so. */
function fixture(revision = 7) {
  const calls: string[] = [], writes: Write[] = [], loads: Deferred<HomeLayoutView>[] = [], saves: Deferred<unknown>[] = [], resets: Deferred<unknown>[] = [];
  const service: HomeLayoutEditorService = {
    loadLayout: (signal?: AbortSignal) => {calls.push('load:' + (signal ? 'signal' : 'none'));const read = deferred<HomeLayoutView>();loads.push(read);return read.promise;},
    saveLayout: async (rowOrder, hiddenRowIds, idempotencyKey, expectedRevision) => {calls.push('save');writes.push({kind: 'save', rowOrder, hiddenRowIds, requestId: idempotencyKey(), revision: expectedRevision});const write = deferred<unknown>();saves.push(write);return write.promise as never;},
    resetLayout: async (idempotencyKey, expectedRevision) => {calls.push('reset');writes.push({kind: 'reset', requestId: idempotencyKey(), revision: expectedRevision});const write = deferred<unknown>();resets.push(write);return write.promise as never;},
  };
  let next = 0, live = revision, alive = true;
  const editor = new HomeLayoutEditor(service, {requestId: () => 'request-' + ++next, isCurrent: () => alive});
  const ready = async () => {const loading = editor.load();loads[0]!.resolve(view(live));await loading;return editor.getSnapshot().draft!;};
  const retire = () => {alive = false;editor.dispose();};
  return {editor, calls, writes, loads, saves, resets, ready, retire, setLive: (value: number) => {live = value;}};
}

test('load, edit and save publish the draft and write the revision captured at load', async () => {
  const f = fixture(), seen: string[] = [];
  assert.equal(f.editor.getSnapshot().phase, 'loading');
  const draft = await f.ready();
  assert.deepEqual(f.calls, ['load:signal']);
  assert.equal(f.editor.getSnapshot().phase, 'ready');
  assert.equal(draft.view.revision, 7);
  f.editor.subscribe(() => seen.push(f.editor.getSnapshot().phase + ':' + (f.editor.getSnapshot().pending ?? 'idle')));
  f.editor.move('recommended', 0);
  assert.deepEqual(f.editor.getSnapshot().draft!.order, ['recommended', 'fixed', 'continue', 'view:existing']);
  assert.deepEqual(seen, ['ready:idle']);
  const saving = f.editor.save();
  assert.equal(f.editor.getSnapshot().pending, 'save');
  assert.deepEqual(f.writes, [{kind: 'save', rowOrder: ['recommended', 'continue', 'view:existing'], hiddenRowIds: HIDDEN, requestId: 'request-1', revision: 7}]);
  f.saves[0]!.resolve(undefined);
  assert.equal(await saving, true);
  assert.equal(f.editor.getSnapshot().pending, undefined);
});

test('a retried command reuses its request id; a different command takes a fresh one', async () => {
  const f = fixture();
  await f.ready();
  const first = f.editor.save();
  f.saves[0]!.reject(new Error('network'));
  assert.equal(await first, false);
  const retry = f.editor.save();
  f.saves[1]!.reject(new Error('network'));
  assert.equal(await retry, false);
  assert.deepEqual(f.writes.map(write => write.requestId), ['request-1', 'request-1']);
  // A different draft is a different command, even though the first one never succeeded.
  f.editor.move('recommended', 0);
  const changed = f.editor.save();
  f.saves[2]!.resolve(undefined);
  assert.equal(await changed, true);
  assert.equal(f.writes.at(-1)!.requestId, 'request-2');
  assert.equal(f.writes.at(-1)!.rowOrder![0], 'recommended');
  // A reset is never a retry of a save, and its own retry reuses one id.
  const resetting = f.editor.reset();
  f.resets[0]!.reject(new Error('network'));
  assert.equal(await resetting, false);
  const again = f.editor.reset();
  f.resets[1]!.reject(new Error('network'));
  assert.equal(await again, false);
  assert.deepEqual(f.writes.slice(-2).map(write => write.kind + ':' + write.requestId), ['reset:request-3', 'reset:request-3']);
  assert.equal(f.writes.at(-1)!.revision, 7);
});

test('one command at a time: a second save, a reset or an edit while a command is pending is refused', async () => {
  const f = fixture();
  const draft = await f.ready();
  const saving = f.editor.save();
  assert.equal(await f.editor.save(), false);
  assert.equal(await f.editor.reset(), false);
  f.editor.move('recommended', 0);
  assert.equal(f.editor.getSnapshot().draft, draft);
  assert.deepEqual(f.calls, ['load:signal', 'save']);
  f.saves[0]!.resolve(undefined);
  assert.equal(await saving, true);
  assert.equal(f.writes.length, 1);
});

test('a conflict keeps the draft and blocks writes until an explicit reload succeeds', async () => {
  const f = fixture();
  await f.ready();
  f.editor.move('recommended', 0);
  const conflictedSave = f.editor.save();
  f.saves[0]!.reject(Object.assign(new Error('conflict'), {code: 'home_layout_conflict'}));
  assert.equal(await conflictedSave, false);
  const state = f.editor.getSnapshot();
  assert.equal(state.conflict, true);
  assert.equal(state.phase, 'ready');
  assert.deepEqual(state.draft!.order, ['recommended', 'fixed', 'continue', 'view:existing']);
  // Repeated commands cannot clear it or reach the server again.
  assert.equal(await f.editor.save(), false);
  assert.equal(await f.editor.reset(), false);
  assert.equal(f.writes.length, 1);
  // Only a successful reload clears it, and that reload is the editor's one read.
  f.setLive(8);
  const reloading = f.editor.load();
  f.loads[1]!.resolve(view(8));
  await reloading;
  assert.equal(f.editor.getSnapshot().conflict, false);
  assert.equal(f.editor.getSnapshot().draft!.view.revision, 8);
  // A reset conflict is a conflict too, and it outlives the failed command.
  const conflictReset = f.editor.reset();
  assert.equal(f.writes.at(-1)!.kind, 'reset');
  assert.equal(f.writes.at(-1)!.revision, 8);
  f.resets[0]!.reject(Object.assign(new Error('conflict'), {code: 'home_layout_conflict'}));
  assert.equal(await conflictReset, false);
  assert.equal(f.editor.getSnapshot().conflict, true);
  assert.equal(f.editor.getSnapshot().draft!.view.revision, 8);
});

test('closing the dialog drops a late load and a late write', async () => {
  const f = fixture();
  void f.editor.load();
  const loadSeen: string[] = [];
  f.editor.subscribe(() => loadSeen.push(f.editor.getSnapshot().phase));
  f.retire();
  f.loads[0]!.resolve(view(7));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.editor.getSnapshot().draft, undefined);
  assert.deepEqual(loadSeen, []);

  const g = fixture();
  await g.ready();
  const writeSeen: string[] = [];
  g.editor.subscribe(() => writeSeen.push(g.editor.getSnapshot().pending ?? 'idle'));
  const saving = g.editor.save();
  assert.deepEqual(writeSeen, ['save']);
  g.retire();
  g.saves[0]!.resolve(undefined);
  assert.equal(await saving, false);
  assert.deepEqual(writeSeen, ['save']);
  assert.equal(g.editor.getSnapshot().pending, 'save');
});

test('the editor never reads the revision again between loading and writing', async () => {
  const f = fixture();
  await f.ready();
  f.setLive(9);
  f.editor.move('recommended', 0);
  const saving = f.editor.save();
  f.saves[0]!.resolve(undefined);
  await saving;
  const resetting = f.editor.reset();
  f.resets[0]!.resolve(undefined);
  await resetting;
  assert.deepEqual(f.calls, ['load:signal', 'save', 'reset']);
  assert.deepEqual(f.writes.map(write => write.revision), [7, 7]);
  assert.deepEqual(f.writes[0]!.rowOrder, ['recommended', 'continue', 'view:existing']);
  assert.deepEqual(f.writes[0]!.hiddenRowIds, HIDDEN);
  assert.equal(f.writes[1]!.rowOrder, undefined);
});
