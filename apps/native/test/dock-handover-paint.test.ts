// @fabric-react
import {dockFence} from './helpers/dock-fence.ts';
/** Authored UNRUN source vectors. Actual presenter hooks with controlled shared
 * values and manually supplied child hooks; no reconciler, native image, frame
 * scheduling, native file transfer or physical glass acceptance is claimed. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createFabricHookHost, act} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {sameDockFence} from '../src-next/kit/dock/model.ts';
import {freezeOverlayPaint} from '../src-next/kit/overlays/overlay-paint-model.ts';

const INERT = Object.freeze({visible: true, interactive: false, root: true});
const fence = dockFence;
const occupant = (id: string, mode = 'music', remote?: any) => ({mode, playback: {fence: fence(id)}, remote});
async function rig() {
  let motion = policy(false), tree: any, props: any;
  const cells: any[] = [];
  function policy(reduce: boolean) {return {reduce, transforms: !reduce, duration: () => reduce ? 160 : 180,
    timing: () => ({duration: reduce ? 160 : 180, easing: reduce ? 'linear' : 'out'})};}
  const animated = {default: {View: 'AnimatedView'}, runOnJS: (fn: any) => fn, useAnimatedStyle: (read: () => any) => ({read}),
    useSharedValue(initial: number) {
      const ref = React.useRef<any>(null);
      if (!ref.current) {
        let value = initial;
        const cell: any = {get value() {return value;}, set value(next: any) {
          if (cell.running) cell.running.stopped = true;
          if (next && typeof next === 'object' && next.animation) {
            const handle: any = {...next, from: value, stopped: false, tick(to: number) {value = to;},
              finish() {assert.equal(handle.stopped, false); value = handle.to; handle.complete?.(true);},
              retire() {handle.complete?.(false);}};
            cell.running = handle;
          } else {value = next; cell.running = undefined;}
        }};
        ref.current = cell; cells.push(cell);
      }
      return ref.current;
    },
    withTiming: (to: number, config: any, complete?: any) => ({animation: true, to, config, complete}),
    cancelAnimation: (cell: any) => {if (cell.running) cell.running.stopped = true;},
  };
  const {DockHandover, DockPaintRow} = await screenModule(new URL('../src-next/kit/dock/DockHandover.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', Image: 'Image'}, 'react-native-reanimated': animated,
    '../overlays/overlay-paint-model': {freezeOverlayPaint}, '../overlays/OverlayPaintContext': {OverlayPaintProvider: 'PaintMode', OVERLAY_PAINT_INERT: INERT},
    '../useMotion': {useMotion: () => motion}, './model': {sameDockFence}, './ListeningCoverBridge': {useListeningMiniPaint: () => ({active: false})},
  });
  const allowed = [DockPaintRow, 'View', 'Text', 'Input', 'Image', React.Fragment];
  function row(label: string, width: number, onPress = () => {}) {
    return React.createElement(DockPaintRow, {opener: React.createElement('Input', {onPress, ref: {current: {native: 'original'}}}),
      art: React.createElement('View', {style: {width, height: 44}}, React.createElement('Text', {}, label)),
      words: React.createElement('Text', {}, label), controls: React.createElement('View', {}, React.createElement('Input', {onPress})),
      expanded: false, containerStyle: {}, textStyle: {}, artWidth: width, openerGap: 12, rowGap: 12});
  }
  const host = createFabricHookHost(() => {tree = DockHandover(props);});
  const phase = () => tree.props.children[1].props.value;
  const retiring = () => tree.props.children[0] as any[];
  return {row, allowed, phase, retiring, cells,
    async mount(next: any) {props = {...next, allowedTypes: allowed}; await host.mount(); await act(() => {});},
    async update(next: any) {props = {...props, ...next}; await host.rerender(); await act(() => {});},
    async reduce() {motion = policy(true); await host.rerender(); await act(() => {});},
    async mountRetiring(element: any) {let output: any; const child = createFabricHookHost(() => {output = element.type(element.props);}); await child.mount();
      return {host: child, get tree() {return output;}, phase: () => output.props.children.props.children.props.value};},
    async close() {await host.unmount();}};
}

test('an interrupted live copy retires from its actual pose while earlier exits keep their own clocks', async () => {
  const r = await rig(); let commands = 0;
  await r.mount({occupant: occupant('a'), row: r.row('A', 80, () => commands++)});
  await r.update({occupant: occupant('b'), row: r.row('B', 44)});
  const first = r.retiring()[0], outgoing = await r.mountRetiring(first);
  try {
    const frozen = first.props.entry.node;
    frozen.props.opener.props.onPress(); assert.equal(commands, 0);
    assert.equal('ref' in frozen.props.opener.props, false);
    assert.equal(outgoing.tree.props.mode, INERT);
    const firstFade = outgoing.phase().alpha.running; firstFade.tick(.7);
    const live = r.phase();
    live.alpha.running.tick(.4); live.x.running.tick(21.6); live.y.running.tick(3.6); live.scale.running.tick(.91);
    await r.update({occupant: occupant('c'), row: r.row('C', 80)});
    const entries = r.retiring();
    assert.equal(entries.length, 2);
    assert.equal(entries[0].props.entry, first.props.entry);
    assert.deepEqual(entries[1].props.entry.pose, {alpha: .4, x: 21.6, y: 3.6, scale: .91});
    assert.equal(firstFade.stopped, false);
    assert.equal(outgoing.phase().alpha.value, .7);
    assert.equal(r.phase().alpha.running.from, 0);
    assert.equal(r.phase().alpha.running.config.duration, 180);
  } finally {await outgoing.host.unmount(); await r.close();}
});

test('reduction retargets partial opacity at160 without retaining hidden spatial motion', async () => {
  const r = await rig();
  await r.mount({occupant: occupant('a'), row: r.row('A', 80)});
  await r.update({occupant: occupant('b'), row: r.row('B', 44)});
  const outgoing = await r.mountRetiring(r.retiring()[0]);
  try {
    r.phase().alpha.running.tick(.42); outgoing.phase().alpha.running.tick(.6);
    const retired = outgoing.phase().alpha.running;
    await r.reduce(); await outgoing.host.rerender(); await act(() => {});
    assert.equal(r.phase().alpha.running.from, .42);
    assert.equal(r.phase().alpha.running.config.duration, 160);
    assert.equal(r.phase().alpha.running.config.easing, 'linear');
    assert.equal(r.phase().x.value, 0); assert.equal(r.phase().y.value, 0); assert.equal(r.phase().scale.value, 1);
    assert.equal(outgoing.phase().alpha.running.from, .6);
    assert.equal(outgoing.phase().alpha.running.config.duration, 160);
    retired.retire();
    assert.equal(r.retiring().length, 1, 'a stopped completion removes no active layer');
    assert.equal(outgoing.phase().alpha.running.stopped, false);
  } finally {await outgoing.host.unmount(); await r.close();}
});

test('receiver sequence-only observations update live facts without replaying a content handover', async () => {
  const r = await rig();
  await r.mount({occupant: occupant('receiver', 'cast', {id: 'receiver', phase: 'playing', sequence: '1'}), row: r.row('Receiver', 80)});
  try {
    await r.update({occupant: occupant('receiver', 'cast', {id: 'receiver', phase: 'playing', sequence: '2'}), row: r.row('Receiver updated', 80)});
    assert.equal(r.retiring().length, 0);
    assert.equal(r.phase().alpha.value, 1);
    assert.equal(r.phase().alpha.running, undefined);
  } finally {await r.close();}
});

test('only an offered original video file is painted, and retirement disposes that exact resource', async () => {
  const r = await rig(), originalFence = fence('video');
  let releases = 0, current = true; const listeners = new Set<() => void>(), claims: any[] = [];
  const life = new AbortController();
  const video = {fence: originalFence, windowBounds: {x: 24, y: 700, width: 80, height: 45}, image: {uri: 'file:///original', width: 1920, height: 1080},
    signal: life.signal, paintCurrent: () => current, subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, release: async () => {releases++;}};
  await r.mount({occupant: occupant('video', 'video'), row: r.row('Video', 80), takeVideoExit: (original: any) => {claims.push(original); return video;}});
  await r.update({occupant: occupant('audio'), row: r.row('Audio', 44)});
  const entry = r.retiring()[0], outgoing = await r.mountRetiring(entry);
  try {
    assert.deepEqual(claims, [originalFence]);
    assert.equal(entry.props.entry.video, video);
    // The presenter clones the frozen row and paints the offered frame beside the
    // row's own art, so the picture is the cloned art's Image, not the live tree.
    const painted = [entry.props.entry.node.props.art.props.children]
      .flatMap((node: any) => node.props.children as any[]);
    const images = painted.filter((node: any) => node.type === 'Image');
    assert.equal(images.length, 1, 'exactly the offered original frame is painted');
    assert.equal(images[0].props.source.uri, 'file:///original');
    await act(() => {current = false; for (const notify of [...listeners]) notify();});
    assert.equal(outgoing.tree, null);
    assert.equal(r.retiring().length, 0);
  } finally {await outgoing.host.unmount(); await r.close(); await Promise.resolve();}
  assert.equal(releases, 1);
});


test('missing original video permission skips old picture pixels while retaining inert words and controls', async () => {
  const r = await rig();
  await r.mount({occupant: occupant('video', 'video'), row: r.row('Video', 80)});
  await r.update({occupant: occupant('audio'), row: r.row('Audio', 44)});
  const entry = r.retiring()[0], outgoing = await r.mountRetiring(entry);
  try {
    assert.equal(entry.props.entry.video, undefined);
    assert.equal(entry.props.entry.skipArt, true);
    assert.equal(outgoing.phase().hideArt, true);
    assert.equal(entry.props.entry.node.props.words.props.children, 'Video');
    assert.equal(outgoing.tree.props.mode, INERT);
  } finally {await outgoing.host.unmount(); await r.close();}
});
