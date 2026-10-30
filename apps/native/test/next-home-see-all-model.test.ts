import assert from 'node:assert/strict';
import test from 'node:test';
import {homeSeeAllCardWidth, homeSeeAllQuickIds, homeSeeAllQuickOrder} from '../src-next/screens/home/see-all-model.ts';

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


test('compact See all fits three default posters at every supported phone width and honors density scaling', () => {
  for (const surface of [320, 375, 390, 430, 599]) {
    const width = homeSeeAllCardWidth(surface, 20, 118, 1, true);
    assert.equal(Math.floor((surface - 40 + 10) / (width + 10)), 3);
    assert.ok(3 * width + 20 <= surface - 40);
  }
  assert.equal(homeSeeAllCardWidth(390, 20, 118, 1.25, true), 137);
  assert.equal(homeSeeAllCardWidth(390, 20, 118, .85, true), 93);
  assert.equal(homeSeeAllCardWidth(834, 28, 140, 1, false), 140, 'tablet keeps its platform width');
  assert.equal(homeSeeAllCardWidth(1920, 200, 200, 1, false), 200, 'TV remains unchanged');
});
