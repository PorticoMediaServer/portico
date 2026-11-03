import test from 'node:test';
import assert from 'node:assert/strict';
import {homeContinuationCard, homeContinuationSource} from '../src/presentation/home-continuation.ts';
import {cardText} from '../src/presentation/kit-model/index.ts';
import {contentEntrySubject} from '../src/presentation/content-entry-subject.ts';
import {defaultI18n} from '../../i18n/src/index.ts';
import type {ContentEntry} from '../src/library-content.ts';

const env = {t: defaultI18n.t, locale: 'en-US'};
const book = {id: 'hobbit', kind: 'book', title: 'The Hobbit', author: 'J. R. R. Tolkien', libraryId: 'books', navigation: {view: 'book', entityId: 'hobbit'},
  duration: 40000, progressSeconds: 23680,
  bookProgress: {bookId: 'hobbit', itemId: 'hobbit-part-2', partPositionMs: 3_680_000, positionMs: 23_680_000, durationMs: 40_000_000, remainingMs: 16_320_000,
    chapter: {index: 1, number: 5, title: 'Chapter 5', startMs: 3_000_000, endMs: 4_000_000}}} as unknown as ContentEntry;
/** The row's resume target: the current part, with the part's own duration and position. */
const part = {id: 'hobbit-part-2', kind: 'audiobook_file', title: 'The Hobbit, part 2', libraryId: 'books', navigation: {view: 'item', entityId: 'hobbit-part-2'},
  duration: 6000, progressSeconds: 3680} as unknown as ContentEntry;
const album = {id: 'after-hours', kind: 'album', title: 'After Hours', artist: {id: 'weeknd', name: 'The Weeknd'}, year: 2020, libraryId: 'music', navigation: {view: 'album', entityId: 'after-hours'}} as unknown as ContentEntry;
const text = (entry: ContentEntry, continuation: boolean) => cardText(contentEntrySubject(entry), continuation ? 'continuation' : 'ordinary', env);

test('Continue Listening draws the time left where the author sits, with the title and identity kept (HM-4, phone-app f2)', () => {
  const card = homeContinuationCard('listen_next', text(book, false), text(book, true));
  assert.equal(card.title, 'The Hobbit');
  assert.deepEqual(card.caption.map(part => part.id), ['remaining']);
  assert.deepEqual(card.detail, []);
  assert.ok(card.progress !== undefined && card.progress > 0.5 && card.progress < 0.7);
});

test('Continue your books draws "Chapter N · time left" across the whole book, never the part\'s (W2-A6, tablet-read f11)', () => {
  const source = homeContinuationSource(book, part);
  assert.equal(source, book, 'a book with whole-book progress is read itself, not its current part');
  const card = homeContinuationCard('continue_books', text(book, false), text(source, true));
  assert.deepEqual(card.caption.map(entry => entry.id), ['chapter', 'remaining']);
  assert.equal(card.caption[0]!.text, defaultI18n.t('next.kit.chapter', {number: 5}), 'the whole-book number, not the part index');
  assert.equal(card.caption[1]!.text, text(book, true).detail.find(entry => entry.id === 'remaining')!.text);
  assert.notEqual(card.caption[1]!.text, text(part, true).detail.find(entry => entry.id === 'remaining')?.text, 'not the current part\'s time left');
  assert.equal(homeContinuationSource(part, part), part, 'anything else reads the resume target');
});

test('nothing is guessed: no known time left, or another row, keeps the card as it was', () => {
  const ordinary = text(album, false);
  assert.equal(homeContinuationCard('listen_next', ordinary, text(album, true)), ordinary, 'an album with no resume keeps artist and year');
  const bookCard = text(book, false);
  assert.equal(homeContinuationCard('new_on_server', bookCard, text(book, true)), bookCard);
});
