import * as tvKeyAdapter from '../src-next/platform/tv-key-adapter.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import * as seeAllModel from '../src-next/screens/home/see-all-model.ts';
import {nativeWatchMenuGroups} from '../src-next/screens/watch/menu-model.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flat)) : style ?? {};
const t = (key: string) => key;
const theme = {isTV: false, platform: 'phone', layoutMode: 'phone', gutter: 20, textScale: 1,
  colors: {ink: '#070f17', text: '#edf5fa', textSecondary: '#aaa', textTertiary: '#777', line: '#333'},
  type: {caption: {}, body: {}}, spec: {card: {width: {poster: {phone: 118, tablet: 140}}}}};
const type = (value: any, scale = 1) => ({fontSize: value.size * scale, fontWeight: String(value.weight), lineHeight: value.lineHeight});

/** Explicit shallow element seams; no claim to emulate React Native layout. */
function host() {
  let owner = hooks();
  const react: any = {...owner.react};
  for (const key of ['useRef', 'useState', 'useMemo', 'useCallback', 'useEffect', 'useSyncExternalStore']) react[key] = (...args: any[]) => (owner.react as any)[key](...args);
  react.useLayoutEffect = react.useEffect; react.useId = () => 'surface'; react.default = react;
  return {react, render: (run: () => any) => {owner = hooks(); return owner.render(run);}};
}

test('expanded Search shelves keep every loaded result and prefetch ten cards before the end', async () => {
  const h = host(), calls: string[] = [];
  const entries = Array.from({length: 40}, (_, index) => ({id: String(index), kind: 'movie', title: String(index)}));
  const group = {id: 'movies', items: entries, status: 'success', totalCount: 100, totalCountExact: true, nextCursor: 'actual-page-2'};
  const snapshot: any = {text: 'query', group: 'all', groups: [group], recent: [], paging: {}, search: {phase: 'ready', data: {capabilities: {groups: [{id: 'movies', available: true}]}}}};
  let visible = true;
  const {SearchScreen} = await screenModule(new URL('../src-next/screens/search/SearchScreen.tsx', import.meta.url), {'../../../../../packages/client-core/src/timer': await import('../../../packages/client-core/src/timer.ts'), 
    '../../../../../packages/client-core/src/guide/now-next': {nowNext: () => ({})},
    react: h.react, 'react-native': {FlatList: 'FlatList', ScrollView: 'ScrollView', Text: 'Text', View: 'View', Keyboard: {}, useWindowDimensions: () => ({width: 1200, height: 900})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})},
    '../../../../../packages/client-core/src/presentation/search-model': {orderedSearchGroups: (groups: any) => groups, SEARCH_CHIPS: [], searchGroupLabel: (id: string) => id, topResultKindLabel: t},
    '../../../../../packages/client-core/src/presentation/kit-model': {channelTint: () => '#000'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/cards': {GenreTile: 'GenreTile'}, '../../kit/cards/primitives': {CardFrame: 'CardFrame'},
    '../../kit/controls': {Chip: 'Chip', Focusable: 'Focusable', TextField: 'TextField'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'},
    '../../kit/overlays': {Banner: 'Banner'}, '../../kit/states': {GridSkeleton: 'GridSkeleton', LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => ({...theme, platform: 'tablet', layoutMode: 'desktop'})},
    '../home/model': {homeCardText: () => ({}), nativeSearchCardText: () => ({})}, './model': {nativeSearchEntryKey: (id: string, entry: any) => `${id}:${entry.id}`},
    './SearchGridCard': {SearchGridCard: 'SearchGridCard'}, '../../pages/tv/search/SearchPage': {TVSearchPage: 'TVSearchPage'}, './genre-tiles': {nativeSearchGenreTiles: () => []},
  });
  const props = {destination: {subscribe: () => () => {}, getSnapshot: () => snapshot, setText() {}, next: async (id: string) => {calls.push(id);}, retryGroup: async () => {calls.push('retry');}},
    runtime: {text: {t}, reportError: (error: unknown) => {throw error;}, error: (_error: unknown, retry: () => void) => ({primaryAction: {onPress: retry}})},
    actions: {}, activation: {subscribe: () => () => {}, getSnapshot: () => ({visible})}, explore: {subscribe: () => () => {}, getSnapshot: () => ({phase: 'idle'})}, tvCopy: {}};
  const shelf = () => nodes(h.render(() => SearchScreen(props))).find(node => node.props.testID === 'search-shelf-movies');
  const first = shelf();
  assert.equal(first.props.data, entries); assert.equal(first.props.data.length, 40);
  const pitch = first.props.getItemLayout(undefined, 1).length;
  assert.equal(first.props.onEndReachedThreshold * 1200, pitch * 10);
  assert.equal(first.props.renderItem({item: entries[39]}).props.entry, entries[39]);
  first.props.onEndReached(); assert.deepEqual(calls, ['movies']);
  snapshot.paging.movies = {loading: true}; const pending = shelf(); pending.props.onEndReached();
  assert.equal(pending.props.ListFooterComponent.type, 'LoadingPaint'); assert.equal(calls.length, 1);
  snapshot.paging.movies = {loading: false, error: new Error('page failed')}; const failed = shelf(); failed.props.onEndReached();
  assert.equal(calls.length, 1, 'a failing cursor is not automatically retried in a loop');
  failed.props.ListFooterComponent.props.primaryAction.onPress(); assert.equal(calls.at(-1), 'retry');
  snapshot.paging.movies = {}; visible = false; shelf().props.onEndReached(); assert.equal(calls.length, 2);
});

test('phone See all draws on the library frame: its header regions, one toolbar row with the kinds in it, the three-column grid', async () => {
  const h = host(), selected: unknown[] = [];
  const row = {id: 'recommended', title: 'Recommended', reason: {text: 'Your actual recommendation reason'}, entries: []};
  const state = {phase: 'ready', row, capabilities: {library: {kind: 'movie'}}, sort: {field: 'groupOrder'}, filters: [], counts: [{id: 'all', total: 72}]};
  const controller = {subscribe: () => () => {}, getSnapshot: () => state, rowId: row.id, kinds: [{id: 'movies'}], setSort() {}, setFilters() {}, selectKind: (id: unknown) => selected.push(id), collection: {subscribe: () => () => {}, getSnapshot: () => ({})}};
  const {HomeSeeAll} = await screenModule(new URL('../src-next/screens/home/HomeSeeAll.tsx', import.meta.url), {
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    react: h.react, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView', useWindowDimensions: () => ({width: 390, height: 844})},
    '../../../../../packages/i18n/src': {defaultI18n: {has: () => false}},
    '../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel: () => ({sorts: [], sort: {}, groups: [], quick: [{id: 'unwatched', active: false, query: {}, label: {text: 'Unwatched'}}]})},
    '../../../../../packages/client-core/src/home-row-browse': await import('../../../packages/client-core/src/home-row-browse'),
    '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_labels: unknown, value: any, fallback: any) => value ?? fallback}, '../../../../../packages/client-core/src/presentation/home-row-reason': {homeRowReasonParts: () => undefined},
    '../../../../../packages/client-core/src/home-selection': {homeEntryCapabilities: () => ({})},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'}, '../../kit/browse': {BrowseToolbar: 'BrowseToolbar', FiltersButton: 'FiltersButton', QuickToggle: 'QuickToggle', SortMenu: 'SortMenu', ViewToggle: 'ViewToggle'}, '../../kit/frames': {BrowseFrame: 'BrowseFrame'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame', Parts: 'Parts'}, '../../kit/cards': {PosterCard: 'PosterCard'}, '../../kit/artwork': {generatedPoster: () => null, StandardBackdrop: 'StandardBackdrop', MediaArt: 'MediaArt', AmbientArt: 'AmbientArt'},
    '../../kit/browse/HomeRowCard': {HomeRowCard: 'HomeRowCard'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => ({subscribe: () => () => {}, getSnapshot: () => 0})},
    '../../kit/layout': {WindowedGrid: 'WindowedGrid'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states': {GridSkeleton: 'GridSkeleton', LoadingBoundary: 'LoadingBoundary', LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../../kit/theme': {nativeType: type, useTheme: () => theme}, '../../shell/model': {authorityKey: () => 'authority'}, './browse-label': {homeBrowseLabel: t}, './see-all-model': seeAllModel,
    './model': {homeCardText: () => ({})}, './row-controller': {},
  });
  const runtime = {text: {t}, getSnapshot: () => ({authority: {}, account: {viewerRole: 'member'}})};
  const root = h.render(() => HomeSeeAll({controller, runtime, active: true, kindLabel: t}));
  const boundary = nodes(root).find(node => node.type === 'LoadingBoundary');
  const ready = nodes(boundary).find(node => typeof node.type === 'function');
  const tree = h.render(() => ready.type(ready.props)), frame = nodes(tree).find(node => node.type === 'BrowseFrame');
  const grid = nodes(frame.props.grid).find(node => node.type === 'WindowedGrid');
  // The kit toolbar's controls carry no focus ids: inside a focus region they would refuse to render (touch has none).
  assert.equal(nodes(tree).some(node => node.type === 'FocusRegion'), false, 'the touch page has no focus region');
  assert.equal(grid.props.itemWidth, 110); assert.equal(grid.props.gap, 10);
  assert.equal(grid.props.header, undefined, 'the frame holds the header; the grid scrolls under it');
  const header = frame.props.header;
  assert.equal(header.title, 'Recommended'); assert.equal(header.back.testID, 'home-see-all-back');
  assert.equal(header.intro, 'Your actual recommendation reason');
  const buttons = nodes(header.actions).filter(node => node.type === 'Button'); assert.equal(buttons.length, 2);
  // The frame halves a phone's action row; a button's own flex inside that half collapsed it under the toolbar (Pixel 6a).
  assert.ok(buttons.every(node => node.props.style === undefined), 'the buttons carry no sizing of their own');
  // One toolbar row (owner: Filters, then Sort, then the kinds and quick filters in a line that scrolls, never four wrapped rows).
  const toolbar = frame.props.toolbar;
  assert.equal(toolbar.type, 'BrowseToolbar'); assert.equal(toolbar.props.resultTotal, 72);
  assert.deepEqual(toolbar.props.model.quick.map((quick: any) => quick.id), ['unwatched']);
  nodes(toolbar.props.children).find(node => node.type === 'Chip' && node.props.selected === false).props.onPress(); assert.deepEqual(selected, ['movies']);
  const skeleton = h.render(() => boundary.props.skeleton.type(boundary.props.skeleton.props));
  const skeletonGrid = nodes(skeleton).find(node => node.type === 'GridSkeleton');
  assert.equal(skeletonGrid.props.cardWidth, grid.props.itemWidth); assert.equal(skeletonGrid.props.columns, 3);
  assert.equal(skeletonGrid.props.gap, grid.props.gap);
});

test('Notifications is the Settings sheet on a phone and a plain page on its layer on a tablet (W4 F14)', async () => {
  const h = host(); let phone = true;
  const {NativeNotificationsScreen} = await screenModule(new URL('../src-next/screens/notifications/NotificationsScreen.tsx', import.meta.url), {'./OwnFeedbackReport': {OwnFeedbackReport: 'OwnFeedbackReport'}, './notification-route': await import('../src-next/screens/notifications/notification-route.ts'), 
    react: h.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34})}, '../../../../../packages/i18n/src': {defaultI18n: {}},
    '../../../../../packages/client-core/src/notifications': {}, '../../kit/controls': {Button: 'Button', Tabs: 'Tabs'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, '../../kit/overlays': {Menu: 'Menu'}, '../../kit/shell': {NotificationItem: 'NotificationItem', SettingsSheet: 'SettingsSheet'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/theme': {useTheme: () => ({...theme, platform: phone ? 'phone' : 'tablet'})},
    '../../shell/account': {}, '../../shell/model': {sameAuthority: (a: any, b: any) => a === b},
    '../../platform/event-resync': {recoverEventState: () => () => {}},
  });
  const feed = {subscribe: () => () => {}, getSnapshot: () => ({})};
  const pending = h.render(() => NativeNotificationsScreen({context: {}, route: {kind: 'notifications'}, runtime: {text: {t}}, profileService: feed, retirement: {subscribe: () => () => {}, getSnapshot: () => undefined}}));
  const sheet = h.render(() => pending.type(pending.props));
  assert.equal(sheet.type, 'SettingsSheet'); assert.equal(sheet.props.open, true);
  assert.equal(sheet.props.surfaceBackground, undefined, 'a sheet is a plain surface: it draws no backdrop of its own');
  assert.equal(sheet.props.style.backgroundColor, undefined);
  assert.deepEqual(sheet.props.detents, [782]);
  assert.equal(sheet.props.style.paddingTop, 0); assert.equal(sheet.props.showGrabber, false);
  assert.equal(sheet.props.headerContent.props.style.height, 56);
  phone = false; const tablet = h.render(() => pending.type(pending.props));
  assert.equal(tablet.type, 'View'); assert.equal(tablet.props.style.paddingTop, 61);
  assert.ok(!JSON.stringify(tablet.props.children).includes('Backdrop'), 'a tablet Notifications page stands on its layer and draws no backdrop of its own');
});

test('the shared touch entry menu composes offered Play, My List, playlist and recommendation actions', async () => {
  const h = host(), calls: string[] = [];
  const value: any = {phase: 'menu', capture: {entry: {id: 'actual', kind: 'movie', title: 'Actual'}},
    detail: {personal: {watchlisted: false}, actions: [{id: 'watchlist', enabled: true}, {id: 'play', enabled: true, playback: {itemId: 'actual'}}]},
    actions: {entry: {play() {}, addToPlaylist() {}}, fileInfo() {}, extraActions: [{id: 'not-interested', label: 'Not interested', onSelect() {}}]}};
  const presenters = {subscribe: () => () => {}, getSnapshot: () => value, overlayCurrent: () => true, current: () => true, togetherActions: () => ({queue: false, watch: false}),
    playEntry: () => calls.push('play'), personal: () => calls.push('watchlist'), addToPlaylist: () => calls.push('playlist'),
    extraEntry: (opening: unknown, id: string) => {assert.equal(opening, value); calls.push(id);}};
  const {NativeWatchOverlays} = await screenModule(new URL('../src-next/screens/watch/WatchOverlays.tsx', import.meta.url), {
    '../../../../../packages/client-core/src/presentation/kit-model/technical': {},
    react: h.react, 'react-native': {Modal: 'Modal', Platform: {isTV: false}, ScrollView: 'ScrollView', StyleSheet: {}, Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../../../../packages/client-core/src/administration': {}, '../../../../../packages/client-core/src/title-identity': {},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {}, '../../shell/model': {}, '../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog', Menu: 'Menu'},
    '../../kit/overlays/Menu': {TouchMenuHeader: 'TouchMenuHeader'}, '../../kit/overlays/surface': {}, '../../kit/overlays/TVPanelMotion': {useTVPanelPresence: () => ({})},
    '../../kit/overlays/title-menu': await screenModule(new URL('../src-next/kit/overlays/title-menu.ts', import.meta.url), {react: h.react, '../../../../../packages/i18n/src': {defaultI18n: {t}}}),
    './menu-model': {nativeWatchMenuGroups},
    '../../kit/overlays/OverlayHost': {}, '../../kit/theme': {nativeType: type, useTheme: () => theme}, './VersionChooser': {}, './DiscTitlesSheet': {}, './FileInfoContent': {}, './file-info': {},
    '../../kit/overlays/SurfacePaintHost': {overlayPaintKey: () => 'opening', overlayPaintPermit: () => ({}), RetainedOverlay: 'RetainedOverlay'},
  });
  const render = () => nodes(h.render(() => NativeWatchOverlays({presenters, runtime: {artwork: () => undefined}, common: {}}))).find(node => node.type === 'Menu');
  const menu = render();
  assert.deepEqual(menu.props.items.filter((item: any) => !item.separator).map((item: any) => item.id), ['play', 'watchlist', 'addToPlaylist', 'file-info', 'not-interested']);
  for (const id of ['play', 'watchlist', 'addToPlaylist', 'not-interested']) menu.props.items.find((item: any) => item.id === id).onSelect();
  assert.deepEqual(calls, ['play', 'watchlist', 'playlist', 'not-interested']);
  value.detail.actions[0].enabled = false; value.detail.actions[1].enabled = false; value.actions.extraActions = [];
  assert.deepEqual(render().props.items.filter((item: any) => !item.separator).map((item: any) => item.id), ['addToPlaylist', 'file-info']);
});

test('the shared toast preserves an explicitly authored TV Undo without changing other TV notices', async () => {
  const h = host(), calls: string[] = [];
  const pan: any = {};
  for (const method of ['enabled', 'minDistance', 'activeOffsetX', 'activeOffsetY', 'onStart', 'onBegin', 'onUpdate', 'onEnd', 'onFinalize']) pan[method] = () => pan;
  const {Toast} = await screenModule(new URL('../src-next/kit/overlays/Feedback.tsx', import.meta.url), {'../useScreenReader': {useScreenReader: () => false}, 
    react: h.react, 'react-native': {AccessibilityInfo: {}, AppState: {}, StyleSheet: {}, Text: 'Text', View: 'View'},
    'react-native-gesture-handler': {Gesture: {Pan: () => pan}, GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useSharedValue: (value: any) => ({value}), useAnimatedStyle: (run: () => any) => run(), runOnJS: (run: any) => run},
    'react-native-safe-area-context': {}, '../../../../../packages/i18n/src/index': {defaultI18n: {t}}, '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false})},
    '../controls/Button': {Button: 'Button'}, '../Icon': {Icon: 'Icon'}, '../theme': {nativeType: type, useTheme: () => ({...theme, isTV: true, form: 'tv'})},
    '../useMotion': {useMotion: () => ({reduce: true, timing: () => ({}), spring: () => ({})})}, './geometry': {}, './toast-clearance': {}, './toast-clock': {}, './surface': {alpha: (value: string) => value},
  });
  const props = {id: 'undo', message: 'Removed', action: {label: 'Undo', onPress: () => calls.push('undo')}, onDismiss: (_id: string, reason: string) => calls.push(reason)};
  assert.equal(nodes(h.render(() => Toast(props))).some(node => node.type === 'Button'), false);
  const tree = h.render(() => Toast({...props, actionOnTV: true}));
  const button = nodes(tree).find(node => node.type === 'Button'); assert.ok(button); button.props.onPress();
  assert.deepEqual(calls, ['undo', 'action']);
  assert.ok(nodes(tree).find(node => node.type === 'AnimatedView').props.accessibilityActions.some((item: any) => item.name === 'activate'));
});

test('a superseded or dismissed See all Undo callback is revoked before the next render', async () => {
  const h = hooks(), react: any = {...h.react, useLayoutEffect() {}}; react.default = react;
  const route = {kind: 'home-see-all', destination: {type: 'row', rowId: 'recommended'}};
  const navigation = {route, revision: 1}, first = {id: 'a', title: 'A'}, second = {id: 'b', title: 'B'};
  const requests: {entry: string; value: boolean; resolve: () => void}[] = [];
  let extra: any;
  class Controller {
    collection = {subscribe: () => () => {}, getSnapshot: () => ({refreshing: false}), residentItems: () => [{entry: first}, {entry: second}]};
    subscribe = () => () => {};
    getSnapshot = () => ({phase: 'ready', row: {id: 'recommended', kind: 'recommendation'}});
    load = async () => {};
    dispose() {}
  }
  const {createNativeHomeRows} = await screenModule(new URL('../src-next/screens/home/SeeAllRoute.tsx', import.meta.url), {...stackSlotFixture, 
    react, 'react-native': {AccessibilityInfo: {}, Platform: {isTV: false}, View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    '@portico/design': {frameForm: () => 'phone'},
    '../../pages/touch/library/SeeAllPage': {SeeAllPage: 'SeeAllPage'}, '../../pages/tv/library/SeeAllPage': {SeeAllPage: 'TVSeeAllPage'},
    '../../kit/focus': {useFocusRegistry: () => ({})}, '../../kit/dock/DockClearance': {useDockClearance: () => 84},
    '../../kit/overlays': {ToastHost: 'ToastHost'}, '../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../../../../packages/client-core/src/recommendation-feedback': {isRecommendationRow: () => true, notInterestedTarget: (entry: any) => entry.id,
      setNotInterested: (_api: unknown, input: any) => new Promise<void>(resolve => requests.push({entry: input.itemId, value: input.notInterested, resolve}))},
    './HomeSeeAll': {HomeSeeAll: 'HomeSeeAll'}, './HomeRowFilters': {HomeRowFilters: 'HomeRowFilters'}, './row-controller': {NativeHomeRowController: Controller},
    '../../platform/server-connections': {nativeRouteCrypto: {random: (length: number) => new Uint8Array(length)}},
    '../../shell/routes': {}, '../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}}, '../../shell/model': {}, '../../kit/theme': {},
    '../read/platform': {}, '../read/reading-books-open': {}, './entry-lifetime': {},
    '../destinations/entry-actions': {createNativeEntryActions: () => ({menu: (_entry: unknown, _anchor: unknown, _select: unknown, extras: any[]) => {extra = extras[0];}, dispose() {}})},
  });
  const context = {current: () => true, authority: () => ({serverId: 'server', viewerFence: 'viewer'}), libraries: () => [], api: {},
    navigation: {retainedRoutes: () => [], getSnapshot: () => navigation, subscribe: () => () => {}}};
  const runtime = {reportError: (error: unknown) => {throw error;}};
  const rows = createNativeHomeRows(context, runtime.reportError); rows.reconcile();
  const retained = rows.render(runtime)[0];
  const render = () => h.render(() => retained.type(retained.props));
  const mark = (tree: any, entry: any) => {nodes(tree).find(node => node.type === 'SeeAllPage').props.onMenu(entry); extra.onSelect();};
  const toast = (tree: any) => nodes(tree).find(node => node.type === 'ToastHost');
  mark(render(), first); requests[0].resolve(); await new Promise(resolve => setImmediate(resolve));
  const firstTree = render(), firstUndo = toast(firstTree).props.toast.action.onPress;
  assert.equal(toast(firstTree).props.toast.durationMs, 8000, 'the shared toast clock keeps Undo for a screen reader');
  assert.equal(toast(firstTree).props.toast.actionOnTV, true);
  mark(firstTree, second);
  firstUndo();
  assert.deepEqual(requests.map(value => [value.entry, value.value]), [['a', true], ['b', true]], 'the queued old callback cannot undo after a new mark begins');
  requests[1].resolve(); await new Promise(resolve => setImmediate(resolve));
  const secondToast = toast(render()), secondUndo = secondToast.props.toast.action.onPress;
  secondToast.props.onDismiss(secondToast.props.toast.id); secondUndo();
  assert.equal(requests.length, 2, 'dismissal also revokes its action synchronously');
  rows.dispose();
});

test('shared menu Play requires the exact live opening and an enabled detail grant', async () => {
  const {NativeWatchPresenters} = await screenModule(new URL('../src-next/screens/watch/presenters.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {Platform: {isTV: false}}, '../../../../../packages/player-core/src': {},
    '../../../../../packages/client-core/src/detail': {}, '../../../../../packages/client-core/src/administration': {},
    '../../../../../packages/client-core/src/search': {}, '../../../../../packages/client-core/src/title-identity': {},
    '../../shell/model': {sameAuthority: (a: unknown, b: unknown) => a === b}, './file-info': {}, './FileInfoPanel': {}, '../../../../../packages/client-core/src/file-info-session': {}, '../settings-live/together-session': {nativeTogetherSession: () => ({subscribe: () => () => {}, getSnapshot: () => ({phase: 'idle'})})},
  });
  const authority = {}, capture = {authority, routeRevision: 1, current: () => true, entry: {id: 'actual'}};
  const subscribe = () => () => {}, calls: string[] = [];
  const context = {current: () => true, authority: () => authority, subscribeAuthority: subscribe,
    navigation: {subscribe, getSnapshot: () => ({authority, revision: 1})}, playback: {subscribe, getSnapshot: () => ({})}};
  const presenter = new NativeWatchPresenters(context, {reportError: (error: unknown) => {throw error;}});
  let grant: any = {actions: [{id: 'play', enabled: true, playback: {itemId: 'actual'}}]};
  const actions = {fileInfo: async () => {}, entry: {current: () => grant, subscribe,
    play: async () => {calls.push('play');}, mutate: async () => {}, dispose() {}}};
  presenter.openMenu(capture, actions); const stale = presenter.getSnapshot();
  presenter.openMenu(capture, actions); const live = presenter.getSnapshot();
  presenter.playEntry(stale); assert.deepEqual(calls, []);
  presenter.playEntry(live); assert.deepEqual(calls, ['play']);
  presenter.playEntry(live); assert.deepEqual(calls, ['play'], 'a double press cannot reuse the closed menu');
  for (const offered of [[], [{id: 'play', enabled: false, playback: {itemId: 'actual'}}], [{id: 'start_over', enabled: true, playback: {itemId: 'actual'}}]]) {
    grant = {actions: offered}; presenter.openMenu(capture, actions); presenter.playEntry(presenter.getSnapshot());
    assert.deepEqual(calls, ['play']); assert.equal(presenter.getSnapshot().phase, 'menu');
  }
  presenter.dispose();
});

test('touch Customize exposes saved-view removal and keeps its normal footer during conflicts', async () => {
  const h = host(), calls: string[] = [], state: any = {phase: 'ready', pending: false, dirty: true, conflict: false,
    rows: [{id: 'view:v', title: 'A view', savedView: {id: 'v'}, reorderable: true, hideable: true, required: false}]};
  const {HomeCustomize} = await screenModule(new URL('../src-next/screens/home/HomeCustomize.tsx', import.meta.url), {'../../platform/tv-key-source': {tvKeySource: 'native'}, '../../platform/tv-key-adapter': tvKeyAdapter, 
    react: h.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-gesture-handler': {Gesture: {Native: () => ({})}, GestureDetector: 'GestureDetector'}, '../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_i18n: unknown, text: unknown, fallback: string) => text ?? fallback}, '../../../../../packages/client-core/src/presentation/home-row-reason': {homeRowReasonParts: () => undefined},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch'}, '../../kit/focus': {useCovered: () => false, useFocusRegistry: () => ({})},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/browse/presentation': {alpha: (value: string) => value},
    '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog', SidePanel: 'SidePanel'}, '../../kit/overlays/OverlayHost': {},
    '../../kit/theme': {nativeType: type, useTheme: () => theme}, './customize-controller': {}, './customize-drag': {},
  });
  const controller = {subscribe: () => () => {}, getSnapshot: () => state, isCurrent: () => false,
    removeSavedView: (id: string) => {calls.push(id); return false;}, keepEditing: () => calls.push('keep'),
    pick: () => false, move: () => calls.push('move'), drop: () => calls.push('drop')};
  const props = {controller, copy: {save: 'Save', conflictTitle: 'Changed', conflictBody: 'Review', reload: 'Reload', keepEditing: 'Keep editing'},
    i18n: {t}, onClose() {}, onAddView() {}, summary: () => 'Real view definition', testID: 'customize'};
  const draw = () => nodes(h.render(() => HomeCustomize(props))).find(node => node.type === 'BottomSheet');
  const ready = draw(), row = nodes(ready).find(node => node.props.testID === 'customize-row-view:v');
  assert.equal(row.props.onPress, undefined, 'a normal touch on the row does not start the old stuck pickup');
  assert.ok(nodes(ready).some(node => node.type === 'Icon' && node.props.name === 'i-filter'));
  const remove = nodes(ready).find(node => node.props.testID === 'customize-remove-view:v'); assert.equal(remove.props.style.width, 44);
  remove.props.onPress(); assert.deepEqual(calls, ['view:v']);
  state.pickedId = 'other'; row.props.onAccessibilityAction({nativeEvent: {actionName: 'increment'}});
  assert.deepEqual(calls, ['view:v'], 'a stale accessibility action cannot move another row after pickup is refused');
  state.pickedId = undefined; state.conflict = true; state.showConflict = true;
  const conflicted = draw(), footer = nodes(conflicted.props.footer).filter(node => node.type === 'Button');
  assert.deepEqual(footer.map(node => [node.props.focusId, node.props.disabled]), [['reset', true], ['save', true]]);
  assert.ok(nodes(conflicted).some(node => node.props.focusId === 'reload'));
  nodes(conflicted).find(node => node.props.focusId === 'keep-editing').props.onPress(); assert.equal(calls.at(-1), 'keep');
});
