import assert from 'node:assert/strict';
import test from 'node:test';
import {homeSeeAllQuickIds, homeSeeAllQuickOrder} from '../src-next/screens/home/see-all-model.ts';

test('See all keeps the three drawn quick toggles and leaves My List in Filters', () => {
  assert.deepEqual([...homeSeeAllQuickIds], ['unwatched', 'in-progress', 'favorites']);
  assert.equal(homeSeeAllQuickOrder('unwatched'), true);
  assert.equal(homeSeeAllQuickOrder('in-progress'), true);
  assert.equal(homeSeeAllQuickOrder('favorites'), true);
  assert.equal(homeSeeAllQuickOrder('watchlist'), false);
  assert.equal(homeSeeAllQuickOrder('all'), false);
});

test('TV See all keeps the two playback-state toggles drawn in collections#48', () => {
  assert.equal(homeSeeAllQuickOrder('unwatched', true), true);
  assert.equal(homeSeeAllQuickOrder('in-progress', true), true);
  assert.equal(homeSeeAllQuickOrder('favorites', true), false);
  assert.equal(homeSeeAllQuickOrder('watchlist', true), false);
});
