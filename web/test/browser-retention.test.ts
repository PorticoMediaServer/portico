import test from 'node:test';
import assert from 'node:assert/strict';
import {browserFeedEnvironment} from '../src/platform/event-feed.ts';

test('WEB002 browser feed forwards optional real retention unchanged and preserves omitted defaults', t => {
  const before = Object.getOwnPropertyDescriptor(globalThis, 'document');
  const page = new EventTarget() as EventTarget & {visibilityState: string};page.visibilityState = 'hidden';
  Object.defineProperty(globalThis, 'document', {configurable: true, value: page});
  t.after(() => {if (before) Object.defineProperty(globalThis, 'document', before);else delete (globalThis as Record<string, unknown>).document;});
  let retained = false;
  const listeners = new Set<() => void>(), busy = () => false, onBusy = () => () => {};
  const retain = () => retained, onRetain = (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};};
  const environment = browserFeedEnvironment(busy, onBusy, retain, onRetain) as ReturnType<typeof browserFeedEnvironment> & {retain(): boolean;onRetain(listener: () => void): () => void};
  assert.equal(environment.visible(), false);assert.equal(environment.busy, busy);assert.equal(environment.onBusy, onBusy);
  assert.equal(environment.retain, retain);assert.equal(environment.onRetain, onRetain);assert.equal(environment.graceMs, 30_000);
  let heard = 0;const off = environment.onRetain(() => heard++);retained = true;for (const listener of listeners) listener();
  assert.equal(environment.retain(), true);assert.equal(environment.busy!(), false);assert.equal(heard, 1);off();assert.equal(listeners.size, 0);
  const legacy = browserFeedEnvironment(busy, onBusy);
  assert.equal('retain' in legacy, false);assert.equal('onRetain' in legacy, false);assert.equal(legacy.graceMs, 30_000);
});
