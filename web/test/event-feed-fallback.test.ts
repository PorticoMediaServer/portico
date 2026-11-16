import test from 'node:test';
import assert from 'node:assert/strict';
import {browserEventStream} from '../src/platform/event-feed.ts';
test('a browser without cross-tab sharing still opens the current SSE feed', () => {
 const original = Object.getOwnPropertyDescriptor(globalThis, 'BroadcastChannel');
 try {
  Object.defineProperty(globalThis, 'BroadcastChannel', {value: undefined, configurable: true});
  assert.equal(typeof browserEventStream({} as never, 'viewer', () => 'token'), 'function');
 } finally {
  if (original) Object.defineProperty(globalThis, 'BroadcastChannel', original);else delete (globalThis as Record<string, unknown>).BroadcastChannel;
 }
});
