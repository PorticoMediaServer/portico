import test from 'node:test';
import assert from 'node:assert/strict';
import {personalFieldLabel, personalChoiceLabel, personalChoiceDate} from '../src/personal-labels.ts';

test('field labels use US spelling and fall back for unknown fields', () => {
  assert.equal(personalFieldLabel('watchlisted'), 'My List');
  assert.equal(personalFieldLabel('watchlist'), 'My List');
  assert.equal(personalFieldLabel('favorite'), 'Favorite');
  assert.equal(personalFieldLabel('watched'), 'Watched status');
  assert.equal(personalFieldLabel('rating'), 'Rating');
  assert.equal(personalFieldLabel(''), 'Saved preference');
  assert.equal(personalFieldLabel('something-new'), 'Saved preference');
});

test('choice labels describe ratings and booleans', () => {
  assert.equal(personalChoiceLabel('rating', null), 'Not rated');
  // Ratings are kept from 0 to 10 and shown as stars out of five.
  assert.equal(personalChoiceLabel('rating', 8), '4 / 5');
  assert.equal(personalChoiceLabel('rating', 9), '4.5 / 5');
  assert.equal(personalChoiceLabel('rating', Number.NaN), 'Saved rating');
  assert.equal(personalChoiceLabel('watchlisted', true), 'In My List');
  assert.equal(personalChoiceLabel('watchlisted', false), 'Not in My List');
  assert.equal(personalChoiceLabel('favorite', true), 'Favorite');
  assert.equal(personalChoiceLabel('favorite', false), 'Not a favorite');
  assert.equal(personalChoiceLabel('watched', true), 'Watched');
  assert.equal(personalChoiceLabel('watched', false), 'Unwatched');
  assert.equal(personalChoiceLabel('other', true), 'On');
  assert.equal(personalChoiceLabel('other', false), 'Off');
  assert.equal(personalChoiceLabel('other', null), 'Not set');
  assert.equal(personalChoiceLabel('other', 'x'), 'Saved choice');
});

test('choice dates degrade to a device label on garbage input', () => {
  const good = personalChoiceDate('2026-09-01T12:00:00Z');
  assert.ok(good && good !== 'Another device');
  assert.equal(personalChoiceDate('not-a-date'), 'Another device');
  assert.equal(personalChoiceDate(''), 'Another device');
});
