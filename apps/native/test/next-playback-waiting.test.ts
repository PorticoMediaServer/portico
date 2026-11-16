// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
async function waitingModule(reduce: boolean, repeats: unknown[]) {
  return screenModule(new URL('../src-next/kit/states/PlaybackWaiting.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-svg': {default: 'Svg', Circle: 'Circle'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, cancelAnimation() {}, useAnimatedStyle: (fn: () => unknown) => fn(),
      useSharedValue: (value: number) => React.useRef({value}).current, withTiming: (value: unknown) => value, withRepeat: (value: unknown) => {repeats.push(value); return value;}},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../theme': {useTheme: () => ({isTV: false, textScale: 1, colors: {text: '#edf5fa', textSecondary: '#a9b8c4', ink: '#08121b'}, type: {bodyStrong: {}}})},
    '../useMotion': {useMotion: () => ({reduce})}, '../controls/control-model': {colorAlpha: (color: string, alpha: number) => `${color}/${alpha}`},
  });
}

test('the waiting line appears only after three seconds, says Getting ready while converting and the connection line otherwise, and takes no touch', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const repeats: unknown[] = [], {PlaybackWaiting} = await waitingModule(false, repeats);
  let props = {waiting: true, preparing: true}, tree: any;
  const host = createFabricHookHost(() => {tree = PlaybackWaiting(props);});
  try {
    await act(() => host.mount());
    assert.equal(tree, null, 'a short wait draws nothing');
    await act(() => {t.mock.timers.tick(3000);}); await act(() => host.rerender());
    const text = () => nodes(tree).find(node => node.type === 'Text').props.children;
    assert.equal(text(), defaultI18n.t('player.gettingReady'));
    assert.equal(tree!.props.pointerEvents, 'none');
    assert.equal(repeats.length > 0, true, 'the ring turns');
    props = {waiting: true, preparing: false}; await act(() => host.rerender());
    assert.equal(text(), defaultI18n.t('playback.message.network_stall'));
    props = {waiting: false, preparing: false}; await act(() => host.rerender());
    assert.equal(tree, null, 'the line goes as soon as the wait ends');
  } finally {await host.unmount(); t.mock.timers.reset();}
});

test('with reduced motion the ring stands still', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const repeats: unknown[] = [], {PlaybackWaiting} = await waitingModule(true, repeats);
  let tree: any;
  const host = createFabricHookHost(() => {tree = PlaybackWaiting({waiting: true});});
  try {
    await act(() => host.mount()); await act(() => {t.mock.timers.tick(3000);}); await act(() => host.rerender());
    assert.ok(tree); assert.equal(repeats.length, 0, 'no turn is started');
  } finally {await host.unmount(); t.mock.timers.reset();}
});
