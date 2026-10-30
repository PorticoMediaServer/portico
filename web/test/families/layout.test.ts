import {test} from 'node:test';
import assert from 'node:assert/strict';
import {alphabetKeys, anchoredOffset, boundedIndex, gridColumns, gridWindow, railBand} from '../../src/kit/layout/geometry.ts';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed.ts';

test('50,000 stable identities require only visible rows and sparse resident pages', async () => {
  const requested: number[] = [];
  const collection = createWindowedCollection({pageSize: 40, maxResidentPages: 4, maxConcurrent: 1, keyOf: (item: {id: string}) => item.id,
    fetchPage: async (start, count) => { requested.push(start); return {total: 50_000, items: Array.from({length: Math.min(count, 50_000 - start)}, (_, offset) => ({id: `entity-${start + offset}`}))}; }});
  const columns = gridColumns(1312, 170, 20), visible = gridWindow(50_000, columns, 352, 1_800_000, 844, 1);
  collection.ensureRange(visible.renderFirst, visible.renderLast);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(columns, 7);
  assert.ok(visible.renderLast - visible.renderFirst + 1 <= 42);
  assert.ok(collection.resident().items <= 160);
  assert.ok(requested.length <= 4);
  assert.ok(!requested.includes(0));
  const slot = collection.slotAt(visible.first);
  assert.equal(slot.kind, 'item');
  if (slot.kind === 'item') assert.equal(slot.key, `entity-${visible.first}`);
  collection.dispose();
});

test('window boundaries report visible items independently of overscan and empty totals', () => {
  assert.deepEqual(gridWindow(0, 7, 352, 0, 844), {first: -1, last: -1, renderFirst: 0, renderLast: -1, rows: 0});
  assert.deepEqual(gridWindow(22, 7, 100, 100, 100), {first: 7, last: 13, renderFirst: 0, renderLast: 20, rows: 4});
  const last = gridWindow(22, 7, 100, 1000, 844);
  assert.equal(last.first, 21); assert.equal(last.last, 21); assert.equal(last.renderLast, 21);
  assert.equal(gridColumns(390, 108, 12), 3);
});

test('the complete rail retains absent Latin buckets and actual server Unicode keys in order', () => {
  const anchors = [{key: '#', index: 0}, {key: 'A', index: 2}, {key: 'C', index: 20}, {key: 'Б', index: 500}, {key: 'あ', index: 1000}];
  const keys = alphabetKeys(anchors);
  assert.deepEqual(keys.slice(0, 4), ['#', 'A', 'B', 'C']);
  assert.deepEqual(keys.slice(-2), ['Б', 'あ']);
  assert.equal(keys.length, 29);
  assert.deepEqual(railBand(keys, anchors, {first: 3, last: 21}), {first: 1, last: 3});
  assert.deepEqual(railBand(keys, anchors, {first: 600, last: 1004}), {first: 27, last: 28});
  assert.equal(railBand(keys, [], {first: -1, last: -1}), undefined);
});

test('focus anchors scroll only when the real focused target leaves its safe band', () => {
  assert.equal(anchoredOffset(200, 400, 1000, 0, 200), 0);
  assert.equal(anchoredOffset(50, 250, 1000, 0, 200), 0);
  assert.equal(anchoredOffset(1000, 1200, 1000, 0, 200), 400);
  assert.equal(anchoredOffset(220, 420, 1000, 400, 200), 20);
});

test('controlled spotlight indices remain valid when the server replaces the bounded set', () => {
  assert.equal(boundedIndex(3, 0), -1);
  assert.equal(boundedIndex(7, 4), 3);
  assert.equal(boundedIndex(-2, 4), 0);
  assert.equal(boundedIndex(Number.NaN, 4), 0);
});
