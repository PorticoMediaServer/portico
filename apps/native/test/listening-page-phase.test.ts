// @fabric-react
import {dockFence} from './helpers/dock-fence.ts';
/** Authored UNRUN actual-hook/UI-worklet source vectors with controlled shared
 * values. No reconciler, native image, display timing, input or device proof. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createFabricHookHost, act} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {sameFence} from '../../../packages/player-core/src/index.ts';
import {validRect} from '../src-next/kit/dock/model.ts';
const fence = dockFence();
function cell(initial: any) {
  let value = initial;
  const node: any = {get value() {return value;}, set value(next: any) {
    if (node.running) node.running.stopped = true;
    if (next?.animation || next?.delayed) {
      const animation = next.delayed ? next.inner : next;
      const handle: any = {from: value, to: animation.to, config: animation.config, delay: next.delayed ? next.delay : 0, stopped: false,
        tick(at: number) {assert.equal(handle.stopped, false); value = at;}, finish() {assert.equal(handle.stopped, false); value = handle.to;}};
      node.running = handle;
    } else {value = next; node.running = undefined;}
  }};
  return node;
}
async function rig() {
  let tree: any, reduce = false, current = true, interactive = true;
  const contexts: any[] = [];
  const scrubberContexts = () => tree.props.children.props.children;
  const reactions: any[] = [], listeners = new Set<() => void>();
  const controller = {progress: cell(1), dragging: cell(false), presentationGoal: cell(1)};
  const scope = {}, opening = {}, fullOpening = {};
  const binding = {fence, scope, opening, pageOpen: false, controller, current: () => current,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const {ListeningCoverProvider, useListeningPageStyle, useListeningTabBarPaint, ListeningSeekPaint} = await screenModule(new URL('../src-next/kit/dock/ListeningCoverBridge.tsx', import.meta.url), {
    react: {...React, default: React, createContext: () => {const value = {Provider: 'Provider'};contexts.push(value);return value;},
      useContext: (context: any) => context === contexts[0] ? tree.props.value : context === contexts[1] ? scrubberContexts().props.value : scrubberContexts().props.children.props.value},
    'react-native': {View: 'View'},
    'react-native-gesture-handler': {Gesture: {Pan: () => ({enabled() {return this;}})}, GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'},
      useSharedValue(initial: any) {const ref = React.useRef<any>(null); if (!ref.current) ref.current = cell(initial); return ref.current;},
      useAnimatedReaction(read: any, apply: any) {const ref = React.useRef<any>(null); if (!ref.current) {ref.current = {};reactions.push(ref.current);}Object.assign(ref.current, {read, apply});},
      useAnimatedStyle: (read: any) => ({read}), cancelAnimation: (node: any) => {if (node.running) node.running.stopped = true;},
      withTiming: (to: any, config: any) => ({animation: true, to, config}), withDelay: (delay: number, inner: any) => ({delayed: true, delay, inner})},
    '../../../../../packages/player-core/src': {sameFence}, './model': {validRect},
    '../theme': {useTheme: () => ({layers: {player: 30}, colors: {accentStrong: '#abc'}})},
    '../useMotion': {useMotion: () => ({reduce, transforms: !reduce, timing: (kind: string) => ({duration: reduce ? 160 : kind === 'quick' ? 120 : 180, easing: reduce ? 'linear' : 'out'})})},
    '../overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({interactive})}, './ListeningCoverPaint': {ListeningCoverPaint: 'ListeningCoverPaint'},
    '../overlays/CommittedPaint': {CommittedPaintProvider: 'CommittedPaintProvider'},
  });
  const provider = createFabricHookHost(() => {tree = ListeningCoverProvider({children: null});});await provider.mount();
  const step = () => {for (const reaction of reactions) {const now = reaction.read();reaction.apply(now, reaction.previous);reaction.previous = now;}};
  await act(() => tree.props.value.publish(binding)); tree.props.value.reportPageHeight(690);step();
  let pageStyle: any; const page = createFabricHookHost(() => {pageStyle = useListeningPageStyle(fence as any, 690);}); await page.mount();
  let tab: any; const tabHost = createFabricHookHost(() => {tab = useListeningTabBarPaint();});await tabHost.mount();
  return {controller, context: () => tree.props.value, step,
    async publishScrubber(value: any) {let off: any;await act(() => {off = scrubberContexts().props.value(value);});return async () => {await act(() => off());};},
    async mountSeek() {
      let seekTree: any;
      const host = createFabricHookHost(() => {seekTree = ListeningSeekPaint({fence, fraction: .1, span: 100, seconds: 10, label: 'Seek'});});await host.mount();
      return {host, get tree() {return seekTree;}, view: () => seekTree.props.children, fill: () => seekTree.props.children.props.children.props.children.props.style[1].read()};
    },
    inert() {interactive = false;},
    async publishSame() {await act(() => tree.props.value.publish({...binding, opening: fullOpening, pageOpen: true}));step();},
    async nextTrack() {await act(() => tree.props.value.publish({...binding, fence: dockFence('next'), opening: fullOpening, pageOpen: true}));step();await page.rerender();},
    async reduce() {reduce = true; await provider.rerender();await act(() => {});step();await page.rerender();await tabHost.rerender();},
    async retire() {current = false;await act(() => {for (const listener of [...listeners]) listener();});step();await page.rerender();await tabHost.rerender();},
    tabStyle: () => tab.style.read(), measureTab: (height: number) => tab.measure(height),
    pageStyle: () => pageStyle.read(), async close() {await tabHost.unmount();await page.unmount();await provider.unmount();}};
}

test('phone tabs use their actual footprint and shared page reduction, with no independent transition clock', async () => {
  const r = await rig();
  try {
    r.measureTab(84);r.controller.progress.value = .5;r.step();
    assert.equal(r.tabStyle().transform[0].translateY, 84 * .6);
    r.controller.progress.value = 0;r.step();
    assert.equal(r.tabStyle().transform[0].translateY, 84);
    r.measureTab(62);
    assert.equal(r.tabStyle().transform[0].translateY, 62, 'live layout changes use the actual bar, not the drawn portrait height');
    r.controller.progress.value = .832;r.controller.presentationGoal.value = 0;r.step();await r.reduce();
    assert.ok(Math.abs(r.tabStyle().opacity - .58) < .0001);
    assert.equal(r.tabStyle().transform[0].translateY, 0);
    r.controller.dragging.value = true;r.controller.progress.value = 0;r.step();
    assert.equal(r.tabStyle().transform[0].translateY, 62, 'reduction preserves direct finger tracking');
    await r.retire();
    assert.deepEqual(r.tabStyle(), {opacity: 1, transform: [{translateY: 0}]});
  } finally {await r.close();}
});

test('first mini drag starts one260/320/380 group clock and the admitted body handoff does not restart it', async () => {
  const r = await rig();
  try {
    r.controller.dragging.value = true; r.controller.progress.value = .8; r.step();
    const handles = r.context().groups.map((value: any) => value.running);
    assert.deepEqual(handles.map((value: any) => value.delay), [260, 320, 380]);
    assert.deepEqual(handles.map((value: any) => value.config.duration), [180, 180, 180]);
    assert.ok(Math.abs(r.context().pageAlpha.value - .5) < .0001);
    r.controller.dragging.value = false; r.controller.presentationGoal.value = 0; r.step();
    await r.publishSame();
    assert.deepEqual(r.context().groups.map((value: any) => value.running), handles);
    assert.equal(handles[0].stopped, false);
    r.controller.presentationGoal.value = 1; r.step();
    assert.deepEqual(r.context().groups.map((value: any) => value.running.config.duration), [120, 120, 120]);
    assert.equal(handles[0].stopped, true);
  } finally {await r.close();}
});

test('reduction retargets partial page/group opacity160linear while removing autonomous page travel', async () => {
  const r = await rig();
  try {
    r.controller.presentationGoal.value = 0; r.controller.progress.value = .832; r.step();
    r.context().groups[0].running.tick(.3);
    assert.ok(Math.abs(r.context().pageAlpha.value - .42) < .0001);
    await r.reduce();
    assert.ok(Math.abs(r.context().pageAlpha.running.from - .42) < .0001);
    assert.equal(r.context().pageAlpha.running.config.duration, 160);
    assert.equal(r.context().pageAlpha.running.config.easing, 'linear');
    assert.equal(r.context().groups[0].running.from, .3);
    assert.equal(r.context().groups[0].running.config.duration, 160);
    assert.deepEqual(r.pageStyle().transform, [{translateY: 0}]);
    r.controller.dragging.value = true; r.step();
    assert.equal(r.pageStyle().transform[0].translateY, 690 * .832, 'direct finger progress remains direct under reduction');
  } finally {await r.close();}
});

test('a track replacement in the same open page preserves group paint without admitting old-fence pixels', async () => {
  const r = await rig();
  try {
    r.controller.presentationGoal.value = 0;r.controller.progress.value = 0;r.step();await r.publishSame();
    const handles = r.context().groups.map((value: any) => value.running);
    for (const handle of handles) handle.finish();
    await r.nextTrack();
    assert.deepEqual(r.context().groups.map((value: any) => value.value), [1, 1, 1]);
    assert.deepEqual(r.context().groups.map((value: any) => value.running), handles, 'no second entrance clock on Next');
    assert.equal(r.pageStyle().opacity, 0, 'preserved numeric opacity never grants the old fence paint permission');
  } finally {await r.close();}
});

test('reversing an outgoing page retargets its actual partial paint with only the group stagger', async () => {
  const r = await rig();
  try {
    r.controller.presentationGoal.value = 0;r.step();await r.publishSame();
    for (const value of r.context().groups) value.running.finish();
    r.controller.presentationGoal.value = 1;r.step();
    for (const value of r.context().groups) value.running.tick(.45);
    const outgoing = r.context().groups.map((value: any) => value.running);
    r.controller.presentationGoal.value = 0;r.step();
    assert.deepEqual(r.context().groups.map((value: any) => value.running.from), [.45, .45, .45]);
    assert.deepEqual(r.context().groups.map((value: any) => value.running.delay), [0, 60, 120]);
    assert.ok(outgoing.every((handle: any) => handle.stopped));
    for (const value of r.context().groups) value.running.tick(0);
    r.controller.presentationGoal.value = 1;r.step();
    r.controller.presentationGoal.value = 0;r.step();
    assert.deepEqual(r.context().groups.map((value: any) => value.running.delay), [0, 60, 120],
      'reversal before a delayed group becomes visible must not replay the initial260ms wait');
  } finally {await r.close();}
});

test('artless admitted paint still gets a page clock and original retirement cancels it', async () => {
  const r = await rig();
  try {
    assert.equal(r.context().binding.source, undefined, 'a synthetic cover is never required');
    r.controller.presentationGoal.value = 0; r.controller.progress.value = .4;r.step();
    assert.equal(r.context().pageAlpha.value, 1);
    const old = r.context().groups[0].running;
    await r.retire();
    assert.equal(old.stopped, true);
    assert.equal(r.context().pageAlpha.value, 0);
    assert.deepEqual(r.context().groups.map((value: any) => value.value), [0, 0, 0]);
    assert.equal(r.pageStyle().opacity, 0);
  } finally {await r.close();}
});


test('retiring the live scrubber keeps its last painted fraction and footprint without retaining its recogniser', async () => {
  const r = await rig(), context = r.context();
  const channel = {fence, gesture: {synthetic: 'original'}, scope: 7, activeScope: cell(7), ticket: cell(1), drag: cell(null), base: cell(.2), travel: cell(0)};
  const off = await r.publishScrubber(channel), widget = await r.mountSeek();
  try {
    assert.equal(r.context(), context, 'live scrubber publication does not change the main context read by the business publisher');
    widget.view().props.onLayout({nativeEvent: {layout: {width: 200}}});r.step();
    assert.equal(channel.travel.value, 200);
    channel.drag.value = .65;r.step();
    assert.equal(widget.fill().transform[1].scaleX, .65);
    channel.ticket.value = 0;channel.drag.value = null;r.step();
    r.inert();await off();await widget.host.rerender();r.step();
    assert.equal(widget.fill().transform[1].scaleX, .65, 'engine/rest facts cannot erase the captured actual preview');
    assert.equal(widget.view().props.style.height, 28);
    assert.notEqual(widget.tree.props.gesture, channel.gesture, 'the retired business recogniser is no longer installed');
  } finally {await widget.host.unmount();await r.close();}
});

test('listening scrub accessibility actions use the live publication and refuse retired scopes', async () => {
  const r = await rig(), directions: number[] = [];
  const channel = {fence, gesture: {}, scope: 7, activeScope: cell(7), ticket: cell(1), drag: cell(null), base: cell(.2), travel: cell(0),
    adjust: (direction: number) => directions.push(direction)};
  const off = await r.publishScrubber(channel), widget = await r.mountSeek();
  const action = (name: string) => widget.view().props.onAccessibilityAction({nativeEvent: {actionName: name}});
  try {
    assert.equal(widget.view().props.accessible, true);
    assert.equal(widget.view().props.accessibilityRole, 'adjustable');
    assert.deepEqual(widget.view().props.accessibilityActions, [{name: 'increment'}, {name: 'decrement'}]);
    action('increment'); action('decrement'); action('activate');
    assert.deepEqual(directions, [1, -1]);
    channel.ticket.value = 0;
    action('increment');
    channel.ticket.value = 2; channel.activeScope.value = 8;
    action('decrement');
    assert.deepEqual(directions, [1, -1], 'native callbacks queued after scope retirement cannot seek');
    r.inert(); await off(); await widget.host.rerender();
    assert.equal(widget.view().props.accessible, false);
    assert.deepEqual(widget.view().props.accessibilityActions, []);
    action('increment');
    assert.deepEqual(directions, [1, -1]);
  } finally {await widget.host.unmount(); await r.close();}
});
