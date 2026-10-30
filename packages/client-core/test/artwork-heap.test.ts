import test from 'node:test';
import assert from 'node:assert/strict';
import {MinHeap} from '../src/artwork/heap.ts';

test('pops in order, peeks without removing, and reports size', () => {
  const heap = new MinHeap<number>((a, b) => a < b);
  assert.equal(heap.size, 0);
  assert.equal(heap.pop(), undefined);
  assert.equal(heap.peek(), undefined);
  for (const n of [5, 1, 4, 1, 3]) heap.push(n);
  assert.equal(heap.size, 5);
  assert.equal(heap.peek(), 1);
  const out: number[] = [];
  while (heap.size) out.push(heap.pop()!);
  assert.deepEqual(out, [1, 1, 3, 4, 5]);
  assert.equal(heap.size, 0);
});

test('custom ordering and heap restore after retain', () => {
  const heap = new MinHeap<{p: number; id: string}>((a, b) => a.p < b.p);
  heap.push({p: 3, id: 'c'});
  heap.push({p: 1, id: 'a'});
  heap.push({p: 2, id: 'b'});
  assert.equal(heap.peek()!.id, 'a');
  heap.retain(item => item.id !== 'a');
  assert.equal(heap.size, 2);
  assert.equal(heap.pop()!.id, 'b');
  assert.equal(heap.pop()!.id, 'c');
  heap.push({p: 9, id: 'z'});
  heap.clear();
  assert.equal(heap.size, 0);
  assert.equal(heap.pop(), undefined);
});

test('edge: equal keys, single element and large sequences stay ordered', () => {
  const heap = new MinHeap<number>((a, b) => a < b);
  heap.push(7);
  assert.equal(heap.pop(), 7);
  for (let i = 1000; i >= 0; i--) heap.push(i % 7);
  let last = -1;
  while (heap.size) {
    const v = heap.pop()!;
    assert.ok(v >= last, `${v} after ${last}`);
    last = v;
  }
});
