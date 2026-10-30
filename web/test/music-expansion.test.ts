import test from 'node:test';
import assert from 'node:assert/strict';
import {contentStep, expansionFrame, reducedFrame} from '../src/screens/watch/music/music-expansion.ts';

// UNRUN pure source regression: no runner, browser, clock or scheduler is started.
const dock = {left: 20, top: 640, right: 84, bottom: 704};
const player = {left: 120, top: 120, right: 640, bottom: 640};

test('the frame interpolates only the rectangles the owner measured', () => {
  assert.deepEqual(expansionFrame(dock, player, 0)?.rect, dock);
  assert.deepEqual(expansionFrame(dock, player, 1)?.rect, player);
  const middle = expansionFrame(dock, player, 0.5);
  assert.deepEqual(middle?.rect, {left: 70, top: 380, right: 362, bottom: 672});
  assert.equal(middle?.coverOpacity, 1, 'the cover is already handed off, never faded by progress');
});

test('no admitted rectangle and no finite progress means no invented frame', () => {
  assert.equal(expansionFrame(undefined, player, 0.5), undefined);
  assert.equal(expansionFrame(dock, null, 0.5), undefined);
  assert.equal(expansionFrame({left: 0, top: 0, right: 0, bottom: 10} as never, player, 0.5), undefined);
  assert.equal(expansionFrame({left: 0, top: 0, right: Number.NaN, bottom: 10} as never, player, 0.5), undefined);
  assert.equal(expansionFrame(dock, player, Number.POSITIVE_INFINITY), undefined);
});

test('the cover hands off at the first visible progress and never fades in', () => {
  assert.equal(expansionFrame(dock, player, 0)?.coverOpacity, 0, 'before any progress the dock art owns the slot');
  assert.equal(expansionFrame(dock, player, 0.001)?.coverOpacity, 0, 'the handoff starts only after zero');
  assert.equal(expansionFrame(dock, player, 0.002)?.coverOpacity, 1);
  assert.equal(expansionFrame(dock, player, 1)?.coverOpacity, 1);
});

test('progress reverses from the current frame without any new state', () => {
  const forward = expansionFrame(dock, player, 0.8)!;
  const back = expansionFrame(dock, player, 0.2)!;
  assert.ok(forward.rect.left > back.rect.left);
  assert.equal(back.coverOpacity, 1, 'a reversal keeps the handed-off cover opaque');
  assert.deepEqual(expansionFrame(dock, player, 1.4)!.rect, player, 'clamped, never extrapolated');
  assert.deepEqual(expansionFrame(dock, player, -0.2)!.rect, dock);
});

test('the three content steps are 60 ms apart and 180 ms each', () => {
  assert.deepEqual(contentStep(0), {index: 0, delayMs: 0, durationMs: 180});
  assert.deepEqual(contentStep(1), {index: 1, delayMs: 60, durationMs: 180});
  assert.deepEqual(contentStep(2), {index: 2, delayMs: 120, durationMs: 180});
  assert.equal(contentStep(3), undefined);
  assert.equal(contentStep(-1), undefined);
  assert.equal(contentStep(1.5), undefined);
});

test('reduce motion keeps opacity only, at 160 ms', () => {
  const reduced = reducedFrame(0.4);
  assert.deepEqual(reduced, {opacity: 0.4, durationMs: 160});
  assert.equal(Object.keys(reduced!).includes('rect'), false);
  assert.equal(reducedFrame(Number.NaN), undefined);
});
