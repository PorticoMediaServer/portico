import test from 'node:test';
import assert from 'node:assert/strict';
import {readingDuration} from '../src/reading-manifest-facts.ts';

// A reading time is shown in whole minutes at one ordinary pace (10-reading rule 40): a short
// book is "About 1m", never seconds, and a long one rounds to the nearest minute.
test('a reading time is whole minutes, at least one', () => {
  assert.equal(readingDuration(1), 60);
  assert.equal(readingDuration(7790), 7800);
  assert.equal(readingDuration(Math.ceil(32400 * 60 / 250)), 7800);
});
