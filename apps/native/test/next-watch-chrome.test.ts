// @fabric-react
import {createTVKeyAdapter, type TVKeyEvent} from '../src-next/platform/tv-key-adapter.ts';
import * as playerCore from '../../../packages/player-core/src/index.ts';
import {channelSeconds} from '../../../packages/client-core/src/playback/linear-protocol.ts';
import {channelTint, playbackIdentity} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {playbackFence} from '../../../packages/player-core/src/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import * as registryModule from '../src-next/kit/focus/registry.ts';
import * as hostRefModule from '../src-next/kit/focus/useHostRef.ts';
import {NativeWatchControls} from '../src-next/screens/watch/controls.ts';
import * as landingStoreModule from '../src-next/screens/watch/landing-store.ts';

const gesture = () => {const value: Record<string, unknown> = {}; for (const key of ['enabled', 'runOnJS', 'activeOffsetY', 'failOffsetX', 'onEnd']) value[key] = () => value; return value;};
const focusRegistry = registryModule.createFocusRegistry();
let event: (event: {eventType: string; eventKeyAction: number}) => void;
let isTV = true;
const motion = {duration: () => 180, timing: () => ({duration: 0})};
const chromeModules = {
  '../../platform/tv-keys': {useTVKeyEvent: (handler: (event: TVKeyEvent) => void) => {const adapter = React.useMemo(() => createTVKeyAdapter(), []); event = raw => {const normalized = adapter.read(raw); if (normalized) handler(normalized);};}},
  '../../kit/dock/PlayerMorphBridge': {useInitialPlayerPaint: () => undefined, usePublishedPlayerMorph: () => undefined},
  react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text', AccessibilityInfo: {announceForAccessibility() {}}, useTVEventHandler: (handler: typeof event) => {event = handler;}},
  'react-native-reanimated': {Easing: {linear: (value: number) => value}, default: {View: 'Animated'}, cancelAnimation() {}, useAnimatedStyle: (value: () => unknown) => value(), useSharedValue: (value: unknown) => React.useRef({value}).current, withTiming: (value: unknown) => value},
  'react-native-gesture-handler': {Gesture: {Pan: gesture}, GestureDetector: 'GestureDetector'},
  '../../../../../packages/client-core/src/playback/linear-protocol': {channelSeconds},
  './useSkipPromptMotion': {useSkipPromptMotion: (value: unknown) => ({value, interactive: !!value, bodyStyle: {}, positionStyle: {}})},
  './TVControlLabel': {TVControlLabel: 'TVControlLabel'}, '../settings-live/GroupPill': {NativeTVGroupPill: 'GroupPill'},
  './live-guide': {noLiveGuideSubscription: () => () => {}, emptyLiveGuide: (() => {const snapshot = {}; return () => snapshot;})()},
  './live-actions': {noLiveActionsSubscription: () => () => {}, emptyLiveActions: (() => {const snapshot = {}; return () => snapshot;})()},
  '../../../../../packages/client-core/src/guide/now-next': {nowNext: () => ({})},
  '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage: String},
  './TVMiniGuide': {NativeMiniGuide: 'MiniGuide'}, './TVLiveActions': {TVLiveActions: 'LiveActions'},
  './UpNextCard': {NativeUpNextCard: 'UpNextCard'}, './StillWatchingCard': {NativeStillWatchingCard: 'StillWatchingCard'}, '../../kit/artwork': {Artwork: 'Artwork'},
  'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Path: 'Path', Rect: 'Rect', Stop: 'Stop', Text: 'SvgText'},
  '../../../../../packages/player-core/src': playerCore, '../../../../../packages/client-core/src/presentation/kit-model': {playbackIdentity, channelTint},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`}, '../../../../../packages/i18n/src': {defaultI18n},
  '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => focusRegistry}, '../../kit/Icon': {Icon: 'Icon'},
  '../../kit/theme': {nativeType: () => ({}), useTheme: () => ({isTV, colors: {}, type: {body: {}, bodyStrong: {}, caption: {}}})},
  '../../kit/useMotion': {useMotion: () => motion, useReducedMotion: () => true}, './controls': {NativeWatchControls}, './landing-store': landingStoreModule, './WatchTimeline': {NativeWatchTimeline: 'Timeline'},
  '../../kit/useScreenReader': {useScreenReader: () => false},
};
const {NativeWatchChrome} = await screenModule(new URL('../src-next/screens/watch/WatchChrome.tsx', import.meta.url), chromeModules);
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation: 1});

test('TV first Select event plus its genuine picture press only reveals; second press toggles playback', async () => {
  const controls = new NativeWatchControls(true), commands: string[] = [];
  const source = {fence, retired: false, dock: {local: {expanded: true}}, queue: {}, capabilities: {play: true, pause: true, seek: true}, playback: {intent: 'playing', positionSeconds: 17, duration: 100}};
  const playback = {getSnapshot: () => source, toggle: async () => {commands.push('toggle');}};
  let tree: any;
  controls.source(fence, {expanded: true, autoHide: true, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: false});
  const host = createFabricHookHost(() => {tree = NativeWatchChrome({source, controls, playback, text: {t: defaultI18n.t}, quality: 'Original', width: 1920, height: 1080, compact: false, skipBackSeconds: 10, skipForwardSeconds: 30, onMinimize() {}, onPanel() {}, reportError(error: unknown) {throw error;}});});
  await host.mount();
  try {
    const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : React.isValidElement(node) ? [node, ...all((node as any).props.children)] : [];
    const picture = all(tree).find(node => node.props.testID === 'watch-picture');
    event!({eventType: 'select', eventKeyAction: 1}); picture.props.onPress();
    assert.equal(controls.getSnapshot().visible, true); assert.equal(controls.getSnapshot().focus, 'toggle'); assert.deepEqual(commands, []);
    await host.flush();
    const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : React.isValidElement(node) ? [node, ...descendants((node as any).props.children)] : [];
    const toggle = descendants(tree).find(node => node.props.testID === 'watch-toggle');
    toggle.props.onPress(); assert.deepEqual(commands, ['toggle']);
  } finally {await host.unmount(); controls.dispose();}
});

test('every genuine chrome child mounts the strict focus target hook with unique stable semantic caller IDs', async () => {
  const contexts: {current: unknown}[] = [];
  const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react: {...React, default: React, createContext: (current: unknown) => {const value = {current}; contexts.push(value); return value;}, useContext: (context: {current: unknown}) => context.current},
    'react-native': {findNodeHandle: () => null, TVFocusGuideView: 'Guide', View: 'View'}, '../theme': {useTheme: () => ({isTV})}, './registry': registryModule, './useHostRef': hostRefModule,
  });
  const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : React.isValidElement(node) ? [node, ...descendants((node as any).props.children)] : [];
  for (const {tv, compact} of [{tv: false, compact: false}, {tv: false, compact: true}, {tv: true, compact: false}]) {
    isTV = tv;
    const controls = new NativeWatchControls(tv), currentFence = {...fence, showId: 'show'};
    const source = {fence: currentFence, retired: false, dock: {local: {expanded: true}}, queue: {view: {next: {available: false}, postPlay: {}, items: [{entryId: 'one'}, {entryId: 'two'}]}}, capabilities: {play: true, pause: true, seek: true, audioTrack: true, subtitleTrack: true, previous: true, next: true}, playback: {intent: 'playing', positionSeconds: 17, duration: 100}};
    const playback = {getSnapshot: () => source, toggle: async () => {}, skip: async () => {}, previous: async () => {}, next: async () => {}};
    controls.source(currentFence, {expanded: true, autoHide: true, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: false}); controls.reveal(currentFence);
    let tree: any;
    const host = createFabricHookHost(() => {tree = NativeWatchChrome({source, controls, playback, text: {t: defaultI18n.t}, quality: 'Original', width: 1920, height: 1080, compact, skipBackSeconds: 10, skipForwardSeconds: 30,
      onMinimize() {}, onPictureInPicture() {}, onCast() {}, onFullWindow() {}, reportError: assert.fail});});
    await host.mount();
    try {
      const region = descendants(tree).find(node => node.type === 'FocusRegion' && node.props.id === 'watch-controls'); assert.equal(region.props.id, 'watch-controls');
      const targets = descendants(region).filter(node => node.type === 'Button' || node.type === 'Focusable');
      const ids = targets.map(node => node.props.focusId);
      assert.equal(new Set(ids).size, targets.length); assert.ok(ids.every(id => typeof id === 'string' && id.startsWith('watch-')));
      assert.ok(ids.includes('watch-skip-back')); assert.ok(ids.includes('watch-skip-forward')); assert.ok(ids.includes('watch-option-quality'));
      const registry = registryModule.createFocusRegistry(); registry.configure({id: 'watch-controls', orientation: 'horizontal', entry: 'watch-toggle'});
      contexts[0]!.current = registry; contexts[1]!.current = 'watch-controls';
      for (const target of targets) {
        let binding: any;
        const mounted = createFabricHookHost(() => {binding = focus.useFocusTarget({focusId: target.props.focusId, testID: target.props.testID, disabled: target.props.disabled});});
        await mounted.mount(); try {assert.equal(binding.testID, target.props.testID);} finally {await mounted.unmount();}
      }
    } finally {await host.unmount(); controls.dispose();}
  }
  isTV = true;
});

const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : React.isValidElement(node) ? [node, ...descendants((node as any).props.children)] : [];

for (const kind of ['skip', 'undo', 'up-next']) for (const visible of [false, true]) test(`a ${kind} arrival with controls ${visible ? 'up preserves Quality' : 'hidden owns focus and Back stays in playback'}`, async () => {
  isTV = true;
  const entry = kind === 'skip' ? 'watch-skip-action' : kind === 'undo' ? 'watch-skip-undo' : 'watch-up-next-play';
  const controls = new NativeWatchControls(true), requested: string[] = [];
  const source: any = {fence, retired: false, dock: {local: {expanded: true}}, queue: kind === 'up-next' ? {view: {items: [{entryId: 'next', title: 'Next episode'}], next: {entryId: 'next', available: true}, postPlay: {autoplay: false}}} : {},
    capabilities: {play: true, pause: true, seek: true}, playback: {intent: 'playing', positionSeconds: 1, duration: 100}};
  const policy = new playerCore.PlaybackPolicy();let nowMs = 0;
  policy.begin(fence, [{id: 'intro', automaticSafe: true, kind: kind === 'up-next' ? 'credits' : 'intro', startSeconds: kind === 'up-next' ? 70 : 10, endSeconds: kind === 'up-next' ? 99 : 30}], 100);
  const updatePolicy = () => {source.policy = policy.update({fence, nowMs: ++nowMs, positionSeconds: source.playback.positionSeconds, durationSeconds: 100, rate: 1, playing: true, expanded: true, ended: false, kind: 'episode',
    modes: {intro: kind === 'undo' ? 'auto' : 'ask', recap: 'ask', credits: 'ask'}, postPlay: kind === 'up-next'
      ? {nextEntryId: 'next', available: true, reason: 'ready', countdownSeconds: 10, autoplay: false, passoutCheckDue: false, automaticAdvances: 0, startDuringCredits: false} : null})!.view;};
  updatePolicy();
  const playback = {getSnapshot: () => source, seek: async () => {}, policyDismiss: () => {const dismissed = policy.dismiss(fence);updatePolicy();return dismissed;}};
  controls.source(fence, {expanded: true, autoHide: true, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: false});
  if (visible) controls.reveal(fence);
  const release = focusRegistry.mountRegion('watch-skip', {});
  focusRegistry.configure({id: 'watch-skip', entry, orientation: 'horizontal'});
  focusRegistry.attach({regionId: 'watch-skip', childId: entry}, {requestTVFocus: () => {
    requested.push('skip'); focusRegistry.didFocus({regionId: 'watch-skip', childId: entry});
  }});
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeWatchChrome({source, controls, playback, text: {t: defaultI18n.t}, quality: 'Original', width: 1920, height: 1080, compact: false,
    skipBackSeconds: 10, skipForwardSeconds: 30, onMinimize() {}, reportError: assert.fail});});
  await host.mount();
  try {
    focusRegistry.didFocus({regionId: visible ? 'watch-controls' : 'watch-surface', childId: visible ? 'watch-option-quality' : 'watch-picture'});
    source.playback.positionSeconds = kind === 'up-next' ? 80 : 17; updatePolicy();await host.rerender(); for (let pass = 0; pass < 6 && await host.flush(); pass++) {}
    const prompt = descendants(tree).find(node => node.props.testID === entry || kind === 'up-next' && node.type === 'UpNextCard');
    assert.ok(prompt); if (kind !== 'up-next') assert.equal(prompt.props.hasTVPreferredFocus, false);
    if (visible) {
      assert.deepEqual(requested, []); assert.equal(focusRegistry.capture()?.childId, 'watch-option-quality');
      const region = descendants(tree).find(node => node.props.id === 'watch-transport');
      assert.equal(region.props.edges.right, 'watch-skip');
    } else {
      assert.ok(requested.length > 0); assert.equal(controls.getSnapshot().visible, false);
      event!({eventType: 'select', eventKeyAction: 1}); assert.equal(controls.getSnapshot().visible, false);
      assert.equal(controls.back(fence), 'controls'); await host.flush();
      assert.equal(controls.getSnapshot().visible, false);
      assert.equal(descendants(tree).some(node => node.props.testID === entry || kind === 'up-next' && node.type === 'UpNextCard'), false);
      assert.equal(controls.back(fence), 'player');
      source.playback.positionSeconds += 1; updatePolicy();await host.rerender();
      assert.equal(descendants(tree).some(node => node.props.testID === entry || kind === 'up-next' && node.type === 'UpNextCard'), false);
    }
  } finally {await host.unmount(); focusRegistry.attach({regionId: 'watch-skip', childId: entry}, null); release(); controls.dispose();}
});

test('seek preview keeps transport native targets mounted for a guide-based Down return', async () => {
  isTV = true;
  const controls = new NativeWatchControls(true);
  const source = {fence, retired: false, dock: {local: {expanded: true}}, queue: {}, capabilities: {play: true, pause: true, seek: true}, playback: {intent: 'paused', positionSeconds: 17, duration: 100}};
  const playback = {getSnapshot: () => source};
  controls.source(fence, {expanded: true, autoHide: true, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: false}); controls.reveal(fence);
  focusRegistry.didFocus({regionId: 'watch-seek-region', childId: 'watch-seek'});
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeWatchChrome({source, controls, playback, text: {t: defaultI18n.t}, quality: 'Original', width: 1920, height: 1080, compact: false,
    skipBackSeconds: 10, skipForwardSeconds: 30, onMinimize() {}, reportError: assert.fail});});
  await host.mount();
  try {
    const find = (id: string) => descendants(tree).find(node => node.props.id === id);
    const timeline = descendants(tree).find(node => node.type === 'Timeline' && node.props.tv && node.props.enabled);
    timeline.props.onPreview(25); await host.flush();
    const hidden = find('watch-transport');
    assert.equal(hidden.props.style.opacity, 0);
    assert.ok(descendants(hidden).some(node => node.props.focusId === 'watch-option-quality'));
    assert.equal(find('watch-seek-region').props.edges.down, 'watch-transport');
    assert.equal(hidden.props.edges.up, 'watch-seek-region');
    assert.equal(find('watch-group').props.edges.down, 'watch-seek-region');
    assert.equal(timeline.props.nextFocusUp, undefined);
    assert.equal(timeline.props.onExitPreview, undefined, 'no delayed JS focus mover');
    focusRegistry.didFocus({regionId: 'watch-transport', childId: 'watch-option-quality'}); await host.flush();
    assert.equal(find('watch-transport').props.style.opacity, 1);
    assert.equal(focusRegistry.capture()?.childId, 'watch-option-quality');
  } finally {await host.unmount(); controls.dispose();}
});

/** Authored UNRUN source: actual chrome/controller with controlled paint driver,
 * not a native timing or first-frame admission check. */
test('a slow first frame never bypasses ordinary timer and toggle hiding after the entrance', async t => {
  const previousTV = isTV; isTV = false;
  let now = 10000, nextTimer = 0; const timers = new Map<number, {callback: () => void; at: number}>();
  t.mock.method(Date, 'now', () => now);
  const controls = new NativeWatchControls(false, {now: () => now,
    set: (callback, delay) => {const id = ++nextTimer; timers.set(id, {callback, at: now + delay}); return id;},
    clear: handle => {timers.delete(handle as number);}});
  const paint = {key: {}, fence, title: {value: 0}, page: {value: 1}, controls: {value: 1}, landed: true, entering: false, controlsAdmitted: true};
  const {NativeWatchChrome: Chrome} = await screenModule(new URL('../src-next/screens/watch/WatchChrome.tsx', import.meta.url), {
    ...chromeModules, '../../kit/dock/PlayerMorphBridge': {useInitialPlayerPaint: () => paint, usePublishedPlayerMorph: () => undefined},
    'react-native-reanimated': {...chromeModules['react-native-reanimated'], useAnimatedStyle: (read: () => any) => new Proxy({}, {get: (_target, key) => read()[key]})},
  });
  const source = {fence, retired: false, dock: {local: {expanded: true}}, queue: {}, capabilities: {play: true, pause: true, seek: true}, playback: {intent: 'playing', positionSeconds: 17, duration: 100}};
  const playback = {getSnapshot: () => source, toggle: async () => {}};
  controls.source(fence, {expanded: true, autoHide: true, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: false});
  let tree: any;
  const host = createFabricHookHost(() => {tree = Chrome({source, controls, playback, text: {t: defaultI18n.t}, quality: 'Original', width: 390, height: 844, compact: true, skipBackSeconds: 10, skipForwardSeconds: 30, onMinimize() {}, reportError: assert.fail});});
  const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : React.isValidElement(node) ? [node, ...all((node as any).props.children)] : [];
  try {
    await host.mount(); await host.flush(); now += 5000;
    for (const [id, timer] of [...timers]) if (timer.at <= now) {timers.delete(id); timer.callback();}
    await host.flush();
    assert.equal(controls.getSnapshot().visible, false);
    const hidden = all(tree).find(node => node.type === 'Animated' && node.props.pointerEvents === 'none' && node.props.importantForAccessibility === 'no-hide-descendants');
    assert.ok(hidden); assert.equal(hidden.props.style.at(-1).opacity, 0);
    assert.equal(paint.controls.value, 1, 'the initial presentation is still present and its entrance is already complete');
    controls.reveal(fence); await host.flush(); controls.toggleVisibility(fence); await host.flush();
    const toggled = all(tree).find(node => node.type === 'Animated' && node.props.pointerEvents === 'none' && node.props.importantForAccessibility === 'no-hide-descendants');
    assert.equal(toggled.props.style.at(-1).opacity, 0);
  } finally {await host.unmount(); controls.dispose(); isTV = previousTV;}
});

// JUSTIN-ANSWERS #29 over the shared PlaybackPolicy: the policy owns Undo's six-second window;
// with a screen reader the note stays, announced, and later returns to the segment start.
for (const screenReader of [false, true]) test(`auto-skip Undo ${screenReader ? 'persists and announces for AT' : 'follows the policy\'s six-second window'}`, async () => {
  const previousTV = isTV; isTV = false;
  const spoken: string[] = [], seeks: unknown[] = [], undos: unknown[] = [];
  const {NativeWatchChrome: Chrome} = await screenModule(new URL('../src-next/screens/watch/WatchChrome.tsx', import.meta.url), {
    ...chromeModules,
    '../../kit/useScreenReader': {useScreenReader: () => screenReader},
    'react-native': {...chromeModules['react-native'], AccessibilityInfo: {announceForAccessibility: (text: string) => spoken.push(text)}},
  });
  const controls = new NativeWatchControls(false);
  const marker = {id: 'intro', kind: 'intro', startSeconds: 10, endSeconds: 20, automaticSafe: true};
  const source: any = {fence, retired: false, dock: {local: {expanded: true}}, queue: {},
    policy: {prompt: null, undo: {marker, remainingSeconds: 6}, markers: [marker], finished: false, replay: false, attention: null},
    capabilities: {play: true, pause: true, seek: true}, playback: {intent: 'playing', positionSeconds: 20, duration: 100}};
  const playback = {getSnapshot: () => source, seek: async (expected: unknown, seconds: number) => {seeks.push([expected, seconds]);},
    policyUndo: async (expected: unknown) => {undos.push(expected);}};
  controls.source(fence, {expanded: true, autoHide: false, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: screenReader});
  let tree: any;
  const host = createFabricHookHost(() => {tree = Chrome({source, controls, playback, text: {t: defaultI18n.t}, quality: 'Original', width: 390, height: 844, compact: true,
    skipBackSeconds: 10, skipForwardSeconds: 30, onMinimize() {}, reportError: assert.fail});});
  await host.mount();
  try {
    await host.flush();
    assert.deepEqual(spoken, [`${defaultI18n.t('player.skipped', {kind: 'intro'})}, ${defaultI18n.t('action.undo')}`]);
    const undo = () => descendants(tree).find(node => node.props.testID === 'watch-skip-undo');
    assert.ok(undo());
    // The policy's window closes.
    source.policy = {...source.policy, undo: null}; await host.rerender(); await host.flush();
    if (screenReader) {
      assert.ok(undo(), 'the note stays for a screen reader');
      const notice = descendants(tree).find(node => node.props.accessibilityActions?.some((action: any) => action.name === 'dismiss'));
      notice.props.onAccessibilityAction({nativeEvent: {actionName: 'activate'}}); await host.flush();
      assert.deepEqual(undos, [], 'the expired policy Undo is not called');
      assert.deepEqual(seeks, [[fence, 10]], 'Undo keeps the playback fence and returns to where the segment began');
      assert.equal(undo(), undefined);
    } else {
      assert.equal(undo(), undefined);
      assert.deepEqual(seeks, []);
    }
  } finally {await host.unmount(); controls.dispose(); isTV = previousTV;}
});

test('auto-skip Undo inside the policy window uses the policy, with or without a screen reader', async () => {
  const previousTV = isTV; isTV = false;
  const seeks: unknown[] = [], undos: unknown[] = [];
  const {NativeWatchChrome: Chrome} = await screenModule(new URL('../src-next/screens/watch/WatchChrome.tsx', import.meta.url), {
    ...chromeModules, '../../kit/useScreenReader': {useScreenReader: () => true},
    'react-native': {...chromeModules['react-native'], AccessibilityInfo: {announceForAccessibility: () => {}}},
  });
  const controls = new NativeWatchControls(false);
  const marker = {id: 'intro', kind: 'intro', startSeconds: 10, endSeconds: 20, automaticSafe: true};
  const source: any = {fence, retired: false, dock: {local: {expanded: true}}, queue: {},
    policy: {prompt: null, undo: {marker, remainingSeconds: 4}, markers: [marker], finished: false, replay: false, attention: null},
    capabilities: {play: true, pause: true, seek: true}, playback: {intent: 'playing', positionSeconds: 21, duration: 100}};
  const playback = {getSnapshot: () => source, seek: async (_expected: unknown, seconds: number) => {seeks.push(seconds);}, policyUndo: async (expected: unknown) => {undos.push(expected);}};
  controls.source(fence, {expanded: true, autoHide: false, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: true});
  let tree: any;
  const host = createFabricHookHost(() => {tree = Chrome({source, controls, playback, text: {t: defaultI18n.t}, quality: 'Original', width: 390, height: 844, compact: true,
    skipBackSeconds: 10, skipForwardSeconds: 30, onMinimize() {}, reportError: assert.fail});});
  await host.mount();
  try {
    await host.flush();
    descendants(tree).find(node => node.props.testID === 'watch-skip-undo').props.onPress(); await host.flush();
    assert.deepEqual(undos, [fence]); assert.deepEqual(seeks, []);
  } finally {await host.unmount(); controls.dispose(); isTV = previousTV;}
});
