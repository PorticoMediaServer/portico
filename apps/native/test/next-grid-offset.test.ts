import {nativeSlotOwnerKey, projectNativeSlots} from '../src-next/kit/focus/windowed-slots.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {createTheme} from '../src-next/kit/theme.ts';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import {itemSelection} from '../src-next/kit/layout/types.ts';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// Exercises the unchanged component's native-event and ref boundary with a
// controlled native anchor. This is source evidence, not native scroll proof.
test('the grid reports actual offsets within one row and restores that exact measured offset through its anchor', async () => {
  const host = hooks(), offset = {value: 0}, moved: {offset: number; immediate: boolean}[] = [], measured: number[] = [], geometries: number[][] = [];
  const anchor = {offset, scrollRef: {current: null}, move: (value: number, immediate: boolean) => moved.push({offset: value, immediate}), anchor() {}, stop() {}};
  const react = {...host.react, useLayoutEffect: (run: () => void) => run(), forwardRef: (render: unknown) => render, useImperativeHandle: (ref: {current: unknown}, create: () => unknown) => {ref.current = create();}};
  const collection = createWindowedCollection({fetchPage: async () => ({items: ['actual-item'], total: 1}), pageSize: 40, maxResidentPages: 4, keyOf: (value: string) => value});
  collection.ensureRange(0, 0); await new Promise<void>(resolve => setImmediate(resolve));
  const {WindowedGrid} = await screenModule(new URL('../src-next/kit/layout/WindowedGrid.tsx', import.meta.url), {
    react: {...react, default: react}, 'react-native': {View: 'View'},
    'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (run: unknown) => run, useAnimatedScrollHandler: (handler: unknown) => handler, useSharedValue: (value: unknown) => ({value})},
    '../theme': {useTheme: () => createTheme({isTV: true, width: 1920})}, './FocusAnchor': {useFocusAnchor: () => anchor},
    './model': layoutModel, './types': {itemSelection}, '../focus/windowed-slots': {nativeSlotOwnerKey, projectNativeSlots},
  });
  const ref: {current: {scrollToOffset: (value: number) => void} | null} = {current: null};
  const selections: unknown[] = [];
  try {
    const tree = host.render(() => WindowedGrid({collection, getKey: String, renderItem: (_item: string, context: {selection?: unknown}) => {selections.push(context.selection); return null;}, renderPlaceholder: () => null,
      itemWidth: 200, itemHeight: 360, onScrollOffset: (value: number) => measured.push(value), onGeometry: (columns: number, stride: number) => geometries.push([columns, stride])}, ref));
    assert.deepEqual([...geometries], [], 'unmeasured geometry is never published');
    assert.deepEqual(selections, [undefined], 'a real item renders without a fabricated selection port');
    tree.props.onLayout({nativeEvent: {layout: {width: 1000, height: 700}}});
    host.render(() => WindowedGrid({collection, getKey: String, renderItem: () => null, renderPlaceholder: () => null, itemWidth: 200, itemHeight: 360,
      onGeometry: (columns: number, stride: number) => geometries.push([columns, stride])}, ref));
    assert.deepEqual(geometries, [[3, 396]], 'actual renderer columns/stride are reported without touching the anchor');
    tree.props.onScroll({contentOffset: {x: 0, y: 73.25}});
    tree.props.onScroll({contentOffset: {x: 0, y: 77.75}});
    assert.deepEqual(measured, [73.25, 77.75], 'the final position is not approximated by a row index or height');
    assert.equal(offset.value, 77.75);
    ref.current!.scrollToOffset(measured.at(-1)!);
    assert.deepEqual(moved, [{offset: 77.75, immediate: true}]);
    ref.current!.scrollToOffset(Number.NaN);
    assert.equal(moved.length, 1, 'an invalid offset never reaches the native anchor');
  } finally {collection.dispose();}
});
