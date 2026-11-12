import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLibraryDestination} from '../src-next/screens/library/model.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';

const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
const settle = async () => {for (let n = 0; n < 6; n++) await new Promise<void>(resolve => setImmediate(resolve));};
const collection = (n: number) => ({id: `col_${n}`, kind: 'collection', title: `Collection ${n}`, libraryId: 'lib_f', addedAt: '2026-11-12T00:00:00Z',
  images: {poster: `/v1/images/col_${n}`}, state: {}, counts: {children: 3, leaves: 3, unwatchedLeaves: 3}});

// A film library's Collections tab, against the rebuilt server: the library offers collections as a
// list of its own, and the tab lists them; each opens the collection's page.
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
  const destination = new NativeLibraryDestination({api, scope, library: {id: 'lib_f', name: 'Films', kind: 'movie'}, current: () => true});
  try {
    await destination.load('collections');
    const state = destination.getSnapshot();
    assert.equal(state.phase, 'ready', String((state.error as Error | undefined)?.message));
    state.collection!.ensureRange(0, 1); await settle();
    const first = state.collection!.itemAt(0), second = state.collection!.itemAt(1);
    assert.deepEqual([first?.id, first?.kind, second?.id], ['col_1', 'collection', 'col_2']);
    assert.deepEqual(first?.navigation, {view: 'collection', entityId: 'col_1'}, 'a collection card opens its page');
    assert.ok(seen.some(request => request.startsWith('GET /v1/items?library=lib_f&kind=collection&sort=title&order=asc')), seen.join(' | '));
    assert.ok(!seen.some(request => request.includes('/browse')), 'no old-server browse read is sent');
  } finally {destination.dispose();}
});
