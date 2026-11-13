import assert from 'node:assert/strict';
import test from 'node:test';
import {readOwnerMetadataAgent, writeOwnerMetadataAgent} from '../../packages/client-core/src/owner-library-requests.ts';
import {isMetadataConflict, metadataAgentChange, parseMetadataAgent, saveMetadataAgent} from '../../packages/client-core/src/server-admin/metadata-agent.ts';
import type {LibraryContentApi} from '../../packages/client-core/src/library-content.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tick, tree} from './helpers/interaction-harness.mjs';
import * as model from '../src/screens/settings/libraries/model.ts';

const library = {
  id: 'library / one', kind: 'shows', revision: '7', language: 'fr', region: '', metadataProvider: 'tmdb',
  metadataProviders: [{id: 'tmdb', available: true}, {id: 'tvdb', available: false, reason: "TheTVDB needs the owner's key in Server › Metadata."}, {id: 'local', available: true}],
};

test('metadata reads the admin library and retains the server’s live choices and region presence', async () => {
  const api: LibraryContentApi = {request: async <T,>(path: string) => {
    assert.equal(path, '/v1/admin/libraries/library%20%2F%20one'); return library as T;
  }};
  const doc = parseMetadataAgent(await readOwnerMetadataAgent(api, library.id));
  assert.equal(doc.libraryKind, 'tv'); assert.equal(doc.agent, 'tmdb'); assert.equal(doc.revision, 7);
  assert.equal(doc.language, 'fr'); assert.equal(doc.region, '');
  assert.deepEqual(doc.agents.map(p => [p.id, p.available]), [['tmdb', true], ['tvdb', false], ['local', true]]);
  assert.equal(doc.agents[1].description, library.metadataProviders[1].reason);
  const anime = parseMetadataAgent({...library, kind: 'anime', metadataProvider: 'anilist', metadataProviders: [...library.metadataProviders, {id: 'anilist', available: true}]});
  assert.equal(anime.agent, 'anilist'); assert.equal(anime.agents.at(-1)?.name, 'AniList');
});

test('web and native metadata saves PATCH only the changed fields with the library revision', async () => {
  let writes = 0;
  const api: LibraryContentApi = {request: async () => {throw new Error('No request-only write.');}, requestRaw: async (path, method, options) => {
    writes++; assert.equal(path, '/v1/admin/libraries/library%20%2F%20one'); assert.equal(method, 'PATCH');
    assert.deepEqual(options?.headers, {'If-Match': '"7"'}); assert.equal(options?.singleAttempt, true);
    assert.deepEqual(options?.body, writes === 1 ? {metadataProvider: 'local', region: 'CA'} : {metadataProvider: 'local'});
    return {status: 200, headers: {}, body: {...library, revision: '8', metadataProvider: 'local', region: 'CA'}};
  }};
  const current = parseMetadataAgent(library);
  const web = parseMetadataAgent(await writeOwnerMetadataAgent(api, library.id, metadataAgentChange(current, 'local', 'CA')));
  assert.equal(web.agent, 'local'); assert.equal(web.region, 'CA');
  const native = await saveMetadataAgent(api, current, library.id, 'local'); assert.equal(native.revision, 8);
  assert.equal(writes, 2);
});

test('a stale metadata save is reported once and a transport that drops If-Match cannot write', async () => {
  const current = parseMetadataAgent(library);
  let writes = 0;
  const api: LibraryContentApi = {request: async () => {throw new Error('Unexpected request.');}, requestRaw: async () => {
    writes++;
    return writes === 1 ? {status: 412, headers: {}, body: {error: {code: 'stale_revision', message: 'The library changed.'}}}
      : {status: 400, headers: {}, body: {error: {code: 'invalid_request', message: 'Check the request.', fields: [{path: 'metadataProvider', message: "TheTVDB needs the owner's key in Server › Metadata."}]}}};
  }};
  await assert.rejects(saveMetadataAgent(api, current, library.id, 'local'), error => isMetadataConflict(error));
  assert.equal(writes, 1);
  await assert.rejects(saveMetadataAgent(api, current, library.id, 'tvdb'), /Server › Metadata/);
  await assert.rejects(saveMetadataAgent(api.request, current, library.id, 'local'), /revision safely/);
  assert.equal(writes, 2);
});

test('TheTVDB and AniList libraries keep the menu entry to their metadata refresh actions', async () => {
  for (const [provider, kind, serverKind] of [['tvdb', 'tv', 'shows'], ['anilist', 'anime', 'anime'], ['local', 'tv', 'shows']]) {
    const h = createRenderer(), navigations: unknown[] = [];
    const item = {id: library.id, name: 'Shows', kind, revision: 7, source: {path: '', availability: 'available', basis: 'probe'}, lastScan: null, actions: ['rename', 'scan', 'remove']};
    const service = {subscribe: () => () => {}, getSnapshot: () => ({directory: {phase: 'ready', data: {items: [item]}}, mutation: {phase: 'idle'}}),
      loadLibraries: async () => {}, refreshDirectory: async () => {}, invalidateQuietDirectory: () => {}};
    const api = {request: async () => ({...library, kind: serverKind, metadataProvider: provider, metadataProviders: [{id: provider, available: true}, ...(provider === 'local' ? [] : [{id: 'local', available: true}])]})};
    const app = await componentModule(new URL('../src/screens/settings/libraries/LibrariesPanel.tsx', import.meta.url), {
      react: h.react, '@i18n': {defaultI18n: {t: (key: string) => key}}, './model': model,
      '../../../platform/freshness': {useFreshness: () => ({hub: {}})},
      '../quiet-refresh': {SettingsOpenContext: {value: undefined}, mountQuietInput: () => ({dispose() {}})},
      '../../../kit/controls': {Button: 'Button'}, '../../../kit/overlays/Popover': {Menu: 'Menu'},
      '../../../kit/Icon': {Icon: 'Icon'}, './AddLibrary': {AddLibrary: 'AddLibrary'}, './libraries.module.css': {default: {}},
      '../../../router': {router: {navigate: async (value: unknown) => {navigations.push(value);}}},
    });
    const paint = () => tree(h.render(() => app.LibrariesPanel({api, service, onEdit() {}, onError(error: unknown) {throw error;}})));
    let menu = all(paint(), 'Menu')[0]; menu.props.onOpenChange(true);
    paint(); await tick(); menu = all(paint(), 'Menu')[0];
    const refreshEntry = menu.props.items.find((action: {id: string}) => action.id === 'unmatched');
    assert.equal(!!refreshEntry, provider !== 'local', provider);
    if (refreshEntry) {refreshEntry.onSelect(); await tick(); assert.deepEqual(navigations, [{to: '/library/$libraryId', params: {libraryId: library.id}, search: {unmatched: true}}]);}
  }
});
