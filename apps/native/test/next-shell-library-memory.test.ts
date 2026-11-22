// @fabric-react
import * as homeStart from '../src-next/screens/home/home-start';
import * as layerStack from '../src-next/kit/focus/layer-stack.ts';
import * as pageModal from '../src-next/kit/focus/tv-page-modal.ts';
import {listeningCoverFixture, stackSlotFixture} from './helpers/screen-harness.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {surfacePaintFixture, screenModule} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import {createTheme} from '../src-next/kit/theme';
import * as shellModel from '../src-next/shell/model';
import * as focusModel from '../src-next/shell/tv-focus';
import * as menuKey from '../src-next/platform/menu-key';

async function loadShell(theme: any, registry: any) {
  return screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {...stackSlotFixture,
    '../screens/home/home-start': homeStart,
    '../kit/overlays/SurfacePaintHost': surfacePaintFixture,
    '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    '../platform/tv-menu': {useTVMenuCapture: () => {}}, '../platform/menu-key': menuKey,
    react: {...React, default: React}, 'react-native': {View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (value: number) => React.useRef({value}).current, withTiming: (value: unknown) => value},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34})}, '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'},
    '../kit/shell': {AccountMenu: 'AccountMenu'}, '../kit/artwork': {StandardBackdrop: 'AccountMenu'}, '../kit/theme': {useTheme: () => theme}, '../kit/useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})},
    '../kit/focus': {...layerStack, ...pageModal, CoveredLayer: 'CoveredLayer', CoveredSignal: ({children}: any) => children, useFocusRegistry: () => registry}, './tv-focus': focusModel, './model': shellModel, './HomeScreen': {HomeScreen: 'HomeScreen'},
    './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'}, './MediumRail': {MediumRail: 'MediumRail'}, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}}, './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'}, './TVRail': {}, './TVProfileMenu': {}, './UnreachableLine': {ShellUnreachableLine: () => null}, './NativePlaybackLayer': {}, './DesktopHeader': {},
  });
}
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
test('a real screen-owned Library destination is remembered and a late initial read cannot restore its old ID', async () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'actual'};
  const theme = createTheme({width: 390, isTV: false}), registry = createFocusRegistry(), writes: string[] = [], navigations: any[] = [];
  let finish!: (id: string) => void, snapshot: any = {authority, destination: {kind: 'home'}, contentVisible: true, libraries: [
    {id: 'new', name: 'Actual new', kind: 'movie'}, {id: 'old', name: 'Actual old', kind: 'movie'},
  ], account: {actions: []}, profile: {}, hasLiveTV: false, homeVisible: true, selectionActive: false};
  const runtime = {getSnapshot: () => snapshot, subscribe: () => () => {}, reportError(error: unknown) {throw error;},
    memory: {read: () => new Promise<string>(resolve => {finish = resolve;}), remember: async (_scope: unknown, id: string) => {writes.push(id);}},
    onDestination: (destination: unknown) => {navigations.push(destination); snapshot = {...snapshot, destination};},
    renderRoutes() {}, renderSelection() {}, renderDestination() {}, scrollToTop() {}};
  const {NativeShell} = await loadShell(theme, registry);
  let root: any, tree: any;
  const rootHost = createFabricHookHost(() => {root = NativeShell({runtime});}); await rootHost.mount();
  const session = nodes(root).find(node => node.type?.name === 'ShellSession');
  const page = createFabricHookHost(() => {tree = session.type({runtime, snapshot});});
  try {
    await act(() => page.mount());
    await act(async () => {snapshot = {...snapshot, contentVisible: false}; await page.rerender();});
    assert.equal(nodes(tree).some(node => node.type === 'PhoneTabBar'), false, 'a full-screen account route draws no tab bar underneath');
    await act(async () => {snapshot = {...snapshot, contentVisible: true, destination: {kind: 'library', libraryId: 'new'}}; await page.rerender();});
    assert.deepEqual(writes, ['new']);
    await act(async () => {snapshot = {...snapshot, destination: {kind: 'home'}}; await page.rerender(); finish('old');});
    await act(() => nodes(tree).find(node => node.type === 'PhoneTabBar').props.onSelect('libraries'));
    assert.deepEqual(navigations.at(-1), {kind: 'library', libraryId: 'new'});
  } finally {await act(async () => {await page.unmount(); await rootHost.unmount();});}
});

test('a tab chosen while the watch page is open folds the player into its mini player and goes there (W3 F7)', async () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'actual'};
  const theme = createTheme({width: 390, isTV: false}), registry = createFocusRegistry(), navigations: any[] = [], transitions: any[] = [], scrolled: any[] = [];
  const fence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller',
    controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
  let snapshot: any = {authority, destination: {kind: 'home'}, contentVisible: true, libraries: [], account: {actions: []}, profile: {}, hasLiveTV: false, homeVisible: true, selectionActive: false};
  let local: any = {fence, expanded: true, kind: 'video'};
  const playback = {getSnapshot: () => ({authority, fence, dock: {local}}), dock: {collapse: (value: unknown) => ({kind: 'collapse', fence: value})},
    onTransition: async (transition: unknown) => {transitions.push(transition); local = {...local, expanded: false};}};
  const runtime = {getSnapshot: () => snapshot, subscribe: () => () => {}, reportError(error: unknown) {throw error;}, playback,
    memory: {read: async () => undefined, remember: async () => {}},
    onDestination: (destination: unknown) => {navigations.push(destination); snapshot = {...snapshot, destination};},
    renderRoutes() {}, renderSelection() {}, renderDestination() {}, scrollToTop: (destination: unknown) => {scrolled.push(destination);}};
  const {NativeShell} = await loadShell(theme, registry);
  let root: any, tree: any;
  const rootHost = createFabricHookHost(() => {root = NativeShell({runtime});}); await rootHost.mount();
  const session = nodes(root).find(node => node.type?.name === 'ShellSession');
  const page = createFabricHookHost(() => {tree = session.type({runtime, snapshot});});
  try {
    await act(() => page.mount());
    const tabs = () => nodes(tree).find(node => node.type === 'PhoneTabBar');
    await act(() => tabs().props.onSelect('home'));
    assert.deepEqual(transitions, [{kind: 'collapse', fence}], 'the watch page folds away');
    assert.deepEqual(scrolled, [], 'choosing the tab already shown only reveals it under the folded player');
    local = {...local, expanded: true};
    await act(() => tabs().props.onSelect('search'));
    assert.equal(transitions.length, 2);
    assert.deepEqual(navigations, [{kind: 'search'}], 'another tab is opened as well');
    await act(() => tabs().props.onSelect('home'));
    assert.equal(transitions.length, 2, 'with the player already folded, a tab press is only navigation');
  } finally {await page.unmount(); await rootHost.unmount();}
});
