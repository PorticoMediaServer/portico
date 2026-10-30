import test from 'node:test';
import assert from 'node:assert/strict';
import {firstOfEach, keepReadable, noteSkipped, oneOf, oneOfOr, onSkipped, resetSkippedParts, skippedParts, skippedVersion, subscribeSkipped} from '../src/tolerant.ts';

test('a known value is kept and an unknown one is dropped without failing', () => {
  assert.equal(oneOf('poster', ['poster', 'square']), 'poster');
  assert.equal(oneOf('hexagon', ['poster', 'square']), undefined);
  assert.equal(oneOf(7, ['poster', 'square']), undefined);
});

test('an unknown value becomes the neutral one and is counted; an absent value is not counted', () => {
  resetSkippedParts();
  assert.equal(oneOfOr('home', 'row.artworkShape', 'hexagon', ['poster', 'square'], 'poster'), 'poster');
  assert.equal(oneOfOr('home', 'row.artworkShape', undefined, ['poster', 'square'], 'poster'), 'poster');
  assert.equal(oneOfOr('home', 'row.artworkShape', 'square', ['poster', 'square'], 'poster'), 'square');
  assert.deepEqual(skippedParts(), [{document: 'home', part: 'row.artworkShape', count: 1}]);
});

test('one unreadable row is left out and the others keep their order', () => {
  resetSkippedParts();
  const rows = keepReadable('list', 'rows', [1, 'bad', 3, null], row => {
    if (row === null) return undefined;
    if (typeof row !== 'number') throw new Error('unreadable');
    return row * 2;
  });
  assert.deepEqual(rows, [2, 6]);
  assert.deepEqual(skippedParts(), [{document: 'list', part: 'rows', count: 2}]);
});

test('a repeated row keeps its first appearance', () => {
  resetSkippedParts();
  assert.deepEqual(firstOfEach('list', 'rows', [{id: 'a', n: 1}, {id: 'b', n: 2}, {id: 'a', n: 3}], row => row.id), [{id: 'a', n: 1}, {id: 'b', n: 2}]);
  assert.deepEqual(skippedParts(), [{document: 'list', part: 'rows', count: 1}]);
});

test('the record of skipped parts is bounded and listeners hear each skip', () => {
  resetSkippedParts();
  const heard: string[] = [];
  const stop = onSkipped(part => heard.push(part.part));
  for (let i = 0; i < 100; i++) noteSkipped('doc', 'part' + i);
  stop();
  noteSkipped('doc', 'after');
  assert.equal(skippedParts().length, 64);
  assert.equal(heard.length, 100);
  assert.equal(skippedParts().at(-1)?.part, 'after');
  noteSkipped('doc', 'after', 0);
  assert.equal(skippedParts().at(-1)?.count, 1);
});

test('a screen showing the record is told when it changes, and Clear all empties it', () => {
  resetSkippedParts();
  let told = 0;
  const stop = subscribeSkipped(() => told++);
  const before = skippedVersion();
  noteSkipped('saved', 'entries', 2);
  assert.equal(told, 1);
  assert.notEqual(skippedVersion(), before);
  resetSkippedParts();
  assert.equal(told, 2);
  assert.deepEqual(skippedParts(), []);
  stop();
  noteSkipped('saved', 'entries');
  assert.equal(told, 2);
  resetSkippedParts();
});
