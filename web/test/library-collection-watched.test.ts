import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import type {ContentEntry} from '@core/library-content.ts';
import {collectionWatchedCount, collectionWatchedProgress} from '../src/screens/browse/collection-card.ts';
import {transformTsx} from './transform-tsx.mjs';

// The card asks for its artwork at the device density; node has no window.
(globalThis as {devicePixelRatio?: number}).devicePixelRatio = 1;

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {BrowseEntryCard} = await import('../src/screens/browse/EntryCard.tsx');

const collection = (fields: Partial<ContentEntry> = {}): ContentEntry => Object.freeze({id: 'collection-alien', kind: 'collection', libraryId: 'movies',
  title: 'Alien', count: 2, yearMin: 1979, yearMax: 1986, artworkPaths: [], ...fields}) as ContentEntry;
const render = (entry: ContentEntry) => renderToStaticMarkup(createElement(BrowseEntryCard, {
  entry, actions: {onOpen() {}}, artwork: {foreground: () => undefined, ambient: () => undefined} as never,
  env: {t: defaultI18n.t, locale: 'en-US'}, width: 457, family: 'movie', order: 0, group: 'movies', visible: true, backdrops: true,
}));

test('a collection draws the watched share only from exact compatible counts', () => {
  assert.equal(collectionWatchedCount({watchedCount: 1, count: 2}), 1);
  assert.equal(collectionWatchedProgress({watchedCount: 1, count: 2}), .5);
  assert.equal(collectionWatchedCount({count: 2}), undefined, 'absence is not zero');
  assert.equal(collectionWatchedProgress({count: 2}), undefined, 'an absent tally draws no bar');
  assert.equal(collectionWatchedCount({watchedCount: 1, count: 2, countIsLowerBound: true}), undefined, 'a lower bound is not an exact denominator');
  assert.equal(collectionWatchedCount({watchedCount: 3, count: 2}), undefined, 'an impossible tally is left out');
  assert.equal(collectionWatchedCount({watchedCount: 0, count: 0}), undefined);
  assert.equal(collectionWatchedProgress({watchedCount: 0, count: 2}), undefined, 'nothing watched draws no bar');
});

test('the collection tile names the watched films and bars the exact share', () => {
  const html = render(collection({watchedCount: 1, unwatchedCount: 1}));
  assert.match(html, /2 films/);
  assert.match(html, /1979 to 1986|1979 – 1986|1979–1986/);
  assert.match(html, /1 of 2 watched/);
  assert.match(html, /width:50%/);
  const none = render(collection({watchedCount: 0, unwatchedCount: 2}));
  assert.doesNotMatch(none, /of 2 watched/);
  assert.doesNotMatch(none, /width:50%/);
  const absent = render(collection());
  assert.doesNotMatch(absent, /watched/);
});
