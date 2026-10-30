import {test} from 'node:test';
import assert from 'node:assert/strict';
import {observedProgress, readKeyTurn, readTapTurn} from '../../src/kit/reader/interaction.ts';

test('the same physical arrow reverses between Western comics and manga', () => {
  assert.equal(readKeyTurn('ArrowLeft', 'ltr'), 'previous');
  assert.equal(readKeyTurn('ArrowLeft', 'rtl'), 'next');
  assert.equal(readKeyTurn('ArrowRight', 'ltr'), 'next');
  assert.equal(readKeyTurn('ArrowRight', 'rtl'), 'previous');
  // Semantic next/previous keys keep their meaning after direction switches.
  for (const direction of ['ltr', 'rtl', 'vertical'] as const) {
    assert.equal(readKeyTurn('PageDown', direction), 'next');
    assert.equal(readKeyTurn('PageUp', direction), 'previous');
    assert.equal(readKeyTurn(' ', direction, true), 'previous');
    assert.equal(readKeyTurn(' ', direction), 'next');
    assert.equal(readKeyTurn('Escape', direction), undefined);
    assert.equal(readKeyTurn('ArrowLeft', direction, true), undefined);
    assert.equal(readKeyTurn('ArrowRight', direction, true), undefined);
    assert.equal(readKeyTurn('PageDown', direction, true), undefined);
  }
});

test('tap thirds mirror at the edges and keep the centre for chrome', () => {
  for (const direction of ['ltr', 'rtl'] as const) {
    assert.equal(readTapTurn(1 / 3, direction), 'chrome');
    assert.equal(readTapTurn(.5, direction), 'chrome');
    assert.equal(readTapTurn(2 / 3, direction), 'chrome');
  }
  assert.equal(readTapTurn(0, 'ltr'), 'previous');
  assert.equal(readTapTurn(1, 'ltr'), 'next');
  assert.equal(readTapTurn(0, 'rtl'), 'next');
  assert.equal(readTapTurn(1, 'rtl'), 'previous');
  for (const invalid of [-1, 2, Infinity, NaN]) assert.equal(readTapTurn(invalid, 'ltr'), undefined);
});

test('missing or malformed position never turns into fabricated progress', () => {
  for (const invalid of [undefined, NaN, Infinity, -Infinity, -.01, 1.01]) assert.equal(observedProgress(invalid), undefined);
  assert.equal(observedProgress(0), 0);
  assert.equal(observedProgress(.46), .46);
  assert.equal(observedProgress(1), 1);
});
