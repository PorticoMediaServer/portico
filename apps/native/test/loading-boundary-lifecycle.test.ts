/**
 * The loading boundary's drawn order, exercised as a pure transfer function:
 * cached content never waits, a fresh loading cycle restarts the quiet window,
 * the quiet window reveals the shape once, and the content fade follows the
 * shared reduced-motion duration.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {cachedAfterLoadingCycle, contentFadeDuration, nextLoadingPhase, paintSkeletonVisible} from '../src-next/kit/states/loading-boundary-model.ts';

test('cached content mounts straight into content, uncached waits quiet', () => {
  assert.equal(nextLoadingPhase('quiet', {type: 'mounted', ready: true, cached: true}), 'content');
  assert.equal(nextLoadingPhase('quiet', {type: 'mounted', ready: true, cached: false}), 'quiet');
  assert.equal(nextLoadingPhase('quiet', {type: 'mounted', ready: false, cached: false}), 'quiet');
});

test('the quiet window reveals the shape once and never overrides content', () => {
  assert.equal(nextLoadingPhase('quiet', {type: 'quietElapsed'}), 'skeleton');
  assert.equal(nextLoadingPhase('skeleton', {type: 'quietElapsed'}), 'skeleton');
  assert.equal(nextLoadingPhase('content', {type: 'quietElapsed'}), 'content', 'a late timer never replaces content');
});

test('a ready change owns the transition, and a new loading cycle returns to quiet', () => {
  assert.equal(nextLoadingPhase('skeleton', {type: 'readyChanged', ready: true}), 'content');
  assert.equal(nextLoadingPhase('quiet', {type: 'readyChanged', ready: true}), 'content', 'fast ready skips the shape');
  assert.equal(nextLoadingPhase('content', {type: 'readyChanged', ready: false}), 'quiet', 'reloading restarts the quiet window');
  assert.equal(nextLoadingPhase('skeleton', {type: 'readyChanged', ready: false}), 'quiet');
});

test('a cached mount only skips the first cycle, and a later load fades in', () => {
  assert.equal(cachedAfterLoadingCycle(true, true), true);
  assert.equal(cachedAfterLoadingCycle(true, false), false, 'a new loading cycle clears the cached witness');
  assert.equal(cachedAfterLoadingCycle(false, true), false, 'later content is always faded');
});

test('the content fade uses the shared reduced duration', () => {
  assert.equal(contentFadeDuration(false), 180);
  assert.equal(contentFadeDuration(true), 160);
  assert.equal(contentFadeDuration(true, 180, 160), 160);
});

test('a stale quiet completion never flashes a skeleton onto a new cycle', () => {
  assert.equal(paintSkeletonVisible(true, 0, 1), false, 'ready never paints a shape');
  assert.equal(paintSkeletonVisible(false, 0, 0), true, 'the same cycle may paint');
  assert.equal(paintSkeletonVisible(false, 0, 1), false, 'a previous cycle completion is stale');
  assert.equal(paintSkeletonVisible(false, 1, 1), true, 'the timer for this cycle completed');
});
