import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSelectionModel, SelectionInputError} from '../../packages/client-core/src/selection-model/index.ts';
import {wholeViewQuery} from '../src/screens/browse/selection-query.ts';

const counts = {play: 48000, queue: 48000, playlist: 48000, myList: 48000, watched: 48000, collection: 48000, favorite: 48000, download: 0, edit: 48000, refresh: 48000, delete: 48000};
const descriptor = {
  libraryId: 'movies',
  pivot: 'movies',
  filter: {field: 'year', operator: 'at-least', value: 2000},
  sort: [{field: 'title', direction: 'asc' as const}],
  catalogRevision: 'revision-1',
  total: 48000,
  types: [{kind: 'movie', count: 48000, capabilities: counts, libraries: [{libraryId: 'movies', count: 48000}]}],
};
const context = (revision = 'revision-1') => ({scope: {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', viewId: 'movies|title-asc'},
  revision: {pageRevision: revision}});

test('the descriptor becomes the exact query contract, with every derived as the model recomputes it', () => {
  const query = wholeViewQuery(descriptor as never);
  assert.equal(query.total, 48000);
  assert.equal(query.catalogRevision, 'revision-1');
  assert.equal(query.id, 'whole:revision-1:movies');
  assert.deepEqual(query.selector, {query: {libraryId: 'movies', pivot: 'movies', filter: descriptor.filter, sort: descriptor.sort}});
  assert.equal(query.summary.library.kind, 'single');
  assert.equal(query.summary.types[0]?.count, 48000);
  // Every capability is true for the whole view only when every title carries it.
  assert.equal(query.summary.every.play, true);
  assert.equal(query.summary.every.download, false);
  const model = new BrowseSelectionModel(context());
  assert.equal(model.registerQuery(model.captureContext(), query).ok, true);
  assert.equal(model.selectWholeQuery(query.id).ok, true);
  const snapshot = model.getSnapshot();
  assert.equal(snapshot.active, true);
  assert.equal(snapshot.summary.count, 48000);
  assert.equal(snapshot.target?.mode, 'query');
});

test('a descriptor from another page revision cannot become this page selection', () => {
  const model = new BrowseSelectionModel(context('revision-2'));
  assert.throws(() => model.registerQuery(model.captureContext(), wholeViewQuery(descriptor as never)), SelectionInputError);
});

test('a capability offered for only part of the view is not reported as offered for all', () => {
  const mixed = {...descriptor, types: [{...descriptor.types[0], capabilities: {...counts, download: 4, play: 47999}}]};
  const query = wholeViewQuery(mixed as never);
  assert.equal(query.summary.every.play, false);
  assert.equal(query.summary.every.download, false);
  // The counts themselves remain exactly what the server said.
  assert.equal(query.summary.types[0]?.capabilityCounts.play, 47999);
  assert.equal(query.summary.types[0]?.capabilityCounts.download, 4);
});
