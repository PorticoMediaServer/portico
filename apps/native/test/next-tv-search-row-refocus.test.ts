import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import * as rowState from '../src-next/kit/layout/row-state';
import * as layoutTypes from '../src-next/kit/layout/types';
import {screenModule} from './helpers/screen-harness.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';

// Step 7, search rows: a TV search row whose cursor the server refused (the viewer's access was decided again) is read
// again from its first page, which replaces the row (NativeSearchDestination names `paging.replaced`: the last entry
// that survived, or the first). When focus was on an entry the replacement removed, it stays in the row, on that target.
// TVSearchSection draws a row with Row (its `refocus`) and the songs group as a grid region (useReplacementRefocus).
// The real Row, FocusRegion grid, focus kit and hook run through the fake native focus engine on both platforms.
// Device check this stands in for: Apple TV, Search, All results (see the PR body).

const tick = () => new Promise(resolve => setTimeout(resolve, 0));

async function scene(platform: 'android' | 'tvos') {
  const kit = await tvFocusKit({platform}), {engine, e, focus, Focusable, Stop, React, TVRow} = kit, native = engine.native;
  const theme = createTheme({isTV: true, width: 1920});
  const none = () => null, react = {...React, default: React};
  const row = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {
    react, 'react-native': native,
    'react-native-svg': {default: none, Defs: none, LinearGradient: none, Rect: none, Stop: none},
    'react-native-reanimated': {...engine.animated, default: {View: native.View, ScrollView: native.ScrollView, createAnimatedComponent: (c: unknown) => c},
      runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: () => undefined},
    '../../../../../packages/i18n/src': {defaultI18n}, '../Icon': {Icon: none}, '../theme': {useTheme: () => theme}, '../focus': focus,
    './row-state': rowState, '../focus/page-boundary': {useRowPageBoundary() {}},
    './FocusAnchor': {useFocusAnchor: () => ({anchor() {}, repeating: false, offset: {value: 0}, scrollRef: {current: null}, stop() {}, move() {}})},
    './model': layoutModel, './MotionItem': {MotionItem: ({style, children}: any) => e(native.View, {style}, children)}, './types': layoutTypes, './TVRow': {TVRow}});
  const {replacementRefocusHook} = await import('../src-next/kit/focus/replacement-refocus-hook.ts'), useReplacementRefocus = replacementRefocusHook(react as never);
  const registry = focus.createFocusRegistry();
  const requests: {regionId: string; childId: string}[] = [], focusOf = registry.focus;
  registry.focus = ((address: {regionId: string; childId: string}, options?: unknown) => {requests.push({...address}); return focusOf(address, options as never);}) as typeof registry.focus;
  const unselected = {active: false, isSelected: () => false, onToggle() {}, onEnter() {}};
  // TVSearchSection's two drawings of one group, with what the destination names as `paging.replaced`.
  const SearchRow = ({ids, refocus}: {ids: readonly string[]; refocus?: {focusId: string; serial: number}}) => e(row.Row, {title: '', showHeading: false, items: ids, getKey: (id: string) => id,
    itemWidth: 196, itemHeight: 290, gap: 28, selection: unselected, initialViewportWidth: 1920, focus: {id: 'search.movies', edges: {up: 'search.keyboard'}}, refocus,
    renderItem: (id: string, slot: any) => e(Focusable, {testID: id, focusId: slot.focusId, onFocus: slot.onFocus, onBlur: slot.onBlur, style: {width: 196, height: 290}}, () => null)});
  const SongsGrid = ({ids, refocus, targeted, regionId = 'search.songs'}: {ids: readonly string[]; refocus?: {focusId: string; serial: number}; targeted: boolean; regionId?: string}) => {
    useReplacementRefocus(registry, regionId, targeted ? refocus : undefined, focus.useCovered(), (id: string) => !ids.includes(id));
    return e(focus.FocusRegion, {id: regionId, orientation: 'free', grid: {columns: 1, count: ids.length}, entry: ids[0], edges: {up: 'search.keyboard'}, style: {width: 900}},
      ids.map((id, index) => e(focus.FocusItem, {key: id, focusId: id, cell: {index}}, e(Stop, {id, w: 900, h: 84}))));
  };
  const Scene = ({kind, ids, refocus, targeted = true, covered = false, regionId}: {kind: 'row' | 'grid'; ids: readonly string[]; refocus?: {focusId: string; serial: number}; targeted?: boolean; covered?: boolean; regionId?: string}) =>
    e(focus.FocusProvider, {registry}, e(native.View, {style: {flex: 1}},
      e(focus.FocusRegion, {id: 'search.keyboard', orientation: 'horizontal', style: {height: 120}}, e(Stop, {id: 'keyboard', w: 240, h: 80})),
      e(focus.CoveredLayer, {covered}, kind === 'row' ? e(SearchRow, {ids, refocus: targeted ? refocus : undefined}) : e(SongsGrid, {ids, refocus, targeted, regionId}))));
  return {engine, e, Scene, requests};
}

/** e1–e3 shown, focus on `start`; the row's first page read again brings `after`, naming `target`. */
async function replaced(platform: 'android' | 'tvos', kind: 'row' | 'grid', start: string, after: readonly string[], target: string,
  options: {targeted?: boolean; covered?: boolean} = {}) {
  const s = await scene(platform);
  try {
    await s.engine.render(s.e(s.Scene, {kind, ids: ['e1', 'e2', 'e3']}));
    await s.engine.focus(start);
    await s.engine.render(s.e(s.Scene, {kind, ids: after, refocus: {focusId: target, serial: 1}, ...options}));
    await s.engine.settle(); await tick(); await s.engine.settle();
    // Later renders of the same replacement ask nothing more.
    await s.engine.render(s.e(s.Scene, {kind, ids: after, refocus: {focusId: target, serial: 1}, ...options})); await s.engine.settle(); await tick();
    return {focused: s.engine.focused, requests: s.requests};
  } finally {await s.engine.unmount();}
}

for (const platform of ['android', 'tvos'] as const) for (const [kind, regionId] of [['row', 'search.movies'], ['grid', 'search.songs']] as const) {
  test(`${platform} ${kind}: a search row read again keeps focus in it, on the last entry that survived`, async () => {
    // e1 had focus and is gone; e2 and e3 survive, so the destination names e3.
    const result = await replaced(platform, kind, 'e1', ['e9', 'e2', 'e3'], 'e3');
    assert.deepEqual(result.requests, [{regionId, childId: 'e3'}]);
    assert.equal(result.focused, 'e3');
  });
  test(`${platform} ${kind}: a search row read again with none of its entries left puts focus on its first entry`, async () => {
    const result = await replaced(platform, kind, 'e2', ['x1', 'x2'], 'x1');
    assert.equal(result.focused, 'x1');
  });
  test(`${platform} ${kind}: a focused entry that survives the re-read keeps focus, and nothing is asked`, async () => {
    const result = await replaced(platform, kind, 'e2', ['e9', 'e2', 'e3'], 'e3');
    assert.deepEqual(result.requests, []); assert.equal(result.focused, 'e2');
  });
  test(`${platform} ${kind}: a covered search row asks for no focus when it is read again`, async () => {
    const result = await replaced(platform, kind, 'e1', ['e9', 'e2', 'e3'], 'e3', {covered: true});
    assert.deepEqual(result.requests, [], 'only the top layer takes focus');
  });
  test(`${platform} ${kind}: a search row that did not hold focus asks for none when it is read again`, async () => {
    const result = await replaced(platform, kind, 'keyboard', ['e9', 'e2', 'e3'], 'e3');
    assert.deepEqual(result.requests, []); assert.equal(result.focused, 'keyboard');
  });
}

for (const platform of ['android', 'tvos'] as const) test(`${platform} grid: without the target, focus is wherever recovery puts it, not the named entry`, async () => {
  const result = await replaced(platform, 'grid', 'e1', ['e9', 'e2', 'e3'], 'e3', {targeted: false});
  assert.deepEqual(result.requests, []); assert.notEqual(result.focused, 'e3');
});

// Once per serial (lead, 5 Nov): a replacement's request is decided once. A later change of the region's id (the effect
// running again with the same serial) does not ask again.
for (const platform of ['android', 'tvos'] as const) test(`${platform} grid: a region id change after the request does not ask again for the same replacement`, async () => {
  const s = await scene(platform);
  try {
    await s.engine.render(s.e(s.Scene, {kind: 'grid', ids: ['e1', 'e2', 'e3']}));
    await s.engine.focus('e1');
    await s.engine.render(s.e(s.Scene, {kind: 'grid', ids: ['e9', 'e2', 'e3'], refocus: {focusId: 'e3', serial: 1}}));
    await s.engine.settle(); await tick(); await s.engine.settle();
    assert.deepEqual(s.requests, [{regionId: 'search.songs', childId: 'e3'}]);
    await s.engine.render(s.e(s.Scene, {kind: 'grid', ids: ['e9', 'e2', 'e3'], refocus: {focusId: 'e3', serial: 1}, regionId: 'search.songs-again'}));
    await s.engine.settle(); await tick(); await s.engine.settle();
    assert.deepEqual(s.requests, [{regionId: 'search.songs', childId: 'e3'}], 'one request for one replacement');
  } finally {await s.engine.unmount();}
});
