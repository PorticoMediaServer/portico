import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as management from '../../../packages/client-core/src/library-management.ts';
import * as libraries from '../../../packages/client-core/src/server-admin/libraries.ts';
import * as metadata from '../../../packages/client-core/src/server-admin/metadata-agent.ts';
import * as bindings from '../../../packages/client-core/src/presentation/settings-bindings.ts';
import * as versions from '../../../packages/client-core/src/settings-document.ts';
import * as routes from '../../../packages/contracts/src/routes.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';

async function fixture() {
  const reads: string[] = [], writes: any[] = [];
  let library = {id: 'films', name: 'Films', kind: 'movies', revision: '7', language: 'en', region: 'GB', metadataProvider: 'tmdb', scanning: false,
    metadataProviders: [{id: 'tmdb', available: true}, {id: 'local', available: true}],
    folders: [{id: 'folder-1', path: '/media/films', available: true, watched: true, scanEveryMinutes: 0}],
    options: {depth: 'basic', watch: true, allowDelete: false, groupSeries: true, rescanMinutes: 0, trash: {mode: 'days', days: 30}, segmentDetection: 'server'}};
  const api: LibraryContentApi = {
    request: async <T,>(path: string): Promise<T> => {
      reads.push(path);
      if (path.includes('episode-issues')) return {issues: [], nextCursor: ''} as T;
      if (path.split('?')[0] === '/v1/admin/libraries') return {items: [library]} as T;
      assert.equal(path, '/v1/admin/libraries/films'); return structuredClone(library) as T;
    },
    requestRaw: async (path, method, options) => {
      writes.push({path, method, ...options});
      assert.equal(path, '/v1/admin/libraries/films'); assert.equal(method, 'PATCH');
      if (options?.headers?.['If-Match'] !== `"${library.revision}"`) return {status: 412, headers: {}, body: {error: {code: 'changed_elsewhere', message: 'The library changed elsewhere.'}}};
      const edit = options?.body as any;
      library = {...library, ...edit, revision: String(Number(library.revision) + 1)};
      return {status: 200, headers: {}, body: structuredClone(library)};
    },
  };
  const core = '../../../../../../packages/client-core/src/';
  const module = await screenModule(new URL('../src-next/screens/settings-live/owner-libraries/controller.ts', import.meta.url), {
    react: {default: {}}, '../../../../../../packages/contracts/src/routes': routes,
    [core + 'library-management']: management, [core + 'server-admin/libraries']: libraries,
    [core + 'server-admin/metadata-agent']: metadata, [core + 'settings-document']: versions,
    [core + 'remote-sources']: {RemoteSourceService: class {dispose() {}}}, [core + 'scan-issues']: {}, [core + 'title-identity']: {},
    [core + 'server-admin/library-sonic']: {}, [core + 'presentation/settings-bindings']: bindings,
  });
  const controller = new module.NativeOwnerLibrariesController(api, {serverId: 'server', viewerId: 'owner'}, () => 'operation');
  return {controller, reads, writes, changeElsewhere: () => {library = {...library, revision: '8', options: {...library.options, allowDelete: true}};}};
}

test('the real library readers open the phone page and save only working fields with If-Match', async () => {
  const f = await fixture();
  try {
    await f.controller.load('films'); const state = f.controller.getSnapshot(), document = state.document;
    assert.equal(state.detail, 'ready'); assert.equal(state.inventoryError, undefined);
    assert.equal(state.inventory.sources[0].path, '/media/films');
    assert.equal(bindings.readLibrarySettingsBinding('libraries.scanDepth', document)?.current, 'basic');
    for (const row of ['libraries.cwWeeks', 'libraries.localTags', 'libraries.analysisSteps', 'libraries.scrub', 'libraries.episodeThumbs']) {
      assert.equal(bindings.readLibrarySettingsBinding(row, document), undefined, row);
    }
    assert.equal(await f.controller.save({monitorChanges: false}), true);
    assert.equal(f.writes.length, 1); assert.deepEqual(f.writes[0].headers, {'If-Match': '"7"'});
    assert.equal(f.writes[0].singleAttempt, true);
    assert.deepEqual(f.writes[0].body, {options: {...document.serverLibrary.options, watch: false}});
    assert.equal(f.controller.getSnapshot().document.revision, 8);
    assert.equal(f.controller.getSnapshot().metadata.revision, 8);
    assert.equal(f.controller.getSnapshot().inventory.revision, 8);
    assert.ok(f.reads.every(path => !/\/settings|\/inventory/.test(path)));
  } finally {f.controller.dispose();}
});

test('a refused save uses the shared conflict helper, reads the new value, and never retries the stale change', async () => {
  const f = await fixture();
  try {
    await f.controller.load('films'); f.changeElsewhere();
    assert.equal(await f.controller.save({monitorChanges: false}), false);
    const state = f.controller.getSnapshot();
    assert.equal(versions.settingsSaveConflict(state.error), true); assert.equal(state.document.revision, 8);
    assert.equal(state.document.settings.allowMediaDeletion, true); assert.equal(state.document.settings.monitorChanges, true);
    assert.equal(f.writes.length, 1);
  } finally {f.controller.dispose();}
});
