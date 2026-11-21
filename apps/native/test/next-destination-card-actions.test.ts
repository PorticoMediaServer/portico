// @fabric-react
import {savedFixture} from './helpers/saved-fixture.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
import {holdCard, type CardActions} from '../src-next/kit/cards/model.ts';
import {openNativeSavedDownload} from '../src-next/screens/downloads/playback.ts';

type Element = {type: any; props: Record<string, any>};
function elements(node: any): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children), ...elements(node.props.body), ...elements(node.props.grid)] : [];
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

test('Saved preview cards preserve inactive selection and the actual native menu anchor', async () => {
  const calls: any[] = [], selected = selection(calls), entry = {id: 'saved-id', kind: 'movie', title: 'Actual saved title'};
  const projection = {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'watchlist', entityId: '', viewerFence: 'actual-fence'},
    revision: {catalog: 1, viewer: 1}, navigation: [], sorts: [], filters: [], query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'},
    sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [entry], totalCount: 1, nextCursor: ''}]};
  const state = {phase: 'ready', myList: {projection}, myListSquares: {projection: undefined}, favorites: {}, playlists: {}, collections: {}, views: {}, pins: [],
    summary: {myList: {posters: 1, squares: 0}, favorites: {posters: 0, squares: 0}, playlists: 0, collections: 0, savedViews: 0}};
  const actions = {menuEntry: (...args: any[]) => calls.push(args), isMenuOpen: (value: any) => value === entry};
  let registered = 0, retired = 0;
  const {SavedScreen} = await savedFixture('Saved', {
    '../../pages/touch/saved/SavedPage': {SavedPage: 'SavedPage'},
    '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/saved/SavedLoading': {SavedLoading: 'SavedLoading', SavedTail: 'SavedTail'},
    './paging': {savedNearEnd: () => false},
    react: {...React, default: React}, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame', Parts: 'Parts'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/overlays': {Menu: 'Menu'},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Tabs: 'Tabs'}, '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {}, '../../kit/Icon': {}, '../../kit/browse/presentation': {browseType: () => ({}), alpha: () => ''},
    '../../kit/theme': {useTheme: () => theme}, '../../platform/offline-downloads': {groupOfflineEntries: () => []}, '../home/model': {homeCardText: () => ({caption: []}), savedCardText: () => ({caption: []}), nativeSearchCardText: () => ({caption: []})},
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
    const surface = await mount(tree.type, tree.props);
    assert.equal(surface.tree.props.pointerEvents, 'none');
    assert.equal(surface.tree.props.accessibilityElementsHidden, true, 'a retained hidden Saved screen has no active pointer or accessibility targets');
    await surface.host.unmount();
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
