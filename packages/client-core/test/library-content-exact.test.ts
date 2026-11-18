import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryContentService, validateContentEntry} from '../src/library-content.ts';
import {serverPath} from '../src/query/server-api.ts';
import {withQueries} from './fixtures/viewer-queries.ts';

const scope = {viewerId: JSON.stringify(['hosted', 'account', 'profile']), serverId: 'server'};

test('sections and entries preserve exact totals', async () => {
  const libraryId = 'movies';
  const api = {request: async <T>(path: string): Promise<T> => {
    if (path === serverPath('GET /v1/libraries/{id}', {id: libraryId})) return {id: libraryId, kind: 'movie'} as T;
    assert.equal(path, serverPath('GET /v1/libraries/{id}/landing', {id: libraryId}));
    return {rows: [{id: 'collections', title: 'Collections', total: 10000, items: [
      {id: 'collection', kind: 'collection', title: 'Collection', counts: {leaves: 9000, unwatchedLeaves: 8500}},
    ]}]} as T;
  }};
  const nav = new LibraryContentService(withQueries({api, scope}));
  try {
    await nav.select({libraryId, view: 'discover'});
    assert.equal(nav.getSnapshot().phase, 'ready');
    const [section] = nav.getSnapshot().sections;
    assert.equal(section.totalCount, 10000);
    assert.equal(section.entries[0].count, 9000);
    assert.equal(section.entries[0].watchedCount, 500);
    assert.equal(section.entries[0].unwatchedCount, 8500);
  } finally {nav.dispose();}
});

test('the shared collection card parser keeps exact watched tallies and absence is never zero', () => {
  const collection = {id: 'collection', kind: 'collection', title: 'Collection', count: 2};
  const watched = validateContentEntry({...collection, watchedCount: 1, unwatchedCount: 1});
  const absent = validateContentEntry(collection);
  const invalid = validateContentEntry({...collection, watchedCount: -1, unwatchedCount: 1.5});
  assert.equal(watched.watchedCount, 1);
  assert.equal(watched.unwatchedCount, 1);
  for (const row of [absent, invalid]) {
    assert.equal('watchedCount' in row, false);
    assert.equal('unwatchedCount' in row, false);
  }
});
