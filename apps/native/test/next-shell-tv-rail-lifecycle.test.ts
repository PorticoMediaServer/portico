import {headerWordmark} from '../src-next/kit/shell/brand';
// @fabric-react
import {canReturnToPage, holdsPageFocus} from '../src-next/shell/page-focus-return.ts';
import * as homeStart from '../src-next/screens/home/home-start';
import {listeningCoverFixture, stackSlotFixture} from './helpers/screen-harness.mjs';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {surfacePaintFixture, screenModule} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import {createTheme} from '../src-next/kit/theme';
import * as shellModel from '../src-next/shell/model';
import * as focusModel from '../src-next/shell/tv-focus';
import * as overlayKeys from '../src-next/platform/overlay-keys';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};

for (const adapter of ['native', 'vega'] as const) for (const retirement of ['abort', 'successor', 'register'] as const) {
test(`${adapter}: the real TV shell retains its page opener and retires Menu before passive cleanup (${retirement})`, async () => {
  const successorFirst = retirement === 'successor';
  const registry = createFocusRegistry(), ids = focusModel.tvFocusIds(authority), requests: string[] = [];
  const row = {regionId: ids.row('continue'), childId: 'actual-card'};
  registry.configure({id: row.regionId, order: [row.childId], entry: row.childId, screenStart: ids.start});
  registry.attach(row, {requestTVFocus() {requests.push('card'); registry.didFocus(row);}});
  registry.configure({id: ids.spotlight, order: ['play'], entry: 'play'});
  registry.attach(ids.start, {requestTVFocus() {requests.push('play'); registry.didFocus(ids.start);}});
  const theme = createTheme({isTV: true, width: 1920}), motion = {transforms: false, timing: () => ({duration: 0})};
  const useShared = (value: number) => React.useRef({value}).current;
  const original = new AbortController();
  let keyboardVisible = false, dismissals = 0;
  const menu = await screenModule(new URL(adapter === 'native' ? '../src-next/platform/menu-key.ts'
    : '../../vega/src/overrides/native-next/platform/menu-key.ts', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Keyboard: {isVisible: () => keyboardVisible, dismiss() {dismissals++;}}, Platform: {OS: 'android', isTV: true}, useTVEventHandler() {}},
    './overlay-keys': overlayKeys, '../../../../../native/src-next/platform/overlay-keys': overlayKeys,
  });
  const dispatch = () => menu.dispatchMenuKey({eventType: adapter === 'native' ? 'menu' : 'context_menu', eventKeyAction: 1});
  let menuFallback: (() => void) | undefined, menuSetup: (() => void | (() => void)) | undefined;
  let activeSetup: (() => void | (() => void)) | undefined, registrations = 0, retiredReads = 0, abortDuringRegistration = false;
  // Forward the intercepted effect signature, including the caller-owned dependencies.
  const forwardEffect = React.useEffect;
  const useEffect: typeof React.useEffect = (setup, deps) => forwardEffect(() => {
    activeSetup = setup;
    try {return setup();} finally {activeSetup = undefined;}
    // This fixture forwards the production effect's dependency list verbatim.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  let railBack: (() => boolean) | undefined, tree: any, root: any, sessionRenders = 0;
  let snapshot: any = {authority, destination: {kind: 'home'}, libraries: [], hasLiveTV: false, profile: {}, account: {actions: []}, selectionActive: false,
    homeVisible: true, homeObscured: false, mediaExpanded: false};
  const navigations: unknown[] = [], tops: unknown[] = [], menuCapture: boolean[] = [];
  let homeDocument: any = {...authority, rows: [], spotlight: [{entry: {id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Movie', playback: {itemId: 'movie'}},
    reason: {kind: 'featured', text: {fallback: 'Featured'}}}]};
  const runtime = {home: {getSnapshot: () => ({document: homeDocument})}, playback: {}, retirementSignal: original.signal, getSnapshot: () => {
    if (original.signal.aborted) {retiredReads++; throw new Error('retired shell accessor');}
    return snapshot;
  }, subscribe: () => () => {}, reportError(error: unknown) {throw error;},
    onDestination: (destination: unknown) => navigations.push(destination), scrollToTop: (destination: unknown) => tops.push(destination),
    memory: {read: async () => undefined, remember: async () => {}},
    registerTVRailBack: (fn: () => boolean) => {railBack = fn; return () => {railBack = undefined;};},
    renderRoutes() {}, renderSelection() {}, renderDestination() {}};
  const {NativeShell} = await screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {...stackSlotFixture, 
    './page-focus-return': {canReturnToPage, holdsPageFocus},
    '../screens/home/home-start': homeStart,
    '../kit/overlays/SurfacePaintHost': surfacePaintFixture,
    '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    '../platform/menu-key': {registerTVMenuFallback: (run: () => void) => {
      menuFallback = run; menuSetup = activeSetup; registrations++;
      const off = menu.registerTVMenuFallback(run);
      if (abortDuringRegistration) original.abort();
      return off;
    }},
    '../platform/tv-menu': {useTVMenuCapture: (capture: boolean) => {menuCapture.push(capture);}},
    react: {...React, default: React, useEffect}, 'react-native': {View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: useShared, withTiming: (v: unknown) => v},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})}, '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'},
    '../kit/shell': {AccountMenu: 'AccountMenu'}, '../kit/browse': {}, '../kit/shell/brand': {headerWordmark}, '../kit/theme': {useTheme: () => theme}, '../kit/useMotion': {useMotion: () => motion},
    '../kit/focus': {useFocusRegistry: () => registry}, './tv-focus': focusModel, '../screens/home/HomeScreen': {HomeScreen: 'HomeScreen'}, './model': shellModel,
    './PhoneTabBar': {}, './MediumRail': {MediumRail: 'MediumRail'}, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}}, './PhoneHomeHeader': {}, './TVRail': {TVRail: 'TVRail'}, './TVProfileMenu': {TVProfileMenu: 'ProfileMenu'},
    './NativePlaybackLayer': {NativePlaybackLayer: 'PlaybackLayer'}, './DesktopHeader': {},
  });
  const frames = new Map<number, FrameRequestCallback>(); let sequence = 0;
  const previousFrame = globalThis.requestAnimationFrame, previousCancel = globalThis.cancelAnimationFrame;
  globalThis.requestAnimationFrame = fn => {frames.set(++sequence, fn); return sequence;};
  globalThis.cancelAnimationFrame = id => {frames.delete(id);};
  const rootHost = createFabricHookHost(() => {root = NativeShell({runtime});});
  let sessionHost: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    await rootHost.mount();
    const session = nodes(root).find(node => node.type?.name === 'ShellSession');
    sessionHost = createFabricHookHost(() => {sessionRenders++; tree = session.type({runtime, snapshot});});
    await act(async () => {await sessionHost!.mount();});
    const rail = () => nodes(tree).find(node => node.type === 'TVRail');
    const beforeUnrelated = sessionRenders;
    await act(() => {
      registry.configure({id: 'unrelated-mounted-region', order: ['unrelated-action']});
      registry.attach({regionId: 'unrelated-mounted-region', childId: 'unrelated-action'}, {requestTVFocus() {}});
    });
    assert.equal(sessionRenders, beforeUnrelated, 'unrelated host/config commits do not rerender the entire shell');
    await act(() => {registry.didFocus(row); registry.didFocus({regionId: ids.rail, childId: 'home'}); rail().props.onExpandedChange(true);});
    assert.equal(registry.isRegionActive(row.regionId), true, 'rail retains the exact row opener, including its physical slot');
    assert.equal(railBack!(), true);
    assert.deepEqual(requests, ['card']);
    assert.equal(registry.capture()?.childId, row.childId);
    await act(() => rail().props.onNavigate({kind: 'home'}));
    assert.deepEqual(navigations, [], 'OK on the current rail item does not recreate the destination');
    assert.deepEqual(tops, [{kind: 'home'}]);
    await act(() => rail().props.onNavigate({kind: 'saved'}));
    assert.deepEqual(navigations, [{kind: 'saved'}]);
    for (const patch of [{mediaExpanded: true}, {homeVisible: false}]) {
      registry.didFocus(row);
      await act(async () => {snapshot = {...snapshot, ...patch}; await sessionHost!.rerender();});
      assert.equal(registry.isRegionActive(row.regionId), true);
      registry.didBlur(row); await Promise.resolve();
      await act(async () => {snapshot = {...snapshot, homeVisible: true, mediaExpanded: false}; await sessionHost!.rerender();});
      for (const [id, fn] of [...frames]) {frames.delete(id); fn(0);}
      assert.equal(requests.at(-1), 'card');
      assert.equal(registry.capture()?.childId, row.childId);
    }
    // A logical full-listening close keeps the existing sole return owner until actual paint exits.
    registry.didFocus(row);
    await act(async () => {snapshot = {...snapshot, mediaExpanded: true}; await sessionHost!.rerender();});
    const listening = () => nodes(tree).find(node => node.type === 'PlaybackLayer');
    await act(() => listening().props.onTVListeningPresenceChange(true));
    registry.didBlur(row); await Promise.resolve();
    const beforeExit = requests.length;
    await act(async () => {snapshot = {...snapshot, mediaExpanded: false}; await sessionHost!.rerender();});
    for (const [id, fn] of [...frames]) {frames.delete(id); fn(0);}
    assert.equal(requests.length, beforeExit, 'logical close does not restore beneath retained listening paint');
    await act(() => listening().props.onTVListeningPresenceChange(false));
    for (const [id, fn] of [...frames]) {frames.delete(id); fn(0);}
    assert.equal(requests.length, beforeExit + 1); assert.equal(requests.at(-1), 'card');
    const library = {regionId: 'actual-library-region', childId: 'actual-library-card'};
    registry.configure({id: library.regionId, entry: library.childId, order: [library.childId]});
    registry.attach(library, {requestTVFocus() {requests.push('library-card'); registry.didFocus(library);}});
    await act(async () => {snapshot = {...snapshot, destination: {kind: 'saved'}, homeVisible: false}; await sessionHost!.rerender(); registry.didFocus(library);
      registry.didFocus({regionId: ids.rail, childId: 'saved'}); rail().props.onExpandedChange(true);});
    assert.equal(railBack!(), true);
    assert.equal(requests.at(-1), 'library-card', 'rail return memory follows the actual destination page, not a former Home address');
    await act(async () => {snapshot = {...snapshot, destination: {kind: 'library', libraryId: 'movies'}, railDestination: {kind: 'library', libraryId: 'movies'},
      libraries: [{id: 'movies'}]}; await sessionHost!.rerender();});
    assert.deepEqual(rail().props.destination, {kind: 'library', libraryId: 'movies'}, 'the rail follows the actual title-highlighted page');
    const topsBefore = tops.length, navigationsBefore = navigations.length;
    // cb0ef443: a pushed destination hides the shell chrome. The rail is then absent,
    // so there is no retained hidden origin to act on; hiding alone navigates and
    // scrolls nothing.
    await act(async () => {snapshot = {...snapshot, contentVisible: false}; await sessionHost!.rerender();});
    assert.equal(nodes(tree).some(node => node.type === 'TVRail'), false, 'a pushed destination hides the rail');
    assert.equal(navigations.length, navigationsBefore, 'hiding the chrome by itself navigates nowhere');
    assert.equal(tops.length, topsBefore, 'hiding the chrome never scrolls the hidden origin');
    await act(async () => {snapshot = {...snapshot, destination: {kind: 'home'}, contentVisible: true, homeVisible: true, homeObscured: false, mediaExpanded: false}; await sessionHost!.rerender(); registry.didFocus(ids.start);});
    assert.equal(menuCapture.at(-1), false, 'Menu at the real Home opening focus returns to tvOS');
    registry.attachRegion(ids.rail, {requestTVFocus() {requests.push('rail'); registry.didFocus({regionId: ids.rail, childId: 'home'});}});
    const menuCalls: string[] = [];
    const focusOff = menu.registerFocusedMenu({}, () => menuCalls.push('focused'));
    const overlayOff = overlayKeys.registerOverlayKeys(() => {menuCalls.push('overlay'); return false;});
    try {
      keyboardVisible = true;
      assert.equal(dispatch(), true); assert.equal(dismissals, 1); assert.deepEqual(menuCalls, []);
      keyboardVisible = false;
      assert.equal(dispatch(), true); assert.deepEqual(menuCalls, ['overlay'], 'the owning inert overlay still blocks content');
      overlayOff();
      assert.equal(dispatch(), true); assert.deepEqual(menuCalls, ['overlay', 'focused']);
    } finally {keyboardVisible = false; overlayOff(); focusOff();}
    await act(() => {assert.equal(dispatch(), true);});
    assert.equal(registry.capture()?.regionId, ids.rail, 'ordinary context Menu enters the current rail');
    const beforeOpenRail = requests.length;
    await act(() => menuFallback!());
    assert.equal(requests.length, beforeOpenRail, 'context Menu consumes an already open rail');
    await act(() => {registry.didFocus(row);});
    assert.equal(menuCapture.at(-1), true, 'a focus move below Home captures Menu before Back returns to the top');
    await act(async () => {snapshot = {...snapshot, mediaExpanded: true}; await sessionHost!.rerender();});
    assert.equal(menuCapture.at(-1), true, 'playback retains Menu even if Home had the starting focus');
    const beforePlayer = requests.length;
    await act(() => menuFallback!());
    assert.equal(requests.length, beforePlayer, 'full player context Menu does nothing');
    // Actual ShellSession uses the shared resolved start; it must not keep the
    // former hardcoded Play yield when the real Home has no such action.
    await act(async () => {
      rail().props.onExpandedChange(false);
      snapshot = {...snapshot, mediaExpanded: false};
      homeDocument = {...authority, rows: [], spotlight: []};
      registry.attach(ids.start, null);
      registry.didFocus(row); await sessionHost!.rerender();
    });
    assert.equal(menuCapture.at(-1), false, 'admitted empty Home yields with retained prior row focus and no invented Customize');
    assert.equal(registry.config(ids.customize), undefined);
    const releaseOverlay = registry.retain(row);
    try {
      await act(async () => {await sessionHost!.rerender();});
      assert.equal(menuCapture.at(-1), true, 'the original overlay return lease keeps Menu while Home retains focus');
    } finally {await act(() => releaseOverlay());}
    assert.equal(menuCapture.at(-1), false, 'actual return-lease removal reopens Home yield without a second owner');
    await act(async () => {homeDocument = undefined; await sessionHost!.rerender();});
    assert.equal(menuCapture.at(-1), true, 'loading cannot infer an empty Home or release Menu');
    await act(async () => {homeDocument = {...authority, rows: [{id: 'deferred', title: 'Deferred', kind: 'ordinary', artworkShape: 'poster', entries: [], deferred: true}], spotlight: []}; await sessionHost!.rerender();});
    assert.equal(menuCapture.at(-1), true, 'admitted deferred-only Home is still waiting');
    await act(async () => {homeDocument = {...authority, rows: [], spotlight: []}; await sessionHost!.rerender();});
    assert.equal(menuCapture.at(-1), false);
    await act(() => {registry.didFocus({regionId: ids.rail, childId: 'home'}); rail().props.onExpandedChange(true);});
    assert.equal(menuCapture.at(-1), true, 'an open rail keeps its own Back step');
    const retained = menuFallback!, retainedSetup = menuSetup!;
    assert.equal(typeof retainedSetup, 'function');
    let successorCalls = 0;
    const successorOff = successorFirst ? menu.registerTVMenuFallback(() => {successorCalls++;}) : undefined;
    try {
      const beforeRetirement = requests.length;
      let reentrantCleanup: void | (() => void) = undefined;
      if (retirement === 'register') {
        abortDuringRegistration = true;
        reentrantCleanup = retainedSetup(); // Abort before the new registration returns its unsubscribe.
      } else original.abort(); // No React update/unmount has run: removal must be synchronous.
      assert.doesNotThrow(() => {assert.equal(dispatch(), successorFirst);});
      assert.equal(successorCalls, successorFirst ? 1 : 0);
      assert.doesNotThrow(retained, 'an independently retained original callback refuses before the guarded accessor');
      assert.equal(retiredReads, 0); assert.equal(requests.length, beforeRetirement);
      const beforeSetup = registrations;
      assert.equal(retainedSetup(), undefined, 'an already-retired original never registers a Menu observer');
      assert.equal(registrations, beforeSetup);
      reentrantCleanup?.();
      await sessionHost!.unmount(); sessionHost = undefined;
      assert.equal(dispatch(), successorFirst, 'old passive cleanup cannot erase the successor');
      assert.equal(successorCalls, successorFirst ? 2 : 0);
      assert.equal(retiredReads, 0); assert.equal(requests.length, beforeRetirement);
    } finally {successorOff?.();}

  } finally {
    await sessionHost?.unmount(); await rootHost.unmount();
    globalThis.requestAnimationFrame = previousFrame; globalThis.cancelAnimationFrame = previousCancel;
  }
  assert.equal(railBack, undefined);
});
}

test('the real rail declares current-page entry and semantic Right return; native focus alone never navigates', async () => {
  const {hooks} = await import('./helpers/screen-harness.mjs');
  const {motionRules} = await import('../../../packages/design/src/index');
  const {defaultI18n} = await import('../../../packages/i18n/src');
  const h = hooks(), ids = focusModel.tvFocusIds(authority), changes: boolean[] = [], navigations: unknown[] = [];
  const registry = createFocusRegistry(), returnTo = {regionId: ids.row('continue'), childId: 'remembered-card'};
  const nativeCard = {requestTVFocus() {throw new Error('Right must stay native, with no application focus request');}};
  registry.configure({id: returnTo.regionId, entry: 'first-card', order: ['first-card', returnTo.childId]});
  registry.attach(returnTo, nativeCard);
  const theme = createTheme({isTV: true, width: 1920});
  let subscribeReturn!: (listener: () => void) => () => void;
  const {TVRail} = await screenModule(new URL('../src-next/shell/TVRail.tsx', import.meta.url), {
    react: {...h.react, useSyncExternalStore: (subscribe: typeof subscribeReturn, get: () => unknown) => {subscribeReturn = subscribe; return get();}}, '@portico/design': {motionRules}, 'react-native': {Image: 'Image', ScrollView: 'ScrollView', Text: 'Text', View: 'View', findNodeHandle: (host: unknown) => host === nativeCard ? 719 : 720},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop', Path: 'Path'},
    'react-native-reanimated': {default: {View: 'AnimatedView', Text: 'AnimatedText'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (value: number) => ({value}), withTiming: (value: unknown) => value, withDelay: (_delay: unknown, value: unknown) => value},
    '../../../../packages/i18n/src': {defaultI18n}, '../kit/controls': {Focusable: 'Focusable'},
    '../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => registry, focusTestID: (await import('../src-next/kit/focus/registry')).focusTestID}, './tv-focus': focusModel,
    '../kit/artwork': {ProfileFace: 'ProfileFace'}, '../kit/Icon': {Icon: 'Icon'}, '../kit/shell/brand': {headerWordmark}, '../kit/theme': {useTheme: () => theme},
    '../kit/useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})}, './model': shellModel,
  });
  const render = () => h.render(() => TVRail({authority, libraries: [], hasLiveTV: false, destination: {kind: 'home'}, profile: {id: 'p', name: 'Actual'},
    onNavigate: (next: unknown) => navigations.push(next), onProfile() {}, profileRef: {current: null}, onExpandedChange: (open: boolean) => changes.push(open),
    returnTo}));
  const tree = render();
  const region = nodes(tree).find(node => node.type === 'FocusRegion');
  assert.equal(region.props.entry, 'home'); assert.equal(region.props.entryPolicy, 'entry');
  assert.equal(region.props.orientation, 'vertical');
  assert.equal(region.props.edges.right, ids.row('continue'));
  const search = nodes(tree).find(node => node.type === 'Focusable' && node.props.focusId === 'search');
  const controls = (value: unknown) => nodes(value).filter(node => node.type === 'Focusable');
  assert.ok(controls(tree).every(node => node.props.nextFocusRight === 719), 'all rail entries, including Profile, target the exact live card rather than its region entry');
  let returnChanges = 0;
  const releaseReturn = subscribeReturn(() => {returnChanges++;});
  try {
    registry.configure({id: 'unrelated-rail-neighbor', order: ['action']});
    registry.attach({regionId: 'unrelated-rail-neighbor', childId: 'action'}, {requestTVFocus() {}});
    registry.didFocus(returnTo);
    assert.equal(returnChanges, 0, 'only the exact return host/enabled state can rerender the rail');
    registry.attach(returnTo, null);
    assert.equal(returnChanges, 1);
    assert.ok(controls(render()).every(node => node.props.nextFocusRight === undefined), 'a removed host leaves native region fallback intact');
    registry.attach(returnTo, {requestTVFocus() {}});
    assert.equal(returnChanges, 2);
    assert.ok(controls(render()).every(node => node.props.nextFocusRight === 720), 'a reattached card replaces the old native handle');
    registry.configure({id: returnTo.regionId, enabled: false});
    assert.equal(returnChanges, 3);
    const hidden = render();
    assert.ok(controls(hidden).every(node => node.props.nextFocusRight === undefined), 'a hidden destination cannot be a native return target');
    assert.equal(nodes(hidden).find(node => node.type === 'FocusRegion').props.edges.right, undefined,
      'the region guide must also release the hidden page, rather than routing native Right to its disabled guide');
    registry.configure({id: returnTo.regionId, enabled: true});
    assert.equal(returnChanges, 4);
    const restored = render();
    assert.equal(nodes(restored).find(node => node.type === 'FocusRegion').props.edges.right, returnTo.regionId);
    assert.ok(controls(restored).every(node => node.props.nextFocusRight === 720), 'reactivation restores the current real native host');
  } finally {releaseReturn();}
  region.props.onFocusChange(true);
  assert.deepEqual(changes, [true]); assert.deepEqual(navigations, []);
  assert.equal(search.props.onFocus, undefined, 'Up/Down has no application navigation handler');
  search.props.onPress(); assert.deepEqual(navigations, [{kind: 'search'}]);
});
