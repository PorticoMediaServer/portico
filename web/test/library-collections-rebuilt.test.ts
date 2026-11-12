import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseController} from '../src/screens/browse/controller.ts';
import type {LibraryContentApi} from '@core/library-content.ts';

const settle = async () => {for (let n = 0; n < 6; n++) await new Promise<void>(resolve => setImmediate(resolve));};
const collection = (n: number) => ({id: `col_${n}`, kind: 'collection', title: `Collection ${n}`, libraryId: 'lib_f', addedAt: '2026-11-12T00:00:00Z',
  images: {poster: `/v1/images/col_${n}`}, state: {}, counts: {children: 3, leaves: 3, unwatchedLeaves: 3}});

// The web Collections tab, beside the phone and TV one (apps/native/test/next-native-library-collections.test.ts).
test('a film library\'s Collections tab lists its collections from the rebuilt server', async () => {
  const seen: string[] = [];
  const api: LibraryContentApi = {request: async <T>(path: string, method = 'GET') => {
    seen.push(`${method} ${path}`);
    const url = new URL(path, 'https://server.test'), query = url.searchParams;
    if (url.pathname === '/v1/libraries/lib_f') return {id: 'lib_f', name: 'Films', kind: 'movies', views: [{kind: 'movie'}, {kind: 'collection'}],
      sorts: [{id: 'title', order: 'asc'}, {id: 'added', order: 'desc'}], filters: []} as T;
    if (url.pathname === '/v1/items' && query.get('library') === 'lib_f' && query.get('kind') === 'collection')
      return (query.get('total') === 'true' ? {items: [collection(1)], total: 2} : {items: [collection(1), collection(2)]}) as T;
    if (url.pathname === '/v1/facets') return {facet: 'letter', values: [{value: 'C', count: 2}]} as T;
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  }};
  const controller = new BrowseController(api, {libraryId: 'lib_f', pivot: 'collections'}, () => true);
  await controller.load();
  assert.equal(controller.getSnapshot().phase, 'ready', String((controller.getSnapshot().error as Error | undefined)?.message));
  controller.collection.ensureRange(0, 1); await settle();
  const first = controller.collection.itemAt(0)?.entry;
  assert.deepEqual([first?.id, first?.kind, controller.collection.itemAt(1)?.entry.id], ['col_1', 'collection', 'col_2']);
  assert.deepEqual(first?.navigation, {view: 'collection', entityId: 'col_1'}, 'a collection card opens its page');
  assert.ok(seen.some(request => request.startsWith('GET /v1/items?library=lib_f&kind=collection&sort=title&order=asc')), seen.join(' | '));
});
