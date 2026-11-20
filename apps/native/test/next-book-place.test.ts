import test from 'node:test';
import assert from 'node:assert/strict';
import {bookPlace} from '../src-next/pages/shared/book/book-place.ts';

// `bookPlace` (pages/shared/book) finds where a profile is in a book's rows for every book page (touch, tablet, TV): the
// last row of the resumed file that starts at or before the resumed place, matched by what each row plays, never by its id.

const row = (id: string, startSeconds: number | undefined, extra: Record<string, unknown> = {}) =>
  ({id, kind: 'chapter', title: id, libraryId: 'books', ...(startSeconds === undefined ? {} : {playback: {itemId: 'book-1', startSeconds}}), ...extra}) as any;
const progress = (partPositionMs: number, extra: Record<string, unknown> = {}) =>
  ({bookId: 'book-1', itemId: 'book-1', partPositionMs, positionMs: partPositionMs, durationMs: 1_000_000, remainingMs: 1_000_000 - partPositionMs, ...extra}) as any;

test('the rebuilt reader\'s chapters (book:chapter:N): the chapter the place falls in, to that chapter\'s own end', () => {
  const chapters = [row('book-1:chapter:1', 0), row('book-1:chapter:2', 985), row('book-1:chapter:3', 2201), row('book-1:chapter:4', 3561)];
  // The demo's A Study in Scarlet: the resumed chapter is index 2, number 3; its id is book:chapter:3, not book:2.
  const place = bookPlace(chapters, progress(2_207_000, {chapter: {index: 2, number: 3, title: '1-3', startMs: 2_201_039, endMs: 3_561_039}}))!;
  assert.deepEqual([place.index, place.id], [2, 'book-1:chapter:3']);
  assert.ok(Math.abs(place.fraction! - (2_207_000 - 2_201_000) / (3_561_039 - 2_201_000)) < .01);
  assert.ok(Math.abs(place.secondsLeft! - (3_561_039 - 2_207_000) / 1000) < 1);
});

test('the rebuilt reader\'s parts (book:part:N): the part the place falls in, to the next part\'s start, the last to its own length', () => {
  const parts = [row('book-1:part:1', 0), row('book-1:part:2', 400), row('book-1:part:3', 800, {duration: 200})];
  assert.deepEqual(bookPlace(parts, progress(530_000)), {index: 1, id: 'book-1:part:2', fraction: (530 - 400) / 400, secondsLeft: 270});
  assert.deepEqual(bookPlace(parts, progress(900_000)), {index: 2, id: 'book-1:part:3', fraction: .5, secondsLeft: 100});
  // A part with no start the reader could measure is never the place.
  assert.equal(bookPlace([row('book-1:part:1', undefined)], progress(10_000)), undefined);
});

test('no place: no progress, a place before the first row, or rows of another file; half a second of slack at a row\'s start', () => {
  const chapters = [row('book-1:chapter:1', 100), row('book-1:chapter:2', 200)];
  assert.equal(bookPlace(chapters, undefined), undefined);
  assert.equal(bookPlace(chapters, progress(50_000)), undefined);
  assert.equal(bookPlace(chapters, progress(150_000, {itemId: 'file-9'})), undefined);
  assert.equal(bookPlace(chapters, progress(199_700))!.index, 1, 'a resume saved at a chapter\'s first frame lands in it');
  // The last row with nothing after it and no length: found, with no measured progress.
  assert.deepEqual(bookPlace(chapters, progress(250_000)), {index: 1, id: 'book-1:chapter:2'});
});
