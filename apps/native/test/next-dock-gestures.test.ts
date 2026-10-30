// @fabric-react
import {DockVideoExitImages} from '../src-next/kit/dock/video-exit-paint.ts';
import {dockHandoverFixture} from './helpers/screen-harness.mjs';
import {listeningCoverFixture} from './helpers/screen-harness.mjs';
import {captureListeningOpening, expandedListeningOccupant, localListeningOccupant} from '../src-next/shell/listening-presentation.ts';
import * as signalPath from '../src-next/screens/destinations/music/signal-path-model.ts';
import {nativeEqualizer} from '../platform/equalizer.ts';
import {listeningNoticeKey} from '../../../packages/client-core/src/audio-effects.ts';
import test, {mock} from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {PlaybackDock, sameFence, type ChannelPlaybackFence, type DockPlayback, type QueuePlaybackFence, type QueueScope} from '../../../packages/player-core/src/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import * as model from '../src-next/kit/dock/model.ts';
import {currentMorphFence} from '../src-next/kit/dock/morph-fence.ts';
import {playerPostureCrossFadeMs} from '../src-next/platform/layout/player-presentation.ts';
import * as watchSlotLayout from '../src-next/shell/watch-slot-layout.ts';
import {focusTestID} from '../src-next/kit/focus/registry.ts';
import {tvFocusIds} from '../src-next/shell/tv-focus.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule as transformModule} from './helpers/screen-harness.mjs';
import {useListeningSong} from '../src-next/shell/use-listening-song.ts';

// The same tests can read an immutable git-show base mirror for red evidence.
const screenModule = (file: URL, modules: any) => transformModule(process.env.NATIVE_DOCK_BASE_DIR && file.pathname.includes('/src-next/')
  ? new URL('file://' + process.env.NATIVE_DOCK_BASE_DIR + file.pathname.slice(file.pathname.indexOf('/src-next/'))) : file, modules);

// Real hook/component bodies and gesture factory callbacks. Controlled UI/JS
// queues establish ownership, not RNGH native scheduling or actual motion.
const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
const initial = {fence, kind: 'video', queue: null, positionSeconds: 75, playing: true, expanded: true} satisfies DockPlayback;
const channelFence: ChannelPlaybackFence = {kind: 'channel', playback: {scope, run: 1}, sessionId: 'live-session', channelId: 'channel', channelGeneration: '1', channelKind: 'live-source', sourceId: 'live-source', generation: 1};
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 1, viewerFence: 'viewer'};
const linear = (value: number) => value;
type Animation = {to: any; config?: any; complete?: (finished: boolean) => void; apply?: () => void; sample?: (value: any) => void; owner?: any; cancelled?: boolean};
function elements(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  // Presentation slots are materialised JSX, not evaluated component bodies.
  return React.isValidElement<any>(node) ? [node, ...['children', 'row', 'opener', 'art', 'words', 'controls', 'tools', 'line'].flatMap(key => elements(node.props[key]))] : [];
}
async function rig({reduced = false, docked = false, kind = 'video', os = 'android', tv = false, paused = false, controlledClock = false}: {reduced?: boolean; docked?: boolean; kind?: 'video' | 'live' | 'music'; os?: string; tv?: boolean; paused?: boolean; controlledClock?: boolean} = {}) {
  const animations: Animation[] = [], cancelled: any[] = [], js: (() => void)[] = [], actions: any[] = [], transitions: any[] = [], requests: number[] = [];
  const reactions: any[] = [], measurements: any[] = [], ui: (() => void)[] = [];
  const flushUI = () => {while (ui.length) ui.shift()!();};
  let now = 0, parentRenders = 0;
  const clock = controlledClock ? mock.method(performance, 'now', () => now) : undefined;
  const presentations: any[] = [];
  const runtimeLife = new AbortController(), backCallbacks: (() => boolean)[] = [];
  const activeBack = new Set<() => boolean>();let shellReads = 0;
  const useSharedValue = (initialValue: any) => {
    const ref = React.useRef<any>(null);
    if (!ref.current) {
      let value = initialValue, pending: Animation | undefined;
      const cell = {get value() {return value;}, set value(next: any) {
        if (pending) pending.cancelled = true;
        pending = undefined;
        if (next && typeof next === 'object' && '__animation' in next) {
          pending = next; next.owner = cell; next.apply = () => {value = next.to;}; next.sample = (sample: any) => {value = sample;};
        } else value = next;
      }, cancel() {if (pending) pending.cancelled = true; pending = undefined;}};
      ref.current = cell;
    }
    return ref.current;
  };
  const animate = (to: any, config: any, complete?: Animation['complete']) => {const value = {__animation: true, to, config, complete}; animations.push(value); return value;};
  const animated = {default: {View: 'AnimatedView'}, useSharedValue, useAnimatedStyle: (read: () => any) => ({read}),
    withTiming: animate, withSpring: animate, withDelay: (_: any, animation: any) => animation,
    cancelAnimation: (value: any) => {cancelled.push(value); value.cancel?.();},
    runOnUI: (fn: (...args: any[]) => void) => (...args: any[]) => ui.push(() => fn(...args)),
    runOnJS: (fn: (...args: any[]) => void) => (...args: any[]) => js.push(() => fn(...args)),
    useAnimatedReaction: (read: () => any, callback: (next: any, previous: any) => void) => {
      const cell = React.useRef<any>({previous: null}); cell.current.read = read; cell.current.callback = callback;
      React.useEffect(() => {const reaction = cell.current; reactions.push(reaction); return () => {reactions.splice(reactions.indexOf(reaction), 1);};}, []);
    },
  };
  const pan = () => {
    const callbacks: any = {}, config: any = {};
    const result: any = {callbacks, config};
    for (const name of ['onBegin', 'onStart', 'onUpdate', 'onEnd', 'onFinalize']) result[name] = (fn: any) => {callbacks[name] = fn; return result;};
    for (const name of ['activeOffsetX', 'activeOffsetY', 'failOffsetX', 'failOffsetY', 'enabled', 'minDistance']) result[name] = (value: any) => {config[name] = value; return result;};
    return result;
  };
  const Gesture = {Pan: pan, Race: (...gestures: any[]) => ({gestures})};
  let motion = {reduce: reduced, transforms: !reduced,
    timing: () => reduced ? {duration: 160, easing: linear, reduceMotion: 'never'} : {duration: 180},
    spring: () => reduced ? undefined : {damping: 20}};
  const theme = {isTV: tv, layers: {player: 30}, colors: {text: '#fff', shadow: '#000'}, layout: {gutter: {tvRight: 96}}};
  const react = {...React, default: React};
  const native = {View: 'View', Text: 'Text', Image: 'Image', ActivityIndicator: 'ActivityIndicator', Platform: {OS: os, isTV: tv},
    NativeModules: {PorticoHaptics: {selection: async () => {requests.push(now); return true;}}},
    BackHandler: {addEventListener: (_event: string, callback: () => boolean) => {backCallbacks.push(callback);activeBack.add(callback);return {remove() {activeBack.delete(callback);}};}}, useWindowDimensions: () => ({width: 400, height: 800})};
  const visual = {alpha: () => '#000', DockFacts: 'DockFacts', dockStyles: {fill: {position: 'absolute', inset: 0}}, dockType: () => ({})};
  const common = {'./ListeningCoverBridge': listeningCoverFixture, './DockHandover': dockHandoverFixture, react, 'react-native': native, 'react-native-reanimated': animated, 'react-native-gesture-handler': {Gesture, GestureDetector: 'GestureDetector'},
    '../../../../../packages/player-core/src/index': {sameFence}, '../theme': {useTheme: () => theme}, '../useMotion': {useMotion: () => motion},
    '../Icon': {Icon: 'Icon'}, './visual': visual, './model': model, '../../platform/layout/player-presentation': {playerPostureCrossFadeMs}};
  const {selectionHaptic} = await screenModule(new URL('../src-next/platform/haptics.ts', import.meta.url), {react, 'react-native': native});
  const {usePlayerMorph, PlayerMorph} = await screenModule(new URL('../src-next/kit/dock/PlayerMorph.tsx', import.meta.url), {...common, './morph-fence': {currentMorphFence}});
  const {useDockPresence} = await screenModule(new URL('../src-next/kit/dock/useDockPresence.ts', import.meta.url), common);
  const {MediaBar} = await screenModule(new URL('../src-next/kit/dock/MediaBar.tsx', import.meta.url), {...common,
    '../../../../../packages/i18n/src/index': {defaultI18n}, '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: () => ''},
    '../artwork': {Artwork: 'Artwork'}, '../controls': {Button: 'Button', Focusable: 'Focusable'}, '../overlays': {Popover: 'Popover'},
    '../../platform/native-blur': {BlurView: 'BlurView'}, '../../platform/haptics': {selectionHaptic}});
  const {DockHost} = await screenModule(new URL('../src-next/kit/dock/DockHost.tsx', import.meta.url), {...common,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})}, './MediaBar': {MediaBar}, './NowPlayingCard': {NowPlayingCard: 'NowPlayingCard'},
    './DockNotice': {DockNotice: 'DockNotice'}, './DockClearance': {useDockClearanceReport() {}}, './useDockPresence': {useDockPresence}});
  const playback: DockPlayback = kind === 'live' ? {...initial, kind, fence: channelFence, playing: !paused} : {...initial, kind, playing: !paused};
  const dock = new PlaybackDock(scope); dock.start({...playback, expanded: !docked});
  let snapshot: any;
  const Surface = () => null;
  const adapter = {dock, Surface, subscribe: () => () => {}, getSnapshot: () => snapshot, onTransition: async (transition: any) => {transitions.push(transition);}, captureVideoSnapshot: async () => undefined};
  const refresh = () => {const local = dock.getSnapshot().local; snapshot = {authority, dock: dock.getSnapshot(), fence: local?.fence,
    subject: {}, playback: {duration: 100, phase: paused ? 'paused' : 'playing'}, capabilities: {play: true, pause: true, stop: true, seek: true, previous: false, next: false, cast: false}};};
  refresh();
  const signalFacts = await screenModule(new URL('../src-next/screens/destinations/music/signal-facts.ts', import.meta.url), {react, 'react-native': native});
  const signalView = await screenModule(new URL('../src-next/screens/destinations/music/MusicSignal.tsx', import.meta.url), {react, 'react-native': native, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop', Path: 'Path'}, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../../kit/overlays': {SidePanel: 'SidePanel'}, '../../../kit/theme': {useTheme: () => theme, nativeType: () => ({})}, './signal-path-model': signalPath});
  const {NativePlaybackLayer} = await screenModule(new URL('../src-next/shell/NativePlaybackLayer.tsx', import.meta.url), {'./StartTimedOut': {StartTimedOut: 'StartTimedOut'}, '../kit/dock/StartingDock': {StartingDock: 'StartingDock'}, 
    './listening-presentation': {captureListeningOpening, expandedListeningOccupant, localListeningOccupant},
    '../kit/dock/video-exit-paint': {DockVideoExitImages},
    '../kit/dock/DockHandover': {DockVideoExitProvider: 'DockVideoExitProvider', DockVideoExitCanvas: 'DockVideoExitCanvas'},
    '../kit/dock/InitialPicturePresentation': {InitialPicturePresentation: 'InitialPicturePresentation'},
    '../kit/dock/initial-picture-model': {initialPictureKey: () => 'initial-picture'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    ...common, '../../../../packages/player-core/src': {sameFence}, '../kit/theme': {useTheme: () => theme},
    '../kit/focus': {focusTestID}, './tv-focus': {tvFocusIds},
    '../kit/dock/model': model, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, right: 0, bottom: 0, left: 0})},
    '../screens/destinations/music/NowPlayingScreen': {NowPlayingScreen: 'NowPlayingScreen', ListeningPagePreview: 'ListeningPagePreview', ListeningPageBackground: 'ListeningPageBackground', TVListeningRoute: 'TVListeningRoute'},
    '../screens/destinations/music/NowPlayingLyrics': {NowPlayingLyrics: 'NowPlayingLyrics'},
    '../screens/destinations/music/ListeningNotice': {NativeListeningNotice: 'ListeningNotice'},
    '../screens/destinations/music/MusicSignal': signalView, '../screens/destinations/music/signal-facts': signalFacts,
    '../screens/watch/PlayOnSheets': {NativePlayOnSheets: 'PlayOnSheets'}, './use-listening-song': {useListeningSong},
    './TouchPlaybackPreparation': {TouchPlaybackPreparation: 'TouchPlaybackPreparation'},
    './TVPlaybackPreparation': {TVPlaybackPreparation: 'TVPlaybackPreparation'},
    '../platform/playback/equalizer': {nativeEqualizer}, '../../../../packages/client-core/src/audio-effects': {listeningNoticeKey},
    '../kit/dock': {DockHost, PlayerMorph, usePlayerMorph, bindNativeDock: () => async (command: any) => {actions.push(command);}},
    '../../../../packages/client-core/src/presentation/kit-model': {playbackIdentity: () => ({id: 'item', kind: 'video', title: 'Film', subtitle: []})},
    './model': {sameAuthority: (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b)}, './playback': {nativeShellBackTransition: () => undefined},
    './watch-slot-layout': watchSlotLayout, './TVStartFailure': {TVStartFailure: 'TVStartFailure'}, './TouchStartFailure': {TouchStartFailure: 'TouchStartFailure'},
    './video-snapshot': {NativeVideoSnapshotCapture: class {matches() {return true;} async clear() {} async prepare() {}}},
  });
  const runtime = {retirementSignal: runtimeLife.signal, getSnapshot: () => {
    if (runtimeLife.signal.aborted) throw Object.assign(new Error('native_shell_cancelled'), {name: 'AbortError'});
    shellReads++;return {selectionActive: false, homeObscured: false};
  }, reportError: (error: any) => {throw error;}, text: {}, artwork() {}, ambient() {}};
  let tree: any, hostTree: any, barTree: any, pictureTree: any, hostProps: any, barProps: any, pictureProps: any;
  let barHost: Awaited<ReturnType<typeof createFabricHookHost>> | undefined, closed = false;
  let extraPicture: any = {}, extraBar: any = {};
  const layerHost = createFabricHookHost(() => {parentRenders++; tree = NativePlaybackLayer({adapter, runtime, authority});
    elements(tree).find(node => node.props.testID === 'native-persistent-playback').props.ref.current = {measureInWindow: (cb: any) => cb(0, 0)};
  });
  const dockHost = createFabricHookHost(() => {hostTree = DockHost(hostProps);});
  const onPresentationChange = (state: any) => {presentations.push(state); pictureProps.onPresentationChange?.(state);};
  const pictureHost = createFabricHookHost(() => {pictureTree = PlayerMorph({...pictureProps, ...extraPicture, onPresentationChange});});
  await layerHost.mount();
  hostProps = elements(tree).find(node => node.type === DockHost).props; await dockHost.mount();
  pictureProps = elements(tree).find(node => node.type === PlayerMorph).props; await pictureHost.mount();
  async function sync() {
    flushUI();
    await act(() => {});
    if (closed) return;
    hostProps = elements(tree).find(node => node.type === DockHost).props; await dockHost.rerender();
    pictureProps = elements(tree).find(node => node.type === PlayerMorph).props; await pictureHost.rerender();
    await act(() => {});
    const bar = elements(hostTree).find(node => node.type === MediaBar);
    if (bar) {
      barProps = bar.props;
      if (!barHost) {barHost = createFabricHookHost(() => {barTree = MediaBar({...barProps, ...extraBar});}); await barHost.mount();} else await barHost.rerender();
      await act(() => {});
    } else {await barHost?.unmount(); barHost = undefined; barTree = undefined;}
  }
  await sync();
  const controller = () => elements(tree).find(node => node.type === PlayerMorph).props.controller;
  const fullPan = () => elements(tree).find(node => node.type === 'GestureDetector').props.gesture;
  const aside = () => {const gesture = elements(barTree).find(node => node.type === 'GestureDetector').props.gesture; return gesture.gestures?.[0] ?? gesture;};
  const opener = () => elements(barTree).find(node => node.type === 'Focusable' && node.props.delayLongPress === 500).props;
  const flushJS = async () => {await act(() => {while (js.length) js.shift()!();}); await sync();};
  const complete = async (animation: Animation, force = false) => {if (!animation.cancelled || force) {animation.apply?.(); animation.complete?.(true);} await flushJS();};
  // The picture's own progress reaction is the one that reports stacking/goal
  // state; other hosts register their own reactions around it.
  const pictureReaction = () => reactions.filter(value => {const read = value.read(); return !!read && typeof read === 'object' && 'drag' in read;}).at(-1)!;
  const tickPicture = async () => {const reaction = pictureReaction(); const next = reaction.read(); reaction.callback(next, reaction.previous); reaction.previous = next; await flushJS();};
  const tick = async () => {for (const reaction of reactions) {const next = reaction.read(); reaction.callback(next, reaction.previous); reaction.previous = next;} await flushJS();};
  // Reanimated mappers are input-driven. No unchanged-progress tick may invent
  // a zero derivative, and only state updates scheduled by actual callbacks may
  // render here: sync() would rerender the parent and mask paused settlement.
  const flushMotionJS = () => act(() => {flushUI(); while (js.length) js.shift()!();});
  const motionTick = async (timestamp: number) => {
    now = timestamp; flushUI();
    const same = (a: any, b: any) => Object.is(a, b) || a && b && typeof a === 'object' && typeof b === 'object'
      && Object.keys(a).length === Object.keys(b).length && Object.keys(a).every(key => Object.is(a[key], b[key]));
    for (let pass = 0; pass < 8; pass++) {
      let changed = false;
      for (const reaction of reactions) {
        const next = reaction.read();
        if (same(next, reaction.previous)) continue;
        reaction.callback(next, reaction.previous); reaction.previous = next; changed = true;
      }
      await flushMotionJS();
      if (!changed) return;
    }
    throw new Error('motion reactions did not settle');
  };
  return {dock, adapter, Surface, animations, cancelled, requests, actions, transitions, presentations, controller, fullPan, aside, opener, flushJS, complete, tick, tickPicture, motionTick, flushMotionJS, measurements,
    get backCallback() {return backCallbacks.at(-1)!;}, get activeBackListeners() {return activeBack.size;},
    get shellReads() {return shellReads;}, retireRuntime() {runtimeLife.abort();},
    get parentRenders() {return parentRenders;},
    get tree() {return tree;}, get hostTree() {return hostTree;}, get barTree() {return barTree;}, get pictureTree() {return pictureTree;},
    async render() {refresh(); await layerHost.rerender(); await sync();},
    async refreshMotion() {motion = {...motion}; await layerHost.rerender(); await sync();},
    async replace(render = true) {
      const replacement = playback.kind === 'live'
        ? {...playback, fence: {...playback.fence, sourceId: 'replacement', generation: 2}}
        : {...playback, fence: {...playback.fence, sourceId: 'replacement', generation: 2}};
      dock.start({...replacement, expanded: !docked}); refresh(); if (render) {await layerHost.rerender(); await sync();}
    },
    async media(extra: any) {extraBar = extra; await barHost!.rerender(); await act(() => {});},
    async picture(extra: any) {extraPicture = extra; await pictureHost.rerender(); await act(() => {});},
    async layout() {const slot = elements(barTree).find(node => node.props.collapsable === false); slot.props.ref.current = {measureInWindow: (cb: any) => {measurements.push(true); cb(24, 700, 80, 45);}}; slot.props.onLayout(); await sync();},
    time(value: number) {now = value;},
    async close() {if (closed) return; closed = true; await barHost?.unmount(); await pictureHost.unmount(); await dockHost.unmount(); await layerHost.unmount(); clock?.mock.restore();},
  };
}
const vertical = {translationY: 600, velocityY: 1000}, horizontal = {translationX: 300, velocityX: 1000};
async function measuredPicture(r: Awaited<ReturnType<typeof rig>>, docked: boolean) {
  await r.layout();
  if (!docked) {r.dock.expand(fence); await r.render();}
  for (const animation of r.animations) if (!animation.cancelled) animation.apply?.();
  r.controller().velocity.value = 0; await r.tickPicture();
  for (const animation of r.animations) if (!animation.cancelled) animation.apply?.();
}

test('queued real morph release cannot collapse/expand replacement B in the same authority, with or without rerender', async () => {
  for (const docked of [false, true]) for (const render of [false, true]) {
    const r = await rig({docked});
    try {const pan = docked ? elements(r.barTree).find(node => node.type === 'GestureDetector').props.gesture.gestures[1] : r.fullPan();
      const event = docked ? {translationY: -600, velocityY: -1000} : vertical;
      pan.callbacks.onBegin(); pan.callbacks.onUpdate(event); pan.callbacks.onEnd(event, true);
      await r.replace(render); await r.flushJS();
      assert.equal(r.dock.getSnapshot().local?.expanded, !docked); assert.equal(r.transitions.length, 0);
    } finally {await r.close();}
  }
});
test('successful current release dispatches once, while transition replacement and unmount retire queued release', async () => {
  const r = await rig();
  try {const pan = r.fullPan(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(vertical); pan.callbacks.onEnd(vertical, true); await r.flushJS();
    assert.equal(r.dock.getSnapshot().local?.expanded, false); assert.equal(r.transitions.length, 1);
  } finally {await r.close();}
  for (const stop of ['unmount', 'transition', 'new-pan']) {const r = await rig();
    try {const pan = r.fullPan(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(vertical); pan.callbacks.onEnd(vertical, true);
      if (stop === 'unmount') await r.close(); else if (stop === 'new-pan') pan.callbacks.onBegin(); else {r.dock.collapse(fence); await r.render();}
      await r.flushJS(); assert.equal(r.transitions.length, 0, stop);
    } finally {await r.close();}
  }
});
test('ACTIVE unsuccessful vertical pan restores its goal without releasing, including reduced motion', async () => {
  for (const reduced of [false, true]) {const r = await rig({reduced});
    try {const pan = r.fullPan(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(vertical); pan.callbacks.onEnd(vertical, false); pan.callbacks.onFinalize(vertical, false); await r.flushJS();
      assert.equal(r.transitions.length, 0); assert.equal(r.dock.getSnapshot().local?.expanded, true); assert.equal(r.controller().dragging.value, false); assert.equal(r.animations.at(-1)!.to, 0, 'cancel returns to the current expanded goal');
    } finally {await r.close();}
  }
});
for (const success of [true, false]) test(`reduced vertical ${success ? 'release' : 'ACTIVE cancel'} uses the current memoized timing policy`, async () => {
  for (const docked of [false, true]) for (const refresh of [false, true]) {
    const r = await rig({reduced: true, docked});
    try {
      const initialTiming = r.animations.filter(value => value.owner === r.controller().progress).at(-1)!.config;
      if (refresh) await r.refreshMotion();
      const timing = r.animations.filter(value => value.owner === r.controller().progress).at(-1)!.config;
      if (refresh) assert.notStrictEqual(timing, initialTiming, 'a refreshed policy creates a new memoized config');
      const pan = docked ? elements(r.barTree).find(node => node.type === 'GestureDetector').props.gesture.gestures[1] : r.fullPan();
      const event = docked ? {translationY: -600, velocityY: -1000} : vertical;
      pan.callbacks.onBegin(); pan.callbacks.onUpdate(event);
      pan.callbacks.onEnd(event, success); pan.callbacks.onFinalize(event, success);
      const animation = r.animations.filter(value => value.owner === r.controller().progress).at(-1)!;
      assert.equal(animation.to, Number(success ? !docked : docked));
      assert.strictEqual(animation.config, timing, 'the gesture receives the hook memoized config, including after policy refresh');
      assert.deepEqual(animation.config, {duration: 160, easing: linear, reduceMotion: 'never'});
      assert.equal(animation.config.easing(.25), .25, 'reduced progress uses linear easing');
      assert.equal(typeof animation.complete, 'function', 'timing keeps the guarded completion');
      await r.flushJS(); assert.equal(r.transitions.length, success ? 1 : 0);
    } finally {await r.close();}
  }
});
test('unsuccessful aside cancels x AND fade; late completion cannot close in normal or reduced motion', async () => {
  for (const reduced of [false, true]) {const r = await rig({reduced, docked: true});
    try {const pan = r.aside(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(horizontal); pan.callbacks.onEnd(horizontal, false); pan.callbacks.onFinalize(horizontal, false);
      for (const animation of [...r.animations]) await r.complete(animation);
      await r.flushJS(); assert.deepEqual(r.actions, []);
      assert.ok(r.cancelled.includes(r.controller().dockX)); assert.ok(r.cancelled.includes(r.controller().dockFade));
    } finally {await r.close();}
  }
});
test('old aside dismiss completions and queued callbacks are retired on source, visibility and unmount', async () => {
  for (const reduced of [false, true]) for (const stop of ['source', 'visibility', 'unmount', 'new-pan']) {const r = await rig({reduced, docked: true});
    try {const pan = r.aside(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(horizontal); pan.callbacks.onEnd(horizontal, true);
      const finish = r.animations.filter(value => value.complete).at(-1)!;
      assert.ok(finish);
      // Queue the real finish on JS before retirement too; dispatch must check live ownership.
      finish.complete?.(true);
      if (stop === 'source') await r.replace();
      else if (stop === 'visibility') {r.dock.expand(fence); await r.render();}
      else if (stop === 'new-pan') pan.callbacks.onBegin();
      else await r.close();
      await r.complete(finish, true); await r.flushJS(); assert.deepEqual(r.actions, [], `${reduced}/${stop}`);
    } finally {await r.close();}
  }
});
test('expanding outgoing DockHost keeps distinct physical detector gesture identities', async () => {
  const r = await rig({docked: true});
  try {r.dock.expand(fence); await r.render();
    const full = r.fullPan(), dock = elements(r.barTree).find(node => node.type === 'GestureDetector').props.gesture;
    assert.ok(r.barTree, 'outgoing presence still mounted');
    assert.ok(!dock.gestures.includes(full), 'one PanGesture must never be attached in both detectors');
    assert.equal(dock.gestures[1].config.enabled, false, 'outgoing dock pan has no input ownership');
  } finally {await r.close();}
});
test('horizontal drag/fade transforms the actual common ancestor of chrome, Surface and retained preview', async () => {
  const r = await rig({docked: true, reduced: true});
  try {await r.layout(); const pan = r.aside(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(horizontal);
    const host = elements(r.tree).find(node => node.type === 'AnimatedView' && elements(node).some(child => child.type?.name === 'PlayerMorph') && elements(node).some(child => child.type?.name === 'DockHost'));
    assert.ok(host, 'the actual picture and chrome share the moving host');
    const read = () => host.props.style.find((style: any) => style?.read).read();
    assert.equal(read().transform[0].translateX, 300);
    const measured = r.measurements.length; await r.layout(); assert.equal(r.measurements.length, measured, 'dragged window coordinates never overwrite the settled slot');
    const chrome = elements(r.barTree).find(node => node.type === 'AnimatedView').props.style.find((style: any) => style?.read).read();
    assert.equal(chrome.transform[0].translateX, 0, 'shared motion is applied once'); assert.equal(chrome.opacity, 1);
    const surface = elements(r.pictureTree).find(node => node.type === r.Surface); assert.ok(surface);
    await r.tick(); r.controller().dragging.value = true; r.controller().progress.value = .5;
    await r.picture({retainedSnapshot: {fence, image: 'last-frame'}}); await r.tick();
    assert.ok(elements(r.pictureTree).some(node => node.type === 'AnimatedView' && node.props.children === 'last-frame'));
    pan.callbacks.onEnd(horizontal, true); const fade = r.animations.filter(value => value.to === 0 && value.complete).at(-1)!;
    fade.apply?.(); assert.equal(read().opacity, 0);
    assert.strictEqual(elements(r.pictureTree).find(node => node.type === r.Surface).type, surface.type);
  } finally {await r.close();}
});
test('steady reduced snapshot/fullFrame object churn never restarts the picture fade; actual transition fades once', async () => {
  const r = await rig({reduced: true});
  try {const count = r.animations.length; for (let i = 0; i < 4; i++) {r.dock.update({...initial, positionSeconds: 76 + i}); await r.render();}
    assert.equal(r.animations.length, count, 'facts/frame identities are not morph transitions');
    r.dock.collapse(fence); await r.render(); const after = r.animations.length;
    assert.ok(after > count, 'a real transition still animates');
    await r.render(); assert.equal(r.animations.length, after);
    r.controller().progress.value = 1; r.controller().velocity.value = 0; await r.tickPicture(); await r.tickPicture();
    assert.equal(r.animations.length, after, 'settlement reaction does not replay the reduced transition fade');
  } finally {await r.close();}
});
for (const reduced of [false, true]) test(`actual live channel collapse hosts horizontal x/fade once around the same Surface and chrome (reduced=${reduced})`, async () => {
  const r = await rig({kind: 'live', reduced});
    try {
      assert.equal(r.dock.getSnapshot().local?.kind, 'live');
      assert.ok(sameFence(r.dock.getSnapshot().local?.fence, channelFence));
      const surface = elements(r.pictureTree).find(node => node.type === r.Surface);
      const movingHost = () => elements(r.tree).find(node => node.type === 'AnimatedView' && elements(node).some(child => child.type?.name === 'PlayerMorph') && elements(node).some(child => child.type?.name === 'DockHost'));
      const originalHostType = movingHost().type;
      r.dock.collapse(channelFence); await r.render(); await r.layout();
      assert.equal(r.dock.occupant()?.mode, 'video', 'the real live channel uses hosted video chrome');
      const read = () => movingHost().props.style.find((style: any) => style?.read).read();
      const chrome = () => elements(r.barTree).find(node => node.type === 'AnimatedView').props.style.find((style: any) => style?.read).read();
      const pan = r.aside(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(horizontal);
      assert.equal(read().transform[0].translateX, 300, `${reduced}: common host follows the live drag`);
      assert.equal(read().opacity, 1); assert.equal(chrome().transform[0].translateX, 0); assert.equal(chrome().opacity, 1);
      const measured = r.measurements.length; await r.layout(); assert.equal(r.measurements.length, measured);
      pan.callbacks.onEnd(horizontal, true);
      const dismiss = r.animations.filter(value => value.complete).at(-1)!;
      dismiss.apply?.();
      assert.equal(read().transform[0].translateX, reduced ? 0 : dismiss.to);
      assert.equal(read().opacity, reduced ? 0 : 1);
      assert.equal(chrome().transform[0].translateX, 0); assert.equal(chrome().opacity, 1);
      assert.strictEqual(movingHost().type, originalHostType);
      assert.strictEqual(elements(r.pictureTree).find(node => node.type === r.Surface).type, surface.type);
      await r.complete(dismiss); assert.equal(r.actions.length, 1); assert.ok(sameFence(r.actions[0].fence, channelFence));
    } finally {await r.close();}
});
// Apple composes through ancestor opacity. Android's retained-image fade is
// exercised below; a SurfaceView cannot use this live-layer opacity contract.
for (const docked of [false, true]) for (const snapshotDuringProgress of [false, true]) test(`Apple reduced goal consumes one picture fade across actual intermediate progress (fromDocked=${docked}, snapshot=${snapshotDuringProgress})`, async () => {
  const r = await rig({reduced: true, docked: true, os: 'ios'});
    try {
      await measuredPicture(r, docked); // Persist the previous settled sample before changing the goal.
      const before = r.animations.length;
      if (docked) r.dock.expand(fence); else r.dock.collapse(fence);
      await r.render();
      const progress = r.animations.slice(before).find(value => value.owner === r.controller().progress)!;
      const fade = r.animations.slice(before).find(value => value.owner !== r.controller().progress && !value.complete && value.to === 1)!;
      assert.ok(progress && fade); assert.equal(fade.owner.value, 0, 'the actual reduced effect starts the picture fade');
      const fadeCount = () => r.animations.filter(value => value.owner === fade.owner).length;
      const count = fadeCount();
      fade.sample?.(.65); progress.sample?.(docked ? .6 : .4); await r.tickPicture();
      assert.equal(progress.owner.value, docked ? .6 : .4, 'first transition reaction observes intermediate progress, never the endpoint');
      if (snapshotDuringProgress) {
        r.dock.update({...r.dock.getSnapshot().local!, positionSeconds: 81}); await r.render();
        await r.picture({retainedSnapshot: {fence, image: 'actual-source-last-frame'}});
      }
      assert.equal(fadeCount(), count, `snapshot=${snapshotDuringProgress}: intermediate progress cannot reset opacity`);
      assert.equal(fade.owner.value, .65);
      progress.apply?.(); r.controller().velocity.value = 0; await r.tickPicture(); await r.tickPicture(); await r.render();
      assert.equal(fadeCount(), count, 'the final reaction and later snapshots cannot consume a second fade');
      assert.equal(fade.owner.value, .65); await r.complete(fade); assert.equal(fade.owner.value, 1);
    } finally {await r.close();}
});
test('Apple reduced drag and ACTIVE cancel returning to the same goal each fade once after transition consumption', async () => {
  for (const success of [false, true]) {const r = await rig({reduced: true, docked: true, os: 'ios'});
    try {
      await measuredPicture(r, false);
      r.dock.collapse(fence); const before = r.animations.length; await r.render();
      const transitionProgress = r.animations.slice(before).find(value => value.owner === r.controller().progress)!;
      const fade = r.animations.slice(before).find(value => value.owner !== r.controller().progress && !value.complete && value.to === 1)!;
      transitionProgress.sample?.(.5); await r.tickPicture(); transitionProgress.apply?.(); await r.tickPicture(); await r.complete(fade);
      for (let run = 0; run < 2; run++) {
        const count = r.animations.filter(value => value.owner === fade.owner).length;
        const pan = elements(r.barTree).find(node => node.type === 'GestureDetector').props.gesture.gestures[1];
        pan.callbacks.onBegin(); pan.callbacks.onUpdate({translationY: -80, velocityY: 0}); await r.tickPicture();
        pan.callbacks.onEnd({translationY: -80, velocityY: 0}, success); pan.callbacks.onFinalize({}, success); await r.flushJS();
        const settleProgress = r.animations.filter(value => value.owner === r.controller().progress).at(-1)!;
        settleProgress.sample?.(.95); await r.tickPicture(); await r.render();
        settleProgress.apply?.(); r.controller().velocity.value = 0; await r.tickPicture(); await r.tickPicture(); await r.render();
        const fades = r.animations.filter(value => value.owner === fade.owner);
        assert.equal(fades.length, count + 1, `success=${success}/run=${run}: one fade per genuine return to the same goal`);
        await r.complete(fades.at(-1)!);
        assert.equal(r.dock.getSnapshot().local?.expanded, false);
      }
    } finally {await r.close();}
  }
});
test('Apple reduced drag during its goal timing remains a new fade run, including ACTIVE cancel', async () => {
  for (const success of [false, true]) {const r = await rig({reduced: true, docked: true, os: 'ios'});
    try {
      await measuredPicture(r, true);
      const before = r.animations.length; r.dock.expand(fence); await r.render();
      const progress = r.animations.slice(before).find(value => value.owner === r.controller().progress)!;
      const fade = r.animations.slice(before).find(value => value.owner !== r.controller().progress && !value.complete && value.to === 1)!;
      progress.sample?.(.6); await r.tickPicture(); await r.complete(fade);
      const count = r.animations.filter(value => value.owner === fade.owner).length;
      const pan = r.fullPan(); pan.callbacks.onBegin(); pan.callbacks.onUpdate({translationY: -400, velocityY: 0}); await r.tickPicture();
      pan.callbacks.onEnd({translationY: -400, velocityY: 0}, success); pan.callbacks.onFinalize({}, success); await r.flushJS();
      const returning = r.animations.filter(value => value.owner === r.controller().progress).at(-1)!;
      returning.sample?.(.05); await r.tickPicture(); await r.render();
      returning.apply?.(); r.controller().velocity.value = 0; await r.tickPicture(); await r.render();
      const fades = r.animations.filter(value => value.owner === fade.owner);
      assert.equal(fades.length, count + 1, `success=${success}: a drag interrupting goal progress is a genuine new run`);
      assert.equal(r.dock.getSnapshot().local?.expanded, true);
    } finally {await r.close();}
  }
});

async function pausedCancel() {
  const r = await rig({reduced: true, docked: true, paused: true, controlledClock: true});
  await r.layout();
  for (const animation of r.animations) if (!animation.cancelled) animation.apply?.();
  await r.motionTick(0);
  await r.picture({retainedSnapshot: {fence, image: 'paused-current-frame', ready: true}});
  const parentRenders = r.parentRenders, presentations = r.presentations.length;
  const surface = elements(r.pictureTree).find(node => node.type === r.Surface);
  const pan = () => elements(r.barTree).find(node => node.type === 'GestureDetector').props.gesture.gestures[1];
  const fades = () => r.animations.filter(value => value.owner !== r.controller().progress && value.complete && value.to === 0);
  const before = fades().length;
  const event = {translationY: -160, velocityY: -100};
  pan().callbacks.onBegin(); pan().callbacks.onUpdate(event); await r.motionTick(16);
  assert.equal(r.controller().velocity.value, -.125, 'the actual drag velocity is retained');
  pan().callbacks.onEnd(event, false); pan().callbacks.onFinalize(event, false); await r.motionTick(32);
  const returning = r.animations.filter(value => value.owner === r.controller().progress).at(-1)!;
  assert.equal(returning.to, 1); assert.equal(returning.config.duration, 160);
  // Shared reduced timing is linear: .8 -> 1 at 80/144/160ms.
  returning.sample?.(.9); await r.motionTick(112);
  returning.sample?.(.98); await r.motionTick(176);
  assert.ok(Math.abs(r.controller().velocity.value - 1.25) < 1e-10, 'real controller reaction keeps nonzero return velocity');
  assert.equal(fades().length, before, 'no fade while the return is in flight');
  assert.equal(r.presentations.at(-1).moving, true);
  assert.ok(elements(r.pictureTree).some(node => node.props.children === 'paused-current-frame'));
  assert.equal(r.dock.getSnapshot().local?.playing, false);
  assert.equal(r.parentRenders, parentRenders, 'the paused parent has remained frozen');
  return {r, returning, pan, fades, before, parentRenders, presentations, surface};
}
for (const ordering of ['completion-before-derivative', 'derivative-before-completion']) test(`paused reduced ACTIVE cancel settles after the final sample without parent rescue (${ordering})`, async () => {
  const {r, returning, fades, before, parentRenders, presentations, surface} = await pausedCancel();
  try {
    returning.apply?.();
    if (ordering === 'derivative-before-completion') {
      await r.motionTick(192);
      assert.ok(Math.abs(r.controller().velocity.value - 1.25) < 1e-10, 'last finite difference exceeds the settlement threshold');
      assert.equal(r.presentations.at(-1).moving, true); assert.equal(fades().length, before);
      returning.complete?.(false); await r.motionTick(192);
      assert.ok(r.controller().velocity.value > .02, 'an unsuccessful finish cannot settle');
    }
    returning.complete?.(true); await r.motionTick(192);
    assert.equal(r.controller().velocity.value, 0, 'only current completion supplies terminal zero after the endpoint');
    assert.equal(fades().length, before + 1, 'the cancelled drag consumes exactly one return fade');
    assert.equal(r.presentations.at(-1).moving, true, 'retain the real image through its outgoing fade');
    assert.ok(elements(r.pictureTree).some(node => node.props.children === 'paused-current-frame'));
    assert.equal(elements(r.pictureTree).find(node => node.type === r.Surface).props.surfaceActive, true, 'same live producer is active beneath the outgoing image');
    assert.strictEqual(elements(r.pictureTree).find(node => node.type === r.Surface).type, surface.type);
    assert.deepEqual(elements(r.pictureTree).find(node => node.type === r.Surface).props.dockBounds, surface.props.dockBounds);
    const fade = fades().at(-1)!; fade.sample?.(.6);
    returning.complete?.(true); await r.motionTick(208); await r.motionTick(224);
    assert.equal(fades().length, before + 1); assert.equal(fade.owner.value, .6, 'duplicate completion cannot replay opacity');
    await r.complete(fade); await r.motionTick(240);
    assert.equal(r.presentations.at(-1).moving, false);
    assert.equal(r.presentations.slice(presentations).filter(value => !value.moving).length, 1);
    assert.ok(!elements(r.pictureTree).some(node => node.props.children === 'paused-current-frame'), 'retire the retained image after its one outgoing fade');
    assert.equal(r.parentRenders, parentRenders, 'no layer sync/render or playback fact update rescued settlement');
    assert.equal(r.transitions.length, 0); assert.deepEqual(r.actions, []);
  } finally {await r.close();}
});
test('obsolete progress completion cannot settle a newer drag, source, goal, retarget or unmounted picture', async () => {
  for (const stop of ['new-drag', 'source', 'goal', 'retarget', 'unmount']) {
    const {r, returning, pan, fades, before} = await pausedCancel();
    try {
      if (stop === 'new-drag') {
        pan().callbacks.onBegin(); pan().callbacks.onUpdate({translationY: 3.2, velocityY: 100}); await r.motionTick(184);
        assert.equal(r.controller().velocity.value, .125);
        pan().callbacks.onEnd({}, false); pan().callbacks.onFinalize({}, false);
      } else if (stop === 'source') await r.replace();
      else if (stop === 'goal') {r.dock.expand(fence); await r.render();}
      else if (stop === 'retarget') {r.controller().request(true); await r.motionTick(184);}
      else await r.close();
      const current = r.animations.filter(value => value.owner === r.controller().progress).at(-1)!;
      if (stop !== 'unmount') {current.sample?.(1); await r.motionTick(192);}
      const velocity = r.controller().velocity.value, count = fades().length, states = r.presentations.length;
      assert.ok(Math.abs(velocity) > .02, `${stop}: there is real velocity for an old finish to corrupt`);
      assert.equal(returning.cancelled, true);
      // Force late true as well as the normal false cancellation callback,
      // without allowing the obsolete animation to overwrite current progress.
      returning.complete?.(false); returning.complete?.(true); await r.motionTick(192);
      assert.equal(r.controller().velocity.value, velocity, `${stop}: old completion cannot zero current velocity`);
      assert.equal(fades().length, count); assert.equal(r.presentations.length, states);
      if (stop === 'new-drag') {
        assert.equal(r.presentations.at(-1).moving, true); assert.equal(fades().length, before);
        current.complete?.(true); await r.motionTick(192);
        assert.equal(r.controller().velocity.value, 0); assert.equal(r.presentations.at(-1).moving, true);
        assert.equal(fades().length, before + 1, 'the newer run can still complete once');
        await r.complete(fades().at(-1)!); await r.motionTick(208);
        assert.equal(r.presentations.at(-1).moving, false);
      }
      assert.equal(r.transitions.length, 0); assert.deepEqual(r.actions, []);
    } finally {await r.close();}
  }
});
test('actual 500ms local hold requests policy-respecting selection haptic once and opens its menu', async () => {
  for (const kind of ['music', 'video'] as const) {const r = await rig({docked: true, kind});
    try {const hold = r.opener(); assert.equal(hold.delayLongPress, 500); hold.onPressIn();
      r.time(499); assert.deepEqual(r.requests, []);
      r.time(500); hold.onLongPressAction(); hold.onLongPressAction(); await r.flushJS();
      assert.deepEqual(r.requests, [500]); assert.equal(elements(r.barTree).find(node => node.type === 'Popover').props.open, true);
      hold.onPressOut(); hold.onPressIn(); r.time(1000); hold.onLongPressAction(); await r.flushJS(); assert.deepEqual(r.requests, [500, 1000]);
    } finally {await r.close();}
  }
});
test('stale/unmounted/invisible holds do not request feedback; platform haptic policy remains authoritative', async () => {
  for (const stop of ['source', 'source-before-render', 'visibility', 'unmount', 'release']) {const r = await rig({docked: true});
    try {const hold = r.opener(); hold.onPressIn();
      if (stop === 'source') await r.replace(); else if (stop === 'source-before-render') await r.replace(false); else if (stop === 'visibility') {r.dock.expand(fence); await r.render();} else if (stop === 'release') hold.onPressOut(); else await r.close();
      hold.onLongPressAction(); await r.flushJS(); assert.deepEqual(r.requests, [], stop);
    } finally {await r.close();}
  }
  const r = await rig({docked: true, os: 'web'});
  try {const hold = r.opener(); hold.onPressIn(); hold.onLongPressAction(); await r.flushJS(); assert.deepEqual(r.requests, []);} finally {await r.close();}
});

// Positive completion matters: ownership checks must preserve a current dismiss.
test('successful current aside completion closes exactly once in normal and reduced motion', async () => {
  for (const reduced of [false, true]) {const r = await rig({docked: true, reduced});
    try {const pan = r.aside(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(horizontal); pan.callbacks.onEnd(horizontal, true); pan.callbacks.onFinalize(horizontal, true);
      const finish = r.animations.filter(value => value.complete).at(-1)!;
      await r.complete(finish); await r.complete(finish, true);
      assert.equal(r.actions.length, 1); assert.equal(r.actions[0].type, 'close'); assert.ok(sameFence(r.actions[0].fence, fence));
    } finally {await r.close();}
  }
});
test('interrupted vertical source callbacks cannot move replacement progress or release it', async () => {
  const r = await rig();
  try {const pan = r.fullPan(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(vertical); await r.replace();
    const progress = r.controller().progress.value; pan.callbacks.onUpdate({translationY: 800, velocityY: 2000}); pan.callbacks.onEnd(vertical, true); pan.callbacks.onFinalize(vertical, false); await r.flushJS();
    assert.equal(r.controller().progress.value, progress); assert.equal(r.transitions.length, 0);
  } finally {await r.close();}
});

test('a standalone morph consumer keeps local bar movement without a hosted picture ancestor', async () => {
  const r = await rig({docked: true});
  try {await r.media({morph: {...r.controller(), dockMotionHosted: false}});
    const pan = r.aside(); pan.callbacks.onBegin(); pan.callbacks.onUpdate(horizontal);
    const chrome = elements(r.barTree).find(node => node.type === 'AnimatedView').props.style.find((style: any) => style?.read).read();
    assert.equal(chrome.transform[0].translateX, 300);
  } finally {await r.close();}
});
const picturePropsOf = (r: any) => elements(r.tree).find(node => node.type?.name === 'PlayerMorph').props;
const surfaceIn = (r: any) => elements(r.pictureTree).find(node => node.type === r.Surface);
test('audio playback paints no picture or full-window gesture while the same mounted Surface stays present', async () => {
  for (const docked of [true, false]) {
    const r = await rig({docked, kind: 'music'});
    try {
      await r.render();
      const props = picturePropsOf(r);
      assert.equal(props.fence, undefined, `docked=${docked}: audio has no picture fence`);
      const surface = surfaceIn(r);
      assert.ok(surface, 'the one Surface stays mounted for video↔audio');
      assert.strictEqual(surface.type, r.Surface);
      assert.equal(surface.props.dockBounds, undefined, 'audio never receives measured picture bounds');
      assert.equal(surface.props.expandedBounds, undefined);
      assert.equal(surface.props.surfaceActive, false, 'audio never receives a producer composition');
      assert.ok(!elements(r.tree).some(node => node.type === 'GestureDetector'), 'audio never owns the full-window picture drag');
    } finally {await r.close();}
  }
});
test('video → audio → video keeps one Surface and paints only the current video at its measured slot', async () => {
  const r = await rig({docked: true, kind: 'video', reduced: true});
  try {
    await r.render();
    assert.equal(picturePropsOf(r).fence, undefined, 'an unmeasured dock never falls back to a full-window picture');
    assert.equal(surfaceIn(r).props.dockBounds, undefined);
    await r.layout();
    assert.ok(sameFence(picturePropsOf(r).fence, fence));
    assert.deepEqual(picturePropsOf(r).dockFrame, {x: 24, y: 700, width: 80, height: 45});
    assert.ok(surfaceIn(r).props.dockBounds, 'docked video paints inside its measured slot');
    r.dock.start({fence: {...fence, itemId: 'song', entryId: 'song'}, kind: 'music', queue: null, positionSeconds: 0, playing: true, expanded: false});
    await r.render();
    assert.equal(r.dock.getSnapshot().local?.kind, 'music');
    assert.equal(picturePropsOf(r).fence, undefined, 'audio has no picture even with a previous real dock slot');
    assert.strictEqual(surfaceIn(r).type, r.Surface);
    assert.equal(surfaceIn(r).props.dockBounds, undefined);
    assert.equal(surfaceIn(r).props.surfaceActive, false);
    r.dock.start({...initial, expanded: false});
    await r.render();
    assert.ok(sameFence(picturePropsOf(r).fence, fence), 'the returning video paints again');
    assert.strictEqual(surfaceIn(r).type, r.Surface, 'one Surface component throughout');
    assert.deepEqual(picturePropsOf(r).dockFrame, {x: 24, y: 700, width: 80, height: 45}, 'the returning video reuses its current fence slot');
  } finally {await r.close();}
});
test('docked video needs a current-fence measured slot, and expansion only ever uses the current video', async () => {
  const r = await rig({docked: true, kind: 'video', reduced: true});
  try {
    assert.equal(picturePropsOf(r).fence, undefined);
    await r.layout();
    assert.ok(sameFence(picturePropsOf(r).fence, fence));
    r.dock.expand(fence);
    await r.render();
    assert.ok(sameFence(picturePropsOf(r).fence, fence), 'expanded video keeps its current picture');
    assert.ok(elements(r.tree).some(node => node.type === 'GestureDetector'), 'expanded video keeps the real full-window gesture');
    const replacement = {...initial, fence: {...fence, sourceId: 'replacement', generation: 2}, expanded: false};
    r.dock.start(replacement);
    await r.render();
    const current = r.dock.getSnapshot().local!;
    assert.ok(!sameFence(current.fence, fence));
    if (picturePropsOf(r).fence !== undefined) {
      assert.ok(sameFence(picturePropsOf(r).fence, current.fence), 'a picture is fenced to the source that measured it');
      assert.notDeepEqual(picturePropsOf(r).dockFrame, undefined);
    }
    await r.layout();
    assert.ok(sameFence(picturePropsOf(r).fence, current.fence), 'the replacement measures its own dock slot');
  } finally {await r.close();}
});

// Authored UI-worklet boundary evidence; no physical picture capture claim.
test('local video and chrome use actual travel opacity through an interrupted aside', async () => {
  const r = await rig({docked: true});
  try {
    const pan = r.aside();
    pan.callbacks.onBegin(); pan.callbacks.onUpdate({translationX: 187, velocityX: 0}); await r.tick();
    assert.equal(r.controller().dockTravelFade.value, .5, 'both paints share the measured374pt bar travel');
    pan.callbacks.onEnd({translationX: 187, velocityX: 0}, true);
    const outward = r.animations.filter(animation => animation.owner === r.controller().dockX).at(-1)!;
    outward.sample?.(280.5); await r.tick();
    assert.equal(r.controller().dockTravelFade.value, .25, 'release follows actual distance, not an independent fade clock');
    pan.callbacks.onBegin(); await r.tick();
    assert.equal(r.controller().dockTravelFade.value, .25, 'interrupting at the current offset cannot brighten the paint');
    pan.callbacks.onUpdate({translationX: -93.5, velocityX: 0}); await r.tick();
    assert.equal(r.controller().dockTravelFade.value, .5);
  } finally {await r.close();}
});


test('playback Back refuses the original retired shell before any accessor even with no local media', async () => {
  for (const empty of [false, true]) {
    const r = await rig({docked: true});
    try {
      if (empty) {
        const previous = r.adapter.getSnapshot();
        r.adapter.getSnapshot = () => ({...previous, fence: undefined, dock: {...previous.dock, local: null}});
      }
      const queued = r.backCallback;assert.equal(r.activeBackListeners, 1);
      assert.equal(queued(), false);assert.equal(r.shellReads, 1, 'live Back keeps its original guard/transition path');
      const transitions = r.transitions.length;r.retireRuntime();assert.equal(r.activeBackListeners, 0);
      assert.equal(queued(), false, 'already-queued delivery also refuses the original aborted owner');
      assert.equal(r.shellReads, 1);assert.equal(r.transitions.length, transitions);
    } finally {await r.close();}
  }
});


test('retired passive chrome gate refuses queued begin/update/release and finalizes only its original visual run', async () => {
  const r = await rig();
  try {
    const current = {value: true}, pan = r.controller().chromeGesture(true, current);
    pan.callbacks.onBegin(); pan.callbacks.onUpdate({translationY: 200, velocityY: 100});
    const at = r.controller().progress.value;
    current.value = false;
    pan.callbacks.onUpdate(vertical); assert.equal(r.controller().progress.value, at);
    pan.callbacks.onEnd(vertical, true); pan.callbacks.onFinalize(vertical, true); await r.flushJS();
    assert.equal(r.transitions.length, 0);
    assert.equal(r.controller().dragging.value, false);
    assert.equal(r.animations.at(-1)!.to, 0);
    pan.callbacks.onBegin(); assert.equal(r.controller().dragging.value, false, 'a queued old begin cannot re-arm retired paint');
  } finally {await r.close();}
});


for (const retirement of ['target', 'page'] as const) test(`queued chrome release after original ${retirement} retirement cancels only original paint without a business transition`, async () => {
  const r = await rig();
  try {
    const current = {value: true}; let page = true;
    const pan = r.controller().chromeGesture(true, current, () => page);
    pan.callbacks.onBegin(); pan.callbacks.onUpdate(vertical); pan.callbacks.onEnd(vertical, true);
    if (retirement === 'target') current.value = false; else page = false;
    await r.flushJS(); await r.flushMotionJS();
    assert.equal(r.transitions.length, 0);
    assert.equal(r.dock.getSnapshot().local?.expanded, true);
    assert.equal(r.controller().dragging.value, false);
    assert.equal(r.animations.at(-1)!.to, 0, 'original expanded UI state is restored instead of leaving a rejected mini pose');
  } finally {await r.close();}
});
