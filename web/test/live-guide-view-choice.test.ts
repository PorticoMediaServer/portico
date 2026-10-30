import test from 'node:test';
import assert from 'node:assert/strict';
import {guideViewChoice} from '../src/screens/live/guide-view-choice.ts';
import {guideListRowHeight, guideVisibleRows} from '../src/screens/live/guide-window.ts';

test('First-use guide presentation follows portrait and landscape windows', () => {
  assert.equal(guideViewChoice(undefined, undefined, 390, 844), 'list');
  assert.equal(guideViewChoice(undefined, undefined, 844, 390), 'guide');
  assert.equal(guideViewChoice(undefined, undefined, 1440, 900), 'guide');
});

test('Explicit and remembered guide choices survive orientation changes', () => {
  assert.equal(guideViewChoice('guide', 'list', 390, 844), 'guide');
  assert.equal(guideViewChoice('list', 'guide', 844, 390), 'list');
  assert.equal(guideViewChoice(undefined, 'guide', 390, 844), 'guide');
  assert.equal(guideViewChoice(undefined, 'list', 844, 390), 'list');
});

test('List scrolling requests the compact or desktop row window beyond the first page', () => {
  const compact = guideListRowHeight(true), desktop = guideListRowHeight(false);
  assert.deepEqual(guideVisibleRows(-compact * 30, compact * 5, compact, 100), {first: 29, last: 37});
  assert.deepEqual(guideVisibleRows(-desktop * 80, desktop * 5, desktop, 100), {first: 79, last: 87});
  assert.deepEqual(guideVisibleRows(-compact * 99, compact * 5, compact, 100), {first: 98, last: 99});
  assert.deepEqual(guideVisibleRows(0, 844, compact, 0), {first: 0, last: 0});
});
