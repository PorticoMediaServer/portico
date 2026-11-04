import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import * as rowState from '../src-next/kit/layout/row-state';
import * as layoutTypes from '../src-next/kit/layout/types';
import {screenModule} from './helpers/screen-harness.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';

// #697 review: on TV a re-read of the person (the server refused a credit cursor: access was decided again) replaces the
// page, and the credit window is built again holding only the new first page. The shelf's slots are keyed by index, so the
// focused credit's card turns into a placeholder: none of the new window has arrived on the first render, and a credit
// beyond the new first page never does. The shelf names its own in-row target (credit-refocus): the credit that had focus
// when the new first page holds it, else that page's last credit. Row treats an index-keyed slot that stays but went inert
// as lost. The real Row, focus kit and useCreditRefocus run through the fake native focus engine on both platforms.
// Pages here hold three credits (the person read holds 40); the arithmetic is the same.
// Device check this stands in for: Apple TV, a person's credits, focus on a credit past the first page (see #697's body).

const tick = () => new Promise(resolve => setTimeout(resolve, 0));
const credit = (index: number, fence: string) => ({media: {id: `${fence}-${index}`, kind: 'movie', title: `Credit ${index}`}, creditKind: 'cast'});
const personPage = (fence: string, count = 3) => ({viewerFence: fence, credits: Array.from({length: count}, (_, index) => credit(index, fence)), pageInfo: {total: 9}}) as any;
const slotKey = (index: number) => String(index);

async function scene(platform: 'android' | 'tvos') {
  const kit = await tvFocusKit({platform}), {engine, e, focus, Focusable, Stop, React, TVRow} = kit, native = engine.native;
  const theme = createTheme({isTV: true, width: 1920});
  const none = () => null;
  const react = {...React, default: React};
  const row = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {
    react, 'react-native': native,
    'react-native-svg': {default: none, Defs: none, LinearGradient: none, Rect: none, Stop: none},
    'react-native-reanimated': {...engine.animated, default: {View: native.View, ScrollView: native.ScrollView, createAnimatedComponent: (c: unknown) => c},
      runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: () => undefined},
    '../../../../../packages/i18n/src': {defaultI18n}, '../Icon': {Icon: none}, '../theme': {useTheme: () => theme}, '../focus': focus,
    './row-state': rowState, '../focus/page-boundary': {useRowPageBoundary() {}},
    './FocusAnchor': {useFocusAnchor: () => ({anchor() {}, repeating: false, offset: {value: 0}, scrollRef: {current: null}, stop() {}, move() {}})},
    './model': layoutModel, './MotionItem': {MotionItem: ({style, children}: any) => e(native.View, {style}, children)}, './types': layoutTypes, './TVRow': {TVRow}});
  const {useCreditRefocus} = await screenModule(new URL('../src-next/screens/people/credit-refocus.ts', import.meta.url), {react});
  const registry = focus.createFocusRegistry();
  // The row's own requests (registry.focus); the registry's internal recovery does not go through this property.
  const requests: {regionId: string; childId: string}[] = [], focusOf = registry.focus;
  registry.focus = ((address: {regionId: string; childId: string}, options?: unknown) => {requests.push({...address}); return focusOf(address, options as never);}) as typeof registry.focus;
  const unselected = {active: false, isSelected: () => false, onToggle() {}, onEnter() {}};
  const slots = Array.from({length: 9}, (_, index) => index);
  // TVPerson's mapping (PersonScreen.tsx): index-keyed slots, a credit is focusable once its page has arrived, the shelf
  // names useCreditRefocus's target. `loaded` is how many credits the shown window holds; `credits` are their ids.
  const Credits = ({page, loaded, targeted}: {page: any; loaded: number; targeted: boolean}) => {
    const refocus = useCreditRefocus(page, slotKey);
    return e(row.Row, {title: '', showHeading: false, items: slots, getKey: slotKey, itemWidth: 160, itemHeight: 240, gap: 20, gutter: 0, rightGutter: 0, selection: unselected,
      initialViewportWidth: 1920, focus: {id: 'person.credits'}, isFocusable: (index: number) => index < loaded, ...(targeted ? {refocus: refocus.refocus} : {}),
      renderItem: (index: number, slot: any) => index < loaded
        ? e(Focusable, {testID: `credit-${index}`, focusId: slot.focusId, onFocus: () => {refocus.onFocus(index); slot.onFocus();}, onBlur: slot.onBlur, style: {width: 160, height: 240}}, () => null)
        : e(native.View, {testID: `placeholder-${index}`, style: {width: 160, height: 240}})});
  };
  const Scene = ({page, loaded, targeted = true, covered = false}: {page: any; loaded: number; targeted?: boolean; covered?: boolean}) => e(focus.FocusProvider, {registry},
    e(native.View, {style: {flex: 1}},
      e(focus.FocusRegion, {id: 'person.header', orientation: 'horizontal', style: {height: 120}}, e(Stop, {id: 'filters', w: 200, h: 80})),
      e(focus.CoveredLayer, {covered}, e(Credits, {page, loaded, targeted}))));
  return {engine, e, Scene, requests};
}

/** Credits 0–5 shown (two pages), focus on `start`; a re-read brings a new first page, arriving one render later. */
async function replaced(platform: 'android' | 'tvos', start: string, targeted = true) {
  const s = await scene(platform), before = personPage('old'), after = personPage('moved');
  try {
    await s.engine.render(s.e(s.Scene, {page: before, loaded: 6, targeted}));
    await s.engine.focus(`credit-${start}`);
    // The new window: nothing has arrived on its first render; its first page arrives on the next.
    await s.engine.render(s.e(s.Scene, {page: after, loaded: 0, targeted}));
    await s.engine.settle(); await tick(); await s.engine.settle();
    await s.engine.render(s.e(s.Scene, {page: after, loaded: 3, targeted}));
    await s.engine.settle(); await tick(); await s.engine.settle();
    return {focused: s.engine.focused, requests: s.requests};
  } finally {await s.engine.unmount();}
}

for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: a person's credits read again keep focus in the row, on the new first page's last credit when the focused one is beyond it`, async () => {
    const result = await replaced(platform, '4');
    assert.deepEqual(result.requests, [{regionId: 'person.credits', childId: '2'}]);
    assert.equal(result.focused, 'credit-2');
  });

  test(`${platform}: a person's credits read again keep focus on the same credit when the new first page holds it`, async () => {
    const result = await replaced(platform, '1');
    assert.deepEqual(result.requests, [{regionId: 'person.credits', childId: '1'}]);
    assert.equal(result.focused, 'credit-1');
  });

  test(`${platform}: without the shelf's target, focus leaves the credits when the focused card goes`, async () => {
    const result = await replaced(platform, '4', false);
    assert.deepEqual(result.requests, []);
    assert.ok(!String(result.focused).startsWith('credit-'), `focus is at ${result.focused}, out of the row`);
  });
}

// The cases in which the shelf asks for nothing: only a shelf that held focus, is on the top layer and is still mounted
// when its queued request runs moves focus.
async function noFire(platform: 'android' | 'tvos', mode: 'not-held' | 'covered' | 'unmounted' | 'control') {
  const s = await scene(platform), before = personPage('old'), after = personPage('moved');
  const held: (() => void)[] = [], queue = globalThis.queueMicrotask;
  let mounted = true;
  try {
    await s.engine.render(s.e(s.Scene, {page: before, loaded: 6}));
    await s.engine.focus(mode === 'not-held' ? 'filters' : 'credit-4');
    await s.engine.render(s.e(s.Scene, {page: after, loaded: 0, covered: mode === 'covered'}));
    await s.engine.settle(); await tick();
    const hold = mode === 'unmounted' || mode === 'control';
    if (hold) globalThis.queueMicrotask = callback => {held.push(callback);};
    try {await s.engine.render(s.e(s.Scene, {page: after, loaded: 3, covered: mode === 'covered'}));}
    finally {globalThis.queueMicrotask = queue;}
    if (mode === 'unmounted') {await s.engine.unmount(); mounted = false;}
    for (const callback of held) callback();
    if (mounted) {await s.engine.settle(); await tick();}
    return {requests: s.requests, focused: s.engine.focused, held: held.length};
  } finally {if (mounted) await s.engine.unmount();}
}

for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: person credits that did not hold focus ask for none when they are read again`, async () => {
    const result = await noFire(platform, 'not-held');
    assert.deepEqual(result.requests, []); assert.equal(result.focused, 'filters');
  });
  test(`${platform}: covered person credits ask for no focus when they are read again`, async () => {
    const result = await noFire(platform, 'covered');
    assert.deepEqual(result.requests, [], 'only the top layer takes focus');
  });
  test(`${platform}: person credits unmounted before their queued request runs ask for nothing; mounted, the same request goes out`, async () => {
    const control = await noFire(platform, 'control');
    assert.deepEqual(control.requests, [{regionId: 'person.credits', childId: '2'}], 'the request is queued as a microtask');
    const result = await noFire(platform, 'unmounted');
    assert.ok(result.held > 0); assert.deepEqual(result.requests, []);
  });
}
