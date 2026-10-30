import assert from 'node:assert/strict';
import test from 'node:test';
import {listeningCoverDrawable, listeningCoverProgress, listeningCoverTransform} from '../src-next/kit/dock/listening-cover-model';
import type {DockRect} from '../src-next/kit/dock/types';

/** Authored SOURCE vectors for FX-064's cover placement. Arbitrary measured
 * fixtures - not native-image, cache, host or morph acceptance. */
const full: DockRect = {x: 0, y: 100, width: 400, height: 400};
const mini: DockRect = {x: 320, y: 700, width: 40, height: 40};

test('the full endpoint places the box at its own measured frame', () => {
  const transform = listeningCoverTransform(full, mini, 0)!;
  assert.deepEqual(transform, {translateX: 0, translateY: 0, scaleX: 1, scaleY: 1});
});

test('the mini endpoint lands on the measured mini centre at the mini size', () => {
  const transform = listeningCoverTransform(full, mini, 1)!;
  // Full centre (200, 300) -> mini centre (340, 720).
  assert.equal(transform.translateX, 140);
  assert.equal(transform.translateY, 420);
  assert.equal(transform.scaleX, 0.1);
  assert.equal(transform.scaleY, 0.1);
});

test('the midpoint interpolates the centre and the size', () => {
  const transform = listeningCoverTransform(full, mini, 0.5)!;
  assert.equal(transform.translateX, 70);
  assert.equal(transform.translateY, 210);
  assert.equal(transform.scaleX, 0.55);
  assert.equal(transform.scaleY, 0.55);
});

test('translation follows the axis of the measured rectangles, not a fixed direction', () => {
  const leftward: DockRect = {x: -60, y: 300, width: 80, height: 80};
  const transform = listeningCoverTransform(full, leftward, 1)!;
  // Full centre (200, 300) -> (-20, 340): the x delta is negative, y positive.
  assert.equal(transform.translateX, -220);
  assert.equal(transform.translateY, 40);
  assert.equal(transform.scaleX, 0.2);
  assert.equal(transform.scaleY, 0.2);
});

test('progress clamps to its endpoints; NaN sits at the full end and infinities take the nearest endpoint', () => {
  assert.equal(listeningCoverProgress(-2), 0);
  assert.equal(listeningCoverProgress(4), 1);
  assert.equal(listeningCoverProgress(0.25), 0.25);
  assert.equal(listeningCoverProgress(Number.NaN), 0);
  assert.equal(listeningCoverProgress(Number.NEGATIVE_INFINITY), 0);
  assert.equal(listeningCoverProgress(Number.POSITIVE_INFINITY), 1);
  assert.deepEqual(listeningCoverTransform(full, mini, -1), listeningCoverTransform(full, mini, 0));
  assert.deepEqual(listeningCoverTransform(full, mini, 9), listeningCoverTransform(full, mini, 1));
});

test('a missing or invalid rectangle refuses to draw rather than guessing', () => {
  assert.equal(listeningCoverDrawable(full, mini), true);
  assert.equal(listeningCoverDrawable(undefined, mini), false);
  assert.equal(listeningCoverDrawable(full, undefined), false);
  assert.equal(listeningCoverTransform(undefined, mini, 0.5), undefined);
  assert.equal(listeningCoverTransform(full, undefined, 0.5), undefined);
  assert.equal(listeningCoverDrawable({...full, width: 0}, mini), false);
  assert.equal(listeningCoverDrawable(full, {...mini, height: -1}), false);
  assert.equal(listeningCoverDrawable({...full, x: Number.NaN}, mini), false);
  assert.equal(listeningCoverTransform(full, {...mini, y: Number.POSITIVE_INFINITY}, 0.5), undefined);
});

test('unequal rectangles keep each axis honest instead of forcing one scale', () => {
  const wide: DockRect = {x: 40, y: 40, width: 200, height: 100};
  const transform = listeningCoverTransform(full, wide, 1)!;
  assert.equal(transform.scaleX, 0.5);
  assert.equal(transform.scaleY, 0.25);
  // Full centre (200, 300) -> wide centre (140, 90).
  assert.equal(transform.translateX, -60);
  assert.equal(transform.translateY, -210);
});
