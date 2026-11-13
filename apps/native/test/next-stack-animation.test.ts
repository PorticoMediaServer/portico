import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// Walk, 1 Nov: on Android 16 a pushed page under react-native-screens' `default` animation
// was laid out but never drawn (every See all and title page blank). Android pushes slide.
test('a full-motion push slides on Android, stays the system default on iOS, and cheaper tiers cut', async () => {
  const module = await screenModule(new URL('../src-next/shell/TouchStacks.tsx', import.meta.url), {
    react: await import('react'), 'react-native': {Platform: {OS: 'android'}, StyleSheet: {absoluteFill: {}}, View: 'View'},
    'react-native-reanimated': {default: {View: 'Animated.View'}, Easing: {linear: () => 0}, ReduceMotion: {Never: 'never'}},
    'react-native-screens': {ScreenStack: 'ScreenStack', ScreenStackItem: 'ScreenStackItem'},
    '../kit/navigation/stack-slot': {NativeStackSlotOutlet: 'Outlet'}, './routes': {isModalRoot: () => false}, '../kit/artwork': {StandardBackdrop: 'StandardBackdrop'},
  });
  assert.equal(module.nativeStackAnimation('full'), 'slide_from_right');
  assert.equal(module.nativeStackAnimation('full', 'android'), 'slide_from_right');
  assert.equal(module.nativeStackAnimation('full', 'ios'), 'default');
  for (const tier of ['reduced', 'lite']) for (const os of ['android', 'ios']) assert.equal(module.nativeStackAnimation(tier, os), 'none');
});

// DESIGN.md "One standard backdrop": the stack draws it once behind every page (12 Nov phone survey: 48 screens each
// mounted their own, cut to their own box). Settings' modal root stays see-through over the live page.
test('every stack page sits on the one standard backdrop, and the Settings modal root stays see-through', async () => {
  const h = hooks();
  const react = {...h.react, useLayoutEffect: () => {}, Fragment: 'Fragment'};
  const module = await screenModule(new URL('../src-next/shell/TouchStacks.tsx', import.meta.url), {
    react: {...react, default: react}, 'react-native': {Platform: {OS: 'ios'}, StyleSheet: {absoluteFill: {}}, View: 'View'},
    'react-native-reanimated': {default: {View: 'Animated.View'}, Easing: {linear: () => 0}, ReduceMotion: {Never: 'never'},
      useSharedValue: (value: number) => ({value}), useAnimatedStyle: () => ({}), withTiming: (value: number) => value},
    'react-native-screens': {ScreenStack: 'ScreenStack', ScreenStackItem: 'ScreenStackItem'},
    '../kit/navigation/stack-slot': {NativeStackSlotOutlet: 'Outlet'}, './routes': {isModalRoot: (route: {modal?: boolean}) => route.modal === true},
    '../kit/artwork': {StandardBackdrop: 'StandardBackdrop'},
  });
  const presentations = [{route: {kind: 'shell'}, occurrence: 'home'}, {route: {kind: 'title'}, occurrence: 'title'}, {route: {kind: 'shell', modal: true}, occurrence: 'settings'}];
  const port = {subscribe: () => () => {}, getSnapshot: () => ({active: 'home', stacks: [{tab: 'home', presentations}]}), renderRoute: () => 'Route', commitNativePop: () => true, selectTab() {}, reselectTab() {}};
  const expand = (node: any): any => Array.isArray(node) ? node.map(expand) : node && typeof node === 'object' && 'props' in node
    ? typeof node.type === 'function' ? expand(node.type(node.props)) : {...node, props: {...node.props, children: expand(node.props.children)}} : node;
  const tree = h.render(() => expand(module.TouchStacks({port, slots: {}, tier: 'full', background: '#071018', renderShell: () => 'Shell'})));
  const items: any[] = [], walk = (node: any) => {if (Array.isArray(node)) node.forEach(walk); else if (node && typeof node === 'object') {if (node.type === 'ScreenStackItem') items.push(node); walk(node.props?.children);}};
  walk(tree);
  const drawsBackdrop = (item: any) => JSON.stringify(item.props.children).includes('"StandardBackdrop"');
  assert.deepEqual(items.map(item => [item.props.screenId, drawsBackdrop(item)]), [['home:base:root', true], ['title', true], ['home:modal:root', false]]);
});
