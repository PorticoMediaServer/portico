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
