import test from 'node:test';
import assert from 'node:assert/strict';
import {listeningSimilarPager, readRecommendationRowPage, recommendationRowPager, RelationRowPager} from '../src/relation-rows.ts';
import {relatedRowPager, validateRelatedMovies} from '../src/related-movies.ts';
import {validateHomeRow} from '../src/home.ts';
import {readListeningSimilar} from '../src/library-content.ts';

// Relation rows page to their true end (JUSTIN-ANSWERS #34; WEB-050, TV-005, TV-019, MOB-054).

const movie = (id: string) => ({id, kind: 'movie', title: `Title ${id}`, libraryId: 'lib', available: true, navigation: {view: 'item', entityId: id}});
function row(changes: Record<string, unknown> = {}): any {
  return {id: 'genre:tmdb:18', title: 'More Drama movies', titleText: {fallback: 'More Drama movies'}, kind: 'related', artworkShape: 'poster',
    endpoint: '/v1/items/t1/recommendations/rows/genre:tmdb:18', viewerFence: 'fence', privacySensitivity: 'catalog', policyState: 'available',
    priority: 0, cacheTtlMs: 300000, required: false, hideable: true, reorderable: false, critical: false, cursorCapable: true,
    entries: [movie('m1'), movie('m2')], total: 5, start: 0, limit: 2, hasMore: true, nextCursor: 'c1', revision: {catalog: 1, viewer: 1}, relation: 'genre', ...changes};
}
const api = (handler: (path: string) => unknown) => {
  const paths: string[] = [];
  return {paths, request: async <T>(path: string) => { paths.push(path); const value = handler(path); if (value instanceof Error) throw value; return value as T; }};
};

test('a title row continues through its row route under its id and fence', async () => {
  const first = validateHomeRow(row());
  const server = api(path => path.includes('cursor=c1') ? row({entries: [movie('m2'), movie('m3'), movie('m4')], start: 2, nextCursor: 'c2'}) : row({entries: [movie('m5')], start: 5, hasMore: false, nextCursor: ''}));
  const pager = recommendationRowPager(server, {kind: 'item', id: 't1'}, first, 3);
  await pager.more();
  assert.equal(server.paths[0], '/v1/items/t1/recommendations/rows/genre%3Atmdb%3A18?limit=3&cursor=c1');
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2', 'm3', 'm4'], 'a title already shown keeps its first place');
  assert.equal(pager.getSnapshot().cursor, 'c2');
  await pager.more();
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2', 'm3', 'm4', 'm5']);
  assert.equal(pager.getSnapshot().cursor, undefined, 'the row ends');
  await pager.more();
  assert.equal(server.paths.length, 2, 'nothing is read past the end');
  // A show's row continues through the show's route.
  const show = api(() => row({hasMore: false, nextCursor: ''}));
  await readRecommendationRowPage(show, {kind: 'show', id: 's1'}, first, {cursor: 'c1'});
  assert.equal(show.paths[0], '/v1/shows/s1/recommendations/rows/genre%3Atmdb%3A18?limit=37&cursor=c1');
  // Another row, or another viewer's page, is refused.
  await assert.rejects(readRecommendationRowPage(api(() => row({id: 'genre:tmdb:99'})), {kind: 'item', id: 't1'}, first));
  await assert.rejects(readRecommendationRowPage(api(() => row({viewerFence: 'other'})), {kind: 'item', id: 't1'}, first));
});

test('a failed page keeps the continuation, and a stale one ends the row', async () => {
  let fail: unknown = Object.assign(new Error('offline'), {code: 'request_failed'});
  const pager = new RelationRowPager({entries: [movie('m1')] as any, nextCursor: 'c1'}, async () => { if (fail) throw fail; return {entries: [movie('m2')] as any}; });
  await pager.more();
  assert.equal(pager.getSnapshot().cursor, 'c1');
  assert.equal(pager.getSnapshot().error?.stale, false);
  fail = Object.assign(new Error('stale'), {code: 'stale_continuation'});
  await pager.more();
  assert.equal(pager.getSnapshot().cursor, undefined);
  assert.equal(pager.getSnapshot().error?.stale, true);
  // A moved revision is a stale continuation too.
  const moved = recommendationRowPager(api(() => row({revision: {catalog: 2, viewer: 1}})), {kind: 'item', id: 't1'}, validateHomeRow(row()));
  await moved.more();
  assert.equal(moved.getSnapshot().error?.code, 'stale_continuation');
});

test('one more() at a time, and nothing after dispose', async () => {
  let release!: () => void;
  let reads = 0;
  const pager = new RelationRowPager({entries: [], nextCursor: 'c1'}, () => { reads++; return new Promise(resolve => { release = () => resolve({entries: [movie('m1')] as any}); }); });
  const first = pager.more();
  await pager.more();
  assert.equal(reads, 1);
  assert.equal(pager.getSnapshot().loading, true);
  pager.dispose();
  release();
  await first;
  assert.deepEqual(pager.getSnapshot().entries, []);
});

test('a related row carries its continuation and pages only related titles', async () => {
  const source = {libraryId: 'lib', itemId: 't1', kind: 'movie'};
  const related = validateRelatedMovies({version: 1, libraryId: 'lib', libraryName: 'Movies', rows: [{id: 'genre:tmdb:18', relation: 'genre', provider: 'tmdb', evidenceId: '18', heading: 'More Drama movies', entries: [movie('m1')], hasMore: true, nextCursor: 'c1'}]}, source);
  assert.equal(related.rows[0]!.nextCursor, 'c1');
  const server = api(() => row({entries: [movie('m2'), movie('t1'), {...movie('m3'), kind: 'show'}], hasMore: false, nextCursor: ''}));
  const pager = relatedRowPager(server, source, related.rows[0]!, {viewerFence: 'fence', revision: {catalog: 1, viewer: 1}});
  await pager.more();
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['m1', 'm2'], 'the title itself and another kind are left out');
  assert.equal(pager.getSnapshot().cursor, undefined);
});

test('the similar row pages by cursor on its first basis', async () => {
  const scope = {serverId: 'srv', authority: 'local', accountId: 'a', profileId: 'p'};
  const album = (id: string) => ({id, kind: 'album', title: id, libraryId: 'music', navigation: {view: 'album', entityId: id}});
  const page = (entries: unknown[], next?: string, bySound = true) => ({scope: {serverId: 'srv', authority: 'local', accountId: 'a', profileId: 'p'}, data: {itemId: 'a1', libraryId: 'music', bySound, revision: {catalog: 1, viewer: 1}, entries, ...(next ? {hasMore: true, nextCursor: next} : {hasMore: false})}});
  const server = api(path => path.includes('cursor=') ? page([album('b2')]) : page([album('b1')], 'c1'));
  const first = await readListeningSimilar(server, scope, 'a1', {by: 'sound', limit: 24});
  assert.equal(first.nextCursor, 'c1');
  const pager = listeningSimilarPager(server, scope, first, {limit: 24});
  await pager.more();
  assert.equal(server.paths[1], '/v1/items/a1/similar?limit=24&by=sound&cursor=c1');
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['b1', 'b2']);
  // A page on another basis is a stale continuation.
  const switched = listeningSimilarPager(api(() => page([album('b3')], undefined, false)), scope, first);
  await switched.more();
  assert.equal(switched.getSnapshot().error?.stale, true);
});
