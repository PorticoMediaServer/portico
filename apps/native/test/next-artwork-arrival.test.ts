import test from 'node:test';
import assert from 'node:assert/strict';
import {createArtworkArrivals} from '../src-next/platform/artwork-arrival.ts';

function fixture() {
  let now = 0, id = 0;
  const tasks = new Map<number, {at: number; run: () => void}>();
  const queue = createArtworkArrivals({now: () => now, set: (run, ms) => {const key = ++id; tasks.set(key, {at: now + ms, run}); return key;}, clear: key => {tasks.delete(key as number);}});
  const advance = (ms: number) => {const end = now + ms; for (;;) {const next = [...tasks.entries()].filter(([, task]) => task.at <= end).sort((a, b) => a[1].at - b[1].at)[0]; if (!next) break; now = next[1].at; tasks.delete(next[0]); next[1].run();} now = end;};
  return {queue, advance, time: () => now, pending: () => tasks.size};
}

test('decode order cannot overtake reading order; ready siblings start forty milliseconds apart', () => {
  const f = fixture(), seen: [number, number][] = [];
  const first = f.queue.register(0), second = f.queue.register(1), third = f.queue.register(2);
  third.ready(() => seen.push([2, f.time()])); second.ready(() => seen.push([1, f.time()]));
  f.advance(100); assert.deepEqual([...seen], []);
  first.ready(() => seen.push([0, f.time()])); f.advance(80);
  assert.deepEqual(seen, [[0, 100], [1, 140], [2, 180]]);
});

test('failed/offscreen predecessors release the queue and retired callbacks never run', () => {
  const f = fixture(), seen: number[] = [], first = f.queue.register(0), second = f.queue.register(1);
  second.ready(() => seen.push(1)); first.retire(); f.advance(0); assert.deepEqual(seen, [1]);
  const old = f.queue.register(0); old.ready(() => seen.push(2)); old.retire(); f.advance(100);
  assert.deepEqual(seen, [1]); assert.equal(f.pending(), 0);
});

test('a reflow changes pending ordering without re-registering or revealing twice', () => {
  const f = fixture(), seen: number[] = [], first = f.queue.register(0), second = f.queue.register(1);
  second.ready(() => seen.push(1)); second.move(-1); f.advance(0); assert.deepEqual(seen, [1]);
  first.ready(() => seen.push(0)); f.advance(40); assert.deepEqual(seen, [1, 0]);
  second.ready(() => seen.push(2)); f.advance(100); assert.deepEqual(seen, [1, 0]);
});
