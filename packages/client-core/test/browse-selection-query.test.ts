import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSelectionModel} from '../src/selection-model/index.ts';
import {SelectionInputError} from '../src/selection-model/index.ts';

const context = {scope: {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', viewId: 'movies|title-asc'},
  revision: {pageRevision: 'revision-1'}};
const counts = {play: 48000, queue: 48000, playlist: 48000, myList: 48000, watched: 48000, collection: 48000, favorite: 48000, download: 0, edit: 48000, refresh: 48000, delete: 48000};
const query = (over: Record<string, unknown> = {}) => ({
  id: 'whole:movies',
  selector: {query: {libraryId: 'movies', pivot: 'movies', filter: {field: 'year', operator: 'at-least', value: 2000}, sort: [{field: 'title', direction: 'asc'}]}},
  total: 48000,
  catalogRevision: 'revision-1',
  summary: {
    types: [{kind: 'movie', count: 48000, capabilityCounts: counts, libraries: [{libraryId: 'movies', count: 48000}]}],
    library: {kind: 'single', libraryId: 'movies'},
    every: {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: false, edit: true, refresh: true, delete: true},
  },
  ...over,
});

test('a descriptor naming this page revision registers a whole-view query', () => {
  const model = new BrowseSelectionModel(context);
  const registered = model.registerQuery(model.captureContext(), query() as never);
  assert.equal(registered.ok, true);
  const applied = model.selectWholeQuery('whole:movies');
  assert.equal(applied.ok, true);
  const snapshot = model.getSnapshot();
  assert.equal(snapshot.active, true);
  assert.equal(snapshot.summary.count, 48000);
  assert.deepEqual(snapshot.summary.types, [{kind: 'movie', count: 48000}]);
  assert.equal(snapshot.summary.library.kind, 'single');
  assert.equal(snapshot.target?.mode, 'query');
  assert.equal(snapshot.target?.mode === 'query' ? snapshot.target.expected.catalogRevision : '', 'revision-1');
});

test('a query for any other revision or a contradictory summary is refused', () => {
  const model = new BrowseSelectionModel(context);
  for (const value of [
    query({catalogRevision: 'revision-2'}),
    query({total: 47999}),
    query({summary: {...query().summary, library: {kind: 'single', libraryId: 'tv'}}}),
    query({summary: {...query().summary, every: {...query().summary.every, play: false}}}),
  ]) {
    assert.throws(() => model.registerQuery(model.captureContext(), value as never), SelectionInputError, JSON.stringify(value.catalogRevision));
  }
});
