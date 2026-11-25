// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {hooks, listeningCoverFixture, screenModule, stackSlotFixture, surfacePaintFixture} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import * as layerStack from '../src-next/kit/focus/layer-stack.ts';
import * as pageModal from '../src-next/kit/focus/tv-page-modal.ts';
import {createTheme} from '../src-next/kit/theme';
import * as shellModel from '../src-next/shell/model';
import * as focusModel from '../src-next/shell/tv-focus';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {NativeShellRouteRegistry} from '../src-next/shell/route-registry';
import * as homeStart from '../src-next/screens/home/home-start';
import {createTVPageBack} from '../src-next/screens/destinations/tv-page-back.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};

test('NativeShell routes to TouchShell vs TVShell by theme.isTV and re-exports tvRailScrim', async () => {
  for (const isTV of [false, true]) {
    const h = hooks();
    const {NativeShell, tvRailScrim} = await screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {
      react: h.react,
      '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'},
      '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider'},
      '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
      '../kit/dock/ListeningCoverBridge': {ListeningCoverProvider: 'ListeningCoverProvider'},
      '../kit/overlays/SurfacePaintHost': surfacePaintFixture,
      '../kit/navigation/rail-hold': {RailHoldProvider: 'RailHoldProvider'},
      '../kit/theme': {useTheme: () => ({isTV})},
      './model': shellModel,
      './TouchShell': {TouchShell: 'TouchShell'},
      './TVShell': {TVShell: 'TVShell', tvRailScrim: (ink: string, open: boolean) => `${ink}:${open}`},
    });
    const snapshot = {authority, destination: {kind: 'home'}};
    const tree = h.render(() => NativeShell({runtime: {subscribe: () => () => {}, getSnapshot: () => snapshot}}));
    assert.equal(nodes(tree).some(node => node.type === 'TouchShell'), !isTV);
    assert.equal(nodes(tree).some(node => node.type === 'TVShell'), isTV);
    assert.equal(tvRailScrim('#071018', true), '#071018:true');
  }
});

test('TouchShell mounts with zero TV hooks or TV modules', async () => {
  const theme = createTheme({isTV: false, width: 390});
  const snapshot = {
    authority, destination: {kind: 'home'}, libraries: [], hasLiveTV: false,
    profile: {id: 'p', name: 'Viewer'}, account: {actions: []},
    selectionActive: false, homeVisible: true, homeObscured: false, contentVisible: true, mediaExpanded: false,
  };
  const runtime = {
    getSnapshot: () => snapshot, subscribe: () => () => {}, reportError(error: unknown) {throw error;},
    onDestination() {}, scrollToTop() {},
    memory: {read: async () => undefined, remember: async () => {}, readSidebar: async () => true, rememberSidebar: async () => {}},
    renderRoutes: () => null, renderSelection: () => null, renderDestination: () => null,
  };
  // Deliberately omit ALL TV modules (focus, tv-menu, menu-key, home-start, TVRail, TVProfileMenu).
  // Any accidental import of a TV module in TouchShell.tsx immediately fails screenModule().
  const {TouchShell} = await screenModule(new URL('../src-next/shell/TouchShell.tsx', import.meta.url), {
    ...stackSlotFixture,
    react: {...React, default: React},
    'react-native': {View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn()},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34})},
    '../kit/dock/PlayerMorphBridge': {useInitialPlayerPagePaint: () => undefined},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/shell': {AccountMenu: 'AccountMenu'},
    '../kit/theme': {useTheme: () => theme},
    '../kit/useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})},
    './tv-focus': {sameDestination: focusModel.sameDestination},
    './HomeScreen': {HomeScreen: 'HomeScreen'},
    './model': shellModel,
    './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'},
    './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'},
    './MediumRail': {MediumRail: 'MediumRail'},
    './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'},
    './UnreachableLine': {ShellUnreachableLine: () => null},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = TouchShell({runtime, snapshot});});
  try {
    await act(async () => {await host.mount();});
    const rendered = nodes(tree);
    assert.ok(rendered.some(node => node.type === 'PhoneTabBar'));
    assert.ok(rendered.some(node => node.type === 'AccountMenu'));
    assert.equal(rendered.some(node => node.type === 'TVRail' || node.type === 'TVProfileMenu'), false);
  } finally {await host.unmount();}
});

test('TVShell mounts with zero Touch hooks or Touch modules', async () => {
  const theme = createTheme({isTV: true, width: 1920}), registry = createFocusRegistry();
  const snapshot = {
    authority, destination: {kind: 'home'}, libraries: [], hasLiveTV: false,
    profile: {id: 'p', name: 'Viewer'}, account: {actions: []},
    selectionActive: false, homeVisible: true, homeObscured: false, contentVisible: true, mediaExpanded: false,
  };
  let routeRail: any;
  const runtime = {
    home: {getSnapshot: () => ({document: undefined})},
    getSnapshot: () => snapshot, subscribe: () => () => {}, reportError(error: unknown) {throw error;},
    onDestination() {}, scrollToTop() {},
    renderRoutes: (options: any) => {routeRail = options?.rail; return null;},
    renderSelection: () => null, renderDestination: () => null,
  };
  const back = await screenModule(new URL('../src-next/kit/navigation/back.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}},
  });
  // Deliberately omit ALL Touch modules (TouchStacks, stack-slot, rail-hold, MediumRail, PhoneTabBar,
  // PhoneHomeHeader, AccountMenu, PlayerMorphBridge, ListeningCoverBridge, safe-area-context).
  const {TVShell, tvRailScrim} = await screenModule(new URL('../src-next/shell/TVShell.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (v: number) => React.useRef({value: v}).current, withTiming: (v: unknown) => v},
    '../kit/theme': {useTheme: () => theme},
    '../kit/useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})},
    '../kit/navigation/back': back,
    '../platform/menu-key': {registerTVMenuFallback: () => () => {}},
    '../platform/tv-menu': {useTVMenuCapture() {}},
    '../kit/focus': {...layerStack, ...pageModal, CoveredLayer: 'CoveredLayer', CoveredSignal: ({children}: any) => children, useFocusRegistry: () => registry, requestNativeFocus: () => false},
    './tv-focus': focusModel,
    './HomeScreen': {HomeScreen: 'HomeScreen'},
    '../screens/home/home-start': homeStart,
    './model': shellModel,
    './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './TVRail': {TVRail: 'TVRail'},
    './TVProfileMenu': {TVProfileMenu: 'TVProfileMenu'},
    './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'},
    './UnreachableLine': {ShellUnreachableLine: () => null},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = TVShell({runtime, snapshot});});
  try {
    await act(async () => {await host.mount();});
    assert.ok(nodes(routeRail).some(node => node.type === 'TVRail'));
    assert.ok(nodes(tree).some(node => node.type === 'TVProfileMenu'));
    assert.equal(nodes(tree).some(node => node.type === 'PhoneTabBar' || node.type === 'MediumRail' || node.type === 'TouchStacks'), false);
    assert.match(tvRailScrim('#071018', false), /linear-gradient/);
    assert.equal(back.backRegistrations(), 1);
  } finally {await host.unmount();}
  assert.equal(back.backRegistrations(), 0);
});

test('registerNativeRoutes registers explicit Touch and TV route tables idempotently', async () => {
  const factory = (tag: string) => {
    const fn = () => ({render: () => tag});
    Object.defineProperty(fn, 'name', {value: tag});
    return fn;
  };
  let watchCalls = 0;
  const defaultRegistry = new NativeShellRouteRegistry();
  const {registerNativeRoutes} = await screenModule(new URL('../src-next/shell/register-routes.ts', import.meta.url), {
    react: {default: {}},
    './route-registry': {nativeShellRoutes: defaultRegistry},
    './library-route': {createNativeHandheldLibraryModule: factory('library')},
    './library-overview-route': {createNativeLibraryOverviewModule: factory('libraries')},
    './search-route': {createNativeSearchModule: factory('search')},
    './settings-route': {
      createNativeSettingsRenderer: factory('settings'),
      createNativeServerDashboardRenderer: factory('server-dashboard'),
      createNativeTVServerSettingsRenderer: factory('server-settings'),
      createNativeEqualizerSettingsRenderer: factory('settings-equalizer'),
    },
    './live-route': {createNativeLiveRenderer: factory('live')},
    './saved-route': {createNativeSavedModule: factory('saved-touch')},
    './saved-destination-route': {createNativeTVSavedRenderer: factory('saved-tv')},
    './account-route': {createAccountRoute: factory('account')},
    './collections-route': {createNativeCollectionRenderer: factory('collection')},
    './music-route': {createNativeTVMusicRenderer: factory('music')},
    './audiobooks-route': {createNativeBookRenderer: factory('book')},
    './people-route': {createNativePersonRenderer: factory('person')},
    './reading-search-route': {createNativeReadingSearchModule: factory('search-reading')},
    './library-settings-route': {createNativeLibrarySettingsRenderer: factory('library-settings')},
    './licences-route': {createNativeLicencesRenderer: factory('licenses')},
    './customize-route': {createNativeHomeCustomizeRenderer: factory('home-customize')},
    './see-all-route': {createNativeHomeSeeAllRenderer: factory('home-see-all')},
    './together-route': {createNativeTogetherRenderer: factory('together')},
    './downloads-route': {createNativeDownloadsModule: factory('downloads')},
    './pins-route': {createNativeHandheldPinsModule: factory('pins')},
    './read-route': {createNativeReadModule: factory('read')},
    './read-destinations': {
      createNativeReadBookRenderer: factory('read-book'),
      createNativeReadSeriesRenderer: factory('read-series'),
      createNativeReadPeriodicalRenderer: factory('read-periodical'),
    },
    './reading-settings-route': {createNativeReadingSettingsRenderer: factory('settings-page')},
    './notifications-route': {createNativeNotificationsRenderer: factory('notifications')},
    './notification-destinations': {
      createNativeRecordingsRenderer: factory('recordings'),
      createNativeServerFeedbackRenderer: factory('server-feedback'),
    },
    '../screens/watch': {
      createNativeWatchBindings: () => ({}),
      registerNativeWatch: (_bindings: unknown, reg: NativeShellRouteRegistry) => {
        watchCalls++;
        const watchFactory = () => ({render: () => 'watch'});
        reg.registerRoute('title', watchFactory);
        reg.registerRoute('channel', watchFactory);
        reg.registerWatch(watchFactory);
        return () => {};
      },
    },
  });

  const touchRegistry = new NativeShellRouteRegistry();
  registerNativeRoutes(false, touchRegistry);
  // Idempotent on repeat calls.
  assert.doesNotThrow(() => registerNativeRoutes(false, touchRegistry));
  assert.equal(watchCalls, 1);
  for (const dest of ['library', 'libraries', 'search', 'settings', 'live', 'saved'] as const) {
    assert.equal(touchRegistry.hasDestination(dest), true, `Touch registers destination ${dest}`);
  }
  for (const route of ['profiles', 'servers', 'collection', 'music', 'book', 'audiobook', 'person', 'search-genre', 'search-genres', 'search-reading',
    'server-dashboard', 'server-settings', 'settings-equalizer', 'library-settings', 'licenses', 'home-customize', 'home-see-all', 'together',
    'saved-view', 'saved-destination', 'downloads', 'edit-pins', 'library-edit-pins', 'read', 'read-book', 'read-series', 'read-periodical',
    'settings-page', 'notifications', 'recordings', 'server-feedback', 'title', 'channel'] as const) {
    assert.equal(touchRegistry.hasRoute(route), true, `Touch registers route ${route}`);
  }
  assert.equal(touchRegistry.hasWatch(), true);

  const tvRegistry = new NativeShellRouteRegistry();
  registerNativeRoutes(true, tvRegistry);
  assert.doesNotThrow(() => registerNativeRoutes(true, tvRegistry));
  assert.equal(watchCalls, 2);
  for (const dest of ['library', 'libraries', 'search', 'settings', 'live', 'saved'] as const) {
    assert.equal(tvRegistry.hasDestination(dest), true, `TV registers destination ${dest}`);
  }
  for (const route of ['profiles', 'servers', 'collection', 'music', 'book', 'audiobook', 'person', 'search-genre', 'search-genres', 'search-reading',
    'server-dashboard', 'server-settings', 'settings-equalizer', 'library-settings', 'licenses', 'home-customize', 'home-see-all', 'together',
    'saved-view', 'saved-destination', 'title', 'channel'] as const) {
    assert.equal(tvRegistry.hasRoute(route), true, `TV registers route ${route}`);
  }
  for (const touchOnly of ['downloads', 'edit-pins', 'library-edit-pins', 'read', 'read-book', 'read-series', 'read-periodical',
    'settings-page', 'notifications', 'recordings', 'server-feedback'] as const) {
    assert.equal(tvRegistry.hasRoute(touchOnly), false, `TV omits Touch-only route ${touchOnly}`);
  }
  assert.equal(tvRegistry.hasWatch(), true);
});

test('unified dispatchBack() preserves exact precedence: Home overlays -> Watch overlays -> expanded player -> TV rail -> TV page/route -> TV Home ladder -> navigation back', async () => {
  const events: string[] = [];
  const back = await screenModule(new URL('../src-next/kit/navigation/back.ts', import.meta.url), {
    react: {...React, default: React},
    'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}},
  });
  const subscribe = () => () => {};
  const navigation = new NativeShellNavigation(authority, () => authority);
  let menuOpen = false, watchOverlayOpen = false, mediaExpanded = false;
  const homeDoc = {serverId: 's', viewerFence: 'v', revision: 1, rows: [], spotlight: []};
  const {createNativeShellBindings} = await screenModule(new URL('../src-next/shell/assembly.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Platform: {isTV: true}, View: 'View'},
    '../kit/navigation/back': back,
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../packages/client-core/src/home-selection/index': {
      homeSelectionContext: () => ({}),
      HomeSelectionController: class {
        subscribe = subscribe;
        getSnapshot() {return {context: {}, menu: menuOpen ? {windowId: 'w', key: 'k'} : undefined, selection: {active: false}};}
        closeMenu() {events.push('home-menu'); menuOpen = false;}
        clear() {}
        dispose() {}
      },
    },
    '../../../../packages/client-core/src/home-selection': {homeEntrySelection: () => undefined, homeEntryCapabilities: () => ({})},
    '../../../../packages/client-core/src/library-management': {},
    '../../../../packages/client-core/src/presentation/home-spotlight': {homeSpotlightModels: () => []},
    '../platform/haptics': {}, '../platform/home-action-diagnostics': {homeActionDiagnostics: {action() {}, error() {}, libraryMenu() {}}},
    '../screens/native-screen-services': {nativeScreenServices: () => ({reportError() {}, notice() {}, requestId: async () => 'r'}), NativeScreenFeedbackHost: 'Feedback'},
    '../kit/shell': {OwnerTag: 'OwnerTag'}, './HomeSelectionBars': {HomeSelectionBars: 'Bars'}, './HomeSelectionOverlays': {HomeSelectionOverlays: 'Overlays'},
    './owner-activity': {createNativeOwnerActivity: () => undefined}, './account': {nativeAccountMenu: () => ({actions: []}), nativeAccountOpenTransition: () => undefined, nativeMenuRouteAvailable: () => false},
    './library-scan': {}, '../pages/touch/downloads/DownloadSheet': {NativeDownloadSheet: 'Downloads'},
    '../screens/downloads/request': {}, '../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported: false})}},
    './selection-display': {}, './selection-scope': {nativeHomeSelectionScope: (v: any) => v}, './routes': {homeSeeAllRoute: () => ({}), nativeEntryRoute: () => ({}), nativeEntryRouteSupported: () => false, nativeRailDestination: (nav: any) => nav.destination},
    './route-registry': {nativeShellRoutes: {subscribe, bind: () => ({
      back: () => {if (!watchOverlayOpen) return false; events.push('watch-overlay'); watchOverlayOpen = false; return true;},
      render: () => null, prepare() {}, renderWatch: () => null, renderRetained: () => null, renderOverlays: () => null, scrollToTop() {}, dispose() {},
    })}},
    './model': shellModel, './music-discover-navigation': {createNativeMusicDiscoverNavigation: () => ({open() {}, render: () => null, dispose() {}})},
    '../screens/home/model': {}, './home-windows': {homeRegistrationLimit: () => false, planHomeWindowsReport: () => ({plan: new Map(), overflow: 0})},
    '../screens/read/platform': {isReadingLibraryKind: () => false}, '../screens/read/reading-books-open': {},
    '../screens/home/entry-lifetime': {}, '../screens/destinations/music/mixes': {createNativeMusicMixes: () => ({dispose() {}})},
    './tv-receiver': {useTVReceiver: () => ({open: false})}, '../kit/player/ReceiverApproval': {ReceiverApproval: 'ReceiverApproval'},
    '../kit/home/HomeEmpty': {NativeHomeEmpty: 'HomeEmpty'},
    './listening-playlist': {createNativeListeningPlaylist: () => ({dispose() {}})},
    './listening-queue-policy': {createNativeListeningQueuePolicy: () => ({dispose() {}})},
    './listening-song-detail': {createNativeListeningSongDetail: () => ({dispose() {}})},
  });

  const bindings = createNativeShellBindings({
    current: () => true, authority: () => authority, subscribeAuthority: subscribe,
    home: {getSnapshot: () => ({document: homeDoc}), subscribe},
    session: () => ({viewer: {...authority, authority: 'local'}}),
    navigation,
    api: {baseUrl: 'https://portico.test', getOutbox: () => undefined, onOutboxDropped: () => () => {}},
    account: {account: {role: 'member'}}, libraries: () => [],
    playback: {
      selectionPlayback: () => ({}),
      getSnapshot: () => ({dock: {local: mediaExpanded ? {expanded: true} : undefined}}),
    },
  } as any);

  // Expanded player registered at `surface`.
  const offPlayer = back.registerBack(() => {
    if (!mediaExpanded) return false;
    events.push('player');
    mediaExpanded = false;
    return true;
  }, {scope: 'surface'});

  // TVShell registers rail Back at `surface` (active only when rail focused & !mediaExpanded).
  const registry = createFocusRegistry(), ids = focusModel.tvFocusIds(authority), theme = createTheme({isTV: true, width: 1920});
  const card = {regionId: 'saved-grid', childId: 'first'}, secondCard = {regionId: 'saved-grid', childId: 'second'};
  registry.configure({id: 'saved-grid', entry: 'first', order: ['first', 'second']});
  registry.attach(card, {requestTVFocus() {events.push('focus-saved-first'); registry.didFocus(card);}});
  registry.attach(secondCard, {requestTVFocus() {events.push('focus-saved-second'); registry.didFocus(secondCard);}});

  let snapshot = (): any => ({
    authority, ...bindings.getState(), mediaExpanded, libraries: [], hasLiveTV: false,
    profile: {id: 'p', name: 'Viewer'}, account: {actions: []},
  });
  const runtime: any = {
    home: {getSnapshot: () => ({document: homeDoc})},
    navigation,
    getSnapshot: () => snapshot(),
    subscribe: () => () => {},
    reportError(error: unknown) {throw error;},
    onDestination: bindings.onDestination,
    scrollToTop: bindings.scrollToTop,
    registerScrollToTop: bindings.registerScrollToTop,
    renderRoutes: () => null, renderSelection: () => null, renderDestination: () => null,
  };
  bindings.registerScrollToTop({kind: 'home'}, () => {events.push('home-scroll-top');});

  const {TVShell} = await screenModule(new URL('../src-next/shell/TVShell.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (v: number) => React.useRef({value: v}).current, withTiming: (v: unknown) => v},
    '../kit/theme': {useTheme: () => theme},
    '../kit/useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})},
    '../kit/navigation/back': back,
    '../platform/menu-key': {registerTVMenuFallback: () => () => {}},
    '../platform/tv-menu': {useTVMenuCapture() {}},
    '../kit/focus': {...layerStack, ...pageModal, CoveredLayer: 'CoveredLayer', CoveredSignal: ({children}: any) => children, useFocusRegistry: () => registry, requestNativeFocus: () => false},
    './tv-focus': focusModel, './HomeScreen': {HomeScreen: 'HomeScreen'}, '../screens/home/home-start': homeStart,
    './model': shellModel, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './TVRail': {TVRail: 'TVRail'}, './TVProfileMenu': {TVProfileMenu: 'TVProfileMenu'},
    './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'}, './UnreachableLine': {ShellUnreachableLine: () => null},
  });
  const {useTVPageBack} = await screenModule(new URL('../src-next/screens/destinations/useTVPageBack.ts', import.meta.url), {
    react: {...React, default: React}, '../../kit/focus': {useFocusRegistry: () => registry}, './tv-page-back': {createTVPageBack},
    '../../kit/navigation/back': back,
  });
  const {useScreenBack} = await screenModule(new URL('../src-next/kit/layout/Screen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Text: 'Text', View: 'View'},
    '../../kit/theme': {useTheme: () => theme}, '../../kit/shell/visual': {shellType: () => ({})},
    '../../kit/focus': {useFocusRegistry: () => registry}, '../navigation/back': back,
  });

  // Navigate to Saved so we can test the full ladder down through TV rail -> TV page Back -> Home ladder -> Home top.
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  let savedAtTop = false;
  const shellHost = createFabricHookHost(() => TVShell({runtime, snapshot: snapshot()}));
  const savedBackHost = createFabricHookHost(() => useTVPageBack(runtime, {kind: 'saved'}, true, {
    current: () => true, start: () => card, atTop: () => savedAtTop,
    scrollTop: () => {events.push('saved-scroll-top'); savedAtTop = true;},
  }));
  const screenBackHost = createFabricHookHost(() => useScreenBack(runtime, () => {events.push('live-screen-back'); return true;}, true, {kind: 'live'}));
  try {
    await act(async () => {await shellHost.mount(); await savedBackHost.mount(); await screenBackHost.mount();});
    // Focus card on Saved so TVShell records page focus, then open TV rail, then open player & overlays above it.
    registry.didFocus(secondCard);
    registry.didFocus({regionId: ids.rail, childId: 'saved'});
    mediaExpanded = true; watchOverlayOpen = true; menuOpen = true;

    // 1. Home selection overlay/menu wins first (overlay scope).
    assert.equal(back.dispatchBack(), true);
    assert.deepEqual(events, ['home-menu']);

    // 2. Watch overlay wins next (overlay scope).
    assert.equal(back.dispatchBack(), true);
    assert.deepEqual(events, ['home-menu', 'watch-overlay']);

    // 3. Expanded player wins before TV rail (surface scope; rail inactive while mediaExpanded).
    assert.equal(back.dispatchBack(), true);
    assert.deepEqual(events, ['home-menu', 'watch-overlay', 'player']);

    // 4. TV rail closes and restores page focus BEFORE TV page Back (surface scope before page scope).
    assert.equal(back.dispatchBack(), true);
    assert.deepEqual(events, ['home-menu', 'watch-overlay', 'player', 'focus-saved-second']);

    // 5. TV page Back scrolls Saved to top and focuses first card (Live's useScreenBack is inactive!).
    assert.equal(back.dispatchBack(), true);
    assert.deepEqual(events, ['home-menu', 'watch-overlay', 'player', 'focus-saved-second', 'saved-scroll-top', 'focus-saved-first']);

    // 6. Now at top of Saved: page Back returns false -> TV shell Home ladder returns to Home and scrolls Home to top.
    assert.equal(back.dispatchBack(), true);
    assert.deepEqual(navigation.getSnapshot().destination, {kind: 'home'});
    assert.equal(events.at(-1), 'home-scroll-top');

    // 7. Push a route over Home: shell navigation.back() pops it back to Home.
    navigation.open({kind: 'server-settings'}, authority);
    assert.equal(back.dispatchBack(), true);
    assert.deepEqual(navigation.getSnapshot().route, {kind: 'shell', destination: {kind: 'home'}});

    // 8. At Home with nothing else open: dispatchBack returns false (yields to platform).
    assert.equal(back.dispatchBack(), false);
  } finally {
    await screenBackHost.unmount();
    await savedBackHost.unmount();
    await shellHost.unmount();
    offPlayer();
    bindings.dispose();
    navigation.dispose();
  }
  assert.equal(back.backRegistrations(), 0);
});
