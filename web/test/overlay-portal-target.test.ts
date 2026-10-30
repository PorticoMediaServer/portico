import test from 'node:test';
import assert from 'node:assert/strict';
import {overlayPortalTarget, subscribeOverlayPortalTarget} from '../src/kit/overlays/portal-target.ts';

test('fullscreen entry, replacement and exit select the actual current portal host', () => {
  const body = {}, videoAndChrome = {}, replacement = {};
  const owner = {body, fullscreenElement: null as object | null};
  assert.equal(overlayPortalTarget(owner), body);
  owner.fullscreenElement = videoAndChrome;
  assert.equal(overlayPortalTarget(owner), videoAndChrome);
  owner.fullscreenElement = replacement;
  assert.equal(overlayPortalTarget(owner), replacement);
  owner.fullscreenElement = null;
  assert.equal(overlayPortalTarget(owner), body);
});

test('fullscreen notifications are shared only for the subscribed lifetime', () => {
  const document = new EventTarget();
  let first = 0, second = 0;
  const offFirst = subscribeOverlayPortalTarget(() => first++, document);
  const offSecond = subscribeOverlayPortalTarget(() => second++, document);
  document.dispatchEvent(new Event('fullscreenchange'));
  assert.deepEqual([first, second], [1, 1]);
  offFirst();document.dispatchEvent(new Event('fullscreenchange'));
  assert.deepEqual([first, second], [1, 2]);
  offSecond();document.dispatchEvent(new Event('fullscreenchange'));
  assert.deepEqual([first, second], [1, 2]);
});
