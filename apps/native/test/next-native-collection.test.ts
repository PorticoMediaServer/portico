// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeCollectionController} from '../src-next/screens/collections/controller.ts';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed.ts';

const settle = async () => {for (let n = 0; n < 20; n++) await new Promise(resolve => setTimeout(resolve, 0));};
const members = [{id: 'm1', kind: 'movie', title: 'Toy Story', year: 1995}, {id: 'm2', kind: 'movie', title: 'Toy Story 2', year: 1999, watched: true}];
const window = () => {
  const collection = createWindowedCollection<any>({fetchPage: async () => ({items: members, total: 2}), pageSize: 40, maxResidentPages: 4, keyOf: entry => entry.id});
  collection.ensureRange(0, 0);
  return collection;
};
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];
const projection = (sort = 'custom') => ({
  scope: {serverId: 'server', libraryId: 'library', libraryKind: 'movie', view: 'collection', entityId: 'c1', viewerFence: 'fence'},
  revision: {catalog: 1, viewer: 2}, heading: {key: 'entity.title', fallback: 'Toy Story Collection'}, navigation: [],
  query: {sort, direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'},
  sorts: [{id: 'custom', labelKey: 'sort.custom', directions: ['asc']}, {id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc']}], filters: [],
  sections: [{id: 'items', type: 'grid', heading: {key: 'entity.title', fallback: 'Toy Story Collection'}, totalCount: 2, nextCursor: '',
    entries: [{id: 'm1', kind: 'movie', title: 'Toy Story', year: 1995}, {id: 'm2', kind: 'movie', title: 'Toy Story 2', year: 1999, watched: true}]}],
});

// The rebuilt server's answers for a collection: its title, its library, and a window of its titles.
const rebuilt = (url: URL, titles: (at: number, limit: number) => readonly unknown[], total: number) =>
  url.pathname === '/v1/items' ? {items: titles(Number(url.searchParams.get('at') ?? 0), Number(url.searchParams.get('limit'))), total}
  : url.pathname === '/v1/libraries/library' ? {id: 'library', name: 'Films', kind: 'movies'}
  : {id: 'c1', kind: 'collection', title: 'Toy Story Collection', libraryId: 'library'};

test('the collection controller reads the real collection view and its own sorts', async () => {
  const paths: string[] = [];
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)); if (url.pathname === '/v1/items') paths.push(url.search);
    return new Response(JSON.stringify(rebuilt(url, () => members, 2)), {status: 200});
  });
  const controller = new NativeCollectionController({api, scope: {serverId: 'server', viewerId: 'viewer'}, libraryId: 'library', collectionId: 'c1', title: 'Fallback', current: () => true});
  try {
    assert.equal(paths.length, 0);
    await controller.open();
    await settle();
    const state = controller.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.equal(state.title, 'Toy Story Collection');
    assert.deepEqual([0, 1].map(index => state.collection.itemAt(index)?.title), ['Toy Story', 'Toy Story 2']);
    assert.deepEqual(state.complete?.map(entry => entry.title), ['Toy Story', 'Toy Story 2'], 'a collection that fits in memory is complete');
    assert.equal(state.first?.id, 'm1');
    assert.deepEqual(controller.resident().positions, [0, 1]);
    assert.equal(state.totalCount, 2);
    assert.equal(state.sort, 'custom');
    assert.deepEqual(state.sorts.map(sort => sort.id), ['custom', 'title']);
    await controller.sort('title', 'desc');
    await settle();
    assert.equal(paths.at(-1)?.includes('sort=title'), true);
    assert.equal(paths.at(-1)?.includes('order=desc'), true);
    assert.equal(paths.at(-1)?.includes('cursor='), false, 'pages are read by index, never by cursor');
    assert.equal(paths.at(-1)?.includes('collection=c1'), true);
  } finally {controller.dispose();}
});

test('a long collection keeps a bounded window and reads evicted pages again by index', async () => {
  const total = 1000, starts: number[] = [];
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input));
    if (url.pathname === '/v1/items') starts.push(Number(url.searchParams.get('at') ?? 0));
    const titles = (start: number, limit: number) => Array.from({length: Math.max(0, Math.min(limit, total - start))}, (_, n) => ({id: `m${start + n}`, kind: 'movie', title: `Film ${start + n}`, year: 2000}));
    return new Response(JSON.stringify(rebuilt(url, titles, total)), {status: 200});
  });
  const controller = new NativeCollectionController({api, scope: {serverId: 'server', viewerId: 'viewer'}, libraryId: 'library', collectionId: 'c1', title: 'Fallback', current: () => true});
  try {
    await controller.open(); await settle();
    const {collection} = controller.getSnapshot();
    assert.equal(controller.getSnapshot().totalCount, 1000);
    assert.equal(controller.getSnapshot().complete, undefined, 'no year span from a partial window');
    collection.pin(3);
    for (let first = 40; first <= 600; first += 40) {collection.ensureRange(first, first + 20); await settle();}
    assert.ok(collection.resident().items <= 5 * 40, 'members held stay bounded however far it scrolls');
    assert.equal(collection.itemAt(3)?.id, 'm3', 'the focused card is never evicted');
    assert.equal(collection.itemAt(45), undefined, 'an unfocused page left memory');
    const before = starts.length;
    collection.ensureRange(40, 60); await settle();
    assert.equal(collection.itemAt(45)?.id, 'm45');
    assert.ok(starts.slice(before).includes(40), 'back-scroll reads the page again at its own start');
    const resident = controller.resident();
    assert.ok(resident.entries.every((entry, index) => entry.id === `m${resident.positions[index]}`), 'selection sees absolute positions');
  } finally {controller.dispose();}
});
