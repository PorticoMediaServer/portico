// @fabric-react
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
import * as focusRegistry from '../src-next/kit/focus/registry.ts';
import * as focusHostRef from '../src-next/kit/focus/useHostRef.ts';
import {holdCard, type CardActions} from '../src-next/kit/cards/model.ts';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles.ts';
import {channelTint} from '../../../packages/client-core/src/presentation/kit-model/channels.ts';
import {openNativeSavedDownload} from '../src-next/screens/downloads/playback.ts';

type Element = {type: any; props: Record<string, any>};
function elements(node: any): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children)] : [];
}
const runtime = {text: defaultI18n, artwork: () => undefined, reportError() {}};
const theme = createTheme({width: 390, isTV: false});
const selection = (calls: string[], active = false) => ({active, selected: false, onToggle: () => calls.push('toggle'), onEnter: () => calls.push('enter')});

/** Mounts one shallow component under the real hook driver. React's own hook
 * dispatcher runs the module's effects, so layout effects commit and unmount runs
 * every cleanup and store release. No reconciler draws the tree: the test calls
 * the exported component as a function and inspects its elements. */
async function mount(component: (props: any) => any, props: any) {
  let tree: any;
  const host = createFabricHookHost(() => {tree = component(props);});
  await host.mount();
  return {host, get tree() {return tree;}};
}

/** The actual focus module's exports, loaded from its own source so the card's
 * useFocusWaiting reads its genuine default value. These shallow card trees sit
 * under no FocusItem, so this fixture never runs the focus hook's lifetime; a
 * FocusItem mount is what exercises that, and this test does not claim it. */
async function focusNamespace(react: any) {
  return screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {react: {...react, createContext: (value: any) => ({value}), forwardRef: (render: any) => render},
    'react-native': {findNodeHandle: () => undefined, TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => theme},
    './registry': focusRegistry, './useHostRef': focusHostRef});
}

async function searchFixture(group = 'all') {
  const host = hooks(), calls: any[] = [], selected = selection(calls);
  const entry = {id: 'real-result', kind: 'movie', libraryId: 'movies', title: 'Actual title'};
  const snapshot = {text: 'actual', group, groups: [{id: 'movies', status: 'success', items: [entry], totalCount: 1}], recent: [],
    search: {phase: 'ready', data: {capabilities: {groups: [{id: 'movies', available: true}]}}}};
  const actions = {open: (...args: any[]) => calls.push(['open', ...args]), play() {}, menu: (...args: any[]) => calls.push(['menu', ...args]),
    isMenuOpen: (item: any, kind: string) => item === entry && kind === 'movies'};
  const {SearchGridCard} = await screenModule(new URL('../src-next/screens/search/SearchGridCard.tsx', import.meta.url), {
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/cards/primitives': {CardFrame: 'CardFrame', ArtFrame: 'ArtFrame'},
    '../../kit/browse': {SongRow: 'SongRow'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => ({isTV: false})},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({caption: [], detail: []})},
    react: host.react, 'react-native': {View: 'View'}, '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard', CircleCard: 'CircleCard', EpisodeTile: 'EpisodeTile'},
    '../../kit/focus': await focusNamespace(host.react),
    '../home/model': {homeCardText: () => ({caption: []})}, './model': {nativeSearchEntryKey: (kind: string, item: any) => JSON.stringify([kind, item.kind, item.id])},
  });
  const {SearchScreen} = await screenModule(new URL('../src-next/screens/search/SearchScreen.tsx', import.meta.url), {
    react: {...host.react, useId: () => 'gradient'}, 'react-native': {FlatList: 'FlatList', ScrollView: 'ScrollView', Text: 'Text', View: 'View', Keyboard: {}, useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-svg': {default: 'Svg'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    './genre-tiles': {nativeSearchGenreTiles}, '../../../../../packages/client-core/src/presentation/kit-model': {channelTint},
    '../../../../../packages/client-core/src/presentation/search-model': {orderedSearchGroups: (groups: unknown) => groups, SEARCH_CHIPS: [], searchGroupLabel: (id: string) => id, topSearchResult: () => undefined},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard', CircleCard: 'CircleCard', EpisodeTile: 'EpisodeTile'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame'}, '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'},
    '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/overlays': {Banner: 'Banner'}, '../../kit/states': {}, '../../kit/Icon': {}, '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => theme},
    '../home/model': {homeCardText: () => ({caption: []})}, './model': {nativeSearchEntryKey: (kind: string, item: any) => JSON.stringify([kind, item.kind, item.id])},
    './SearchGridCard': {SearchGridCard}, './TVSearchScreen': {TVSearchScreen: 'TVSearchScreen'},
  });
  const tree = host.render(() => SearchScreen({destination: {subscribe() {}, getSnapshot: () => snapshot, setText() {}}, runtime,
    explore: {subscribe() {}, getSnapshot: (() => {const snapshot = {genres: []}; return () => snapshot;})()}, actions, activation: {subscribe() {}, getSnapshot: (() => {const snapshot = {visible: true, intent: 'enter', revision: 1}; return () => snapshot;})()},
    selection: {item: () => selected, render: () => 'real-selection-toolbar'}}));
  return {host, tree, entry, actions, selected, calls};
}

test('Search rows bind the actual card anchor/live menu and selecting touch holds only toggle', async () => {
  const f = await searchFixture();
  const row = elements(f.tree).find(node => node.type?.name === 'SearchResultRow')!;
  const tree = f.host.render(() => row.type(row.props));
  const card = elements(tree).find(node => node.type === 'CardFrame')!;
  assert.equal(card.props.selection, f.selected, 'inactive selection remains available to CardFrame accessibility actions');
  assert.equal(card.props.moreExpanded, true);
  const realNativeCard = {actual: 'card'}; card.props.menuAnchorRef.current = realNativeCard;
  holdCard(card.props as CardActions, false);
  assert.deepEqual(f.calls, [['menu', f.entry, 'movies', card.props.menuAnchorRef]]);
  assert.equal(f.calls[0][3].current, realNativeCard);
  f.selected.active = true;
  holdCard(card.props as CardActions, false);
  assert.deepEqual(f.calls.at(-1), 'toggle');
  holdCard(card.props as CardActions, true);
  assert.deepEqual(f.calls.at(-1), ['menu', f.entry, 'movies', card.props.menuAnchorRef], 'TV Menu/hold keeps the existing behavior');
  assert.ok(elements(f.tree).some(node => node.props.children?.includes('real-selection-toolbar')));
});

test('Search grid passes genuine inactive selection to media, people and artist shared cards', async () => {
  const f = await searchFixture('movies');
  const list = elements(f.tree).find(node => node.type === 'FlatList')!;
  const element = list.props.renderItem({item: f.entry});
  for (const group of ['movies', 'people', 'artists']) {
    const card = f.host.render(() => element.type({...element.props, group}));
    assert.equal(card.type, group === 'movies' ? 'PosterCard' : 'CircleCard');
    assert.equal(card.props.selection, f.selected);
    card.props.onMore();
    assert.deepEqual(f.calls.at(-1), ['menu', f.entry, group, card.props.menuAnchorRef]);
    assert.equal(card.props.moreExpanded, group === 'movies');
    assert.equal(card.props.onLongPressAction, undefined, 'no custom hold handler bypasses CardFrame');
  }
});

test('Saved preview cards preserve inactive selection and the actual native menu anchor', async () => {
  const calls: any[] = [], selected = selection(calls), entry = {id: 'saved-id', kind: 'movie', title: 'Actual saved title'};
  const projection = {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'watchlist', entityId: '', viewerFence: 'actual-fence'},
    revision: {catalog: 1, viewer: 1}, navigation: [], sorts: [], filters: [], query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'},
    sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [entry], totalCount: 1, nextCursor: ''}]};
  const state = {phase: 'ready', myList: {projection}, myListSquares: {projection: undefined}, favorites: {}, playlists: {}, collections: {}, views: {}, pins: [],
    summary: {myList: {posters: 1, squares: 0}, favorites: {posters: 0, squares: 0}, playlists: 0, collections: 0, savedViews: 0}};
  const actions = {menuEntry: (...args: any[]) => calls.push(args), isMenuOpen: (value: any) => value === entry};
  let registered = 0, retired = 0;
  const {SavedScreen} = await screenModule(new URL('../src-next/screens/saved/SavedScreen.tsx', import.meta.url), {
    '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, './SavedLoading': {SavedLoading: 'SavedLoading', SavedTail: 'SavedTail'},
    './paging': {savedNearEnd: () => false},
    react: {...React, default: React}, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame', Parts: 'Parts'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/overlays': {Menu: 'Menu'},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Tabs: 'Tabs'}, '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {}, '../../kit/Icon': {}, '../../kit/browse/presentation': {browseType: () => ({}), alpha: () => ''},
    '../../kit/theme': {useTheme: () => theme}, '../../platform/offline-downloads': {groupOfflineEntries: () => []}, '../home/model': {homeCardText: () => ({caption: []})},
    './model': {savedProjectionCount: () => undefined, savedDirectoryCount: () => undefined},
    './SavedDownloadCard': {SavedDownloadCard: 'SavedDownloadCard'},
    '../downloads/playback': {openNativeSavedDownload},
    '../../../../../packages/client-core/src/presentation/errors': {presentError},
  });
  const screen = await mount(SavedScreen, {destination: {subscribe() {}, getSnapshot: () => state, load: async () => {}}, runtime: {...runtime, registerScrollToTop() {}},
    device: {subscribe() {}, getSnapshot: (() => {const snapshot = {entries: []}; return () => snapshot;})(), open: async () => {}},
    actions, activation: {subscribe() {}, getSnapshot: (() => {const snapshot = {visible: false, intent: 'restore', revision: 2}; return () => snapshot;})()}, selection: {register: () => {registered++; return () => {retired++;};}, item: () => selected, render: () => 'actual-toolbar'}});
  try {
    const tree = screen.tree;
    assert.equal(registered, 1, 'the committed screen effect registered the actual selection port');
    assert.equal(tree.props.pointerEvents, 'none'); assert.equal(tree.props.accessibilityElementsHidden, true, 'a retained hidden Saved screen has no active pointer or accessibility targets');
    const child = elements(tree).find(node => node.type?.name === 'SavedMyListCard')!;
    const cardScreen = await mount(child.type, child.props);
    try {
      const card = cardScreen.tree;
      assert.equal(card.props.selection, selected); assert.equal(card.props.moreExpanded, true);
      const actualCard = {actual: 'native-saved-card'}; card.props.menuAnchorRef.current = actualCard; card.props.onMore();
      assert.deepEqual(calls, [[entry, card.props.menuAnchorRef, selected.onEnter]]); assert.equal(calls[0][1].current, actualCard);
      selected.active = true; holdCard(card.props, false); assert.equal(calls.at(-1), 'toggle');
    } finally {await cardScreen.host.unmount();}
  } finally {await screen.host.unmount();}
  assert.equal(retired, 1, 'unmount runs the registered selection cleanup');
});
