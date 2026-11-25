// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as shellModel from '../src-next/shell/model.ts';
import * as tvFocus from '../src-next/shell/tv-focus.ts';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';

// P2-71-H14b: a rail, tab or avatar navigation never fades the whole page area from 0. The chrome
// and page stay painted; only the route's own content changes, with its own motion.

const flatten = (style: any): Record<string, any> => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

function animatedHarness() {
  const timingCalls: unknown[] = [];
  const animatedStyles: Record<string, any>[] = [];
  return {
    timingCalls,
    animatedStyles,
    module: {
      default: {View: 'AnimatedView'},
      useSharedValue: (initial: number) => React.useRef({value: initial}).current,
      useAnimatedStyle: (fn: () => Record<string, any>) => {const style = fn(); animatedStyles.push(style); return style;},
      withTiming: (toValue: number, config: unknown) => {timingCalls.push({toValue, config}); return toValue;},
    },
  };
}

const authority = {serverId: 'srv', accountId: 'acc', profileId: 'prof', epoch: '["fam",false]', viewerFence: 'opened', privacyFence: 'opened'};
const motion = {reduce: false, transforms: true, timing: () => ({duration: 180})};

test('H14b: TouchShell navigation keeps the page painted with zero opacity resets or page-wide fade animations', async () => {
  const anim = animatedHarness();
  let destination: shellModel.ShellDestination = {kind: 'home'};
  const destinations: shellModel.ShellDestination[] = [];
  let tree: any;
  const {TouchShell} = await screenModule(new URL('../src-next/shell/TouchShell.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-reanimated': anim.module,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34, left: 0, right: 0})},
    '../kit/dock/PlayerMorphBridge': {useInitialPlayerPagePaint: () => undefined},
    '../kit/dock/ListeningCoverBridge': {useListeningTabBarPaint: () => ({measure() {}, style: {}})},
    '../kit/shell': {AccountMenu: 'AccountMenu'},
    '../kit/shell/Layer': {Layer: 'Layer'},
    './playback': {nativeShellBackTransition: () => undefined},
    '../kit/theme': {useTheme: () => ({isTV: false, platform: 'phone', colors: {ink: '#000'}, layers: {sticky: 10, player: 30}})},
    '../kit/useMotion': {useMotion: () => motion},
    './tv-focus': tvFocus,
    './HomeScreen': {HomeScreen: 'HomeScreen'},
    './model': shellModel,
    './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'},
    './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'},
    './PlayOnButton': {PlayOnButton: 'PlayOnButton'},
    './MediumRail': {MediumRail: 'MediumRail'},
    '../kit/layout/page-inset': {pageInsetElement: (_inset: number, children: any) => children},
    './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'},
    '../kit/navigation/stack-slot': {NativeStackSlots: class {}, NativeStackSlotsProvider: ({children}: any) => children},
    '../kit/navigation/rail-hold': {useRailHeld: () => false},
    './TouchStacks': {TouchStacks: 'TouchStacks', nativeStackMotionTier: () => 'standard'},
    './UnreachableLine': {ShellUnreachableLine: 'ShellUnreachableLine'},
  });

  const makeSnapshot = () => ({
    authority,
    destination,
    libraries: [{id: 'films', name: 'Films', kind: 'movie'}],
    hasLiveTV: true,
    serverName: 'Home',
    profile: {id: 'prof', name: 'Viewer', initial: 'V', color: 'blue', primary: true, pinRequired: false},
    unreadNotifications: false,
    unreadNotificationCount: 0,
    contentVisible: true,
    selectionActive: false,
    homeObscured: false,
    homeVisible: true,
    mediaExpanded: false,
    account: {actions: [], viewerRole: 'member'},
  });
  const runtime: any = {
    getSnapshot: makeSnapshot,
    memory: {read: async () => undefined, remember: async () => {}, readSidebar: async () => true, rememberSidebar: async () => {}},
    onDestination: (next: shellModel.ShellDestination) => {destination = next; destinations.push(next);},
    scrollToTop() {},
    reportError(err: unknown) {throw err;},
    renderDestination: (dest: shellModel.ShellDestination) => React.createElement('Dest', {kind: dest.kind}),
    renderRoutes: () => null,
    renderSelection: () => null,
  };

  const host = createFabricHookHost(() => {tree = TouchShell({runtime, snapshot: makeSnapshot()});});
  try {
    await act(() => host.mount());
    const tabBar = () => nodes(tree).find(n => n.type === 'PhoneTabBar');
    assert.ok(tabBar(), 'PhoneTabBar rendered');

    anim.timingCalls.length = 0;
    await act(() => tabBar().props.onSelect('search'));
    await act(() => host.rerender());
    assert.deepEqual(destinations, [{kind: 'search'}]);
    assert.equal(anim.timingCalls.length, 0, 'TouchShell navigate() starts zero withTiming animations');

    await act(() => tabBar().props.onSelect('home'));
    await act(() => host.rerender());
    assert.equal(anim.timingCalls.length, 0, 'returning to Home starts zero withTiming animations');

    const pageViews = nodes(tree).filter(n => n.type === 'AnimatedView');
    assert.ok(pageViews.length > 0);
    for (const view of pageViews) {
      const style = flatten(view.props.style);
      assert.notEqual(style.opacity, 0, 'no page container resets opacity to 0');
    }
  } finally {
    await host.unmount();
  }
});

test('H14b: TVShell navigation animates nothing page-wide and pageStyle keeps only the rail translateX slide', async () => {
  const anim = animatedHarness();
  const registry = createFocusRegistry();
  let destination: shellModel.ShellDestination = {kind: 'home'};
  const destinations: shellModel.ShellDestination[] = [];
  let tree: any;

  const {TVShell} = await screenModule(new URL('../src-next/shell/TVShell.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-reanimated': anim.module,
    '../kit/theme': {useTheme: () => ({isTV: true, platform: 'tv', colors: {ink: '#0d0d12'}, layers: {player: 30}})},
    '../kit/useMotion': {useMotion: () => motion},
    '../kit/navigation/back': {registerBack: () => () => {}},
    '../platform/menu-key': {registerTVMenuFallback: () => () => {}},
    '../platform/tv-menu': {useTVMenuCapture() {}},
    '../kit/focus': {
      CoveredLayer: 'CoveredLayer',
      CoveredSignal: ({children}: any) => children,
      createTVLayerStack: () => ({sync() {}, dispose() {}}),
      provideTVPage: () => () => {},
      requestNativeFocus() {},
      tvLayerSpecs: () => [{key: 'page'}],
      tvPageModal: {subscribe: () => () => {}, getSnapshot: () => false},
      tvRouteLayerKeys: () => '[]',
      useFocusRegistry: () => registry,
    },
    './tv-focus': tvFocus,
    './HomeScreen': {HomeScreen: 'HomeScreen'},
    '../screens/home/home-start': {canYieldTVHomeMenu: () => false, homeStartModel: () => ({}), resolveTVHomeStart: () => ({kind: 'waiting'})},
    './model': shellModel,
    './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './TVRail': {TVRail: 'TVRail'},
    './TVProfileMenu': {TVProfileMenu: 'TVProfileMenu'},
    './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'},
    './UnreachableLine': {ShellUnreachableLine: 'ShellUnreachableLine'},
  });

  const makeSnapshot = () => ({
    authority,
    destination,
    libraries: [{id: 'films', name: 'Films', kind: 'movie'}],
    hasLiveTV: true,
    serverName: 'Living Room',
    profile: {id: 'prof', name: 'Viewer', initial: 'V', color: 'blue', primary: true, pinRequired: false},
    unreadNotifications: false,
    unreadNotificationCount: 0,
    contentVisible: true,
    selectionActive: false,
    homeObscured: false,
    homeVisible: true,
    mediaExpanded: false,
    account: {actions: [], viewerRole: 'member'},
  });
  let lastRailProps: any;
  const runtime: any = {
    getSnapshot: makeSnapshot,
    home: {getSnapshot: () => ({document: undefined})},
    onDestination: (next: shellModel.ShellDestination) => {destination = next; destinations.push(next);},
    scrollToTop() {},
    reportError(err: unknown) {throw err;},
    renderDestination: (dest: shellModel.ShellDestination) => React.createElement('Dest', {kind: dest.kind}),
    renderRoutes: ({rail}: any) => {
      const railNode = nodes(rail).find(n => n.type === 'TVRail');
      if (railNode) lastRailProps = railNode.props;
      return rail ?? null;
    },
    renderSelection: () => null,
  };

  const host = createFabricHookHost(() => {tree = TVShell({runtime, snapshot: makeSnapshot()});});
  try {
    await act(() => host.mount());
    assert.ok(lastRailProps, 'TVRail rendered inside route rail layer');

    // Find the AnimatedView wrapping native-shell-base (the page container).
    const pageAnimatedView = () => nodes(tree).find(n => n.type === 'AnimatedView' && nodes(n).some(c => c.props?.testID === 'native-shell-base'));
    const initialStyle = flatten(pageAnimatedView().props.style);
    assert.deepEqual(initialStyle.transform, [{translateX: 0}], 'TV page animated style keeps translateX slide');
    assert.equal('opacity' in initialStyle, false, 'TV page animated style has no page-wide opacity');

    // Expand rail -> only translateX slides to 270; still no opacity on the page container.
    await act(() => lastRailProps.onExpandedChange(true));
    const expandedStyle = flatten(pageAnimatedView().props.style);
    assert.deepEqual(expandedStyle.transform, [{translateX: 270}], 'rail expansion slides page by 270');
    assert.equal('opacity' in expandedStyle, false, 'rail expansion adds no page-wide opacity');

    // Navigate from rail -> destination changes, page container still has no opacity reset.
    await act(() => lastRailProps.onNavigate({kind: 'library', libraryId: 'films'}));
    await act(() => host.rerender());
    assert.deepEqual(destinations, [{kind: 'library', libraryId: 'films'}]);
    const navigatedStyle = flatten(pageAnimatedView().props.style);
    assert.deepEqual(navigatedStyle.transform, [{translateX: 0}]);
    assert.equal('opacity' in navigatedStyle, false, 'TV navigation never resets page-wide opacity');
  } finally {
    await host.unmount();
  }
});
