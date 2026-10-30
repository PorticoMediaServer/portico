import test from 'node:test';
import assert from 'node:assert/strict';
import {createTheme} from '../src-next/kit/theme.ts';
import {focusTestID} from '../src-next/kit/focus/registry.ts';
import {titleBucketValue} from '../../../packages/client-core/src/browse-buckets.ts';
import {savedListFilters} from '../../../packages/client-core/src/saved.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

function reactFixture(host: ReturnType<typeof hooks>) {
  const react = {...host.react, useLayoutEffect() {}, useImperativeHandle: (ref: any, create: () => unknown) => {if (ref) ref.current = create();}};
  return {...react, default: react};
}
function nodes(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  return node?.props ? [node, ...nodes(node.props.children)] : [];
}

test('TV Saved tabs have a mounted content target and return edges for every tab', async () => {
  for (const tab of ['watchlist', 'favorites', 'history', 'playlists', 'collections', 'views']) {
    const host = hooks();
    let visible = true, back: any, entry: any;
    const ids = {savedTabs: 'tabs', savedGrid: 'grid', savedToolbar: 'toolbar', rail: 'rail'};
    const {TVSavedScreen} = await screenModule(new URL('../src-next/screens/destinations/saved/TVSavedScreen.tsx', import.meta.url), {
      react: reactFixture(host),
      'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
      '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, number: String}},
      '../../../../../../packages/client-core/src/browse-buckets': {titleBucketValue},
      '../../../../../../packages/client-core/src/saved': {savedListFilters},
      '../../../tv/layout/TVFocusGrid': {TVFocusGrid: 'TVFocusGrid'},
      '../../saved/SavedForms': {SavedConfirmSheet: 'SavedConfirmSheet'},
      '../../saved/SavedTVLoading': {SavedTVLoading: 'SavedTVLoading'},
      '../../library/LibraryJumpSheet': {LibraryJumpSheet: 'LibraryJumpSheet'},
      '../useTVPageBack': {useTVPageBack(_runtime: unknown, _route: unknown, _enabled: boolean, page: unknown) {back = page;}},
      '../useEntryFocus': {useDestinationEntryFocus(_active: unknown, target: unknown) {entry = target;}},
      '../../../shell/model': {sameAuthority: () => true},
      '../../../../../../packages/client-core/src/presentation/kit-model/text': {durationText: String},
      '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'},
      '../../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'},
      '../../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard'},
      '../../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'},
      '../../../kit/states': {StateView: 'StateView'},
      '../../../kit/focus': {FocusRegion: 'FocusRegion', focusTestID},
      '../../../kit/browse/presentation': {browseType: () => ({})},
      '../../../kit/theme': {useTheme: () => createTheme({isTV: true, width: 1920})},
      '../focus': {nativeDestinationFocusIds: () => ids},
      '../../saved/SavedTVTabs': {SavedTVTabs: 'SavedTVTabs'},
      '../../saved/SavedTVDirectory': {SavedTVDirectory: 'SavedTVDirectory'},
    });
    const snapshot = {phase: 'ready', entries: [], resources: [],
      posters: {sections: [{entries: [{id: 'movie/1', kind: 'movie', title: 'Movie'}]}]},
      squares: {sections: [{entries: [{id: 'song/1', kind: 'song', title: 'Song'}]}]},
    };
    const props = {
      controller: {subscribe: () => () => {}, getSnapshot: () => snapshot, source: () => ({}), select: async () => {}},
      activation: {subscribe: () => () => {}, getSnapshot: () => ({visible})},
      runtime: {reportError: () => {}, text: {}, artwork: () => undefined, getSnapshot: () => ({libraries: []})}, actions: {isMenuOpen: () => false}, authority: {}, initialTab: tab,
      openSaved: () => {}, openEntry: () => {}, browseHome: () => {},
    };
    const render = () => nodes(host.render(() => TVSavedScreen(props as any)));
    const mounted = render();
    const tabs = mounted.find(node => node.type === 'SavedTVTabs');
    const directory = mounted.find(node => node.type === 'SavedTVDirectory');
    if (directory) {
      assert.equal(tabs.props.contentRegionId, directory.props.regionId);
      assert.equal(directory.props.enabled, true);
      assert.deepEqual(directory.props.edges, {up: ids.savedTabs, left: ids.rail});
      visible = false;
      assert.equal(render().find(node => node.type === 'SavedTVDirectory').props.enabled, false);
      continue;
    }
    const region = mounted.find(node => node.type === 'FocusRegion' && node.props.id === ids.savedGrid);
    assert.equal(tabs.props.contentRegionId, region.props.id, tab);
    assert.deepEqual(region.props.edges, {up: ids.savedToolbar, left: ids.rail});
    assert.equal(mounted.find(node => node.props.id === ids.savedToolbar).props.edges.down, ids.savedGrid);
    assert.equal(region.props.enabled, true);
    for (const control of nodes(region.props.children).filter(node => ['Button', 'Chip', 'PosterCard', 'SquareCard'].includes(node.type) && !node.props.disabled)) {
      assert.ok(control.props.focusId || control.props.testID, 'every focusable region child has a stable address');
    }
    assert.ok(mounted.some(node => node.props.id === ids.savedToolbar), 'toolbar owns a separate native region');
    if (tab === 'watchlist' || tab === 'favorites') {
      assert.equal(entry.regionId, 'grid.posters');
      const grids = mounted.filter(node => node.type === 'TVFocusGrid');
      assert.equal(grids[0].props.focus.edges.down, 'grid.squares');
      assert.equal(grids[1].props.focus.edges.up, 'grid.posters');
      assert.equal(grids[1].props.focus.neighbors.previous, 'grid.posters');
      const child = grids[0].props.renderItem(snapshot.posters.sections[0].entries[0], {index: 0, top: 0, onFocus() {}});
      assert.equal(nodes(child).find(node => node.type === 'PosterCard').props.testID, grids[0].props.focus.getFocusId(snapshot.posters.sections[0].entries[0]));
    }
    assert.deepEqual(back.start(), entry, 'Back uses the concrete section address, not a nonexistent compound child');
    if (tab === 'favorites') {
      // H14: a new sort/filter or a background reload on the same tab keeps the loaded grid painted.
      const ready = props.controller.getSnapshot;
      props.controller = {...props.controller, getSnapshot: () => ({phase: 'loading', entries: [], resources: []}) as any};
      const reloading = render();
      assert.ok(!reloading.some(node => node.type === 'SavedTVLoading'), 'the loading outline never replaces loaded content');
      assert.ok(reloading.some(node => node.type === 'TVFocusGrid'), 'the last ready grid stays mounted');
      props.controller = {...props.controller, getSnapshot: ready};
    }
    if (tab === 'watchlist') {
      mounted.find(node => node.type === 'Menu' && node.props.title === 'saved.sort').props.items.find((item: any) => item.id === 'title').onSelect();
      let current = render();
      current.find(node => node.type === 'View' && node.props.children?.some((child: any) => child?.props?.id === ids.savedToolbar)).props.onLayout({nativeEvent: {layout: {y: 65}}});
      const body = current.find(node => node.props.id === ids.savedGrid);
      body.props.onLayout({nativeEvent: {layout: {y: 90}}});
      nodes(body.props.children).find(node => node.type === 'View' && node.props.onLayout).props.onLayout({nativeEvent: {layout: {y: 38}}});
      const posters = current.find(node => node.type === 'TVFocusGrid' && node.props.focus.id === 'grid.posters');
      posters.props.onLayout({nativeEvent: {layout: {y: 120}}});
      posters.props.onGeometry(3, 420);
      const scroll: unknown[] = [];
      current.find(node => node.type === 'ScrollView').props.ref.current = {scrollTo: (request: unknown) => scroll.push(request)};
      current.find(node => node.props.focusId === 'saved-jump').props.onPress();
      current = render();
      current.find(node => node.type === 'LibraryJumpSheet').props.onSelect('M');
      assert.deepEqual(scroll, [{y: 289, animated: true}], 'Jump sums outer region, toolbar/body, inner padded section, grid and cell exactly once');
      snapshot.posters = {sections: [{entries: []}]}; snapshot.squares = {sections: [{entries: []}]}; render();
      assert.deepEqual(entry, {regionId: ids.savedGrid, childId: focusTestID('saved-empty', JSON.stringify(['action', 'browse-home']))}, 'the approved empty Browse Home child remains the actual entry');
    }

    visible = false;
    assert.equal(render().find(node => node.props.id === ids.savedGrid).props.enabled, false, 'retained hidden content cannot receive focus');
    const hiddenGrid = render().find(node => node.type === 'TVFocusGrid');
    if (hiddenGrid && tab !== 'history' && snapshot.posters.sections[0].entries[0]) {
      const painted = hiddenGrid.props.renderItem(snapshot.posters.sections[0].entries[0], {index: 0, top: 0, onFocus() {}});
      assert.equal(nodes(painted).find(node => node.type === 'PosterCard').props.disabled, true, 'the native card host retires with the hidden route');
    }
  }
});

test('TV Saved directory keeps filters, retry and pagination inside its content region', async () => {
  const host = hooks();
  let snapshot: any;
  const {SavedTVDirectory} = await screenModule(new URL('../src-next/screens/saved/SavedTVDirectory.tsx', import.meta.url), {
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Saved failed'})},
    './SavedForms': {SavedNameSheet: 'SavedNameSheet'}, './SavedTVLoading': {SavedTVLoading: 'SavedTVLoading'},
    react: reactFixture(host),
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/controls': {Chip: 'Chip', Focusable: 'Focusable'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', CoverMosaic: 'CoverMosaic', MediaArt: 'MediaArt'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame'},
    '../../kit/focus': {FocusRegion: 'FocusRegion', FocusItem: 'FocusItem', useFocusRegistry: () => ({focus() {}})},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/states': {StateView: 'StateView'},
    '../../kit/browse/presentation': {alpha: () => '', browseType: () => ({})},
    '../../kit/theme': {useTheme: () => createTheme({isTV: true, width: 1920})},
    './PlaylistToolbar': {PlaylistToolbar: 'PlaylistToolbar'},
    './presentation': Object.fromEntries(['savedCollectionCount', 'savedItemCount', 'savedLibraryName', 'savedTitleCount', 'savedViewCount', 'savedVisibility'].map(key => [key, String])),
  });
  const props: any = {
    view: 'playlists', enabled: true, activation: {subscribe: () => () => {}, getSnapshot: () => ({visible: true, revision: 1, intent: 'enter'})}, regionId: 'grid', edges: {up: 'tabs', left: 'rail'}, libraries: [],
    destination: {subscribe: () => () => {}, getSnapshot: () => snapshot},
    runtime: {error: () => ({primaryAction: {id: 'retry', label: 'Retry', onPress: () => {}}})},
  };
  for (const phase of ['empty', 'ready', 'error', 'loading']) {
    snapshot = {phase: phase === 'loading' ? 'loading' : 'ready', ...(phase === 'error' ? {error: new Error('offline')} : {}),
      ...(phase === 'loading' ? {} : {saved: {projection: {
        query: {}, sorts: [{id: 'title'}], filters: [{id: 'category', options: [{id: 'all', label: 'All'}]}],
        sections: [{totalCount: phase === 'ready' ? 1 : 0, entries: phase === 'ready' ? [{id: 'one', kind: 'playlist'}] : []}],
      }, pagination: {next: [{sectionId: 'main'}]}}}),
    };
    let tree = host.render(() => SavedTVDirectory(props));
    const body = nodes(tree).find(node => node.type === 'FocusRegion' && node.props.id === 'grid');
    assert.ok(body, phase);
    assert.deepEqual(body.props.edges, props.edges);
    const children = nodes(body.props.children);
    if (phase !== 'loading') assert.ok(children.some(node => node.type === 'PlaylistToolbar'), phase);
    if (phase === 'ready') {
      assert.ok(children.some(node => node.props.focusId === 'tv-saved-more'));
      const grid = children.find(node => node.props.id === 'grid.cards');
      grid.props.onLayout({nativeEvent: {layout: {width: 1586}}});
      tree = host.render(() => SavedTVDirectory(props));
      assert.deepEqual(nodes(tree).find(node => node.props.id === 'grid.cards').props.grid, {columns: 6, count: 1});
      assert.deepEqual(grid.props.edges, {up: 'grid.toolbar', left: 'rail', down: 'grid.more'});
      assert.deepEqual(children.find(node => node.type === 'FocusItem').props.cell, {index: 0});
      const handle: any = {current: null}; props.pageRef = handle;
      tree = host.render(() => SavedTVDirectory(props));
      const scroller = nodes(tree).find(node => node.type === 'ScrollView'), calls: unknown[] = [];
      scroller.props.ref({scrollTo: (request: unknown) => calls.push(request)});
      scroller.props.onScroll({nativeEvent: {contentOffset: {y: 183.25}}});
      assert.equal(handle.current.atTop(), false); handle.current.scrollTop();
      assert.deepEqual(calls, [{y: 0, animated: false}]);
      scroller.props.onScroll({nativeEvent: {contentOffset: {y: 0}}}); assert.equal(handle.current.atTop(), true);
    }
    if (phase === 'error') assert.equal(children.find(node => node.type === 'StateView').props.primaryAction.id, 'retry');
    props.enabled = false;
    assert.equal(nodes(host.render(() => SavedTVDirectory(props))).find(node => node.type === 'FocusRegion' && node.props.id === 'grid').props.enabled, false);
    props.enabled = true;
  }
});


test('the shared loaded-grid adapter binds actual native indices and measured columns without changing paint geometry', async () => {
  const host = hooks();
  const {TVFocusGrid} = await screenModule(new URL('../src-next/tv/layout/TVFocusGrid.tsx', import.meta.url), {
    react: host.react, '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion'}, './TVLoadedGrid': {TVLoadedGrid: 'TVLoadedGrid'},
  });
  const items = Array.from({length: 8}, (_, index) => ({id: `real-${index}`}));
  const props: any = {items, getKey: (item: any) => item.id, itemWidth: 200, itemHeight: 380, gap: 32, rowGap: 40,
    scrollY: 137, viewportHeight: 700, coordinateRoot: {current: null}, renderItem: (item: any, context: any) => ({type: 'Card', props: {item, ...context}}),
    focus: {id: 'posters', getFocusId: (item: any) => `saved-poster-${item.id}`, edges: {up: 'toolbar', left: 'rail'}}};
  let tree = host.render(() => TVFocusGrid(props));
  let grid = nodes(tree).find(node => node.type === 'TVLoadedGrid'); grid.props.onGeometry(3, 420);
  tree = host.render(() => TVFocusGrid(props)); grid = nodes(tree).find(node => node.type === 'TVLoadedGrid');
  assert.deepEqual(tree.props.grid, {columns: 3, count: 8});
  assert.equal(grid.props.itemWidth, 200); assert.equal(grid.props.itemHeight, 380); assert.equal(grid.props.scrollY, 137);
  const bound = grid.props.renderItem(items[7], {index: 7, top: 840, onFocus() {}});
  assert.equal(bound.type, 'FocusItem'); assert.equal(bound.props.focusId, 'saved-poster-real-7'); assert.deepEqual(bound.props.cell, {index: 7});
  assert.equal(nodes(bound).find(node => node.type === 'Card').props.top, 840);
  props.getRowHeight = () => 104; tree = host.render(() => TVFocusGrid(props));
  assert.equal(tree.props.grid.columns, 1, 'measured variable-height history is one native column');
  props.focus.isFocusable = (item: any) => item.id !== 'real-0'; tree = host.render(() => TVFocusGrid(props));
  assert.equal(tree.props.grid.count, 7, 'unavailable history paint is not a fabricated native stop');
  grid = nodes(tree).find(node => node.type === 'TVLoadedGrid');
  assert.deepEqual(grid.props.renderItem(items[1], {index: 1, top: 104}).props.cell, {index: 0}, 'the first actual history host has the true Up boundary');
});
