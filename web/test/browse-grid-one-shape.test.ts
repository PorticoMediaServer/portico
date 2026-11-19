import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import type {ContentEntry} from '@core/library-content.ts';
import {transformTsx} from './transform-tsx.mjs';

// SMALL-FAULTS #15 (lead's ruling): a grid has ONE card shape. A square See all (Recently Added) drew its books and comics as
// 2:3 cover cards among square cards, and every row took the tallest card's height: square rows carried empty bands.
(globalThis as {devicePixelRatio?: number}).devicePixelRatio = 1;
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {BrowseEntryCard} = await import('../src/screens/browse/EntryCard.tsx');

const asked: number[] = [];
const entry = (kind: string, fields: Partial<ContentEntry> = {}): ContentEntry => Object.freeze({id: `${kind}-1`, kind, libraryId: 'lib', title: 'A Title', posterUrl: '/art', ...fields}) as ContentEntry;
const render = (item: ContentEntry, slot?: 'poster' | 'square', family?: string, mixed = false) => renderToStaticMarkup(createElement(BrowseEntryCard, {
  entry: item, actions: {onOpen() {}, onPlay() {}}, artwork: {foreground: (_path: string, _width: number, aspect: number) => {asked.push(aspect); return undefined;}, ambient: () => undefined} as never,
  env: {t: defaultI18n.t, locale: 'en-US'}, width: 170, family, mixed, order: 0, group: 'lib', visible: true, backdrops: true, slot,
}));
const card = (html: string) => /class="card ([a-z]+)/.exec(html)?.[1] ?? /class="[^"]*\b(square|poster|cover|circle)\b/.exec(html)?.[1];

test('in a square grid a comic or book is a square card, its cover drawn whole, keeping its reading marks', () => {
  asked.length = 0;
  const comic = render(entry('comic', {readingMark: {read: true}} as never), 'square', 'comic');
  assert.match(comic, /class="card square/);
  assert.match(comic, new RegExp(defaultI18n.t('next.cards.finished')), 'the finished mark stays');
  assert.deepEqual(asked, [2 / 3], 'the cover is asked for in its own 2:3 shape, never cropped to the slot');
});

test('in a poster grid an audiobook is a poster card (its square cover drawn whole), and a book keeps its cover card', () => {
  assert.match(render(entry('book'), 'poster', 'audiobook'), /class="card poster/);
  assert.match(render(entry('ebook'), 'poster', 'book'), /class="card cover/);
  assert.match(render(entry('artist'), 'square', 'music'), /circle/, 'an artist keeps its circle');
  assert.match(render(entry('book'), undefined, 'audiobook'), /class="card square/, 'outside a grid a card keeps its own shape');
});

test('a MIXED poster grid draws books, comics and ebooks as poster cards (their covers whole over a wash); a Books library keeps cover cards', () => {
  // SMALL-FAULTS 27: See all's books and comics sat at the foot of their slots with no wash, a ragged row beside the posters.
  for (const kind of ['ebook', 'comic'] as const) {
    const html = render(entry(kind, {readingMark: {read: true}} as never), 'poster', kind === 'comic' ? 'comic' : 'book', true);
    assert.match(html, /class="card poster/, `${kind} in a mixed grid`);
    assert.match(html, new RegExp(defaultI18n.t('next.cards.finished')), 'the finished mark stays');
    assert.doesNotMatch(html, /aria-label="Play/, 'a reading title still offers no play');
  }
  assert.match(render(entry('ebook'), 'poster', 'book', false), /class="card cover/, 'a Books library keeps its cover cards');
});
