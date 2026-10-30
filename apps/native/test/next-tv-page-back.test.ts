// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {getEventListeners} from 'node:events';
import * as React from 'react';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {createTVPageBack} from '../src-next/screens/destinations/tv-page-back.ts';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const first = {regionId: 'grid', childId: 'first'}, ninth = {regionId: 'grid', childId: 'ninth'};
function fixture() {
  const registry = createFocusRegistry();
  let top = false, current = true, scrolls = 0, requests = 0;
  registry.didFocus(ninth);
  const owner = createTVPageBack(registry, {current: () => current, start: () => first, atTop: () => top,
    scrollTop: () => {scrolls++; top = true;}});
  const mount = () => registry.attach(first, {requestTVFocus() {requests++; registry.didFocus(first);}});
  return {registry, owner, mount, retire: () => {current = false;}, counts: () => ({scrolls, requests})};
}
test('Back scrolls a virtualized page before requesting its real starting host, then yields to Home', async () => {
  const f = fixture();
  assert.equal(f.owner.back(), true); assert.deepEqual(f.counts(), {scrolls: 1, requests: 0});
  assert.equal(f.owner.back(), true, 'an outstanding first jump cannot skip the page');
  await f.mount(); assert.deepEqual(f.counts(), {scrolls: 1, requests: 1});
  assert.equal(f.owner.back(), false);
  assert.equal(f.registry.remembered('grid'), 'first'); f.owner.dispose();
});
test('a retired page or disposed owner never acquires focus when old data mounts', async () => {
  for (const action of ['retire', 'dispose']) {
    const f = fixture(); f.owner.back();
    if (action === 'retire') f.retire(); else f.owner.dispose();
    await f.mount(); assert.equal(f.counts().requests, 0); f.owner.dispose();
  }
});
test('a later genuine focus move cancels an outstanding Back jump', async () => {
  const f = fixture(); f.owner.back(); f.registry.didFocus({regionId: 'rail', childId: 'saved'});
  await f.mount(); assert.equal(f.counts().requests, 0); f.owner.dispose();
});
test('Back from the first item still resets nonzero scroll without clearing unrelated row memory', async () => {
  const f = fixture(); f.registry.didFocus({regionId: 'other-row', childId: 'remembered'}); f.registry.didFocus(first); await f.mount();
  assert.equal(f.owner.back(), true); assert.deepEqual(f.counts(), {scrolls: 1, requests: 1});
  assert.equal(f.registry.remembered('other-row'), 'remembered'); f.owner.dispose();
});

test('an evicted first item is resolved after the explicit scroll and real host attachment', () => {
  const registry = createFocusRegistry(); registry.didFocus(ninth);
  let top = false, loaded = false, requests = 0;
  const owner = createTVPageBack(registry, {current: () => true, start: () => loaded ? first : undefined,
    atTop: () => top, scrollTop: () => {top = true;}});
  assert.equal(owner.back(), true);
  loaded = true; registry.attach(first, {requestTVFocus() {requests++; registry.didFocus(first);}});
  assert.equal(requests, 1); assert.equal(owner.back(), false); owner.dispose();
});

type Slots = {backs: Set<() => boolean>; tops: Set<() => boolean>};
async function hookFixture(options: {aborted?: boolean; enabled?: boolean; signal?: boolean;
  retireIn?: 'back-registration' | 'top-registration' | 'snapshot' | 'current'; slots?: Slots} = {}) {
  // Real hook, page owner and registry; only the runtime/native-host boundary is controlled.
  const registry = createFocusRegistry(), life = new AbortController();
  const slots = options.slots ?? {backs: new Set<() => boolean>(), tops: new Set<() => boolean>()};
  let subscriptions = 0, snapshots = 0, currentCalls = 0, scrolls = 0, requests = 0, top = false;
  let removedBack = 0, removedTop = 0, back: (() => boolean) | undefined, jump: (() => boolean) | undefined;
  const subscribe = registry.subscribe;
  registry.subscribe = listener => {subscriptions++; const off = subscribe(listener); return () => {subscriptions--; off();};};
  registry.didFocus(ninth);
  const snapshot = {destination: {kind: 'saved'}, contentVisible: true, mediaExpanded: false, homeObscured: false};
  const runtime: any = {retirementSignal: options.signal === false ? undefined : life.signal,
    getSnapshot() {
      snapshots++;
      if (life.signal.aborted) throw new Error('native_shell_cancelled');
      if (options.retireIn === 'snapshot') life.abort();
      return snapshot;
    },
    registerTVPageBack(handler: () => boolean) {
      back = handler; slots.backs.add(handler);
      if (options.retireIn === 'back-registration') life.abort();
      return () => {removedBack++; slots.backs.delete(handler);};
    },
    registerScrollToTop(_destination: unknown, handler: () => boolean) {
      jump = handler; slots.tops.add(handler);
      if (options.retireIn === 'top-registration') life.abort();
      return () => {removedTop++; slots.tops.delete(handler);};
    }};
  const {useTVPageBack} = await screenModule(new URL('../src-next/screens/destinations/useTVPageBack.ts', import.meta.url), {
    react: {...React, default: React}, '../../kit/focus': {useFocusRegistry: () => registry}, './tv-page-back': {createTVPageBack},
  });
  const page = {registerTop: true, current: () => {currentCalls++; if (options.retireIn === 'current') life.abort(); return true;},
    start: () => first, atTop: () => top, scrollTop: () => {scrolls++; top = true;}};
  if (options.aborted) life.abort();
  const host = createFabricHookHost(() => useTVPageBack(runtime, {kind: 'saved'}, options.enabled !== false, page)); await host.mount();
  return {registry, life, slots, host, page, snapshot, back: () => back, jump: () => jump,
    mount: () => registry.attach(first, {requestTVFocus() {requests++; registry.didFocus(first);}}),
    counts: () => ({subscriptions, snapshots, currentCalls, scrolls, requests, removedBack, removedTop})};
}

test('actual PageBack hook aborts a pending virtualized jump before layout cleanup or any later registry notification', async () => {
  const f = await hookFixture();
  try {
    assert.equal(getEventListeners(f.life.signal, 'abort').length, 1);
    const back = f.back()!, jump = f.jump()!;
    assert.equal(back(), true); assert.equal(f.counts().scrolls, 1); assert.equal(f.counts().requests, 0);
    f.life.abort();
    assert.equal(f.counts().subscriptions, 0, 'abort synchronously detaches the actual registry observer');
    assert.equal(f.slots.backs.size, 0); assert.equal(f.slots.tops.size, 0);
    const before = f.counts();
    await f.mount();
    f.registry.configure({id: 'grid', entry: 'first', order: ['first', 'ninth']}); f.registry.attach(first, null);
    assert.equal(back(), false); assert.equal(jump(), false);
    assert.deepEqual(f.counts(), before, 'retained callbacks/attach/configure/detach never read the retired runtime or request focus');
    await f.host.unmount(); assert.equal(f.counts().removedBack, 1); assert.equal(f.counts().removedTop, 1);
    assert.equal(getEventListeners(f.life.signal, 'abort').length, 0);
  } finally {await f.host.unmount();}
});

for (const options of [{aborted: true}, {enabled: false}]) test(`PageBack hook refuses before subscription or registration: ${JSON.stringify(options)}`, async () => {
  const f = await hookFixture(options);
  try {
    assert.equal(f.back(), undefined); assert.equal(f.jump(), undefined);
    assert.deepEqual(f.counts(), {subscriptions: 0, snapshots: 0, currentCalls: 0, scrolls: 0, requests: 0, removedBack: 0, removedTop: 0});
    assert.equal(getEventListeners(f.life.signal, 'abort').length, 0);
  } finally {await f.host.unmount();}
});

test('live PageBack hook preserves visibility/route guards, virtualized Back/top and inert callbacks after unmount', async () => {
  const f = await hookFixture();
  try {
    const back = f.back()!, jump = f.jump()!;
    for (const key of ['mediaExpanded', 'homeObscured'] as const) {
      f.snapshot[key] = true; assert.equal(back(), false); f.snapshot[key] = false;
    }
    f.snapshot.contentVisible = false; assert.equal(jump(), false); f.snapshot.contentVisible = true;
    f.snapshot.destination = {kind: 'home'}; assert.equal(back(), false); f.snapshot.destination = {kind: 'saved'};
    assert.equal(f.counts().scrolls, 0);
    assert.equal(back(), true); assert.equal(f.counts().requests, 0);
    await f.mount(); assert.equal(f.counts().requests, 1); assert.equal(back(), false, 'opening page yields Back');
    assert.equal(jump(), true); assert.equal(f.counts().scrolls, 2); assert.equal(f.counts().requests, 2);
    await f.host.unmount(); const before = f.counts();
    assert.equal(back(), false); assert.equal(jump(), false); assert.deepEqual(f.counts(), before);
    assert.equal(getEventListeners(f.life.signal, 'abort').length, 0);
  } finally {await f.host.unmount();}
});

test('old PageBack retirement and delayed layout cleanup preserve a successor registration', async () => {
  const old = await hookFixture(), next = await hookFixture({slots: old.slots});
  try {
    old.life.abort(); await old.host.unmount();
    assert.deepEqual([...old.slots.backs], [next.back()]); assert.deepEqual([...old.slots.tops], [next.jump()]);
    assert.equal(old.back()!(), false); assert.equal(old.jump()!(), false);
    assert.equal(next.back()!(), true); await next.mount(); assert.equal(next.counts().requests, 1);
    assert.equal(next.counts().removedBack, 0); assert.equal(next.counts().removedTop, 0);
  } finally {await old.host.unmount(); await next.host.unmount();}
});

for (const retireIn of ['back-registration', 'top-registration'] as const) test(`PageBack captures and removes a registration returned after synchronous retirement in ${retireIn}`, async () => {
  const f = await hookFixture({retireIn});
  try {
    assert.equal(f.counts().subscriptions, 0); assert.equal(f.slots.backs.size, 0); assert.equal(f.slots.tops.size, 0);
    assert.equal(f.back()!(), false); assert.equal(f.jump()?.(), retireIn === 'top-registration' ? false : undefined);
    await f.host.unmount(); assert.equal(f.counts().removedBack, 1); assert.equal(f.counts().removedTop, retireIn === 'top-registration' ? 1 : 0);
    assert.equal(f.counts().snapshots, 0); assert.equal(getEventListeners(f.life.signal, 'abort').length, 0);
  } finally {await f.host.unmount();}
});

for (const retireIn of ['snapshot', 'current'] as const) test(`PageBack rechecks original retirement after synchronous ${retireIn} predicate/accessor`, async () => {
  const f = await hookFixture({retireIn});
  try {
    assert.equal(f.back()!(), false); assert.equal(f.counts().scrolls, 0); assert.equal(f.counts().requests, 0);
    assert.equal(f.counts().snapshots, 1); assert.equal(f.counts().currentCalls, retireIn === 'current' ? 1 : 0);
    const before = f.counts(); await f.mount(); assert.equal(f.jump()!(), false); assert.deepEqual(f.counts(), before);
  } finally {await f.host.unmount();}
});

test('PageBack hook retains legacy live behavior without an optional retirement signal and disposes on layout cleanup', async () => {
  const f = await hookFixture({signal: false});
  try {
    assert.equal(f.back()!(), true); await f.host.unmount(); const before = f.counts();
    await f.mount(); assert.equal(f.back()!(), false); assert.deepEqual(f.counts(), before);
    assert.equal(f.counts().subscriptions, 0); assert.equal(f.counts().removedBack, 1); assert.equal(f.counts().removedTop, 1);
  } finally {await f.host.unmount();}
});
