import test from 'node:test';
import assert from 'node:assert/strict';
import {contentViewRehydratable, libraryContentPages, type ContentEntry, type ContentRoute} from '../src/library-content.ts';
import {createWindowedCollection} from '../src/collections/windowed.ts';
import {isStaleAnswer} from '../src/freshness-actions.ts';

const scope = {viewerId: JSON.stringify(['hosted', 'account', 'profile']), serverId: 'server'};
const route: ContentRoute = {libraryId: 'movies', view: 'browse', sort: 'title'};
const tick = () => new Promise<void>(resolve => setTimeout(resolve, 0));
async function settle() {for (let n = 0; n < 10; n++) await tick();}
/** A start-addressed server over `total` titles: one `items` grid per page, its start echoed back. */
function server(total: number) {
  const reads: number[] = [];
  let fence = 'fence', catalog = 3;
  const api = {request: async <T>(path: string): Promise<T> => {
    const u = new URL(path, 'https://server.test'), start = Number(u.searchParams.get('start') ?? 0), limit = Number(u.searchParams.get('limit'));
    assert.equal(u.searchParams.get('cursor'), null, 'a window never pages by cursor');
    reads.push(start);
    const entries = Array.from({length: Math.max(0, Math.min(limit, total - start))}, (_, n) => ({id: `t${start + n}`, kind: 'movie', title: `Title ${start + n}`, navigation: {view: 'item', entityId: `t${start + n}`}}));
    return {heading: {key: 'library.title', fallback: 'Movies'}, scope: {serverId: 'server', libraryId: 'movies', libraryKind: 'movie', view: 'browse', entityId: '', viewerFence: fence},
      revision: {catalog, viewer: 1}, query: {sort: 'title', direction: 'asc', category: '', q: '', limit, searchMode: 'none'}, navigation: [], sorts: [], filters: [],
      sections: entries.length ? [{id: 'items', type: 'grid', heading: {key: 'items', fallback: 'Movies'}, entries, totalCount: total, nextCursor: 'opaque', start}] : []} as T;
  }};
  return {api, reads, change: (next: {fence?: string; catalog?: number}) => {fence = next.fence ?? fence; catalog = next.catalog ?? catalog;}};
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

test('a page from another viewer fence or catalogue revision is a changed library, not spliced in', async () => {
  for (const change of [{fence: 'other-fence'}, {catalog: 4}]) {
    const s = server(100), pages = libraryContentPages({api: s.api, scope, route, pageSize: 10}), signal = new AbortController().signal;
    await pages(0, 10, signal);
    s.change(change);
    await assert.rejects(pages(10, 10, signal), (error: unknown) => isStaleAnswer(error));
  }
});

test('an empty library is an empty window; a section that vanished under a later page is stale', async () => {
  const empty = server(0), signal = new AbortController().signal;
  assert.deepEqual(await libraryContentPages({api: empty.api, scope, route, pageSize: 10})(0, 10, signal), {items: [], total: 0});
  const shrunk = server(10), pages = libraryContentPages({api: shrunk.api, scope, route, pageSize: 10});
  await pages(0, 10, signal);
  await assert.rejects(pages(10, 10, signal), (error: unknown) => isStaleAnswer(error));
});

test('only start-addressable views get a window, and pages stay within the bound', async () => {
  assert.deepEqual(['browse', 'collection', 'collections', 'artist', 'discover', 'show', 'songs'].map(view => contentViewRehydratable(view as ContentRoute['view'])),
    [true, true, true, true, false, false, false]);
  assert.throws(() => libraryContentPages({api: server(1).api, scope, route: {libraryId: 'movies', view: 'discover'}}), /Invalid content window/);
  const pages = libraryContentPages({api: server(100).api, scope, route, pageSize: 10}), signal = new AbortController().signal;
  await assert.rejects(pages(0, 11, signal), /Invalid content window page/);
  await assert.rejects(pages(-1, 10, signal), /Invalid content window page/);
});

test('each accepted page hands its projection to the screen without its sections', async () => {
  const s = server(60), seen: unknown[] = [];
  const collection = window(libraryContentPages({api: s.api, scope, route, pageSize: 10, onPage: projection => seen.push(projection)}));
  try {
    collection.ensureRange(0, 9); await settle();
    collection.ensureRange(10, 19); await settle();
    assert.ok(seen.length >= 2);
    const first = seen[0] as {heading: {fallback?: string}; sections: readonly unknown[]; scope: {viewerFence: string}};
    assert.equal(first.heading.fallback, 'Movies');
    assert.equal(first.scope.viewerFence, 'fence');
    assert.deepEqual(first.sections, [], 'entries live only in the window');
    const accepted = seen.length;
    s.change({catalog: 9});
    collection.ensureRange(50, 59); await settle();
    assert.equal(seen.length, accepted, 'a changed library hands over nothing');
  } finally {collection.dispose();}
});
