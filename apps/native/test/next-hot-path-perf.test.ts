// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {browseToolbarModel} from '../../../packages/client-core/src/presentation/kit-model';
import {libraryTabs} from '../../../packages/client-core/src/presentation/library-tabs';
import {visibleLibraryCounts} from '../../../packages/client-core/src/library-summary';
import {serverTextLabel} from '../../../packages/client-core/src/presentation/server-text';
import {shapeFor} from '../../../packages/client-core/src/presentation/card-shape';
import {homeRowPresentation, homeSpotlightModels} from '../../../packages/client-core/src/presentation/home-spotlight';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {libraryAppliedFilters} from '../src-next/screens/library/applied-filters.ts';
import {libraryAlphabetRail} from '../src-next/screens/library/jump.ts';
import {nativeLibraryCanSave, nativeLibraryEntryKey} from '../src-next/screens/library/model.ts';
import {tvFocusIds, tvHomeRegionMap} from '../src-next/shell/tv-focus.ts';
import {NativeHomePaging} from '../src-next/shell/home-paging.ts';
import {tvHomeRows} from '../src-next/screens/home/tv-presentation.ts';
import * as homeStart from '../src-next/screens/home/home-start.ts';
import * as homeWindow from '../src-next/screens/home/home-window.ts';
import * as homeModel from '../src-next/screens/home/model.ts';
import * as liveExpiry from '../src-next/screens/home/live-expiry.ts';
import {phoneHeaderGeometry, sameAuthority} from '../src-next/shell/model.ts';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry.ts';
import {nativeDestinationFocusIds} from '../src-next/screens/destinations/focus.ts';
import {nativeSearchChipCount, nativeSearchEntryKey, nativeSearchTailKey} from '../src-next/screens/search/model.ts';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles.ts';
import {searchGroupLabel, topResultKindLabel} from '../../../packages/client-core/src/presentation/search-model';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import * as rowState from '../src-next/kit/layout/row-state.ts';
import * as layoutTypes from '../src-next/kit/layout/types.ts';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed.ts';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes)
  : value && typeof value === 'object' && 'props' in value ? [value, ...nodes(value.props.children)] : [];

test('useLibraryGrid preserves model, appliedChips and callback identities across onVisibleRange scroll updates', async () => {
  const react = {...React, default: React};
  const {useLibraryGrid} = await screenModule(new URL('../src-next/pages/shared/library/useLibrary.ts', import.meta.url), {
    react,
    '../../../../../../packages/client-core/src/presentation/card-shape': {shapeFor},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel},
    '../../../../../../packages/client-core/src/presentation/library-tabs': {libraryTabs},
    '../../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel},
    '../../../../../../packages/client-core/src/library-summary': {visibleLibraryCounts},
    '../../../screens/share/share-actions': {shareLinkActions: () => []},
    '../../../screens/library/model': {nativeLibraryCanSave, nativeLibraryEntryKey},
    '../../../screens/library/applied-filters': {libraryAppliedFilters},
    '../../../screens/library/jump': {libraryAlphabetRail},
    '../../../screens/destinations/focus': {createDestinationEntryFocus: () => ({}), nativeDestinationFocusIds},
    '../../../screens/destinations/useTVPageBack': {useTVPageBack() {}},
    '../../../kit/focus/useDestinationEntryFocus': {useDestinationEntryFocus() {}},
    '../../../kit/focus': {useFocusRegistry: () => createFocusRegistry()},
    '../../../screens/read/platform': {hiddenReadingLibraryOnTV: () => false},
    '../../../kit/states/usePullRefresh': {usePullRefresh: () => undefined},
    './useFirstScanning': {useFirstScanning: () => false},
  });
  const items = Array.from({length: 60}, (_, index) => ({id: `movie-${index}`, kind: 'movie', libraryId: 'movies', title: `Movie ${index}`}));
  const collection = createWindowedCollection({fetchPage: async () => ({items, total: items.length}), pageSize: 60, maxResidentPages: 4, keyOf: (item: any) => item.id});
  collection.ensureRange(0, 20);
  await new Promise<void>(resolve => setImmediate(resolve));
  const capabilities = {
    library: {id: 'movies', name: 'Movies', kind: 'movie', defaultView: 'grid', pinned: false},
    pivots: [], resolvedPivot: null,
    fields: [{id: 'genre', labelKey: 'filter.field.genre', type: 'identity-set', operators: ['contains-any'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']}],
    sorts: [{field: 'title', direction: 'asc', labelKey: 'sort.title'}], quickFilters: [],
    queryLimits: {maximumDepth: 8, maximumClauses: 100, maximumBytes: 65536, maximumSorts: 4, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 60000},
  };
  const predicate = {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Drama']};
  const sort = {field: 'title', direction: 'asc'};
  let source: any = {
    phase: 'ready', library: capabilities.library, capabilities, predicates: [predicate], sort,
    tab: {id: 'browse', shows: 'grid', pivot: 'movies'}, collection, queryRevision: 0,
    memory: {firstVisibleIndex: 0, offset: 0}, discover: {appended: []},
  };
  const props: any = {
    activation: {subscribe: () => () => {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})},
    destination: {subscribe: () => () => {}, getSnapshot: () => source, remember() {}, setQuery() {}},
    directory: {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready', libraries: []})},
    runtime: {text: defaultI18n, reportError(error: unknown) {throw error;}, registerScrollToTop: () => () => {}},
    libraries: [capabilities.library], actions: {}, labels: {field: () => 'Genre'}, owner: true,
  };
  let latest: any;
  const host = createFabricHookHost(() => {latest = useLibraryGrid(props, source, false);});
  try {
    await act(() => host.mount());
    const initialModel = latest.model;
    const initialChips = latest.appliedChips;
    const initialClear = latest.clear;
    const initialSetSort = latest.setSort;
    const initialRemove = latest.remove;
    const initialReshuffle = latest.reshuffle;
    const initialOnValueChange = latest.onValueChange;

    await act(() => {latest.onVisibleRange(12, 35);});
    assert.deepEqual(latest.range, {first: 12, last: 35});
    assert.equal(latest.model, initialModel, 'model keeps referential identity across scroll range updates');
    assert.equal(latest.appliedChips, initialChips, 'appliedChips keeps referential identity across scroll range updates');
    assert.equal(latest.clear, initialClear);
    assert.equal(latest.setSort, initialSetSort);
    assert.equal(latest.remove, initialRemove);
    assert.equal(latest.reshuffle, initialReshuffle);
    assert.equal(latest.onValueChange, initialOnValueChange);

    source = {...source, predicates: []};
    await act(() => host.rerender());
    assert.notEqual(latest.model, initialModel, 'model recomputes when predicates change');
    assert.notEqual(latest.appliedChips, initialChips, 'appliedChips recomputes when predicates change');
  } finally {
    await act(() => host.unmount());
    collection.dispose();
  }
});

test('useHome memoizes models and boxes and avoids re-running layout box sync on unrelated state changes', async () => {
  const react = {...React, default: React};
  const theme = createTheme({isTV: false, width: 390});
  let presentationCalls = 0;
  let scrollBoxesWrites = 0;
  const vertical = {scrollRef: {}, offset: {value: 0}, scrollStyle: {}, stop() {}, move() {}, anchor() {}};
  const {useHome} = await screenModule(new URL('../src-next/pages/shared/home/useHome.ts', import.meta.url), {
    react,
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: 390, height: 844, fontScale: 1})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34, left: 0, right: 0})},
    'react-native-reanimated': {
      runOnJS: (fn: unknown) => fn,
      useAnimatedScrollHandler: (fn: unknown) => fn,
      useSharedValue: (initial: unknown) => {
        const ref = React.useRef<any>(undefined);
        if (!ref.current) {
          let current = initial;
          ref.current = {
            get value() {return current;},
            set value(next: unknown) {if (Array.isArray(next)) scrollBoxesWrites++; current = next;},
          };
        }
        return ref.current;
      },
    },
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/home-spotlight': {
      homeRowPresentation: (row: any) => {presentationCalls++; return homeRowPresentation(row);},
      homeSpotlightModels, homeTitleFactParts: () => [],
    },
    '../../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel},
    '../../../kit/layout/FocusAnchor': {useFocusAnchor: () => vertical},
    '../../../kit/home': {homeRowMetrics: () => ({itemWidth: 140, itemHeight: 210, captionHeight: 40, estimate: 290, gap: 24})},
    '../../../kit/frames/metrics': {useFrameMetrics: () => ({width: 390, form: 'phone'})},
    '../../../kit/states': {skeletonTiming: {delayMs: 300, slowMs: 3000}},
    '../../../kit/theme': {useTheme: () => theme},
    '../../../shell/home-read-log': {noteHomeRead() {}},
    '../../../shell/boot-timing': {logBootMilestone() {}},
    '../../../shell/model': {phoneHeaderGeometry, sameAuthority},
    '../../../shell/tv-focus': {tvFocusIds},
    '../../../screens/home/home-start': homeStart,
    '../../../screens/home/home-window': homeWindow,
    '../../../screens/home/live-expiry': liveExpiry,
    '../../../screens/home/model': homeModel,
  });
  const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};
  const entry = {id: 'm1', kind: 'movie', libraryId: 'movies', title: 'Movie 1'};
  const document = {
    serverId: 's', viewerFence: 'v',
    rows: [{id: 'new_movies', kind: 'new', artworkShape: 'poster', title: 'New movies', entries: [entry], nextCursor: ''}],
    spotlight: [{entry, reason: {kind: 'featured', text: {fallback: 'Featured'}}}],
  };
  const homeSnapshot = {document, phase: 'ready'};
  const runtime: any = {
    home: {subscribe: () => () => {}, getSnapshot: () => homeSnapshot, load() {}, demand() {}, more() {}, revalidate: async () => {}, retry: async () => {}},
    subscribe: () => () => {},
    getSnapshot: () => ({authority, libraries: [], librariesUnavailable: false, selectionActive: false}),
    text: defaultI18n, registerScrollToTop: () => () => {}, setHomeWindow() {}, reportError() {},
    actions: {play() {}, myList: async () => {}, customize() {}, seeAll() {}},
  };
  let latest: any;
  const host = createFabricHookHost(() => {latest = useHome({runtime, authority});});
  try {
    await act(() => host.mount());
    const initialModels = latest.models;
    const initialBoxes = latest.boxes;
    const initialRow = latest.row(0);
    const writesAfterMount = scrollBoxesWrites;
    const presentationsAfterMount = presentationCalls;
    assert.equal(latest.rowTitle(document.rows[0] as any), 'New movies');
    assert.equal(presentationCalls, presentationsAfterMount, 'rowTitle reuses memoized presentation');

    await act(() => {latest.setActiveIndex(1);});
    assert.equal(latest.models, initialModels, 'models remain memoized across activeIndex changes');
    assert.equal(latest.boxes, initialBoxes, 'boxes remain memoized across activeIndex changes');
    assert.equal(scrollBoxesWrites, writesAfterMount, 'scrollBoxes layout effect does not re-run when boxes are unchanged');
    assert.equal(presentationCalls, presentationsAfterMount, 'homeRowPresentation does not re-run on unrelated renders');
    const afterRow = latest.row(0);
    assert.equal(afterRow.onLayout, initialRow.onLayout, 'row onLayout handler stays stable');
    assert.equal(afterRow.onRange, initialRow.onRange, 'row onRange handler stays stable');
    assert.equal(afterRow.onVisibleKeys, initialRow.onVisibleKeys, 'row onVisibleKeys handler stays stable');
  } finally {
    await act(() => host.unmount());
  }
});

test('useTVHome uses scoped region subscriptions and preserves rowModels, rowBoxes and startModel across intra-row D-pad moves', async () => {
  const react = {...React, default: React};
  const theme = createTheme({isTV: true, width: 1920});
  const baseRegistry = createFocusRegistry();
  let globalSubscribes = 0, scopedSubscribes = 0, scrollBoxesWrites = 0;
  const registry = {
    ...baseRegistry,
    subscribe: (listener: () => void) => {globalSubscribes++; return baseRegistry.subscribe(listener);},
    subscribeRegion: (id: string, listener: () => void) => {scopedSubscribes++; return baseRegistry.subscribeRegion(id, listener);},
  };
  const tvVertical = {scrollRef: {}, offset: {value: 0}, scrollStyle: {}, repeating: false, stop() {}, move() {}, anchor() {}};
  const {useTVHome} = await screenModule(new URL('../src-next/pages/shared/home/useTVHome.ts', import.meta.url), {
    react,
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}},
    'react-native-reanimated': {
      runOnJS: (fn: unknown) => fn,
      useAnimatedScrollHandler: (fn: unknown) => fn,
      useSharedValue: (initial: unknown) => {
        const ref = React.useRef<any>(undefined);
        if (!ref.current) {
          let current = initial;
          ref.current = {
            get value() {return current;},
            set value(next: unknown) {if (Array.isArray(next)) scrollBoxesWrites++; current = next;},
          };
        }
        return ref.current;
      },
    },
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/home-spotlight': {homeRowPresentation, homeSpotlightModels, homeTitleFactParts: () => []},
    '../../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel},
    '../../../kit/focus': {useFocusRegistry: () => registry},
    '../../../kit/focus/initial-entry': {createInitialRegionEntry},
    '../../../kit/home': {homeRowMetrics: () => ({itemWidth: 210, itemHeight: 315, captionHeight: 54, estimate: 420, gap: 40})},
    '../../../kit/layout/FocusAnchor': {useFocusAnchor: () => tvVertical},
    '../../../kit/states': {skeletonTiming: {delayMs: 300, slowMs: 3000}},
    '../../../kit/theme': {useTheme: () => theme},
    '../../../shell/home-read-log': {noteHomeRead() {}},
    '../../../shell/boot-timing': {logBootMilestone() {}},
    '../../../shell/model': {sameAuthority},
    '../../../shell/home-paging': {NativeHomePaging},
    '../../../shell/tv-focus': {tvFocusIds, tvHomeRegionMap},
    '../../../screens/destinations/useTVPageBack': {useTVPageBack() {}},
    '../../../screens/home/home-start': homeStart,
    '../../../screens/home/home-window': homeWindow,
    '../../../screens/home/live-expiry': liveExpiry,
    '../../../screens/home/model': homeModel,
    '../../../screens/home/tv-presentation': {tvHomeRows},
    '../../../screens/home/useTvHomeRecovery': {useTvHomeRecovery: () => ({visible: false})},
  });
  const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};
  const e1 = {id: 'm1', kind: 'movie', libraryId: 'movies', title: 'Movie 1'};
  const e2 = {id: 'm2', kind: 'movie', libraryId: 'movies', title: 'Movie 2'};
  const document = {
    serverId: 's', viewerFence: 'v',
    rows: [{id: 'new_movies', kind: 'new', artworkShape: 'poster', title: 'New movies', entries: [e1, e2], nextCursor: ''}],
    spotlight: [],
  };
  const homeSnapshot = {document, phase: 'ready'};
  const runtime: any = {
    home: {subscribe: () => () => {}, getSnapshot: () => homeSnapshot, load() {}, demand() {}, more() {}, revalidate: async () => {}, retry: async () => {}},
    subscribe: () => () => {},
    getSnapshot: () => ({authority, libraries: [], librariesUnavailable: false, selectionActive: false}),
    text: defaultI18n, setHomeWindow() {}, reportError() {},
    actions: {play() {}, myList: async () => {}, customize() {}, seeAll() {}, navigate() {}, more() {}},
  };
  const ids = tvFocusIds(authority);
  const rowRegionId = ids.row('new_movies');
  baseRegistry.configure({id: rowRegionId, order: [ids.entry(e1 as any), ids.entry(e2 as any)]});
  baseRegistry.attach({regionId: rowRegionId, childId: ids.entry(e1 as any)}, {requestTVFocus() {}});
  baseRegistry.attach({regionId: rowRegionId, childId: ids.entry(e2 as any)}, {requestTVFocus() {}});
  baseRegistry.didFocus({regionId: rowRegionId, childId: ids.entry(e1 as any)});

  let latest: any;
  const previousFrame = globalThis.requestAnimationFrame;
  globalThis.requestAnimationFrame = (callback => {callback(0); return 1;}) as typeof requestAnimationFrame;
  const host = createFabricHookHost(() => {latest = useTVHome({runtime, authority});});
  try {
    await act(() => host.mount());
    assert.ok(scopedSubscribes >= 2, 'ownership subscribes per Home region via subscribeRegion');
    const globalAfterMount = globalSubscribes;
    const writesAfterMount = scrollBoxesWrites;
    const initialRowModels = latest.rowModels;
    const initialRowBoxes = latest.rowBoxes;
    const initialStartModel = latest.startModel;
    const initialPreview = latest.preview;

    // Simulate moving focus to card 2 in the same row.
    await act(() => {
      baseRegistry.didFocus({regionId: rowRegionId, childId: ids.entry(e2 as any)});
      latest.rowModel(0).onItemFocus(e2.id);
    });
    assert.equal(latest.rowModels, initialRowModels, 'rowModels stay memoized across intra-row D-pad moves');
    assert.equal(latest.rowBoxes, initialRowBoxes, 'rowBoxes stay memoized across intra-row D-pad moves');
    assert.equal(latest.startModel, initialStartModel, 'startModel stays memoized across intra-row D-pad moves');
    assert.equal(scrollBoxesWrites, writesAfterMount, 'scrollBoxes layout effect does not re-run on D-pad moves');
    assert.equal(globalSubscribes, globalAfterMount, 'initialEntry effect does not resubscribe registry.subscribe on D-pad moves');
    assert.notEqual(latest.preview, initialPreview, 'preview updates to the newly focused card');
    assert.equal(latest.preview.id, 'm2');
  } finally {
    await act(() => host.unmount());
    globalThis.requestAnimationFrame = previousFrame;
  }
});

test('TVSearchSection is React.memo and TVSearchResults skips viewport re-renders in all-mode and micro-scrolls', async () => {
  const react = {...React, default: React};
  const theme = createTheme({isTV: true, width: 1920});
  const {TVSearchSection} = await screenModule(new URL('../src-next/kit/search/TVSearchSection.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/search-model': {searchGroupLabel},
    '../controls': {Focusable: 'Focusable'},
    '../focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useCovered: () => false, useFocusRegistry: () => createFocusRegistry()},
    '../focus/replacement-refocus': {useReplacementRefocus() {}},
    '../focus/page-boundary': {useRowPageBoundary() {}},
    '../states': {LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../theme': {useTheme: () => theme},
    '../layout/Row': {Row: 'Row'},
    '../../tv/layout/TVLoadedGrid': {TVLoadedGrid: 'TVLoadedGrid'},
    '../../screens/search/model': {nativeSearchEntryKey, nativeSearchTailKey},
    './SearchGridCard': {SearchGridCard: 'SearchGridCard'},
  });
  assert.equal((TVSearchSection as any).$$typeof, Symbol.for('react.memo'), 'TVSearchSection is wrapped in React.memo');

  let renders = 0;
  const nextCalls: string[] = [];
  const SectionProbe = React.memo(function SectionProbe(_props: any) {renders++; return null;});
  const {TVSearchResults} = await screenModule(new URL('../src-next/kit/search/TVSearchContent.tsx', import.meta.url), {
    react, 'react-native': {ScrollView: 'ScrollView', View: 'View', Text: 'Text'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({caption: [], detail: []})},
    '../../../../../packages/client-core/src/presentation/search-model': {searchGroupLabel, topResultKindLabel},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../artwork': {Artwork: 'Artwork'}, '../controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'},
    '../cards/primitives': {Parts: 'Parts', Progress: 'Progress'},
    '../focus': {FocusRegion: 'FocusRegion'}, '../Icon': {Icon: 'Icon'}, './Search': {TopResult: 'TopResult'},
    '../layout/TVRow': {TVRow: ({children}: any) => children},
    '../layout/ListEdgeFade': {useWholeItems: () => ({scroll: {}, container: {}, pad: 0, focus() {}, item: () => ({})})},
    '../states': {StateView: 'StateView'}, '../browse/presentation': {alpha: () => ''},
    '../theme': {useTheme: () => theme},
    '../frames/metrics': {useFrameMetrics: () => ({width: 1140, rightGutter: 96, bottomClear: 72, sectionGap: 44, headingGap: 22, cardRadius: 14})},
    '../../screens/destinations/focus': {nativeDestinationFocusIds},
    '../../screens/home/model': {homeSubject: (v: unknown) => v},
    '../../screens/search/model': {nativeSearchChipCount, nativeSearchEntryKey},
    '../../screens/search/genre-tiles': {nativeSearchGenreTiles},
    './TVSearchSection': {TVSearchSection: SectionProbe},
  });
  const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};
  const ids = nativeDestinationFocusIds(authority);
  const movie = {id: 'm1', kind: 'movie', libraryId: 'movies', title: 'Movie 1'};
  const groups = [{id: 'movies', status: 'success', items: [movie], totalCount: 10, totalCountExact: true, nextCursor: 'c2'}];
  let currentGroup = 'all';
  const makeProps = () => ({
    destination: {next: async (id: string) => {nextCalls.push(id);}, retry: async () => {}, selectGroup() {}, useRecent() {}},
    runtime: {text: defaultI18n, artwork: () => undefined, reportError(error: unknown) {throw error;}, error: () => ({})},
    explore: {load: async () => {}, openGenre() {}, openAll() {}},
    actions: {}, ids, keyboard: ids.searchKeyboard, scroll: {current: null},
    view: {
      t: defaultI18n.t,
      state: {text: 'mov', group: currentGroup, groups, search: {phase: 'ready'}, recent: [], paging: {}},
      landing: {phase: 'ready', genres: []}, active: {visible: true}, typed: true, empty: false, emptyAll: false, emptyIn: undefined,
      top: undefined, groups, problem: undefined, chips: [], chipsDrawn: false,
    },
  });
  let tree: any, resultsRenders = 0;
  const Wrapper = () => {
    resultsRenders++;
    return React.createElement(TVSearchResults, makeProps());
  };
  // Use hooks host for TVSearchResults directly so we can drive ScrollView.onScroll with real React state.
  let scrollView: any;
  const Direct = () => {
    const props = makeProps();
    tree = (TVSearchResults as any).type(props);
    scrollView = nodes(tree).find(node => node.type === 'ScrollView');
    return null;
  };
  const host = createFabricHookHost(() => Direct());
  try {
    await act(() => host.mount());
    const initialRenders = resultsRenders;
    void Wrapper; void initialRenders; void renders;
    const section = () => nodes(tree).find(node => node.type === SectionProbe);
    assert.equal(section().props.scrollY, 0);
    // In 'all' mode without songs, scrolling does not change viewport.y at all.
    await act(() => {
      scrollView.props.onScroll({nativeEvent: {contentOffset: {y: 350}, contentSize: {height: 2000}, layoutMeasurement: {height: 900}}});
    });
    assert.equal(section().props.scrollY, 0, 'all-mode horizontal sections keep scrollY at 0');

    // In single-group mode, micro-scrolls within a 120px bucket do not change quantized scrollY,
    // while crossing a 120px bucket updates scrollY and near-end pagination still uses exact y.
    currentGroup = 'movies';
    await act(() => host.rerender());
    await act(() => {
      scrollView.props.onScroll({nativeEvent: {contentOffset: {y: 45}, contentSize: {height: 2000}, layoutMeasurement: {height: 900}}});
    });
    assert.equal(section().props.scrollY, 0, 'micro-scroll under 120px stays in bucket 0');
    await act(() => {
      scrollView.props.onScroll({nativeEvent: {contentOffset: {y: 165}, contentSize: {height: 2000}, layoutMeasurement: {height: 900}}});
    });
    assert.equal(section().props.scrollY, 120, 'crossing 120px updates quantized scrollY to 120');
    await act(() => {
      scrollView.props.onScroll({nativeEvent: {contentOffset: {y: 550}, contentSize: {height: 2000}, layoutMeasurement: {height: 900}}});
    });
    assert.deepEqual(nextCalls, ['movies'], 'near-end pagination still fires using exact contentOffset.y');
  } finally {
    await act(() => host.unmount());
  }
});

test('Row skips registry.subscribeRegion on non-TV platforms', async () => {
  const h = hooks();
  let subscribed = false;
  const react = {
    ...h.react,
    useLayoutEffect: () => {},
    useId: () => 'id',
    useSyncExternalStore: (sub: any, get: any) => {const off = sub(() => {}); if (typeof off === 'function') off(); subscribed = true; return get();},
  };
  const theme = createTheme({isTV: false, width: 390});
  let regionSubscribes = 0;
  const registry = {
    subscribeRegion: () => {regionSubscribes++; return () => {};},
    regionSnapshot: () => 0,
    isRegionActive: () => false,
    remembered: () => undefined,
    recovery: () => [],
  };
  const {Row} = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {
    react, 'react-native': {Pressable: 'Pressable', Text: 'Text', View: 'View'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (v: unknown) => ({value: v})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme},
    '../focus': {FocusProvider: 'FocusProvider', FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', focusTestID: () => 'id', useCovered: () => false, useOptionalFocusRegistry: () => registry},
    './row-state': rowState, '../focus/page-boundary': {useRowPageBoundary() {}},
    './FocusAnchor': {useFocusAnchor: () => ({scrollRef: {}, offset: {value: 0}, scrollStyle: {}, repeating: false, stop() {}, move() {}, anchor() {}})},
    './model': layoutModel, './MotionItem': {MotionItem: 'MotionItem'}, './types': layoutTypes,
  });
  h.render(() => {
    const child = Row({title: 'Shelf', items: [{id: 'a'}], getKey: (item: any) => item.id, renderItem: () => null, itemWidth: 140, itemHeight: 210,
      selection: {active: false, isSelected: () => false, onToggle() {}, onEnter() {}}, focus: {id: 'shelf.row'}}).props.children[0];
    return child.type(child.props);
  });
  assert.equal(subscribed, true);
  assert.equal(regionSubscribes, 0, 'non-TV Row never subscribes to registry.subscribeRegion');
});
