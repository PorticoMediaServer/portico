import test from 'node:test';
import assert from 'node:assert/strict';
import {listeningSimilarPager, recommendationRowPager, RelationRowPager} from '../src/relation-rows.ts';
import {readListeningSimilar} from '../src/library-content.ts';
import {fetchItemRecommendations} from '../src/recommendations.ts';

// Relation rows page to their true end (JUSTIN-ANSWERS #34; WEB-050, TV-005, TV-019, MOB-054).

const movie = (id: string) => ({id, kind: 'movie', title: `Title ${id}`, libraryId: 'lib', available: true, navigation: {view: 'item', entityId: id}});
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

test('a listening row with no continuation makes no later request', async () => {
  const scope = {serverId: 'srv', authority: 'local', accountId: 'a', profileId: 'p'};
  const album = (id: string) => ({id, kind: 'album', title: id, libraryId: 'music'});
  const server = api(path => path.includes('/related') ? {groups: [{relation: 'more_like_this', items: [album('b1')]}]} : album('a1'));
  const first = await readListeningSimilar(server, scope, 'a1', {by: 'sound', limit: 24});
  const pager = listeningSimilarPager(server, scope, first, {limit: 24});
  await pager.more();
  assert.deepEqual(server.paths, ['/v1/items/a1', '/v1/items/a1/related?relation=more_like_this&limit=24']);
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['b1']);
  assert.equal(pager.getSnapshot().cursor, undefined);
});


for (const kind of ['item', 'show'] as const) test(`${kind} More like this follows ranked cursors through an empty page to the true end`, async () => {
  const wire = (id: string) => ({id, kind: 'movie', title: id, libraryId: 'lib'});
  const server = api(path => {
    const cursor = new URL(path, 'http://test').searchParams.get('cursor');
    return {groups: [{relation: 'more_like_this', items: cursor === 'c1' ? [wire('m2'), wire('m3')] : cursor === 'c2' ? [] : cursor === 'c3' ? [wire('m4')] : [wire('m1'), wire('m2')],
      ...(cursor === 'c3' ? {} : {nextCursor: cursor === 'c1' ? 'c2' : cursor === 'c2' ? 'c3' : 'c1'})}]};
  });
  const first = (await fetchItemRecommendations(server, 'seed')).rows[0]!;
  const pager = recommendationRowPager(server, {kind, id: 'seed'}, first);
  try {
    assert.equal(pager.getSnapshot().cursor, 'c1');
    for (let n = 0; n < 3; n++) await pager.more();
    assert.deepEqual(pager.getSnapshot().entries.map(entry => entry.id), ['m1', 'm2', 'm3', 'm4']);
    assert.equal(pager.getSnapshot().cursor, undefined);
    await pager.more();assert.equal(server.paths.length, 4);
    assert.equal(server.paths[1], '/v1/items/seed/related?relation=more_like_this&cursor=c1&limit=15');
  } finally {pager.dispose();}
});

test('an expired ranking replaces the old first page once, then continues its new cursor', async () => {
  const reads: string[] = [];
  const pager = new RelationRowPager({entries: [movie('old')] as any, nextCursor: 'expired'}, async cursor => {
    reads.push(cursor);
    if (cursor === 'expired') throw Object.assign(new Error('ranking changed'), {code: 'cursor_expired'});
    return cursor ? {entries: [movie('last')] as any} : {entries: [movie('fresh')] as any, nextCursor: 'new'};
  });
  try {
    await pager.more();assert.deepEqual(pager.getSnapshot().entries.map(entry => entry.id), ['fresh']);
    await pager.more();assert.deepEqual(pager.getSnapshot().entries.map(entry => entry.id), ['fresh', 'last']);
    assert.deepEqual(reads, ['expired', '', 'new']);assert.equal(pager.getSnapshot().cursor, undefined);
  } finally {pager.dispose();}
});

test('a listening row retains its continuation and later page order', async () => {
  const album = (id: string) => ({id, kind: 'album', title: id, libraryId: 'music'});
  const server = api(path => path.includes('/related') ? {groups: [{relation: 'more_like_this', items: [album(path.includes('cursor=') ? 'last' : 'first')], ...(path.includes('cursor=') ? {} : {nextCursor: 'next'})}]} : album('seed'));
  const scope = {serverId: 'srv', authority: 'local', accountId: 'a', profileId: 'p'};
  const first = await readListeningSimilar(server, scope, 'seed');
  const pager = listeningSimilarPager(server, scope, first);
  try {
    await pager.more();assert.deepEqual(pager.getSnapshot().entries.map(entry => entry.id), ['first', 'last']);
    assert.equal(pager.getSnapshot().cursor, undefined);
    assert.equal(server.paths.at(-1), '/v1/items/seed/related?relation=more_like_this&cursor=next&limit=24');
  } finally {pager.dispose();}
});
