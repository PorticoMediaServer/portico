// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const animations: ((done: boolean) => void)[] = [], queued: (() => void)[] = [];
const motion = {reduce: false, timing: () => ({duration: 0})};
const {TVPanelTransition} = await screenModule(new URL('../src-next/kit/overlays/TVPanelMotion.tsx', import.meta.url), {
  react: {...React, default: React}, 'react-native': {StyleSheet: {absoluteFill: {}}, TVFocusGuideView: 'Guide'},
  'react-native-reanimated': {default: {View: 'Animated'}, useSharedValue: (value: number) => React.useRef({value}).current,
    useAnimatedStyle: (run: () => unknown) => run(), cancelAnimation() {},
    withTiming: (value: number, _config: unknown, done: (complete: boolean) => void) => {animations.push(done); return value;},
    runOnJS: (run: (...args: any[]) => void) => (...args: any[]) => queued.push(() => run(...args)),
  }, '../useMotion': {useMotion: () => motion},
});

test('queued old panel completions cannot admit or remove a reversed/replayed presentation', async () => {
  let open = true, exits = 0, tree: any;
  const host = createFabricHookHost(() => {tree = TVPanelTransition({open, onExited: () => {exits++;}, children: null});});
  await host.mount();
  try {
    animations.at(-1)!(true); // Opening completion is queued behind the next render.
    open = false; await host.rerender(); await host.flush();
    queued.shift()!(); await host.flush();
    assert.equal(tree.props.value.interactive, false);
    animations.at(-1)!(true); // Closing completion is queued behind a reversal.
    open = true; await host.rerender(); await host.flush();
    queued.shift()!(); await host.flush();
    assert.equal(exits, 0); assert.equal(tree.props.value.interactive, false);
    animations.at(-1)!(true); queued.shift()!(); await host.flush();
    assert.equal(tree.props.value.interactive, true);
    open = false; await host.rerender(); await host.flush();
    animations.at(-1)!(true); queued.shift()!(); await host.flush();
    assert.equal(exits, 1);
    open = true; await host.rerender(); animations.at(-1)!(true);
  } finally {await host.unmount();}
  queued.shift()!();
  assert.equal(exits, 1, 'unmount invalidates queued completion ownership');
});
