// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {playbackFence, sameFence} from '../../../packages/player-core/src/index.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import {NativeWatchOutlet} from '../src-next/shell/watch-outlet.ts';
import {NativeWatchControls} from '../src-next/screens/watch/controls.ts';
import {resolvePlayerPresentation, playerControlsRevealDeadline} from '../src-next/platform/layout/player-presentation.ts';
import {nativeWatchTVPresentation} from '../src-next/screens/watch/tv-presentation.ts';
const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 's', accountId: 'a', profileId: 'p', controllerId: 'c', controllerEpoch: 'e', commandLaneId: 'l', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});
let window = {width: 890, height: 626}, safeTop = 0;
let posture: any = {status: 'unavailable', window: null, folds: []};
const {NativeWatchHost} = await screenModule(new URL('../src-next/screens/watch/WatchHost.tsx', import.meta.url), {
    '../../kit/dock/PlayerMorphBridge': {useInitialPlayerPaint: () => undefined},
  '../settings-live/group-port': {groupPort: () => undefined, subscribeGroupPort: () => () => {}},
  '../../kit/focus': {useOptionalFocusRegistry: () => undefined},
  '../../kit/useScreenReader': {useScreenReader: () => false},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: safeTop, bottom: 0, left: 0, right: 0})},
  react: {...React, default: React}, 'react-native': {StatusBar: {pushStackEntry: () => ({}), popStackEntry() {}}, View: 'View', useWindowDimensions: () => window, Dimensions: {get: () => window}},
  '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
  '../../platform/layout/player-presentation': {resolvePlayerPresentation, playerControlsRevealDeadline, playerPostureCrossFadeMs: 250},
  '../../platform/platformPosture': {usePlatformPosture: () => posture},
  './WatchChrome': {NativeWatchChrome: 'Chrome'}, './picture-slot': {NativeWatchPicture: 'Picture'},
  './HandheldMiniGuide': {NativeHandheldMiniGuide: 'HandheldMiniGuide'},
  './live-guide': {noLiveGuideSubscription: () => () => {}, emptyLiveGuide: (() => {const snapshot = {open: false, rows: []}; return () => snapshot;})()},
  '../../kit/overlays/TVPanelMotion': {TVPanelTransition: 'Transition', TVPanelSlide: 'Slide', TVPanelScrim: 'Scrim', useTVPanelPresence: (value: unknown) => ({value, open: value !== undefined})},
  './tv-presentation': {nativeWatchTVPresentation},
  'react-native-reanimated': {default: {View: 'AnimatedView'}, useSharedValue: (value: any) => React.useRef({value}).current, useAnimatedStyle: (run: any) => run(),
    withTiming: (value: any) => value, cancelAnimation: () => {}},
  '../../kit/useMotion': {useMotion: () => ({reduce: false, timing: () => ({duration: 0})})},
});
const {NativeWatchPicture} = await screenModule(new URL('../src-next/screens/watch/picture-slot.tsx', import.meta.url), {
  react: {...React, default: React}, 'react-native': {View: 'View', useWindowDimensions: () => window},
  '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
});
const child = (tree: any, id: string): any => {
  for (const node of React.Children.toArray(tree?.props?.children) as any[]) {
    if (!React.isValidElement<any>(node)) continue;
    if (node.props.testID === id) return node;
    const rendered = typeof node.type === 'function' && !Reflect.get(node.type, 'prototype')?.isReactComponent && node.props.context ? (node.type as React.FunctionComponent<any>)(node.props) : node;
    const found = child(rendered, id);
    if (found) return found;
  }
  return undefined;
};
async function fixture(isTV = false, failed?: (...args: any[]) => unknown) {
  window = {width: 890, height: 626}; posture = {status: 'unavailable', window: null, folds: []};
  let source: any = {authority, fence, retired: false, dock: {local: {fence, expanded: true, kind: 'video'}}, playback: {intent: 'playing'}};
  const listeners = new Set<() => void>(), calls: string[] = [], reveals: number[] = [];
  let minimizes = 0;
  const controls = new NativeWatchControls(isTV);
  const outlet = new NativeWatchOutlet(() => ({authority, fence: source.fence, window}));
  const playback = {getSnapshot: () => source, subscribe: (f: () => void) => {listeners.add(f); return () => {listeners.delete(f);};}};
  const setPresentation = (context: any) => {calls.push('presentation'); controls.source(source.fence, {...context, expanded: source.dock.local?.expanded === true, paused: source.playback.intent === 'paused'});};
  const reveal = controls.revealAfterLayout.bind(controls);
  controls.revealAfterLayout = (f, deadline) => {calls.push('reveal'); reveals.push(deadline); return reveal(f, deadline);};
  const bindings = {chrome: () => ({quality: 'actual selected quality', onMinimize: () => {minimizes++;}}), panel: () => null, content: () => null, failed};
  let tree: any;
  const current = () => authority;
  const host = createFabricHookHost(() => {tree = NativeWatchHost({authority, current, playback, outlet, runtime: {text: {}}, controls, bindings, setPresentation, isTV});});
  await host.mount();
  return {host, controls, outlet, calls, reveals, minimizes: () => minimizes, tree: () => tree,
    source: (change: any) => {source = {...source, ...change}; for (const listener of listeners) listener();},
    dispose: async () => {await host.unmount(); controls.dispose(); outlet.dispose();}};
}
test('root uses the actual canonical outlet and full measured pane across book geometry', async () => {
  const d = await fixture(); let measured: any; const measurements: ((x: number, y: number, w: number, h: number) => void)[] = [];
  const view = {measureInWindow: (callback: typeof measurements[number]) => {measurements.push(callback);}};
  const picture = () => d.tree().props.children[0];
  const measurementHost = createFabricHookHost(() => {measured = NativeWatchPicture(picture().props); measured.props.ref.current = view;});
  try {
    await act(() => {});
    assert.deepEqual(picture().props.style, {position: 'absolute', left: 0, top: 0, width: 890, height: 626});
    await measurementHost.mount(); measurements.at(-1)!(0, 0, 890, 626);
    assert.deepEqual(d.outlet.getSnapshot()?.frameInWindow, {x: 0, y: 0, width: 890, height: 626});
    const revision = d.outlet.getSnapshot()!.layoutRevision;
    posture = {status: 'available', window, folds: [{bounds: {left: 445, right: 445, top: 0, bottom: 626}, orientation: 'vertical', state: 'half-opened'}]};
    await act(() => d.host.rerender());
    await measurementHost.rerender(); measured.props.onLayout(); measurements.at(-1)!(0, 0, 445, 250.3125);
    assert.ok(d.outlet.getSnapshot()!.layoutRevision > revision);
    assert.deepEqual(d.outlet.getSnapshot()?.frameInWindow, {x: 0, y: 0, width: 445, height: 250.3125});
    assert.equal(child(d.tree(), 'watch-controls-pane').props.children.props.edgeInsets.right, 16);
    assert.equal(d.reveals.length, 1); assert.deepEqual(d.calls.slice(-2), ['presentation', 'reveal']);
  } finally {await measurementHost.unmount(); await d.dispose();}
});
test('the upright player pushes its top controls below the status bar (P2-74 E-mobile-14)', async () => {
  const d = await fixture();
  try {
    await act(() => {});
    window = {width: 390, height: 844}; safeTop = 59;
    await act(() => d.host.rerender());
    assert.deepEqual(child(d.tree(), 'watch-controls-pane').props.children.props.edgeInsets, {top: 59});
    safeTop = 0;
    await act(() => d.host.rerender());
    assert.equal(child(d.tree(), 'watch-controls-pane').props.children.props.edgeInsets, undefined);
  } finally {safeTop = 0; await d.dispose();}
});
test('TV uses one logical canvas while its actual native pane remains whole and unscaled beneath Options and Info', async () => {
  const d = await fixture(true);
  try {
    await act(async () => {window = {width: 960, height: 540}; await d.host.rerender();});
    const picture = d.tree().props.children[0];
    assert.deepEqual(picture.props.style, {position: 'absolute', left: 0, top: 0, width: 960, height: 540});
    const canvas = child(d.tree(), 'watch-tv-canvas');
    assert.equal(canvas.props.style.width, 1920); assert.equal(canvas.props.style.height, 1080);
    assert.deepEqual(canvas.props.style.transform, [{scale: .5}]);
    for (const [panel, width, right] of [['audio', 680, 780], ['playback-info', 960, 1056]] as const) {
      await act(() => {d.controls.open(panel, fence);});
      const drawn = child(d.tree(), 'watch-tv-canvas').props.children;
      assert.equal(child(drawn, 'watch-panel-pane').props.style.width, width);
      assert.equal(child(drawn, 'watch-controls-pane').props.children.props.tvPanelRight, right);
      assert.equal(d.tree().props.children[0].type, picture.type);
      assert.deepEqual(d.tree().props.children[0].props.style, picture.props.style);
    }
  } finally {await d.dispose();}
});
test('panel state suppresses fold reveal, closing alone does not reveal, docked/pending output has no slot', async () => {
  const d = await fixture();
  try {
    await act(() => {});
    await act(() => {d.controls.open('audio', fence);});
    assert.equal(d.controls.getSnapshot().visible, false);
    assert.equal(child(d.tree(), 'watch-panel-pane').props.style.width, 356);
    assert.equal(d.tree().props.children[0].props.style.width, 890);
    await act(async () => {window = {width: 1000, height: 626}; await d.host.rerender();});
    assert.equal(d.reveals.length, 0);
    await act(() => {d.controls.closePanel(fence);});
    assert.equal(d.reveals.length, 0);
    await act(() => {d.source({dock: {local: {fence, expanded: false, kind: 'video'}}});});
    assert.equal(d.tree(), null); assert.equal(d.outlet.getSnapshot(), undefined);
    await act(() => {d.source({fence: undefined, dock: {}});});
    assert.equal(d.tree(), null);
  } finally {await d.dispose();}
});


test('handheld failed fence is presented only while its panel does not own the space', async () => {
  const failures: any[] = [], d = await fixture(false, (_host, failure) => {failures.push(failure); return 'failure';});
  try {
    await act(() => d.source({playback: {intent: 'playing', phase: 'error', positionSeconds: 42}, capabilities: {retry: true}}));
    assert.ok(child(d.tree(), 'watch-failed-pane'));
    assert.equal(failures.at(-1).fence, fence);
    await act(() => {d.controls.open('audio', fence);});
    assert.equal(child(d.tree(), 'watch-failed-pane'), undefined);
    await act(() => {d.controls.closePanel(fence);});
    assert.ok(child(d.tree(), 'watch-failed-pane'));
  } finally {await d.dispose();}
});


for (const scenario of [
  {name: 'portrait phone', width: 390, height: 844, folds: []},
  {name: 'portrait tablet', width: 768, height: 1024, folds: []},
  {name: 'book fold', width: 890, height: 626, folds: [{bounds: {left: 445, right: 445, top: 0, bottom: 626}, orientation: 'vertical', state: 'half-opened'}]},
  {name: 'tabletop fold', width: 626, height: 890, folds: [{bounds: {left: 0, right: 626, top: 445, bottom: 445}, orientation: 'horizontal', state: 'half-opened'}]},
] as const) test(`${scenario.name} failure is confined to the measured picture and leaves chrome mounted`, async () => {
  const d = await fixture(false, () => 'failure');
  try {
    await act(async () => {window = {width: scenario.width, height: scenario.height}; posture = {status: 'available', window, folds: scenario.folds}; await d.host.rerender();});
    await act(() => d.source({playback: {intent: 'playing', phase: 'error', positionSeconds: 42}, capabilities: {retry: true}}));
    const pane = child(d.tree(), 'watch-failed-pane'), picture = d.tree().props.children[0];
    assert.deepEqual(pane.props.style, picture.props.style);
    const chrome = child(d.tree(), 'watch-controls-pane');
    assert.ok(chrome, 'player chrome remains mounted for minimize and exit');
    chrome.props.children.props.onMinimize(fence); assert.equal(d.minimizes(), 1);
    await act(() => {d.controls.open('audio', fence);});
    assert.equal(child(d.tree(), 'watch-failed-pane'), undefined);
  } finally {await d.dispose();}
});
