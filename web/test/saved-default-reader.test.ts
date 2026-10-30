import assert from 'node:assert/strict';
import test from 'node:test';
import {savedReaderRoute} from '../src/screens/saved/runtime.ts';
import {sameSavedRoute} from '../src/screens/saved/route.ts';
import type {SavedRoute} from '../../packages/client-core/src/saved.ts';

// Authored UNRUN: the default must be shared by the request, exact route fences,
// preview and visible sort. Changing only the request would blank the whole list.
test('My List default is the same exact query for both loaded shapes and the directory preview', () => {
  const original = Object.freeze({view: 'watchlist'} as const), reader = savedReaderRoute(original);
  const home = savedReaderRoute({view: 'home'});
  if (reader.view !== 'watchlist' || home.view !== 'watchlist') throw new Error('Expected the actual My List query.');
  assert.deepEqual(reader, {view: 'watchlist', sort: 'added', direction: 'desc'});
  for (const shape of ['posters', 'squares'] as const) {
    const issued: SavedRoute = {...reader, shape};
    assert.equal(sameSavedRoute(issued, {...reader, shape}), true);
    assert.equal(sameSavedRoute(issued, {...original, shape}), false, 'the existing exact fence is never weakened');
    assert.equal(sameSavedRoute(issued, {...home, shape}), true);
  }
  assert.ok(Object.isFrozen(savedReaderRoute({view: 'home'})));
  assert.deepEqual(original, {view: 'watchlist'});
});
test('an explicit direction remains original when the sort field is absent', () => {
  assert.deepEqual(savedReaderRoute({view: 'watchlist', direction: 'asc', filter: 'unwatched'}),
    {view: 'watchlist', direction: 'asc', filter: 'unwatched', sort: 'added'});
});
test('explicit sort/direction, including no explicit direction, are not rebound to the default', () => {
  for (const original of [{view: 'watchlist', sort: 'title', direction: 'asc'}, {view: 'watchlist', sort: 'updated'}] as const) {
    assert.equal(savedReaderRoute(original), original);
  }
});
test('Favorites and other Saved destinations keep their own original query', () => {
  for (const original of [{view: 'favorites'}, {view: 'history', period: '7d'}, {view: 'downloads'}] as const) {
    assert.equal(savedReaderRoute(original), original);
  }
});


test('the playlist directory shares Last changed/Newest first between its reader and toolbar route', () => {
  const original = Object.freeze({view: 'playlists'} as const), reader = savedReaderRoute(original);
  assert.deepEqual(reader, {view: 'playlists', sort: 'updated', direction: 'desc'});
  assert.equal(sameSavedRoute(reader as SavedRoute, original), false, 'the exact request/visible-route fence stays intact');
  assert.deepEqual(savedReaderRoute({view: 'playlists', direction: 'asc'}), {view: 'playlists', sort: 'updated', direction: 'asc'});
  const explicit = Object.freeze({view: 'playlists', sort: 'title', direction: 'asc'} as const);
  assert.equal(savedReaderRoute(explicit), explicit);
});
