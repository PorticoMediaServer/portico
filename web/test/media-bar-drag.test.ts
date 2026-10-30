import test from 'node:test';
import assert from 'node:assert/strict';
import {dismissOffsets, dragOpacity, releaseOpacity} from '../src/kit/dock/media-bar-drag.ts';

test('drag opacity follows the finger distance over the actual bar width', () => {
  assert.equal(dragOpacity(0, 300), 1);
  assert.equal(dragOpacity(75, 300), .75);
  assert.equal(dragOpacity(-150, 300), .5);
  assert.equal(dragOpacity(300, 300), 0);
  assert.equal(dragOpacity(900, 300), 0);
  assert.equal(dragOpacity(20, 0), 0);
});

test('a committed outward release decelerates into its target', () => {
  const offsets = dismissOffsets(90, 300);
  assert.equal(offsets.length, 33);
  assert.equal(offsets[0], 90);
  assert.equal(offsets.at(-1), 300);
  for (let index = 1; index < offsets.length; index++) {
    assert.ok(offsets[index] >= offsets[index - 1], 'monotone outward travel');
    if (index > 1) assert.ok(offsets[index] - offsets[index - 1] <= offsets[index - 1] - offsets[index - 2], 'decelerating steps');
  }
});

test('release opacity starts at the finger paint and ends at the committed value', () => {
  const out = releaseOpacity(.64, 0, 3), back = releaseOpacity(.64, 1, 3);
  assert.ok(Math.abs(out[0] - .64) < 1e-9 && Math.abs(out[1] - .32) < 1e-9 && Math.abs(out[2]) < 1e-9);
  assert.ok(Math.abs(back[0] - .64) < 1e-9 && Math.abs(back[1] - .82) < 1e-9 && Math.abs(back[2] - 1) < 1e-9);
});
