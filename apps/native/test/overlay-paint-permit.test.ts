/** Authored UNRUN source vectors for presentation watchers only. No bootstrap,
 * runtime cancellation, focus callback or native crash acceptance is claimed. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
const {overlayPaintPermit} = await screenModule(new URL('../src-next/kit/overlays/SurfacePaintHost.tsx', import.meta.url), {
  react: {...React, default: React}, '../theme': {}, '../useMotion': {}, './overlay-paint-model': {},
  './OverlayPaintContext': {},
});

test('an already retired original paint permit subscribes nothing', () => {
  let subscriptions = 0;
  const permit = overlayPaintPermit(() => false, [() => {subscriptions++; throw new Error('native_shell_cancelled');}]);
  assert.doesNotThrow(() => permit.subscribe(() => {}));
  assert.equal(subscriptions, 0);
  assert.equal(permit.current(), false);
});

test('a watcher retirement releases partial subscriptions and permanently refuses only this paint', () => {
  let released = 0, notified = 0, original = true;
  const permit = overlayPaintPermit(() => original, [() => () => {released++;}, () => {throw new Error('native_shell_cancelled');}]);
  const off = permit.subscribe(() => {notified++; assert.equal(permit.current(), false);});
  assert.equal(released, 1);
  assert.equal(notified, 1);
  off(); off(); assert.equal(released, 1);
  original = false; assert.equal(permit.current(), false);
  original = true; assert.equal(permit.current(), false, 'no successor or later accessor can re-arm a failed original watcher');
});

test('one failing unsubscribe cannot prevent the other original watchers from releasing', () => {
  let released = 0;
  const permit = overlayPaintPermit(() => true, [() => () => {throw new Error('native_shell_cancelled');}, () => () => {released++;}]);
  const off = permit.subscribe(() => {});
  assert.doesNotThrow(off); assert.equal(released, 1);
  assert.equal(permit.current(), false);
  off(); assert.equal(released, 1);
});

test('abort is a notification to the original permit and never substitutes a local lifetime rule', () => {
  const local = new AbortController(); let notified = 0;
  const permit = overlayPaintPermit(() => true, [], [local.signal]);
  const off = permit.subscribe(() => {notified++;}); local.abort();
  assert.equal(notified, 1);
  assert.equal(permit.current(), true, 'an original opening.paintCurrent may remain valid after local business abort');
  off();
});
