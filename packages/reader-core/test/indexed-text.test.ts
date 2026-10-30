import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ReaderError} from '../src/errors.ts';
import {goSpace, indexedNormalize} from '../src/epub/indexed-text.ts';

const active = () => new AbortController().signal;
const deadline = () => Date.now() + 1000;
const normalize = (raw: string, starts: readonly number[], ends: readonly number[], signal = active(), at = deadline()) => indexedNormalize(raw, starts, ends, signal, at);

test('leading synthetic block space is trimmed without an inner space', () => {
  const result = normalize(' abc', [0], [3])!;
  assert.equal(result.text, 'abc');
  assert.equal(result.startRaw.get(0), 1);
  assert.equal(result.endRaw.get(3), 4);
});

test('offset zero and the text end map independently', () => {
  const result = normalize('abc', [0], [3])!;
  assert.equal(result.startRaw.get(0), 0);
  assert.equal(result.endRaw.get(3), 3);
  assert.equal(result.startRaw.get(3), undefined);
  assert.equal(result.endRaw.get(0), undefined);
});

test('a missing sibling target does not invalidate the valid one', () => {
  const result = normalize('ab cd', [0, 99], [2, 5])!;
  assert.equal(result.startRaw.get(0), 0);
  assert.equal(result.startRaw.has(99), false);
  assert.equal(result.endRaw.get(2), 2);
  assert.equal(result.endRaw.get(5), 5);
});

test('inline and block boundary spaces normalize identically', () => {
  assert.equal(normalize('a b', [0], [3])!.text, 'a b');
  assert.equal(normalize('a  b', [0], [3])!.text, 'a b');
  assert.equal(normalize(' a  b ', [0], [3])!.text, 'a b');
  assert.equal(normalize('ab', [0], [2])!.text, 'ab');
});

test('Go whitespace includes NEL and NBSP but not BOM', () => {
  assert.equal(goSpace(0x85), true);
  assert.equal(goSpace(0xa0), true);
  assert.equal(goSpace(0xfeff), false);
  assert.equal(normalize('a\u0085b', [0], [3])!.text, 'a b');
  assert.equal(normalize('a\u00a0b', [0], [3])!.text, 'a b');
  assert.equal(normalize('a\uFEFFb', [0], [3])!.text, 'a\uFEFFb');
});

test('supplementary code points keep exact UTF-16 endpoints', () => {
  const result = normalize('😀x', [0], [2])!;
  assert.equal(result.text, '😀x');
  assert.equal(result.text.length, 3);
  assert.equal(result.startRaw.get(0), 0);
  assert.equal(result.endRaw.get(2), 2);
});

test('aborted or budget-limited normalization declines', () => {
  const controller = new AbortController(); controller.abort();
  assert.throws(() => normalize('abc', [0], [3], controller.signal), (error: unknown) => error instanceof ReaderError && error.code === 'aborted');
  assert.equal(normalize('abc', [0], [3], active(), 0), null);
});
