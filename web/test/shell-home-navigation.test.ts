import test from 'node:test';
import assert from 'node:assert/strict';
import {entryDestination, librarySearch, searchQuery, seeAllDestination} from '../src/shell/navigation.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';

test('See all keeps the actual compound filter, ordered sorts and target identity', () => {
  const query = {variant:'group',all: [{variant:'predicate',field: 'genre', operator: 'contains' as const, value: f.genre}, {variant:'group',not: {variant:'predicate',field: 'watched', operator: 'equals' as const, value: true}}]};
  const target = {type: 'library' as const, libraryId: 'library', pivot: 'movies', query, sort: [{field: 'title', direction: 'asc' as const}]};
  const route = seeAllDestination(target);
  assert.deepEqual(route, {to: '/library/$libraryId', params: {libraryId: 'library'}, search: {pivot: 'movies', query, sort: target.sort}});
  assert.equal(route.to, '/library/$libraryId');
  if (route.to === '/library/$libraryId') assert.deepEqual(librarySearch(route.search), route.search);
});
test('validated search preserves a random ordering seed and rejects an invalid seed', () => {
  assert.deepEqual(librarySearch({sort: [{field: 'random', direction: 'asc', seed: 42}]}), {sort: [{field: 'random', direction: 'asc', seed: 42}]});
  assert.deepEqual(librarySearch({sort: [{field: 'random', direction: 'asc', seed: -1}]}), {});
});
test('Search navigation preserves validated Unicode and query spacing without dispatching malformed URL state', () => {
  const q = '  ' + f.genre + '  '; assert.deepEqual(searchQuery({q}), {q});
  assert.deepEqual(searchQuery({q: ['unexpected']}), {q: ''});
  assert.deepEqual(searchQuery({q: 'x'.repeat(2049)}), {q: ''});
});
test('entry links use the server navigation identity without substituting its playable child', () => {
  assert.deepEqual(entryDestination({id: 'show', title: f.show, kind: 'show', libraryId: 'library', navigation: {view: 'show', entityId: 'show'}, playback: {itemId: 'episode'}}),
    {to: '/library/$libraryId', params: {libraryId: 'library'}, search: {view: 'show', entityId: 'show'}});
  assert.deepEqual(entryDestination({id: 'episode', title: f.episode, kind: 'episode', navigation: {view: 'item', entityId: 'actual-item'}, playback: {itemId: 'other-file'}}),
    {to: '/item/$itemId', params: {itemId: 'actual-item'}});
});
test('every server See all kind has a real registered destination', () => {
  assert.deepEqual(seeAllDestination({type: 'row', rowId: 'because-one'}), {to: '/home/rows/$rowId', params: {rowId: 'because-one'}});
  assert.deepEqual(seeAllDestination({type: 'saved_view', resourceId: 'view'}), {to: '/saved', search: {resourceId: 'view'}});
  assert.deepEqual(seeAllDestination({type: 'collection', resourceId: 'collection', libraryId: 'library'}), {to: '/library/$libraryId', params: {libraryId: 'library'}, search: {view: 'collection', entityId: 'collection'}});
  assert.deepEqual(seeAllDestination({type: 'guide', group: 'group', sourceId: 'source'}), {to: '/live', search: {sourceId: 'source', group: 'group'}});
});
test('malformed navigation state cannot introduce arbitrary predicates or destinations', () => {
  assert.deepEqual(librarySearch({view: 'admin', entityId: '\u0000', pivot: 'x'.repeat(1000), query: {sql: 'untrusted'}, sort: [{field: 'title', direction: 'unsafe'}]}), {});
});
