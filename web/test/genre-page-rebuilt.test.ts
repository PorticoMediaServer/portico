import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseController} from '../src/screens/browse/controller.ts';
import {searchGenreBrowseRequest} from '@core/search-genres.ts';
import type {LibraryContentApi} from '@core/library-content.ts';

const settle = async () => {for (let n = 0; n < 6; n++) await new Promise<void>(resolve => setImmediate(resolve));};

// The web genre page (a Search genre tile's Browse), beside the phone and TV one (apps/native/test/next-search-genre-browse.test.ts).
test('a genre page lists the genre\'s films and shows across every library from the rebuilt server', async () => {
  const paths: string[] = [];
  const api: LibraryContentApi = {request: async <T>(path: string) => {
    paths.push(path);
    const url = new URL(path, 'https://server.test');
    if (url.pathname === '/v1/facets') return {facet: 'letter', values: [{value: 'H', count: 1}, {value: 'J', count: 1}]} as T;
    if (url.pathname === '/v1/items') return {items: [{id: 'm1', kind: 'movie', title: 'Heat', libraryId: 'lib_f', images: {}, state: {}, addedAt: '2026-11-12T00:00:00Z'},
      {id: 's1', kind: 'show', title: 'Justified', libraryId: 'lib_s', images: {}, state: {}, addedAt: '2026-11-12T00:00:00Z'}], total: 2} as T;
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  }};
  const target = searchGenreBrowseRequest('tag_crime');
  const controller = new BrowseController(api, {libraryId: '*', pivot: target.pivot, predicates: [target.query!], sort: target.sort![0]}, () => true);
  await controller.load();
  assert.equal(controller.getSnapshot().phase, 'ready', String((controller.getSnapshot().error as Error | undefined)?.message));
  controller.collection.ensureRange(0, 1); await settle();
  assert.deepEqual([controller.collection.itemAt(0)?.entry.id, controller.collection.itemAt(1)?.entry.kind], ['m1', 'show']);
  const list = new URL(paths.find(path => path.startsWith('/v1/items?'))!, 'https://server.test').searchParams;
  assert.deepEqual([list.get('kind'), list.get('tag')], ['movie,show', 'tag_crime']);
  assert.ok(!paths.some(path => path.includes('browse')), paths.join(' | '));
});
