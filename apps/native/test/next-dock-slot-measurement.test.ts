// @fabric-react
import {dockHandoverFixture} from './helpers/screen-harness.mjs';
import {listeningCoverFixture} from './helpers/screen-harness.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {PlaybackDock, sameFence, type DockPlayback, type QueuePlaybackFence, type QueueScope} from '../../../packages/player-core/src/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import * as model from '../src-next/kit/dock/model.ts';
import type {DockHostProps, DockPresentation, DockRect} from '../src-next/kit/dock/types.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

// Actual DockHost, useDockPresence and MediaBar run under the existing React
// driver. Only native hosts/measurement delivery and Reanimated are controlled.
// This proves callback ownership, not Android frame timing or physical pixels.
const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
const video: DockPlayback = {fence, kind: 'video', queue: null, positionSeconds: 75, playing: true, expanded: false};
const presentation: DockPresentation = {fence, identity: {id: 'item', kind: 'video', title: 'Film', subtitle: []}, skipBackSeconds: 10,
  capabilities: {play: true, pause: true, stop: true, seek: true, previous: false, next: false, cast: false}};
type Timing = {to: number; complete?: (finished: boolean) => void; apply?: () => void};
function elements(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  // Presentation slots are materialised JSX, not evaluated component bodies.
  return React.isValidElement<any>(node) ? [node, ...['children', 'row', 'opener', 'art', 'words', 'controls', 'tools', 'line'].flatMap(key => elements(node.props[key]))] : [];
}
async function rig(reduced = false) {
  const timings: Timing[] = [], cancelled: object[] = [], deliveries: DockRect[] = [], proofs: (() => boolean)[] = [];
  const measurements: {rect: DockRect; deliver: (rect?: DockRect) => void}[] = [];
  const chain: any = new Proxy({}, {get: () => () => chain});
  let motion = {transforms: !reduced, timing: () => ({duration: reduced ? 160 : 180}), spring: () => undefined};
  const theme = {isTV: false, layers: {player: 30}, colors: {text: '#fff', shadow: '#000'}, layout: {gutter: {tvRight: 96}}};
  const animated = {
    default: {View: 'AnimatedView'},
    useSharedValue: (initial: number | boolean) => {
      const ref = React.useRef<any>(null);
      if (!ref.current) {
        let current = initial;
        ref.current = {get value() {return current;}, set value(next: number | boolean | Timing) {
          if (typeof next !== 'object') current = next;
          else next.apply = () => {current = next.to;};
        }};
      }
      return ref.current;
    },
    useAnimatedStyle: (read: () => unknown) => ({read}),
    useAnimatedReaction() {},
    withTiming: (to: number, _config: unknown, complete?: Timing['complete']) => {
      const timing = {to, complete}; timings.push(timing); return timing;
    },
    withDelay: (_delay: number, timing: Timing) => timing,
    withSpring: () => {throw new Error('Unexpected spring in slot measurement case');},
    cancelAnimation: (value: object) => {cancelled.push(value);}, runOnJS: (fn: unknown) => fn,
  };
  const react = {...React, default: React};
  const {useDockPresence} = await screenModule(new URL('../src-next/kit/dock/useDockPresence.ts', import.meta.url), {
    react, 'react-native-reanimated': animated, '../../../../../packages/player-core/src/index': {sameFence}, '../useMotion': {useMotion: () => motion}, './model': model,
  });
  const {MediaBar} = await screenModule(new URL('../src-next/kit/dock/MediaBar.tsx', import.meta.url), {
    react, 'react-native': {View: 'View', Text: 'Text', ActivityIndicator: 'ActivityIndicator'},
    'react-native-gesture-handler': {Gesture: {Pan: () => chain}, GestureDetector: 'GestureDetector'}, 'react-native-reanimated': animated,
    '../../../../../packages/i18n/src/index': {defaultI18n}, '../../../../../packages/player-core/src/index': {sameFence},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: () => ''},
    './DockHandover': dockHandoverFixture,
    './ListeningCoverBridge': listeningCoverFixture,
    '../artwork': {Artwork: 'Artwork'}, '../controls': {Button: 'Button', Focusable: 'Focusable'}, '../overlays': {Popover: 'Popover'}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => theme}, '../useMotion': {useMotion: () => motion}, '../../platform/native-blur': {BlurView: 'BlurView'}, '../../platform/haptics': {selectionHaptic: async () => false}, './model': model,
    './visual': {alpha: () => '#000', DockFacts: 'DockFacts', dockStyles: {}, dockType: () => ({})},
  });
  const {DockHost} = await screenModule(new URL('../src-next/kit/dock/DockHost.tsx', import.meta.url), {
    react, 'react-native': {View: 'View'}, 'react-native-reanimated': animated,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})}, '../theme': {useTheme: () => theme},
    '../../../../../packages/player-core/src/index': {sameFence}, './MediaBar': {MediaBar}, './NowPlayingCard': {NowPlayingCard: 'NowPlayingCard'},
    './DockNotice': {DockNotice: 'DockNotice'}, './model': model, './visual': {alpha: () => '#000', dockStyles: {}}, './DockClearance': {useDockClearanceReport() {}}, '../useMotion': {useMotion: () => motion}, './useDockPresence': {useDockPresence},
  });
  const dock = new PlaybackDock(scope);
  let props: DockHostProps = {dock, snapshot: dock.getSnapshot(), presentation: {local: presentation}, onAction() {}, onVideoSlot: (rect, proof) => {deliveries.push(rect); if (proof) proofs.push(proof);}};
  let closed = false;
  let tree: any, barTree: any, barProps: any, barHost: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  let layout = {x: 24, y: 699.5, width: 80, height: 45};
  const translatedY = () => {
    // The dim layer is a separate AnimatedView. Native slot coordinates inherit the
    // presence container that actually owns MediaBar, not that earlier sibling.
    const container = elements(tree).find(node => node.type === 'AnimatedView'
      && elements(node.props.children).some(child => child.type === MediaBar));
    assert.ok(container, 'the measured art belongs to the drawn presence container');
    const animatedStyle = container.props.style.flat(Infinity).find((style: any) => typeof style?.read === 'function');
    assert.ok(animatedStyle, 'the presence container exposes the controlled native transform');
    return animatedStyle.read().transform[0].translateY;
  };
  const artNode = {measureInWindow(callback: (x: number, y: number, width: number, height: number) => void) {
    const rect = {...layout, y: layout.y + translatedY()};
    measurements.push({rect, deliver: (value = rect) => callback(value.x, value.y, value.width, value.height)});
  }};
  const host = createFabricHookHost(() => {tree = DockHost(props);});
  await host.mount();
  async function sync() {
    await act(() => {});
    if (closed) return;
    const bar = elements(tree).find(node => node.type === MediaBar);
    if (!bar) {await barHost?.unmount(); barHost = undefined; return;}
    barProps = bar.props;
    if (!barHost) {
      barHost = createFabricHookHost(() => {
        barTree = MediaBar(barProps);
        elements(barTree).find(node => node.props.collapsable === false).props.ref.current = artNode;
      });
      await barHost.mount();
    } else await barHost.rerender();
    await act(() => {});
  }
  async function render(next: Partial<DockHostProps> = {}) {
    props = {...props, snapshot: dock.getSnapshot(), ...next}; await host.rerender(); await sync();
  }
  async function start() {dock.start(video); await render();}
  async function complete(timing = timings.filter(timing => !!timing.complete).at(-1)!, finished = true) {
    if (finished) timing.apply?.();
    await act(() => timing.complete?.(finished)); await sync();
  }
  function layoutEvent() {elements(barTree).find(node => node.props.collapsable === false).props.onLayout();}
  return {dock, start, render, complete, layoutEvent, timings, measurements, deliveries, proofs, cancelled, translatedY,
    artNode, get tree() {return tree;}, get barTree() {return barTree;},
    layout(value: typeof layout) {layout = value;},
    async reduce(value: boolean) {motion = {...motion, transforms: !value}; await render();},
    async close() {closed = true; await barHost?.unmount(); await host.unmount();},
  };
}

test('actual presence completion remeasures the same art slot without another layout and rejects the initial translation', async () => {
  const r = await rig();
  try {
    await r.start();
    assert.equal(r.translatedY(), 12, 'the controlled native measurement includes the real ancestor transform');
    r.layoutEvent();
    for (const measurement of r.measurements) measurement.deliver();
    assert.deepEqual(r.deliveries, [], 'unsettled window coordinates must not be admitted');
    const node = elements(r.barTree).find(element => element.props.collapsable === false).props.ref.current;
    await r.complete();
    assert.equal(r.translatedY(), 0);
    assert.strictEqual(elements(r.barTree).find(element => element.props.collapsable === false).props.ref.current, node);
    assert.ok(r.measurements.length > 0, 'completion requests a new native measurement without onLayout');
    r.measurements.at(-1)!.deliver();
    assert.deepEqual(r.deliveries, [{x: 24, y: 699.5, width: 80, height: 45}]);
    assert.equal(elements(r.barTree).filter(element => element.props.collapsable === false).length, 1);
  } finally {await r.close();}
});

test('reduced motion admits real layout immediately without waiting for the opacity callback', async () => {
  const r = await rig(true);
  try {
    await r.start(); r.layoutEvent();
    assert.equal(r.translatedY(), 0);
    r.measurements.at(-1)!.deliver();
    assert.deepEqual(r.deliveries, [{x: 24, y: 699.5, width: 80, height: 45}]);
  } finally {await r.close();}
});

test('interrupted entry, exit and return reject old completion and measurement ownership', async () => {
  const r = await rig();
  try {
    await r.start(); const oldEntry = r.timings.filter(timing => !!timing.complete).at(-1)!;
    await r.complete(oldEntry, false); r.layoutEvent();
    for (const measurement of r.measurements) measurement.deliver();
    assert.deepEqual(r.deliveries, []);
    await r.render({visible: false}); const exit = r.timings.filter(timing => !!timing.complete).at(-1)!;
    await r.complete(oldEntry); assert.deepEqual(r.deliveries, []);
    await r.render({visible: true}); const entry = r.timings.filter(timing => !!timing.complete).at(-1)!;
    await r.complete(exit); assert.ok(elements(r.tree).some(node => node.type?.name === 'MediaBar'));
    await r.complete(entry); r.layoutEvent(); const pending = r.measurements.at(-1)!;
    await r.render({visible: false}); pending.deliver();
    assert.deepEqual(r.deliveries, [], 'the outgoing chrome is not measurement authority');
  } finally {await r.close();}
});

test('a new native layout supersedes earlier replies and the measured ref must still be the same', async () => {
  const r = await rig(true);
  try {
    await r.start(); r.layoutEvent(); const first = r.measurements.at(-1)!;
    r.layout({x: 30, y: 680, width: 80, height: 45}); r.layoutEvent(); const second = r.measurements.at(-1)!;
    second.deliver(); first.deliver();
    assert.deepEqual(r.deliveries, [{x: 30, y: 680, width: 80, height: 45}]);
    r.layoutEvent(); const oldRef = r.measurements.at(-1)!;
    elements(r.barTree).find(element => element.props.collapsable === false).props.ref.current = {};
    oldRef.deliver(); assert.equal(r.deliveries.length, 1);
  } finally {await r.close();}
});

test('live core changes reject a late measure even before DockHost rerenders', async () => {
  for (const change of ['source', 'authority', 'expanded', 'remote'] as const) {
    const r = await rig(true);
    try {
      await r.start(); r.layoutEvent(); const pending = r.measurements.at(-1)!;
      if (change === 'source') r.dock.start({...video, fence: {...fence, generation: 2}});
      if (change === 'authority') r.dock.reset({...scope, profileId: 'other-profile'});
      if (change === 'expanded') r.dock.expand(fence);
      if (change === 'remote') r.dock.beginRemote({id: 'receiver', kind: 'cast', sequence: '1', phase: 'connecting', playback: video, positionSeconds: 75}, 0);
      pending.deliver(); assert.deepEqual(r.deliveries, [], change);
    } finally {await r.close();}
  }
});

test('source and authority replacement require a new settlement and discard prior geometry', async () => {
  const r = await rig();
  try {
    await r.start(); const firstEntry = r.timings.filter(timing => !!timing.complete).at(-1)!;
    const nextFence = {...fence, playback: {...fence.playback, scope: {...scope, profileId: 'next-profile'}}, generation: 2};
    r.dock.reset(nextFence.playback.scope); r.dock.start({...video, fence: nextFence});
    await r.render({presentation: {local: {...presentation, fence: nextFence}}}); const nextEntry = r.timings.filter(timing => !!timing.complete).at(-1)!;
    await r.complete(firstEntry); r.layoutEvent();
    for (const measurement of r.measurements) measurement.deliver();
    assert.deepEqual(r.deliveries, []);
    await r.complete(nextEntry); r.measurements.at(-1)!.deliver();
    assert.equal(r.deliveries.length, 1);
  } finally {await r.close();}
});

test('position updates preserve a current entry instead of continually restarting its completion', async () => {
  const r = await rig();
  try {
    await r.start(); const entry = r.timings.filter(timing => !!timing.complete).at(-1)!, count = r.timings.length;
    r.dock.update({...video, positionSeconds: 76}); await r.render();
    assert.equal(r.timings.length, count, 'new playback facts are not a new presence intent');
    await r.complete(entry); r.measurements.at(-1)!.deliver();
    assert.equal(r.deliveries.length, 1);
  } finally {await r.close();}
});

test('callback replacement, invalid native geometry and unmount cannot apply an old reply', async () => {
  const r = await rig(true), newer: DockRect[] = [];
  try {
    await r.start(); r.layoutEvent(); const oldCallback = r.measurements.at(-1)!;
    await r.render({onVideoSlot: rect => newer.push(rect)}); oldCallback.deliver();
    assert.deepEqual(r.deliveries, []); assert.deepEqual(newer, []);
    r.layoutEvent(); r.measurements.at(-1)!.deliver({x: 24, y: NaN, width: 80, height: 45});
    r.layoutEvent(); r.measurements.at(-1)!.deliver({x: 24, y: 699.5, width: 0, height: 45});
    assert.deepEqual(newer, []);
    r.layoutEvent(); const pending = r.measurements.at(-1)!;
    await r.close(); pending.deliver();
    for (const timing of r.timings) await r.complete(timing);
    assert.deepEqual(r.deliveries, []); assert.deepEqual(newer, []);
    assert.ok(r.cancelled.length > 0);
  } finally {await r.close();}
});

test('a motion-policy change immediately retires the prior admission and remeasures without a transform', async () => {
  const r = await rig();
  try {
    await r.start(); const entry = r.timings.filter(timing => !!timing.complete).at(-1)!;
    await r.complete(entry); r.layoutEvent(); const pending = r.measurements.at(-1)!;
    await r.reduce(true); pending.deliver();
    assert.deepEqual(r.deliveries, []);
    r.measurements.at(-1)!.deliver();
    assert.deepEqual(r.deliveries, [{x: 24, y: 699.5, width: 80, height: 45}]);
    await r.complete(entry); pending.deliver();
    assert.equal(r.deliveries.length, 1, 'late old animation and native callbacks stay retired');
  } finally {await r.close();}
});

// Authored source vector, UNRUN: this hook/native-prop fixture does not measure
// physical glass, decoded outgoing video or a native presentation handover.
test('a touch occupant handover keeps settled glass in place and admits only the new slot completion', async () => {
  const r = await rig();
  try {
    await r.start();
    const first = r.timings.filter(timing => !!timing.complete).at(-1)!;
    await r.complete(first);
    assert.equal(r.translatedY(), 0);
    const nextFence = {...fence, generation: 2, sourceId: 'second-source', itemId: 'second-item'};
    r.dock.start({...video, fence: nextFence});
    await r.render({presentation: {local: {...presentation, fence: nextFence}}});
    const handover = r.timings.filter(timing => !!timing.complete).at(-1)!;
    assert.notEqual(handover, first);
    assert.equal(r.translatedY(), 0, 'the existing glass never resets to its insertion offset');
    const before = r.deliveries.length;
    await r.complete(first); r.layoutEvent();
    for (const measurement of r.measurements) measurement.deliver();
    assert.equal(r.deliveries.length, before, 'a retired completion cannot admit successor geometry');
    await r.complete(handover);
    r.measurements.at(-1)!.deliver();
    assert.equal(r.deliveries.length, before + 1);
    assert.equal(r.translatedY(), 0);
  } finally {await r.close();}
});


// Authored UNRUN original-layout witness vector. No measured-device or native
// snapshot-transfer acceptance follows from the controlled callback fixture.
test('an admitted mini proof survives ordinary facts but not native relayout or source replacement', async () => {
  const r = await rig();
  try {
    await r.start(); await r.complete();
    r.measurements.at(-1)!.deliver();
    const original = r.proofs.at(-1)!; assert.equal(original(), true);
    r.dock.update({...video, positionSeconds: 76}); await r.render();
    assert.equal(original(), true, 'callback replacement for live facts does not invent a new view opening');
    r.layoutEvent();
    assert.equal(original(), false, 'the original native layout witness retires before a new measurement reply');
    r.measurements.at(-1)!.deliver();
    const next = r.proofs.at(-1)!; assert.equal(next(), true);
    assert.equal(original(), false, 'the old proof never adopts the new layout');
    r.dock.start({...video, fence: {...fence, generation: 2}});
    assert.equal(next(), false, 'a source change retires the witness even before React rerenders');
  } finally {await r.close();}
});
