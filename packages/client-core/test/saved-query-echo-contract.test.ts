import test from 'node:test';
import assert from 'node:assert/strict';
import {SavedService, type SavedRoute} from '../src/saved.ts';

// The server publishes the selected filter alone as the saved query's category.
// These cases pin the producer echo the strict parser accepts, and keep the
// rejection of the old filter:shape echo so a regression cannot pass silently.
const scope = {serverId: 'server', viewerId: '["local","account","profile"]'};
const filters = [{id: 'filter', labelKey: 'saved.filter', options: [
  {id: 'all', label: 'All', count: 1}, {id: 'unwatched', label: 'Unwatched', count: 1}, {id: 'inProgress', label: 'In progress', count: 1},
  {id: 'watched', label: 'Watched', count: 1}, {id: 'favorites', label: 'Favorites', count: 1},
]}];
function page(category: string) {
  return {
    scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'watchlist', entityId: '', viewerFence: 'fence'},
    revision: {catalog: 1, viewer: 0},
    heading: {key: 'saved.title', fallback: 'Saved'},
    navigation: [{id: 'watchlist', labelKey: 'saved.watchlist', view: 'watchlist'}, {id: 'favorites', labelKey: 'saved.favorites', view: 'favorites'}],
    query: {sort: 'added', direction: 'desc', category, q: '', limit: 40, searchMode: 'none'},
    sorts: [{id: 'added', labelKey: 'sort.added', directions: ['asc', 'desc']}],
    filters,
    sections: [{id: 'entries', type: 'grid', heading: {key: 'saved.entries', fallback: 'Entries'},
      entries: [{id: 'movie', libraryId: 'library', kind: 'movie', title: 'Movie', available: true, playback: {itemId: 'movie'}}], totalCount: 1, nextCursor: ''}],
  };
}
type RouteShape = Readonly<{shape?: 'posters' | 'squares'; filter?: 'all' | 'unwatched' | 'inProgress' | 'watched' | 'favorites'}>;
async function read(category: string, route: RouteShape) {
  let calls = 0;
  const service = new SavedService({scope, api: {request: async () => { calls++; return page(category); }},
    requestId: async () => '00000000-0000-4000-8000-000000000001', timeoutMs: 200});
  await service.select({view: 'watchlist', sort: 'added', direction: 'desc', ...route} as SavedRoute);
  const snapshot = service.getSnapshot();
  service.dispose();
  return {...snapshot, calls};
}

test('the published filter alone parses for every shape and filter', async () => {
  for (const shape of [undefined, 'posters', 'squares'] as const) {
    for (const filter of ['all', 'unwatched', 'inProgress', 'watched', 'favorites'] as const) {
      const snapshot = await read(filter, {...(shape ? {shape} : {}), ...(filter === 'all' ? {} : {filter})});
      assert.equal(snapshot.phase, 'ready', `${shape ?? 'both'} ${filter}: ${snapshot.error?.code}`);
      assert.equal(snapshot.projection?.query.category, filter, `${shape ?? 'both'} ${filter}`);
      assert.equal(snapshot.calls, 1);
    }
  }
});

test('a filter:shape echo is refused instead of split back to the filter', async () => {
  for (const shape of ['posters', 'squares']) {
    const snapshot = await read(`all:${shape}`, {shape});
    assert.equal(snapshot.projection, null, shape);
    assert.equal(snapshot.error?.code, 'invalid_saved', shape);
  }
});

test('an unpublished category is refused', async () => {
  for (const category of ['recent', 'all:', 'unwatched:posters:extra']) {
    const snapshot = await read(category, category === 'recent' ? {} : {shape: 'posters'});
    assert.equal(snapshot.error?.code, 'invalid_saved', category);
  }
});
