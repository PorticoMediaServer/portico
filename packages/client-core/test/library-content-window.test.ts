import test from 'node:test';
import assert from 'node:assert/strict';
import {contentViewRehydratable, libraryContentPages, type ContentEntry, type ContentRoute} from '../src/library-content.ts';
import {createWindowedCollection} from '../src/collections/windowed.ts';
import {serverPath} from '../src/query/server-api.ts';

const scope = {viewerId: JSON.stringify(['hosted', 'account', 'profile']), serverId: 'server'};
const route: ContentRoute = {libraryId: 'movies', view: 'collection', entityId: 'collection', sort: 'title'};
const tick = () => new Promise<void>(resolve => setTimeout(resolve, 0));
async function settle() {for (let n = 0; n < 10; n++) await tick();}
/** A start-addressed item list inside a collection. */
function server(total: number) {
  const reads: number[] = [];
  const api = {request: async <T>(path: string): Promise<T> => {
    if (path === serverPath('GET /v1/libraries/{id}', {id: route.libraryId})) return {id: route.libraryId, kind: 'movie'} as T;
    if (path === serverPath('GET /v1/items/{id}', {id: route.entityId})) return {id: route.entityId, kind: 'collection', title: 'Collection'} as T;
    const u = new URL(path, 'https://server.test'), start = Number(u.searchParams.get('at') ?? 0), limit = Number(u.searchParams.get('limit'));
    assert.equal(u.pathname, serverPath('GET /v1/items'));
    assert.equal(u.searchParams.get('collection'), route.entityId);
    assert.equal(u.searchParams.get('cursor'), null, 'a window never pages by cursor');
    reads.push(start);
    const items = Array.from({length: Math.max(0, Math.min(limit, total - start))}, (_, n) => ({id: `t${start + n}`, kind: 'movie', title: `Title ${start + n}`}));
    return {items, total} as T;
  }};
  return {api, reads, shrinkTo: (next: number) => {total = next;}};
}
const window = (fetchPage: ReturnType<typeof libraryContentPages>) =>
  createWindowedCollection<ContentEntry>({fetchPage, pageSize: 10, maxResidentPages: 3, prefetchPages: 0, maxConcurrent: 1, keyOf: entry => entry.id});

test('a long section holds a bounded window and reads evicted pages again by their index', async () => {
  const s = server(1000), collection = window(libraryContentPages({api: s.api, scope, route, pageSize: 10}));
  try {
    collection.ensureRange(0, 9); await settle();
    assert.equal(collection.getSnapshot().total, 1000);
    for (let first = 10; first <= 500; first += 10) {collection.ensureRange(first, first + 9); await settle();}
    assert.ok(collection.residentItems().length <= 30, 'only a few pages stay resident however far it scrolls');
    assert.equal(collection.itemAt(0), undefined, 'the first page left memory');
    const before = s.reads.length;
    collection.ensureRange(0, 9); await settle();
    assert.equal(collection.itemAt(0)?.id, 't0', 'back-scroll reads it again');
    assert.deepEqual(s.reads.slice(before), [0]);
    const jump = s.reads.length;
    collection.ensureRange(250, 259); await settle();
    assert.equal(collection.itemAt(255)?.id, 't255');
    const reads = s.reads.slice(jump);
    assert.ok(reads.includes(250) && reads.every(start => start >= 240), `any page is read at its own start, not by walking from the top: ${reads}`);
  } finally {collection.dispose();}
});

test('empty windows and later pages beyond the end carry the collection total', async () => {
  const empty = server(0), signal = new AbortController().signal;
  assert.deepEqual(await libraryContentPages({api: empty.api, scope, route, pageSize: 10})(0, 10, signal), {items: [], total: 0});
  const shrunk = server(20), pages = libraryContentPages({api: shrunk.api, scope, route, pageSize: 10});
  await pages(0, 10, signal);
  shrunk.shrinkTo(10);
  assert.deepEqual(await pages(10, 10, signal), {items: [], total: 10});
});

test('only start-addressable views get a window, and pages stay within the bound', async () => {
  assert.deepEqual(['browse', 'collection', 'collections', 'artist', 'discover', 'show', 'songs'].map(view => contentViewRehydratable(view as ContentRoute['view'])),
    [false, true, false, true, false, false, false]);
  assert.throws(() => libraryContentPages({api: server(1).api, scope, route: {libraryId: 'movies', view: 'discover'}}), /Invalid content window/);
  const pages = libraryContentPages({api: server(100).api, scope, route, pageSize: 10}), signal = new AbortController().signal;
  await assert.rejects(pages(0, 11, signal), /Invalid content window page/);
  await assert.rejects(pages(-1, 10, signal), /Invalid content window page/);
});
