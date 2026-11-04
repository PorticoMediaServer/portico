import assert from 'node:assert/strict';
import * as layerStack from '../src-next/kit/focus/layer-stack.ts';
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
    renderWatch: () => h.react.createElement('VisibleSplitWatchPanes'), playback: {}, renderRoutes: () => h.react.createElement('Routes'), renderSelection: () => null};
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
    '../kit/focus': {...layerStack, CoveredLayer: 'CoveredLayer', useFocusRegistry: () => registry}, './tv-focus': {tvFocusIds: () => ({rail: 'rail'}), sameDestination: (a: unknown, b: unknown) => a === b},
    '../screens/home/HomeScreen': {HomeScreen: 'HomeScreen'},
    '../screens/home/home-start': {homeStartModel: () => ({}), resolveTVHomeStart: () => undefined, canYieldTVHomeMenu: () => false},
    './model': model, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'}, './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'}, './MediumRail': {MediumRail: 'MediumRail'}, './TVRail': {TVRail: 'TVRail'}, './TVProfileMenu': {TVProfileMenu: 'TVProfileMenu'}, './UnreachableLine': {ShellUnreachableLine: () => null}, './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'},
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
    // Focus containment: on TV the covered page blocks D-pad focus, so focus can never escape from
    // a pushed screen into Home underneath (the Android TV show-page trap). Touch has no D-pad.
    const layer = nodes(tree).find(node => node.type === 'CoveredLayer');
    if (tv) {
      assert.ok(layer, 'the TV base page sits in a focus-containment layer');
      assert.ok(nodes(layer.props.children).some(child => child.type === 'HomeScreen'), 'the layer holds the base page');
      assert.equal(layer.props.covered, hidden, routeCovered ? 'a pushed route blocks focus into the page under it' : 'an uncovered page takes focus normally');
      // W-12: the base page never holds focus itself; every CoveredLayer is an autoFocus guide on TV (next-covered-layer.test.ts).
      assert.equal(layer.props.autoFocus, undefined, 'no per-caller option: the layer itself decides');
    } else assert.equal(layer, undefined);
    const watch = nodes(tree).find(node => (node.type === 'View' || node.type === 'CoveredLayer') && (Array.isArray(node.props.children) ? node.props.children : [node.props.children]).some((child: any) => child?.type === 'VisibleSplitWatchPanes'));
    assert.ok(watch, 'the separate watch outlet remains mounted');
    assert.notEqual(watch.props.accessibilityElementsHidden, true);
    assert.notEqual(watch.props.importantForAccessibility, 'no-hide-descendants');
    if (tv) assert.equal(watch.props.covered, false, 'nothing presented: the watch outlet takes focus normally');
  }
  if (!tv) return;
  // #593 review: a presented state (the refused-link page) over the expanded player or the listening screen covers
  // them too, so the chrome's preferred picture can't pull focus from under the page. The same element stays mounted.
  const byTestID = (tree: unknown, id: string) => nodes(tree).find(node => node.type === 'CoveredLayer' && node.props.testID === id);
  snapshot.contentVisible = true; snapshot.mediaExpanded = true;
  let tree = render();
  assert.equal(byTestID(tree, 'native-shell-playback').props.covered, false, 'the expanded player is the top layer');
  assert.equal(byTestID(tree, 'native-shell-watch').props.covered, false);
  for (const id of ['native-shell-playback', 'native-shell-watch']) assert.equal(byTestID(tree, id).props.inertWhenUncovered, true, `${id} is no focus guide while uncovered (next-covered-layer)`);
  const playbackElement = byTestID(tree, 'native-shell-playback').props.children;
  const release = layerStack.presentTVLayer('content-link-unavailable', () => true);
  try {
    tree = render();
    assert.equal(byTestID(tree, 'native-shell-playback').props.covered, true, 'the player and listening host are covered under the page');
    assert.equal(byTestID(tree, 'native-shell-watch').props.covered, true, 'and so is the watch chrome');
    assert.equal(byTestID(tree, 'native-shell-playback').props.children.type, playbackElement.type, 'the playback layer is the same element: nothing remounts');
    const playbackWrapper = nodes(tree).find(node => node.type === 'View' && [node.props.children].flat().some((child: any) => child?.props?.testID === 'native-shell-playback'));
    assert.equal(playbackWrapper.props.style.zIndex, 8, 'and keeps its place above the routes');
  } finally {release();}
  tree = render();
  assert.equal(byTestID(tree, 'native-shell-playback').props.covered, false, 'closed: the player takes focus again');
});
