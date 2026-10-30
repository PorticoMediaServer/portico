import assert from 'node:assert/strict';
import {test} from 'node:test';
import {touchMenuRect} from '../src-next/kit/browse/touch-menu-geometry.ts';

const insets = {left: 0, right: 0, top: 24, bottom: 20};
test('tablet9 opens right, falls back left, and opens upward at the bottom', () => {
  const anchor = {x: 100, y: 100, width: 150, height: 225};
  const right = touchMenuRect(anchor, {width: 1180, height: 820}, 500, insets);
  assert.equal(right.width, 300); assert.equal(right.side, 'right'); assert.equal(right.x, 262); assert.equal(right.y, 100);
  const left = touchMenuRect({...anchor, x: 900, y: 550}, {width: 1180, height: 820}, 500, insets);
  assert.equal(left.side, 'left'); assert.equal(left.x, 588); assert.equal(left.y, 275);
});
test('remeasuring after rotation keeps the same item anchor inside the new safe area', () => {
  const rect = touchMenuRect({x: 600, y: 800, width: 150, height: 225}, {width: 820, height: 1180}, 900, insets);
  assert.equal(rect.width, 300); assert.equal(rect.side, 'left');
  assert.ok(rect.y >= 32); assert.ok(rect.y + rect.height <= 1152);
  assert.ok(rect.arrowY >= 16 && rect.arrowY <= rect.height - 16);
});
