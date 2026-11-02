import assert from 'node:assert/strict';
import {test} from 'node:test';
import {defaultI18n} from '../../i18n/src/index.ts';
import {continueBookFacts} from '../src/presentation/continue-books.ts';
import type {ContentEntry} from '../src/library-content.ts';

const t = defaultI18n.t.bind(defaultI18n);
const entry = (v: Partial<ContentEntry>) => ({id: 'x', kind: 'ebook', title: 'T', ...v}) as ContentEntry;

test('a paged title shows its page of the known count and pages left (tablet-read 11)', () => {
  const facts = continueBookFacts(entry({kind: 'ebook', publicationType: 'magazine', pageCount: 96, readingProgress: {progression: .64, pageIndex: 61}}), t);
  assert.deepEqual(facts, {reading: true, shape: 'magazine', progress: .64, where: 'Page 62 of 96', left: '34 pages left'});
});

test('an EPUB shows its progress line only, no invented chapter or time; a page without a count shows the page only', () => {
  assert.deepEqual(continueBookFacts(entry({readingProgress: {progression: .3}}), t), {reading: true, shape: 'book', progress: .3});
  assert.deepEqual(continueBookFacts(entry({kind: 'comic', readingProgress: {progression: .5, pageIndex: 4}}), t), {reading: true, shape: 'book', progress: .5, where: 'Page 5'});
  assert.equal(continueBookFacts(entry({pageCount: 10, readingProgress: {progression: .99, pageIndex: 9}}), t).left, undefined);
});

test('a stale saved position is progress only, never a current place', () => {
  assert.deepEqual(continueBookFacts(entry({pageCount: 96, readingProgress: {progression: .4, pageIndex: 10, stale: true}}), t), {reading: true, shape: 'book', progress: .4});
});

test('an audiobook shows its chapter and time left', () => {
  const facts = continueBookFacts(entry({kind: 'book', bookProgress: {bookId: 'x', itemId: 'p', partPositionMs: 1, positionMs: 3_600_000, durationMs: 36_720_000, remainingMs: 33_120_000, chapter: {index: 4, title: 'Chapter 5', startMs: 0, endMs: 1}}}), t);
  assert.deepEqual(facts, {reading: false, shape: 'audiobook', progress: 3_600_000 / 36_720_000, where: 'Chapter 5', left: '9 h 12 min left'});
});
