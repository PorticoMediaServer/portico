import assert from 'node:assert/strict';
import {test} from 'node:test';
import {isWholeWordPhrase} from '../src/search-boundary.ts';

test('a word is not matched inside a longer word', () => {
  assert.equal(isWholeWordPhrase('sometimes', 0, 4), false);
  assert.equal(isWholeWordPhrase('some times', 5, 9), false);
  assert.equal(isWholeWordPhrase('foo_bar', 0, 3), false);
  assert.equal(isWholeWordPhrase('foo', 0, 3), true);
  assert.equal(isWholeWordPhrase(' foo ', 1, 4), true);
});

test('apostrophes join only an internal word', () => {
  assert.equal(isWholeWordPhrase("don't stop", 0, 5), true);
  assert.equal(isWholeWordPhrase("xdon't stop", 1, 6), false);
  assert.equal(isWholeWordPhrase("don'tstop", 0, 5), false);
  assert.equal(isWholeWordPhrase("'tis", 0, 4), true);
  assert.equal(isWholeWordPhrase("x'tis", 1, 5), false);
  assert.equal(isWholeWordPhrase("readers' club", 0, 8), true);
  assert.equal(isWholeWordPhrase("readers'club", 0, 8), false);
});

test('Unicode letter and combining-mark words keep whole-token edges', () => {
  assert.equal(isWholeWordPhrase('привет мир', 0, 6), true);
  const decomposed = 'cafe\u0301';
  assert.equal(isWholeWordPhrase(decomposed, 0, 4), false);
  assert.equal(isWholeWordPhrase(decomposed, 0, 5), true);
  // Scripts without deterministic word segmentation stay one token rather than
  // inventing a dictionary boundary.
  assert.equal(isWholeWordPhrase('漢字', 0, 1), false);
  assert.equal(isWholeWordPhrase('漢字', 0, 2), true);
});

test('punctuation-only spans are literal matches, not guessed words', () => {
  assert.equal(isWholeWordPhrase('a.b', 1, 2), true);
  assert.equal(isWholeWordPhrase('a, b', 1, 2), true);
});
