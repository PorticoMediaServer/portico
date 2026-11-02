import test from 'node:test';
import assert from 'node:assert/strict';
import {notificationRoute} from '../src-next/screens/notifications/notification-route.ts';

test('J-32: "New device signed in" opens Settings for its security and device actions, never a dead button', () => {
  assert.deepEqual(notificationRoute({kind: 'navigate', label: 'Review security', target: {view: 'settings-security'}}, false), {kind: 'shell', destination: {kind: 'settings'}});
  assert.deepEqual(notificationRoute({kind: 'command', label: 'Review device', command: 'review-device', arguments: {deviceId: 'd1'}}, false), {kind: 'shell', destination: {kind: 'settings'}});
  assert.equal(notificationRoute({kind: 'command', label: 'Review device', command: 'review-device', arguments: {}}, false), undefined, 'no device, no action');
});

test('the administrators\' copy opens the server\'s people, where devices are listed', () => {
  assert.deepEqual(notificationRoute({kind: 'navigate', label: 'Review devices', target: {view: 'admin-devices'}}, false), {kind: 'server-settings', page: 'people'});
});

test('existing destinations are unchanged and an unknown view stays out', () => {
  assert.deepEqual(notificationRoute({kind: 'command', label: 'Open', command: 'open-download'}, false), {kind: 'downloads'});
  assert.equal(notificationRoute({kind: 'navigate', label: 'Live', target: {view: 'live'}}, false), undefined);
  assert.deepEqual(notificationRoute({kind: 'navigate', label: 'Live', target: {view: 'live'}}, true), {kind: 'shell', destination: {kind: 'live'}});
  assert.equal(notificationRoute({kind: 'navigate', label: 'Somewhere', target: {view: 'not-a-view'}}, false), undefined);
});
