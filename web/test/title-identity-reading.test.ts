import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readTitleIdentity} from '../src/platform/title-identity.ts';

// P2-74 E-web-23: after a Reading open failed, the bare /item route read GET /v1/items/{id}, which
// serves timed media only and answers 404 for a book, so the title seemed to vanish. The title's
// own detail route serves it under the same privacy fence.
const book = {scope: {serverId: 'server', libraryId: 'reading', itemId: 'book', viewerFence: 'fence'}, item: {id: 'book', libraryId: 'reading', kind: 'book', title: 'Atlas', posterUrl: '/art/book'}};
const api = (answer: (path: string) => unknown) => {
  const paths: string[] = [];
  return {paths, request: async <T,>(path: string) => {paths.push(path); const value = answer(path); if (value instanceof Error) throw value; return value as T;}};
};

test('a Reading title resolves from its detail route, never the timed-media item read', async () => {
  const fake = api(path => path.startsWith('/v1/items/book/detail') ? book : new Error('404 not_found'));
  const identity = await readTitleIdentity(fake, 'book');
  assert.deepEqual(fake.paths, ['/v1/items/book/detail?related=none']);
  assert.deepEqual(identity, {id: 'book', libraryId: 'reading', kind: 'book', title: 'Atlas', posterUrl: '/art/book'});
});

test('an answer naming another title or library is refused', async () => {
  for (const wrong of [
    {...book, item: {...book.item, id: 'other'}},
    {...book, scope: {...book.scope, itemId: 'other'}},
    {...book, scope: {...book.scope, libraryId: 'elsewhere'}},
    {...book, item: {...book.item, libraryId: ''}},
    {scope: book.scope},
  ]) await assert.rejects(readTitleIdentity(api(() => wrong), 'book'), /Invalid title identity/);
});
