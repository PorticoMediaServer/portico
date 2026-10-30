import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeRemoteSourcesController} from '../src-next/screens/settings/remote-sources/controller.ts';
import type {NativeSettingsLivePorts} from '../src-next/screens/settings-live/ports';

const source = {id: 'archive', kind: 'webdav', name: 'Archive', rootPath: 'Media', generation: 1,
  state: 'ready', rangeSupport: 'conditional', insecureLocal: false, credentialPresent: true, origin: 'https://storage.example'};
function fixture() {
  let authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'local'};
  const listeners = new Set<() => void>();
  const calls: string[] = [];
  const account = {serverId: 'server', canManage: true, account: {id: 'account', role: 'owner', disabled: false, primaryProfileId: 'primary'}};
  let read = async (_signal?: AbortSignal): Promise<unknown> => ({scope: {serverId: 'server', viewerFence: 'wire'}, sources: [source], mountSupported: true});
  const context = {account, authority: () => authority, current: () => true,
    subscribeAuthority: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};},
    api: {request: async (path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {calls.push(path); return read(signal);}}};
  return {ports: {context, requestId: () => '00000000-0000-4000-8000-000000000000'} as unknown as NativeSettingsLivePorts,
    calls, account, listeners, setRead: (next: typeof read) => {read = next;},
    switchViewer: () => {authority = {...authority, profileId: 'other', epoch: 'two'}; for (const fn of [...listeners]) fn();}};
}

test('remote sources reads the selected primary owner and clears retained metadata on viewer switch', async () => {
  const f = fixture(), controller = new NativeRemoteSourcesController(f.ports);
  assert.equal(controller.getSnapshot().sources.loading, true);
  await controller.load();
  assert.equal(f.calls.length, 1);
  assert.equal(controller.getSnapshot().sources.sources[0]?.name, 'Archive');
  f.switchViewer();
  assert.equal(controller.getSnapshot().allowed, false);
  assert.deepEqual(controller.getSnapshot().sources.sources, []);
  assert.equal(f.listeners.size, 0);
  await controller.load();
  assert.equal(f.calls.length, 1);
});

test('administrator, disabled owner and secondary profile cannot start a remote source request', async () => {
  for (const denied of ['administrator', 'disabled', 'secondary']) {
    const f = fixture();
    if (denied === 'administrator') f.account.account.role = 'admin';
    if (denied === 'disabled') f.account.account.disabled = true;
    if (denied === 'secondary') f.account.account.primaryProfileId = 'another';
    const controller = new NativeRemoteSourcesController(f.ports);
    await controller.load();
    assert.equal(controller.getSnapshot().allowed, false);
    assert.deepEqual(f.calls, []);
    controller.dispose();
  }
});

test('a response completing after authority retirement is aborted and never repopulates the screen', async () => {
  const f = fixture(); let finish: (value: unknown) => void = () => {}; let signal: AbortSignal | undefined;
  f.setRead(next => {signal = next; return new Promise(resolve => {finish = resolve;});});
  const controller = new NativeRemoteSourcesController(f.ports), loading = controller.load();
  f.switchViewer();
  assert.equal(signal?.aborted, true);
  finish({scope: {serverId: 'server', viewerFence: 'wire'}, sources: [source], mountSupported: true});
  await loading;
  assert.equal(controller.getSnapshot().allowed, false);
  assert.deepEqual(controller.getSnapshot().sources.sources, []);
});

test('permission withdrawal during a read discards its response even before an authority event', async () => {
  const f = fixture(); let finish: (value: unknown) => void = () => {};
  f.setRead(() => new Promise(resolve => {finish = resolve;}));
  const controller = new NativeRemoteSourcesController(f.ports), loading = controller.load();
  f.account.account.disabled = true;
  finish({scope: {serverId: 'server', viewerFence: 'wire'}, sources: [source], mountSupported: true});
  await loading;
  assert.equal(controller.getSnapshot().allowed, false);
  assert.deepEqual(controller.getSnapshot().sources.sources, []);
});

const connection = {name: 'Archive', root: 'https://storage.example/Media', username: 'review',
  password: 'test-only-secret', insecureLocal: false};

test('connection testing returns only checked results and never retains credential fields', async () => {
  const f = fixture();
  f.setRead(async () => ({scope: {serverId: 'server', viewerFence: 'wire'},
    result: {origin: 'https://storage.example', rangeSupport: 'conditional'}}));
  const controller = new NativeRemoteSourcesController(f.ports);
  const result = await controller.testConnection(connection);
  assert.deepEqual(result, {origin: 'https://storage.example', rangeSupport: 'conditional'});
  assert.equal(JSON.stringify(controller.getSnapshot()).includes(connection.password), false);
  controller.dispose();
});

test('all command entry points refuse a retired viewer without sending a request', async () => {
  const f = fixture(), controller = new NativeRemoteSourcesController(f.ports);
  f.switchViewer();
  await assert.rejects(controller.testConnection(connection), {name: 'AbortError'});
  await assert.rejects(controller.configure(null, {...connection, keepPassword: false, keepConnection: false}), {name: 'AbortError'});
  await assert.rejects(controller.remove({...source, kind: 'webdav', rangeSupport: 'conditional'}), {name: 'AbortError'});
  await assert.rejects(controller.recover(), {name: 'AbortError'});
  assert.deepEqual(f.calls, []);
});

test('connection test completion after permission withdrawal returns no result or retained credentials', async () => {
  const f = fixture(); let finish: (value: unknown) => void = () => {};
  f.setRead(() => new Promise(resolve => {finish = resolve;}));
  const controller = new NativeRemoteSourcesController(f.ports);
  const testing = controller.testConnection(connection);
  f.account.account.disabled = true;
  finish({scope: {serverId: 'server', viewerFence: 'wire'},
    result: {origin: 'https://storage.example', rangeSupport: 'conditional'}});
  await assert.rejects(testing);
  assert.equal(controller.getSnapshot().allowed, false);
  assert.equal(JSON.stringify(controller.getSnapshot()).includes(connection.password), false);
});


test('a held terminal transport refusal clears loaded metadata and retires all later commands with cached owner flags unchanged', async () => {
  for (const refusal of [{status: 401}, {status: 403}, {statusCode: 401}, {statusCode: 403},
    {code: 'unauthorized'}, {code: 'forbidden'}, {code: 'not_permitted'}]) {
    const f = fixture(), controller = new NativeRemoteSourcesController(f.ports);
    await controller.load();
    assert.equal(controller.getSnapshot().sources.sources[0]?.name, 'Archive');
    let deny: (error: unknown) => void = () => {};
    f.setRead(() => new Promise((_resolve, reject) => {deny = reject;}));
    const reloading = controller.load();
    assert.equal(controller.getSnapshot().sources.sources[0]?.name, 'Archive');
    deny(refusal);
    await reloading;
    assert.equal(f.account.account.disabled, false);
    assert.equal(f.account.canManage, true);
    assert.equal(controller.getSnapshot().allowed, false);
    assert.deepEqual(controller.getSnapshot().sources.sources, []);
    assert.equal(controller.getSnapshot().sources.receipt, null);
    assert.equal(controller.getSnapshot().sources.operationId, null);
    assert.equal(f.listeners.size, 0);
    const calls = f.calls.length;
    await controller.load();
    await assert.rejects(controller.testConnection(connection), {name: 'AbortError'});
    await assert.rejects(controller.configure(null, {...connection, keepPassword: false, keepConnection: false}), {name: 'AbortError'});
    await assert.rejects(controller.remove({...source, kind: 'webdav', rangeSupport: 'conditional'}), {name: 'AbortError'});
    await assert.rejects(controller.recover(), {name: 'AbortError'});
    assert.equal(f.calls.length, calls);
  }
});
