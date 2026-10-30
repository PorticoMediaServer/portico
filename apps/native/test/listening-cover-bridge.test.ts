// @fabric-react
import {dockFence} from './helpers/dock-fence.ts';
/** Authored UNRUN source vectors. Actual bridge hooks, manually supplied context,
 * controlled native measurement callbacks and source-ready notifications only.
 * This is not a reconciler, decoded-image, native geometry or gesture proof. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {sameFence} from '../../../packages/player-core/src/index.ts';
import {validRect} from '../src-next/kit/dock/model.ts';
import {createFabricHookHost, act} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const fence = dockFence();
const source = {cacheKey: 'original', uri: '/actual-art', headers: {Authorization: 'fixture'}};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : React.isValidElement<any>(node) ? [node, ...nodes(node.props.children)] : [];
async function rig() {
  let tree: any;
  const ctx = () => tree.props.value;
  const {ListeningCoverProvider, useListeningCoverSlot} = await screenModule(new URL('../src-next/kit/dock/ListeningCoverBridge.tsx', import.meta.url), {
    react: {...React, default: React, createContext: () => ({Provider: 'Provider'}), useContext: () => ctx()},
    'react-native': {View: 'View'},
    'react-native-gesture-handler': {Gesture: {Pan: () => ({enabled() {return this;}})}, GestureDetector: 'GestureDetector'}, 'react-native-reanimated': {default: {View: 'AnimatedView'}, useSharedValue: (value: any) => React.useRef({value}).current,
      useAnimatedReaction() {}, cancelAnimation() {}, withTiming: (value: number) => value, withDelay: (_delay: number, value: number) => value, useAnimatedStyle: (read: () => unknown) => ({read})},
    '../../../../../packages/player-core/src': {sameFence}, '../theme': {useTheme: () => ({layers: {player: 30}})},
    '../overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({interactive: true})},
    '../overlays/CommittedPaint': {CommittedPaintProvider: 'CommittedPaintProvider'},
    '../useMotion': {useMotion: () => ({transforms: true, timing: () => ({duration: 180})})}, './ListeningCoverPaint': {ListeningCoverPaint: 'ListeningCoverPaint'}, './model': {validRect},
  });
  const provider = createFabricHookHost(() => {tree = ListeningCoverProvider({children: null});}); await provider.mount();
  const canvas = () => nodes(tree).find(node => node.props.collapsable === false).props.ref;
  const canvasReplies: ((...rect: number[]) => void)[] = [];
  canvas().current = {measureInWindow: (reply: (...rect: number[]) => void) => canvasReplies.push(reply)};
  let current: any; const initialOpening = {}; const listeners = new Set<() => void>();
  const controller = {progress: {value: .5}, dragging: {value: true}};
  async function publish(nextFence: any = fence, nextScope: object = {}, nextSource: any = source, opening: object = initialOpening) {
    const entry = {fence: nextFence, source: nextSource, controller, scope: nextScope, opening, pageOpen: false, current: () => current === entry, subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
    current = entry;
    await act(() => {ctx().publish(entry);});
    return entry;
  }
  async function slot(kind: 'full' | 'mini', ownFence: any = fence) {
    const replies: ((...rect: number[]) => void)[] = [], node = {current: {measureInWindow: (reply: (...rect: number[]) => void) => {replies.push(reply);}}};
    let output: any;
    const host = createFabricHookHost(() => {output = useListeningCoverSlot(ownFence, kind, node);}); await host.mount();
    return {host, node, replies, get output() {return output;}};
  }
  async function deliver(viewSlot: Awaited<ReturnType<typeof slot>>, rect: number[]) {
    await act(() => {viewSlot.replies.at(-1)!(...rect);});
    await act(() => {canvasReplies.at(-1)!(10, 20, 480, 900);});
  }
  const layer = () => nodes(tree).find(node => node.type?.name === 'CoverLayer');
  return {provider, ctx, publish, slot, deliver, layer, canvas, canvasReplies,
    relayout: () => nodes(tree).find(node => node.props.collapsable === false).props.onLayout(),
    retire: () => {current = undefined; for (const listener of [...listeners]) listener();}};
}

test('only actual measured full and mini frames are converted through the measured canvas', async () => {
  const r = await rig(); await r.publish();
  const full = await r.slot('full'), mini = await r.slot('mini');
  try {
    assert.equal(r.layer(), undefined, 'no guessed mini frame');
    await r.deliver(full, [110, 220, 300, 300]);
    assert.deepEqual(r.ctx().full.bounds, {x: 100, y: 200, width: 300, height: 300});
    assert.equal(r.layer(), undefined);
    await r.deliver(mini, [34, 820, 44, 44]);
    assert.deepEqual(r.ctx().mini.bounds, {x: 24, y: 800, width: 44, height: 44});
    assert.equal(r.layer().props.full, r.ctx().full.bounds);
    assert.equal(r.ctx().decoded, false, 'geometry cannot stand in for an image-ready notification');
  } finally {await full.host.unmount(); await mini.host.unmount(); await r.provider.unmount();}
});

test('retired source, native node and canvas callbacks cannot publish successor geometry', async () => {
  const r = await rig(); await r.publish();
  const full = await r.slot('full'), mini = await r.slot('mini');
  try {
    const oldSourceReply = full.replies.at(-1)!;
    await r.publish({...fence, sourceId: 'successor', generation: 2});
    await act(() => {oldSourceReply(110, 220, 300, 300);});
    assert.equal(r.ctx().full, undefined);
    await r.publish(); await full.host.rerender();
    const oldNodeReply = full.replies.at(-1)!;
    full.node.current = {measureInWindow() {}};
    await act(() => {oldNodeReply(110, 220, 300, 300);});
    assert.equal(r.ctx().full, undefined);
    await mini.host.rerender();
    await act(() => {mini.replies.at(-1)!(34, 820, 44, 44);});
    const oldCanvasReply = r.canvasReplies.at(-1)!;
    r.canvas().current = {measureInWindow() {}};
    await act(() => {oldCanvasReply(10, 20, 480, 900);});
    assert.equal(r.ctx().mini, undefined);
  } finally {await full.host.unmount(); await mini.host.unmount(); await r.provider.unmount();}
});

test('a late mini-sized ready notification cannot erase readiness for the actual full-sized request', async () => {
  const r = await rig(), scope = {}; await r.publish(fence, scope);
  const full = await r.slot('full'), mini = await r.slot('mini');
  try {
    await r.deliver(mini, [34, 820, 44, 44]);
    const oldMiniReady = r.layer().props.onReady;
    await r.deliver(full, [110, 220, 300, 300]);
    const fullReady = r.layer().props.onReady;
    await act(() => {fullReady(source);});
    assert.equal(r.ctx().decoded, true);
    await act(() => {oldMiniReady(source);});
    assert.equal(r.ctx().decoded, true);
    await r.publish(fence, scope, {...source, headers: {Authorization: 'different-fixture'}});
    assert.equal(r.ctx().decoded, false, 'a source with different headers has no inherited ready witness');
  } finally {await full.host.unmount(); await mini.host.unmount(); await r.provider.unmount();}
});


test('a same-source reopen needs its own actual full slot while keeping the measured mini slot', async () => {
  const r = await rig(), scope = {}; await r.publish(fence, scope);
  const full = await r.slot('full'), mini = await r.slot('mini');
  try {
    await r.deliver(mini, [34, 820, 44, 44]);
    await r.deliver(full, [110, 220, 300, 300]);
    assert.ok(r.layer().props.full);
    await r.publish(fence, scope, source, {});
    assert.equal(r.layer().props.full, undefined, 'the retired opening is never the new target');
    assert.deepEqual(r.layer().props.mini, {x: 24, y: 800, width: 44, height: 44});
    await full.host.rerender();
    await r.deliver(full, [130, 250, 280, 280]);
    assert.deepEqual(r.layer().props.full, {x: 120, y: 230, width: 280, height: 280});
  } finally {await full.host.unmount(); await mini.host.unmount(); await r.provider.unmount();}
});


test('a canvas relayout invalidates both original coordinate frames until actual remeasurement', async () => {
  const r = await rig(); await r.publish();
  const full = await r.slot('full'), mini = await r.slot('mini');
  try {
    await r.deliver(full, [110, 220, 300, 300]); await r.deliver(mini, [34, 820, 44, 44]);
    assert.ok(r.layer());
    await act(() => r.relayout());
    assert.equal(r.layer(), undefined, 'old-origin geometry is never silently reused');
    await full.host.rerender(); await mini.host.rerender();
    await r.deliver(full, [110, 220, 300, 300]);
    assert.equal(r.layer(), undefined, 'the mini also needs this actual canvas revision');
    await r.deliver(mini, [34, 820, 44, 44]);
    assert.ok(r.layer());
  } finally {await full.host.unmount(); await mini.host.unmount(); await r.provider.unmount();}
});

test('original publication retirement removes passive paint without a successor publication', async () => {
  const r = await rig(); await r.publish();
  const full = await r.slot('full'), mini = await r.slot('mini');
  try {
    await r.deliver(full, [110, 220, 300, 300]); await r.deliver(mini, [34, 820, 44, 44]);
    assert.ok(r.layer());
    await act(() => r.retire());
    assert.equal(r.ctx().active, false);
    assert.equal(r.layer(), undefined);
  } finally {await full.host.unmount(); await mini.host.unmount(); await r.provider.unmount();}
});
