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

test('a title row continues through its row route under its id', async () => {
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
  // Another row is refused. A page answered under a moved fence is the newer answer (step 7): a cursor from before
  // the move is the server's to refuse (invalid_cursor), and that refusal ends the row (next test).
  await assert.rejects(readRecommendationRowPage(api(() => row({id: 'genre:tmdb:99'})), {kind: 'item', id: 't1'}, first));
  assert.equal((await readRecommendationRowPage(api(() => row({viewerFence: 'moved'})), {kind: 'item', id: 't1'}, first)).viewerFence, 'moved');
});

test('a failed page keeps the continuation; a refused one reads the row again once; a stale one ends it', async () => {
  let fail: unknown = Object.assign(new Error('offline'), {code: 'request_failed'});
  const pager = new RelationRowPager({entries: [movie('m1')] as any, nextCursor: 'c1'}, async () => { if (fail) throw fail; return {entries: [movie('m2')] as any}; });
  await pager.more();
  assert.equal(pager.getSnapshot().cursor, 'c1');
  assert.equal(pager.getSnapshot().error?.stale, false);
  fail = Object.assign(new Error('stale'), {code: 'stale_continuation'});
  await pager.more();
  assert.equal(pager.getSnapshot().cursor, undefined);
  assert.equal(pager.getSnapshot().error?.stale, true);
  // A refused cursor (invalid_cursor: its fence or profile no longer matches, so access was decided again) reads the
  // row once from its first page, whose entries and cursor replace what was shown: nothing allowed is hidden and
  // nothing read under the old fence stays. Later pages continue the new first page's revision.
  const reads: string[] = [];
  const reread = recommendationRowPager(api(path => { reads.push(path); return path.includes('cursor=c1') ? Object.assign(new Error('Expired'), {code: 'invalid_cursor'})
    : path.includes('cursor=c9') ? row({entries: [movie('m8')], hasMore: false, nextCursor: '', revision: {catalog: 2, viewer: 1}})
    : row({viewerFence: 'moved', entries: [movie('m7'), movie('m1')], nextCursor: 'c9', revision: {catalog: 2, viewer: 1}}); }), {kind: 'item', id: 't1'}, validateHomeRow(row({entries: [movie('m1'), movie('m2')]})));
  await reread.more();
  assert.deepEqual(reread.getSnapshot().entries.map(e => e.id), ['m7', 'm1'], 'the first page read again replaces the row: m2, read under the old fence, is gone');
  assert.equal(reread.getSnapshot().cursor, 'c9'); assert.equal(reread.getSnapshot().error, undefined);
  assert.equal(reads[1], '/v1/items/t1/recommendations/rows/genre%3Atmdb%3A18?limit=37', 'the first page is read without a cursor');
  await reread.more();
  assert.deepEqual(reread.getSnapshot().entries.map(e => e.id), ['m7', 'm1', 'm8']);
  // A second refusal ends the row: one re-read per row, never a loop.
  let tries = 0;
  const twice = recommendationRowPager(api(path => { tries++; return path.includes('cursor=') ? Object.assign(new Error('Expired'), {code: 'invalid_cursor'}) : row({entries: [movie('m3')], nextCursor: 'c2'}); }), {kind: 'item', id: 't1'}, validateHomeRow(row()));
  await twice.more();
  assert.deepEqual(twice.getSnapshot().entries.map(e => e.id), ['m3']); assert.equal(twice.getSnapshot().cursor, 'c2');
  await twice.more(); await twice.more();
  assert.equal(tries, 3); assert.equal(twice.getSnapshot().cursor, undefined); assert.deepEqual(twice.getSnapshot().error, {code: 'invalid_cursor', stale: true});
  assert.deepEqual(twice.getSnapshot().entries.map(e => e.id), ['m3'], 'what was shown stays when the row ends');
  // A later, separate access change is read again too: the guard clears once a continuation succeeds.
  // refuse → re-read → next page → refuse → re-read again.
  const seq: string[] = []; let refusals = 0;
  const later = recommendationRowPager(api(path => {
    seq.push(path.includes('cursor=') ? path.split('cursor=')[1]! : 'first');
    if (path.includes('cursor=c1') || path.includes('cursor=c3')) { refusals++; return Object.assign(new Error('Expired'), {code: 'invalid_cursor'}); }
    if (path.includes('cursor=c2')) return row({entries: [movie('m5')], nextCursor: 'c3'});
    return row({entries: [movie(refusals === 1 ? 'm4' : 'm6')], nextCursor: refusals === 1 ? 'c2' : 'c4'});
  }), {kind: 'item', id: 't1'}, validateHomeRow(row()));
  await later.more(); await later.more(); await later.more();
  assert.deepEqual(seq, ['c1', 'first', 'c2', 'c3', 'first']);
  assert.deepEqual(later.getSnapshot().entries.map(e => e.id), ['m6']); assert.equal(later.getSnapshot().cursor, 'c4'); assert.equal(later.getSnapshot().error, undefined);
  // A first page that fails on the re-read ends the row too.
  const gone = recommendationRowPager(api(() => Object.assign(new Error('Expired'), {code: 'invalid_cursor'})), {kind: 'item', id: 't1'}, validateHomeRow(row()));
  await gone.more();
  assert.equal(gone.getSnapshot().cursor, undefined); assert.equal(gone.getSnapshot().error?.stale, true);
  // A real move of the row (its revision) is a stale continuation and still ends it, with no re-read.
  let movedReads = 0;
  const moved = recommendationRowPager(api(() => { movedReads++; return row({revision: {catalog: 2, viewer: 1}}); }), {kind: 'item', id: 't1'}, validateHomeRow(row()));
  await moved.more();
  assert.equal(movedReads, 1); assert.equal(moved.getSnapshot().error?.code, 'stale_continuation'); assert.equal(moved.getSnapshot().cursor, undefined);
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
  // A refused cursor reads the row's first page ('', no cursor) under its new revision, which replaces the row and continues.
  const paths: string[] = [];
  const again = relatedRowPager(api(path => { paths.push(path); return path.includes('cursor=c1') ? Object.assign(new Error('Expired'), {code: 'invalid_cursor'})
    : path.includes('cursor=c2') ? row({entries: [movie('m5')], hasMore: false, nextCursor: '', revision: {catalog: 3, viewer: 1}})
    : row({entries: [movie('m1'), movie('m4')], nextCursor: 'c2', revision: {catalog: 3, viewer: 1}}); }), source, related.rows[0]!, {viewerFence: 'fence', revision: {catalog: 1, viewer: 1}});
  await again.more(); await again.more();
  assert.equal(paths[1], '/v1/items/t1/recommendations/rows/genre%3Atmdb%3A18?limit=37');
  assert.deepEqual(again.getSnapshot().entries.map(e => e.id), ['m1', 'm4', 'm5']); assert.equal(again.getSnapshot().error, undefined);
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
