import test from 'node:test';
import assert from 'node:assert/strict';
import {parseSavedScreenRoute} from '../src/screens/saved/route.ts';

test('Saved URL policy keeps opaque resources and drops unowned raw query state', () => {
  assert.deepEqual(parseSavedScreenRoute({resourceId: 'view:opaque/a', token: 'unowned', unexpected: true}), {view: 'resource', resourceId: 'view:opaque/a'});
  assert.deepEqual(parseSavedScreenRoute({view: 'playlist', playlistId: 'opaque:list', shape: 'squares'}), {view: 'playlist', playlistId: 'opaque:list'});
  // A bare /saved is the phone directory; unowned raw keys never name a view.
  assert.deepEqual(parseSavedScreenRoute({}), {view: 'home'});
  assert.deepEqual(parseSavedScreenRoute({token: 'unowned', unexpected: true}), {view: 'home'});
});
test('Saved filters and shapes remain bound to the real destination', () => {
  assert.deepEqual(parseSavedScreenRoute({view: 'watchlist', filter: 'unwatched', shape: 'posters', direction: 'desc', sort: 'added'}),
    {view: 'watchlist', filter: 'unwatched', shape: 'posters', direction: 'desc', sort: 'added'});
  for (const raw of [{view: 'watchlist', filter: 'mine'}, {view: 'playlists', filter: 'unwatched'},
    {view: 'collections', filter: 'all'}, {view: 'history', shape: 'posters'}, {view: 'favorites', period: '7d'}]) {
    assert.throws(() => parseSavedScreenRoute(raw));
  }
});
test('Saved rejects malformed known state before any service can consume it', () => {
  for (const raw of [{view: 'resource'}, {view: 'playlist', playlistId: ''}, {view: 'unknown'},
    {view: 'watchlist', sort: ['added']}, {view: 'history', period: 'today'}, {view: 'resource', resourceId: 'x\n'}]) {
    assert.throws(() => parseSavedScreenRoute(raw));
  }
  assert.deepEqual(parseSavedScreenRoute({view: 'history', period: '7d'}), {view: 'history', period: '7d'});
});
