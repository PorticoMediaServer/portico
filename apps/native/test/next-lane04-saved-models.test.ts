import test from 'node:test';
import assert from 'node:assert/strict';
import {createSavedReorderGesture, savedNearEnd, savedReorderTarget} from '../src-next/screens/saved/paging.ts';
import {createDownloadInventory, createDownloadViewport, downloadWindow} from '../src-next/screens/downloads/inventory.ts';

test('Saved prefetch measures about ten cards in shelf, list and multi-column grids', () => {
  assert.equal(savedNearEnd(0, 400, 2000, 120), false);
  assert.equal(savedNearEnd(400, 400, 2000, 120), true);
  assert.equal(savedNearEnd(0, 600, 1600, 250, 3), true);
  assert.equal(savedNearEnd(0, 600, 1601, 250, 3), false);
  assert.equal(savedNearEnd(100, 0, 1000, 80), false);
  assert.equal(savedNearEnd(100, 600, 1000, 0), false);
  assert.equal(savedNearEnd(Number.NaN, 600, 1000, 80), false);
  assert.equal(savedNearEnd(-40, 600, 1400, 80), true, 'bounce never subtracts from the viewport');
});

test('Reorder targets preserve occurrence IDs, bound to loaded neighbors and reject no movement', () => {
  const ids = ['occurrence-a', 'occurrence-b', 'occurrence-c', 'occurrence-d'];
  assert.equal(savedReorderTarget(ids, ids[1]!, 30, 80), undefined);
  assert.deepEqual(savedReorderTarget(ids, ids[1]!, -80, 80), {index: 0, afterEntryId: ''});
  assert.deepEqual(savedReorderTarget(ids, ids[1]!, 80, 80), {index: 2, afterEntryId: ids[2]});
  assert.deepEqual(savedReorderTarget(ids, ids[1]!, 8000, 80), {index: 3, afterEntryId: ids[3]});
  assert.deepEqual(savedReorderTarget(ids, ids[2]!, -8000, 80), {index: 0, afterEntryId: ''});
  assert.equal(savedReorderTarget(ids, 'missing', 80, 80), undefined);
  assert.equal(savedReorderTarget(ids, ids[1]!, Number.NaN, 80), undefined);
  assert.equal(savedReorderTarget(ids, ids[1]!, 80, 0), undefined);
});

type Entry = Readonly<{id: string; owner: string; group: string; bytes: number; title: string}>;
test('Progress changes invalidate only the matching download row and never rebuild grouping', () => {
  let entries: readonly Entry[] = Array.from({length: 10000}, (_, index) => ({id: String(index), owner: 'viewer-a', group: String(index % 7), bytes: 0, title: `Entry ${index}`}));
  let snapshot = {entries}, groups = 0, notices = 0;
  const listeners = new Set<() => void>();
  const source = {getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const inventory = createDownloadInventory(source, {identity: entry => `${entry.owner}:${entry.id}`, membershipKey: entry => entry.group,
    project: values => {groups++; return values.map(entry => `${entry.owner}:${entry.id}`);}});
  const membership = inventory.getMembership(), untouched = inventory.getEntry('viewer-a:1'), transfer = inventory.getEntry('viewer-a:2');
  const stop = inventory.subscribe(() => notices++);
  entries = entries.map(entry => ({...entry, bytes: entry.id === '2' ? 50 : entry.bytes})); snapshot = {entries}; listeners.forEach(listener => listener());
  assert.equal(notices, 1);
  assert.equal(inventory.getMembership(), membership);
  assert.equal(groups, 1);
  assert.equal(inventory.getEntry('viewer-a:1'), untouched, 'bridge-created equivalent entry objects retain the selected snapshot');
  assert.notEqual(inventory.getEntry('viewer-a:2'), transfer);
  assert.equal(inventory.getEntry('viewer-a:2')?.entry.bytes, 50);
  entries = entries.map(entry => entry.id === '2' ? {...entry, group: 'other'} : entry); snapshot = {entries};
  assert.notEqual(inventory.getMembership(), membership); assert.equal(groups, 2);
  stop(); assert.equal(listeners.size, 0);
});

test('Download identity replacement, removal and playability fences retire the old row', () => {
  let snapshot = {entries: [{id: 'same-local-id', owner: 'viewer-a', group: 'films', bytes: 1, title: 'A'}] as readonly Entry[]};
  let permitted = true;
  const inventory = createDownloadInventory({getSnapshot: () => snapshot, subscribe: () => () => {}}, {
    identity: entry => `${entry.owner}:${entry.id}`, membershipKey: entry => entry.group, entryFence: () => permitted,
    project: entries => entries.map(entry => `${entry.owner}:${entry.id}`),
  });
  const first = inventory.getEntry('viewer-a:same-local-id');
  permitted = false;
  assert.notEqual(inventory.getEntry('viewer-a:same-local-id'), first, 'native playability changes notify a mounted row even if its entry is unchanged');
  snapshot = {entries: [{...snapshot.entries[0]!, owner: 'viewer-b'}]};
  assert.equal(inventory.getEntry('viewer-a:same-local-id'), undefined);
  assert.deepEqual(inventory.getMembership(), ['viewer-b:same-local-id']);
  snapshot = {entries: []}; assert.equal(inventory.getEntry('viewer-b:same-local-id'), undefined); assert.deepEqual(inventory.getMembership(), []);
});

test('Window bounds stay small for a large inventory and include the actual scrolled interval', () => {
  const offsets = Array.from({length: 100001}, (_, index) => index * 88);
  const first = downloadWindow(offsets, 0, 800), middle = downloadWindow(offsets, 400000, 800), last = downloadWindow(offsets, offsets.at(-1)! - 800, 800);
  for (const range of [first, middle, last]) {assert.ok(range.start >= 0); assert.ok(range.end <= 100000); assert.ok(range.end - range.start <= 30);}
  assert.equal(first.start, 0); assert.equal(last.end, 100000);
  assert.ok(offsets[middle.start]! <= 400000); assert.ok(offsets[middle.end]! >= 400800);
  assert.deepEqual(downloadWindow([0], 0, 800), {start: 0, end: 0});
  assert.deepEqual(downloadWindow([0, 87, 217, 304], 90, 80), {start: 0, end: 3}, 'measured larger rows keep their actual extent');
});

test('Viewport notifications change only for real finite scroll/layout facts', () => {
  const viewport = createDownloadViewport(); let calls = 0;
  const stop = viewport.subscribe(() => calls++);
  viewport.update({offset: 0, height: 800}); const first = viewport.getSnapshot();
  viewport.update({offset: 0, height: 800}); assert.equal(viewport.getSnapshot(), first); assert.equal(calls, 1);
  viewport.update({offset: Number.NaN, height: 800}); assert.equal(viewport.getSnapshot(), first);
  viewport.update({offset: 80, height: 800}); assert.equal(calls, 2);
  stop(); viewport.update({offset: 160, height: 800}); assert.equal(calls, 2);
});


test('Reorder cancellation releases its owner and an unstarted peer cannot cancel or commit the active drag', () => {
  let dragging = false, admitted = true; const moves: number[] = [], drops: boolean[] = [];
  const gesture = createSavedReorderGesture({start: () => {if (!admitted) return false; dragging = true; return true;},
    move: value => moves.push(value), drop: success => {dragging = false; drops.push(success);}});
  assert.equal(gesture.begin('handle', 'occurrence'), true);
  assert.equal(gesture.begin('row', 'occurrence'), false);
  gesture.finish('row', false); assert.equal(dragging, true, 'failed long-press peer leaves the grip owner intact');
  gesture.move('row', 300); gesture.move('handle', 80); assert.deepEqual(moves, [80]);
  gesture.finish('handle', false); assert.equal(dragging, false, 'interrupted owner releases the parent scroll lock'); assert.deepEqual(drops, [false]);
  gesture.finish('row', true); gesture.finish('handle', true); assert.deepEqual(drops, [false], 'late finalization cannot commit an ended pickup');
  assert.equal(gesture.begin('row', 'occurrence'), true); gesture.finish('handle', false); assert.equal(dragging, true);
  gesture.finish('row', true); assert.deepEqual(drops, [false, true]); assert.equal(dragging, false);
  admitted = false; assert.equal(gesture.begin('handle', 'retired'), false); gesture.finish('handle', true);
  assert.deepEqual(drops, [false, true], 'a refused opening owns no completion');
});
