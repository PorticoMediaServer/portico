import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseController} from '../src/screens/browse/controller.ts';
import type {LibraryContentApi} from '@core/library-content.ts';

const settle = async () => {for (let n = 0; n < 6; n++) await new Promise<void>(resolve => setImmediate(resolve));};

// The web Authors and Series tabs, beside the phone and TV ones (apps/native/test/next-native-audiobook-tabs.test.ts).
const api: LibraryContentApi = {request: async <T>(path: string) => {
  const url = new URL(path, 'https://server.test'), query = url.searchParams;
  if (url.pathname === '/v1/libraries/lib_a') return {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks', views: [{kind: 'audiobook'}, {kind: 'series'}],
    sorts: [{id: 'title', order: 'asc'}, {id: 'added', order: 'desc'}], filters: []} as T;
  if (url.pathname === '/v1/people' && query.get('role') === 'author') return {items: [{id: 'per_1', name: 'Ada Adams', images: {}, titleCount: 2}], total: 1} as T;
  if (url.pathname === '/v1/items' && query.get('kind') === 'series') return {items: [{id: 'ser_1', kind: 'series', title: 'Tides', libraryId: 'lib_a', addedAt: '2026-11-12T00:00:00Z', images: {}, state: {}}], total: 1} as T;
  if (url.pathname === '/v1/facets') return {facet: 'letter', values: [{value: 'T', count: 1}]} as T;
  throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
}};

for (const [pivot, want] of [['authors', ['per_1', 'author', 'author']], ['series', ['ser_1', 'book_series', 'book_series']]] as const) {
  test(`an audiobook library's ${pivot} tab lists them from the rebuilt server, each opening its page`, async () => {
    const controller = new BrowseController(api, {libraryId: 'lib_a', pivot}, () => true);
    await controller.load();
    assert.equal(controller.getSnapshot().phase, 'ready', String((controller.getSnapshot().error as Error | undefined)?.message));
    controller.collection.ensureRange(0, 0); await settle();
    const first = controller.collection.itemAt(0)?.entry;
    assert.deepEqual([first?.id, first?.kind, first?.navigation?.view], want);
  });
}
