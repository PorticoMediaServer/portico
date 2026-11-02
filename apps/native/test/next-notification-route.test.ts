import test from 'node:test';
import assert from 'node:assert/strict';
import {notificationRoute} from '../src-next/screens/notifications/notification-route.ts';

test('J-32: "New device signed in" opens Profile & account (sign-in and devices) for its security and device actions, never a dead button', () => {
  assert.deepEqual(notificationRoute({kind: 'navigate', label: 'Review security', target: {view: 'settings-security'}}, false), {kind: 'shell', destination: {kind: 'settings', page: 'profile'}});
  assert.deepEqual(notificationRoute({kind: 'command', label: 'Review device', command: 'review-device', arguments: {deviceId: 'd1'}}, false), {kind: 'shell', destination: {kind: 'settings', page: 'profile'}});
  assert.equal(notificationRoute({kind: 'command', label: 'Review device', command: 'review-device', arguments: {}}, false), undefined, 'no device, no action');
});

test('the administrators\' copy opens the server\'s people, where devices are listed', () => {
  assert.deepEqual(notificationRoute({kind: 'navigate', label: 'Review devices', target: {view: 'admin-devices'}}, false), {kind: 'server-settings', page: 'people'});
});

test('existing destinations are unchanged and an unknown view stays out', () => {
  assert.deepEqual(notificationRoute({kind: 'command', label: 'Open', command: 'open-download', arguments: {downloadId: 'dl-1'}}, false), {kind: 'downloads'});
  assert.equal(notificationRoute({kind: 'command', label: 'Open', command: 'open-download'}, false), undefined, 'a command reads only its own argument');
  assert.equal(notificationRoute({kind: 'navigate', label: 'Live', target: {view: 'live'}}, false), undefined);
  assert.deepEqual(notificationRoute({kind: 'navigate', label: 'Live', target: {view: 'live'}}, true), {kind: 'shell', destination: {kind: 'live'}});
  assert.equal(notificationRoute({kind: 'navigate', label: 'Somewhere', target: {view: 'not-a-view'}}, false), undefined);
});

// OW-1: every action the server's producers publish does something on the phone, tablet and TV.
test('the published commands and views open a real surface or run in place', async () => {
  const {notificationCommand} = await import('../src-next/screens/notifications/notification-route.ts');
  const nav = (view: string, extra: Record<string, string> = {}) => ({kind: 'navigate' as const, label: view, target: {view, ...extra}});
  const cmd = (command: string, args: Record<string, string>) => ({kind: 'command' as const, label: command, command, arguments: args});
  assert.deepEqual(notificationRoute(nav('settings-network'), false), {kind: 'server-settings', page: 'remote'});
  assert.deepEqual(notificationRoute(nav('settings-storage'), false), {kind: 'server-settings', page: 'storage'});
  assert.deepEqual(notificationRoute(nav('recordings', {entityId: 'rec-1'}), false), {kind: 'recordings'});
  assert.deepEqual(notificationRoute(nav('admin-feedback', {entityId: 'rep-1'}), false), {kind: 'server-feedback'});
  assert.deepEqual(notificationRoute(cmd('retry-job', {downloadId: 'dl-1'}), false), {kind: 'downloads'});
  assert.deepEqual(notificationCommand(cmd('dismiss-conflict', {recordingId: 'rec-1'})), {kind: 'archive'});
  assert.deepEqual(notificationCommand(cmd('run-scan', {libraryId: 'lib-1'})), {kind: 'scan', libraryId: 'lib-1'});
  assert.equal(notificationCommand(cmd('run-scan', {})), undefined, 'no library, no scan');
  assert.equal(notificationRoute(cmd('dismiss-conflict', {recordingId: 'rec-1'}), false), undefined, 'a command is not a route');
});
