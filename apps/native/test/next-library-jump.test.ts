import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryJumpLetter, libraryJumpLetters} from '../src-next/screens/library/jump.ts';

test('jump letters follow the server index order and name the first visible card', () => {
  const letters = libraryJumpLetters({'#': 0, A: 4, B: 12, C: 40});
  assert.deepEqual(letters.map(letter => letter.key), ['#', 'A', 'B', 'C']);
  assert.equal(libraryJumpLetter(letters, 0), '#');
  assert.equal(libraryJumpLetter(letters, 5), 'A');
  assert.equal(libraryJumpLetter(letters, 12), 'B', 'the first card of a letter keeps that letter selected');
  assert.equal(libraryJumpLetter(letters, 39), 'B');
  assert.equal(libraryJumpLetter(letters, 100), 'C', 'past the last index keeps the last letter');
});

test('a library without a title index offers no jump letters', () => {
  assert.deepEqual(libraryJumpLetters(undefined), []);
  assert.equal(libraryJumpLetter([], 0), undefined);
  assert.equal(libraryJumpLetter(libraryJumpLetters({}), 3), undefined);
});

test('LB-6: the A–Z rail is drawn for a title order A–Z or Z–A, filtered or not, whenever the server indexed a letter in the list', async () => {
  const {libraryAlphabetRail} = await import('../src-next/screens/library/jump.ts');
  const az = {'#': 0, A: 4, B: 12}, za = {Z: 0, Y: 3, A: 9};
  assert.equal(libraryAlphabetRail(az, 20, {field: 'title'}), true, 'A–Z');
  assert.equal(libraryAlphabetRail(za, 12, {field: 'title'}), true, 'Z–A: the server publishes the descending places');
  assert.equal(libraryAlphabetRail({D: 0, M: 2}, 5, {field: 'title'}), true, 'a filtered list keeps its letters');
  assert.equal(libraryAlphabetRail(az, 20, {field: 'added'}), false, 'another order has no letters to jump to');
  assert.equal(libraryAlphabetRail({}, 20, {field: 'title'}), false, 'no index, no rail');
  assert.equal(libraryAlphabetRail({A: 40}, 20, {field: 'title'}), false, 'a place outside the list is nothing to jump to');
  assert.equal(libraryAlphabetRail(az, undefined, {field: 'title'}), false, 'before the first page there is no rail');
});
