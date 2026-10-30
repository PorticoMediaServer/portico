import test from 'node:test';
import assert from 'node:assert/strict';
import {bufferedSegments} from '../src/screens/watch/buffered-ranges.ts';

test('buffered seek segments retain disjoint observed ranges and clip to seek bounds', () => {
  assert.deepEqual(bufferedSegments([{start: 0, end: 20}, {start: 40, end: 50}, {start: 90, end: 120}], {start: 10, end: 110}), [
    {left: 0, width: 10}, {left: 30, width: 10}, {left: 80, width: 20},
  ]);
});
test('buffered seek segments omit invalid, empty and out-of-window ranges', () => {
  const ranges = [{start: NaN, end: 50}, {start: 30, end: Infinity}, {start: 40, end: 20}, {start: 0, end: 10}, {start: 60, end: 70}];
  assert.deepEqual(bufferedSegments(ranges, {start: 10, end: 60}), []);
  assert.deepEqual(bufferedSegments([{start: 0, end: 30}], null), []);
  assert.deepEqual(bufferedSegments([{start: 0, end: 30}], {start: 30, end: 30}), []);
  assert.deepEqual(bufferedSegments([{start: 0, end: 30}], {start: 0, end: Infinity}), []);
});
