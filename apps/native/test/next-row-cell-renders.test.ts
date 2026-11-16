import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import * as rowState from '../src-next/kit/layout/row-state';
import * as layoutTypes from '../src-next/kit/layout/types';
import {screenModule} from './helpers/screen-harness.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';

// A focus move along a row draws again only the cards whose focus changed: the row's own state
// (the focused card, its scroll) no longer hands every card new props. Measured on Home with a held
// arrow, every visible card was drawn again on each move (the TV-web speed pass). The real Row and
// focus kit run through the fake native focus engine on both platforms; focus lands as before.

async function scene(platform: 'android' | 'tvos') {
  const kit = await tvFocusKit({platform}), {engine, e, focus, Focusable, React, TVRow} = kit, native = engine.native;
  const theme = createTheme({isTV: true, width: 1920});
  const none = () => null;
  const row = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': native,
    'react-native-svg': {default: none, Defs: none, LinearGradient: none, Rect: none, Stop: none},
    'react-native-reanimated': {...engine.animated, default: {View: native.View, ScrollView: native.ScrollView, createAnimatedComponent: (c: unknown) => c},
      runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: () => undefined},
    '../../../../../packages/i18n/src': {defaultI18n}, '../Icon': {Icon: none}, '../theme': {useTheme: () => theme}, '../focus': focus,
    './row-state': rowState, '../focus/page-boundary': {useRowPageBoundary() {}},
    './FocusAnchor': {useFocusAnchor: () => ({anchor() {}, repeating: false, offset: {value: 0}, scrollRef: {current: null}, stop() {}, move() {}})},
    './model': layoutModel, './MotionItem': {MotionItem: ({style, children}: any) => e(native.View, {style}, children)}, './types': layoutTypes, './TVRow': {TVRow}});
  const registry = focus.createFocusRegistry();
  const draws = new Map<string, number>(), focused: string[] = [];
  const keys = Array.from({length: 8}, (_, index) => `card-${index}`);
  const unselected = {active: false, isSelected: () => false, onToggle() {}, onEnter() {}};
  const renderItem = (key: string, slot: any) => {
    draws.set(key, (draws.get(key) ?? 0) + 1);
    return e(Focusable, {testID: key, focusId: slot.focusId, onFocus: slot.onFocus, onBlur: slot.onBlur, style: {width: 160, height: 240}}, () => null);
  };
  const Scene = () => e(focus.FocusProvider, {registry}, e(row.Row, {title: '', showHeading: false, items: keys, getKey: (key: string) => key,
    itemWidth: 160, itemHeight: 240, gap: 20, gutter: 0, rightGutter: 0, selection: unselected, initialViewportWidth: 1920,
    focus: {id: 'home.row'}, onItemFocus: (key: string) => focused.push(key), renderItem}));
  return {engine, e, Scene, draws, focused};
}

for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: moving along a row draws only the card that lost focus and the card that took it`, async () => {
    const s = await scene(platform);
    try {
      await s.engine.render(s.e(s.Scene, {}));
      await s.engine.focus('card-0');
      await s.engine.settle();
      s.draws.clear();
      await s.engine.press('right'); await s.engine.settle();
      await s.engine.press('right'); await s.engine.settle();
      assert.equal(s.engine.focused, 'card-2', 'focus moves along the row as before');
      assert.deepEqual(s.focused.slice(-3), ['card-0', 'card-1', 'card-2'], 'each card reports its own focus, in order');
      const drawn = [...s.draws.keys()].sort();
      assert.ok(drawn.every(key => ['card-0', 'card-1', 'card-2'].includes(key)), `cards drawn again: ${drawn.join(', ')}`);
    } finally {await s.engine.unmount();}
  });
}
