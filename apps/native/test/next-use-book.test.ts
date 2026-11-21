import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import * as kitText from '../../../packages/client-core/src/presentation/kit-model/text';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as bookPlace from '../src-next/pages/shared/book/book-place.ts';

// `useBook` is what an audiobook's page shows on a phone (collections 41a), from the audiobook controller the old book
// screen read: this holds what it shows and what each control does.

const t = defaultI18n.t;
const entity = {id: 'book-1', libraryId: 'books', kind: 'book', title: 'The Martian', author: 'Andy Weir', narrators: ['R. C. Bray'], duration: 39200,
  overview: 'A stranded astronaut.', posterUrl: '/m.jpg', series: [{name: 'Mars', position: '1'}],
  bookProgress: {bookId: 'book-1', itemId: 'file-1', partPositionMs: 530000, positionMs: 530000, durationMs: 1000000, remainingMs: 470000,
    chapter: {index: 6, number: 7, title: 'Chapter 6', startMs: 500000, endMs: 560000}}} as any;
// Rows as the reader builds them (library-content-rebuilt.ts): `book:chapter:N`, each playing its file from its own start.
const chapters = [{id: 'book-1:chapter:1', libraryId: 'books', kind: 'chapter', title: 'Chapter 1', duration: 100, watched: true, playback: {itemId: 'file-1', startSeconds: 400}},
  {id: 'book-1:chapter:2', libraryId: 'books', kind: 'chapter', title: 'Chapter 6', duration: 60, playback: {itemId: 'file-1', startSeconds: 500}},
  {id: 'book-1:chapter:3', libraryId: 'books', kind: 'chapter', title: 'Chapter 7', duration: 90, playback: {itemId: 'file-1', startSeconds: 560}}] as any[];
const series = {id: 's1', kind: 'book_series', title: 'Mars', libraryId: 'books'}, author = {id: 'a1', kind: 'author', title: 'Andy Weir', libraryId: 'books'};

async function book(over: Record<string, unknown> = {}, props: Record<string, unknown> = {}) {
  const calls: unknown[][] = [], h = hooks();
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]); return Promise.resolve();};
  const {useBook} = await screenModule(new URL('../src-next/pages/shared/book/useBook.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/errors': errors,
    '../../../../../../packages/client-core/src/presentation/kit-model/text': kitText, './book-place': bookPlace,
    '../../../screens/downloads/request': {askNativeDownload: (request: unknown) => calls.push(['download', request])},
    '../../../screens/watch/download-request': {nativeDownloadRequest: (value: any) => value.noDownload ? undefined : {id: value.id}}});
  const snapshot = {phase: 'ready', finished: false, entity, listening: {resume: {itemId: 'file-1', positionSeconds: 530}, resumeUnavailable: false},
    sections: [{id: 'chapters', entries: chapters, totalCount: 17}, {id: 'context', entries: [series, author]}], ...over};
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, load: record('load'), markFinished: record('finished'), resume: record('resume'),
    startOver: record('startOver'), playChapter: record('chapter'), next: record('next'), recover: record('recover'), resumeBook: record('resumeBook')};
  const view = h.render(() => useBook({controller, entry: entity, runtime: {artwork: (path?: string) => path ? {uri: path} : undefined, ambient: (path?: string) => path ? {url: path} : undefined,
    reportError: record('error')}, actions: {menu: (...args: unknown[]) => {calls.push(['menu', ...args]);}}, openEntry: record('open'), onBack: record('back'), ...props}));
  return {view, calls};
}

test('a book shows its series and place, the author as a link, who reads it, its length and chapters, and Resume chapter N with its progress', async () => {
  const {view, calls} = await book();
  assert.equal(view.kicker.label, t('title.seriesAndBook', {series: 'Mars', number: '1'}));
  view.kicker.onPress(); assert.deepEqual(calls.at(-1), ['open', series]);
  view.byline.onPress(); assert.deepEqual(calls.at(-1), ['open', author]);
  assert.deepEqual(view.facts.map((fact: any) => fact.text), [t('next.kit.readBy', {name: 'R. C. Bray'}), kitText.durationText(39200, t), t('title.chapterCount', {count: 17})]);
  // The chapter you are in is found by what it plays (its file and start), whatever its id, and the label names its place.
  assert.equal(view.primary.label, t('title.resumeChapterNumber', {number: 2}));
  assert.equal(view.primary.progress, .53);
  assert.equal(view.primary.remaining, t('next.kit.remaining', {time: kitText.durationText(470, t)}));
  view.primary.onPress(); assert.deepEqual(calls.at(-1), ['resume']);
  assert.deepEqual([view.cover, view.glow], [{uri: '/m.jpg'}, {url: '/m.jpg'}]);
});

test('the chapters: the finished one checked, the one you are in with its progress and time left; a chapter plays, and the list pages on', async () => {
  const {view, calls} = await book();
  assert.deepEqual([view.chapters.title, view.chapters.count], [t('next.browse.chapters'), '17']);
  assert.deepEqual(view.chapters.rows.map((row: any) => [row.number, !!row.done, !!row.current]), [[1, true, false], [2, false, true], [3, false, false]]);
  const current = view.chapters.rows[1];
  assert.equal(current.progress, .5);
  assert.equal(current.text.detail[0].text, t('next.kit.remaining', {time: kitText.durationText(30, t)}));
  view.chapters.onPlay('book-1:chapter:3'); assert.deepEqual(calls.at(-1), ['chapter', chapters[2]]);
  view.onNearEnd(); assert.deepEqual(calls.at(-1), ['next', 'chapters']);
});

test('a book of parts marks the part the place falls in, its progress running to the next part', async () => {
  const parts = [{id: 'book-1:part:1', libraryId: 'books', kind: 'chapter', title: 'Part 1', playback: {itemId: 'file-1', startSeconds: 0}},
    {id: 'book-1:part:2', libraryId: 'books', kind: 'chapter', title: 'Part 2', playback: {itemId: 'file-1', startSeconds: 400}},
    {id: 'book-1:part:3', libraryId: 'books', kind: 'chapter', title: 'Part 3', playback: {itemId: 'file-1', startSeconds: 800}}];
  const {view} = await book({entity: {...entity, bookProgress: {...entity.bookProgress, chapter: undefined}}, sections: [{id: 'files', entries: parts, totalCount: 3}]});
  assert.deepEqual(view.chapters.rows.map((row: any) => !!row.current), [false, true, false]);
  assert.equal(view.chapters.rows[1].progress, (530 - 400) / 400);
  // A parts book names no chapter count, and its Resume names no chapter.
  assert.equal(view.facts.some((fact: any) => fact.id === 'chapters'), false);
  assert.equal(view.primary.label, t('entry.resume'));
  // W3 F11: a book of parts heads its list "Chapters", never a bare count.
  assert.equal(view.chapters.title, t('next.browse.chapters'));
});

test('Finished, Download, and Start over in the book\'s menu; no resume means Play, which starts over', async () => {
  const {view, calls} = await book();
  view.finished.onPress(); assert.deepEqual(calls.at(-1), ['finished']);
  view.download(); assert.deepEqual(calls.at(-1), ['download', {id: 'book-1'}]);
  view.more(); const [, menuEntry, , , extras] = calls.at(-1) as any[];
  assert.equal(menuEntry, entity);
  assert.deepEqual(extras.map((extra: any) => extra.id), ['book-start-over']);
  extras[0].onSelect(); assert.deepEqual(calls.at(-1), ['startOver']);
  view.startOver(); assert.deepEqual(calls.at(-1), ['startOver'], 'a TV page\'s Start over button starts the book over too');
  const fresh = await book({listening: undefined});
  assert.equal(fresh.view.primary.label, t('entry.play'));
  fresh.view.primary.onPress(); assert.deepEqual(fresh.calls.at(-1), ['startOver']);
  assert.equal((await book({entity: {...entity, noDownload: true}})).view.download, undefined, 'the read\'s entity decides, as on the old page');
});

test('a failed read offers Try again; a further page loading or failed says so; the description opens with Read more', async () => {
  const failed = await book({phase: 'error', error: {code: 'unavailable', message: 'x', retryable: true}});
  failed.view.state.action.onPress(); assert.deepEqual(failed.calls.at(-1), ['load']);
  assert.equal((await book({continuation: {sectionId: 'chapters', phase: 'loading'}})).view.continuation.kind, 'loading');
  const more = await book({continuation: {sectionId: 'chapters', phase: 'error', error: {code: 'unavailable', message: 'x', retryable: true}}});
  more.view.continuation.action.onPress(); assert.deepEqual(more.calls.at(-1), ['recover']);
  const {view} = await book();
  assert.deepEqual([view.description.text, view.description.readMore.expanded], ['A stranded astronaut.', false]);
  // A book with no series and no context names none, and no link.
  const plain = await book({entity: {...entity, series: undefined}, sections: [{id: 'chapters', entries: chapters, totalCount: 17}]});
  assert.equal(plain.view.kicker, undefined);
  assert.equal(plain.view.byline.onPress, undefined);
});
