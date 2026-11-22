import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseController} from '../src/screens/browse/controller.ts';
import type {LibraryContentApi} from '@core/library-content.ts';

const settle = async () => {for (let n = 0; n < 8; n++) await new Promise<void>(resolve => setImmediate(resolve));};
const film = (id: string) => ({id, kind: 'movie', title: id, libraryId: 'lib_f', addedAt: '2026-11-12T00:00:00Z', images: {}, state: {}, available: true});

// Web's Select all: with the screen's selection ports the whole list is the server's exact summary, read when someone
// selects; ports given after the list opened still apply, and changing them drops a summary already held.
test("Select all reads the server's exact summary once the screen gives its ports, and drops it when they change", async () => {
  const paths: string[] = [];
  const api: LibraryContentApi = {request: async <T>(path: string) => {
    paths.push(path);
    const url = new URL(path, 'https://server.test');
    if (url.pathname === '/v1/libraries/lib_f') return {id: 'lib_f', name: 'Films', kind: 'movies', views: [{kind: 'movie'}], sorts: [{id: 'title', order: 'asc'}], filters: []} as T;
    if (url.pathname === '/v1/items/selection') return {total: 2, kinds: [{kind: 'movie', count: 2, available: 1}]} as T;
    if (url.pathname === '/v1/items') return {items: [film('a'), film('b')], total: 2, revision: 'r1'} as T;
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  }};
  const controller = new BrowseController(api, {libraryId: 'lib_f', pivot: 'movies'}, () => true);
  await controller.load();
  controller.collection.ensureRange(0, 1); await settle();
  assert.equal(controller.getSnapshot().phase, 'ready');

  controller.setSelectionPorts({play: true, myList: true});
  assert.equal(controller.wholeSelection(), undefined, 'no estimate once the screen gives its ports');
  assert.ok(!paths.some(path => path.startsWith('/v1/items/selection')), 'nothing is read until someone selects');

  controller.requestWholeSelection(); await settle();
  const whole = controller.wholeSelection();
  assert.equal(paths.filter(path => path.startsWith('/v1/items/selection')).length, 1);
  assert.equal(whole?.total, 2);
  assert.equal(whole?.types[0]?.capabilities.play, 1, 'only the available title can play');

  controller.setSelectionPorts({play: true});
  assert.equal(controller.wholeSelection(), undefined, 'a summary for other actions is dropped');
});
