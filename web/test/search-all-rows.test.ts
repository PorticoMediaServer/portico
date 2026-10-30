import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {AllResultsRow} = await import('../src/screens/search/AllResultsRow.tsx');
function render(id: string, items: any[], cursor = 'next', excluded?: any) {
  const result = {id, title: id, entityKind: id, status: 'success', errorCode: '', items, totalCount: 100, totalCountExact: true, hasMore: !!cursor, nextCursor: cursor};
  const binding = {results: {page: () => ({loading: false, error: null})}, service: {getSnapshot: () => ({query: {q: 'harbor'}})}, context: {effects: {reportError() {}}}};
  return renderToStaticMarkup(createElement(AllResultsRow, {result, binding, size: 1, excluded, onSeeAll() {},
    renderEntry: (entry: any, index: number) => createElement('button', {'data-result': entry.id, 'data-position': index}, entry.id)} as any));
}

test('Search All renders every admitted movie, omits only its top hit, and removes its sole pending slot at EOF', () => {
  const items = Array.from({length: 18}, (_, index) => ({id: String(index), libraryId: 'library', kind: 'movie'}));
  const ongoing = render('movies', items, 'next', items[0]), ended = render('movies', items, '', items[0]);
  assert.equal((ongoing.match(/data-result=/g) ?? []).length, 17);
  assert.equal((ongoing.match(/data-row-pending/g) ?? []).length, 1);
  assert.doesNotMatch(ongoing, /data-result="0"/);assert.match(ongoing, /data-result="17"/);
  assert.doesNotMatch(ended, /data-row-pending/);
});

test('mixed Reading preserves two shaped rows, original selection positions, all entries and one pending slot', () => {
  const items = Array.from({length: 14}, (_, index) => ({id: String(index), libraryId: 'library', kind: 'ebook', publicationType: index % 2 ? 'magazine' : 'book'}));
  const html = render('reading', items);
  assert.equal((html.match(/data-result=/g) ?? []).length, 14);
  assert.equal((html.match(/data-row-pending/g) ?? []).length, 1);
  assert.match(html, /followingRow/);
  assert.match(html, /data-result="13" data-position="13"/);
  assert.ok(html.indexOf('data-result="12"') < html.indexOf('data-result="1"'), 'books remain in their own row before issues');
});

test('Songs continuation keeps every song available to the four-row horizontal layout', () => {
  const items = Array.from({length: 13}, (_, index) => ({id: String(index), libraryId: 'library', kind: 'song'}));
  const html = render('songs', items);
  assert.equal((html.match(/data-result=/g) ?? []).length, 13);
  assert.equal((html.match(/data-row-pending/g) ?? []).length, 1);
  assert.match(html, /data-group="songs"/);
});
