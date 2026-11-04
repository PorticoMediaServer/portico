import {test} from 'node:test';
import assert from 'node:assert/strict';
import {observedProgress, readKeyTurn, readTapTurn, readerToolbarOrder, readingDirection} from '../../src/kit/reader/interaction.ts';

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

test('RD-9: a comic keeps its chosen direction, vertical included; other books follow their file', () => {
  assert.equal(readingDirection(true, 'vertical', 'ltr'), 'vertical', 'a vertical default is not read as a left-to-right paged comic');
  assert.equal(readingDirection(true, 'rtl', 'ltr'), 'rtl');
  assert.equal(readingDirection(true, 'file', 'rtl'), 'rtl');
  assert.equal(readingDirection(true, 'file', undefined), 'ltr');
  assert.equal(readingDirection(false, 'vertical', 'rtl'), 'rtl', 'the comic choice never applies to an EPUB or PDF');
  assert.equal(readKeyTurn('ArrowDown', 'vertical'), 'next');
  assert.equal(readKeyTurn('ArrowUp', 'vertical'), 'previous');
  assert.equal(readKeyTurn('ArrowDown', 'ltr'), undefined, 'only a vertical strip turns with up and down');
});

test('desktop-read 6, 9 and 10: each reader\'s toolbar runs in its frame\'s order', () => {
  const ids = (list: string[], kind?: 'epub' | 'pdf' | 'comic') => readerToolbarOrder(list.map(id => ({id})), kind).map(action => action.id);
  assert.deepEqual(ids(['bookmark', 'contents', 'search', 'display', 'fullscreen']), ['contents', 'search', 'bookmark', 'display', 'fullscreen'], 'EPUB (frame 6)');
  assert.deepEqual(ids(['bookmark', 'contents', 'search', 'pages', 'zoom-out', 'zoom-in', 'layout', 'zoom', 'fullscreen'], 'pdf'),
    ['zoom-out', 'zoom', 'zoom-in', 'pages', 'layout', 'contents', 'search', 'bookmark', 'fullscreen'], 'PDF (frame 10)');
  assert.deepEqual(ids(['bookmark', 'contents', 'layout', 'zoom', 'direction', 'fullscreen'], 'comic'), ['zoom', 'layout', 'direction', 'contents', 'bookmark', 'fullscreen'], 'comic (frame 9)');
  assert.deepEqual(ids(['search', 'bookmark', 'more'], 'pdf'), ['search', 'bookmark', 'more'], 'a compact document keeps More last');
  const pdf = readerToolbarOrder([{id: 'zoom'}, {id: 'pages'}, {id: 'fullscreen', separatorBefore: true}], 'pdf');
  assert.deepEqual(pdf.map(action => !!action.separatorBefore), [false, true, true], 'a rule after the PDF zoom group');
});
