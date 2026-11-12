// Add to playlist and Add to collection from a title's menu and from a library selection, end to end on the rebuilt
// server: the title's page offers the action, the rows are the profile's lists, and the titles are added (area 6
// rules 24, 25, 27, 33).
import test from 'node:test';
import assert from 'node:assert/strict';
import {EntryPickerProducer} from '../src/shell/entry-picker.ts';
import {LibraryPickerProducer} from '../src/screens/browse/selection-picker.ts';
import {testQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

type Call = {path: string; method: string; body?: any};
const state = {positionMs: 0, watched: false, playCount: 0, favorite: false, inList: false, notInterested: false};
const list = (id: string, kind: string, extra: Record<string, unknown> = {}) => ({id, kind, name: id, owner: {id: 'me', name: 'Me'}, role: 'owner', count: 1, covers: [], revision: '5',
  createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', ...extra});
/** The rebuilt server: two titles, the profile's playlists and collections, and additions (a collection says what it held). */
function server() {
  const calls: Call[] = [];
  const api = {async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    calls.push({path, method, body});
    const url = new URL(path, 'https://x'), at = decodeURIComponent(url.pathname);
    const title = /^\/v1\/items\/(m\d)$/.exec(at);
    if (method === 'GET' && title) return {id: title[1], kind: 'movie', title: 'Film ' + title[1], libraryId: 'films', available: true, state} as T;
    if (method === 'GET' && at === '/v1/lists') {const kind = url.searchParams.get('kind')!; return {items: [list(kind === 'playlist' ? 'Mix' : 'Best', kind), list('Shared', kind, {role: 'viewer'})]} as T;}
    const one = /^\/v1\/lists\/([^/]+)$/.exec(at);
    if (method === 'GET' && one) return list(one[1]!, one[1] === 'Mix' ? 'playlist' : 'collection') as T;
    const add = /^\/v1\/lists\/([^/]+)\/entries$/.exec(at);
    if (method === 'POST' && add) {const items = (body as {items: string[]}).items; return {list: list(add[1]!, add[1] === 'Mix' ? 'playlist' : 'collection', {count: 3, revision: '6'}), added: add[1] === 'Best' ? items.length - 1 : items.length, already: add[1] === 'Best' ? 1 : 0} as T;}
    if (method === 'POST' && at === '/v1/lists') return list('New', (body as {kind: string}).kind, {count: (body as {items: string[]}).items.length}) as T;
    throw new Error(`unexpected ${method} ${path}`);
  }};
  return {api, calls};
}
const ports = (api: unknown) => ({api, queries: testQueries(api as never, {serverId: 's', viewerId: 'v'}), serverId: 's', viewerId: '["local","a","p"]', isCurrent: () => true,
  requestId: async () => '00000000-0000-4000-8000-000000000001', pickerIdentity: {authority: 'local' as const, accountId: 'a', profileId: 'p', events: {on: () => () => {}}}}) as never;
const posts = (calls: Call[]) => calls.filter(call => call.method === 'POST').map(call => [decodeURIComponent(call.path), call.body]);
const every = {play: true, queue: true, playlist: true, collection: true, myList: true, watched: true, favorite: true, download: false, edit: false, refresh: false, delete: false};

test('a title\'s Add to playlist lists the profile\'s playlists and adds the title to the one chosen', async () => {
  const s = server();
  let shown: any;
  const picker = new EntryPickerProducer({libraryId: 'films', itemId: 'm1', kind: 'movie'} as never, 'playlist', ports(s.api), true, next => {shown = next;});
  try {
    await picker.open();
    assert.deepEqual(shown.rows.map((row: any) => [row.name, row.canAdd]), [['Mix', true], ['Shared', false]]);
    await picker.choose('Mix');
    assert.deepEqual(posts(s.calls), [['/v1/lists/Mix/entries', {items: ['m1']}]]);
    assert.deepEqual([shown.phase, shown.receipt?.added], ['added', 1]);
    assert.ok(s.calls.every(call => !call.path.includes('/home/destination') && !call.path.includes('/v1/jobs')), 'nothing asks the old server\'s routes');
  } finally {picker.close();}
});

test('a title\'s Add to collection says it was already there, and a new collection is made with the title in it', async () => {
  const s = server();
  let shown: any;
  const picker = new EntryPickerProducer({libraryId: 'films', itemId: 'm1', kind: 'movie'} as never, 'collection', ports(s.api), true, next => {shown = next;});
  try {
    await picker.open(); await picker.choose('Best');
    assert.deepEqual([shown.receipt?.added, shown.receipt?.alreadyPresent], [0, 1], 'the collection already held it');
  } finally {picker.close();}
  const fresh = server();
  const again = new EntryPickerProducer({libraryId: 'films', itemId: 'm1', kind: 'movie'} as never, 'collection', ports(fresh.api), true, next => {shown = next;});
  try {
    await again.open(); await again.create('Favourites');
    assert.deepEqual(posts(fresh.calls), [['/v1/lists', {kind: 'collection', name: 'Favourites', items: ['m1']}]]);
    assert.deepEqual([shown.phase, shown.destination?.id], ['added', 'New']);
  } finally {again.close();}
});

test('picked titles in a library go into a playlist in one addition, in their picked order', async () => {
  const s = server();
  let shown: any;
  const picked = ['m2', 'm1'].map(id => ({kind: 'movie', id, libraryId: 'films', capabilities: every}));
  const picker = new LibraryPickerProducer(picked as never, 'playlist', ports(s.api), true, next => {shown = next;});
  try {
    await picker.open(); await picker.choose('Mix');
    assert.deepEqual(posts(s.calls), [['/v1/lists/Mix/entries', {items: ['m2', 'm1']}]]);
    assert.deepEqual([shown.phase, shown.receipt?.added], ['added', 2]);
  } finally {picker.close();}
});
