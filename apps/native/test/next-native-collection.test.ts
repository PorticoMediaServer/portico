// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {NativeCollectionController} from '../src-next/screens/collections/controller.ts';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed.ts';

const themed = {useTheme: () => ({isTV: true, colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', raised: '#111', line: '#222', accent: '#3af', accentStrong: '#4bf'}, type: {}})};
const {NativeCollectionScreen, CollectionCard} = await screenModule(new URL('../src-next/screens/collections/CollectionScreen.tsx', import.meta.url), {
  react: {...React, default: React},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
  '../../kit/cards/primitives': {CardFrame: 'CardFrame'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'},
  '../saved/presentation': {savedDuration: (value: number) => String(value)},
  'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
  '../../../../../packages/i18n/src': {defaultI18n},
  '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'Ambient', Artwork: 'Artwork'}, '../../kit/cards': {PosterCard: 'Poster'},
  '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'}, '../../kit/Icon': {Icon: 'Icon'},
  '../../kit/overlays': {Menu: 'Menu'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/layout': {WindowedGrid: 'WindowedGrid', WindowedRow: 'WindowedRow'},
  '../../kit/browse/presentation': {browseType: (_theme: unknown, size: number) => ({fontSize: size})}, '../../kit/theme': themed,
});
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

test('the collection controller reads the real collection view and its own sorts', async () => {
  const paths: string[] = [];
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)); paths.push(url.search);
    return new Response(JSON.stringify(projection(url.searchParams.get('sort') ?? 'custom')), {status: 200});
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
    assert.equal(paths.at(-1)?.includes('cursor='), false, 'pages are read by index, never by cursor');
    assert.equal(paths.at(-1)?.includes('view=collection'), true);
    assert.equal(paths.at(-1)?.includes('entityId=c1'), true);
  } finally {controller.dispose();}
});

test('TV collection draws the name, film facts, Play all, Shuffle and the real member row', async () => {
  const calls: string[] = [], back: (() => boolean)[] = [];
  const state = {phase: 'ready' as const, title: 'Toy Story Collection', collection: window(), complete: members, first: members[0],
    totalCount: 2, sorts: [{id: 'custom', labelKey: 'sort.custom', directions: ['asc'] as const}, {id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'] as const}], sort: 'custom'};
  const runtime = {ambient: () => undefined, artwork: () => undefined, registerTVPageBack: (handler: () => boolean) => {back.push(handler); return () => {};}, error: () => ({})} as any;
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeCollectionScreen({state, runtime,
    onPlay: () => calls.push('play'), onShuffle: () => calls.push('shuffle'), onSort: () => calls.push('sort'),
    onOpen: () => calls.push('open'), onBack: () => calls.push('back'), onRetry: () => calls.push('retry')});});
  await host.mount();
  try {
    const nodes = descendants(tree);
    assert.ok(nodes.some(node => node.type === 'Text' && node.props.children === 'Toy Story Collection'));
    assert.ok(nodes.some(node => node.type === 'Text' && node.props.children === defaultI18n.t('next.browse.filmCount', {count: 2})));
    assert.ok(nodes.some(node => node.type === 'Text' && node.props.children === '1995 – 1999'));
    const play = nodes.find(node => node.props.testID === 'collection-play-all');
    assert.equal(play.props.hasTVPreferredFocus, true); play.props.onPress(); assert.ok(calls.includes('play'));
    nodes.find(node => node.props.testID === 'collection-shuffle').props.onPress(); assert.ok(calls.includes('shuffle'));
    const row = nodes.find(node => node.type === 'WindowedRow');
    assert.ok(row, 'TV draws the members as one windowed row');
    assert.equal(row.props.collection, state.collection);
    assert.equal(nodes.some(node => node.type === 'WindowedGrid'), false);
    const card = row.props.renderItem(members[0], {index: 0, id: 'movie:m1', onFocus: () => {}, onBlur: () => {}});
    assert.equal(card.type, CollectionCard);
    assert.equal(card.props.entry, members[0]);
    assert.equal(nodes.find(node => node.props.testID === 'collection-sort') !== undefined, true);
    assert.equal(back.length, 1); assert.equal(back[0](), true); assert.ok(calls.includes('back'));
  } finally {await host.unmount();}
});

test('a long collection keeps a bounded window and reads evicted pages again by index', async () => {
  const total = 1000, starts: number[] = [];
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)), start = Number(url.searchParams.get('start') ?? 0), limit = Number(url.searchParams.get('limit'));
    starts.push(start);
    const body = projection();
    const entries = Array.from({length: Math.max(0, Math.min(limit, total - start))}, (_, n) => ({id: `m${start + n}`, kind: 'movie', title: `Film ${start + n}`, year: 2000}));
    return new Response(JSON.stringify({...body, sections: [{...body.sections[0], totalCount: total, entries, start}]}), {status: 200});
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
