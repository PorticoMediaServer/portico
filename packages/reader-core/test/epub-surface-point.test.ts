import test from 'node:test';
import assert from 'node:assert/strict';
import {READER_EDGE, surfacePoint} from '../src/epub/surface-point.ts';

test('a tap is placed across the reader on screen, not across a wide scrolled chapter iframe', () => {
  const width = 402, pages = 5;
  // Page 3 of a chapter whose iframe is five pages wide and scrolled two pages left under the reader (WebKit).
  const frameLeft = -2 * width, right = 2 * width + 0.9 * width, middle = 2 * width + 0.5 * width, left = 2 * width + 0.1 * width;
  assert.ok(Math.abs(surfacePoint(right, frameLeft, 0, width).fraction - 0.9) < 1e-9);
  assert.ok(Math.abs(surfacePoint(middle, frameLeft, 0, width).fraction - 0.5) < 1e-9);
  assert.ok(Math.abs(surfacePoint(left, frameLeft, 0, width).fraction - 0.1) < 1e-9);
  // The old reading, clientX over the iframe's own width, put that right-third tap in the middle third.
  assert.ok(right / (pages * width) > 1 / 3 && right / (pages * width) < 2 / 3);
});

test('an unscrolled chapter, a reader not at the window edge, and the bounds', () => {
  assert.equal(surfacePoint(201, 0, 0, 402).fraction, 0.5);
  assert.equal(surfacePoint(100, 0, 80, 400).x, 20);
  assert.equal(surfacePoint(-50, 0, 0, 400).fraction, 0);
  assert.equal(surfacePoint(900, 0, 0, 400).fraction, 1);
  assert.equal(surfacePoint(10, 0, 0, 0).fraction, 0);
  assert.ok(surfacePoint(12, -804, -804, 402).x <= READER_EDGE);
});
