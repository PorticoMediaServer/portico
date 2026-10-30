import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeApi} from '../src/home.ts';
import {HomeService} from '../src/home-service.ts';

function entry(id: string) {
  return {id, kind: 'movie', title: `Title ${id}`, libraryId: 'movies', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}};
}
function row(id: string, ids: string[], changes: Record<string, unknown> = {}): any {
  return {
    id, title: id, kind: 'recent', artworkShape: 'poster', endpoint: '/v1/home/rows/' + id, libraryId: 'movies', viewerFence: 'fence', privacySensitivity: 'catalog', policyState: 'available', priority: 100,
    required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
    entries: ids.map(entry), total: 50, start: 0, limit: ids.length, hasMore: true, nextCursor: 'first.' + id, revision: {catalog: 3, viewer: 1}, ...changes,
  };
}
function document(rows: any[]): any {
  return {serverId: 'server', viewerFence: 'fence', rows, spotlight: [], layout: {revision: 2, rowOrder: rows.map(r => r.id), hiddenRowIds: []}, revision: {catalog: 3, viewer: 1}, generatedAt: '2026-09-16T12:00:00.000Z'};
}
const settle = () => new Promise(resolve => setImmediate(resolve));
function home(rowIds: string[], answer: (id: string) => unknown = id => row(id, [id + '-card'])) {
  const fetched: string[] = [];
  const api: HomeApi = {request: async <T>(path: string) => {
    if (path === '/v1/home') return document(rowIds.map(id => row(id, [], {deferred: true, limit: 12}))) as T;
    const id = path.split('/v1/home/rows/')[1]?.split('?')[0];
    if (!id) throw new Error(path);
    fetched.push(id);
    return answer(id) as T;
  }};
  return {service: new HomeService(api), fetched};
}
const cards = (service: HomeService, id: string) => service.getSnapshot().document!.rows.find(r => r.id === id)?.entries.map(e => e.id).join(',');

test('declared demand loads only visible deferred rows; the rest load once when they come into view', async () => {
  const {service, fetched} = home(['a', 'b', 'c']);
  service.setRowDemand(['a']);
  await service.load();await settle();
  assert.deepEqual(fetched, ['a']);
  assert.equal(cards(service, 'a'), 'a-card');
  assert.equal(cards(service, 'b'), '', 'a hidden shelf keeps its placeholder');
  service.setRowDemand(['a', 'b']);await settle();
  service.setRowDemand(['b', 'a']);await settle();
  assert.deepEqual(fetched, ['a', 'b'], 'overlapping demand fetches a row once per document');
  assert.equal(cards(service, 'b'), 'b-card');
  service.dispose();
});

test('without declared demand every deferred row loads, as before', async () => {
  const {service, fetched} = home(['a', 'b']);
  await service.load();await settle();
  assert.deepEqual(fetched.sort(), ['a', 'b']);
  service.dispose();
});

test('a refreshed document marks undemanded rows dirty instead of fetching them', async () => {
  const {service, fetched} = home(['a', 'b']);
  service.setRowDemand(['a']);
  await service.load();await settle();
  await service.revalidate();await settle();
  assert.deepEqual(fetched, ['a', 'a'], 'only the visible row is read again');
  service.setRowDemand(['b']);await settle();
  assert.deepEqual(fetched, ['a', 'a', 'b']);
  service.dispose();
});

test('demand after disposal or for a replaced document fetches nothing', async () => {
  const {service, fetched} = home(['a', 'b']);
  service.setRowDemand([]);
  await service.load();await settle();
  service.dispose();
  service.setRowDemand(['a', 'b']);await settle();
  assert.deepEqual(fetched, []);
});

test('the for_you placeholder loads when it or one of its rows is declared', async () => {
  const {service, fetched} = home(['for_you', 'b'], id => id === 'for_you'
    ? row('for_you', [], {children: [row('for_you_1', ['x'], {family: 'for_you'})]})
    : row(id, [id + '-card']));
  service.setRowDemand(['for_you']);
  await service.load();await settle();
  assert.deepEqual(fetched, ['for_you']);
  service.dispose();
});

test('a demanded row that is refused is removed; hidden rows are untouched', async () => {
  const {service, fetched} = home(['a', 'b'], id => { throw Object.assign(new Error('denied'), {status: 403, code: 'forbidden'}); });
  service.setRowDemand(['a']);
  await service.load();await settle();
  assert.deepEqual(fetched, ['a']);
  assert.equal(service.getSnapshot().document!.rows.some(r => r.id === 'a'), false);
  assert.equal(service.getSnapshot().document!.rows.some(r => r.id === 'b'), true);
  service.dispose();
});
