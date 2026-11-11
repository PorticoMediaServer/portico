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
