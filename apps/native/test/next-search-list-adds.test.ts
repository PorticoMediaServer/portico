// Add to playlist and Add to collection from native Search, library selection, title actions and the selection screen
// (all through NativeLoadedDestinations), end to end on the rebuilt server: the titles' pages offer the action, the rows
// are the profile's lists, and the titles go in (area 6 rules 24, 25, 27, 33).
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLoadedDestinations} from '../src-next/screens/search/loaded-destinations.ts';

type Call = {path: string; method: string; body?: any};
const state = {positionMs: 0, watched: false, playCount: 0, favorite: false, inList: false, notInterested: false};
const list = (id: string, kind: string, extra: Record<string, unknown> = {}) => ({id, kind, name: id, owner: {id: 'me', name: 'Me'}, role: 'owner', count: 0, covers: [], revision: '5',
  createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', ...extra});
function server() {
  const calls: Call[] = [], revisions: Record<string, number> = {Mix: 5, Best: 5};
  const api = {async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    calls.push({path, method, body});
    const url = new URL(path, 'https://x'), at = decodeURIComponent(url.pathname);
    const title = /^\/v1\/items\/(m\d)$/.exec(at);
    if (method === 'GET' && title) return {id: title[1], kind: 'movie', title: 'Film ' + title[1], libraryId: 'films', available: true, state} as T;
    if (method === 'GET' && at === '/v1/lists') {const kind = url.searchParams.get('kind')!; return {items: [list(kind === 'playlist' ? 'Mix' : 'Best', kind)]} as T;}
    const one = /^\/v1\/lists\/([^/]+)$/.exec(at);
    if (method === 'GET' && one) return list(one[1]!, one[1] === 'Mix' ? 'playlist' : 'collection', {revision: String(revisions[one[1]!])}) as T;
    if (method === 'GET' && /^\/v1\/lists\/[^/]+\/shares$/.test(at)) return {shares: [], people: []} as T;
    if (method === 'GET' && at === '/v1/items' && url.searchParams.get('list')) return {items: [], total: 0} as T;
    const add = /^\/v1\/lists\/([^/]+)\/entries$/.exec(at);
    if (method === 'POST' && add) {
      const item = (body as {items: string[]}).items[0], held = add[1] === 'Best' && item === 'm2';
      if (!held) revisions[add[1]!]!++;
      return {list: list(add[1]!, add[1] === 'Mix' ? 'playlist' : 'collection', {revision: String(revisions[add[1]!])}), added: held ? 0 : 1, already: held ? 1 : 0} as T;
    }
    throw new Error(`unexpected ${method} ${path}`);
  }};
  return {api, calls};
}
const entries = ['m1', 'm2'].map(id => ({id, kind: 'movie', title: 'Film ' + id, libraryId: 'films'}));
function destinations(api: unknown) {
  return new NativeLoadedDestinations({api, queries: undefined as never, scope: {serverId: 's', viewerId: 'v'}, requestId: async () => '00000000-0000-4000-8000-000000000001',
    signal: new AbortController().signal, isCurrent: () => true, viewerFence: 'opened'} as never, entries as never);
}
const posts = (calls: Call[]) => calls.filter(call => call.method === 'POST').map(call => [decodeURIComponent(call.path), call.body.items]);

test('picked titles go into the playlist chosen from the profile\'s playlists', async () => {
  const s = server(), picker = destinations(s.api);
  try {
    await picker.load('playlist');
    const page = picker.snapshot().picker!;
    assert.deepEqual(page.rows.map(row => [row.name, row.canAdd]), [['Mix', true]]);
    await picker.choose('playlist', 'Mix', page.token);
    assert.deepEqual(posts(s.calls), [['/v1/lists/Mix/entries', ['m1']], ['/v1/lists/Mix/entries', ['m2']]]);
    assert.equal(picker.snapshot().completed, 2);
    assert.ok(s.calls.every(call => !call.path.includes('/home/destination') && !call.path.includes('/v1/jobs')), 'nothing asks the old server\'s routes');
  } finally {picker.dispose();}
});

test('picked titles go into a collection, each saying whether it went in or was already there', async () => {
  const s = server(), picker = destinations(s.api);
  try {
    await picker.load('collection');
    const page = picker.snapshot().picker!;
    await picker.choose('collection', 'Best', page.token);
    assert.deepEqual(posts(s.calls), [['/v1/lists/Best/entries', ['m1']], ['/v1/lists/Best/entries', ['m2']]]);
    assert.deepEqual(picker.snapshot().collectionOutcome, {added: ['m1'], unchanged: ['m2'], failed: []});
    assert.equal(picker.snapshot().collection.resource?.name, 'Best');
  } finally {picker.dispose();}
});
