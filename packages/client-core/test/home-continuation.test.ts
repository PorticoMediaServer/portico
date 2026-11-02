import test from 'node:test';
import assert from 'node:assert/strict';
import {homeContinuationCard} from '../src/presentation/home-continuation.ts';
import {cardText} from '../src/presentation/kit-model/index.ts';
import {contentEntrySubject} from '../src/presentation/content-entry-subject.ts';
import {defaultI18n} from '../../i18n/src/index.ts';
import type {ContentEntry} from '../src/library-content.ts';

const env = {t: defaultI18n.t, locale: 'en-US'};
const book = {id: 'hobbit', kind: 'book', title: 'The Hobbit', author: 'J. R. R. Tolkien', libraryId: 'books', navigation: {view: 'book', entityId: 'hobbit'},
  duration: 40000, progressSeconds: 23680, bookProgress: {chapterNumber: 5, chapterCount: 19, positionMs: 23680000, durationMs: 40000000}} as unknown as ContentEntry;
const album = {id: 'after-hours', kind: 'album', title: 'After Hours', artist: {id: 'weeknd', name: 'The Weeknd'}, year: 2020, libraryId: 'music', navigation: {view: 'album', entityId: 'after-hours'}} as unknown as ContentEntry;
const text = (entry: ContentEntry, continuation: boolean) => cardText(contentEntrySubject(entry), continuation ? 'continuation' : 'ordinary', env);

test('Continue Listening draws the time left where the author sits, with the title and identity kept (HM-4, phone-app f2)', () => {
  const card = homeContinuationCard('listen_next', text(book, false), text(book, true));
  assert.equal(card.title, 'The Hobbit');
  assert.deepEqual(card.caption.map(part => part.id), ['remaining']);
  assert.deepEqual(card.detail, []);
  assert.ok(card.progress !== undefined && card.progress > 0.5 && card.progress < 0.7);
});

test('Continue your books draws the chapter (when the card knows it) and the time left (tablet-read f11)', () => {
  assert.deepEqual(homeContinuationCard('continue_books', text(book, false), text(book, true)).caption.map(part => part.id), ['remaining'],
    'a part-relative chapter index is not a book chapter number, so none is drawn');
  const continued = text(book, true);
  const withChapter = {...continued, detail: [{id: 'chapter', text: 'Chapter 5', role: 'context' as const}, ...continued.detail]};
  assert.deepEqual(homeContinuationCard('continue_books', text(book, false), withChapter).caption.map(part => part.text), ['Chapter 5', continued.detail.find(part => part.id === 'remaining')!.text]);
});

test('nothing is guessed: no known time left, or another row, keeps the card as it was', () => {
  const ordinary = text(album, false);
  assert.equal(homeContinuationCard('listen_next', ordinary, text(album, true)), ordinary, 'an album with no resume keeps artist and year');
  const bookCard = text(book, false);
  assert.equal(homeContinuationCard('new_on_server', bookCard, text(book, true)), bookCard);
});
