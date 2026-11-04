import assert from 'node:assert/strict';
import * as layerStack from '../src-next/kit/focus/layer-stack.ts';
import test from 'node:test';
import * as model from '../src-next/shell/model';
import {hooks, screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';

// N2-3 (tablet 6 and 8): on a tablet the rail stays beside a pushed page, which is drawn in the width beside it.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
const home = {kind: 'shell', destination: {kind: 'home'}} as const;
const title = {kind: 'title', entry: {id: 'm1', kind: 'movie', title: 'Film'}} as const;
const reader = {kind: 'read', itemId: 'b1', sourceId: 's1'} as const;

async function shell(width: number, height: number, top: unknown) {
  const h = hooks(), react = {...h.react, useLayoutEffect() {}};
  const snapshot = {authority, destination: {kind: 'home'}, libraries: [], profile: {}, account: {actions: []}, homeVisible: false,
    contentVisible: false, mediaExpanded: false, selectionActive: false, homeObscured: false};
  const stacks = {subscribe: () => () => {}, getSnapshot: () => ({active: 'home', stacks: [{tab: 'home', presentations: [{route: home}, {route: top}]}]}),
    renderRoute: () => null, commitNativePop: () => true, selectTab() {}, reselectTab() {}};
  const runtime = {getSnapshot: () => snapshot, subscribe: () => () => {}, home: {getSnapshot: () => ({})}, stacks,
    renderWatch: () => null, renderRoutes: () => null, renderSelection: () => null};
  const registry = {subscribe: () => () => {}, getSnapshot: () => ({}), capture: () => undefined, hasRetainedFocus: () => false};
  const {NativeShell} = await screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {...stackSlotFixture,
    react: {...react, default: react}, 'react-native': {View: 'View', useWindowDimensions: () => ({width, height})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (value: unknown) => ({value}), withTiming: (value: unknown) => value},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'}, '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined},
    '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
    '../kit/dock/ListeningCoverBridge': {ListeningCoverProvider: 'ListeningCoverProvider', useListeningTabBarPaint: () => ({style: {}, measure() {}})},
    '../kit/overlays/SurfacePaintHost': {SurfacePaintHost: 'SurfacePaintHost'}, '../kit/shell': {AccountMenu: 'AccountMenu'},
    '../kit/theme': {useTheme: () => ({isTV: false, colors: {ink: '#071018'}, layers: {navigation: 3, sticky: 4, player: 8}})},
    '../kit/useMotion': {useMotion: () => ({transforms: false})}, '../platform/menu-key': {}, '../platform/tv-menu': {useTVMenuCapture() {}},
    '../kit/focus': {...layerStack, CoveredLayer: 'CoveredLayer', useFocusRegistry: () => registry}, './tv-focus': {tvFocusIds: () => ({rail: 'rail'}), sameDestination: (a: unknown, b: unknown) => a === b},
    '../screens/home/HomeScreen': {HomeScreen: 'HomeScreen'},
    '../screens/home/home-start': {homeStartModel: () => ({}), resolveTVHomeStart: () => undefined, canYieldTVHomeMenu: () => false},
    './model': model, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'}, './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'}, './MediumRail': {MediumRail: 'MediumRail'}, './TVRail': {TVRail: 'TVRail'}, './TVProfileMenu': {TVProfileMenu: 'TVProfileMenu'}, './UnreachableLine': {ShellUnreachableLine: () => null}, './NativePlaybackLayer': {NativePlaybackLayer: 'NativePlaybackLayer'},
  });
  const root = h.render(() => NativeShell({runtime}));
  const session = nodes(root).find(node => node.type?.name === 'ShellSession'); assert.ok(session);
  const tree = h.render(() => session.type({runtime, snapshot}));
  const rail = nodes(tree).find(node => node.type === 'MediumRail');
  const page = nodes(tree).find(node => typeof node.props?.routeStyle === 'function')!.props.routeStyle(top, 'Page');
  const style = Object.assign({}, ...[page.props.style].flat().filter(Boolean));
  return {rail, left: style.left, provider: [page.props.children].flat()[0]};
}

for (const [width, height, size] of [[1180, 820, 'expanded'], [820, 1180, 'medium']] as const) test(`N2-3: a ${size} tablet keeps the rail beside a pushed title page, which starts where the rail ends`, async () => {
  const f = await shell(width, height, title);
  assert.ok(f.rail, 'the rail stays while a title is open');
  const railWidth = f.rail.props.expanded ? 248 : model.mediumRailMetrics.width;
  assert.equal(f.left, railWidth, 'the page is drawn in the width beside the rail');
  assert.equal(f.provider.props.value, railWidth, 'and the page is told its inset, so it sizes itself from the width beside the rail');
});

test('N2-3: the reader stays full screen on a tablet, and a phone has no rail', async () => {
  const read = await shell(1180, 820, reader);
  assert.equal(read.rail, undefined, 'the reader is immersive');
  assert.equal(read.left, 0);
  assert.equal(read.provider.props.value, 0);
  const phone = await shell(390, 844, title);
  assert.equal(phone.rail, undefined);
  assert.equal(phone.left, 0);
  assert.equal(phone.provider.props.value, 0);
});
