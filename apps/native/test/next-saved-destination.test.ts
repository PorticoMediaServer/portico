import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeSavedDestination, savedDirectoryCount, savedProjectionCount} from '../src-next/screens/saved/model.ts';
import type {SavedApi} from '../../../packages/client-core/src/saved.ts';
import {legacySavedRequest} from '../../../packages/client-core/test/fixtures/legacy-saved.ts';

const scope = {serverId: 'server-a', viewerId: 'local/account-a/profile-a'};
function response(path: string) {
  const url = new URL(path, 'https://test.invalid');
  if (url.pathname.endsWith('/saved/summary')) return {serverId: scope.serverId, viewerFence: 'summary-fence', revision: {catalog: 1, viewer: 1},
    myList: {posters: 12, squares: 3}, favorites: {posters: 21, squares: 3}, playlists: 3, collections: 11, savedViews: 6};
  if (url.pathname.includes('saved-pins')) return {serverId: scope.serverId, viewerFence: 'pins-fence', revision: 1,
    order: [{kind: 'collection', id: 'second-by-name', name: 'Zebra', pinRevision: 1}, {kind: 'playlist', id: 'first-by-name', name: 'Alpha', pinRevision: 2}]};
  if (url.pathname.includes('saved-resources')) return {serverId: scope.serverId, viewerFence: 'resources-fence', revision: 1,
    resources: [], nextCursor: url.searchParams.get('kind') === 'collection' ? 'next-resource-page' : ''};
  const view = url.searchParams.get('view')!;
  return {
    scope: {serverId: scope.serverId, libraryId: '', libraryKind: 'mixed', view, entityId: '', viewerFence: 'saved-fence'},
    revision: {catalog: 1, viewer: 1}, heading: {key: 'saved.title', fallback: ''},
    navigation: [{id: view, labelKey: 'saved.title', view}],
    query: {sort: 'title', direction: 'asc', category: view === 'playlists' ? '' : 'all', q: '', limit: 40, searchMode: 'none'},
    sorts: [], filters: [], ...(view === 'watchlist' ? {shapeCounts: {posters: 12, squares: 3}} : {}), ...(view === 'playlists' ? {actions: []} : {}), sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [], totalCount: 12, nextCursor: ''}],
  };
}

test('the Saved hub reads the summary, one page per drawn My List shape and the pin order, and never scans directories', async () => {
  // `asked` is what the app asks the rebuilt server; `calls` is what the old-document fixture behind it is asked.
  const calls: string[] = [], asked: string[] = [];
  const legacy = legacySavedRequest((async (path: string) => {calls.push(path); return response(path);}) as never);
  const api: SavedApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {asked.push(path); return legacy(path, method, body, signal) as T;}};
  const hub = new NativeSavedDestination({api, scope, requestId: async () => 'operation-id', current: () => true});
  try {
    const loading = hub.load(); assert.equal(hub.load(), loading, 'a concurrent mount shares the same bounded read');
    await loading;
    const snapshot = hub.getSnapshot();
    assert.equal(snapshot.phase, 'ready');
    const query = (path: string) => new URL(path, 'https://test.invalid').searchParams;
    const pages = asked.filter(path => path.startsWith('/v1/items?') && query(path).get('limit') !== '1');
    assert.deepEqual(pages.map(path => query(path).get('kind')).sort(), ['artist,album,track', 'movie,show,season,episode,audiobook,book,comic,series,collection'],
      'the poster window and the wide My List squares window are separate reads of real kinds, never a mixed first page');
    assert.ok(asked.filter(path => query(path).get('limit') === '1').every(path => query(path).get('total') === 'true'), 'every count is a counted read of one title');
    assert.equal(asked.filter(path => path.startsWith('/v1/lists?')).length, 1, 'the landing totals read the lists once');
    assert.equal(calls.filter(path => path.includes('saved-pins')).length, 1, 'the genuine pin order is read once');
    assert.ok(asked.every(path => !path.includes('cursor=')), 'the hub does not scan directories or whole libraries');
    assert.deepEqual(snapshot.pins.map(pin => pin.id), ['second-by-name', 'first-by-name']);
    assert.equal(savedProjectionCount(snapshot.myList), 12);
    assert.equal(snapshot.summary?.myList.squares, 3, 'the hub total includes real squares outside the poster preview');
    assert.equal(snapshot.summary?.collections, 11, 'the directory total comes from summary, never a first page');
    assert.equal(savedDirectoryCount(snapshot.collections), undefined, 'an unread resource directory has no inferred total');
    assert.equal(calls.some(path => path.includes('saved-resources')), false, 'the hub no longer fetches resource pages just to count them');
  } finally {hub.dispose();}
});

test('retiring the actual viewer aborts pending reads and clears all private hub snapshots', async () => {
  const pending: {path: string; resolve: (value: unknown) => void; signal?: AbortSignal}[] = [];
  let current = true;
  const api: SavedApi = {request: <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => new Promise<T>(resolve => {pending.push({path, signal, resolve: value => resolve(value as T)});})};
  api.request = legacySavedRequest(api.request.bind(api) as never) as typeof api.request;
  const hub = new NativeSavedDestination({api, scope, requestId: async () => 'operation-id', current: () => current});
  const loading = hub.load();
  current = false;
  const closed = hub.getSnapshot();
  assert.equal(closed.phase, 'idle'); assert.deepEqual(closed.pins, []);
  assert.equal(closed.myList.projection, null); assert.deepEqual(closed.collections.resources, []);
  assert.equal(closed.summary, undefined);
  assert.ok(pending.every(read => read.signal?.aborted));
  for (const read of pending) read.resolve(response(read.path));
  await loading;
  assert.equal(hub.getSnapshot(), closed, 'late reads cannot recreate the retired profile view');
});

test('an authenticated producer failure is retained as an error, not an empty Saved success', async () => {
  const api: SavedApi = {request: async <T>(path: string) => {
    if (path.endsWith('/saved/summary')) throw Object.assign(new Error('private diagnostic'), {status: 403, code: 'forbidden'});
    return response(path) as T;
  }};
  api.request = legacySavedRequest(api.request.bind(api) as never) as typeof api.request;
  const hub = new NativeSavedDestination({api, scope, requestId: async () => 'operation-id', current: () => true});
  try {await hub.load(); assert.equal(hub.getSnapshot().phase, 'error'); assert.ok(hub.getSnapshot().error);}
  finally {hub.dispose();}
});
