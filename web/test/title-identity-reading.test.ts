import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readTitleIdentity} from '../src/platform/title-identity.ts';
import {testQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

// P2-74 E-web-23: after a Reading open failed, the bare /item route read GET /v1/items/{id}, which
// serves timed media only and answers 404 for a book, so the title seemed to vanish. The title's
// own detail route serves it under the same privacy fence.
const book = {scope: {serverId: 'server', libraryId: 'reading', itemId: 'book', viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1}, item: {id: 'book', libraryId: 'reading', kind: 'book', title: 'Atlas', posterUrl: '/art/book'},
  personal: {watchlisted: false, favorite: false, watched: false, rating: null, revision: 1, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: []}, actions: []};
const api = (answer: (path: string) => unknown) => {
  const paths: string[] = [];
  return {paths, queries: undefined as never, request: async <T,>(path: string) => {paths.push(path); const value = answer(path); if (value instanceof Error) throw value; return value as T;}};
};

test('a Reading title resolves from its detail route, never the timed-media item read', async () => {
  const fake = api(path => path.startsWith('/v1/items/book/detail') ? book : new Error('404 not_found'));
  const identity = await readTitleIdentity(testQueries(fake, {serverId: 'server', viewerId: 'viewer'}), 'book');
  assert.deepEqual(fake.paths, ['/v1/items/book/detail?related=none']);
  assert.deepEqual(identity, {id: 'book', libraryId: 'reading', kind: 'book', title: 'Atlas', posterUrl: '/art/book'});
});

test('an answer naming another title or library is refused (the shared page read refuses it)', async () => {
  for (const wrong of [
    {...book, item: {...book.item, id: 'other'}},
    {...book, scope: {...book.scope, itemId: 'other'}},
    {...book, scope: {...book.scope, libraryId: 'elsewhere'}},
    {...book, item: {...book.item, libraryId: ''}},
    {scope: book.scope},
  ]) await assert.rejects(readTitleIdentity(testQueries(api(() => wrong), {serverId: 'server', viewerId: 'viewer'}), 'book'));
});

test('the route guard, the page and a menu asking for one title share one read', async () => {
  const fake = api(path => path.startsWith('/v1/items/book/detail') ? book : new Error('404 not_found'));
  const queries = testQueries(fake, {serverId: 'server', viewerId: 'viewer'});
  const [a, b] = await Promise.all([readTitleIdentity(queries, 'book'), readTitleIdentity(queries, 'book')]);
  assert.deepEqual(a, b);
  await readTitleIdentity(queries, 'book');
  assert.deepEqual(fake.paths, ['/v1/items/book/detail?related=none']);
  queries.dispose();
});
