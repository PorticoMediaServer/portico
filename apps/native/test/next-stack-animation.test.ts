import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Walk, 1 Nov: on Android 16 a pushed page under react-native-screens' `default` animation
// was laid out but never drawn (every See all and title page blank). Android pushes slide.
test('a full-motion push slides on Android, stays the system default on iOS, and cheaper tiers cut', async () => {
  const module = await screenModule(new URL('../src-next/shell/TouchStacks.tsx', import.meta.url), {
    react: await import('react'), 'react-native': {Platform: {OS: 'android'}, StyleSheet: {absoluteFill: {}}, View: 'View'},
    'react-native-reanimated': {default: {View: 'Animated.View'}, Easing: {linear: () => 0}, ReduceMotion: {Never: 'never'}},
    'react-native-screens': {ScreenStack: 'ScreenStack', ScreenStackItem: 'ScreenStackItem'},
    '../kit/navigation/stack-slot': {NativeStackSlotOutlet: 'Outlet'}, './routes': {isModalRoot: () => false},
  });
  assert.equal(module.nativeStackAnimation('full'), 'slide_from_right');
  assert.equal(module.nativeStackAnimation('full', 'android'), 'slide_from_right');
  assert.equal(module.nativeStackAnimation('full', 'ios'), 'default');
  for (const tier of ['reduced', 'lite']) for (const os of ['android', 'ios']) assert.equal(module.nativeStackAnimation(tier, os), 'none');
});
