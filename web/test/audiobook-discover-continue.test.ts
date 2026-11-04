import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import type {ContentEntry} from '@core/library-content.ts';
import {transformTsx} from './transform-tsx.mjs';

(globalThis as {devicePixelRatio?: number}).devicePixelRatio = 1;
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {BrowseContinueBookCard, BrowseEntryCard} = await import('../src/screens/browse/EntryCard.tsx');

/** DR-6: a multi-part book's continuation is one book card; its playback target is the current part at the saved offset. */
const book = {id: 'hobbit', kind: 'book', title: 'The Hobbit', author: 'J. R. R. Tolkien', libraryId: 'books', navigation: {view: 'book', entityId: 'hobbit'},
  duration: 40000, progressSeconds: 23680, playback: {itemId: 'hobbit-part-2', startSeconds: 3680},
  bookProgress: {bookId: 'hobbit', itemId: 'hobbit-part-2', partPositionMs: 3_680_000, positionMs: 23_680_000, durationMs: 40_000_000, remainingMs: 16_320_000,
    chapter: {index: 1, number: 5, title: 'Chapter 5', startMs: 3_000_000, endMs: 4_000_000}}} as unknown as ContentEntry;
const shared = {entry: book, artwork: {foreground: () => undefined, ambient: () => undefined} as never, env: {t: defaultI18n.t, locale: 'en-US'}, order: 0, group: 'continue_listening', visible: true};

test('from 600px an audiobook Continue listening card is the wide resume card: title, author, chapter, time left and the whole book\'s progress (desktop-listen #books)', () => {
  const html = renderToStaticMarkup(createElement(BrowseContinueBookCard, {...shared, actions: {onOpen() {}, onPlay() {}}}));
  assert.match(html, /resume/);
  assert.match(html, /The Hobbit/);
  assert.match(html, /J\. R\. R\. Tolkien/);
  assert.match(html, /Chapter 5/);
  assert.match(html, /4 h 32 min left/);
  assert.match(html, /aria-valuenow="59"|width:59(\.\d+)?%|--progress:0\.59/);
});

test('pressing the wide card plays the book\'s target (the current part at the saved offset); without one it opens the book', () => {
  let played = 0, opened = 0;
  const pressed = (actions: {onOpen: () => void; onPlay?: () => void}) => {
    const element = BrowseContinueBookCard({...shared, actions}) as {props: {onNavigate: () => void}};
    element.props.onNavigate();
  };
  pressed({onOpen: () => {opened++;}, onPlay: () => {played++;}});
  assert.deepEqual([played, opened], [1, 0]);
  pressed({onOpen: () => {opened++;}});
  assert.deepEqual([played, opened], [1, 1]);
  // The library's play action starts the entry's own playback target, never the book's first part.
  const route = readFileSync(new URL('../src/screens/browse/Library.route.tsx', import.meta.url), 'utf8');
  assert.match(route, /context\.effects\.play\(\{libraryId: target\.libraryId!, itemId: target\.playback\.itemId, startSeconds: target\.playback\.startSeconds \?\? 0/);
});

test('on a phone the card stays square, with the time left where the author sits (phone-app, audiobook library)', () => {
  const html = renderToStaticMarkup(createElement(BrowseEntryCard, {...shared, actions: {onOpen() {}}, width: 112, family: 'audiobook', backdrops: true, continuing: true}));
  assert.match(html, /4 h 32 min left/);
  assert.doesNotMatch(html, /Tolkien/);
  const ordinary = renderToStaticMarkup(createElement(BrowseEntryCard, {...shared, actions: {onOpen() {}}, width: 112, family: 'audiobook', backdrops: true}));
  assert.match(ordinary, /Tolkien/);
});

test('Discover draws the wide card for an audiobook library\'s continue_listening from 600px, and the square card below', () => {
  const source = readFileSync(new URL('../src/screens/browse/Discovery.tsx', import.meta.url), 'utf8');
  assert.match(source, /const continuing = snapshot\.projection!\.scope\.libraryKind === 'audiobook' && section\.id === 'continue_listening', resume = continuing && width >= 600;/);
  assert.match(source, /resume \? <BrowseContinueBookCard /);
});
