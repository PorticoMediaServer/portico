// @fabric-react
import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import * as hostRefModule from '../src-next/kit/focus/useHostRef.ts';
import * as registryModule from '../src-next/kit/focus/registry.ts';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

type Host = {nativeTag: number; requestTVFocus(): void};
async function focusModule() {
  const contexts: {current: unknown}[] = [];
  const controlledReact = {...React, default: React,
    createContext: (current: unknown) => {const context = {current}; contexts.push(context); return context;},
    useContext: (context: {current: unknown}) => context.current,
  };
  const mod = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react: controlledReact,
    'react-native': {findNodeHandle: (host: Host) => host.nativeTag, TVFocusGuideView: 'TVFocusGuideView', View: 'View'},
    '../theme': {useTheme: () => ({isTV: true})}, './registry': registryModule, './useHostRef': hostRefModule,
  });
  return {mod, contexts};
}
test('real target hook installs native edges only on boundary children and preserves caller host refs', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry; contexts[1]!.current = 'row';
  registry.configure({id: 'row', orientation: 'horizontal', order: ['a', 'b', 'c'], edges: {left: 'rail', up: 'spotlight', down: 'below'}});
  registry.attachRegion('rail', {nativeTag: 201, requestTVFocus() {}} as Host);
  registry.attachRegion('spotlight', {nativeTag: 202, requestTVFocus() {}} as Host);
  registry.attachRegion('below', {nativeTag: 203, requestTVFocus() {}} as Host);
  let focusId = 'a', disabled = false, bound: any;
  const actual = {nativeTag: 101, requestTVFocus() {}};
  const forwarded = {current: null as Host | null};
  const driver = createFabricHookHost(() => {bound = mod.useFocusTarget({focusId, disabled, ref: forwarded});});
  await driver.mount();
  try {
    bound.ref(actual); await driver.flush();
    assert.equal(forwarded.current, actual); assert.equal(registry.child({regionId: 'row', childId: 'a'}), actual);
    assert.equal(bound.testID, registryModule.focusTestID('row', 'a'));
    assert.equal(bound.nextFocusLeft, 201); assert.equal(bound.nextFocusUp, 202); assert.equal(bound.nextFocusDown, 203);
    bound.onFocus(); await driver.flush(); assert.equal(registry.capture()?.childId, 'a');
    focusId = 'b'; await driver.rerender();
    assert.equal(bound.nextFocusLeft, undefined, 'left from middle child stays under native intra-row search');
    assert.equal(bound.nextFocusDown, 203); assert.equal(registry.child({regionId: 'row', childId: 'a'}), undefined);
    assert.equal(registry.child({regionId: 'row', childId: 'b'}), actual);
    disabled = true; await driver.rerender();
    assert.equal(registry.child({regionId: 'row', childId: 'b'}), undefined);
    bound.ref(null); assert.equal(forwarded.current, null);
  } finally {await driver.unmount();}
});
test('real target hook retains supplied IDs, cursor identity and callback refs when waiting fills', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry; contexts[1]!.current = 'row';
  contexts[2]!.current = {focusId: 'source-cursor', testID: 'actual-resource-id', waiting: true};
  registry.configure({id: 'row', order: ['source-cursor']});
  const refs: unknown[] = [], callback = (host: unknown) => refs.push(host);
  let bound: any;
  const driver = createFabricHookHost(() => {bound = mod.useFocusTarget({ref: callback});});
  await driver.mount();
  try {
    const host = {nativeTag: 10, requestTVFocus() {}};
    bound.ref(host); bound.onFocus(); await driver.flush();
    const ref = bound.ref; assert.equal(bound.waiting, true); assert.equal(bound.testID, 'actual-resource-id');
    contexts[2]!.current = {focusId: 'source-cursor', testID: 'actual-resource-id', waiting: false}; await driver.rerender();
    assert.equal(bound.ref, ref); assert.equal(bound.waiting, false); assert.equal(bound.testID, 'actual-resource-id');
    assert.equal(registry.child({regionId: 'row', childId: 'source-cursor'}), host);
    assert.deepEqual(refs, [host], 'data arriving neither detaches nor replaces native host');
  } finally {await driver.unmount();}
});
test('native guide composition uses one shared primitive with native holds and no directional handlers', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry;
  let outer: any;
  const driver = createFabricHookHost(() => {outer = mod.FocusRegion.render({id: 'actual-menu', hold: true, children: null}, null);});
  await driver.mount();
  let native: any;
  const guideDriver = createFabricHookHost(() => {native = outer.type.render(outer.props, null);});
  await guideDriver.mount();
  try {
    assert.equal(native.type, 'TVFocusGuideView'); assert.equal(native.props.autoFocus, true);
    for (const edge of ['Up', 'Down', 'Left', 'Right']) assert.equal(native.props[`trapFocus${edge}`], true);
    assert.equal(native.props.testID, 'portico.region.actual-menu');
    assert.equal(native.props.onKeyDown, undefined); assert.equal(native.props.onTVEvent, undefined);
  } finally {await guideDriver.unmount(); await driver.unmount();}
});

test('a guide sends native destinations only when the remembered host or guide changes', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry;
  const first = {nativeTag: 101, requestTVFocus() {}}, second = {nativeTag: 102, requestTVFocus() {}};
  registry.attach({regionId: 'row', childId: 'a'}, first);
  registry.attach({regionId: 'row', childId: 'b'}, second);
  let outer: any, native: any;
  const root = createFabricHookHost(() => {outer = mod.FocusRegion.render({id: 'row', order: ['a', 'b'], children: null}, null);}); await root.mount();
  const host = createFabricHookHost(() => {native = outer.type.render(outer.props, null);}); await host.mount();
  const sent: number[][] = [], replacementSent: number[][] = [];
  const guide = {nativeTag: 201, requestTVFocus() {}, setDestinations: (handles: number[]) => sent.push(handles)};
  try {
    native.props.ref(guide); await host.flush();
    assert.deepEqual(sent, [[101]]);
    registry.didFocus({regionId: 'row', childId: 'a'}); await host.flush();
    registry.didFocus({regionId: 'row', childId: 'a'}); await host.flush();
    registry.configure({id: 'unrelated', order: ['other']}); await host.flush();
    assert.deepEqual(sent, [[101]], 'unchanged entry and unrelated commits do not cross the native bridge');
    registry.didFocus({regionId: 'row', childId: 'b'}); await host.flush();
    assert.deepEqual(sent, [[101], [102]]);
    native.props.ref(null); await host.flush();
    native.props.ref({nativeTag: 202, requestTVFocus() {}, setDestinations: (handles: number[]) => replacementSent.push(handles)}); await host.flush();
    assert.deepEqual(replacementSent, [[102]], 'a newly attached guide receives its remembered destination');
  } finally {native.props.ref(null); await host.unmount(); await root.unmount();}
});

test('F-RAIL-5 and F-RAIL-8 declare native vertical end stops while leaving Right open', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry;
  let outer: any, native: any;
  const driver = createFabricHookHost(() => {outer = mod.FocusRegion.render({id: 'rail', orientation: 'vertical',
    order: ['search', 'home', 'settings', 'profile'], edges: {right: 'home-row'}, children: null}, null);});
  await driver.mount();
  const guideDriver = createFabricHookHost(() => {native = outer.type.render(outer.props, null);});
  await guideDriver.mount();
  try {
    assert.equal(native.props.trapFocusUp, true, 'Search cannot leave the rail through its top edge');
    assert.equal(native.props.trapFocusDown, true, 'profile cannot leave the rail through its bottom edge');
    assert.equal(native.props.trapFocusRight, false, 'Right can return to the page opener');
    assert.equal(native.props.onKeyDown, undefined); assert.equal(native.props.onTVEvent, undefined);
  } finally {await guideDriver.unmount(); await driver.unmount();}
});

test('vertical native end stops preserve each declared handoff and held sheets still trap every edge', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry;
  let props = {id: 'column', orientation: 'vertical', edges: {} as {up?: string; down?: string}, hold: false, children: null};
  let outer: any, native: any;
  const driver = createFabricHookHost(() => {outer = mod.FocusRegion.render(props, null);}); await driver.mount();
  const guideDriver = createFabricHookHost(() => {native = outer.type.render(outer.props, null);}); await guideDriver.mount();
  try {
    for (const edges of [{up: 'toolbar'}, {down: 'next-row'}, {up: 'toolbar', down: 'next-row'}]) {
      props = {...props, edges}; await driver.rerender(); await guideDriver.rerender();
      assert.equal(native.props.trapFocusUp, !('up' in edges));
      assert.equal(native.props.trapFocusDown, !('down' in edges));
    }
    props = {...props, hold: true}; await driver.rerender(); await guideDriver.rerender();
    for (const edge of ['Up', 'Down', 'Left', 'Right']) assert.equal(native.props[`trapFocus${edge}`], true);
  } finally {await guideDriver.unmount(); await driver.unmount();}
});

test('F07 committed region configuration publishes a revision to reused target hooks without render-time mutation', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry; contexts[1]!.current = 'row';
  registry.attachRegion('rail', {nativeTag: 200, requestTVFocus() {}} as Host);
  let props = {id: 'row', orientation: 'horizontal', order: ['a', 'b'], edges: {left: 'rail'}};
  let outer: any, guide: any, bound: any, duringRender: unknown; const renderOrders: unknown[] = [];
  const outerDriver = createFabricHookHost(() => {outer = mod.FocusRegion.render(props, null);}); await outerDriver.mount();
  const regionDriver = createFabricHookHost(() => {guide = outer.type.render(outer.props, null); duringRender = registry.config('row')?.order; renderOrders.push(duringRender);}); await regionDriver.mount();
  const targetDriver = createFabricHookHost(() => {bound = mod.useFocusTarget({focusId: 'a'});}); await targetDriver.mount();
  try {
    assert.equal(bound.nextFocusLeft, 200);
    const revision = registry.getSnapshot();
    renderOrders.length = 0; props = {...props, order: ['b', 'a']}; await outerDriver.rerender(); await regionDriver.rerender(); await targetDriver.flush();
    assert.deepEqual(renderOrders[0], ['a', 'b'], 'speculative rendering sees last committed config');
    assert.deepEqual(renderOrders.at(-1), ['b', 'a'], 'the subscription render sees the committed replacement');
    assert.deepEqual(registry.config('row')?.order, ['b', 'a']); assert.equal(bound.nextFocusLeft, undefined);
    assert.equal(registry.getSnapshot(), revision + 1);
    await regionDriver.rerender(); assert.equal(registry.getSnapshot(), revision + 1, 'equal config does not emit a render loop');
    assert.equal(guide.props.onKeyDown, undefined);
  } finally {await targetDriver.unmount(); await regionDriver.unmount(); await outerDriver.unmount();}
});

test('F11 actual target ref keeps one native callback while replacing and cleaning React 19 caller refs', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry; contexts[1]!.current = 'row';
  const host = {nativeTag: 1, requestTVFocus() {}}, calls: string[] = [];
  let ref: React.ForwardedRef<Host> = value => {assert.equal(value, host); calls.push('attach-a'); return () => {calls.push('cleanup-a');};};
  let bound: any;
  const driver = createFabricHookHost(() => {bound = mod.useFocusTarget({focusId: 'a', ref});}); await driver.mount();
  const nativeRef = bound.ref, cleanup = nativeRef(host);
  try {
    ref = value => {assert.equal(value, host); calls.push('attach-b'); return () => {calls.push('cleanup-b');};}; await driver.rerender();
    assert.equal(bound.ref, nativeRef, 'Reanimated sees a stable native ref across consumer replacements');
    assert.deepEqual(calls, ['attach-a', 'cleanup-a', 'attach-b']);
    cleanup(); assert.deepEqual(calls, ['attach-a', 'cleanup-a', 'attach-b', 'cleanup-b']);
    assert.equal(registry.child({regionId: 'row', childId: 'a'}), undefined);
  } finally {await driver.unmount();}
  assert.deepEqual(calls, ['attach-a', 'cleanup-a', 'attach-b', 'cleanup-b']);
});

test('F11 guide forwards its real host and honors ref cleanup on replacement and unmount', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry(); contexts[0]!.current = registry;
  const calls: string[] = [], host = {nativeTag: 2, requestTVFocus() {}, setDestinations() {}};
  let ref: React.ForwardedRef<any> = value => {assert.equal(value, host); calls.push('attach'); return () => {calls.push('cleanup');};};
  let outer: any, guide: any;
  const outerDriver = createFabricHookHost(() => {outer = mod.FocusRegion.render({id: 'menu', children: null}, ref);}); await outerDriver.mount();
  const driver = createFabricHookHost(() => {guide = outer.type.render(outer.props, outer.props.ref);}); await driver.mount();
  guide.props.ref(host);
  try {
    ref = {current: null}; await outerDriver.rerender(); await driver.rerender(); assert.deepEqual(calls, ['attach', 'cleanup']); assert.equal(ref.current, host);
  } finally {await driver.unmount(); await outerDriver.unmount();}
  assert.equal(ref.current, null); assert.equal(registry.host('menu'), undefined);
});

test('F-HOME-63 shared native boundary retries data once per Right stroke only on actual failed tail', async () => {
  const registry = registryModule.createFocusRegistry(); let listener: ((event: any) => void) | undefined, removed = 0, retries = 0;
  registry.configure({id: 'row', order: ['a', 'b']});
  registry.attach({regionId: 'row', childId: 'b'}, {nativeTag: 17, requestTVFocus() {throw new Error('Retry must not select a target');}} as Host);
  const mod = await screenModule(new URL('../src-next/kit/focus/page-boundary.ts', import.meta.url), {
    '../../platform/tv-key-adapter': {createTVKeyAdapter}, '../../platform/tv-key-source': {tvKeySource: 'native'},
    react: {...React, default: React}, 'react-native': {findNodeHandle: (host: Host) => host.nativeTag,
      TVEventHandler: {addListener: (value: typeof listener) => {listener = value; return {remove() {listener = undefined; removed++;}};}}},
  });
  let page = {waitingId: 'cursor', hasMore: true, failed: true, loading: false, requestNext: () => {retries++;}};
  const driver = createFabricHookHost(() => {mod.useRowPageBoundary(true, registry, 'row', 'b', page);}); await driver.mount();
  try {
    registry.didFocus({regionId: 'row', childId: 'a'}); listener!({eventType: 'right', eventKeyAction: 0}); listener!({eventType: 'right', eventKeyAction: 1}); assert.equal(retries, 0);
    registry.didFocus({regionId: 'row', childId: 'b'});
    listener!({eventType: 'right', eventKeyAction: 0, tag: 18}); listener!({eventType: 'right', eventKeyAction: 1}); assert.equal(retries, 0);
    listener!({eventType: 'right', eventKeyAction: 0, tag: 17}); listener!({eventType: 'right', eventKeyAction: 0, tag: 17}); listener!({eventType: 'right', eventKeyAction: 1}); assert.equal(retries, 1);
    listener!({eventType: 'right', eventKeyAction: 0, tag: 17}); listener!({eventType: 'right', eventKeyAction: 1}); assert.equal(retries, 2);
    page = {...page, loading: true}; await driver.rerender(); assert.equal(listener, undefined); assert.equal(removed, 1);
    page = {...page, loading: false}; await driver.rerender(); listener!({eventType: 'right', eventKeyAction: 0, tag: 17}); assert.equal(retries, 3, 'same failed cursor can be retried after another failure');
  } finally {await driver.unmount();}
  assert.equal(removed, 2);
});

test('opted-in cell bindings resolve semantic first-column and short-row destinations on real native hosts', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry; contexts[1]!.current = 'grid';
  contexts[2]!.current = {focusId: 'last', cell: {index: 10}};
  registry.configure({id: 'grid', grid: {columns: 4, count: 11}, edges: {left: 'keyboard'}});
  registry.attachRegion('keyboard', {nativeTag: 20, requestTVFocus() {}} as Host);
  const previous = {nativeTag: 7, requestTVFocus() {}} as Host;
  registry.attach({regionId: 'grid', childId: 'previous'}, previous, {}, {index: 7});
  registry.didFocus({regionId: 'grid', childId: 'previous'});
  let bound: any;
  const driver = createFabricHookHost(() => {bound = mod.useFocusTarget({});}); await driver.mount();
  try {
    bound.ref({nativeTag: 10, requestTVFocus() {}}); await driver.flush(); bound.onFocus(); await driver.flush();
    assert.equal(bound.nextFocusUp, 7, 'native Up restores the column before the short row');
    assert.equal(bound.nextFocusRight, 10, 'the native host owns the true-end stop');
    contexts[2]!.current = {focusId: 'first-column', cell: {index: 8}}; await driver.rerender();
    assert.equal(bound.nextFocusLeft, 20, 'Left on a later first-column cell reaches the keyboard region');
    const before = registry.getSnapshot();
    contexts[2]!.current = {focusId: 'first-column', cell: {index: 8}}; await driver.rerender();
    assert.equal(registry.getSnapshot(), before, 'equal inline descriptors do not detach or republish the native host');
  } finally {await driver.unmount();}
});

test('a guide that was disabled and is enabled again sends its destination anew', async () => {
  const {mod, contexts} = await focusModule(), registry = registryModule.createFocusRegistry();
  contexts[0]!.current = registry;
  registry.attach({regionId: 'row', childId: 'a'}, {nativeTag: 101, requestTVFocus() {}} as Host);
  let enabled = true, outer: any, native: any;
  const root = createFabricHookHost(() => {outer = mod.FocusRegion.render({id: 'row', order: ['a'], enabled, children: null}, null);}); await root.mount();
  const host = createFabricHookHost(() => {native = outer.type.render(outer.props, null);}); await host.mount();
  const sent: number[][] = [];
  try {
    native.props.ref({nativeTag: 201, requestTVFocus() {}, setDestinations: (handles: number[]) => sent.push(handles)}); await host.flush();
    assert.deepEqual(sent, [[101]]);
    // The native guide drops its destinations when it is disabled; enabling it must send them again.
    enabled = false; await root.rerender(); await host.rerender();
    enabled = true; await root.rerender(); await host.rerender();
    assert.equal(sent.at(-1)?.[0], 101);
    assert.ok(sent.length >= 3, 'a send after each enabled change');
  } finally {native.props.ref(null); await host.unmount(); await root.unmount();}
});
