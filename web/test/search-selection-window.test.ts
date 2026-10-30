import test from 'node:test';
import assert from 'node:assert/strict';
import {displayedSearchEntries} from '../src/screens/search/selection-window.ts';

test('a continued shelf admits its actual later viewport without retaining its first sixty entries', () => {
  const entries = Array.from({length: 140}, (_, index) => Object.freeze({id: String(index)}));
  const first = displayedSearchEntries(entries, {count: 140, first: 0, last: 6}, true);
  const continued = displayedSearchEntries(entries, {count: 140, first: 90, last: 96}, true);
  assert.deepEqual(first, entries.slice(0, 7));
  assert.deepEqual(continued, entries.slice(90, 97));
  assert.strictEqual(continued[0], entries[90]);
  assert.equal(continued.some(entry => first.includes(entry)), false);
  assert.equal(entries.length, 140, 'the canonical admitted answer is unchanged');
});

test('no viewport is invented before measurement, offscreen, or after a successor changes the measured count', () => {
  const entries = Array.from({length: 80}, (_, index) => ({id: index}));
  assert.deepEqual(displayedSearchEntries(entries, undefined, true), []);
  assert.deepEqual(displayedSearchEntries(entries, {count: 80, first: 70, last: 77}, false), []);
  assert.deepEqual(displayedSearchEntries(entries, {count: 40, first: 0, last: 6}, true), []);
  assert.deepEqual(displayedSearchEntries(entries, {count: 80, first: -1, last: -1}, true), []);
  assert.deepEqual(displayedSearchEntries(entries, {count: 80, first: 79, last: 80}, true), [entries[79]], 'a pending slot never becomes an entity');
});

test('Reading parts contribute only their own observed entries and keep canonical object order', () => {
  const books = Array.from({length: 90}, (_, index) => ({id: 'book-' + index})), issues = [{id: 'issue'}];
  const shownBooks = displayedSearchEntries(books, {count: 90, first: 75, last: 78}, true);
  const offscreenIssues = displayedSearchEntries(issues, {count: 1, first: 0, last: 0}, false);
  assert.deepEqual([...shownBooks, ...offscreenIssues], books.slice(75, 79));
  assert.deepEqual(displayedSearchEntries(issues, {count: 1, first: 0, last: 0}, true), issues);
});
