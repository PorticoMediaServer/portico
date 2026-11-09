import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession, type OutboxEntry} from '../src/index.ts';
import {routes} from '../../contracts/src/routes.ts';
import {cachedLibraries, cachedTitle, readWriteAnswer, writeDependencies, writeRoute, writeRouteIds, type ConfirmedWrite, type WriteAnswer} from '../src/query/write-dependencies.ts';
import {ShowWorkspaceService} from '../src/show-workspace.ts';
import type {PutItemsIdPersonalStateResponse, PutItemsPersonalStateBatchResponse, SetContainerPersonalStateResponse} from '../../contracts/src/types.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries} from '../src/query/viewer-queries.ts';
import {queryKeys} from '../src/query/keys.ts';

// Client data step 7: every write the server confirms on this device moves what it changed in the
// viewer's cache, whoever made it, through one hook in the API's transport.
const nothingCached = {title: () => undefined, libraries: () => []};
const deps = (route: ConfirmedWrite['route'], params: Record<string, string>, answer?: WriteAnswer, context: Parameters<typeof writeDependencies>[1] = nothingCached) =>
  writeDependencies({route, params, ...(answer ? {answer} : {})}, context);

// The server's answers, as generated from its contract (types.ts): only the item route names a library.
const personal = {conflicts: [], favorite: false, lastPlayedAt: '', progressMs: 0, rating: null, revision: 4, status: 'watched', watched: true, watchlisted: false};
const itemAnswer = (itemId: string, libraryId: string) => ({current: personal, itemId, libraryId, operationId: 'op', personal, receiptLifetimeMs: 60000, serverId: 'server', viewerFence: 'f'}) satisfies PutItemsIdPersonalStateResponse;
const batchAnswer = (ok: readonly string[], failed: readonly string[] = []) => ({failed: failed.length, operationId: 'op', receiptLifetimeMs: 60000, serverId: 'server', updated: ok.length, viewerFence: 'f',
  results: [...ok.map(itemId => ({itemId, ok: true, personal})), ...failed.map(itemId => ({itemId, ok: false, code: 'conflict', message: 'changed'}))]}) satisfies PutItemsPersonalStateBatchResponse;
const containerAnswer = {favorite: false, revision: 2, watched: true, watchlisted: false, watermark: 'w', undoToken: 'u', undoUntil: '2099-01-01T00:00:00Z'} satisfies SetContainerPersonalStateResponse;

test('every write the table names is a generated route, and is found from its path', () => {
  for (const id of writeRouteIds) {
    const route = routes.find(r => r.id === id && r.method !== 'GET');
    assert.ok(route, id);
    const path = route.path.replace(/\{([^}]+)\}/g, (_, name: string) => 'v-' + name);
    const found = writeRoute(path, route.method);
    assert.equal(found?.route, id, path);
    for (const name of route.path.match(/\{([^}]+)\}/g) ?? []) assert.equal(found?.params[name.slice(1, -1)], 'v-' + name.slice(1, -1));
  }
});

test('a write is found with its parameters decoded; a read, an unnamed write or another origin is not', () => {
  assert.deepEqual(writeRoute('/v1/items/a%2Fb/personal-state', 'PUT')?.params, {id: 'a/b'});
  assert.equal(writeRoute('/v1/items/personal-state:batch', 'PUT')?.route, 'put_items_personal_state_batch');
  assert.equal(writeRoute('/v1/items/one/personal-state?x=1', 'PUT')?.route, 'put_items_id_personal_state');
  assert.equal(writeRoute('/v1/items/one/personal-state', 'GET'), undefined);
  assert.equal(writeRoute('/v1/jobs', 'POST'), undefined, 'a job is the hub\'s: its effect lands later');
  assert.equal(writeRoute('https://elsewhere/v1/items/one/personal-state', 'PUT'), undefined);
  assert.equal(writeRoute('/v1/items/one/personal-state', 'PUT')?.readsAnswer, true);
  assert.equal(writeRoute('/v1/items/personal-state:batch', 'PUT')?.readsAnswer, true);
  assert.equal(writeRoute('/v1/containers/show/s/personal-state', 'PUT')?.readsAnswer, false, 'a container\'s answer names no library or title');
  assert.equal(writeRoute('/v1/preferences', 'PATCH')?.readsAnswer, false);
});

test('only the library and the changed titles are read from an answer', () => {
  assert.deepEqual(readWriteAnswer('put_items_id_personal_state', itemAnswer('ep', 'L')), {libraryId: 'L'});
  assert.deepEqual(readWriteAnswer('put_items_personal_state_batch', batchAnswer(['a', 'b'], ['c'])), {itemIds: ['a', 'b']}, 'a title the batch refused did not change');
  assert.equal(readWriteAnswer('set_container_personal_state', containerAnswer), undefined);
  assert.equal(readWriteAnswer('put_items_personal_state_batch', {results: 'many'}), undefined);
  assert.equal(readWriteAnswer('patch_preferences', {libraryId: 'L'}), undefined, 'a route that reads no answer names nothing');
});

test('a title\'s state: its library from the answer, else from a page the cache holds, else every library', () => {
  assert.deepEqual([...deps('put_items_id_personal_state', {id: 'ep'}, readWriteAnswer('put_items_id_personal_state', itemAnswer('ep', 'L')))].sort(), ['personal:*', 'personal:L', 'personal:title:ep']);
  const cached = {title: (id: string) => id === 'ep' ? {libraryId: 'L', titleIds: ['show', 'season']} : undefined, libraries: () => ['L']};
  assert.deepEqual([...deps('put_items_itemId_reading_mark', {itemId: 'ep'}, undefined, cached)].sort(), ['personal:*', 'personal:L', 'personal:title:ep', 'personal:title:season', 'personal:title:show'],
    'an episode marked anywhere moves its show and season pages too');
  const unknown = deps('put_items_itemId_reading_favorite', {itemId: 'x'}, undefined, {title: () => undefined, libraries: () => ['L']});
  assert.deepEqual([...unknown].sort(), ['personal:*', 'personal:title:x']);
  assert.ok(!unknown.some(d => d.startsWith('personal:any:')), 'a write about one title never moves every title page');
});

test('a batch moves each title it changed in its own library; a container moves every page of its library', () => {
  const held = {title: (id: string) => ({a: {libraryId: 'L1', titleIds: []}, b: {libraryId: 'L2', titleIds: ['show']}} as Record<string, {libraryId: string; titleIds: string[]}>)[id], libraries: () => ['L1', 'L2']};
  const batch = deps('put_items_personal_state_batch', {}, readWriteAnswer('put_items_personal_state_batch', batchAnswer(['a', 'b'], ['c'])), held);
  for (const d of ['personal:L1', 'personal:L2', 'personal:*', 'personal:title:a', 'personal:title:b', 'personal:title:show']) assert.ok(batch.includes(d), d);
  assert.ok(!batch.includes('personal:title:c'), 'a refused title did not change');
  const unread = deps('put_items_personal_state_batch', {}, undefined, held);
  for (const d of ['personal:*', 'personal:L1', 'personal:L2', 'personal:any:L1', 'personal:any:L2']) assert.ok(unread.includes(d), 'an answer not read moves every held library: ' + d);
  const container = deps('set_container_personal_state', {kind: 'season', id: 'season'}, readWriteAnswer('set_container_personal_state', containerAnswer), {title: id => id === 'season' ? {libraryId: 'L', titleIds: ['show']} : undefined, libraries: () => ['L', 'M']});
  assert.deepEqual([...container].sort(), ['personal:*', 'personal:L', 'personal:any:L', 'personal:title:season', 'personal:title:show']);
  const elsewhere = deps('undo_container_personal_state', {kind: 'show', id: 'show'}, undefined, {title: () => undefined, libraries: () => ['L', 'M']});
  for (const d of ['personal:*', 'personal:title:show', 'personal:L', 'personal:any:L', 'personal:M', 'personal:any:M']) assert.ok(elsewhere.includes(d), 'a container of a library not known here moves every held library: ' + d);
  assert.deepEqual([...deps('post_libraries_libraryId_reading_mark_all', {libraryId: 'L'})].sort(), ['personal:*', 'personal:L', 'personal:any:*', 'personal:any:L']);
});

test('Saved, pins, history, preferences and the owner\'s libraries move what shows them', () => {
  assert.ok(deps('put_saved_pins_kind_id', {kind: 'k', id: 'i'}).includes('personal:*'));
  assert.ok(deps('post_personal_history_actions', {}).includes('progress:*'));
  assert.ok(deps('post_me_recommendations_reset', {}).includes('event:list.changed:recommendations'));
  assert.deepEqual(deps('patch_preferences', {}), ['settings:preferences']);
  assert.deepEqual(deps('patch_admin_libraries_id', {id: 'L'}), ['directory', 'settings:server']);
  assert.ok(deps('post_library_channels_preferences', {}).includes('event:list.changed:libraryChannels'));
});

// ── The transport ────────────────────────────────────────────────────────────

class Store {
  entries = new Map<string, OutboxEntry>();
  async list() { return [...this.entries.values()]; }
  async put(entry: OutboxEntry) { this.entries.set(entry.id, entry); }
  async delete(id: string) { this.entries.delete(id); }
}
const session = {accessToken: 'token', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z', tokenGeneration: '1', sessionFamilyId: 'family', deviceId: 'device',
  viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}} as unknown as LocalSession;
function transport(options: {queued?: boolean; status?: number} = {}) {
  const store = new Store();
  const fetcher = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input)), method = init?.method ?? 'GET', headers = new Headers(init?.headers);
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined;
    if (method === 'GET') return Response.json({scope: {libraryId: 'L'}, personal: {revision: 3}});
    if (options.status && options.status >= 400) return Response.json({error: {code: 'refused'}}, {status: options.status});
    if (url.pathname.endsWith('/reading/mark')) return Response.json({operationId: body?.operationId, revision: 4});
    if (url.pathname.startsWith('/v1/containers/')) return Response.json(containerAnswer);
    if (url.pathname === '/v1/items/personal-state:batch') return Response.json(batchAnswer(['ep1'], ['ep2']));
    if (url.pathname.endsWith('/personal-state')) return Response.json({...itemAnswer(url.pathname.split('/')[3], 'L'), operationId: body?.operationId});
    return Response.json({ok: true});
  };
  const api = new HttpLocalApi('http://server.test', session.accessToken, fetcher);
  if (options.queued) { api.setOutboxSession(session); api.configureOutbox(store); }
  const heard: ConfirmedWrite[] = [];
  const stop = api.onConfirmedWrite(write => heard.push(write));
  return {api, heard, stop, dispose: () => { api.getOutbox()?.dispose(); }};
}
const settle = () => new Promise(resolve => setTimeout(resolve, 5));

test('the transport tells of a confirmed write once, with its answer; the writer reads its own answer untouched', async () => {
  const t = transport();
  try {
    const value = await t.api.request<{libraryId: string}>('/v1/items/one/personal-state', 'PUT', {operationId: '00000000-0000-4000-8000-000000000000', expectedRevision: 3, watched: true});
    assert.equal(value.libraryId, 'L', 'the writer\'s answer is whole');
    await settle();
    assert.equal(t.heard.length, 1);
    assert.equal(t.heard[0].route, 'put_items_id_personal_state');
    assert.deepEqual(t.heard[0].params, {id: 'one'});
    assert.deepEqual(t.heard[0].answer, {libraryId: 'L'}, 'only what the answer names');
    await t.api.request('/v1/me/recommendations:reset', 'POST', {operationId: 'x'}); await settle();
    assert.equal(t.heard.at(-1)?.route, 'post_me_recommendations_reset', 'a write outside the durable queue is told too');
    await t.api.request('/v1/items/one/detail', 'GET'); await t.api.request('/v1/jobs', 'POST', {}); await settle();
    assert.equal(t.heard.length, 2, 'a read, and a write the table does not name, tell nothing');
    t.stop();
    await t.api.request('/v1/preferences', 'PATCH', {}); await settle();
    assert.equal(t.heard.length, 2, 'a listener that stopped hears nothing more');
  } finally { t.dispose(); }
});

test('a listener never hears a write\'s body or its whole answer', async () => {
  const t = transport();
  try {
    await t.api.request('/v1/admin/backups/settings', 'PUT', {backupFolder: '/backups', passphrase: 'correct horse battery staple'});
    await t.api.request('/v1/networking/remote/config', 'POST', {authorityId: 'a', config: {enabled: true}});
    await t.api.request('/v1/items/one/personal-state', 'PUT', {operationId: '00000000-0000-4000-8000-000000000000', expectedRevision: 3, watched: true});
    await settle();
    assert.deepEqual(t.heard.map(w => w.route), ['put_admin_backup_settings', 'post_networking_remote_config', 'put_items_id_personal_state']);
    for (const write of t.heard) {
      assert.deepEqual(Object.keys(write).sort(), write.answer ? ['answer', 'params', 'route'] : ['params', 'route']);
      assert.ok(!JSON.stringify(write).includes('horse') && !JSON.stringify(write).includes('viewerFence'), JSON.stringify(write));
    }
  } finally { t.dispose(); }
});

test('a refused write tells nothing', async () => {
  const t = transport({status: 409});
  try {
    await assert.rejects(t.api.request('/v1/me/recommendations:reset', 'POST', {operationId: 'x'}));
    await settle();
    assert.equal(t.heard.length, 0);
  } finally { t.dispose(); }
});

test('a write through the outbox is told when the server confirms it', async () => {
  const t = transport({queued: true});
  try {
    const value = await t.api.request<{operationId: string}>('/v1/items/one/reading/mark', 'PUT', {operationId: 'original-uuid', expectedRevision: 3, read: true});
    assert.equal(value.operationId, 'original-uuid', 'the writer still gets its own intent back');
    await settle();
    const told = t.heard.filter(w => w.route === 'put_items_itemId_reading_mark');
    assert.equal(told.length, 1, 'told once: ' + t.heard.map(w => w.route).join(','));
    assert.deepEqual(told[0].params, {itemId: 'one'});
  } finally { t.dispose(); }
});

// ── The cache ────────────────────────────────────────────────────────────────

test('the viewer\'s cache moves what a write elsewhere changed, using a page it holds for the episode\'s show; it stops hearing when it ends', async () => {
  const listeners = new Set<(write: ConfirmedWrite) => void>();
  let unsubscribed = 0;
  const writes = (listener: (write: ConfirmedWrite) => void) => { listeners.add(listener); return () => { unsubscribed++; listeners.delete(listener); }; };
  const hub = new FreshnessHub({settleMs: 0});
  const queries = createViewerQueries({scope: {serverId: 'server', viewerId: 'viewer'}, hub, api: {request: async () => { throw new Error('unused'); }}, writes});
  queries.client.setQueryData(queryKeys.itemDetail('ep', 'all'), {scope: {libraryId: 'L'}, item: {id: 'ep', episode: {showId: 'show', seasonId: 'season'}}});
  const before = (d: string) => hub.version(d);
  const watched = ['personal:L', 'personal:title:ep', 'personal:title:show', 'personal:title:season'], was = watched.map(before);
  for (const listener of listeners) listener({route: 'put_items_itemId_reading_mark', params: {itemId: 'ep'}});
  watched.forEach((d, i) => assert.ok(hub.version(d) > was[i], d + ' moved'));
  queries.dispose();
  assert.equal(unsubscribed, 1); assert.equal(listeners.size, 0);
  hub.dispose();
});

// ── A show page open while its season or episodes are marked from a menu ────────────────────

// A show's page as the server answers it (as in show-workspace.test.ts).
function showPage(seasonId = 'season1', episodes = ['ep1', 'ep2']) {
  const page = {scope: {serverId: 'server', libraryId: 'L', libraryKind: 'tv', view: 'season', entityId: seasonId, viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1}};
  return {scope: {serverId: 'server', libraryId: 'L', viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1},
    show: {id: 'show', libraryId: 'L', title: 'The Harbor', libraryKind: 'tv', providerMatchStatus: 'unresolved'},
    seasons: [{id: 'season1', showId: 'show', number: 1, title: 'Season 1'}, {id: 'season2', showId: 'show', number: 2, title: 'Season 2'}], seasonTotalCount: 2, nextSeasonCursor: '', groups: [],
    selected: {seasonId},
    episodes: {...page, heading: {key: 'entity.title', fallback: 'Episodes'}, navigation: [], query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 2, searchMode: 'none'},
      sorts: [{id: 'episode', labelKey: 'sort.episode', directions: ['asc']}], filters: [],
      sections: [{id: 'episodes', type: 'list', heading: {key: 'content.episodes', fallback: 'Episodes'}, totalCount: 2, nextCursor: '',
        entries: episodes.map(id => ({id, kind: 'episode', title: id, available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id, startMs: 0}}))}]}};
}
async function openShow() {
  const reads: string[] = [];
  const fetcher = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input)), method = init?.method ?? 'GET';
    if (method === 'GET' && url.pathname === '/v1/libraries/L/show-workspace') { reads.push(url.pathname); return Response.json(showPage()); }
    if (url.pathname.startsWith('/v1/containers/')) return Response.json(containerAnswer);
    if (url.pathname === '/v1/items/personal-state:batch') return Response.json(batchAnswer(['ep1', 'ep2']));
    return Response.json({error: {code: 'not_found'}}, {status: 404});
  };
  const api = new HttpLocalApi('http://server.test', session.accessToken, fetcher);
  const hub = new FreshnessHub({settleMs: 0});
  const queries = createViewerQueries({scope: {serverId: 'server', viewerId: 'viewer'}, hub, api, writes: api.onConfirmedWrite, gcTimeMs: Infinity});
  const show = new ShowWorkspaceService({queries, scope: {serverId: 'server', viewerId: 'viewer'} as never, pageSize: 2, seasonPageSize: 2});
  await show.select({libraryId: 'L', showId: 'show'});
  assert.equal(show.getSnapshot().phase, 'ready');
  return {api, hub, queries, show, reads, dispose: () => { show.dispose(); queries.dispose(); hub.dispose(); }};
}
const until = async (done: () => boolean) => { for (let i = 0; i < 100 && !done(); i++) await new Promise(resolve => setTimeout(resolve, 5)); };

test('a held show page names its show, seasons and episodes, and its library', async () => {
  const s = await openShow();
  try {
    assert.deepEqual(cachedTitle(s.queries.client, 'show'), {libraryId: 'L', titleIds: []});
    assert.deepEqual(cachedTitle(s.queries.client, 'season2'), {libraryId: 'L', titleIds: ['show']});
    assert.deepEqual(cachedTitle(s.queries.client, 'ep2'), {libraryId: 'L', titleIds: ['show', 'season1']});
    assert.equal(cachedTitle(s.queries.client, 'other'), undefined);
    assert.deepEqual(cachedLibraries(s.queries.client), ['L']);
  } finally { s.dispose(); }
});

test('a season or a show marked from a menu reads the open show page again, though its answer names no library', async () => {
  for (const path of ['/v1/containers/season/season1/personal-state', '/v1/containers/show/show/personal-state']) {
    const s = await openShow();
    try {
      const before = s.reads.length, was = s.hub.version('personal:L');
      await s.api.request(path, 'PUT', {expectedRevision: 1, watched: true});
      await until(() => s.reads.length > before);
      assert.ok(s.hub.version('personal:L') > was, path + ' moves the show\'s library');
      assert.equal(s.reads.length, before + 1, path + ' reads the show page again');
    } finally { s.dispose(); }
  }
});

test('episodes marked together read the open show page again', async () => {
  const s = await openShow();
  try {
    const before = s.reads.length;
    await s.api.request('/v1/items/personal-state:batch', 'PUT', {operationId: 'op', items: [{itemId: 'ep1', expectedRevision: 1, watched: true}, {itemId: 'ep2', expectedRevision: 1, watched: true}]});
    await until(() => s.reads.length > before);
    assert.equal(s.reads.length, before + 1);
  } finally { s.dispose(); }
});

test('a held album or book page names its titles and its library', () => {
  const hub = new FreshnessHub({settleMs: 0});
  const queries = createViewerQueries({scope: {serverId: 'server', viewerId: 'viewer'}, hub, api: {request: async () => { throw new Error('unused'); }}, gcTimeMs: Infinity});
  try {
    queries.client.setQueryData(queryKeys.content('M', 'view=album&entityId=album'), {scope: {serverId: 'server', libraryId: 'M', libraryKind: 'music', view: 'album', entityId: 'album', viewerFence: 'f'},
      entity: {id: 'album'}, sections: [{id: 'tracks', type: 'list', entries: [{id: 'track1', playback: {itemId: 'track1'}}]}]});
    assert.deepEqual(cachedTitle(queries.client, 'album'), {libraryId: 'M', titleIds: []});
    assert.deepEqual(cachedTitle(queries.client, 'track1'), {libraryId: 'M', titleIds: []});
    assert.equal(cachedTitle(queries.client, 'track2'), undefined);
  } finally { queries.dispose(); hub.dispose(); }
});
