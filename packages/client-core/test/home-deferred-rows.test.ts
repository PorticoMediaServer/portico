// Saved views on Home past the first few come deferred (API.md, GET /v1/home): Home keeps the row and reads its titles
// with the row's own library and query when it comes near the screen.
import test from 'node:test';
import assert from 'node:assert/strict';
import {HomeService} from '../src/home-service.ts';

const item = (id: string) => ({id, kind: 'movie', title: id, libraryId: 'lib_films', state: {watched: false, playCount: 0, favorite: false, inList: false}});
const deferred = (id: string, query = 'genre=Drama&sort=year') => ({id: 'view:' + id, title: 'View ' + id, shape: 'poster', items: [], deferred: true, library: 'lib_films', query});
function server(rows: unknown[], answers: Record<string, (signal?: AbortSignal) => Promise<unknown>> = {}) {
  const asked: string[] = [];
  const api = {request: async <T>(path: string, _method = 'GET', _body?: unknown, signal?: AbortSignal): Promise<T> => {
    asked.push(path);
    if (path === '/v1/home') return {rows} as T;
    const url = new URL(path, 'https://x'), view = url.searchParams.get('genre') ?? '';
    if (url.pathname === '/v1/items') return (answers[view] ? await answers[view]!(signal) : {items: [item('t1'), item('t2')]}) as T;
    throw new Error('unexpected ' + path);
  }};
  return {api, asked};
}
const reads = (asked: string[]) => asked.filter(path => path.startsWith('/v1/items'));

test('a deferred saved-view row is kept, read once when it comes near with its own library and query, and drawn', async () => {
  const s = server([{id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [item('a')]}, deferred('v5')]), home = new HomeService(s.api);
  await home.load();
  const row = home.getSnapshot().document!.rows.find(each => each.id === 'view:v5')!;
  assert.deepEqual([row.deferred, row.entries.length, row.viewRead], [true, 0, {library: 'lib_films', query: 'genre=Drama&sort=year'}]);
  assert.deepEqual(reads(s.asked), [], 'nothing is read until the row comes near');
  home.demand(['view:v5']); home.demand(['view:v5', 'recently_added']);
  await new Promise(resolve => setImmediate(resolve));
  const asked = reads(s.asked);
  assert.equal(asked.length, 1, 'one read per row');
  assert.deepEqual(Object.fromEntries(new URL(asked[0]!, 'https://x').searchParams), {genre: 'Drama', sort: 'year', library: 'lib_films', limit: '20'});
  const filled = home.getSnapshot().document!.rows.find(each => each.id === 'view:v5')!;
  assert.deepEqual([filled.deferred, filled.entries.map(entry => entry.id)], [undefined, ['t1', 't2']]);
  home.demand(['view:v5']); await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads(s.asked).length, 1, 'a filled row is not read again');
});

test('a row that leaves before its titles arrive stops its read; one that turns out empty is not drawn', async () => {
  let aborted = false;
  const s = server([deferred('slow', 'genre=Slow'), deferred('empty', 'genre=Empty')], {
    Slow: signal => new Promise((_, reject) => signal?.addEventListener('abort', () => {aborted = true; reject(Object.assign(new Error('stopped'), {name: 'AbortError'}));})),
    Empty: async () => ({items: []}),
  }), home = new HomeService(s.api);
  await home.load();
  home.demand(['view:slow', 'view:empty']); home.demand(['view:empty']);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(aborted, true, 'the read of the row that left is stopped');
  assert.deepEqual(home.getSnapshot().document!.rows.map(row => [row.id, !!row.deferred]), [['view:slow', true]], 'the empty view is taken out; the slow one waits');
});

test('a failed read is not repeated while the row stays near, and is asked again once it leaves and returns', async () => {
  let fail = true;
  const s = server([deferred('v1')], {Drama: async () => {if (fail) throw Object.assign(new Error('down'), {status: 503}); return {items: [item('x')]};}}), home = new HomeService(s.api);
  await home.load();
  home.demand(['view:v1']); await new Promise(resolve => setImmediate(resolve));
  home.demand(['view:v1']); await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads(s.asked).length, 1);
  fail = false; home.demand([]); home.demand(['view:v1']); await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads(s.asked).length, 2);
  assert.deepEqual(home.getSnapshot().document!.rows[0]!.entries.map(entry => entry.id), ['x']);
});

test('a quiet re-read that sends the row deferred again keeps the titles already read', async () => {
  const s = server([deferred('v1')]), home = new HomeService(s.api);
  await home.load(); home.demand(['view:v1']); await new Promise(resolve => setImmediate(resolve));
  const before = home.getSnapshot().document!;
  await home.refresh();
  assert.equal(home.getSnapshot().document, before, 'nothing changed on screen');
  assert.equal(reads(s.asked).length, 1);
});

test('at most three deferred rows are read at once; each finished read starts the next demanded row', async () => {
  const gates: (() => void)[] = [];
  const rows = ['a', 'b', 'c', 'd', 'e'].map(id => deferred(id, 'genre=' + id));
  const answers = Object.fromEntries(['a', 'b', 'c', 'd', 'e'].map(id => [id, () => new Promise(resolve => gates.push(() => resolve({items: [item('t-' + id)]})))]));
  const s = server(rows, answers), home = new HomeService(s.api);
  await home.load();
  home.demand(rows.map(row => row.id)); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(reads(s.asked).map(path => new URL(path, 'https://x').searchParams.get('genre')), ['a', 'b', 'c'], 'a fast scroll starts three, in Home\'s order');
  gates.shift()!(); await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(reads(s.asked).map(path => new URL(path, 'https://x').searchParams.get('genre')), ['a', 'b', 'c', 'd'], 'a finished read starts the next');
  home.demand(['view:b', 'view:c']); await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads(s.asked).length, 4, 'a row that left is not started; one that left mid-read was stopped');
  while (gates.length) {gates.shift()!(); await new Promise(resolve => setImmediate(resolve));}
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(home.getSnapshot().document!.rows.filter(row => !row.deferred).map(row => row.id), ['view:a', 'view:b', 'view:c']);
});
