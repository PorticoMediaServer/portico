// @fabric-react
import {DockVideoExitImages} from '../src-next/kit/dock/video-exit-paint.ts';
import {listeningCoverFixture} from './helpers/screen-harness.mjs';
import {captureListeningOpening, expandedListeningOccupant, localListeningOccupant} from '../src-next/shell/listening-presentation.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {PlaybackDock, playbackFence, sameFence} from '../../../packages/player-core/src';
import * as dockModel from '../src-next/kit/dock/model.ts';
import {nativeEqualizer} from '../src-next/platform/playback/equalizer.ts';
import * as audioEffects from '../../../packages/client-core/src/audio-effects';
import * as i18n from '../../../packages/i18n/src';
import * as signalPath from '../src-next/screens/destinations/music/signal-path-model.ts';
import {nativeShellBackTransition} from '../src-next/shell/playback.ts';
import {authorityKey, sameAuthority} from '../src-next/shell/model.ts';
import {NativeVideoSnapshotCapture} from '../src-next/shell/video-snapshot.ts';
import * as watchSlotLayout from '../src-next/shell/watch-slot-layout.ts';
import {NativeWatchOutlet, type NativeExpandedVideoSlot, type NativeWatchReader} from '../src-next/shell/watch-outlet.ts';
import {resolvePlayerPresentation, playerControlsRevealDeadline} from '../src-next/platform/layout/player-presentation.ts';
import {NativeWatchControls} from '../src-next/screens/watch/controls.ts';
import {nativeWatchTVPresentation} from '../src-next/screens/watch/tv-presentation.ts';
import {focusTestID} from '../src-next/kit/focus/registry.ts';
import {tvFocusIds} from '../src-next/shell/tv-focus.ts';
import type {NativeWatchPictureSlot} from '../src-next/screens/watch/picture-slot.tsx';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {groupPort, subscribeGroupPort} from '../src-next/screens/settings-live/group-port.ts';
import {useListeningSong} from '../src-next/shell/use-listening-song.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});
const window = {width: 840, height: 880};
const slot: NativeExpandedVideoSlot = {authority, fence, layoutRevision: 1, window, frameInWindow: {x: 0, y: 32, width: 840, height: 472.5}};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : React.isValidElement<any>(node) ? [node, ...nodes(node.props.children)] : [];

function reader() {
  let value: NativeExpandedVideoSlot | undefined;
  const listeners = new Set<() => void>();
  const source: NativeWatchReader = {
    getSnapshot() {assert.strictEqual(this, source); return value;},
    subscribe(listener) {assert.strictEqual(this, source); listeners.add(listener); return () => {listeners.delete(listener);};},
  };
  return {source, get subscriptions() {return listeners.size;}, async publish(next?: NativeExpandedVideoSlot, notify = true) {value = next; await act(() => {if (notify) for (const listener of listeners) listener();});}};
}

async function layer(withWatch = true, canonical?: NativeWatchReader, options: {os?: string; tv?: boolean; capture?: (source: any) => Promise<any>} = {}) {
  const outlet = reader(), dock = new PlaybackDock(fence.playback.scope), transitions: any[] = [];
  dock.start({kind: 'video', fence, queue: null, positionSeconds: 540, playing: true, expanded: true});
  let dimensions = window, current: any, tree: any;
  const refresh = () => {current = {authority, fence, dock: dock.getSnapshot(), playback: {duration: 5700}, subject: {}, capabilities: {}};};
  refresh();
  const adapter = {getSnapshot: () => current, subscribe: () => () => {}, Surface: 'ActualSurface', dock,
    captureVideoSnapshot: options.capture ?? (async () => undefined), onTransition: async (value: any) => {transitions.push(value);}};
  const callbacks: ((x: number, y: number) => void)[] = [];
  let node: any = {measureInWindow: (callback: any) => {callbacks.push(callback);}};
  const morph = {dragging: {value: false}, dockX: {value: 0}, dockFade: {value: 1}};
  const signalFacts = await screenModule(new URL('../src-next/screens/destinations/music/signal-facts.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: options.os ?? 'android'}},
  });
  const musicSignal = await screenModule(new URL('../src-next/screens/destinations/music/MusicSignal.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Pressable: 'Pressable', ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop', Path: 'Path'},
    '../../../../../../packages/i18n/src': i18n, './signal-path-model': signalPath,
    '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../../kit/overlays': {SidePanel: 'SidePanel'},
    '../../../kit/theme': {useTheme: () => {throw Error('unexpected_music_signal_render');}, nativeType: () => {throw Error('unexpected_music_signal_render');}},
  });
  const {NativePlaybackLayer} = await screenModule(new URL('../src-next/shell/NativePlaybackLayer.tsx', import.meta.url), {'./StartTimedOut': {StartTimedOut: 'StartTimedOut'}, '../kit/dock/StartingDock': {StartingDock: 'StartingDock'}, 
    './listening-presentation': {captureListeningOpening, expandedListeningOccupant, localListeningOccupant},
    '../kit/dock/video-exit-paint': {DockVideoExitImages},
    '../kit/dock/DockHandover': {DockVideoExitProvider: 'DockVideoExitProvider', DockVideoExitCanvas: 'DockVideoExitCanvas'},
    '../kit/dock/InitialPicturePresentation': {InitialPicturePresentation: 'InitialPicturePresentation'},
    '../kit/dock/initial-picture-model': {initialPictureKey: () => 'initial-picture'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    react: {...React, default: React}, 'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, Image: 'Image', Platform: {OS: options.os ?? 'android'}, View: 'View', useWindowDimensions: () => dimensions},
    'react-native-gesture-handler': {GestureDetector: 'GestureDetector'}, '../../../../packages/player-core/src': {sameFence},
    'react-native-reanimated': {default: {View: 'Animated.View'}, useAnimatedStyle: (style: () => unknown) => style()},
    '../kit/focus': {focusTestID}, './tv-focus': {tvFocusIds}, '../kit/theme': {useTheme: () => ({isTV: options.tv === true, layers: {player: 30}})},
    '../kit/dock': {DockHost: 'DockHost', PlayerMorph: 'PlayerMorph', bindNativeDock: () => () => {}, usePlayerMorph: () => morph},
    '../../../../packages/client-core/src/presentation/kit-model': {playbackIdentity: () => ({title: 'Fixture'})},
    '../kit/dock/model': dockModel,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, right: 0, bottom: 0, left: 0})},
    '../screens/destinations/music/NowPlayingScreen': {NowPlayingScreen: 'NowPlayingScreen', ListeningPagePreview: 'ListeningPagePreview', ListeningPageBackground: 'ListeningPageBackground', TVListeningRoute: 'TVListeningRoute'},
    '../screens/destinations/music/NowPlayingLyrics': {NowPlayingLyrics: 'NowPlayingLyrics'},
    '../screens/destinations/music/ListeningNotice': {NativeListeningNotice: 'NativeListeningNotice'},
    '../screens/destinations/music/MusicSignal': musicSignal,
    '../screens/destinations/music/signal-facts': signalFacts,
    '../platform/playback/equalizer': {nativeEqualizer}, '../../../../packages/client-core/src/audio-effects': audioEffects,
    './model': {authorityKey, sameAuthority}, './playback': {nativeShellBackTransition}, './video-snapshot': {NativeVideoSnapshotCapture}, './watch-slot-layout': watchSlotLayout, './TVStartFailure': {TVStartFailure: 'TVStartFailure'}, './TouchStartFailure': {TouchStartFailure: 'TouchStartFailure'},
    '../screens/watch/PlayOnSheets': {NativePlayOnSheets: 'PlayOnSheets'}, './use-listening-song': {useListeningSong},
    './TouchPlaybackPreparation': {TouchPlaybackPreparation: 'TouchPlaybackPreparation'},
    './TVPlaybackPreparation': {TVPlaybackPreparation: 'TVPlaybackPreparation'},
  });
  let runtime: any = {text: {}, reportError: (error: unknown) => {throw error;}, artwork: () => undefined, ambient: () => undefined, watch: withWatch ? canonical ?? outlet.source : undefined};
  const host = createFabricHookHost(() => {tree = NativePlaybackLayer({adapter, runtime, authority}); if (tree) tree.props.ref.current = node;});
  await host.mount(); await act(() => {});
  const picture = () => nodes(tree).find(item => item.type === 'PlayerMorph').props;
  const surface = () => {
    const props = picture(), frame = props.docked ? props.dockFrame : props.fullFrame;
    return props.children({bounds: {x: 0, y: 0, width: frame.width, height: frame.height}, docked: props.docked, subtitlesVisible: true, surfaceActive: true});
  };
  return {host, outlet, dock, adapter, callbacks, transitions, picture, surface, get tree() {return tree;},
    async render() {refresh(); await act(() => host.rerender());},
    async resize(next: typeof window) {dimensions = next; await act(() => host.rerender());},
    async replaceReader(next: Awaited<ReturnType<typeof reader>>) {runtime = {...runtime, watch: next.source}; await act(() => host.rerender());},
    setCurrent(patch: any) {current = {...current, ...patch};},
    detachHost() {node = null; tree.props.ref.current = null;},
    async close() {await host.unmount();},
  };
}

for (const os of ['android', 'ios']) {
  test(`${os} persistent layer captures the fenced source and admits only its decoded retained image`, async () => {
    let captures = 0, released = 0;
    const d = await layer(true, undefined, {os, capture: async source => {
      captures++; assert.ok(sameFence(source, fence));
      return {fence: source, image: {uri: 'file:///native-current-frame.jpg', width: 890, height: 501}, release: async () => {released++;}};
    }});
    try {
      const surface = d.surface();
      await act(async () => {await d.picture().prepareLayout(fence);});
      assert.equal(captures, 1);
      assert.equal(d.picture().retainedSnapshot.ready, false);
      assert.equal(d.picture().retainedSnapshot.failed, false);
      assert.equal(d.picture().retainedSnapshot.image.props.resizeMode, 'contain');
      await act(() => d.picture().retainedSnapshot.image.props.onLoad());
      assert.equal(d.picture().retainedSnapshot.ready, true);
      assert.strictEqual(d.surface().type, surface.type);
      assert.strictEqual(d.surface().key, surface.key);
      assert.deepEqual(d.transitions, []);
      await act(() => d.picture().onPresentationChange({fence, docked: false, moving: false, hideSubtitles: false}));
      assert.equal(d.picture().retainedSnapshot, undefined);
      assert.equal(released, 1);
      await act(async () => {await d.picture().prepareLayout(fence);});
      const staleImage = d.picture().retainedSnapshot.image;
      const replacement = {...fence, generation: 2};
      d.setCurrent({fence: replacement});
      await act(async () => {await d.host.rerender(); staleImage.props.onLoad();});
      assert.equal(d.picture().retainedSnapshot, undefined, 'a late decode cannot cover a replacement source');
      assert.equal(released, 2);
      assert.deepEqual(d.transitions, []);
    } finally {await d.close();}
  });

  test(`${os} touch preparation uses the existing fenced capture and TV never starts it`, async () => {
    for (const tv of [false, true]) {
      let captures = 0, released = 0;
      const d = await layer(true, undefined, {os, tv, capture: async source => {
        captures++; return {fence: source, image: {uri: 'file:///native-current-frame.jpg', width: 840, height: 472}, release: async () => {released++;}};
      }});
      try {
        if (!tv) {
          await d.outlet.publish(slot); await act(() => d.callbacks.shift()!(0, 0));
          const target = nodes(d.tree).find(node => node.type === 'GestureDetector').props.children;
          await act(() => target.props.onTouchStart());
          assert.equal(captures, 1);
          await act(() => d.picture().retainedSnapshot.image.props.onError());
          assert.equal(d.picture().retainedSnapshot.failed, true);
          assert.equal(d.picture().retainedSnapshot.ready, false);
          await act(() => target.props.onTouchEnd());
          assert.equal(released, 1);
        } else {
          await act(async () => {await d.picture().prepareLayout(fence);});
          await act(() => nodes(d.tree).find(node => node.props.onTouchStart)?.props.onTouchStart());
          assert.equal(captures, 0);
        }
        assert.deepEqual(d.transitions, []);
      } finally {await d.close();}
    }
  });
}

test('watch consumption waits for its accepted measured picture, converts origin and scopes pan away from details', async () => {
  const d = await layer();
  try {
    const type = d.surface().type;
    assert.equal(d.surface().props.expandedBounds, undefined);
    assert.equal(nodes(d.tree).filter(node => node.type === 'GestureDetector').length, 0);
    await d.outlet.publish(slot);
    assert.equal(d.callbacks.length, 1);
    assert.equal(d.surface().props.expandedBounds, undefined);
    await act(() => d.callbacks.shift()!(4, -45));
    assert.deepEqual(d.picture().fullFrame, {x: -4, y: 77, width: 840, height: 472.5});
    assert.deepEqual(d.surface().props.expandedBounds, {x: 0, y: 0, width: 840, height: 472.5});
    assert.deepEqual(nodes(d.tree).find(node => node.type === 'GestureDetector').props.children.props.style,
      {position: 'absolute', left: -4, top: 77, width: 840, height: 472.5});
    const panel = {...slot, layoutRevision: 2, frameInWindow: {x: 0, y: 0, width: 445, height: 626}};
    await d.outlet.publish(panel);
    assert.deepEqual(d.picture().fullFrame, {x: -4, y: 77, width: 840, height: 472.5}, 'internal geometry is retained, never promoted while measuring');
    assert.equal(d.surface().props.expandedBounds, undefined);
    await act(() => d.callbacks.shift()!(0, 0));
    assert.deepEqual(d.picture().fullFrame, panel.frameInWindow);
    assert.equal(nodes(d.tree).find(node => node.type === 'GestureDetector').props.children.props.style.width, 445);
    await d.outlet.publish();
    assert.equal(d.surface().props.expandedBounds, undefined);
    assert.equal(nodes(d.tree).filter(node => node.type === 'GestureDetector').length, 0);
    assert.strictEqual(d.surface().type, type);
    assert.deepEqual(d.transitions, []);
  } finally {await d.close();}
  assert.equal(d.outlet.subscriptions, 0);
});

test('the measured full-window pane retains letterbox space in native bounds and the expanded gesture', async () => {
  const d = await layer(), wide = {width: 890, height: 626};
  try {
    await d.resize(wide);
    const layout = resolvePlayerPresentation({window: wide, posture: {status: 'unavailable', window: null, folds: []}, docked: false, panelOpen: false})!;
    assert.equal(layout.picture!.height, 500.625);
    await d.outlet.publish({...slot, window: wide, frameInWindow: layout.surface!});
    await act(() => d.callbacks.shift()!(0, 0));
    assert.deepEqual(d.picture().fullFrame, {x: 0, y: 0, width: 890, height: 626});
    assert.deepEqual(d.surface().props.expandedBounds, {x: 0, y: 0, width: 890, height: 626});
    assert.deepEqual(nodes(d.tree).find(node => node.type === 'GestureDetector').props.children.props.style,
      {position: 'absolute', left: 0, top: 0, width: 890, height: 626});
    assert.deepEqual(d.transitions, []);
  } finally {await d.close();}
});

test('the checked watch measurement publishes its full pane through the canonical outlet and common consumer', async () => {
  const wide = {width: 890, height: 626}, current = () => ({authority, fence, window: wide});
  const outlet = new NativeWatchOutlet(current), d = await layer(true, outlet);
  const callbacks: ((x: number, y: number, width: number, height: number) => void)[] = [];
  let picture: any;
  const {NativeWatchPicture} = await screenModule(new URL('../src-next/screens/watch/picture-slot.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', useWindowDimensions: () => wide},
    '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
  });
  const view = {measureInWindow(callback: typeof callbacks[number]) {callbacks.push(callback);}};
  const owner = createFabricHookHost(() => {
    picture = NativeWatchPicture({authority, fence, outlet, current, style: {width: wide.width, height: wide.height}});
    picture.props.ref.current = view;
  });
  await d.resize(wide);
  await owner.mount();
  try {
    const surfaceType = d.surface().type;
    assert.equal(picture.type, 'View');
    assert.equal(picture.props.children, undefined);
    await act(() => callbacks.shift()!(8, 24, 890, 626));
    const measured: NativeWatchPictureSlot = outlet.getSnapshot()!;
    assert.deepEqual(measured.frameInWindow, {x: 8, y: 24, width: 890, height: 626});
    await act(() => d.callbacks.shift()!(8, 24));
    assert.deepEqual(d.picture().fullFrame, {x: 0, y: 0, width: 890, height: 626});
    assert.deepEqual(d.surface().props.expandedBounds, {x: 0, y: 0, width: 890, height: 626});
    assert.strictEqual(d.surface().type, surfaceType);
    assert.deepEqual(d.transitions, []);
    await act(() => owner.unmount());
    assert.equal(outlet.getSnapshot(), undefined);
    assert.equal(d.surface().props.expandedBounds, undefined);
    assert.strictEqual(d.surface().type, surfaceType, 'retiring the watch producer cannot retire the player');
  } finally {await owner.unmount(); await d.close(); outlet.dispose();}
});

test('actual WatchHost panel/control geometry reaches the persistent layer through its measured canonical pane', async () => {
  const wide = {width: 890, height: 626};
  let posture: any = {status: 'unavailable', window: null, folds: []}, root: any, measured: any;
  const outlet = new NativeWatchOutlet(() => ({authority, fence, window: wide}));
  const d = await layer(true, outlet, {os: 'ios'}), controls = new NativeWatchControls(false);
  const measurements: ((x: number, y: number, width: number, height: number) => void)[] = [];
  const {NativeWatchHost} = await screenModule(new URL('../src-next/screens/watch/WatchHost.tsx', import.meta.url), {
    '../../kit/dock/PlayerMorphBridge': {useInitialPlayerPaint: () => undefined},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, react: {...React, default: React}, 'react-native': {View: 'View', useWindowDimensions: () => wide, Dimensions: {get: () => wide},
      StatusBar: {pushStackEntry: () => ({}), popStackEntry() {}}},
    '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
    '../../platform/layout/player-presentation': {resolvePlayerPresentation, playerControlsRevealDeadline, playerPostureCrossFadeMs: 250},
    '../../platform/platformPosture': {usePlatformPosture: () => posture},
    './WatchChrome': {NativeWatchChrome: 'Chrome'}, './picture-slot': {NativeWatchPicture: 'Picture'},
    './HandheldMiniGuide': {NativeHandheldMiniGuide: 'HandheldMiniGuide'},
    './live-guide': {noLiveGuideSubscription: () => () => {}, emptyLiveGuide: (() => {const snapshot = {open: false, rows: []}; return () => snapshot;})()},
    './tv-presentation': {nativeWatchTVPresentation},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useSharedValue: (value: any) => React.useRef({value}).current, useAnimatedStyle: (run: any) => run(),
      withTiming: (value: any) => value, cancelAnimation: () => {}},
    '../../kit/useMotion': {useMotion: () => ({reduce: false, timing: () => ({duration: 0})})},
    '../settings-live/group-port': {groupPort, subscribeGroupPort}, '../../kit/focus': {useOptionalFocusRegistry: () => undefined},
    '../../kit/useScreenReader': {useScreenReader: () => false},
    '../../kit/overlays/TVPanelMotion': {TVPanelTransition: 'TVPanelTransition', TVPanelSlide: 'TVPanelSlide', TVPanelScrim: 'TVPanelScrim', useTVPanelPresence: () => ({open: false, value: undefined, exited() {}})},
  });
  const {NativeWatchPicture} = await screenModule(new URL('../src-next/screens/watch/picture-slot.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', useWindowDimensions: () => wide},
    '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
  });
  const view = {measureInWindow(callback: typeof measurements[number]) {measurements.push(callback);}};
  const setPresentation = (context: any) => controls.source(fence, {...context, expanded: d.adapter.getSnapshot().dock.local?.expanded === true, paused: false});
  const bindings = {chrome: () => ({}), panel: () => null};
  const watch = createFabricHookHost(() => {root = NativeWatchHost({authority, current: () => authority, playback: d.adapter as any, outlet, runtime: {text: {}} as any, controls, bindings: bindings as any, setPresentation});});
  const picture = createFabricHookHost(() => {measured = NativeWatchPicture(root.props.children[0].props); measured.props.ref.current = view;});
  const measure = async (width: number, height: number) => {
    await act(async () => {await picture.rerender(); measured.props.onLayout(); measurements.at(-1)!(0, 0, width, height);});
    await act(() => d.callbacks.at(-1)!(0, 0));
  };
  try {
    await d.resize(wide); await watch.mount(); await picture.mount();
    await measure(890, 626);
    const surface = d.surface();
    assert.deepEqual(d.picture().fullFrame, {x: 0, y: 0, width: 890, height: 626});
    posture = {status: 'available', window: wide, folds: [{bounds: {left: 445, right: 445, top: 0, bottom: 626}, orientation: 'vertical', state: 'flat'}]};
    await act(() => watch.rerender());
    await act(() => {controls.open('episodes', fence);});
    assert.equal(controls.getSnapshot().visible, false, '3d panel suppression applies before fold reveal');
    const panel = nodes(root).find(node => node.props.testID === 'watch-panel-pane');
    assert.equal(panel.props.style.left, 445); assert.equal(panel.props.style.width, 445);
    await measure(445, 626);
    assert.deepEqual(d.picture().fullFrame, {x: 0, y: 0, width: 445, height: 626});
    await act(async () => {controls.closePanel(fence); posture = {...posture, folds: [{...posture.folds[0], state: 'half-opened'}]}; await watch.rerender();});
    await measure(445, 250.3125);
    assert.deepEqual(d.picture().fullFrame, {x: 0, y: 0, width: 445, height: 250.3125});
    const chrome = nodes(root).find(node => node.type === 'Chrome');
    assert.equal(chrome.props.edgeInsets.right, 16);
    assert.strictEqual(d.surface().type, surface.type); assert.strictEqual(d.surface().key, surface.key);
    assert.deepEqual(d.transitions, []);
    await act(() => {d.dock.collapse(fence);}); await d.render(); await act(() => watch.rerender());
    assert.equal(root, null);
    // This hook driver mounts child bodies explicitly; mirror the real root's
    // removal of its measurement child when WatchHost returns null.
    await act(() => picture.unmount());
    assert.equal(outlet.getSnapshot(), undefined);
    assert.equal(d.surface().props.docked, true); assert.strictEqual(d.surface().type, surface.type);
    assert.deepEqual(d.transitions, []);
  } finally {await picture.unmount(); await watch.unmount(); controls.dispose(); await d.close(); outlet.dispose();}
});

test('watch origin callbacks reject replacement slot, source, authority, window, host, reader, dock and unmount before notification', async () => {
  for (const stop of ['slot', 'source', 'authority', 'window', 'host', 'reader', 'dock', 'unmount']) {
    const d = await layer(); let closed = false;
    try {
      await d.outlet.publish(slot);
      const late = d.callbacks.shift()!;
      if (stop === 'slot') await d.outlet.publish({...slot, layoutRevision: 2}, false);
      else if (stop === 'source') d.setCurrent({fence: {...fence, generation: 2}});
      else if (stop === 'authority') d.setCurrent({authority: {...authority, viewerFence: 'retired'}});
      else if (stop === 'window') await d.resize({width: 880, height: 840});
      else if (stop === 'host') d.detachHost();
      else if (stop === 'reader') {const next = reader(); await next.publish(slot); await d.replaceReader(next);}
      else if (stop === 'dock') {d.dock.collapse(fence); await d.render();}
      else {await d.close(); closed = true;}
      await act(() => late(0, -45));
      if (!closed) assert.equal(d.surface().props.expandedBounds, undefined, stop);
      assert.deepEqual(d.transitions, []);
    } finally {if (!closed) await d.close();}
  }
});

test('only the newest persistent-host origin measurement can move the same watch picture', async () => {
  const d = await layer();
  try {
    await d.outlet.publish(slot); await act(() => d.callbacks.shift()!(0, 0));
    d.tree.props.onLayout(); d.tree.props.onLayout();
    await act(() => d.callbacks.shift()!(0, -40));
    assert.equal(d.picture().fullFrame.y, 32);
    await act(() => d.callbacks.shift()!(0, -45));
    assert.equal(d.picture().fullFrame.y, 77);
  } finally {await d.close();}
});

test('docking keeps its measured slot while expanded watch layout is invalidated', async () => {
  const d = await layer();
  try {
    await d.outlet.publish(slot); await act(() => d.callbacks.shift()!(0, 0));
    d.dock.collapse(fence); await d.render();
    const dockFrame = {x: 16, y: 720, width: 80, height: 45};
    nodes(d.tree).find(node => node.type === 'DockHost').props.onVideoSlot(dockFrame);
    await act(() => d.callbacks.shift()!(0, 0));
    await d.outlet.publish();
    assert.equal(d.picture().docked, true);
    assert.deepEqual(d.picture().dockFrame, dockFrame);
    assert.equal(d.surface().props.docked, true);
    assert.deepEqual(d.surface().props.dockBounds, {x: 0, y: 0, width: 80, height: 45});
    assert.equal(d.callbacks.length, 0);
  } finally {await d.close();}
});

test('old fixture runtimes without a watch reader preserve the existing full-window behavior', async () => {
  const d = await layer(false);
  try {
    assert.deepEqual(d.picture().fullFrame, {x: 0, y: 0, width: 840, height: 880});
    assert.deepEqual(d.surface().props.expandedBounds, {x: 0, y: 0, width: 840, height: 880});
    assert.deepEqual(nodes(d.tree).find(node => node.type === 'GestureDetector').props.children.props.style, {position: 'absolute', inset: 0});
    assert.equal(d.callbacks.length, 0);
  } finally {await d.close();}
});
