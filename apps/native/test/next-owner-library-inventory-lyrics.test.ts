import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as routes from '../../../packages/contracts/src/routes.ts';

async function fixture() {
  const posts: any[] = [], selections: string[] = [];
  let response: () => Promise<unknown> = async () => ({id: 'lyrics-job', state: 'queued'});
  const documents = new Map<string, any>();
  class Management {
    selected: any = {phase: 'idle', data: null};
    subscribe = () => () => {}; dispose() {}
    getSnapshot = () => ({directory: {phase: 'ready', data: {items: []}}, selected: this.selected});
    selectLibrary = async (id: string) => {selections.push(id); this.selected = {phase: 'ready', data: {library: {id, lastScan: null}}};};
  }
  let inventoryRead: (id: string) => Promise<unknown> = async id => ({libraryId: id, revision: 1, sources: []});
  const core = '../../../../../../packages/client-core/src/';
  const module = await screenModule(new URL('../src-next/screens/settings-live/owner-libraries/controller.ts', import.meta.url), {
    react: {default: {}}, '../../../../../../packages/contracts/src/routes': routes,
    [core + 'library-management']: {LibraryManagementService: Management, fetchEpisodeIssues: async () => ({issues: [], nextCursor: ''})},
    [core + 'library-inventory']: {},
    [core + 'remote-sources']: {RemoteSourceService: class {dispose() {}}},
    [core + 'server-admin/libraries']: {readLibraryInventoryConfig: (_: unknown, _scope: unknown, id: string) => inventoryRead(id), readLibrarySettings: async (_: unknown, id: string) => {
      const document = {libraryId: id, libraryKind: 'music', settings: {lyricsSource: 'lrclib'}, eligibleControls: ['libraries.lyrics'], sources: [{id: 'forbidden-fallback'}]};
      documents.set(id, document); return document;
    }},
    [core + 'scan-issues']: {}, [core + 'title-identity']: {},
    [core + 'server-admin/metadata-agent']: {metadataAgentPath: (id: string) => id, parseMetadataAgent: () => undefined},
    [core + 'server-admin/library-sonic']: {readLibrarySonic: async () => undefined}, [core + 'presentation/settings-bindings']: {},
  });
  let keys = 0;
  const controller = new module.NativeOwnerLibrariesController({request: async (path: string, method?: string, body?: unknown) => {
    if (method === 'POST') {posts.push({path, body}); return response();} return {};
  }}, {serverId: 'server', viewerId: 'owner'}, () => `key-${++keys}`);
  return {controller, posts, selections, documents, inventory: (read: typeof inventoryRead) => {inventoryRead = read;}, respond: (next: typeof response) => {response = next;}};
}

test('library replacement clears inventory immediately and retires a late folder read', async () => {
  const f = await fixture(); let release!: (value: unknown) => void;
  f.inventory(async id => id === 'first' ? new Promise(resolve => {release = resolve;}) : Promise.reject({code: 'offline'}));
  try {
    const first = f.controller.load('first'); await new Promise(resolve => setImmediate(resolve));
    const second = f.controller.load('second'); assert.equal(f.controller.getSnapshot().inventory, undefined);
    await second; assert.equal(f.controller.getSnapshot().inventoryError.code, 'offline');
    release({libraryId: 'first', revision: 9, sources: []}); await first;
    assert.equal(f.controller.getSnapshot().inventory, undefined);
    f.inventory(async id => ({libraryId: id, revision: 42, sources: []}));
    await f.controller.reloadInventory();
    assert.equal(f.controller.getSnapshot().inventory.revision, 42); assert.equal(f.controller.getSnapshot().inventoryError, undefined);
  } finally {f.controller.dispose();}
});

test('lyrics fetch keeps one idempotent operation, rejects stale openings and reconciles the real job', async () => {
  const f = await fixture();
  try {
    await f.controller.load('music / one'); const document = f.controller.getSnapshot().document;
    let finish: (job: unknown) => void = () => {};
    f.respond(() => new Promise(resolve => {finish = resolve;}));
    const pending = f.controller.fetchLyrics(document, () => true);
    await f.controller.fetchLyrics(document, () => true);
    assert.equal(f.posts.length, 1); assert.equal(f.posts[0].path, '/v1/libraries/music%20%2F%20one/lyrics/fetch');
    finish({id: 'actual-job', state: 'queued'}); await pending;
    assert.equal(f.controller.getSnapshot().lyricsJob.id, 'actual-job'); assert.equal(f.selections.length, 2);
    f.respond(async () => {throw {code: 'offline'};});
    await f.controller.fetchLyrics(document, () => true); assert.ok(f.controller.getSnapshot().lyricsError);
    assert.notDeepEqual(f.posts[0].body, f.posts[1].body, 'a confirmed job ends the previous operation');
    await f.controller.fetchLyrics(document, () => true);
    assert.deepEqual(f.posts[1].body, f.posts[2].body, 'uncertain outcomes never get a new operation key');
    await f.controller.fetchLyrics(document, () => false); assert.equal(f.posts.length, 3);
    await f.controller.load('other');
    await f.controller.fetchLyrics(document, () => true); assert.equal(f.posts.length, 3);
    assert.equal(f.controller.getSnapshot().lyricsJob, undefined);
  } finally {f.controller.dispose();}
});
