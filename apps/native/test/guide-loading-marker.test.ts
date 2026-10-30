import test from 'node:test';
import assert from 'node:assert/strict';
import {guideLoadingMarkerBox} from '../src-next/screens/settings-live/guide-loading-marker.ts';
import {paintSkeletonVisible} from '../src-next/kit/states/loading-boundary-model.ts';

// Pure geometry and the shared visibility vector only. These are not a mounted
// render or lifecycle test; the quiet window itself is LoadingPaint's and is
// covered by its own existing harness, not here.
test('the marker keeps the cell footprint exactly', () => {
  assert.deepEqual(guideLoadingMarkerBox(120, 48, true), {width: 120, height: 42, radius: 8, top: 3});
  assert.deepEqual(guideLoadingMarkerBox(120, 48, false), {width: 120, height: 48, radius: 12, top: 0});
  assert.deepEqual(guideLoadingMarkerBox(-4, 10, true), {width: 0, height: 4, radius: 8, top: 3});
});

test('the shared paint is held until the quiet window elapses', () => {
  assert.equal(paintSkeletonVisible(false, -1, 0), false);
  assert.equal(paintSkeletonVisible(false, 0, 0), true);
  assert.equal(paintSkeletonVisible(true, 0, 0), false);
});
