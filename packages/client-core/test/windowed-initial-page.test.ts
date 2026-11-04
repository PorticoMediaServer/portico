import test from 'node:test';
import assert from 'node:assert/strict';
import {createWindowedCollection} from '../src/collections/windowed.ts';

// A page the caller has just read (a person's first credits) is resident from the first render: no request returns it,
// and a screen built on it never paints placeholders where it already has the items.
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function seeded(seed = Array.from({length: 10}, (_, i) => `seed${i}`)) {
  const starts: number[] = [];
  const list = createWindowedCollection<string>({pageSize: 10, maxResidentPages: 4, prefetchPages: 0, maxConcurrent: 1, keyOf: item => item,
    fetchPage: async (start, count) => {starts.push(start); return {items: Array.from({length: Math.min(count, 35 - start)}, (_, i) => `read${start + i}`), total: 35};},
    initialPage: {items: seed, total: 35}});
  return {list, starts};
}

test('a seeded first page is resident and current at creation, with no request', async () => {
  const {list, starts} = seeded();
  try {
    assert.equal(list.itemAt(0), 'seed0'); assert.equal(list.itemAt(9), 'seed9'); assert.equal(list.itemAt(10), undefined);
    assert.equal(list.getSnapshot().total, 35); assert.equal(list.getSnapshot().status, 'ready');
    list.ensureRange(0, 9); await tick();
    assert.deepEqual(starts, [], 'page 0 is not read again'); assert.equal(list.getSnapshot().status, 'ready');
    // Later pages are read as usual.
    list.ensureRange(10, 19); await tick();
    assert.equal(starts[0], 10); assert.ok(!starts.includes(0), 'page 0 is never read'); assert.equal(list.itemAt(10), 'read10');
  } finally {list.dispose();}
});

test('a new generation reads its first page again; the seed belongs to the first one only', async () => {
  const {list, starts} = seeded();
  try {
    list.ensureRange(0, 9); list.invalidate(); await tick(); await tick();
    assert.deepEqual(starts, [0]); assert.equal(list.itemAt(0), 'read0');
  } finally {list.dispose();}
});

test('a seed with no usable total is ignored, and page 0 is read', async () => {
  const starts: number[] = [];
  const list = createWindowedCollection<string>({pageSize: 10, maxResidentPages: 4, prefetchPages: 0, keyOf: item => item,
    fetchPage: async start => {starts.push(start); return {items: ['read0'], total: 1};}, initialPage: {items: ['seed0'], total: Number.NaN}});
  try {
    assert.equal(list.itemAt(0), undefined); list.ensureRange(0, 0); await tick();
    assert.deepEqual(starts, [0]); assert.equal(list.itemAt(0), 'read0');
  } finally {list.dispose();}
});
