import {canReturnToPage, holdsPageFocus} from '../src-next/shell/page-focus-return.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as model from '../src-next/shell/model';
import {hooks, screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

for (const tv of [false, true]) test(`${tv ? 'TV' : 'touch'} shell hides its covered destination while keeping the watch outlet accessible`, async () => {
  const h = hooks(), react = {...h.react, useLayoutEffect() {}};
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  const snapshot = {authority, destination: {kind: 'home'}, libraries: [], profile: {}, account: {actions: []}, homeVisible: true,
    contentVisible: true, mediaExpanded: false, selectionActive: false, homeObscured: false};
  const runtime = {getSnapshot: () => snapshot, subscribe: () => () => {}, home: {getSnapshot: () => ({})},
    renderWatch: () => h.react.createElement('VisibleSplitWatchPanes'), renderRoutes: () => h.react.createElement('Routes'), renderSelection: () => null};
  const registry = {subscribe: () => () => {}, getSnapshot: () => ({}), capture: () => undefined, hasRetainedFocus: () => false};
  const {NativeShell} = await screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {...stackSlotFixture, 
    react: {...react, default: react}, 'react-native': {View: 'View', useWindowDimensions: () => ({width: tv ? 1920 : 840, height: tv ? 1080 : 880})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (value: unknown) => ({value}), withTiming: (value: unknown) => value},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'}, '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined},
    '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
    '../kit/dock/ListeningCoverBridge': {ListeningCoverProvider: 'ListeningCoverProvider', useListeningTabBarPaint: () => ({style: {}, measure() {}})},
    '../kit/overlays/SurfacePaintHost': {SurfacePaintHost: 'SurfacePaintHost'}, '../kit/shell': {AccountMenu: 'AccountMenu'},
    '../kit/theme': {useTheme: () => ({isTV: tv, colors: {ink: '#071018'}, layers: {navigation: 3, sticky: 4, player: 8}})},
    '../kit/useMotion': {useMotion: () => ({transforms: false})}, '../platform/menu-key': {}, '../platform/tv-menu': {useTVMenuCapture() {}},
    '../kit/focus': {useFocusRegistry: () => registry}, './tv-focus': {tvFocusIds: () => ({rail: 'rail'}), sameDestination: (a: unknown, b: unknown) => a === b},
    '../screens/home/HomeScreen': {HomeScreen: 'HomeScreen'},
    '../screens/home/home-start': {homeStartModel: () => ({}), resolveTVHomeStart: () => undefined, canYieldTVHomeMenu: () => false},
    './model': model, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'}, './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'}, './MediumRail': {MediumRail: 'MediumRail'},
    './page-focus-return': {canReturnToPage, holdsPageFocus}, './TVRail': {TVRail: 'TVRail'}, './TVProfileMenu': {TVProfileMenu: 'TVProfileMenu'}, './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'},
  });
  const root = h.render(() => NativeShell({runtime}));
  const session = nodes(root).find(node => node.type?.name === 'ShellSession'); assert.ok(session);
  const render = () => h.render(() => session.type({runtime, snapshot}));
  const base = (tree: unknown) => nodes(tree).find(node => node.type === 'AnimatedView' && nodes(node.props.children).some(child => child.type === 'HomeScreen'));
  for (const [routeCovered, expanded] of [[false, false], [true, false], [false, true], [true, true], [false, false]]) {
    snapshot.contentVisible = !routeCovered; snapshot.mediaExpanded = expanded;
    const tree = render(), hidden = routeCovered || expanded;
    assert.equal(base(tree).props.accessibilityElementsHidden, hidden);
    assert.equal(base(tree).props.importantForAccessibility, hidden ? 'no-hide-descendants' : 'auto');
    const watch = nodes(tree).find(node => node.type === 'View' && (Array.isArray(node.props.children) ? node.props.children : [node.props.children]).some((child: any) => child?.type === 'VisibleSplitWatchPanes'));
    assert.ok(watch, 'the separate watch outlet remains mounted');
    assert.notEqual(watch.props.accessibilityElementsHidden, true);
    assert.notEqual(watch.props.importantForAccessibility, 'no-hide-descendants');
  }
});
