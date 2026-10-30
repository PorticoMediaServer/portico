import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryAdminDocument, LibrarySettings} from '@core/administration.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {PreferencesReader} from '@core/preferences-reader.ts';
import {settingsSpec} from '@core/presentation/settings-spec.ts';
import {SettingsController} from '../src/screens/settings/controller.ts';
import type {SettingsLibraryFolderEdit} from '../src/screens/settings/library-folders-source.ts';

const section = settingsSpec.find(item => item.scope === 'server' && item.id === 'libraries')!;
const settings: LibrarySettings = {scanDepth: 'basic', groupCollections: true, localTags: 'prefer', lyricsSource: 'files', titleSource: 'tags', episodeNumbering: 'absolute', findExtras: true, sortIgnoreArticles: true, refreshMode: 'replace_unlocked', acoustidEnabled: false,
  continueWatching: {weeks: 16, maximumItems: 40, includeSeasonPremieres: true, videoPlayedThreshold: 95, videoCompletion: 'earliest'}, allowMediaDeletion: false, monitorChanges: true, providers: [], analysis: ['probe', 'local_metadata', 'subtitle_discovery'],
  navigation: {trickplayIntervalMs: 10000, trickplayTileWidth: 320, trickplayMaxTiles: 1080, chapterThumbnailMode: 'embedded', videoPreviewEnabled: false, videoPreviewMs: 30000}, segmentDetection: 'server'};
function document(libraryId: string, revision = 7): LibraryAdminDocument {
  return {libraryId, libraryName: libraryId, libraryKind: 'anime', revision, digest: 'actual-document', eligibleControls: ['libraries.cwWeeks'], audience: {everyone: false, accounts: 3}, actions: ['scan'],
    settings, defaults: {...settings, continueWatching: {...settings.continueWatching, weeks: 8}}, analysisMatrix: {costClasses: [], operations: []}, availableProviders: [], enumerations: {}, sources: []};
}
function inventory(libraryId: string, revision = 41) {
  return {scope: {serverId: 'server', viewerFence: 'fence-1'}, libraryId, revision,
    sources: [{id: 'folder', libraryId, kind: 'local', name: 'Folder', path: '/media', resolvedPath: '/media', classification: 'local', generation: 1, health: 'healthy', enabled: true, followSymlinks: false, identityConfirmed: true, intervalMs: 3600000, nextScanAt: '', lastCompleteAt: '', lastProgressAt: ''}],
    policy: {tier: 'basic', revision: 1, operations: []}};
}
function fixture(request: LibraryContentApi['request'], access: Partial<{role: 'owner' | 'admin' | 'member'; manageServer: boolean; mainProfile: boolean}> = {}) {
  let current = true;
  const calls: string[] = [];
  const api: LibraryContentApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {calls.push(path);return request<T>(path, method, body, signal);}};
  const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};
  const reader = new PreferencesReader(api, {serverId: 'server', viewerId: 'viewer'});
  const controller = new SettingsController({api, serverId: 'server', viewerId: 'viewer', role: access.role ?? 'admin', mainProfile: access.mainProfile ?? true,
    capabilities: {manageServer: access.manageServer ?? true, managePeople: false, manageSignIn: false}, preferences, reader, isCurrent: () => current, reportError: error => {throw error;},
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: () => false, library: () => undefined},
    reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
  });
  return {controller, reader, calls, retire: () => {current = false;}};
}
const selected = {section, panelId: 'library', recordId: 'selected'};

test('the controller selects and clears its one folder source with the library panel', async () => {
  const f = fixture(async <T>(path: string) => (path.endsWith('/inventory-config') ? inventory(path.includes('/other/') ? 'other' : 'selected') : document(path.includes('/other/') ? 'other' : 'selected')) as T);
  try {
    await f.controller.loadContext(selected);
    assert.deepEqual(f.calls, ['/v1/admin/libraries/selected/settings', '/v1/admin/libraries/selected/inventory-config']);
    assert.equal(f.controller.folders.getSnapshot().libraryId, 'selected');
    assert.equal(f.controller.folders.getSnapshot().config?.revision, 41);
    assert.equal(f.controller.folders.getSnapshot().config?.sources[0].path, '/media');
    await f.controller.loadContext({...selected, recordId: 'other'});
    assert.equal(f.controller.folders.getSnapshot().libraryId, 'other');
    assert.equal(f.controller.folders.getSnapshot().config?.sources[0].libraryId, 'other');
    await f.controller.loadContext({section});
    assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
    assert.deepEqual(f.controller.folders.getSnapshot().config, undefined);
    assert.deepEqual(f.calls.filter(path => path.endsWith('/inventory-config')).length, 2);
  } finally {f.controller.dispose();f.reader.dispose();}
});

test('a non-library context or a different panel never reads folders', async () => {
  const f = fixture(async <T>(path: string) => (path.endsWith('/inventory-config') ? inventory('selected') : document('selected')) as T);
  try {
    await f.controller.loadContext({section});
    const dashboard = settingsSpec.find(item => item.scope === 'server' && item.id === 'dashboard')!;
    await f.controller.loadContext({section: dashboard, panelId: 'library', recordId: 'selected'});
    assert.deepEqual(f.calls.filter(path => path.endsWith('/inventory-config')), []);
    assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
  } finally {f.controller.dispose();f.reader.dispose();}
});

test('disposing or retiring the controller suppresses a held folder read', async () => {
  for (const close of ['dispose', 'retire'] as const) {
    let release!: (value: unknown) => void, signal: AbortSignal | undefined;
    const f = fixture(async <T>(path: string, _method?: string, _body?: unknown, captured?: AbortSignal) => {
      if (path.endsWith('/inventory-config')) {signal = captured;return new Promise(resolve => {release = resolve;}) as Promise<T>;}
      return document('selected') as T;
    });
    const pending = f.controller.loadContext(selected);
    if (close === 'dispose') f.controller.dispose();
    else f.retire();
    assert.equal(signal?.aborted, close === 'dispose');
    release(inventory('selected'));
    await pending;
    assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
    assert.equal(f.controller.folders.getSnapshot().libraryId, undefined);
    f.controller.dispose();f.reader.dispose();
  }
});

const edit: SettingsLibraryFolderEdit = {name: 'Folder', path: '/media/new', classification: 'local', followSymlinks: false, intervalSeconds: 900, acceptReplacement: false};
test('a denied controller gives its folder port no IO or authority', async () => {
  for (const access of [{role: 'member' as const}, {manageServer: false}, {mainProfile: false}]) {
    const f = fixture(async <T>(path: string) => (path.endsWith('/inventory-config') ? inventory('selected') : document('selected')) as T, access);
    try {
      await f.controller.folders.select('selected');
      await f.controller.folders.refresh();
      assert.equal(await f.controller.folders.save('selected', undefined, edit), false);
      assert.equal(await f.controller.folders.remove('selected', 'folder'), false);
      await f.controller.loadContext(selected);
      assert.deepEqual(f.calls.filter(path => path.endsWith('/inventory-config')), []);
      assert.deepEqual(f.controller.folders.getSnapshot(), {loading: false, mutation: 'idle'});
      assert.equal(f.controller.folders.config('selected'), undefined);
    } finally {f.controller.dispose();f.reader.dispose();}
  }
});
