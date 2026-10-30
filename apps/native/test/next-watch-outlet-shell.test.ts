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
import * as model from '../src-next/shell/model';
import * as focus from '../src-next/shell/tv-focus';
import * as menuKey from '../src-next/platform/menu-key';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
test('the real shell mounts one persistent player and a separate box-none watch chrome outlet below menus', async () => {
  const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};
  const registry = createFocusRegistry(), theme = createTheme({isTV: true, width: 1920});
  const snapshot = {authority, destination: {kind: 'home'}, libraries: [], profile: {}, account: {actions: []}, selectionActive: false, homeVisible: true, contentVisible: true, mediaExpanded: false};
  let root: any, tree: any;
  const runtime = {home: {getSnapshot: () => ({document: undefined})}, getSnapshot: () => snapshot, subscribe: () => () => {}, playback: {}, memory: {read: async () => undefined}, reportError(error: unknown) {throw error;},
    renderWatch: () => React.createElement('WatchChrome'),
    renderRoutes: () => React.createElement('TitleRoutes'), renderSelection: () => React.createElement('MenuOverlays'), renderDestination() {}};
  const {NativeShell} = await screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {...stackSlotFixture, 
    './page-focus-return': {canReturnToPage, holdsPageFocus},
    '../screens/home/home-start': homeStart,
    '../kit/overlays/SurfacePaintHost': surfacePaintFixture,
    '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    '../platform/tv-menu': {useTVMenuCapture: () => {}}, '../platform/menu-key': menuKey,
    react: {...React, default: React}, 'react-native': {View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (value: number) => React.useRef({value}).current, withTiming: (value: unknown) => value},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})}, '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'},
    '../kit/shell': {}, '../kit/browse': {}, '../kit/theme': {useTheme: () => theme}, '../kit/useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})},
    '../kit/focus': {useFocusRegistry: () => registry}, './tv-focus': focus, '../screens/home/HomeScreen': {HomeScreen: 'HomeScreen'}, './model': model,
    './PhoneTabBar': {}, './MediumRail': {MediumRail: 'MediumRail'}, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}}, './PhoneHomeHeader': {}, './TVRail': {TVRail: 'TVRail'}, './TVProfileMenu': {TVProfileMenu: 'ProfileMenu'},
    './NativePlaybackLayer': {NativePlaybackLayer: 'PersistentPlaybackLayer'}, './DesktopHeader': {},
  });
  const rootHost = createFabricHookHost(() => {root = NativeShell({runtime});});
  let sessionHost: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    await act(async () => {await rootHost.mount();});
    const session = nodes(root).find(node => node.type?.name === 'ShellSession');
    sessionHost = createFabricHookHost(() => {tree = session.type({runtime, snapshot});});
    await act(async () => {await sessionHost!.mount();});
    const rendered = nodes(tree), player = rendered.filter(node => node.type === 'PersistentPlaybackLayer');
    assert.equal(player.length, 1);
    assert.strictEqual(player[0].props.adapter, runtime.playback);
    assert.equal(rendered.filter(node => node.type === 'WatchChrome').length, 1);
    const chrome = rendered.find(node => node.type === 'View' && node.props.children?.type === 'WatchChrome');
    assert.equal(chrome.props.pointerEvents, 'box-none');
    assert.equal(chrome.props.style.zIndex, theme.layers.player + 1);
    assert.ok(chrome.props.style.zIndex < theme.layers.menu);
    assert.ok(rendered.indexOf(player[0]) < rendered.findIndex(node => node.type === 'WatchChrome'));
    const base = () => nodes(tree).find(node => node.type === 'AnimatedView' && nodes(node.props.children).some(child => child.type === 'HomeScreen'));
    assert.equal(base().props.accessibilityElementsHidden, false);
    assert.equal(base().props.importantForAccessibility, 'auto');
    snapshot.contentVisible = false;
    await act(async () => {await sessionHost!.rerender();});
    assert.equal(base().props.accessibilityElementsHidden, true);
    assert.equal(base().props.importantForAccessibility, 'no-hide-descendants');
    snapshot.contentVisible = true; snapshot.mediaExpanded = true;
    await act(async () => {await sessionHost!.rerender();});
    assert.equal(base().props.accessibilityElementsHidden, true);
    const watch = nodes(tree).find(node => node.type === 'View' && node.props.children?.type === 'WatchChrome');
    assert.notEqual(watch.props.importantForAccessibility, 'no-hide-descendants', 'visible split watch panes stay exposed in their separate outlet');
    snapshot.mediaExpanded = false;
    await act(async () => {await sessionHost!.rerender();});
    assert.equal(base().props.accessibilityElementsHidden, false, 'a collapsed dock leaves the destination accessible');
  } finally {await sessionHost?.unmount(); await rootHost.unmount();}
});
