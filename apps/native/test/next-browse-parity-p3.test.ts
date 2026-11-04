// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {nativeWatchMenuGroups} from '../src-next/screens/watch/menu-model.ts';
import {titleMenuItems} from '../src-next/kit/overlays/title-menu.ts';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed.ts';
import * as kitModel from '../../../packages/client-core/src/presentation/kit-model/index.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const t = defaultI18n.t;
const text = (tree: any) => nodes(tree).filter(node => node.type === 'Text').map(node => node.props.children).filter(child => typeof child === 'string');

// Phone person credits header (phone-detail.html 7: "Credits 6 titles on this server")

test('the phone person page counts its credits as titles on this server', async () => {
  const {PersonScreen} = await screenModule(new URL('../src-next/screens/people/PersonScreen.tsx', import.meta.url), {
    '../share/share-actions': {shareLinkActions: () => []}, react: {...React, default: React},
    './credit-refocus': await screenModule(new URL('../src-next/screens/people/credit-refocus.ts', import.meta.url), {react: {...React, default: React}}),
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', VirtualizedList: 'VirtualizedList'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', PersonCircle: 'PersonCircle', AmbientArt: 'AmbientArt', Artwork: 'Artwork'},
    '../../kit/cards': {PosterCard: 'PosterCard'}, '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/browse/presentation': {browseType: (_theme: unknown, size: number) => ({fontSize: size})},
    '../../kit/theme': {useTheme: () => ({isTV: false, platform: 'phone', colors: {text: '#fff', textSecondary: '#ccc', textTertiary: '#999', raised: '#111'}, type: {}})},
    '../../kit/overlays': {Menu: 'Menu'}, '../../tv/layout/TVShelf': {TVShelf: 'TVShelf'}, '../../kit/layout/TVRow': {TVRow: 'TVRow'},
    '../../kit/layout/page-inset': {usePageWidth: () => 390},
  });
  const page = {person: {id: 'p', name: 'Steven Spielberg', roles: [], knownFor: []}, pageInfo: {total: 6}} as any;
  const collection = {pageSize: 60, pageOf: () => 0, subscribe: () => () => {}, subscribePage: () => () => {},
    getSnapshot: () => ({generation: 1, total: 6, status: 'ready', refreshing: false, totalIsLowerBound: false}),
    getPageVersion: () => 'v1', itemAt: () => undefined, ensureRange: () => {}, retry: () => {}} as any;
  const runtime = {artwork: () => undefined, ambient: () => undefined, text: {locale: 'en-US'}, error: () => ({})} as any;
  let tree: any;
  const host = createFabricHookHost(() => {tree = PersonScreen({focusScope: 'person:p', page, role: 'all', collection, runtime,
    actions: {menuState: {subscribe: () => () => {}, getSnapshot: () => 0}, isMenuOpen: () => false} as any, onRole() {}, onBack() {}});});
  await host.mount();
  try {
    const list = nodes(tree).find(node => node.type === 'VirtualizedList');
    const words = text(list.props.ListHeaderComponent);
    assert.ok(words.includes('6 titles on this server'));
    assert.equal(words.includes('6 titles'), false);
  } finally {await host.unmount();}
});

// 3, 4. TV collection: More, lede, summary

const themed = (isTV: boolean) => ({useTheme: () => ({isTV, platform: isTV ? 'tv' : 'phone', colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', raised: '#111', line: '#222', accent: '#3af', accentStrong: '#4bf'}, type: {}})});
async function collectionScreen(isTV: boolean) {
  return screenModule(new URL('../src-next/screens/collections/CollectionScreen.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'},
    '../../../../../packages/client-core/src/presentation/kit-model': kitModel, '../saved/presentation': {savedDuration: (value: number) => String(value)},
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'Ambient', Artwork: 'Artwork'}, '../../kit/cards': {PosterCard: 'Poster'},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/overlays': {Menu: 'Menu'}, '../../kit/states': {StateView: 'StateView', Quiet: 'Quiet'}, '../../kit/layout': {WindowedGrid: 'WindowedGrid', WindowedRow: 'WindowedRow'},
    '../../kit/browse/presentation': {browseType: (_theme: unknown, size: number) => ({fontSize: size})}, '../../kit/theme': themed(isTV),
    '../../kit/layout/page-inset': {usePageWidth: () => 390},
  });
}
const members = [{id: 'm1', kind: 'movie', title: 'Toy Story', year: 1995, watched: true}, {id: 'm2', kind: 'movie', title: 'Toy Story 2', year: 1999, watched: true},
  {id: 'm3', kind: 'movie', title: 'Toy Story 3', year: 2010}, {id: 'm4', kind: 'movie', title: 'Toy Story 5', year: 2026}];
const windowOf = () => {
  const collection = createWindowedCollection<any>({fetchPage: async () => ({items: members, total: 4}), pageSize: 40, maxResidentPages: 4, keyOf: entry => entry.id});
  collection.ensureRange(0, 0);
  return collection;
};
const sorts = [{id: 'custom', labelKey: 'sort.custom', directions: ['asc'] as const}, {id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'] as const}];
async function mountCollection(isTV: boolean, state: any, extra: any = {}) {
  const {NativeCollectionScreen} = await collectionScreen(isTV);
  const runtime = {ambient: () => undefined, artwork: () => undefined, registerTVPageBack: () => () => {}, error: () => ({})} as any;
  let tree: any;
  const calls: string[] = [];
  const host = createFabricHookHost(() => {tree = NativeCollectionScreen({state, runtime, onPlay: () => calls.push('play'), onShuffle: () => calls.push('shuffle'), onSort: () => {},
    onOpen: () => {}, onBack: () => {}, onRetry: () => {}, ...extra});});
  await host.mount();
  return {host, calls, get tree() {return tree;}};
}
const base = () => ({phase: 'ready' as const, title: 'Toy Story', collection: windowOf(), complete: members, first: members[0], totalCount: 4, sorts, sort: 'custom'});

test('TV collection draws More beside Play all and Shuffle, focusable, with the page menu', async () => {
  const started: number[] = [];
  const f = await mountCollection(true, base(), {selection: {subscribe: () => () => {}, getSnapshot: () => 0, start: () => started.push(1), item: () => undefined, render: () => null}});
  try {
    const buttons = nodes(f.tree).filter(node => node.type === 'Button').map(node => node.props.testID);
    assert.deepEqual(buttons, ['collection-play-all', 'collection-shuffle', 'collection-menu'], 'tv-detail collection frame: Play all, Shuffle and More in one row');
    const more = nodes(f.tree).find(node => node.props.testID === 'collection-menu');
    assert.equal(more.props.focusId, 'collection-menu');
    assert.equal(more.props.size, 'hero'); assert.equal(more.props.iconOnly, true);
    assert.equal(more.props.accessibilityLabel, t('card.more', {title: 'Toy Story'}));
    assert.equal(nodes(f.tree).find(node => node.props.testID === 'collection-page-menu').props.open, false);
    more.props.onPress(); await f.host.rerender();
    const menu = nodes(f.tree).find(node => node.props.testID === 'collection-page-menu');
    assert.equal(menu.props.open, true);
    assert.deepEqual(menu.props.items.map((item: any) => item.id), ['play', 'shuffle', 'select']);
    menu.props.items[1].onSelect(); menu.props.items[2].onSelect();
    assert.deepEqual([f.calls, started], [['shuffle'], [1]]);
  } finally {await f.host.unmount();}
});

test('TV collection hero: the overview is the lede when there is one, else one composed from the titles; no studio it does not carry', async () => {
  const overview = 'Four films on your server, from 1995 to Toy Story 5.';
  const f = await mountCollection(true, {...base(), projection: {entity: {id: 'c1', kind: 'collection', title: 'Toy Story', overview, studio: 'Pixar'}}});
  try {
    const lede = nodes(f.tree).find(node => node.props.testID === 'collection-lede');
    assert.equal(lede.props.children, overview);
    assert.equal(text(f.tree).includes('Pixar'), false, 'no studio field exists on the read, so none is drawn');
  } finally {await f.host.unmount();}
  const bare = await mountCollection(true, base());
  try {
    const composed = nodes(bare.tree).find(node => node.props.testID === 'collection-lede');
    assert.match(composed?.props.children ?? '', /^Four films on your server, from 1995 to /, 'no overview: the lede is composed from the titles (f10)');
  } finally {await bare.host.unmount();}
});

test('TV collection toolbar says how many are watched, and never claims collection order the catalogue view cannot give', async () => {
  const f = await mountCollection(true, base());
  try {
    assert.equal(nodes(f.tree).find(node => node.props.testID === 'collection-watched-summary').props.children, '2 of 4 watched');
    assert.equal(nodes(f.tree).some(node => node.props.testID === 'collection-order-summary'), false, 'the catalogue collection view offers title, added and year sorts only');
    assert.equal(text(f.tree).includes('In collection order'), false);
  } finally {await f.host.unmount();}
  const partial = await mountCollection(true, {...base(), sort: 'title', complete: undefined, projection: {entity: {id: 'c1', kind: 'collection', title: 'Toy Story', watchedCount: 3}}});
  try {
    assert.equal(nodes(partial.tree).find(node => node.props.testID === 'collection-watched-summary').props.children, '3 of 4 watched', 'a partial window uses the server\'s tally');
  } finally {await partial.host.unmount();}
  const none = await mountCollection(true, {...base(), complete: undefined});
  try {assert.equal(nodes(none.tree).some(node => node.props.testID === 'collection-watched-summary'), false, 'no tally, no summary');} finally {await none.host.unmount();}
});

test('the phone collection keeps its own More and none of the TV summary', async () => {
  const f = await mountCollection(false, base());
  try {
    const header = nodes(f.tree).find(node => node.type === 'WindowedGrid').props.header;
    assert.ok(nodes(header).some(node => node.props.testID === 'collection-menu'));
    assert.equal(nodes(header).some(node => node.props.testID === 'collection-watched-summary' || node.props.testID === 'collection-lede'), false);
  } finally {await f.host.unmount();}
});

// 5. TV hold-OK menu order (tv-more.html 2)

const row = (id: string): any => ({id, label: id, icon: 'i-film', onSelect() {}});
const ids = (items: readonly any[]) => items.map(item => item.id).filter(id => !id.startsWith('separator') && id !== 'surface-separator' && !id.startsWith('group:'));
const entry = {id: 'film', kind: 'movie', title: 'Actual film', libraryId: 'library'} as any;
const detail = (grants: string[]): any => ({personal: {watchlisted: false, favorite: false, watched: false, rating: null, progressSeconds: 0},
  actions: grants.map(id => ({id, enabled: true, ...(id === 'play' ? {playback: {itemId: 'film', startSeconds: 0}} : {})}))});
const noop = async (): Promise<void> => {};
const menu: any = {play: noop, enqueue: noop, addToPlaylist() {}, addToCollection() {}, rate() {}, details() {}, mutate: noop, dispose() {}, subscribe: () => () => {}, current: (): undefined => undefined};

test('the TV menu follows tv-more.html 2 and 3 and tv-detail.html 7; the phone menu keeps its own order', () => {
  const groups = nativeWatchMenuGroups({entry, detail: detail(['play', 'watchlist', 'watched', 'add_to_collection']), menu});
  const tv = ids(titleMenuItems(groups, {tv: true, run() {}, surface: [row('select'), row('go-to-artist'), row('disc-titles')], afterDetails: [row('different-version'), row('file-info')]}));
  assert.deepEqual(tv.slice(tv.indexOf('addToCollection')), ['addToCollection', 'select', 'go-to-artist', 'disc-titles', 'details', 'different-version', 'file-info']);
  const phone = ids(titleMenuItems(groups, {tv: false, run() {}, surface: [row('different-version'), row('select'), row('file-info')]}));
  assert.deepEqual(phone.slice(phone.indexOf('different-version')), ['different-version', 'select', 'file-info', 'details'], 'phone-detail frame 9: version, then File info');
  const song = ids(titleMenuItems(nativeWatchMenuGroups({entry, detail: detail(['play']), menu: {...menu, details: undefined}}), {tv: true, run() {}, surface: [row('select'), row('go-to-artist')], afterDetails: [row('file-info')]}));
  assert.deepEqual(song.slice(-3), ['select', 'go-to-artist', 'file-info'], 'with no Details row, File info follows Select and Go to artist (tv-more.html 3)');
  const episode = titleMenuItems(nativeWatchMenuGroups({entry, detail: detail(['play']), menu: {...menu, details: undefined}}), {tv: true, run() {}, related: [row('go-to-season')], afterDetails: [row('different-version'), row('file-info')]});
  assert.deepEqual(ids(episode).slice(-3), ['go-to-season', 'different-version', 'file-info'], 'tv-detail.html 7: Go to season, then Play a different version and File info');
  assert.ok(episode.findIndex((item: any) => item.id === 'surface-separator') < episode.findIndex((item: any) => item.id === 'different-version'), 'the separator still leads to them');
});

test('the real TV Watch overlay puts Select before Details, then Play a different version and File info', async () => {
  const h = hooks();
  const shared = await screenModule(new URL('../src-next/kit/overlays/title-menu.ts', import.meta.url), {react: h.react, '../../../../../packages/i18n/src': {defaultI18n}});
  const theme = {isTV: true, layoutMode: 'tv', platform: 'tv', type: {caption: {}, body: {}}, colors: {}, textScale: 1};
  const {NativeWatchOverlays} = await screenModule(new URL('../src-next/screens/watch/WatchOverlays.tsx', import.meta.url), {
    react: h.react, 'react-native': {Modal: 'Modal', Platform: {isTV: true}, ScrollView: 'ScrollView', StyleSheet: {}, Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/administration': {}, '../../../../../packages/client-core/src/title-identity': {},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {}, '../../shell/model': {}, '../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog', Menu: 'Menu'},
    '../../kit/overlays/title-menu': shared, './menu-model': {nativeWatchMenuGroups}, '../../kit/overlays/Menu': {TouchMenuHeader: 'TouchMenuHeader'}, '../../kit/overlays/surface': {},
    '../../kit/overlays/TVPanelMotion': {useTVPanelPresence: () => ({})}, '../../kit/overlays/OverlayHost': {}, '../../kit/theme': {nativeType: () => ({}), useTheme: () => theme},
    './VersionChooser': {}, './DiscTitlesSheet': {}, './FileInfoContent': {}, './file-info': {},
    '../../kit/overlays/SurfacePaintHost': {overlayPaintKey: () => 'opening', overlayPaintPermit: () => ({}), RetainedOverlay: 'RetainedOverlay'},
  });
  const value: any = {phase: 'menu', capture: {entry}, busy: false, detail: detail(['play', 'watchlist', 'watched', 'add_to_collection']),
    actions: {fileInfo() {}, select() {}, differentVersion() {}, entry: {...menu}}};
  const presenters = {subscribe: () => () => {}, getSnapshot: () => value, overlayCurrent: () => true, current: () => true, togetherActions: () => ({queue: false, watch: false})};
  const items = nodes(h.render(() => NativeWatchOverlays({presenters, runtime: {artwork: () => undefined}, common: {}} as any))).find(node => node.type === 'Menu').props.items;
  const order = ids(items);
  assert.deepEqual(order.slice(order.indexOf('addToCollection')), ['addToCollection', 'select', 'details', 'different-version', 'file-info']);
});

// #754 review: with no provider overview the TV collection lede is composed from the titles as Home's banner does
// (tv-detail f10: "Four films on your server, from 1995 to Toy Story 5."); the overview replaces it when there is one.
test('the collection lede is composed from its titles when there is no overview', async () => {
  const mod = await collectionScreen(true) as any;
  const env = {t: defaultI18n.t as never, locale: 'en-US'};
  const film = (title: string, year: number) => ({id: title, kind: 'movie', title, year}) as any;
  const all = [film('Toy Story', 1995), film('Toy Story 2', 1999), film('Toy Story 3', 2010), film('Toy Story 5', 2026)];
  assert.equal(mod.composedCollectionLede({totalCount: 4, complete: all}, env), 'Four films on your server, from 1995 to Toy Story 5.');
  assert.equal(mod.composedCollectionLede({totalCount: 4, first: all[0]}, env), 'Four films on your server.', 'only part in memory: the count alone');
  assert.equal(mod.composedCollectionLede({totalCount: 2, complete: [film('A', 2001), {id: 'show', kind: 'show', title: 'S', year: 2003}]}, env), undefined, 'not all films: no lede');
  assert.equal(mod.composedCollectionLede({totalCount: 0}, env), undefined);
});
