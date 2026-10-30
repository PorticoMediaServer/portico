import {test} from 'node:test';
import assert from 'node:assert/strict';
import {generatedCoverTint, type GeneratedCoverTint} from '../src/index.ts';

const palette = {
  a: {first: '#2f86bb', second: '#1e5b52'},
  b: {first: '#b4642a', second: '#5e2a22'},
  c: {first: '#8f2e24', second: '#3f2150'},
  d: {first: '#2e6a6f', second: '#1f4f7a'},
  e: {first: '#55709a', second: '#2a3a66'},
  f: {first: '#3f8f6e', second: '#153749'},
  g: {first: '#6a58a8', second: '#26305f'},
  h: {first: '#a8892a', second: '#5a3a1a'},
} as const satisfies Record<string, GeneratedCoverTint>;

test('all eight exact design pairs are reachable in the specified palette order', () => {
  const vectors = [
    ['e', 'a'], ['f', 'b'], ['c', 'c'], ['d', 'd'],
    ['a', 'e'], ['b', 'f'], ['g', 'g'], ['h', 'h'],
  ] as const;
  for (const [title, tint] of vectors) assert.deepEqual(generatedCoverTint(title), palette[tint]);
});

test('UTF-16 vectors preserve supplementary characters, combining forms and empty titles', () => {
  const vectors = [
    ['', 'f'], ['\u00e9', 'e'], ['e\u0301', 'd'],
    ['\u{1f600}', 'a'], ['\u{1f600}a', 'd'], ['\ud83d', 'a'], ['\ude00', 'h'],
  ] as const;
  for (const [title, tint] of vectors) assert.deepEqual(generatedCoverTint(title), palette[tint]);
});

test('multi-character vectors keep order, leading whitespace and integer wrapping stable', () => {
  const vectors = [
    ['hello', 'd'], ['foobar', 'a'], ['ab', 'c'], ['ba', 'e'], [' a', 'c'], ['\u0000', 'h'],
  ] as const;
  for (const [title, tint] of vectors) assert.deepEqual(generatedCoverTint(title), palette[tint]);
});

test('the exact title gives the same tint regardless of intervening calls or title length', () => {
  const title = 'a'.repeat(100_001) + '\u{1f600}';
  const expected = generatedCoverTint(title);
  for (const other of ['', '\u00e9', 'e\u0301', 'h', 'a']) generatedCoverTint(other);
  assert.deepEqual(generatedCoverTint(title), expected);
  assert.ok(Object.values(palette).some(tint => tint.first === expected.first && tint.second === expected.second));
});

test('a consumer cannot mutate a palette pair and change another cover', () => {
  const tint = generatedCoverTint('e');
  assert.equal(Object.isFrozen(tint), true);
  assert.throws(() => Object.assign(tint, {first: '#000000'}), TypeError);
  assert.deepEqual(generatedCoverTint('e'), palette.a);
});
