import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const flat = (node: any): any[] => !node || typeof node !== 'object' ? []
  : Array.isArray(node) ? node.flatMap(flat) : [node, ...flat(node.props?.children), ...flat(node.props?.toolbar), ...flat(node.props?.grid)];

test('touch AlbumPage renders ChoiceChips for multi-disc albums', async () => {
  const discs = [{id: '1', label: 'Disc 1', selected: true, onPress: () => {}}, {id: '2', label: 'Disc 2', selected: false, onPress: () => {}}];
  const {AlbumPage} = await screenModule(new URL('../src-next/pages/touch/music/AlbumPage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork', missingArt: () => null},
    '../../../kit/frames': {ChoiceChips: 'ChoiceChips', DetailsAside: 'DetailsAside', musicScroller: () => ({}), PanelBlock: 'PanelBlock', StateRow: 'StateRow', TitleFrame: 'TitleFrame'},
    '../../../kit/layout': {HeroMetadata: 'HeroMetadata', PosterRow: 'PosterRow'},
    '../../../kit/music/MusicFormatBadges': {MusicFormatBadges: 'MusicFormatBadges'},
    '../../../kit/music/MusicSectionControls': {MusicSectionControls: 'MusicSectionControls'},
    '../../../kit/music/TrackWindow': {TrackWindow: 'TrackWindow'},
    '../../shared/music/useAlbum': {useAlbum: () => ({title: 'Double Album', facts: [], badges: [], play() {}, shuffle() {}, more() {}, favoriteAction: {icon: 'i-heart', label: 'Fav', onPress() {}}, discs,
      songs: {rows: [], section: {}, next() {}, previous() {}, retry() {}, refresh() {}, onPlay() {}, onNearEnd() {}, onFocusRow() {}, onBlurRow() {}}})},
  });
  const tree = AlbumPage({onBack() {}});
  const chips = flat(tree).find(node => node.type === 'ChoiceChips');
  assert.ok(chips, 'ChoiceChips rendered for multi-disc album');
  assert.equal(chips.props.items, discs);
});

test('createNativeTitleSelection enables selection on ready movie pages when workspace is idle', async () => {
  let capturedSource: any;
  const {createNativeTitleSelection} = await screenModule(new URL('../src-next/pages/shared/title/title-selection.ts', import.meta.url), {
    react: {...React, default: React},
    '../../../shell/native-selection': {createNativeSelection: (_ctx: unknown, source: unknown) => {capturedSource = source; return {};}},
  });
  const context = {current: () => true, session: () => ({viewer: {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p'}})};
  const controller = {subscribe: () => () => {}, getSnapshot: () => ({retired: false, workspace: {phase: 'idle', data: null},
    detail: {phase: 'ready', data: {scope: {libraryId: 'movies'}, item: {id: 'm1'}, related: {rows: [{id: 'r1', entries: [{id: 'm2', kind: 'movie', title: 'Other'}]}]}}},
    recommendationRows: []})};
  createNativeTitleSelection(context, controller, 'movie:m1');
  const snapshot = capturedSource.getSnapshot();
  assert.ok(snapshot.context, 'movie page with ready detail supplies a SelectionContext');
  assert.equal(snapshot.groups.length, 1);
  assert.equal(snapshot.groups[0].id, 'title-related:r1');
});

test('useHomeRowFilters allows applying filters for lower-bound counts and unavailable preview counts', async () => {
  const h = hooks();
  const applied: unknown[] = [];
  let closed = 0;
  const t = (key: string, values?: any) => values ? `${key}:${JSON.stringify(values)}` : key;
  const {useHomeRowFilters} = await screenModule(new URL('../src-next/pages/shared/home/useHomeRowFilters.ts', import.meta.url), {
    react: h.react,
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/browse-toolbar': {
      activeRangePreset: () => undefined, browseValueLabel: String, filterControlOf: () => 'values', filterValuesField: (f: any) => ({field: f.id, facet: f.id}), orderFilterValues: (f: any, v: any) => v, rangePresets: () => [],
    },
    '../../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel: () => ({groups: []})},
    '../../../kit/theme': {useTheme: () => ({isTV: false})},
    '../../../screens/home/row-filters': {nativeRowFilterChange: () => [], nativeRowFilterField: () => undefined, nativeRowFilterValues: () => ({})},
    '../../../screens/home/browse-label': {homeBrowseLabel: (v: any) => v.id},
  });
  const controller = {
    rowId: 'row-1', subscribe: () => () => {}, getSnapshot: () => ({filters: [], capabilities: {library: {kind: 'movies'}, fields: []}, sort: {field: 'title', direction: 'asc'}, selectedKind: undefined}),
    previewCounts: async () => [], facets: async () => ({values: [], total: 0}),
    setFilters: (draft: unknown) => {applied.push(draft); return true;},
  };
  const runtime = {text: {t, locale: 'en-US'}, getSnapshot: () => ({account: {viewerRole: 'member'}}), reportError() {}, error: () => ({body: 'err'})};
  const fallbackView = h.render(() => useHomeRowFilters({controller, runtime, onClose: () => {closed++;}}));
  assert.equal(fallbackView.showResults.label, 'action.done');
  fallbackView.showResults.onPress();
  assert.equal(applied.length, 1);
  assert.equal(closed, 1);
});

test('SeeAllPage passes onClearFilters to BrowseToolbar and resets BrowseGrid key when shape changes', async () => {
  let currentView = 'list';
  const clear = () => {};
  const {SeeAllPage} = await screenModule(new URL('../src-next/pages/touch/library/SeeAllPage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../kit/frames': {BrowseFrame: 'BrowseFrame'},
    '../../../kit/frames/BrowseGrid': {BrowseGrid: 'BrowseGrid'},
    '../../../kit/browse': {BrowseToolbar: 'BrowseToolbar', FilteredEmpty: 'FilteredEmpty'},
    '../../../kit/browse/HomeRowCard': {HomeRowCard: 'HomeRowCard'},
    '../../../kit/browse/LibraryListRow': {LibraryListRow: 'LibraryListRow'},
    '../../../kit/controls': {Button: 'Button', Chip: 'Chip'},
    '../../../kit/artwork': {AmbientArt: 'AmbientArt'},
    '../../../kit/states': {StateView: 'StateView'},
    '../../shared/library/useSeeAll': {useSeeAll: () => ({
      t: (k: string) => k, title: 'Row', backLabel: 'Home', ready: true, slow: false, model: {}, labels: {}, total: 10,
      applied: [], remove() {}, clear, view: currentView, setView() {}, kinds: [], square: false, squareCard: () => false,
      onQuickChange() {}, playable: false, empty: false, isCurrent: () => true,
    })},
  });
  const props = {controller: {setSort() {}, collection: {}}, runtime: {appearanceCardSizePercent: 100}, active: true, onBack() {}, onPlay() {}, onOpen() {}, onPlayEntry() {}, onFilters() {}, onReshuffle() {}, kindLabel: String};
  const listFrame = SeeAllPage(props);
  assert.equal(listFrame.props.toolbar.props.onClearFilters, clear);
  const listKey = listFrame.props.grid.key;
  currentView = 'grid';
  const gridFrame = SeeAllPage(props);
  const gridKey = gridFrame.props.grid.key;
  assert.notEqual(listKey, gridKey, 'switching List to Grid changes BrowseGrid key');
});

test('SearchPage deduplicates topEntry by nativeSearchEntryKey even across distinct object references', async () => {
  const h = hooks();
  const topCopy = {id: 'm1', kind: 'movie', title: 'Dune'};
  const groupCopy = {id: 'm1', kind: 'movie', title: 'Dune'};
  const other = {id: 'm2', kind: 'movie', title: 'Arrival'};
  const {SearchPage} = await screenModule(new URL('../src-next/pages/touch/search/SearchPage.tsx', import.meta.url), {
    react: h.react,
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/search-model': {searchGroupLabel: () => 'movies', topResultKindLabel: () => 'movie'},
    '../../../kit/artwork': {Artwork: 'Artwork'},
    '../../../kit/cards/primitives': {Parts: 'Parts'},
    '../../../kit/controls': {Button: 'Button', Chip: 'Chip', TextField: 'TextField'},
    '../../../kit/frames': {SearchFrame: 'SearchFrame'},
    '../../../kit/frames/metrics': {useFrameMetrics: () => ({form: 'phone'})},
    '../../../kit/overlays': {Banner: 'Banner'},
    '../../../kit/search': {SearchCancel: 'SearchCancel', SearchClear: 'SearchClear', SearchHistory: 'SearchHistory', TopResult: 'TopResult'},
    '../../../kit/search/SearchResults': {SearchGenres: 'SearchGenres', SearchLoading: 'SearchLoading', SearchOnNow: 'SearchOnNow', SearchResultRow: 'SearchResultRow', SearchResultsGrid: 'SearchResultsGrid', SearchResultShelf: 'SearchResultShelf', SearchSection: 'SearchSection'},
    '../../../kit/states': {StateView: 'StateView'},
    '../../../screens/search/genre-tiles': {nativeSearchGenreTiles: () => []},
    '../../../screens/search/model': {nativeSearchChipCount: () => 2, nativeSearchEntryKey: (g: string, e: any) => JSON.stringify([g, e.kind, e.id])},
    '../../shared/search/useSearch': {useSearch: () => ({
      active: {visible: true}, state: {text: 'dune', group: 'all', search: {phase: 'ready'}, groups: [], recent: [], clearing: false},
      typed: true, emptyAll: false, emptyIn: undefined, problem: undefined,
      groups: [{id: 'movies', items: [groupCopy, other], totalCount: 2, totalCountExact: true, status: 'success'}],
      top: {group: 'movies', entry: topCopy}, chips: [], chipsDrawn: false, landing: {phase: 'ready', genres: []}, live: {phase: 'idle', cards: []}, liveNow: 0, showLive: false,
      t: (k: string) => k,
    })},
  });
  const tree = h.render(() => SearchPage({destination: {}, runtime: {artwork: () => undefined, registerScrollToTop: () => () => {}, reportError() {}}, explore: {load: async () => {}}, actions: {open() {}, play() {}, menu() {}}}));
  const rows = flat(tree).filter(node => node.type === 'SearchResultRow');
  assert.deepEqual(rows.map(row => row.props.entry.id), ['m2'], 'topEntry is deduplicated by key, not object identity');
});

test('ReadingSettingsPage uses runtime.text and omits unsupported registry fields without throwing', async () => {
  const {ReadingSettingsPage} = await screenModule(new URL('../src-next/pages/touch/settings/ReadingSettingsPage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/client-core/src/presentation/settings-bindings': {readingSettingsProperty: (id: string) => id === 'known' ? 'fontSize' : id === 'missingField' ? 'fontFamily' : undefined, changedFromDefault: () => false},
    '../../../../../../packages/client-core/src/preference-effects': {readingPreferencesPatch: () => ({})},
    '../../../kit/controls': {Button: 'Button', Segmented: 'Segmented', Select: 'Select', Switch: 'Switch'},
    '../../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame'},
    '../../../kit/shell': {DefaultHint: 'DefaultHint', SettingsRow: 'SettingsRow'},
    '../../../kit/states': {StateView: 'StateView'},
    '../../../screens/settings/reading-preferences': {nativeReadingSettingsSection: {title: 'reading.title'}},
    '../../shared/settings/useReadingSettings': {useReadingSettings: () => ({
      state: {busy: false, preferences: {registry: {fields: [{key: 'reading.fontSize', default: 'medium'}]}}},
      values: {fontSize: 'medium'}, advanced: false, setAdvanced() {}, change() {},
      groups: [{title: 'group.main', rows: [
        {id: 'known', key: 'reading.fontSize', label: 'reading.size', control: {type: 'select', options: [{value: 'medium', label: 'size.medium'}]}},
        {id: 'missingField', key: 'reading.fontFamily', label: 'reading.font', control: {type: 'select', options: []}},
        {id: 'unknownBinding', key: 'reading.other', label: 'reading.other', control: {type: 'toggle'}},
      ]}],
    })},
  });
  const tree = ReadingSettingsPage({
    source: {rowEnabled: () => true, refresh: async () => {}},
    runtime: {text: {t: (key: string) => `LOC:${key}`}, error: () => ({})},
    current: () => true,
    onBack() {},
  });
  assert.equal(tree.props.title, 'LOC:reading.title');
  const rows = flat(tree).filter(node => node.type === 'SettingsRow');
  assert.deepEqual(rows.map(row => row.props.testID), ['reading-settings-row-known']);
  assert.equal(rows[0].props.first, true);
});

test('settings-route does not add duplicate subscribePreferences and disposes settings controller', async () => {
  let prefSubscribes = 0, settingsDisposed = 0, dashboardDisposed = 0, lifetimeDisposed = 0;
  const {createNativeSettingsRenderer} = await screenModule(new URL('../src-next/shell/settings-route.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Platform: {isTV: true}},
    '../platform/event-resync': {readEventState: (_e: unknown, _s: unknown, fn: () => unknown) => fn()},
    '../../../../packages/client-core/src/admin/server-now-playing': {readServerNowPlaying: async () => ({items: [], totals: {}})},
    '../../../../packages/client-core/src/admin/dashboard': {DashboardDataClient: class {}},
    '../../../../packages/client-core/src/server-admin/server-storage': {readServerStorage: async () => ({})},
    '../screens/settings-live/lifetime': {NativeScreenLifetime: class {authority = {serverId: 's'}; signal = new AbortController().signal; current() {return true;} assert() {} dispose() {lifetimeDisposed++;}}},
    '../screens/settings-live/settings-controller': {createBoundNativeSettingsController: () => ({accept() {}, dispose() {settingsDisposed++;}})},
    '../screens/settings-live/device-preference-consumer': {nativeDevicePreferences: () => ({})},
    '../screens/settings-live/dashboard-controller': {NativeDashboardController: class {dispose() {dashboardDisposed++;}}},
    '../screens/settings-live/server/tv-server-reader': {nativeServerAdmission: () => 'denied'},
    '../pages/tv/settings/ServerSettingsPage': {ServerSettingsPage: 'ServerSettingsPage'},
    '../pages/shared/settings/tv-server-pages': {isTVServerPage: () => false},
    '../pages/shared/settings/EqualizerSettingsPage': {EqualizerSettingsPage: 'EqualizerSettingsPage'},
    '../pages/shared/settings/SettingsPage': {NativeSettingsPage: 'NativeSettingsPage'},
    '../pages/touch/dashboard/ServerDashboardPage': {ServerDashboardPage: 'ServerDashboardPage'},
    '../screens/settings-live/settings-ports': {createNativeSettingsPorts: () => ({}), nativeSettingsRequestId: () => 'id'},
    '../screens/settings-live/downloads-summary': {createNativeSettingsDownloads: () => ({dispose() {}})},
  });
  const renderer = createNativeSettingsRenderer({
    subscribePreferences: () => {prefSubscribes++; return () => {};},
    navigation: {open() {}}, authority: () => ({}), api: {}, events: {}, account: {},
  });
  assert.equal(prefSubscribes, 0, 'settings-route relies on createBoundNativeSettingsController subscription without duplicating it');
  renderer.dispose();
  assert.equal(settingsDisposed, 1, 'settings.dispose() is called');
  assert.equal(dashboardDisposed, 1);
  assert.equal(lifetimeDisposed, 1);
});

test('useDownloadChoice marks container downloads as checking while options load and uses context locale', async () => {
  const h = hooks();
  const {useDownloadChoice} = await screenModule(new URL('../src-next/pages/shared/downloads/useDownloadChoice.ts', import.meta.url), {
    react: {...h.react, useLayoutEffect: (fn: () => void) => fn()},
    'react-native': {NativeModules: {}, Platform: {OS: 'ios'}},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/downloads': {downloadBytes: String, downloadReasonMessage: String},
    '../../../kit/overlays/SurfacePaintHost': {overlayPaintPermit: () => undefined},
    '../../../screens/downloads/device': {createNativeDownloadsDevice: () => ({
      device: {subscribe: () => () => {}, getSnapshot: () => ({}), open: async () => {}},
      service: {containerOptions: () => new Promise(() => {})},
      dispose() {},
    })},
    '../../../platform/device-settings': {nativeDeviceSettings: () => ({read: () => 'automatic'})},
    '../../../screens/downloads/original-opens': {originalMayNotPlay: () => false},
    '../../../screens/downloads/request': {closeNativeDownload() {}, subscribeNativeDownload: () => () => {}},
    '../../../screens/downloads/download-opening': {createNativeDownloadOpening: () => ({
      current: () => true, close() {}, retire() {}, signal: new AbortController().signal, paintCurrent: () => true,
      claim: () => 't1', owns: () => true, release() {}, viewDownloads() {},
    })},
    '../../../platform/abort-signals': {linkNativeAbortSignals: () => ({signal: new AbortController().signal, dispose() {}})},
    '../../../screens/downloads/download-sheet-facts': {containerDownloadBytes: () => 100, downloadSheetMeaning: () => undefined},
    '../../../screens/downloads/presentation': {downloadRequestLines: () => []},
    '../../../screens/downloads/download-attempt': {createDownloadAttempts: () => ({operationFor: () => 'op', settle() {}}), isDownloadQualityRequired: () => false},
    '../../../screens/settings/runtime-region': {nativeRuntimeRegionText: () => ({t: (key: string) => `CTX:${key}`, locale: 'fr-FR'})},
    '../../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(16)}},
  });
  const context = {
    current: () => true,
    preferences: () => ({registry: {fields: []}}),
    subscribePreferences: () => () => {},
    navigation: {subscribe: () => () => {}},
    subscribeAuthority: () => () => {},
    session: () => ({deviceId: 'dev-1'}),
  };
  const request = {title: 'Season 1', container: {kind: 'season', id: 's1'}};
  // First pass runs useLayoutEffect and sets opening; second pass reads opening.deviceId.
  h.render(() => useDownloadChoice({context, request}));
  const view = h.render(() => useDownloadChoice({context, request}));
  assert.equal(view.checking, true, 'container download with deviceId is checking until choices arrive');
  assert.equal(view.t('action.download'), 'CTX:action.download', 'uses context-derived locale translator');
});
