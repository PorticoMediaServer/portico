// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {validateHomeSeeAll} from '../../../packages/client-core/src/home.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {homeSeeAllRoute} from '../src-next/shell/routes.ts';
import {createTheme} from '../src-next/kit/theme.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

// N4-1: every server See all destination opens a real page, never an empty row route.
test('See all opens the page the server names, and only a row opens the row page', () => {
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'row', rowId: 'recently_added'}), 'Recently added'),
    {kind: 'home-see-all', destination: {type: 'row', rowId: 'recently_added'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'guide'}), 'On now'), {kind: 'shell', destination: {kind: 'live'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'guide', group: 'news', sourceId: 'src'}), 'On now'), {kind: 'shell', destination: {kind: 'live'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'saved_view', resourceId: 'view_1'}), 'Sci-fi'),
    {kind: 'saved-view', view: {view: 'resource', resourceId: 'view_1'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'collection', resourceId: 'col_1', libraryId: 'lib_1'}), 'Pixar'),
    {kind: 'collection', libraryId: 'lib_1', collectionId: 'col_1', title: 'Pixar'});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'library', libraryId: 'lib_1', pivot: 'titles', sort: [{field: 'added', direction: 'desc'}]}), 'New'),
    {kind: 'shell', destination: {kind: 'library', libraryId: 'lib_1', definition: {libraryId: 'lib_1', pivot: 'titles', query: null, sort: [{field: 'added', direction: 'desc'}], presentation: 'grid'}}});
});

test('assembly actions.seeAll routes every destination through homeSeeAllRoute', async () => {
  const app = await standardHomeApp({mountUI: false});
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  let context: NativeShellBootstrapContext | undefined;
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    runtime = await restoreNativeShellRuntime(
      value => {context = value; return createNativeShellBindings(value);},
      {onAuthorityEnded() {}},
    );
    assert.ok(runtime && context);
    const cases = [
      {dest: validateHomeSeeAll({type: 'row', rowId: 'recently_added'}), title: 'Recently added'},
      {dest: validateHomeSeeAll({type: 'guide'}), title: 'On now'},
      {dest: validateHomeSeeAll({type: 'saved_view', resourceId: 'view_1'}), title: 'Sci-fi'},
      {dest: validateHomeSeeAll({type: 'collection', resourceId: 'col_1', libraryId: 'movies'}), title: 'Pixar'},
      {dest: validateHomeSeeAll({type: 'library', libraryId: 'movies', pivot: 'titles', sort: [{field: 'added', direction: 'desc'}]}), title: 'New'},
    ];
    for (const item of cases) {
      runtime.actions.seeAll(item.dest, item.title);
      assert.deepEqual(context.navigation.getSnapshot().route, homeSeeAllRoute(item.dest, item.title), `actions.seeAll routes ${item.dest.type}`);
    }
  } finally {
    try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
  }
});

test('Home ordinary rows, On now live row, and TV Hold-OK menu all open See all via runtime.actions.seeAll', async () => {
  const calls: Array<{destination: unknown; title: string}> = [];
  const authority = {serverId: 'srv', accountId: 'acc', profileId: 'prof', epoch: '["fam",false]', viewerFence: 'opened', privacyFence: 'opened'};
  const rowSeeAll = validateHomeSeeAll({type: 'collection', resourceId: 'col_1', libraryId: 'movies'});
  const liveSeeAll = validateHomeSeeAll({type: 'guide'});

  // 1) Ordinary row through useHome
  const phoneTheme = createTheme({isTV: false, width: 390});
  const {useHome} = await screenModule(new URL('../src-next/pages/shared/home/useHome.ts', import.meta.url), {
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: 390, height: 844, fontScale: 1})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34, left: 0, right: 0})},
    'react-native-reanimated': {runOnJS: (fn: any) => fn, useAnimatedScrollHandler: (fn: any) => fn, useSharedValue: (v: any) => React.useRef({value: v}).current},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/home-spotlight': {
      homeRowPresentation: (row: any) => ({title: {fallback: row.title}, subtitle: undefined, cards: []}),
      homeSpotlightModels: () => [],
      homeTitleFactParts: () => [],
    },
    '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({category: 'other'})},
    '../../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_l: unknown, text: any, fb?: string) => text?.fallback ?? fb ?? ''},
    '../../../kit/layout/FocusAnchor': {useFocusAnchor: () => React.useMemo(() => ({offset: {value: 0}, scrollRef: {current: null}, scrollStyle: {}, move() {}, stop() {}}), [])},
    '../../../kit/home': {homeRowMetrics: () => ({estimate: 220, gap: 16, cardWidth: 120})},
    '../../../kit/frames/metrics': {useFrameMetrics: () => ({width: 390, form: 'compact'})},
    '../../../kit/states': {skeletonTiming: {delayMs: 300, slowMs: 2500}},
    '../../../kit/theme': {useTheme: () => phoneTheme},
    '../../../shell/home-read-log': {noteHomeRead() {}},
    '../../../shell/boot-timing': {logBootMilestone() {}},
    '../../../shell/model': {phoneHeaderGeometry: () => ({top: 54, height: 104}), sameAuthority: () => true},
    '../../../shell/tv-focus': {tvFocusIds: () => ({entry: (e: any) => e.id})},
    '../../../screens/home/home-start': {homeSpotlightFocusActions: () => ({offers: {}})},
    '../../../screens/home/home-window': {homeMountedRows: () => new Set([0]), homeVisibleRange: () => [0, 0]},
    '../../../screens/home/live-expiry': {liveRereadPlan: () => undefined},
    '../../../screens/home/model': {authorizedHome: (doc: any) => doc, homeRowHasContent: () => true, pageAtVisibleEnd: () => false, platformRows: (doc: any) => doc.rows},
  });
  const doc = {
    spotlight: [],
    rows: [{id: 'pixar', title: 'Pixar', kind: 'collection', artworkShape: 'poster', entries: [{id: 'm1', kind: 'movie', title: 'Toy Story'}], seeAll: rowSeeAll}],
  };
  const homeSnapshot = {phase: 'ready', document: doc};
  const shellSnapshot = {authority, libraries: [], selectionActive: false, librariesUnavailable: false};
  let homeResult: any;
  const homeRuntime: any = {
    text: defaultI18n,
    home: {subscribe: () => () => {}, getSnapshot: () => homeSnapshot, demand() {}, load: async () => {}, revalidate: async () => {}, more: async () => {}},
    getSnapshot: () => shellSnapshot,
    registerScrollToTop: () => () => {},
    setHomeWindow() {},
    actions: {seeAll: (destination: unknown, title: string) => {calls.push({destination, title});}},
    reportError() {},
  };
  const homeHost = createFabricHookHost(() => {homeResult = useHome({runtime: homeRuntime, authority});});
  try {
    await act(() => homeHost.mount());
    const rowView = homeResult.row(0);
    assert.equal(typeof rowView.seeAll, 'function', 'ordinary row exposes seeAll');
    rowView.seeAll();
    assert.deepEqual(calls.at(-1), {destination: rowSeeAll, title: 'Pixar'});
  } finally {
    await homeHost.unmount();
  }

  // 2) Live On now row through HomeLiveRow
  const {HomeLiveRow} = await screenModule(new URL('../src-next/kit/home/HomeRows.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/home-spotlight': {homeCollectionCaption: () => [], homeRowPresentation: (r: any) => ({title: {fallback: r.title}})},
    '../../../../../packages/client-core/src/presentation/home-continuation': {homeContinuationCard: () => ({}), homeContinuationSource: (e: any) => e},
    '../../../../../packages/client-core/src/home-selection': {homeEntryCapabilities: () => ({})},
    '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_l: unknown, text: any, fb?: string) => text?.fallback ?? fb ?? ''},
    '../../../../../packages/client-core/src/presentation/kit-model': {programmeFactParts: () => [], programmeTimeRange: () => undefined},
    '../../../../../packages/client-core/src/presentation/continue-books': {continueBookFacts: () => ({})},
    '../../../../../packages/client-core/src/presentation/home-card': {homeCardText: () => ({}), homePlayTarget: () => undefined},
    '../../platform/ArtworkPipeline': {ArtworkPlacementContext: React.createContext(null)},
    '../artwork': {ArtworkArrivalContext: React.createContext(0), generatedPoster: () => undefined},
    '../cards': {CollectionBanner: 'CollectionBanner', LiveCard: 'LiveCard', PosterCard: 'PosterCard', RankedCard: 'RankedCard', ReadResumeCard: 'ReadResumeCard', ResumeCard: 'ResumeCard', SquareCard: 'SquareCard', WideCard: 'WideCard'},
    '../layout': {Row: 'Row'},
    '../layout/types': {itemSelection: () => undefined},
    '../states': {LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock'},
    '../theme': {useTheme: () => phoneTheme},
    '../../platform/home-action-diagnostics': {homeActionDiagnostics: {action() {}}},
  });
  let liveTree: any;
  const liveHost = createFabricHookHost(() => {
    liveTree = HomeLiveRow({
      row: {id: 'on_now', title: 'On now', seeAll: liveSeeAll, live: {observedAt: new Date(Date.now() - 1000).toISOString(), expiresAt: new Date(Date.now() + 3600_000).toISOString(), channels: []}},
      runtime: {...homeRuntime, selection: () => ({})},
      visible: true,
      itemWidth: 220,
      onRange() {},
      onVisibleKeys() {},
      onItemFocus() {},
      current: () => true,
      viewportWidth: 390,
      tabletMedium: false,
    });
  });
  try {
    await act(() => liveHost.mount());
    assert.equal(liveTree.type, 'Row');
    assert.equal(typeof liveTree.props.onSeeAll, 'function', 'HomeLiveRow exposes onSeeAll');
    liveTree.props.onSeeAll();
    assert.deepEqual(calls.at(-1), {destination: liveSeeAll, title: 'On now'});
  } finally {
    await liveHost.unmount();
  }

  // 3) TV Hold-OK menu through HomeSelectionOverlays
  const tvTheme = createTheme({isTV: true, width: 1920});
  const {HomeSelectionOverlays} = await screenModule(new URL('../src-next/shell/HomeSelectionOverlays.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {StyleSheet: {create: (s: any) => s}, View: 'View'},
    '../../../../packages/i18n/src': {defaultI18n},
    '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: ''})},
    '../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_l: unknown, text: any, fb?: string) => text?.fallback ?? fb ?? ''},
    '../kit/overlays': {Menu: 'Menu', ToastHost: 'ToastHost'},
    '../kit/overlays/title-menu': {titleMenuItems: (_groups: unknown, opts: any) => [...(opts.surface ?? []), ...(opts.trailing ?? [])]},
    '../kit/overlays/Menu': {TouchMenuHeader: 'TouchMenuHeader'},
    '../kit/selection': {DeleteMediaDialog: 'DeleteMediaDialog', MixedEditExplainer: 'MixedEditExplainer', RatingDialog: 'RatingDialog'},
    '../kit/artwork': {Artwork: 'Artwork'},
    '../kit/shell': {ServerMark: 'ServerMark'},
    '../kit/theme': {useTheme: () => tvTheme},
    '../platform/haptics': {selectionHaptic: async () => {}},
    './selection-display': {memberTitle: () => 'Toy Story'},
    './HomeDestinationPicker': {HomeDestinationPicker: 'HomeDestinationPicker'},
    './HomeBulkEditor': {HomeBulkEditor: 'HomeBulkEditor'},
    './tv-focus': {tvFocusIds: () => ({menu: () => 'menu-id', surface: (s: string) => s})},
  });
  let closedMenu = 0;
  const controllerSnapshot = {
    displayMembers: [],
    selection: {selectedKeys: [], active: false, summary: {count: 0}},
    menu: {key: 'k1', windowId: 'home-row:pixar', groups: [], member: {entry: {id: 'm1', kind: 'movie', title: 'Toy Story'}}},
    mixedEdit: false,
    deletion: null,
    rating: null,
  };
  const controller: any = {
    subscribe: () => () => {},
    getSnapshot: () => controllerSnapshot,
    closeMenu: () => {closedMenu++;},
  };
  let overlayTree: any;
  const overlayHost = createFabricHookHost(() => {
    overlayTree = HomeSelectionOverlays({
      controller,
      runtime: {
        ...homeRuntime,
        getSnapshot: () => ({authority, account: {viewerRole: 'member'}}),
        artwork: () => undefined,
        homeMenuRow: () => ({id: 'pixar', title: 'Pixar', seeAll: rowSeeAll}),
      },
      ownerTag: null,
      reportError() {},
    });
  });
  try {
    await act(() => overlayHost.mount());
    const menuNode = nodes(overlayTree).find(n => n.type === 'Menu');
    assert.ok(menuNode, 'TV Hold-OK menu renders');
    const seeAllItem = menuNode.props.items.find((item: any) => item.id === 'see-all');
    assert.ok(seeAllItem, 'TV Hold-OK menu includes See all action');
    seeAllItem.onSelect();
    assert.equal(closedMenu, 1, 'See all closes the menu first');
    assert.deepEqual(calls.at(-1), {destination: rowSeeAll, title: 'Pixar'});
  } finally {
    await overlayHost.unmount();
  }
});
