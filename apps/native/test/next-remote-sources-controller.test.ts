import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeRemoteSourcesController} from '../src-next/screens/settings/remote-sources/controller.ts';
import type {NativeSettingsLivePorts} from '../src-next/screens/settings-live/ports';

const source = {id: 'rs_1', kind: 'webdav', name: 'Archive', preset: 'read-once', limits: {connections: 4, perSecond: 4}, webdav: {address: 'https://storage.example', passwordSet: true}, state: 'setup', folders: 0};
function fixture() {
  let authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'local'};
  const listeners = new Set<() => void>();
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const account = {serverId: 'server', canManage: true, account: {id: 'account', role: 'owner', disabled: false, primaryProfileId: 'primary'}};
  const context = {account, authority: () => authority, current: () => true,
    subscribeAuthority: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};},
    api: {request: async (path: string, method?: string, body?: unknown) => {calls.push({path, method, body}); return path === '/v1/admin/remote-sources' && method !== 'POST' ? {items: [source]} : source;}}};
  return {ports: {context, requestId: () => 'id'} as unknown as NativeSettingsLivePorts, calls, account, listeners,
    switchViewer: () => {authority = {...authority, profileId: 'other', epoch: 'two'}; for (const fn of [...listeners]) fn();}};
}

test('the owner reads the sources from the new server, and a viewer switch clears them and stops reading (rule 7)', async () => {
  const f = fixture(), controller = new NativeRemoteSourcesController(f.ports);
  await controller.load();
  assert.deepEqual(f.calls.map(one => one.path), ['/v1/admin/remote-sources']);
  assert.equal(controller.getSnapshot().sources[0]?.state, 'setup');
  f.switchViewer();
  assert.equal(controller.getSnapshot().allowed, false);
  assert.deepEqual(controller.getSnapshot().sources, []);
  await controller.load();
  assert.equal(f.calls.length, 1);
});

test('an administrator, a disabled owner and another profile reach nothing (rule 7)', async () => {
  for (const denied of ['administrator', 'disabled', 'secondary']) {
    const f = fixture();
    if (denied === 'administrator') f.account.account.role = 'admin';
    if (denied === 'disabled') f.account.account.disabled = true;
    if (denied === 'secondary') f.account.account.primaryProfileId = 'someone-else';
    const controller = new NativeRemoteSourcesController(f.ports);
    await controller.load();
    await assert.rejects(controller.add({kind: 'webdav', name: 'x', webdav: {address: 'https://x'}}));
    assert.equal(f.calls.length, 0, denied);
  }
});

test('adding is one call that tests and saves, and the two steps the server does not have yet send nothing', async () => {
  const f = fixture(), controller = new NativeRemoteSourcesController(f.ports);
  await controller.add({kind: 'webdav', name: 'Archive', preset: 'read-once', webdav: {address: 'https://storage.example'}});
  assert.deepEqual(f.calls[0], {path: '/v1/admin/remote-sources', method: 'POST', body: {kind: 'webdav', name: 'Archive', preset: 'read-once', webdav: {address: 'https://storage.example'}}});
  const before = f.calls.length;
  await assert.rejects(controller.addCloudToken({name: 'OneDrive', provider: 'onedrive', token: '{}', path: '', preset: 'read-once'}), {code: 'not_on_server'});
  await assert.rejects(controller.addRcloneFromFields({name: 'Bucket', type: 's3', options: {}, path: '', preset: 'read-once'}), {code: 'not_on_server'});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.calls.filter(one => one.method === 'POST').length, 1);
  assert.ok(f.calls.length >= before);
});
