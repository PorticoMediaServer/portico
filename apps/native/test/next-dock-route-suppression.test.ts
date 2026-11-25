import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackDock, playbackFence, sameFence} from '../../../packages/player-core/src';
import {listeningNoticeKey} from '../../../packages/client-core/src/audio-effects.ts';
import {focusTestID} from '../src-next/kit/focus/registry';
import * as dockModel from '../src-next/kit/dock/model.ts';
import {createTheme} from '../src-next/kit/theme';
import {nativeEqualizer} from '../src-next/platform/playback/equalizer.ts';
import * as signalPath from '../src-next/screens/destinations/music/signal-path-model.ts';
import {captureListeningOpening, expandedListeningOccupant, localListeningOccupant} from '../src-next/shell/listening-presentation.ts';
import {authorityKey, sameAuthority} from '../src-next/shell/model';
import {dockSuppressedForRoute, nativeShellBackTransition} from '../src-next/shell/playback.ts';
import type {NativeShellRoute} from '../src-next/shell/routes';
import {tvFocusIds} from '../src-next/shell/tv-focus';
import * as watchSlotLayout from '../src-next/shell/watch-slot-layout.ts';
import {hooks, listeningCoverFixture, screenModule} from './helpers/screen-harness.mjs';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({
  kind: 'queue',
  playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
  sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1,
});
const entry = {id: 'item', kind: 'movie', title: 'Dune'} as any;
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.type ? [node, ...nodes(node.props.children)] : [];

test('dockSuppressedForRoute suppresses only full-screen account and immersive touch routes', () => {
  const suppressed: NativeShellRoute[] = [
    {kind: 'profiles'},
    {kind: 'profiles', profileId: 'p2'},
    {kind: 'servers'},
    {kind: 'servers', initialPanel: 'add'},
    {kind: 'read', itemId: 'book-1', sourceId: 'src-1'},
    {kind: 'channel', sourceId: 'src-1', channelId: 'ch-1'},
  ];
  for (const route of suppressed) {
    assert.equal(dockSuppressedForRoute(route), true, `expected ${route.kind} to suppress dock`);
  }

  const allowed: (NativeShellRoute | undefined)[] = [
    undefined,
    {kind: 'shell', destination: {kind: 'home'}},
    {kind: 'shell', destination: {kind: 'libraries'}},
    {kind: 'shell', destination: {kind: 'library', libraryId: 'lib-1'}},
    {kind: 'shell', destination: {kind: 'search'}},
    {kind: 'shell', destination: {kind: 'live'}},
    {kind: 'shell', destination: {kind: 'saved'}},
    {kind: 'shell', destination: {kind: 'settings'}},
    {kind: 'title', entry},
    {kind: 'music', entry},
    {kind: 'book', entry},
    {kind: 'audiobook', entry},
    {kind: 'person', personId: 'person-1'},
    {kind: 'downloads'},
    {kind: 'notifications'},
    {kind: 'together'},
    {kind: 'collection', libraryId: 'lib-1', collectionId: 'col-1', title: 'Favorites'},
  ];
  for (const route of allowed) {
    assert.equal(dockSuppressedForRoute(route), false, `expected ${route?.kind ?? 'undefined'} not to suppress dock`);
  }
});

async function layerFixture({isTV = false, width = isTV ? 1920 : 390, height = isTV ? 1080 : 844, expanded = false, starting = false}: {
  isTV?: boolean; width?: number; height?: number; expanded?: boolean; starting?: boolean;
} = {}) {
  const h = hooks(), dock = new PlaybackDock(fence.playback.scope);
  if (!starting) {
    dock.start({kind: 'video', fence, queue: null, positionSeconds: 120, playing: true, expanded});
  }
  let route: NativeShellRoute = {kind: 'shell', destination: {kind: 'home'}};
  let revision = 0;
  let navSnapshot: {authority: typeof authority; route: NativeShellRoute; destination: {kind: 'home'}; revision: number} = {authority, route, destination: {kind: 'home'}, revision};
  const navListeners = new Set<() => void>();
  const navigation = {
    subscribe: (listener: () => void) => {navListeners.add(listener); return () => {navListeners.delete(listener);};},
    getSnapshot: () => navSnapshot,
  };
  const current = {
    retired: false,
    authority,
    fence: starting ? undefined : fence,
    dock: dock.getSnapshot(),
    playback: {duration: 3600, phase: 'playing', listeningNotice: 'resumed'},
    subject: {},
    capabilities: {play: true, pause: true, stop: true, seek: true, previous: false, next: false, cast: false},
    ...(starting ? {starting: {key: 'start-1', display: {title: 'Dune', icon: 'i-film'}, stop() {}}} : {}),
  };
  const adapter = {
    getSnapshot: () => current,
    subscribe: () => () => {},
    Surface: 'ActualSurface',
    dock,
  };
  const morph = {dragging: {value: false}, dockX: {value: 0}, dockFade: {value: 1}};
  const theme = createTheme({isTV, width, height});
  const react = {...h.react, useLayoutEffect: h.react.useEffect, useId: () => 'dock-suppression'};
  react.default = react;
  const signalFacts = await screenModule(new URL('../src-next/screens/destinations/music/signal-facts.ts', import.meta.url), {
    '../../../platform/device-platform': {isTablet: width >= 600 && !isTV},
    react, 'react-native': {Platform: {OS: 'ios'}},
  });
  const {NativePlaybackLayer} = await screenModule(new URL('../src-next/shell/NativePlaybackLayer.tsx', import.meta.url), {
    '../platform/tv-keys': {useTVKeyEvent() {}},
    './StartTimedOut': {StartTimedOut: 'StartTimedOut'},
    '../kit/dock/StartingDock': {StartingDock: 'StartingDock'},
    './use-listening-song': {useListeningSong: () => ({song: undefined, pending: false, favorite() {}})},
    './listening-presentation': {captureListeningOpening, expandedListeningOccupant, localListeningOccupant},
    '../kit/player/ListeningSurface': {ListeningPagePreview: 'ListeningPagePreview', ListeningPageBackground: 'ListeningPageBackground'},
    '../pages/touch/player/AudioPlayerPage': {AudioPlayerPage: 'NowPlayingScreen'},
    '../pages/tv/player/ListeningRoute': {TVListeningRoute: 'TVListeningRoute'},
    '../kit/dock/InitialPicturePresentation': {InitialPicturePresentation: 'InitialPicturePresentation'},
    '../kit/dock/initial-picture-model': {initialPictureKey: () => 'initial-picture'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    react,
    'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, Image: 'Image', Platform: {OS: 'ios'}, View: 'View', useWindowDimensions: () => ({width, height})},
    'react-native-gesture-handler': {GestureDetector: 'GestureDetector'},
    '../../../../packages/player-core/src': {sameFence},
    'react-native-reanimated': {default: {View: 'Animated.View'}, useAnimatedStyle: (style: () => unknown) => style()},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 59, right: 0, bottom: 34, left: 0})},
    '../kit/focus': {focusTestID},
    './tv-focus': {tvFocusIds},
    '../kit/theme': {useTheme: () => theme},
    '../kit/dock': {DockHost: 'DockHost', PlayerMorph: 'PlayerMorph', bindNativeDock: () => () => {}, usePlayerMorph: () => morph},
    '../../../../packages/client-core/src/presentation/kit-model': {playbackIdentity: () => ({title: 'Dune'})},
    '../kit/dock/model': dockModel,
    './model': {authorityKey, sameAuthority},
    './playback': {dockSuppressedForRoute, nativeShellBackTransition},
    '../kit/player/NowPlayingLyrics': {NowPlayingLyrics: 'NowPlayingLyrics'},
    '../kit/player/ListeningNotice': {NativeListeningNotice: 'ListeningNotice'},
    '../screens/destinations/music/signal-path-model': signalPath,
    '../screens/destinations/music/signal-facts': signalFacts,
    '../kit/player/PlayOnSheets': {NativePlayOnSheets: 'PlayOnSheets'},
    '../platform/playback/equalizer': {nativeEqualizer},
    '../../../../packages/client-core/src/audio-effects': {listeningNoticeKey},
    './TouchPlaybackPreparation': {TouchPlaybackPreparation: 'TouchPlaybackPreparation'},
    './TVPlaybackPreparation': {TVPlaybackPreparation: 'TVPlaybackPreparation'},
    './watch-slot-layout': watchSlotLayout,
    './TVStartFailure': {TVStartFailure: 'TVStartFailure'},
    './TouchStartFailure': {TouchStartFailure: 'TouchStartFailure'},
  });
  const runtime = {
    navigation,
    text: {t: (key: string) => key},
    reportError() {},
    artwork: () => undefined,
    ambient: () => undefined,
  };
  const render = () => {
    const tree = h.render(() => NativePlaybackLayer({adapter, runtime, authority}));
    if (tree?.props?.ref) {
      tree.props.ref.current = {measureInWindow: (cb: (x: number, y: number) => void) => cb(0, 0)};
    }
    return tree;
  };
  const navigate = (next: NativeShellRoute) => {
    route = next;
    revision++;
    navSnapshot = {authority, route, destination: {kind: 'home' as const}, revision};
    for (const listener of navListeners) listener();
  };
  return {render, navigate};
}

test('NativePlaybackLayer suppresses DockHost and docked video picture on full-screen routes while keeping Surface mounted', async () => {
  const f = await layerFixture();
  let tree = f.render();
  // Measure the docked video slot on Home so pictureFence becomes active.
  const dockHost = () => nodes(tree).find(node => node.type === 'DockHost');
  const morphNode = () => nodes(tree).find(node => node.type === 'PlayerMorph');
  const renderSurface = () => {
    const childFn = morphNode().props.children[0];
    return childFn({bounds: {x: 16, y: 699, width: 80, height: 45}, docked: true, subtitlesVisible: false, surfaceActive: true});
  };
  dockHost().props.onVideoSlot({x: 16, y: 699, width: 80, height: 45});
  tree = f.render();

  assert.equal(dockHost().props.visible, true);
  assert.ok(sameFence(morphNode().props.fence, fence), 'docked video has pictureFence on shell home');
  assert.equal(renderSurface().type, 'ActualSurface');
  assert.equal(renderSurface().props.surfaceActive, true);
  assert.deepEqual(renderSurface().props.dockBounds, {x: 16, y: 699, width: 80, height: 45});

  // Suppress on profiles, servers, read, and channel — Surface stays mounted while picture and DockHost hide.
  for (const next of [
    {kind: 'profiles'} as const,
    {kind: 'servers'} as const,
    {kind: 'read', itemId: 'b1', sourceId: 's1'} as const,
    {kind: 'channel', sourceId: 's1', channelId: 'c1'} as const,
  ]) {
    f.navigate(next);
    tree = f.render();
    assert.equal(dockHost().props.visible, false, `DockHost hidden on ${next.kind}`);
    assert.equal(morphNode().props.fence, undefined, `docked pictureFence hidden on ${next.kind}`);
    const surface = renderSurface();
    assert.equal(surface.type, 'ActualSurface', `Surface stays mounted on ${next.kind}`);
    assert.equal(surface.props.surfaceActive, false, `Surface composition hidden on ${next.kind}`);
    assert.equal(surface.props.dockBounds, undefined, `dockBounds cleared while suppressed on ${next.kind}`);
    assert.equal(nodes(tree).some(node => node.type === 'ListeningNotice'), false, `ListeningNotice hidden on ${next.kind}`);
  }

  // Returning to title, settings, or shell restores DockHost and docked pictureFence immediately.
  for (const next of [
    {kind: 'title', entry} as const,
    {kind: 'shell', destination: {kind: 'settings'}} as const,
    {kind: 'shell', destination: {kind: 'home'}} as const,
  ]) {
    f.navigate(next);
    tree = f.render();
    assert.equal(dockHost().props.visible, true, `DockHost visible on ${next.kind}`);
    assert.ok(sameFence(morphNode().props.fence, fence), `docked pictureFence restored on ${next.kind}`);
    assert.equal(renderSurface().props.surfaceActive, true, `Surface composition restored on ${next.kind}`);
  }
});

test('NativePlaybackLayer gates StartingDock on suppressed touch routes', async () => {
  const f = await layerFixture({starting: true});
  let tree = f.render();
  assert.equal(nodes(tree).some(node => node.type === 'StartingDock'), true);

  f.navigate({kind: 'profiles'});
  tree = f.render();
  assert.equal(nodes(tree).some(node => node.type === 'StartingDock'), false);

  f.navigate({kind: 'shell', destination: {kind: 'home'}});
  tree = f.render();
  assert.equal(nodes(tree).some(node => node.type === 'StartingDock'), true);
});

test('NativePlaybackLayer keeps expanded video pictureFence active on channel route while hiding DockHost', async () => {
  const f = await layerFixture({expanded: true});
  f.navigate({kind: 'channel', sourceId: 's1', channelId: 'c1'});
  const tree = f.render();
  const dockHost = nodes(tree).find(node => node.type === 'DockHost');
  const morphNode = nodes(tree).find(node => node.type === 'PlayerMorph');
  assert.equal(dockHost.props.visible, false);
  assert.ok(sameFence(morphNode.props.fence, fence), 'expanded video picture is never suppressed on channel route');
});

test('NativePlaybackLayer does not apply touch route dock suppression on TV', async () => {
  const f = await layerFixture({isTV: true});
  f.navigate({kind: 'profiles'});
  const tree = f.render();
  const dockHost = nodes(tree).find(node => node.type === 'DockHost');
  assert.equal(dockHost.props.visible, true);
});

test('dockBottomClear reserves touch mini-player clearance without changing resting or TV clearance', async () => {
  const h = hooks();
  const {dockBottomClear} = await screenModule(new URL('../src-next/kit/frames/metrics.ts', import.meta.url), {
    react: {...h.react, createContext: (value: unknown) => ({value})},
    'react-native': {useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, right: 0, bottom: 34, left: 0})},
    '../dock/DockClearance': {useDockClearance: () => 0},
    '../theme': {useTheme: () => createTheme({isTV: false, width: 390, height: 844})},
    '../layout/page-inset': {usePageWidth: () => 390},
  });
  assert.equal(dockBottomClear('phone', 12, 0, 34), 12);
  assert.equal(dockBottomClear('medium', 12, 0, 20), 12);
  assert.equal(dockBottomClear('expanded', 88, 0, 20), 88);
  assert.equal(dockBottomClear('tv', 56, 154, 0), 56);
  // Phone: tab bar is 84 (insets.bottom=34), dock top is 154 -> 70 above tab bar -> 12 + 70 = 82.
  assert.equal(dockBottomClear('phone', 12, 154, 34), 82);
  // Medium tablet: dock top is 84 -> 84 + 12 = 96.
  assert.equal(dockBottomClear('medium', 12, 84, 20), 96);
  // Expanded tablet: dock top is 100 -> 100 + 12 = 112 (beats base 88).
  assert.equal(dockBottomClear('expanded', 88, 100, 20), 112);
});
