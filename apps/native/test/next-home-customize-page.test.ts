import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

function nodes(value: any): any[] {
  if (Array.isArray(value)) return value.flatMap(nodes);
  if (!value?.props) return [];
  return [value, ...nodes(value.props.children), ...nodes(value.props.footer), ...nodes(value.props.trailing)];
}

async function page(view: Record<string, unknown>) {
  const calls: string[] = [];
  const {HomeCustomizePage} = await screenModule(new URL('../src-next/pages/touch/home/HomeCustomizePage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_i18n: unknown, _text: unknown, fallback: string) => fallback},
    '../../../kit/controls': {Button: 'Button', Switch: 'Switch'}, '../../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet', Dialog: 'Dialog'},
    '../../../kit/overlays/surface': {OverlayFootRow: 'OverlayFootRow'}, '../../../kit/states': {StateView: 'StateView'},
    '../../../kit/home/HomeLayout': {HomeAlwaysOn: 'HomeAlwaysOn', HomeDragHandle: 'HomeDragHandle', HomeLayoutCard: 'HomeLayoutCard', HomeLayoutRow: 'HomeLayoutRow',
      HomeLayoutScroll: 'HomeLayoutScroll', HomeSavedViewChoices: 'HomeSavedViewChoices', HomeViewMark: 'HomeViewMark'},
    '../../shared/home/useHomeCustomize': {useHomeCustomize: () => ({...view, save: () => calls.push('save'), startReset: () => calls.push('reset')})},
  });
  const copy = {title: 'Customize Home', save: 'Save', reset: 'Reset', alwaysOn: 'Always on Home', addView: 'Add a saved view', conflictTitle: 'c', conflictBody: 'c', reload: 'r', keepEditing: 'k'};
  const props = {controller: {toggle: () => calls.push('toggle')}, copy, i18n: {t: (key: string) => key}, onClose() {}, onAddView() {}, testID: 'home-customize',
    summary: () => undefined};
  return {tree: HomeCustomizePage(props), calls};
}

const rows = [{id: 'continue', title: 'Continue Watching', required: true, hideable: false, reorderable: true, hidden: false},
  {id: 'trending', title: 'Trending now', required: false, hideable: true, reorderable: true, hidden: false}];
const ready = {state: {rows, phase: 'ready', dirty: true, pending: false, conflict: false, showConflict: false}, choosing: false, blocked: false, displayed: rows,
  heights: new Map(), positions: new Map(), targetY: undefined, dragGeometry: () => [], scrollGesture: {}, rowRefs: {current: new Map()}, picked: {}, first: {},
  firstView: {}, addView: {}, resetButton: {}, cancelReset: {}, setChoosing() {}, returnToAdd() {}, measure() {}, removeRow() {}, back() {}};

test('Customize Home on a phone is the tall sheet: one card of rows, each its switch or "Always on Home", then Reset to default and Save', async () => {
  const {tree, calls} = await page(ready);
  const sheet = nodes(tree).find(node => node.type === 'BottomSheet');
  assert.equal(sheet.props.title, 'Customize Home'); assert.equal(sheet.props.defaultDetent, 1);
  const all = nodes(tree), card = all.find(node => node.type === 'HomeLayoutCard');
  const [first, second] = nodes(card).filter(node => node.type === 'HomeLayoutRow');
  assert.equal(nodes(first.props.trailing).some(node => node.type === 'HomeAlwaysOn'), true);
  nodes(second.props.trailing).find(node => node.type === 'Switch').props.onCheckedChange(); assert.deepEqual(calls, ['toggle']);
  const foot = nodes(sheet.props.footer).filter(node => node.type === 'Button');
  assert.deepEqual(foot.map(node => node.props.variant), ['text', undefined]);
  foot[0].props.onPress(); foot[1].props.onPress(); assert.deepEqual(calls, ['toggle', 'reset', 'save']);
});

test('while a row is lifted the list holds still and the foot steps away', async () => {
  const {tree} = await page({...ready, state: {...ready.state, pickedId: 'trending'}});
  const sheet = nodes(tree).find(node => node.type === 'BottomSheet');
  assert.equal(sheet.props.footer, undefined);
  assert.equal(nodes(sheet).find(node => node.type === 'HomeLayoutScroll').props.locked, true);
});

test('TV: while a row is lifted every grip is off, its own too, so focus stays on the lifted row (Up and Down move only a row that holds focus)', async () => {
  const tvParts = ['TVHomeAlwaysOn', 'TVHomeLayoutConflict', 'TVHomeLayoutFoot', 'TVHomeLayoutHandle', 'TVHomeLayoutList', 'TVHomeLayoutNotice', 'TVHomeLayoutPanel',
    'TVHomeLayoutRemove', 'TVHomeLayoutRow', 'TVHomeMoveHints', 'TVHomeResetButton', 'TVHomeRowSwitch', 'TVHomeViewMark'];
  const lifted = {...ready, state: {...ready.state, pickedId: 'trending'}, pickRow() {}, rowHeight: 58};
  const {HomeCustomizePage} = await screenModule(new URL('../src-next/pages/tv/home/HomeCustomizePage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_i18n: unknown, _text: unknown, fallback: string) => fallback},
    '../../../kit/controls': {Button: 'Button', Switch: 'Switch'}, '../../../kit/overlays': {Dialog: 'Dialog'}, '../../../kit/overlays/surface': {OverlayFootRow: 'OverlayFootRow'},
    '../../../kit/home/HomeLayout': {HomeSavedViewChoices: 'HomeSavedViewChoices'}, '../../../kit/home/HomeTVLayout': Object.fromEntries(tvParts.map(name => [name, name])),
    '../../shared/home/useHomeCustomize': {useHomeCustomize: () => lifted},
  });
  const tree = HomeCustomizePage({controller: {}, copy: {title: 'Customize Home', save: 'Save', alwaysOn: 'Always on Home', addView: 'Add', move: 'Move', drop: 'Drop', putBack: 'Put back'},
    i18n: {t: (key: string) => key}, onClose() {}, onAddView() {}, testID: 'home-customize', summary: () => undefined});
  const rowsOf = nodes(tree).filter(node => node.type === 'TVHomeLayoutRow');
  const grips = rowsOf.map(row => [row.props.lifted, nodes(row.props.handle).find(node => node.type === 'TVHomeLayoutHandle')?.props.disabled]);
  assert.deepEqual(grips, [[false, true], [true, true]], 'both grips are off while "trending" is lifted');
  assert.equal(rowsOf.find(row => row.props.lifted).props.disabled, false, 'the lifted row itself stays reachable');
});
