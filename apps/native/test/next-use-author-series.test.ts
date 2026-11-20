import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import * as kitText from '../../../packages/client-core/src/presentation/kit-model/text';
import {audiobookAuthorInitials} from '../src-next/screens/destinations/audiobooks/presentation.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// `useAuthorSeries` is what an author's or a series' page shows on a phone (collections 41b, 41c), from the audiobook
// controller the old author / series screen read: this holds what it shows and what each control does.

const t = defaultI18n.t;
const martian = {id: 'b1', kind: 'book', title: 'The Martian', libraryId: 'books', posterUrl: '/m.jpg', narrators: ['R. C. Bray'], author: 'Andy Weir',
  series: [{name: 'Mars', position: '1'}], bookProgress: {bookId: 'b1', itemId: 'f1', partPositionMs: 1, positionMs: 1, durationMs: 2, remainingMs: 16_440_000}} as any;
const hail = {id: 'b2', kind: 'book', title: 'Project Hail Mary', libraryId: 'books', posterUrl: '/h.jpg', narrators: ['Ray Porter'], author: 'Andy Weir'} as any;
const authorEntry = {id: 'a1', kind: 'author', title: 'Andy Weir', libraryId: 'books'};

async function page(kind: 'author' | 'book_series', over: Record<string, unknown> = {}) {
  const calls: unknown[][] = [], h = hooks();
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]); return Promise.resolve();};
  const {useAuthorSeries} = await screenModule(new URL('../src-next/pages/shared/book/useAuthorSeries.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/errors': errors,
    '../../../../../../packages/client-core/src/presentation/kit-model/text': kitText,
    '../../../screens/destinations/audiobooks/presentation': {audiobookAuthorInitials}});
  const entry = {id: 'x', kind, title: kind === 'author' ? 'Andy Weir' : 'Mars', libraryId: 'books', count: 2, duration: 97_380} as any;
  const snapshot = {phase: 'ready', finished: false, entity: entry, listening: {resume: {itemId: 'f1', positionSeconds: 1}},
    sections: [{id: 'books', entries: [martian, hail], totalCount: 2}, ...(kind === 'book_series' ? [{id: 'context', entries: [authorEntry]}] : [])], ...over};
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, load: record('load'), next: record('next'), recover: record('recover'), resumeBook: record('resumeBook')};
  const view = h.render(() => useAuthorSeries({controller, entry, runtime: {artwork: (path?: string) => path ? {uri: path} : undefined, ambient: (path?: string) => path ? {url: path} : undefined,
    reportError: record('error')}, actions: {menu: (...args: unknown[]) => {calls.push(['menu', ...args]);}}, openEntry: record('open'), onBack: record('back')}));
  return {view, calls, entry};
}

test('an author: the circle of their books\' covers with their initials, their count and length, Resume the book they are in, and Details', async () => {
  const {view, calls} = await page('author');
  assert.equal(view.series, false);
  assert.deepEqual([view.kicker, view.initials], [t('title.kind', {kind: 'author'}), audiobookAuthorInitials('Andy Weir')]);
  assert.deepEqual(view.covers, [{uri: '/m.jpg'}, {uri: '/h.jpg'}]);
  assert.deepEqual(view.facts.map((fact: any) => fact.text), [t('next.kit.books', {count: 2}), kitText.durationText(97_380, t)]);
  assert.equal(view.primary.label, t('next.browse.resume', {title: 'The Martian'}));
  view.primary.onPress(); assert.deepEqual(calls.at(-1), ['resumeBook', martian]);
  assert.equal(view.startOver, undefined, 'an author has no Start over');
  assert.deepEqual(view.details.map((row: any) => [row.label, row.values[0].text]),
    [[t('next.kit.narrators'), defaultI18n.list(['R. C. Bray', 'Ray Porter'])], [t('next.kit.length'), kitText.durationText(97_380, t)]]);
});

test('the books: who read each and the time left, a series position only when the server sent one; a row opens its book, its ⋯ its menu', async () => {
  const author = await page('author');
  const [first, second] = author.view.books.rows;
  assert.deepEqual([author.view.books.title, author.view.books.count], [t('next.kit.booksHeading'), '2']);
  assert.deepEqual([first.square, first.kicker, first.facts, first.note], [true, undefined, [t('next.kit.readBy', {name: 'R. C. Bray'})], t('next.kit.remaining', {time: kitText.durationText(16_440, t)})]);
  assert.equal(second.note, undefined);
  // A finished book reads "Finished", not a film's "Watched".
  assert.equal(first.doneLabel, t('title.finished'));
  second.onNavigate(); assert.deepEqual(author.calls.at(-1), ['open', hail]);
  const anchor = {current: null};
  first.onMore(anchor); assert.deepEqual(author.calls.at(-1), ['menu', martian, anchor]);
  const series = await page('book_series');
  assert.equal(series.view.books.title, t('next.kit.booksInSeries'));
  assert.equal(series.view.books.rows[0].kicker, t('next.reader.bookNumber', {number: '1'}));
  assert.equal(series.view.books.rows[1].kicker, undefined, 'nothing is numbered by its place');
  series.view.onNearEnd(); assert.deepEqual(series.calls.at(-1), ['next', 'books']);
});

test('a series: its first book\'s cover, its author as a link, Start the series over; no Details', async () => {
  const {view, calls} = await page('book_series');
  assert.deepEqual([view.series, view.cover, view.covers], [true, {uri: '/m.jpg'}, []]);
  assert.equal(view.byline.text, 'Andy Weir');
  view.byline.onPress(); assert.deepEqual(calls.at(-1), ['open', authorEntry]);
  view.startOver(); assert.deepEqual(calls.at(-1), ['open', martian]);
  assert.equal(view.details, undefined);
});

test('no book in progress: Play opens the first book; a failed read offers Try again and holds Play; a further page loading or failed says so', async () => {
  const fresh = await page('author', {listening: undefined, sections: [{id: 'books', entries: [hail], totalCount: 1}]});
  assert.equal(fresh.view.primary.label, t('entry.play'));
  fresh.view.primary.onPress(); assert.deepEqual(fresh.calls.at(-1), ['open', hail]);
  const failed = await page('author', {phase: 'error', error: {code: 'unavailable', message: 'x', retryable: true}});
  assert.equal(failed.view.primary.disabled, true);
  failed.view.state.action.onPress(); assert.deepEqual(failed.calls.at(-1), ['load']);
  assert.equal((await page('author', {continuation: {sectionId: 'books', phase: 'loading'}})).view.continuation.kind, 'loading');
  const more = await page('author', {continuation: {sectionId: 'books', phase: 'error', error: {code: 'unavailable', message: 'x', retryable: true}}});
  more.view.continuation.action.onPress(); assert.deepEqual(more.calls.at(-1), ['recover']);
});
