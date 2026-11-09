import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as routes from '../../../packages/contracts/src/routes.ts';

async function fixture() {
  const inventories: any[] = [], posts: any[] = [], selections: string[] = [];
  let response: () => Promise<unknown> = async () => ({id: 'lyrics-job', state: 'queued'});
  const documents = new Map<string, any>();
  class Management {
    selected: any = {phase: 'idle', data: null};
    subscribe = () => () => {}; dispose() {}
    getSnapshot = () => ({directory: {phase: 'ready', data: {items: []}}, selected: this.selected});
    selectLibrary = async (id: string) => {selections.push(id); this.selected = {phase: 'ready', data: {library: {id, lastScan: null}}};};
  }
  class Inventory {
    snapshot: any = {loading: false, error: null, config: null};
    listener = () => {}; disposed = false;
    constructor() {inventories.push(this);}
    subscribe = (listener: () => void) => {this.listener = listener; return () => {};};
    dispose() {this.disposed = true;}
    getSnapshot = () => this.snapshot;
    publish(next: any) {this.snapshot = next; this.listener();}
    refresh = async () => {this.publish({...this.snapshot, loading: false});};
  }
  const core = '../../../../../../packages/client-core/src/';
  const module = await screenModule(new URL('../src-next/screens/settings-live/owner-libraries/controller.ts', import.meta.url), {
    react: {default: {}}, '../../../../../../packages/contracts/src/routes': routes,
    [core + 'library-management']: {LibraryManagementService: Management, fetchEpisodeIssues: async () => ({issues: [], nextCursor: ''})},
    [core + 'library-inventory']: {LibraryInventoryService: Inventory},
    [core + 'remote-sources']: {RemoteSourceService: class {dispose() {}}},
    [core + 'server-admin/libraries']: {readLibrarySettings: async (_: unknown, id: string) => {
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
  return {controller, inventories, posts, selections, documents, respond: (next: typeof response) => {response = next;}};
}

test('library replacement clears inventory immediately and failed inventory reads expose no configuration', async () => {
  const f = await fixture();
  try {
    await f.controller.load('first');
    const first = f.inventories[0], config = {libraryId: 'first', revision: 9, sources: [{id: 'first-source'}]};
    first.publish({config, loading: false, error: null}); assert.equal(f.controller.getSnapshot().inventory, config);
    first.publish({config, loading: true, error: null}); assert.equal(f.controller.getSnapshot().inventory, undefined, 'a refresh does not republish old configuration');
    first.publish({config, loading: false, error: null});
    const loading = f.controller.load('second');
    assert.equal(f.controller.getSnapshot().inventory, undefined);
    assert.equal(first.disposed, true);
    await loading;
    const second = f.inventories[1], error = {code: 'offline'};
    second.publish({config: {...config, libraryId: 'second'}, loading: false, error});
    assert.equal(f.controller.getSnapshot().inventory, undefined); assert.equal(f.controller.getSnapshot().inventoryError, error);
    first.publish({config, loading: false, error: null}); assert.equal(f.controller.getSnapshot().inventory, undefined);
    second.snapshot = {config: {libraryId: 'second', revision: 42, sources: []}, loading: false, error: null};
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
