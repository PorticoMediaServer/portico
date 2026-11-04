import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import * as rowState from '../src-next/kit/layout/row-state';
import * as layoutTypes from '../src-next/kit/layout/types';
import {RelationRowPager} from '../../../packages/client-core/src/relation-rows.ts';
import {NativeRelatedRow, type NativeRelatedRowSnapshot} from '../src-next/screens/watch/title-related.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';

// #674 review: a TV related row read again from its first page (the server refused its cursor) can drop the title that
// had focus. The platforms recover differently (Android: the nearest focus guide, the row; tvOS: the screen's preferred
// view, outside the row), so the row names its own in-row target: the last title that survived, or the first title.
// The real Row, focus kit and NativeRelatedRow run through the fake native focus engine on both platforms.
// Device check this stands in for: Apple TV, a title's related row (see #674's body).

const entry = (id: string) => ({id, kind: 'movie', title: id}) as any;
const tick = () => new Promise(resolve => setTimeout(resolve, 0));

async function scene(platform: 'android' | 'tvos') {
  const kit = await tvFocusKit({platform}), {engine, e, focus, Focusable, Stop, React, TVRow} = kit, native = engine.native;
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
  // The row's own requests (registry.focus); the registry's internal recovery does not go through this property.
  const requests: {regionId: string; childId: string}[] = [], focusOf = registry.focus;
  registry.focus = ((address: {regionId: string; childId: string}, options?: unknown) => {requests.push({...address}); return focusOf(address, options as never);}) as typeof registry.focus;
  const unselected = {active: false, isSelected: () => false, onToggle() {}, onEnter() {}};
  // TitleScreen's mapping of a related row's pages to the shelf's paging (TitleScreen.tsx, related rows).
  const Scene = ({pages, targeted = true, covered = false}: {pages: NativeRelatedRowSnapshot; targeted?: boolean; covered?: boolean}) => e(focus.FocusProvider, {registry},
    e(native.View, {style: {flex: 1}},
      e(focus.FocusRegion, {id: 'title.actions', orientation: 'horizontal', edges: {down: 'title.related'}, style: {height: 120}}, e(Stop, {id: 'play', w: 240, h: 80})),
      e(focus.CoveredLayer, {covered}, e(row.Row, {title: '', showHeading: false, items: pages.entries, getKey: (value: any) => value.id, itemWidth: 214, itemHeight: 420, gap: 28, selection: unselected,
        initialViewportWidth: 1920, focus: {id: 'title.related', edges: {up: 'title.actions'}},
        paging: {hasMore: pages.hasMore, loading: pages.loading, failed: pages.failed, waitingId: pages.waitingId, fulfilled: pages.fulfilled, waitingItem: entry('waiting'),
          requestNext() {}, ...(targeted && pages.replaced ? {refocus: {focusId: pages.replaced.entryId, serial: pages.replaced.serial}} : {})},
        renderItem: (value: any, slot: any) => e(Focusable, {testID: slot.waiting ? 'waiting' : value.id, focusId: slot.focusId, onFocus: slot.onFocus, onBlur: slot.onBlur,
          style: {width: 214, height: 321}}, () => null)}))));
  return {engine, e, Scene, requests};
}

async function replaced(platform: 'android' | 'tvos', reread: readonly string[], targeted = true, start = 'm3') {
  const s = await scene(platform);
  const pager = new RelationRowPager({entries: ['m1', 'm2', 'm3'].map(entry), nextCursor: 'c1'}, async cursor => {
    if (cursor === '') return {entries: reread.map(entry)};
    throw Object.assign(new Error('Expired'), {code: 'invalid_cursor'});
  });
  const related = new NativeRelatedRow('title', pager, () => {});
  try {
    await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot(), targeted}));
    await s.engine.focus(start);
    const playTag = s.engine.tag('play');
    related.next(related.getSnapshot()); await tick();
    assert.deepEqual(related.getSnapshot().entries.map(value => value.id), reread);
    await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot(), targeted}));
    await s.engine.settle(); await tick(); await s.engine.settle();
    return {focused: s.engine.focused, target: related.getSnapshot().replaced?.entryId, log: s.engine.log, playTag};
  } finally {related.dispose(); await s.engine.unmount();}
}

for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: a related row read again keeps focus in the row, on the last title that survived`, async () => {
    const result = await replaced(platform, ['m9', 'm1', 'm2']);
    assert.equal(result.target, 'm2');
    assert.equal(result.focused, 'm2', 'm3 had focus and is gone: focus is on the last surviving title, in the row');
  });

  test(`${platform}: the row's own target decides, not the platform's or the registry's recovery`, async () => {
    // m1 had focus and is gone; m2 and m3 survive. The registry's own recovery picks m1's next neighbour (m2);
    // the row names the last surviving title (m3), and that is where focus lands.
    const result = await replaced(platform, ['m9', 'm2', 'm3'], true, 'm1');
    assert.equal(result.target, 'm3');
    assert.equal(result.focused, 'm3');
    const without = await replaced(platform, ['m9', 'm2', 'm3'], false, 'm1');
    assert.equal(without.focused, 'm2', 'without the target, where focus ends is whatever recovery picks');
  });

  test(`${platform}: a related row read again with none of its titles left puts focus on its first title`, async () => {
    const result = await replaced(platform, ['x1', 'x2']);
    assert.equal(result.target, 'x1');
    assert.equal(result.focused, 'x1');
  });
}

test('without the row\'s target, tvOS itself leaves the row: the engine recovers to the screen\'s preferred view (Play) and only the registry\'s JS recovery brings focus back', async () => {
  const result = await replaced('tvos', ['m9', 'm1', 'm2'], false);
  const at = result.log.findIndex((step: any) => step.why === 'removed');
  assert.equal(result.log[at + 1]?.why, 'recover-preferred'); assert.equal(result.log[at + 1]?.to, result.playTag, 'tvOS moves focus out of the row to Play');
  const android = await replaced('android', ['m9', 'm1', 'm2'], false);
  const from = android.log.findIndex((step: any) => step.why === 'removed');
  assert.equal(android.log[from + 1]?.why, 'recover', 'Android recovers through the row\'s own focus guide');
});

// The three cases in which the row asks for nothing (#674 review hardening): only a row that held focus, is on the top
// layer and is still mounted when its queued request runs moves focus.
async function noFire(platform: 'android' | 'tvos', mode: 'not-held' | 'covered' | 'unmounted' | 'control') {
  const s = await scene(platform);
  const pager = new RelationRowPager({entries: ['m1', 'm2', 'm3'].map(entry), nextCursor: 'c1'}, async cursor => {
    if (cursor === '') return {entries: ['m9', 'm2', 'm3'].map(entry)};
    throw Object.assign(new Error('Expired'), {code: 'invalid_cursor'});
  });
  const related = new NativeRelatedRow('title', pager, () => {});
  const held: (() => void)[] = [], queue = globalThis.queueMicrotask;
  let mounted = true;
  try {
    await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot()}));
    await s.engine.focus(mode === 'not-held' ? 'play' : 'm1');
    related.next(related.getSnapshot()); await tick();
    const hold = mode === 'unmounted' || mode === 'control';
    if (hold) globalThis.queueMicrotask = callback => {held.push(callback);};
    try {await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot(), covered: mode === 'covered'}));}
    finally {globalThis.queueMicrotask = queue;}
    if (mode === 'unmounted') {await s.engine.unmount(); mounted = false;}
    for (const callback of held) callback();
    await s.engine.settle(); await tick();
    return {requests: s.requests, focused: s.engine.focused, held: held.length};
  } finally {related.dispose(); if (mounted) await s.engine.unmount();}
}

for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: a row that did not hold focus asks for none when it is read again`, async () => {
    const result = await noFire(platform, 'not-held');
    assert.deepEqual(result.requests, []); assert.equal(result.focused, 'play');
  });
  test(`${platform}: a covered row (inside a cover claim's wait) asks for no focus when it is read again`, async () => {
    const result = await noFire(platform, 'covered');
    assert.deepEqual(result.requests, [], 'only the top layer takes focus');
  });
  test(`${platform}: a row unmounted before its queued request runs asks for nothing; mounted, the same request goes out`, async () => {
    const control = await noFire(platform, 'control');
    assert.deepEqual(control.requests, [{regionId: 'title.related', childId: 'm3'}], 'the request is queued as a microtask');
    const result = await noFire(platform, 'unmounted');
    assert.ok(result.held > 0); assert.deepEqual(result.requests, []);
  });
}

// #697: Row also counts a slot that stays but went inert as lost (an index-keyed window). A title row has no isFocusable,
// so none of its slots is ever inert: what it does is unchanged. A focused title that survives the re-read keeps focus
// and the row asks for nothing; one that is gone still moves to the row's target, once.
for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: a related row read again that keeps the focused title asks for nothing (unchanged by #697)`, async () => {
    const s = await scene(platform);
    const pager = new RelationRowPager({entries: ['m1', 'm2', 'm3'].map(entry), nextCursor: 'c1'}, async cursor => {
      if (cursor === '') return {entries: ['m9', 'm2', 'm3'].map(entry)};
      throw Object.assign(new Error('Expired'), {code: 'invalid_cursor'});
    });
    const related = new NativeRelatedRow('title', pager, () => {});
    try {
      await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot()}));
      await s.engine.focus('m2');
      related.next(related.getSnapshot()); await tick();
      await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot()}));
      await s.engine.settle(); await tick(); await s.engine.settle();
      // Later renders of the same replacement (its page settling) do not ask again either.
      await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot()})); await s.engine.settle(); await tick();
      assert.deepEqual(s.requests, []); assert.equal(s.engine.focused, 'm2');
    } finally {related.dispose(); await s.engine.unmount();}
  });

  test(`${platform}: a related row whose focused title is gone asks for its target once, across later renders (unchanged by #697)`, async () => {
    const s = await scene(platform);
    const pager = new RelationRowPager({entries: ['m1', 'm2', 'm3'].map(entry), nextCursor: 'c1'}, async cursor => {
      if (cursor === '') return {entries: ['m9', 'm1', 'm2'].map(entry)};
      throw Object.assign(new Error('Expired'), {code: 'invalid_cursor'});
    });
    const related = new NativeRelatedRow('title', pager, () => {});
    try {
      await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot()}));
      await s.engine.focus('m3');
      related.next(related.getSnapshot()); await tick();
      for (let render = 0; render < 3; render++) {await s.engine.render(s.e(s.Scene, {pages: related.getSnapshot()})); await s.engine.settle(); await tick();}
      assert.deepEqual(s.requests, [{regionId: 'title.related', childId: 'm2'}]); assert.equal(s.engine.focused, 'm2');
    } finally {related.dispose(); await s.engine.unmount();}
  });
}
