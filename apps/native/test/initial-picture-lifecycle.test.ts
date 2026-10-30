// @fabric-react
/** Authored UNRUN source. Actual presenter hooks/effects with controlled image
 * callbacks, UI shared values and animator completions; no native host, decoder,
 * RNGH scheduling, SurfaceView composition or device acceptance. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as model from '../src-next/kit/dock/initial-picture-model.ts';

type Animation = {animation: true; kind: string; to: any; config: any; callback?: (finished: boolean) => void; owner?: Value; stopped?: boolean; finish: () => void};
class Value {
  raw: any; running?: Animation;
  constructor(value: any) {this.raw = value;}
  get value() {return this.raw;}
  set value(next: any) {
    if (this.running) this.running.stopped = true;
    if (next?.animation === true) {this.running = next; next.owner = this;}
    else {this.running = undefined; this.raw = next;}
  }
}
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : React.isValidElement(value) ? [value, ...nodes((value as any).props.children)] : [];

async function fixture(t: any) {
  const animations: Animation[] = [], reactions = new Set<any>(), js: (() => void)[] = [];
  const make = (kind: string, to: any, config: any, callback?: (finished: boolean) => void): Animation => {
    const animation: Animation = {animation: true, kind, to, config, callback, finish() {
      assert.ok(animation.owner); assert.notEqual(animation.stopped, true, 'a cancelled animation cannot write a final value'); animation.owner!.raw = to; animation.owner!.running = undefined; callback?.(true);
    }};
    animations.push(animation); return animation;
  };
  const animated = {default: {View: 'AnimatedView'},
    useSharedValue: (value: any) => React.useRef(new Value(value)).current,
    useAnimatedStyle: (read: () => any) => ({read}),
    useAnimatedReaction: (prepare: () => any, react: (value: any, previous: any) => void) => {
      const record = React.useRef<any>({previous: undefined}).current; Object.assign(record, {prepare, react});
      React.useLayoutEffect(() => {reactions.add(record); return () => {reactions.delete(record);};}, [record]);
    },
    cancelAnimation: (value: Value) => {if (value.running) value.running.stopped = true; value.running = undefined;},
    runOnJS: (fn: (...args: any[]) => void) => (...args: any[]) => js.push(() => fn(...args)),
    withTiming: (to: any, config: any, callback?: any) => make('timing', to, config, callback),
    withSpring: (to: any, config: any, callback?: any) => make('spring', to, config, callback), withDelay: (_ms: number, animation: Animation) => animation,
  };
  const flushUI = () => {
    for (let pass = 0; pass < 20; pass++) {
      let changed = false;
      for (const record of reactions) {
        const next = record.prepare(), before = record.previous;
        if (before && Object.keys(next).every(key => Object.is(next[key], before[key]))) continue;
        record.previous = next; changed = true; record.react(next, before);
      }
      if (!changed) return;
    }
    assert.fail('controlled UI reaction did not settle');
  };
  const flushJS = () => {for (let n = 0; js.length; n++) {assert.ok(n < 100); js.shift()!();}};
  let publication: any, tree: any;
  const motion = {reduce: false, duration: () => 180, spring: () => ({damping: 32, stiffness: 260}), timing: (name = 'standard') => ({duration: name === 'quick' ? 120 : 180})};
  const react = {...React, default: React};
  const {InitialPicturePresentation} = await screenModule(new URL('../src-next/kit/dock/InitialPicturePresentation.tsx', import.meta.url), {
    react, 'react-native': {View: 'View', ActivityIndicator: 'ActivityIndicator'}, 'react-native-reanimated': animated,
    '../artwork': {Artwork: 'Artwork'}, '../useMotion': {useMotion: () => motion}, '../theme': {useTheme: () => ({colors: {ink: '#000', text: '#fff'}})},
    './initial-picture-model': model,
    './PlayerMorphBridge': {useInitialPlayerPublication: (value: any) => React.useLayoutEffect(() => {publication = value; return () => {if (publication === value) publication = undefined;};}, [value])},
  });
  const oldFrame = globalThis.requestAnimationFrame, oldCancel = globalThis.cancelAnimationFrame;
  let frameId = 0; const frames = new Map<number, FrameRequestCallback>();
  globalThis.requestAnimationFrame = callback => {const id = ++frameId; frames.set(id, callback); return id;};
  globalThis.cancelAnimationFrame = id => {frames.delete(id);};
  const offer = {key: Object.freeze({}), artwork: Object.freeze({uri: 'actual-local-art', cacheKey: 'actual-art'}),
    bounds: {x: 0, y: 250, width: 400, height: 520}, fence: {}, signal: new AbortController().signal, paintCurrent: () => true, subscribe: () => () => {}};
  const controller = {progress: new Value(0), velocity: new Value(0), dragging: new Value(false), presentationGoal: new Value(0)};
  const host = {current: {measureInWindow: (callback: any) => callback(0, 0, 400, 800)}};
  const props = {offer, host, controller, full: {x: 0, y: 0, width: 400, height: 225}, dock: {x: 20, y: 650, width: 80, height: 45}, docked: false, firstFrame: true, travel: 800};
  const driver = createFabricHookHost(() => {tree = InitialPicturePresentation(props);});
  t.after(async () => {await driver.unmount(); globalThis.requestAnimationFrame = oldFrame; globalThis.cancelAnimationFrame = oldCancel;});
  await act(() => driver.mount());
  await act(() => {nodes(tree).find(node => node.type === 'Artwork').props.onReady(offer.artwork);});
  const activeSpring = () => animations.findLast(animation => animation.kind === 'spring' && !animation.stopped);
  await act(() => {activeSpring()!.finish(); flushUI(); flushJS();});
  assert.equal(publication.landed, true);
  const firstPoster = animations.findLast(animation => animation.kind === 'timing' && animation.to === 0 && animation.config.duration === 180)!;
  assert.ok(firstPoster);
  return {animations, controller, activeSpring, firstPoster, flushUI, flushJS, get publication() {return publication;}, get tree() {return tree;}};
}

for (const completedBeforeDrag of [false, true]) test(`a fresh settlement re-arms disposal after a ${completedBeforeDrag ? 'completed' : 'partial'} poster fade and batched true-to-true landing`, async t => {
  const f = await fixture(t);
  await act(() => {
    if (completedBeforeDrag) f.firstPoster.finish();
    else f.firstPoster.owner!.raw = .42;
    f.controller.dragging.value = true; f.controller.progress.value = .2; f.controller.velocity.value = .5;
    f.flushUI();
    if (!completedBeforeDrag) {
      assert.equal(f.firstPoster.stopped, true, 'UI interruption cancels the in-flight fade immediately');
      f.firstPoster.callback?.(true); // maliciously late completion, never a value write
      assert.equal(f.firstPoster.owner!.value, .42);
    }
    f.flushJS(); // completion refuses active drag, pending false has not rendered
    // Settle back to the SAME expanded state before React flushes pending false.
    f.controller.dragging.value = false; f.controller.progress.value = 0; f.controller.velocity.value = 0;
    f.flushUI(); f.activeSpring()!.finish(); f.flushUI(); f.flushJS();
  });
  assert.ok(f.publication, 'the replacement fade still owns disposal');
  const replacement = f.animations.findLast(animation => animation.to === 0 && animation.config.duration === 180)!;
  assert.notEqual(replacement, f.firstPoster, 'a fresh UI receipt must re-arm even when landed remains true after batching');
  assert.equal(replacement.owner!.value, completedBeforeDrag ? 0 : .42, 're-arm starts at the actual painted value');
  await act(() => {replacement.finish(); f.flushJS();});
  assert.equal(f.publication, undefined); assert.equal(f.tree, null);
});

test('a 1-to-0 release keeps a live full-target spring without a later docked/full prop effect', async t => {
  const f = await fixture(t);
  await act(() => {
    // UI begins a collapse while the admitted JS props remain expanded. A rapid
    // reversal can coalesce that intermediate docked render away entirely.
    f.controller.presentationGoal.value = 1; f.controller.progress.value = .6; f.controller.velocity.value = 1;
    f.flushUI();
    f.controller.dragging.value = true; f.flushUI();
    f.controller.presentationGoal.value = 0; f.controller.dragging.value = false;
    f.controller.progress.value = .35; f.controller.velocity.value = -1;
    f.flushUI();
    const spring = f.activeSpring();
    assert.ok(spring); assert.equal(spring!.owner!.running, spring, 'goal invalidation must precede, not cancel, the final-goal settlement');
    assert.notEqual(spring!.stopped, true);
    spring!.finish(); f.controller.progress.value = 0; f.controller.velocity.value = 0;
    f.flushUI(); f.flushJS();
  });
  const poster = f.animations.findLast(animation => animation.to === 0 && animation.config.duration === 180)!;
  assert.notEqual(poster, f.firstPoster);
  await act(() => {poster.finish(); f.flushJS();});
  assert.equal(f.publication, undefined); assert.equal(f.tree, null);
});
