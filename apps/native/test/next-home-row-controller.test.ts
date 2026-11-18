import assert from 'node:assert/strict';
import test from 'node:test';
import {setImmediate} from 'node:timers/promises';
import {NativeHomeRowController} from '../src-next/screens/home/row-controller';
import {queueSource} from '../../../packages/client-core/src/playback-v1/queue.ts';
import type {BrowseNode} from '../../../packages/client-core/src/browse.ts';

const filters: readonly BrowseNode[] = [
  {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['drama', 'action']},
  {variant: 'predicate', field: 'year', operator: 'between', value: [2000, 2009]},
  {variant: 'predicate', field: 'playState', operator: 'equals', value: 'unplayed'},
  {variant: 'predicate', field: 'favorite', operator: 'equals', value: true},
  {variant: 'predicate', field: 'watchlisted', operator: 'equals', value: false},
];
const settle = async () => {for (let i = 0; i < 5; i++) await setImmediate();};

function fixture(rowId = 'recently_released') {
  let current = true;
  const calls: URL[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    const url = new URL(path, 'http://test');
    calls.push(url);
    if (url.pathname === `/v1/home/rows/${rowId}`) return {id: rowId, title: 'A row', shape: 'poster',
      items: [{id: 'episode-real', libraryId: 'shows', kind: 'episode', title: 'Actual episode', state: {}}],
      ...(url.searchParams.has('total') ? {total: 87} : {}), nextCursor: 'next'} as T;
    if (url.pathname === '/v1/facets') return {facet: url.searchParams.get('facet'),
      values: url.searchParams.get('facet') === 'letter' ? [{value: 'A', count: 60}, {value: 'B', count: 27}]
        : [{value: 'drama', label: 'Drama', count: 2}]} as T;
    throw new Error(`unexpected_fixture_request: ${path}`);
  }};
  const controller = new NativeHomeRowController({api, rowId, current: () => current, kinds: [{id: 'tv', libraryIds: ['shows', 'anime']}]});
  return {controller, calls, retire: () => {current = false;}};
}

test('the selected row, library, filters and order reach the pages, scoped choices and queue', async () => {
  const {controller, calls} = fixture();
  try {
    await controller.load();
    controller.selectKind('tv');
    controller.setFilters(filters);
    controller.setSort({field: 'title', direction: 'asc'});
    controller.collection.ensureRange(0, 0);
    await settle();
    assert.equal(controller.collection.itemAt(0)?.membershipId, 'episode-real');
    assert.equal(controller.collection.itemAt(0)?.entry.id, 'episode-real');
    const applied = {library: 'any:shows,anime', genre: 'any:drama,action', year: '2000..2009', watched: false, favorite: true, inList: false};
    const facet = await controller.facets('genre', 'dram');
    assert.deepEqual(facet.values, [{value: 'drama', label: 'Drama', count: 2}]);
    const pageRequest = calls.find(url => url.searchParams.get('sort') === 'title')!;
    const facetRequest = calls.find(url => url.pathname === '/v1/facets')!;
    for (const [key, value] of Object.entries(applied)) {
      assert.equal(pageRequest.searchParams.get(key), String(value));
      assert.equal(facetRequest.searchParams.get(key), String(value));
    }
    assert.equal(facetRequest.searchParams.get('homeRow'), 'recently_released');
    assert.deepEqual(JSON.parse(JSON.stringify(queueSource({homeRow: controller.queueSource()!}))),
      {homeRow: {id: 'recently_released', ...applied, sort: 'title', order: 'asc'}});
    assert.equal((await controller.previewCounts([]))[0].total, 87);
    assert.deepEqual(controller.getSnapshot().filters, filters);
  } finally {controller.dispose();}
});

test('title order learns letter positions from the same row and scope', async () => {
  const {controller, calls} = fixture();
  try {
    await controller.load();
    controller.selectKind('tv');
    controller.setSort({field: 'title', direction: 'asc'});
    controller.collection.ensureRange(0, 0);
    await settle();
    assert.deepEqual(controller.collection.getSnapshot().positionIndex, {A: 0, B: 60});
    const facet = calls.find(url => url.searchParams.get('facet') === 'letter')!;
    assert.equal(facet.searchParams.get('homeRow'), 'recently_released');
    assert.equal(facet.searchParams.get('library'), 'any:shows,anime');
  } finally {controller.dispose();}
});

test('Continue keeps its server order and real episode cards without requesting facets', async () => {
  const {controller, calls, retire} = fixture('continue');
  try {
    await controller.load();
    assert.equal(controller.setSort({field: 'title', direction: 'asc'}), false);
    controller.collection.ensureRange(0, 0);
    await settle();
    assert.equal(controller.getSnapshot().counts[0].total, 87);
    assert.equal(controller.collection.itemAt(0)?.entry.id, 'episode-real');
    assert.equal(controller.queueSource()?.id, 'continue');
    assert.ok(calls.every(url => !url.searchParams.has('sort') && url.pathname === '/v1/home/rows/continue'));
    retire();
    assert.equal(controller.queueSource(), undefined);
  } finally {controller.dispose();}
});
