import * as mutationRecovery from '../../../packages/client-core/src/user-mutation-recovery.ts';
import {NativePendingChanges} from '../src-next/shell/pending-changes.ts';
// @fabric-react
import * as layoutModel from '../src-next/kit/layout/model.ts';
import * as homeWindow from '../src-next/screens/home/home-window';
import {createNativeMusicMixes} from '../src-next/screens/destinations/music/mixes';
import * as errorPresentation from '../../../packages/client-core/src/presentation/errors';
import * as homeStart from '../src-next/screens/home/home-start';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry';
import {heroSpec} from '../../../packages/design/src/index';
import {homeActionDiagnostics} from '../src-next/platform/home-action-diagnostics.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {HomeService} from '../../../packages/client-core/src/home-service';
import * as homeSelection from '../../../packages/client-core/src/home-selection/index';
import * as homeCapabilities from '../../../packages/client-core/src/home-selection';
import * as homePresentation from '../../../packages/client-core/src/presentation/home-spotlight';
import * as serverText from '../../../packages/client-core/src/presentation/server-text';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import * as shellModel from '../src-next/shell/model';
import * as focusModel from '../src-next/shell/tv-focus';
import * as homeModel from '../src-next/screens/home/model';
import * as readPlatform from '../src-next/screens/read/platform';
import {nativeHomeEntryLifetime} from '../src-next/screens/home/entry-lifetime';
import {NativeScreenServices} from '../src-next/screens/screen-services';
import {tvHomeRows} from '../src-next/screens/home/tv-presentation';
import {NativeHomePaging} from '../src-next/shell/home-paging';
import {nativeHomeSelectionScope} from '../src-next/shell/selection-scope';
import {NativeShellNavigation, nativeRailDestination, nativeEntryRoute, titleRoute} from '../src-next/shell/routes';
import {planHomeWindows} from '../src-next/shell/home-windows';
import * as rowState from '../src-next/kit/layout/row-state';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
const source = (file: string) => process.env.PORTICO_HOME_LOOP_SOURCE ? pathToFileURL(path.join(process.env.PORTICO_HOME_LOOP_SOURCE, file)) : new URL(`../src-next/${file}`, import.meta.url);
const entry = (id: string) => ({id, libraryId: 'movies', kind: 'movie', title: id, available: true});
const row = (id: string) => ({id, viewerFence: 'fence', title: id, kind: 'recent', artworkShape: 'poster', entries: [entry(`${id}-a`), entry(`${id}-b`), entry(`${id}-c`)],
  privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: false,
  total: 3, start: 0, limit: 20, hasMore: false, nextCursor: '', endpoint: '', revision: {catalog: 1, viewer: 1}});

for (const tv of [false, true]) test(`actual ${tv ? 'TV' : 'phone'} Home/Row effects settle against real controller publications without registration cleanup churn`, async () => {
  const raw = {serverId: 'server', viewerFence: 'fence', rows: [row('recent'), row('offscreen')], spotlight: [], layout: {revision: 1, rowOrder: ['recent', 'offscreen'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T20:00:00Z'};
  const home = new HomeService({request: async () => JSON.parse(JSON.stringify(raw))}); await home.load();
  const registry = createFocusRegistry(), theme = createTheme({isTV: tv, width: tv ? 1920 : 390});
  const react = {...React, default: React};
  const animated = {default: {View: 'AnimatedView', ScrollView: 'AnimatedScrollView'}, runOnJS: (fn: unknown) => fn,
    useSharedValue: (value: unknown) => React.useRef({value}).current, useAnimatedScrollHandler: (fn: unknown) => fn};
  const native = {View: 'View', Text: 'Text', RefreshControl: 'RefreshControl', Pressable: 'Pressable',
    AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: tv ? 1920 : 390, height: tv ? 1080 : 844})};
  const useFocusAnchor = () => React.useMemo(() => ({scrollRef: {current: null}, offset: {value: 0}, repeating: false, stop() {}, move() {}, anchor() {}}), []);
  const {Row} = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {
    react, 'react-native': native, 'react-native-reanimated': animated,
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme},
    '../focus': {FocusProvider: 'FocusProvider', FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', focusTestID: () => 'native-card', useOptionalFocusRegistry: () => registry},
    '../focus/page-boundary': {useRowPageBoundary() {}}, './model': layoutModel, './row-state': rowState, './FocusAnchor': {useFocusAnchor}, './MotionItem': {MotionItem: 'MotionItem'}, './types': {itemSelection: () => undefined},
  });
  const recovery = await screenModule(new URL('../src-next/screens/home/useTvHomeRecovery.ts', import.meta.url), {
    react, 'react-native': native, '../../../../../packages/client-core/src/presentation/errors': errorPresentation, '../../shell/model': shellModel,
  });
  const {HomeScreen} = await screenModule(source('screens/home/HomeScreen.tsx'), {'./live-expiry': await import('../src-next/screens/home/live-expiry.ts'), '../../shell/boot-timing': {logBootMilestone: () => {}}, '../../shell/home-read-log': {noteHomeRead: () => {}, createHomeReadLog: () => () => 'home-service'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, 
    '../../kit/useMotion': {useMotion: () => ({transforms: true, reduce: false, timing: () => ({})})}, 
    '../../../../../packages/client-core/src/presentation/errors': errorPresentation,
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable', folds: []})},
    // Physical page Back and recovery paint are outside this registration fixture;
    // recovery ownership/effects above still execute the actual hook.
    '../destinations/useTVPageBack': {useTVPageBack() {}},
    '../../kit/focus/initial-entry': {createInitialRegionEntry}, './home-window': homeWindow, './home-start': homeStart,
    '../destinations/music/MusicMixesRow': {MusicMixesRow: 'MusicMixesRow'},
    './TvHomeRecovery': {TvHomeRecovery: 'TvHomeRecovery'}, './useTvHomeRecovery': recovery,
    '@portico/design': {heroSpec},
    react, 'react-native': native, 'react-native-reanimated': animated, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/home-spotlight': homePresentation, '../../../../../packages/client-core/src/home-selection': homeCapabilities,
    '../../../../../packages/client-core/src/presentation/server-text': serverText, '../../../../../packages/client-core/src/presentation/kit-model': {},
    '../../platform/ArtworkPipeline': {}, '../../kit/artwork': {}, '../../kit/controls': {Button: 'Button'}, '../../kit/cards': {},
    '../../kit/layout': {Row, Hero: 'Hero', FocusPreviewHero: 'Preview'}, '../../kit/layout/types': {}, '../../kit/layout/FocusAnchor': {useFocusAnchor},
    '../../kit/overlays': {Banner: 'Banner'}, '../../kit/states': {skeletonTiming: {delayMs: 300, slowMs: 8000}, LoadingPaint: 'LoadingPaint', SkeletonBlock: 'Skeleton', HomeSkeleton: 'HomeSkeleton'}, '../../platform/home-action-diagnostics': {homeActionDiagnostics}, '../../kit/theme': {nativeType, useTheme: () => theme}, '../../kit/focus': {useFocusRegistry: () => registry},
    '../../shell/model': shellModel, '../../shell/tv-focus': focusModel, '../../shell/home-paging': {NativeHomePaging}, './tv-presentation': {tvHomeRows}, './HomeHeroFacts': {}, './model': homeModel,
  });
  const screenServices = await screenModule(source('screens/native-screen-services.tsx'), {
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})},
    '../kit/dock/DockClearance': {useDockClearance: () => 0, feedbackBottomOffset: (base: number) => base},
    react, 'react-native': {NativeModules: {}}, './screen-services': {NativeScreenServices},
    '../kit/overlays': {ToastHost: 'Toast'}, '../kit/states': {StateView: 'State'},
  });
  // Independent listening overlays are outside this Home registration fixture,
  // as are the already-stubbed route renderers. Any attempted action is a failure.
  const inactiveOverlay = () => ({open() {throw new Error('unexpected listening action in Home fixture');}, render: () => null, dispose() {}});
  const {createNativeShellBindings} = await screenModule(source('shell/assembly.tsx'), {
    '../screens/library/selection-actions': {}, '../screens/downloads/request': {}, '../platform/offline-downloads': {}, './selection-display': {},
    './listening-playlist': {createNativeListeningPlaylist: inactiveOverlay},
    './listening-queue-policy': {createNativeListeningQueuePolicy: inactiveOverlay},
    './listening-song-detail': {createNativeListeningSongDetail: inactiveOverlay},
    '../screens/home/HomeEmpty': {NativeHomeEmpty: 'NativeHomeEmpty'}, './model': shellModel,
    './music-discover-navigation': {createNativeMusicDiscoverNavigation: inactiveOverlay},
    '../screens/destinations/music/mixes': {createNativeMusicMixes},
    react, 'react-native': {...native, Platform: {isTV: tv}, NativeModules: {}, BackHandler: {addEventListener: () => ({remove() {}})}},
    '../../../../packages/i18n/src': {defaultI18n}, '../../../../packages/client-core/src/user-mutation-recovery': mutationRecovery,
    './pending-changes': {NativePendingChanges}, './PendingChanges': {PendingChanges: 'PendingChanges'},
    '../../../../packages/client-core/src/home-selection/index': homeSelection,
    '../../../../packages/client-core/src/home-selection': homeCapabilities, '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({silent: true})},
    '../../../../packages/client-core/src/library-management': {}, '../../../../packages/client-core/src/presentation/home-spotlight': homePresentation,
    '../platform/home-action-diagnostics': {homeActionDiagnostics}, '../platform/haptics': {}, '../kit/overlays': {ToastHost: 'Toast'}, '../kit/states': {}, '../kit/shell': {OwnerTag: 'Owner'},
    './HomeSelectionBars': {HomeSelectionBars: 'Bars'}, './HomeSelectionOverlays': {HomeSelectionOverlays: 'Overlays'}, './account': {nativeAccountMenu() {}, nativeMenuRouteAvailable: () => true}, './library-scan': {libraryScanReadiness: () => ({ok: true})}, '../screens/downloads/DownloadSheet': {NativeDownloadSheet: 'NativeDownloadSheet'},
    './home-windows': {planHomeWindows}, './selection-scope': {nativeHomeSelectionScope}, './routes': {nativeRailDestination, nativeEntryRoute, titleRoute},
    './route-registry': {nativeShellRoutes: {subscribe: () => () => {}, bind: () => ({render: () => null, renderWatch: () => null, renderRetained: () => null, renderOverlays: () => null, back: () => false, scrollToTop() {}, dispose() {}})}}, '../screens/home/model': homeModel,
    '../screens/read/platform': readPlatform, '../screens/settings-live/NativeTVReceiver': {NativeTVReceiver: 'NativeTVReceiver'},
    '../screens/native-screen-services': screenServices,
    '../screens/read/reading-books-open': {openNativeReadingBookEntry: async () => false},
    '../screens/home/entry-lifetime': {nativeHomeEntryLifetime},
  });
  const navigation = new NativeShellNavigation(authority, () => authority);
  const bindings = createNativeShellBindings({home, current: () => true, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local', role: 'member'}}),
    account: {account: {role: 'member'}}, api: {getUserMutationCoordinator: () => undefined, getUserMutationScope: () => undefined, request: async () => {throw new Error('unexpected API read in Home registration fixture');}}, events: {on: () => () => {}}, navigation, subscribeAuthority: navigation.subscribe, libraries: () => [],
    playback: {selectionPlayback: () => ({play() {throw new Error('unexpected playback');}, enqueue() {throw new Error('unexpected queue');}, playChannel() {throw new Error('unexpected channel');}}), getSnapshot: () => ({dock: {local: null}})}});
  const listeners = new Set<() => void>();
  let revision = 0, snapshot = {...bindings.getState(), authority, libraries: [], mediaExpanded: false, revision};
  const runtime = {...bindings, home, text: {t: defaultI18n.t, locale: 'en-US'}, getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, artwork() {}, ambient() {}};
  const remove = bindings.subscribe(() => {snapshot = {...snapshot, ...bindings.getState(), revision: ++revision}; for (const listener of listeners) listener();});
  bindings.attach(runtime);
  const controller = nodes(bindings.renderSelection()).find(node => node.type === 'Overlays').props.controller as homeSelection.HomeSelectionController;
  const tokens = new Map<string, () => void>(), register = controller.registerWindow.bind(controller);
  let admissions = 0, renders = 0;
  controller.registerWindow = input => {admissions++; const result = register(input); if (result.ok) tokens.set(input.id, result.unregister); return result;};
  // This fixed two-row host composes the actual components in hook order. Effects,
  // cleanups and subscriptions run through React; native layout is delivered once
  // at host attachment. No test invokes Row's range callbacks or rescues a render.
  function HomeRowHost({element, index}: {element: any; index: number}) {
    const body = element.type(element.props);
    const rowElement = nodes(body).find(node => node.type === Row)!;
    const provider = rowElement.type(rowElement.props), content = nodes(provider).find(node => typeof node.type === 'function')!;
    const track = content.type(content.props);
    const initial = React.useRef({body, track, index});
    React.useLayoutEffect(() => {
      const {body: attachedBody, track: attachedTrack, index: attachedIndex} = initial.current;
      // TV rows sit in the row-shift frame (motion.html 4.1), which forwards the same row layout.
      const view = nodes(attachedBody).find(node => (node.type === 'View' || node.type?.name === 'TVHomeRowShift') && node.props.onLayout)!;
      view.props.onLayout({nativeEvent: {layout: {y: attachedIndex ? 1600 : tv ? 820 : 588, height: 350}}});
      const nativeTrack = nodes(attachedTrack).find(node => node.type === 'AnimatedScrollView');
      const ref = nativeTrack?.props.ref;
      if (ref) ref.current = {scrollTo() {}, requestTVFocus() {}};
      return () => {if (ref) ref.current = null;};
    }, []);
  }
  function Harness() {
    React.useSyncExternalStore(runtime.subscribe, runtime.getSnapshot);
    if (++renders > 80) throw new Error('Maximum Home registration update depth exceeded');
    const tree = HomeScreen({runtime, authority});
    const windows = nodes(tree).filter(node => node.type?.name === 'HomeWindow');
    HomeRowHost({element: windows[0], index: 0});
    HomeRowHost({element: windows[1], index: 1});
  }
  const host = createFabricHookHost(Harness);
  try {
    await act(() => host.mount());
    assert.ok(renders < 25, `Home settled after ${renders} renders`);
    const admitted = admissions, beforeTokens = new Map(tokens);
    assert.ok(admitted > 0 && admitted <= 32);
    await act(() => bindings.selection('recent').onToggle('recent-a'));
    assert.equal(controller.getSnapshot().selection.summary.count, 1, 'current genuine membership remains actionable');
    assert.equal(admissions, admitted, 'selection publications and inline callback changes do not replace unchanged authoritative registrations');
    for (const [id, token] of beforeTokens) assert.equal(tokens.get(id), token, `${id} keeps its cleanup token`);
    await act(async () => {raw.rows[1].title = 'Updated offscreen row'; await home.revalidate();});
    assert.equal(admissions, admitted, 'an unrelated document update retains the canonical unchanged-row registration');
    for (const [id, token] of beforeTokens) assert.equal(tokens.get(id), token);
    await act(() => bindings.selection('recent').onToggle('recent-b'));
    assert.equal(controller.getSnapshot().selection.summary.count, 1, 'canonical reconciliation still leaves genuine unchanged membership actionable');
  } finally {await host.unmount(); remove(); bindings.dispose(); navigation.dispose(); home.dispose();}
});
