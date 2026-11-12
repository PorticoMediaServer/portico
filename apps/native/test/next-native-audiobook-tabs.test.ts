import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLibraryDestination} from '../src-next/screens/library/model.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';

const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
const settle = async () => {for (let n = 0; n < 6; n++) await new Promise<void>(resolve => setImmediate(resolve));};

// An audiobook library's Authors and Series tabs on phones and TVs, against the rebuilt server
// (web: web/test/audiobook-tabs-rebuilt.test.ts).
function api(seen: string[]): LibraryContentApi {
  return {request: async <T>(path: string, method = 'GET') => {
    seen.push(`${method} ${path}`);
    const url = new URL(path, 'https://server.test'), query = url.searchParams;
    if (url.pathname === '/v1/libraries/lib_a') return {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks', views: [{kind: 'audiobook'}, {kind: 'series'}],
      sorts: [{id: 'title', order: 'asc'}, {id: 'added', order: 'desc'}], filters: []} as T;
    if (url.pathname === '/v1/people' && query.get('library') === 'lib_a' && query.get('role') === 'author')
      return {items: [{id: 'per_1', name: 'Ada Adams', images: {}, titleCount: 2}, {id: 'per_2', name: 'Ben Brook', images: {}, titleCount: 1}], total: 2} as T;
    if (url.pathname === '/v1/items' && query.get('kind') === 'series')
      return {items: [{id: 'ser_1', kind: 'series', title: 'Tides', libraryId: 'lib_a', addedAt: '2026-11-12T00:00:00Z', images: {}, state: {}}], total: 1} as T;
    if (url.pathname === '/v1/facets') return {facet: 'letter', values: [{value: 'T', count: 1}]} as T;
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  }};
}

for (const [tab, want, view] of [['authors', [['per_1', 'author', 'Ada Adams'], ['per_2', 'author', 'Ben Brook']], 'author'], ['series', [['ser_1', 'book_series', 'Tides']], 'book_series']] as const) {
  test(`an audiobook library's ${tab === 'authors' ? 'Authors' : 'Series'} tab lists them from the rebuilt server, each opening its page`, async () => {
    const seen: string[] = [];
    const destination = new NativeLibraryDestination({api: api(seen), scope, library: {id: 'lib_a', name: 'Audiobooks', kind: 'audiobook'}, current: () => true});
    try {
      await destination.load(tab);
      const state = destination.getSnapshot();
      assert.equal(state.phase, 'ready', String((state.error as Error | undefined)?.message));
      state.collection!.ensureRange(0, want.length - 1); await settle();
      const shown = want.map((_, index) => state.collection!.itemAt(index));
      assert.deepEqual(shown.map(entry => [entry?.id, entry?.kind, entry?.title]), want);
      assert.equal(shown[0]?.navigation?.view, view);
      assert.ok(!seen.some(request => request.includes('/browse')), seen.join(' | '));
    } finally {destination.dispose();}
  });
}
