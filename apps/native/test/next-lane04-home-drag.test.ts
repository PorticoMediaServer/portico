import * as tvKeyAdapter from '../src-next/platform/tv-key-adapter.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import type {HomeLayoutView} from '../../../packages/client-core/src/home';
import {NativeHomeCustomizeController} from '../src-next/screens/home/customize-controller';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createHomeRowDrag} from '../src-next/screens/home/customize-drag';

async function setup(locked?: string) {
  const layout: HomeLayoutView = {revision: 3, rowOrder: ['a', 'b', 'c', 'd'], hiddenRowIds: [], rows:
    ['a', 'b', 'c', 'd'].map(id => ({id, kind: 'recent', title: id, artworkShape: 'poster', required: false, hideable: true, reorderable: id !== locked, hidden: false}))};
  let writes = 0, current = true;
  const controller = new NativeHomeCustomizeController({current: () => current, requestId: async () => 'drag-write', home: {
    loadLayout: async () => layout, saveLayout: async () => {writes++; return layout;}, resetLayout: async () => {writes++;},
  }});
  await controller.load();
  const rows = [{id: 'a', top: 8, height: 58}, {id: 'b', top: 66, height: 90}, {id: 'c', top: 156, height: 58}, {id: 'd', top: 214, height: 58}];
  return {controller, drag: (id: string) => createHomeRowDrag(controller, id, () => rows), writes: () => writes, retire: () => {current = false;}};
}

test('grip drag crosses measured rows in both directions and releases a savable draft', async () => {
  const {controller, drag, writes} = await setup(), gesture = drag('a');
  assert.equal(gesture.begin(), true);
  gesture.update(20);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['a', 'b', 'c', 'd']);
  gesture.update(240);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['b', 'c', 'd', 'a']);
  gesture.update(80);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['b', 'a', 'c', 'd']);
  gesture.finish(true);
  assert.equal(controller.getSnapshot().pickedId, undefined);
  assert.equal(controller.getSnapshot().dirty, true);
  assert.equal(writes(), 0, 'the gesture never persists without Save');
  assert.equal(await controller.save(), true);
  assert.equal(writes(), 1);
});

test('cancelled drags restore the pickup order, and a locked row cannot be crossed', async () => {
  const {controller, drag} = await setup('c'), gesture = drag('a');
  assert.equal(drag('c').begin(), false);
  assert.equal(gesture.begin(), true);
  gesture.update(10000);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['b', 'a', 'c', 'd']);
  gesture.finish(false);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['a', 'b', 'c', 'd']);
  assert.equal(controller.getSnapshot().dirty, false);
  assert.equal(controller.getSnapshot().pickedId, undefined);
});

test('stale gesture callbacks cannot adopt a newer pickup or a reloaded layout', async () => {
  const {controller, drag, retire} = await setup(), old = drag('b');
  old.begin();
  controller.cancelPickup();
  controller.pick('b');
  old.update(1000); old.finish(true);
  assert.equal(controller.getSnapshot().pickedId, 'b', 'the new pickup still belongs to its opener');
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['a', 'b', 'c', 'd']);
  controller.drop();
  const reloaded = drag('a'); reloaded.begin();
  await controller.load();
  reloaded.update(1000); reloaded.finish(true);
  assert.equal(controller.getSnapshot().dirty, false);
  retire();
  assert.equal(drag('a').begin(), false);
});

test('nonfinite deltas and a second active grip do not change the draft', async () => {
  const {controller, drag} = await setup(), gesture = drag('a');
  gesture.begin();
  assert.equal(drag('b').begin(), false);
  for (const value of [NaN, Infinity, -Infinity]) gesture.update(value);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['a', 'b', 'c', 'd']);
  gesture.finish(true);
  assert.equal(controller.getSnapshot().dirty, false);
});


test('touch grip waits for a short press and blocks the scroll gesture before pickup', async () => {
  const {controller} = await setup(), ui = hooks(), scrollGesture = {};
  const calls: any[] = [], handlers: Record<string, (...args: any[]) => void> = {};
  const pan: any = {};
  for (const name of ['enabled', 'activateAfterLongPress', 'blocksExternalGesture', 'activeOffsetY', 'runOnJS']) {
    pan[name] = (value: unknown) => {calls.push([name, value]); return pan;};
  }
  for (const name of ['onStart', 'onUpdate', 'onFinalize']) pan[name] = (fn: (...args: any[]) => void) => {handlers[name] = fn; return pan;};
  const {HomeDragHandle} = await screenModule(new URL('../src-next/screens/home/HomeCustomize.tsx', import.meta.url), {'../../platform/tv-key-source': {tvKeySource: 'native'}, '../../platform/tv-key-adapter': tvKeyAdapter, 
    react: ui.react, 'react-native': {View: 'View'}, 'react-native-gesture-handler': {Gesture: {Pan: () => pan}, GestureDetector: 'GestureDetector'},
    '../../../../../packages/i18n/src': {}, '../../../../../packages/client-core/src/presentation/server-text': {},
    '../../kit/controls': {}, '../../kit/focus': {}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/browse/presentation': {},
    '../../kit/overlays': {}, '../../kit/overlays/OverlayHost': {}, '../../kit/theme': {useTheme: () => ({colors: {}})},
    './customize-controller': {}, './customize-drag': {createHomeRowDrag},
  });
  const render = () => ui.render(() => HomeDragHandle({controller, id: 'a', disabled: false, scrollGesture, testID: 'grip',
    geometry: () => [{id: 'a', top: 0, height: 58}, {id: 'b', top: 58, height: 58}, {id: 'c', top: 116, height: 58}, {id: 'd', top: 174, height: 58}]}));
  render();
  assert.equal(calls.find(([name]) => name === 'activateAfterLongPress')[1], 200);
  assert.equal(calls.find(([name]) => name === 'blocksExternalGesture')[1], scrollGesture);
  assert.equal(controller.getSnapshot().pickedId, undefined, 'touch begin alone does not pick');
  handlers.onStart(); handlers.onUpdate({translationY: 80}); handlers.onFinalize({}, true);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['b', 'a', 'c', 'd']);
  assert.equal(controller.getSnapshot().pickedId, undefined);
});
