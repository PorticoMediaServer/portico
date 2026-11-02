import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import type {SearchEntry} from '@core/search.ts';
import type {ContentEntry} from '@core/library-content.ts';

// The real Search result and detail-row markup runs here; only CSS Modules and the TSX loader are supplied.
// This is source proof that each card carries its ⋯, not a browser capture.
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {SearchResult, SearchTitleMenu} = await import('../src/screens/search/Search.tsx');

const context = {artwork: {foreground: () => undefined, ambient: () => undefined}, isCurrent: () => true, effects: {reportError() {}}, preferences: {getSnapshot: () => ({effective: {}}), subscribe: () => () => {}}};
const binding = {context, live: {subscribe: () => () => {}, getSnapshot: () => new Map()}, open() {}, async play() {}, showing: () => true} as never;
const base = {libraryId: 'lib', available: true, navigation: {view: 'item', entityId: 'x'}} as const;
const entries: Record<string, SearchEntry> = {
  movie: {...base, id: 'm', kind: 'movie', title: 'Heat', playback: {itemId: 'm'}} as SearchEntry,
  show: {...base, id: 's', kind: 'show', title: 'Dark'} as SearchEntry,
  episode: {...base, id: 'e', kind: 'episode', title: 'Pilot', playback: {itemId: 'e'}} as SearchEntry,
  song: {...base, id: 'so', kind: 'song', title: 'Blue', playback: {itemId: 'so'}} as SearchEntry,
  album: {...base, id: 'a', kind: 'album', title: 'Kind of Blue'} as SearchEntry,
  artist: {...base, id: 'ar', kind: 'artist', title: 'Miles'} as SearchEntry,
  ebook: {...base, id: 'b', kind: 'ebook', title: 'Dune', pageCount: 10} as SearchEntry,
  audiobook: {...base, id: 'ab', kind: 'audiobook_file', title: 'Part 1', playback: {itemId: 'ab'}} as SearchEntry,
};
const render = (entry: SearchEntry, menu: ((anchor: HTMLElement, subject: unknown) => void) | undefined) => renderToStaticMarkup(createElement(SearchTitleMenu.Provider, {value: menu as never},
  createElement(SearchResult, {entry, binding, onCommit() {}})));
const opener = () => {};
const moreLabels = (html: string) => [...html.matchAll(/aria-haspopup="menu"[^>]*aria-label="([^"]*)"|aria-label="([^"]*)"[^>]*aria-haspopup="menu"/g)].map(match => match[1] ?? match[2]);

test('every search result that is a title carries a ⋯ in each group', () => {
  for (const [name, entry] of Object.entries(entries)) {
    const labels = moreLabels(render(entry, opener));
    assert.equal(labels.length, 1, `${name}: ${JSON.stringify(labels)}`);
    assert.ok(labels[0]!.includes(entry.title), `${name} names its title: ${labels[0]}`);
  }
});

test('a result with no menu host draws no ⋯, and a person or channel never has one', () => {
  assert.deepEqual(moreLabels(render(entries.movie!, undefined)), []);
  const person = {id: 'p', kind: 'person', title: 'Pacino', navigation: {view: 'person', entityId: 'p'}} as SearchEntry;
  const channel = {id: 'c', kind: 'channel', title: 'News', navigation: {view: 'channel', entityId: 'c', sourceId: 's', channelId: 'c'}} as SearchEntry;
  assert.deepEqual(moreLabels(render(person, opener)), []);
  assert.deepEqual(moreLabels(render(channel, opener)), []);
});

// Library Browse / Discover (FX-S3): the card's ⋯ is the shared menu's opener, for every card.
const {useLibraryCardMenu} = await import('../src/screens/browse/use-card-menu.tsx');
const {BrowseEntryCard} = await import('../src/screens/browse/EntryCard.tsx');
const {TitleRow} = await import('../src/screens/detail/Detail.tsx');
Object.assign(globalThis, {devicePixelRatio: 1});
const shellContext = {...context, scope: {serverId: 's', viewerId: 'v'}, api: {request: async () => ({})}, events: {}, effects: {...context.effects, openEntry() {}}} as never;
const admission = {getSnapshot: () => ({queryRevision: 1}), contains: () => true};
const env = {t: (id: string) => id, locale: 'en-US'} as never;
const artwork = {foreground: () => undefined, ambient: () => undefined} as never;
function Probe({entry, actions}: {entry: ContentEntry; actions: {onOpen: () => void; onPlay?: () => void; onMore?: (anchor: HTMLButtonElement) => void}}) {
  const {wrap, menu} = useLibraryCardMenu({admission, queryRevision: 1, context: shellContext});
  return createElement('div', null, createElement(BrowseEntryCard, {entry, actions: wrap(actions, entry), artwork, env, width: 170, order: 0, group: 'g', visible: true, backdrops: false}), menu);
}
const probe = (entry: ContentEntry, actions: Parameters<typeof Probe>[0]['actions']) => renderToStaticMarkup(createElement(Probe, {entry, actions}));
const library = (kind: string, patch: Record<string, unknown> = {}) => ({id: kind, kind, title: 'T-' + kind, libraryId: 'lib', ...patch}) as unknown as ContentEntry;

test('a Browse or Discover card has the ⋯ whether or not it can play or be selected', () => {
  for (const kind of ['movie', 'show', 'episode', 'song', 'album', 'audiobook_file', 'collection']) {
    assert.equal(moreLabels(probe(library(kind), {onOpen() {}, onPlay() {}})).length, 1, kind + ' with Play');
    assert.equal(moreLabels(probe(library(kind), {onOpen() {}})).length, 1, kind + ' without Play');
  }
});

test('a card whose owner supplies its own menu keeps it', () => {
  let own = 0;
  const html = probe(library('movie'), {onOpen() {}, onMore() { own++; }});
  assert.equal(moreLabels(html).length, 1);
  assert.equal(own, 0, 'nothing runs while drawing');
});

// Detail rows (More like this, Person known-for and credits, Artist albums all draw through TitleRow).
test('a detail page row card carries the ⋯', () => {
  const row = renderToStaticMarkup(createElement(TitleRow, {heading: 'More like this', entries: [library('movie', {title: 'Ronin'}), library('album', {title: 'Blue'})], binding: {context: shellContext} as never}));
  const labels = moreLabels(row);
  assert.deepEqual(labels.length, 2, row.slice(0, 400));
  assert.ok(labels[0]!.includes('Ronin'));
});
