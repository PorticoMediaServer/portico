import assert from 'node:assert/strict';
import test from 'node:test';
import {homeMountedRows, homeVisibleRange} from '../src-next/screens/home/home-window.ts';
import {createToastClock} from '../src-next/kit/overlays/toast-clock.ts';

test('Home row window uses exact intersection boundaries, including gaps and the end', () => {
  const boxes = [{id: 'a', y: 560, height: 200}, {id: 'b', y: 788, height: 350}, {id: 'c', y: 1166, height: 120}];
  assert.deepEqual(homeVisibleRange(boxes, 0, 560), [0, -1]);
  assert.deepEqual(homeVisibleRange(boxes, 0, 561), [0, 0]);
  assert.deepEqual(homeVisibleRange(boxes, 760, 28), [1, 0], 'a gap does not falsely intersect either row');
  assert.deepEqual(homeVisibleRange(boxes, 760, 29), [1, 1]);
  assert.deepEqual(homeVisibleRange(boxes, 800, 500), [1, 2]);
  assert.deepEqual(homeVisibleRange(boxes, 1286, 100), [3, 2]);
  assert.deepEqual(homeVisibleRange([], 0, 800), [0, -1]);
});

test('64 Home rows keep a bounded viewport plus genuine TV focus-return neighbors', () => {
  const boxes = Array.from({length: 64}, (_, index) => ({id: String(index), y: 440 + index * 250, height: 224}));
  const range = homeVisibleRange(boxes, 440 + 30 * 250, 750);
  assert.deepEqual(range, [30, 32]);
  const mounted = homeMountedRows(64, range[0], range[1], [2, 63, -1, 99]);
  assert.deepEqual([...mounted].sort((a, b) => a - b), [1, 2, 3, 28, 29, 30, 31, 32, 33, 34, 62, 63]);
  assert.ok(mounted.size < boxes.length / 4);
  assert.deepEqual([...homeMountedRows(0, 0, -1, [])], []);
});

function timer() {
  let now = 0, sequence = 0;
  const jobs = new Map<number, {at: number; run: () => void}>(), expired: string[] = [];
  const clock = createToastClock({now: () => now, schedule: (run, delay) => {
    const id = ++sequence; jobs.set(id, {at: now + delay, run}); return id;
  }, cancel: id => {jobs.delete(id as number);}}, id => expired.push(id));
  return {clock, jobs, expired, advance(ms: number) {
    now += ms;
    for (const [id, job] of [...jobs]) if (job.at <= now) {jobs.delete(id); job.run();}
  }};
}

test('Undo has eight seconds of active time, independently paused by touch, focus and suspension', () => {
  const f = timer(); f.clock.replace('undo', 8000);
  f.advance(3000); f.clock.pause('touch', true); f.clock.pause('focus', true);
  f.advance(9000); f.clock.pause('touch', false); f.advance(9000);
  assert.deepEqual(f.expired, []);
  f.clock.pause('focus', false); f.advance(4999); assert.deepEqual(f.expired, []);
  f.clock.pause('background', true); f.advance(20000); f.clock.pause('background', false);
  f.advance(1); assert.deepEqual(f.expired, ['undo']); f.clock.dispose();
});

