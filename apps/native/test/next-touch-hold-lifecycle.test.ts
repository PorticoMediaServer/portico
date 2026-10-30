import assert from 'node:assert/strict';
import {test} from 'node:test';
import type {AppStateStatus, GestureResponderEvent} from 'react-native';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createTouchHold} from '../src-next/kit/browse/touch-hold.ts';

async function fixture(initialState: AppStateStatus = 'active', platform = 'android') {
  const host = hooks(), cleanups: (() => void)[] = [];
  const listeners = new Map<string, Set<(state: AppStateStatus) => void>>();
  const appState = {
    currentState: initialState,
    addEventListener(event: string, listener: (state: AppStateStatus) => void) {
      const handlers = listeners.get(event) ?? new Set();
      handlers.add(listener); listeners.set(event, handlers);
      return {remove: () => handlers.delete(listener)};
    },
  };
  let now = 0, id = 0, fires = 0;
  const tasks = new Map<number, {at: number; run: () => void}>();
  const clock = {
    schedule(run: () => void, delay: number) {tasks.set(++id, {at: now + delay, run}); return id;},
    cancel(ticket: unknown) {tasks.delete(ticket as number);},
  };
  const {useTouchHold} = await screenModule(new URL('../src-next/kit/browse/useTouchHold.ts', import.meta.url), {
    react: {...host.react, useEffect(effect: () => void | (() => void)) {const cleanup = effect(); if (cleanup) cleanups.push(cleanup);}},
    'react-native': {AppState: appState, Platform: {OS: platform}},
    './touch-hold': {createTouchHold: (fire: () => void, phase: Parameters<typeof createTouchHold>[1]) => createTouchHold(fire, phase, clock)},
  });
  const touch = host.render(() => useTouchHold(() => {fires++;}, true));
  return {
    touch, tasks, fires: () => fires,
    start() {touch.handlers.onTouchStart({nativeEvent: {touches: [{}], pageX: 10, pageY: 20}} as GestureResponderEvent);},
    change(state: AppStateStatus) {appState.currentState = state; for (const listener of listeners.get('change') ?? []) listener(state);},
    blur() {for (const listener of listeners.get('blur') ?? []) listener(appState.currentState);},
    advance(ms: number) {now += ms; for (const [ticket, task] of [...tasks]) if (task.at <= now) {tasks.delete(ticket); task.run();}},
    unmount() {for (const cleanup of cleanups) cleanup();},
    listenerCount: () => [...listeners.values()].reduce((count, handlers) => count + handlers.size, 0),
  };
}

test('leaving the foreground retires a pending hold instead of opening a menu on return', async () => {
  for (const state of ['inactive', 'background'] as const) {
    const f = await fixture(); f.start(); f.advance(499); f.change(state);
    assert.equal(f.tasks.size, 0);
    f.change('active'); f.advance(1);
    assert.equal(f.fires(), 0); assert.equal(f.touch.consumePress(), true);
    f.start(); f.advance(500); assert.equal(f.fires(), 1);
    f.unmount();
  }
});

test('a background view cannot begin a hold; a new foreground short tap remains a tap', async () => {
  const f = await fixture('background'); f.start(); f.advance(500);
  assert.equal(f.tasks.size, 0); assert.equal(f.fires(), 0);
  f.change('active'); f.start(); f.advance(499); f.touch.end(); f.advance(1);
  assert.equal(f.fires(), 0); assert.equal(f.touch.consumePress(), false);
  f.unmount();
});

test('losing Android window focus cancels even while AppState is active', async () => {
  const f = await fixture(); f.start(); f.advance(499); f.blur(); f.advance(1);
  assert.equal(f.fires(), 0); assert.equal(f.tasks.size, 0); assert.equal(f.touch.consumePress(), true);
  f.start(); f.advance(500); assert.equal(f.fires(), 1);
  f.unmount();
});

test('iOS and tvOS never register the Android-only blur listener', async () => {
  for (const platform of ['ios', 'tvos'] as const) {
    const f = await fixture('active', platform);
    assert.equal(f.listenerCount(), 1);
    f.unmount();
  }
});

test('unmount removes foreground listeners and retires an already queued timer callback', async () => {
  const f = await fixture(); f.start(); const queued = [...f.tasks.values()][0]!.run;
  assert.equal(f.listenerCount(), 2);
  f.unmount(); queued(); f.change('background');
  assert.equal(f.listenerCount(), 0); assert.equal(f.tasks.size, 0); assert.equal(f.fires(), 0);
});
