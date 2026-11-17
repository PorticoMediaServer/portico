// Saved views on Home past the first few come deferred (API.md, GET /v1/home): Home keeps the row and reads its titles
// through the row endpoint when it comes near the screen.
import test from 'node:test';
import assert from 'node:assert/strict';
import {HomeService} from '../src/home-service.ts';

const item = (id: string) => ({id, kind: 'movie', title: id, libraryId: 'lib_films', state: {watched: false, playCount: 0, favorite: false, inList: false}});
const deferred = (id: string, query = 'genre=Drama&sort=year') => ({id: 'view:' + id, title: 'View ' + id, shape: 'poster', items: [], deferred: true, library: 'lib_films', query});
function server(rows: {id: string; query?: string; items: unknown[]; title: string; shape: string}[], answers: Record<string, (signal?: AbortSignal) => Promise<object>> = {}) {
  const asked: string[] = [];
  const api = {request: async <T>(path: string, _method = 'GET', _body?: unknown, signal?: AbortSignal): Promise<T> => {
    asked.push(path);
    if (path === '/v1/home') return {rows} as T;
    const url = new URL(path, 'https://x'), id = decodeURIComponent(url.pathname.split('/').at(-1)!);
    const key = url.searchParams.get('cursor') ?? new URLSearchParams(rows.find(row => row.id === id)?.query).get('genre') ?? '';
    if (url.pathname.startsWith('/v1/home/rows/')) return {id, shape: 'poster', ...(answers[key] ? await answers[key]!(signal) : {items: [item('t1'), item('t2')]})} as T;
    throw new Error('unexpected ' + path);
  }};
  return {api, asked};
}
const reads = (asked: string[]) => asked.filter(path => path.startsWith('/v1/home/rows/'));

test('a deferred saved view loads on approach and continues through the same row endpoint', async () => {
  const s = server([{id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [item('a')]}, deferred('v5')], {
    Drama: async () => ({items: [item('t1'), item('t2')], nextCursor: 'page2'}),
    page2: async () => ({items: [item('t2'), item('t3')]}),
  }), home = new HomeService(s.api);
  await home.load();
  const row = home.getSnapshot().document!.rows.find(each => each.id === 'view:v5')!;
  assert.deepEqual([row.deferred, row.entries.length, row.viewRead], [true, 0, {library: 'lib_films', query: 'genre=Drama&sort=year'}]);
  assert.deepEqual(reads(s.asked), [], 'nothing is read until the row comes near');
  home.demand(['view:v5']); home.demand(['view:v5', 'recently_added']);
  await new Promise(resolve => setImmediate(resolve));
  const asked = reads(s.asked);
  assert.equal(asked.length, 1, 'one read per row');
  assert.equal(asked[0], '/v1/home/rows/view%3Av5?limit=20');
  const filled = home.getSnapshot().document!.rows.find(each => each.id === 'view:v5')!;
  assert.deepEqual([filled.deferred, filled.entries.map(entry => entry.id)], [undefined, ['t1', 't2']]);
  home.demand(['view:v5']); await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads(s.asked).length, 1, 'a filled row is not read again');
  assert.deepEqual(await home.more('view:v5'), {rowId: 'view:v5', requestedCursor: 'page2', status: 'applied', applied: true, firstItemId: 't3'});
  assert.equal(s.asked.at(-1), '/v1/home/rows/view%3Av5?cursor=page2');
  const complete = home.getSnapshot().document!.rows[1]!;
  assert.deepEqual(complete.entries.map(entry => entry.id), ['t1', 't2', 't3']);
  assert.equal(complete.nextCursor, '');
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
  const answers = Object.fromEntries(['a', 'b', 'c', 'd', 'e'].map(id => [id, () => new Promise<object>(resolve => gates.push(() => resolve({items: [item('t-' + id)]})))]));
  const s = server(rows, answers), home = new HomeService(s.api);
  await home.load();
  home.demand(rows.map(row => row.id)); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(reads(s.asked), ['a', 'b', 'c'].map(id => '/v1/home/rows/view%3A' + id + '?limit=20'), 'a fast scroll starts three, in Home\'s order');
  gates.shift()!(); await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(reads(s.asked), ['a', 'b', 'c', 'd'].map(id => '/v1/home/rows/view%3A' + id + '?limit=20'), 'a finished read starts the next');
  home.demand(['view:b', 'view:c']); await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads(s.asked).length, 4, 'a row that left is not started; one that left mid-read was stopped');
  while (gates.length) {gates.shift()!(); await new Promise(resolve => setImmediate(resolve));}
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(home.getSnapshot().document!.rows.filter(row => !row.deferred).map(row => row.id), ['view:a', 'view:b', 'view:c']);
});
