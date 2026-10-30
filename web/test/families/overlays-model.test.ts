import {test} from 'node:test';
import assert from 'node:assert/strict';
import {dismissToast, releaseSheet, resistedHeight, sheetDetents, springFrames, sheetViewport} from '../../src/kit/overlays/model.ts';

test('sheet detents stay inside the visual viewport and preserve caller indices', () => {
  assert.deepEqual(sheetDetents([700, 420, 1000, -1, NaN, 420], 744), [{height: 420, index: 1}, {height: 700, index: 0}, {height: 744, index: 2}]);
  assert.deepEqual(sheetDetents([0, Infinity], 744), []);
});
test('a sheet release projects velocity for 200ms instead of picking the nearest current height', () => {
  const detents = sheetDetents([422, 744], 824);
  assert.equal(releaseSheet(560, 0, detents)?.index, 0);
  assert.equal(releaseSheet(560, -600, detents)?.index, 1);
  assert.equal(releaseSheet(400, 1100, detents), null);
  assert.equal(releaseSheet(210, 0, detents), null);
  assert.equal(releaseSheet(212, 0, detents)?.index, 0);
});
test('sheet overdrag resists the top without jumping or producing negative height', () => {
  assert.equal(resistedHeight(744, 744), 744);
  assert.equal(resistedHeight(844, 744), 764);
  assert.equal(resistedHeight(-20, 744), 0);
});
test('toast release accepts side or down flicks while an upward drag stays available to cancel', () => {
  assert.equal(dismissToast(20, 0, 501, 0), true);
  assert.equal(dismissToast(0, 20, 0, 501), true);
  assert.equal(dismissToast(41, 0, 0, 0), true);
  assert.equal(dismissToast(0, 41, 0, 0), true);
  assert.equal(dismissToast(10, 10, 100, 100), false);
  assert.equal(dismissToast(0, -60, 0, -600), false);
});
test('spring trajectories start at the release point and retain the direction of finger velocity', () => {
  const up = springFrames(422, 744, 600), down = springFrames(422, 744, -600);
  assert.equal(up[0], 422); assert.equal(down[0], 422);
  assert.equal(up.at(-1), 744); assert.equal(down.at(-1), 744);
  assert.ok(up[1]! > down[1]!);
  assert.ok(up.every(Number.isFinite));
  const still = springFrames(744, 744, 0);
  assert.ok(still.every(value => value === 744));
});

test('phone sheets follow keyboard shrink and visual-viewport panning without changing the ordinary frame', () => {
  assert.deepEqual(sheetViewport(714), {height: 714, bottom: 0});
  const keyboard = sheetViewport(714, {height: 378, offsetTop: 0});
  assert.equal(714 - keyboard.bottom, 378, 'footer clears the keyboard');
  const panned = sheetViewport(714, {height: 378, offsetTop: 110});
  assert.equal(714 - panned.bottom, 488, 'scrolling visual viewport moves the visible bottom too');
  assert.deepEqual(sheetViewport(378, {height: 378, offsetTop: 0}), {height: 378, bottom: 0}, 'layout-resizing browsers do not get a second offset');
});
