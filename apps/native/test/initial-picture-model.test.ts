/** Authored UNRUN source vectors; no native image, first-frame or motion proof. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {initialPictureInHost, initialPictureMix, initialPictureTransform, initialPictureKey, initialPictureRetired, retireInitialPicture, initialPictureSettled} from '../src-next/kit/dock/initial-picture-model.ts';

test('only real source and host window measurements become a local frame', () => {
  const source = {x: 30, y: 90, width: 200, height: 300};
  assert.deepEqual(initialPictureInHost(source, {x: 10, y: 40, width: 390, height: 800}), {x: 20, y: 50, width: 200, height: 300});
  assert.equal(initialPictureInHost(source, {x: 0, y: 0, width: 0, height: 800}), undefined);
  assert.equal(initialPictureInHost({...source, x: NaN}, {x: 0, y: 0, width: 390, height: 800}), undefined);
});
test('a measured clip changes aspect without stretching the shared image texture', () => {
  const from = {x: 20, y: 50, width: 200, height: 300}, to = {x: 0, y: 0, width: 400, height: 225};
  assert.deepEqual(initialPictureMix(from, to, 0), from);
  assert.deepEqual(initialPictureMix(from, to, 1), to);
  const placement = initialPictureTransform(from, to);
  assert.equal(placement.translateX, 80); assert.equal(placement.translateY, -87.5);
  assert.equal(placement.scaleX, 2); assert.equal(placement.scaleY, .75);
  assert.equal(placement.scaleX * placement.imageScaleX, 2);
  assert.equal(placement.scaleY * placement.imageScaleY, 2);
});
test('a current frame is the start of a late target, so the first retarget sample stays put', () => {
  const current = {x: 18, y: 386, width: 280, height: 220}, measuredDock = {x: 28, y: 646, width: 80, height: 45};
  assert.deepEqual(initialPictureMix(current, measuredDock, 0), current);
  assert.deepEqual(initialPictureMix(current, measuredDock, 1), measuredDock);
});
test('landing requires geometry and velocity, including late manual target settlement', () => {
  assert.equal(initialPictureSettled(0, 0, 0, false, false, false, true), false);
  assert.equal(initialPictureSettled(0, 0, 0, false, true, false, true), true);
  assert.equal(initialPictureSettled(1, 1, 0, false, false, false, false), false);
  assert.equal(initialPictureSettled(1, 1, 0, false, false, true, true), false);
  assert.equal(initialPictureSettled(1, 1, 0, false, true, true, true), true);
  assert.equal(initialPictureSettled(1, 1, .1, false, true, true, true), false);
  assert.equal(initialPictureSettled(0, 0, 0, true, true, false, true), false);
});
test('same-value starts keep distinct actual original occurrence identity and consume only themselves', () => {
  const a = Object.freeze({}), b = Object.freeze({});
  assert.equal(initialPictureKey(a), initialPictureKey(a)); assert.notEqual(initialPictureKey(a), initialPictureKey(b));
  retireInitialPicture(a); assert.equal(initialPictureRetired(a), true); assert.equal(initialPictureRetired(b), false);
});
