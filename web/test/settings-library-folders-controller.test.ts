import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {PreferencesReader} from '@core/preferences-reader.ts';
import {settingsSpec, settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {SettingsController} from '../src/screens/settings/controller.ts';
import type {SettingsLibraryFolderEdit} from '../src/screens/settings/library-folders-source.ts';

const section = settingsSpec.find(item => item.scope === 'server' && item.id === 'libraries')!;
function document(id: string, revision = 41) {
  return {id, revision: String(revision), name: id, kind: 'movies', metadataProvider: 'local', metadataProviders: [{id: 'local', available: true}],
    options: {depth: 'basic', allowDelete: false, watch: true}, folders: [{id: 'folder', path: '/media', available: true}]};
}
function fixture(request: LibraryContentApi['request'], access: Partial<{role: 'owner' | 'admin' | 'member'; manageServer: boolean; mainProfile: boolean}> = {}, requestRaw?: LibraryContentApi['requestRaw']) {
  let current = true;
  const calls: string[] = [];
  const api: LibraryContentApi = {requestRaw, request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {calls.push(path);return request<T>(path, method, body, signal);}};
  const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};
  const reader = new PreferencesReader(api);
  const controller = new SettingsController({api, serverId: 'server', viewerId: 'viewer', role: access.role ?? 'admin', mainProfile: access.mainProfile ?? true,
    capabilities: {manageServer: access.manageServer ?? true, managePeople: false, manageSignIn: false}, preferences, reader, isCurrent: () => current, reportError: error => {throw error;},
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: () => false, library: () => undefined},
    reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
  });
  return {controller, reader, calls, retire: () => {current = false;}};
}
const selected = {section, panelId: 'library', recordId: 'selected'};

test('the controller selects and clears its one folder source with the library panel', async () => {
  const f = fixture(async <T>(path: string) => document(path.endsWith('/other') ? 'other' : 'selected') as T);
  try {
    await f.controller.loadContext(selected);
    assert.deepEqual(f.calls, ['/v1/admin/libraries/selected', '/v1/admin/libraries/selected']);
    assert.equal(f.controller.folders.getSnapshot().libraryId, 'selected');
    assert.equal(f.controller.folders.getSnapshot().config?.revision, 41);
    assert.equal(f.controller.folders.getSnapshot().config?.sources[0].path, '/media');
    await f.controller.loadContext({...selected, recordId: 'other'});
    assert.equal(f.controller.folders.getSnapshot().libraryId, 'other');
    assert.equal(f.controller.folders.getSnapshot().config?.sources[0].libraryId, 'other');
    await f.controller.loadContext({section});
    assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
    assert.deepEqual(f.controller.folders.getSnapshot().config, undefined);
    assert.deepEqual(f.calls.length, 4);
  } finally {f.controller.dispose();f.reader.dispose();}
});

test('a non-library context or a different panel never reads folders', async () => {
  const f = fixture(async <T>(path: string) => document('selected') as T);
  try {
    await f.controller.loadContext({section});
    const dashboard = settingsSpec.find(item => item.scope === 'server' && item.id === 'dashboard')!;
    await f.controller.loadContext({section: dashboard, panelId: 'library', recordId: 'selected'});
    assert.deepEqual(f.calls, []);
    assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
  } finally {f.controller.dispose();f.reader.dispose();}
});

test('disposing or retiring the controller suppresses a held folder read', async () => {
  for (const close of ['dispose', 'retire'] as const) {
    let release!: (value: unknown) => void, signal: AbortSignal | undefined, reads = 0;
    const f = fixture(async <T>(path: string, _method?: string, _body?: unknown, captured?: AbortSignal) => {
      if (++reads === 2) {signal = captured;return new Promise(resolve => {release = resolve;}) as Promise<T>;}
      return document('selected') as T;
    });
    const pending = f.controller.loadContext(selected);
    if (close === 'dispose') f.controller.dispose();
    else f.retire();
    assert.equal(signal?.aborted, close === 'dispose');
    release(document('selected'));
    await pending;
    assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
    assert.equal(f.controller.folders.getSnapshot().libraryId, undefined);
    f.controller.dispose();f.reader.dispose();
  }
});

const edit: SettingsLibraryFolderEdit = {path: '/media/new'};
test('a denied controller gives its folder port no IO or authority', async () => {
  for (const access of [{role: 'member' as const}, {manageServer: false}, {mainProfile: false}]) {
    const f = fixture(async <T>(path: string) => document('selected') as T, access);
    try {
      await f.controller.folders.select('selected');
      await f.controller.folders.refresh();
      assert.equal(await f.controller.folders.save('selected', undefined, edit), false);
      assert.equal(await f.controller.folders.remove('selected', 'folder'), false);
      await f.controller.loadContext(selected);
      assert.deepEqual(f.calls, []);
      assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
      assert.equal(f.controller.folders.config('selected'), undefined);
    } finally {f.controller.dispose();f.reader.dispose();}
  }
});


test('General and folder edits keep the same library revision; an already-open editor retains its original version', async () => {
  let server = document('selected'), writes = 0;
  const f = fixture(async <T>() => server as T, {}, async (_path, _method, options) => {
    assert.deepEqual(options?.headers, {'If-Match': '"' + server.revision + '"'});writes++;
    const patch = options?.body as {options?: typeof server.options; folders?: string[]};
    server = {...server, revision: String(Number(server.revision) + 1), ...(patch.options ? {options: patch.options} : {}),
      ...(patch.folders ? {folders: patch.folders.map(path => ({id: path, path, available: true}))} : {})};
    return {status: 200, headers: {}, body: server};
  });
  const turn = () => new Promise(resolve => setImmediate(resolve));
  try {
    await f.controller.loadContext(selected);
    const watch = settingsRowIndex.get('libraries.watch')!.row;
    f.controller.change(watch, false, selected);await turn();
    assert.equal(f.controller.rowState(watch, selected).value, false);assert.equal(f.controller.folders.config('selected')?.revision, 42);
    assert.equal(await f.controller.folders.save('selected', undefined, {path: '/new'}, 41), false);
    assert.equal(writes, 1, 'an editor opened before the General save cannot overwrite it with a newer revision');
    assert.equal(await f.controller.folders.save('selected', undefined, {path: '/new'}, 42), true);await turn();
    f.controller.change(watch, true, selected);await turn();assert.equal(writes, 3);
    assert.equal(f.controller.rowState(watch, selected).value, true);assert.equal(f.controller.folders.config('selected')?.revision, 44);
  } finally {f.controller.dispose();f.reader.dispose();}
});
