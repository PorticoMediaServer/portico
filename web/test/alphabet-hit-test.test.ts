import test from 'node:test';
import assert from 'node:assert/strict';
import {railIndexAt} from '../src/kit/layout/geometry.ts';

test('alphabet dragging uses actual rendered cells, including a changed touch pitch', () => {
  const cells = [{top: 100, bottom: 144}, {top: 144, bottom: 188}, {top: 188, bottom: 232}];
  assert.equal(railIndexAt(143.9, cells), 0);
  assert.equal(railIndexAt(144, cells), 1);
  assert.equal(railIndexAt(211, cells), 2);
});
test('fractional and scrolled cells use viewport coordinates without a second scroll offset', () => {
  const cells = [{top: -19.5, bottom: 1.75}, {top: 1.75, bottom: 23}, {top: 23, bottom: 44.25}];
  assert.equal(railIndexAt(0, cells), 0);
  assert.equal(railIndexAt(2, cells), 1);
  assert.equal(railIndexAt(23, cells), 2);
  assert.equal(railIndexAt(-100, cells), 0);
  assert.equal(railIndexAt(1000, cells), 2);
});
test('empty, not-yet-mounted and zero-height cells cannot invent a jump', () => {
  assert.equal(railIndexAt(10, []), -1);
  assert.equal(railIndexAt(10, [undefined, {top: 0, bottom: 0}]), -1);
  assert.equal(railIndexAt(10, [undefined, {top: 0, bottom: 21}]), 1);
});
