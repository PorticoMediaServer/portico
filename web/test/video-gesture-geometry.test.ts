import test from 'node:test';
import assert from 'node:assert/strict';
import {videoGestureFrame, videoGestureGeometry, videoGesturePosition, videoGestureRelease} from '../src/kit/dock/video-gesture-geometry.ts';

const full = {left: 0, top: 100, width: 400, height: 300};
const dock = {left: 20, top: 500, width: 200, height: 150};

test('captures measured slots and interpolates finite frames without borrowing inputs', () => {
  const a = {...dock}, b = {...full}, geometry = videoGestureGeometry(a, b)!;
  a.left = 99; b.width = 99;
  assert.deepEqual(videoGestureFrame(geometry, 0), full);
  assert.deepEqual(videoGestureFrame(geometry, 1), dock);
  assert.deepEqual(videoGestureFrame(geometry, .5), {left: 10, top: 300, width: 300, height: 225});
  assert.notStrictEqual(geometry.dock, a); assert.notStrictEqual(geometry.full, b);
  assert.ok(Object.values(videoGestureFrame(geometry, .5)!).every(Number.isFinite));
});

test('uses actual travel and maps both directions through player release boundaries', () => {
  const long = videoGestureGeometry(dock, full)!;
  const short = videoGestureGeometry({...dock, top: 200}, full)!;
  assert.equal(videoGestureRelease(0, videoGesturePosition(0, 35, long)!, 0, long)?.target, 0);
  assert.equal(videoGestureRelease(0, videoGesturePosition(0, 36, short)!, 0, short)?.target, 1);
  for (const geometry of [long, short]) {
    assert.equal(videoGestureRelease(0, .35, 0, geometry)?.target, 0);
    assert.equal(videoGestureRelease(0, .351, 0, geometry)?.target, 1);
    assert.equal(videoGestureRelease(1, .65, 0, geometry)?.target, 1);
    assert.equal(videoGestureRelease(1, .649, 0, geometry)?.target, 0);
    assert.equal(videoGestureRelease(0, .1, 600, geometry)?.target, 0);
    assert.equal(videoGestureRelease(0, .1, 601, geometry)?.target, 1);
    assert.equal(videoGestureRelease(1, .9, -600, geometry)?.target, 1);
    assert.equal(videoGestureRelease(1, .9, -601, geometry)?.target, 0);
    assert.equal(videoGestureRelease(1, .5, -601, geometry)?.velocity, -601 / geometry.travel);
    assert.equal(videoGesturePosition(1, -geometry.travel * 2, geometry), 0);
    assert.equal(videoGesturePosition(0, geometry.travel * 2, geometry), 1);
  }
});

test('refuses absent or invalid measurements and nonfinite inputs', () => {
  assert.equal(videoGestureGeometry({...dock, top: full.top}, full), undefined);
  assert.equal(videoGestureGeometry({...dock, top: full.top - 1}, full), undefined);
  assert.equal(videoGestureGeometry({...dock, width: 0}, full), undefined);
  assert.equal(videoGestureGeometry({...dock, left: NaN}, full), undefined);
  assert.equal(videoGestureGeometry({...dock, height: Infinity}, full), undefined);
  assert.equal(videoGestureFrame(undefined, .5), undefined);
  assert.equal(videoGesturePosition(0, 10, undefined), undefined);
  assert.equal(videoGestureRelease(0, .5, 10, undefined), undefined);
  const geometry = videoGestureGeometry(dock, full)!;
  assert.equal(videoGestureFrame(geometry, Infinity), undefined);
  assert.equal(videoGesturePosition(0, NaN, geometry), undefined);
  assert.equal(videoGestureRelease(0, .5, Infinity, geometry), undefined);
  assert.equal(videoGestureFrame({...geometry, travel: 0}, .5), undefined);
});
