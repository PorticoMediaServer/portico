// Adding titles to playlists and collections on the rebuilt server: the rows, the additions, a new list made with the
// titles in it, and a whole view resolved to its titles (area 6 rules 24, 25, 27, 29, 33).
import test from 'node:test';
import assert from 'node:assert/strict';
import {addToDestination, createDestination, readDestinationPage, selectionItemIds, LIST_ADD_AT_ONCE} from '../src/home-selection/list-destinations.ts';
import {detailProjection} from '../src/detail.ts';

type Call = {path: string; method: string; body?: any; headers?: Record<string, string>};
const list = (id: string, extra: Record<string, unknown> = {}) => ({id, kind: 'playlist', name: id, owner: {id: 'me', name: 'Me'}, role: 'owner', count: 0, covers: [], revision: '3',
  createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', ...extra});
function server(answer: (call: Call, url: URL) => unknown, raw = false) {
  const calls: Call[] = [];
  const api: any = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {const call = {path, method, body}; calls.push(call); return answer(call, new URL(path, 'https://x')) as T;}};
  if (raw) api.requestRaw = async (path: string, method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) => {
    const call = {path, method, body: options.body, headers: options.headers}; calls.push(call);
    return {status: 200, headers: {}, body: answer(call, new URL(path, 'https://x'))};
  };
  return {api, calls};
}

test('the rows are the profile\'s lists of the kind, searched by name, and only those it made or may edit can be added to', async () => {
  const s = server((_call, url) => url.searchParams.get('cursor') ? {items: [list('Zoo', {role: 'viewer'})]} : {items: [list('Road trip', {covers: [{poster: '/img/1'}]}), list('Party', {role: 'editor'}), list('Hers', {role: 'viewer'})], nextCursor: 'more'});
  const page = await readDestinationPage(s.api, {kind: 'playlist', query: '', limit: 40});
  assert.deepEqual(page.rows.map(row => [row.name, row.canAdd]), [['Road trip', true], ['Party', true], ['Hers', false], ['Zoo', false]]);
  assert.equal(page.rows[0]!.artwork[0]!.posterUrl, '/img/1', 'a list\'s cover is drawn on its row');
  assert.deepEqual(s.calls.map(call => new URL(call.path, 'https://x').searchParams.get('kind')), ['playlist', 'playlist'], 'every page of the profile\'s playlists');
  const found = await readDestinationPage(s.api, {kind: 'playlist', query: 'par', limit: 40});
  assert.deepEqual(found.rows.map(row => row.name), ['Party']);
  const paged = await readDestinationPage(s.api, {kind: 'playlist', query: '', limit: 2});
  assert.deepEqual([paged.rows.length, paged.nextCursor], [2, '2']);
  assert.deepEqual((await readDestinationPage(s.api, {kind: 'playlist', query: '', limit: 2, cursor: '2'})).rows.map(row => row.name), ['Hers', 'Zoo']);
});

test('an addition goes in parts of 500 under one key per part, the same on a retry, and says what was already there', async () => {
  const s = server(call => ({list: list('pl', {revision: '9', count: 600, kind: 'collection'}), added: call.body.items.length - 1, already: 1}), true);
  const ids = Array.from({length: 600}, (_, n) => `t${n}`), seen: number[] = [];
  const done = await addToDestination(s.api, 'pl', ids, 'op', undefined, sent => seen.push(sent));
  assert.deepEqual(s.calls.map(call => [call.path, call.body.items.length, call.headers?.['Idempotency-Key']]), [['/v1/lists/pl/entries', 500, 'op.0'], ['/v1/lists/pl/entries', 100, 'op.1']]);
  assert.deepEqual([done.added, done.already, done.revision, seen], [598, 2, 9, [500, 600]]);
  await addToDestination(s.api, 'pl', ids, 'op');
  assert.deepEqual(s.calls.slice(2).map(call => call.headers?.['Idempotency-Key']), ['op.0', 'op.1'], 'a retry names the same parts');
});

test('a new list is made with the titles in it, and titles past 500 are added after', async () => {
  const s = server(call => call.path === '/v1/lists' ? list('new', {name: call.body.name, count: call.body.items.length}) : {list: list('new', {count: 520, revision: '4'}), added: call.body.items.length, already: 0});
  const made = await createDestination(s.api, 'playlist', ' Long drive ', Array.from({length: 520}, (_, n) => `t${n}`), 'op');
  assert.deepEqual(s.calls.map(call => [call.path, call.body.name, call.body.items.length]), [['/v1/lists', 'Long drive', 500], ['/v1/lists/new/entries', undefined, 20]]);
  assert.deepEqual([made.row.id, made.row.count, made.added], ['new', 520, 520]);
});

const whole = (total: number) => ({mode: 'query', total, sourceTotal: total, refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: 'r'},
  selector: {query: {libraryId: 'films', pivot: 'movies', filter: {field: 'year', operator: 'equals', value: 1999}, sort: [{field: 'title', direction: 'asc'}]}},
  summary: {count: total, types: [{kind: 'movie', count: total}], library: {kind: 'single', libraryId: 'films'}, every: {}}, context: {}}) as never;

test('a whole view is resolved to its titles in the app, up to 500, and a larger one is refused saying how many can be added at once', async () => {
  const s = server((_call, url) => ({items: Array.from({length: url.searchParams.get('cursor') ? 50 : 200}, (_, n) => ({id: `${url.searchParams.get('cursor') ?? 'a'}${n}`})), ...(url.searchParams.get('cursor') ? {} : {nextCursor: 'b'})}));
  const ids = await selectionItemIds(s.api, whole(250));
  assert.equal(ids.length, 250);
  const query = new URL(s.calls[0]!.path, 'https://x').searchParams;
  assert.deepEqual([query.get('library'), query.get('kind'), query.get('year'), query.get('sort')], ['films', 'movie', '1999', 'title'], 'the view\'s own filter and order');
  const before = s.calls.length;
  await assert.rejects(selectionItemIds(s.api, whole(LIST_ADD_AT_ONCE + 1)), (error: any) => error.code === 'too-many-to-add' && error.limit === 500 && error.total === 501);
  assert.equal(s.calls.length, before, 'nothing is read for a view too large to add');
});

test('a title\'s page offers Add to playlist and Add to collection to every profile', () => {
  const state = {positionMs: 0, watched: false, playCount: 0, favorite: false, inList: false, notInterested: false};
  for (const kind of ['movie', 'episode', 'track', 'show', 'album', 'audiobook', 'book']) {
    const page = detailProjection({id: 'i', kind, title: 'T', libraryId: 'l', state} as never, {serverId: 's', viewerId: 'v'} as never);
    assert.deepEqual(page.actions.filter(action => action.id.startsWith('add_to_')).map(action => [action.id, action.enabled]), [['add_to_playlist', true], ['add_to_collection', true]], kind);
  }
});
