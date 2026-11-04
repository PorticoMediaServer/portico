import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, emptyUserMutationState, type UserMutationState, type LocalSession} from '../src/index.ts';
import {routes} from '../../contracts/src/routes.ts';
import {writeDependencies, writeRoute, writeRouteIds, type ConfirmedWrite} from '../src/query/write-dependencies.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries} from '../src/query/viewer-queries.ts';
import {queryKeys} from '../src/query/keys.ts';

// Client data step 7: every write the server confirms on this device moves what it changed in the
// viewer's cache, whoever made it, through one hook in the API's transport.
const nothingCached = {title: () => undefined};
const deps = (route: ConfirmedWrite['route'], params: Record<string, string>, body?: unknown, response?: unknown, context: Parameters<typeof writeDependencies>[1] = nothingCached) =>
  writeDependencies({route, params, body, response}, context);

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
  assert.equal(writeRoute('/v1/items/one/personal-state', 'PUT')?.needsResponse, true);
  assert.equal(writeRoute('/v1/preferences', 'PATCH')?.needsResponse, false);
});

test('a title\'s state: its library from the answer, else from a page the cache holds, else every library', () => {
  assert.deepEqual([...deps('put_items_id_personal_state', {id: 'ep'}, {}, {libraryId: 'L'})].sort(), ['personal:*', 'personal:L', 'personal:title:ep']);
  const cached = {title: (id: string) => id === 'ep' ? {libraryId: 'L', titleIds: ['show', 'season']} : undefined};
  assert.deepEqual([...deps('put_items_itemId_reading_mark', {itemId: 'ep'}, {}, undefined, cached)].sort(), ['personal:*', 'personal:L', 'personal:title:ep', 'personal:title:season', 'personal:title:show'],
    'an episode marked anywhere moves its show and season pages too');
  const unknown = deps('put_items_itemId_reading_favorite', {itemId: 'x'});
  assert.deepEqual([...unknown].sort(), ['personal:*', 'personal:title:x']);
  assert.ok(!unknown.some(d => d.startsWith('personal:any:')), 'a write about one title never moves every title page');
});

test('a batch moves each title in its own library; a container moves every page of its library', () => {
  const batch = deps('put_items_personal_state_batch', {}, {items: [{itemId: 'a'}, {itemId: 'b'}]}, {items: [{itemId: 'a', ok: true, personal: {libraryId: 'L1'}}, {itemId: 'b', ok: true, personal: {libraryId: 'L2'}}]});
  for (const d of ['personal:L1', 'personal:L2', 'personal:*', 'personal:title:a', 'personal:title:b']) assert.ok(batch.includes(d), d);
  const container = deps('set_container_personal_state', {kind: 'show', id: 'show'}, {}, {libraryId: 'L'});
  for (const d of ['personal:L', 'personal:*', 'personal:title:show', 'personal:any:L']) assert.ok(container.includes(d), d);
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

const copy = <T,>(value: T): T => JSON.parse(JSON.stringify(value));
class Store {
  state = emptyUserMutationState();
  async read() { return copy(this.state); }
  async compareAndSwap(revision: number, next: UserMutationState) { if (this.state.revision !== revision) return false; this.state = copy(next); return true; }
}
const session = {accessToken: 'token', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z', tokenGeneration: '1', sessionFamilyId: 'family', deviceId: 'device',
  viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}} as unknown as LocalSession;
function transport(options: {queued?: boolean; status?: number} = {}) {
  const store = new Store();
  const fetcher = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input)), method = init?.method ?? 'GET', headers = new Headers(init?.headers);
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined;
    if (url.pathname === '/v1/user-mutations/policy') return Response.json({serverTimeMs: Date.now() + 86400000, retiredBeforeMs: 0});
    if (url.pathname.startsWith('/v1/user-mutations/')) {
      const key = url.pathname.split('/').pop()!;
      const entry = store.state.entries.find(e => e.key === key);
      return Response.json({key, operation: entry?.operation, status: 'applied', targetId: 'target', revision: 4, generation: 1, committedMs: Date.now(), expiresMs: Date.now() + 2592000000});
    }
    if (method === 'GET') return Response.json({scope: {libraryId: 'L'}, personal: {revision: 3}});
    if (options.status && options.status >= 400) return Response.json({error: {code: 'refused'}}, {status: options.status});
    if (url.pathname.endsWith('/reading/mark')) return Response.json({operationId: body?.operationId, revision: 4}, {headers: {'Portico-Mutation-Key': headers.get('Idempotency-Key') ?? ''}});
    if (url.pathname.endsWith('/personal-state')) return Response.json({id: 'p', itemId: url.pathname.split('/')[3], libraryId: 'L', revision: 4, operationId: body?.operationId, serverId: 'server', viewerFence: 'f'}, {headers: headers.get('Idempotency-Key') ? {'Portico-Mutation-Key': headers.get('Idempotency-Key')!} : {}});
    return Response.json({ok: true});
  };
  const api = new HttpLocalApi('http://server.test', session.accessToken, fetcher);
  let coordinator: {dispose(): void} | undefined;
  if (options.queued) { api.setUserMutationSession(session); coordinator = api.configureUserMutations(store); }
  const heard: ConfirmedWrite[] = [];
  const stop = api.onConfirmedWrite(write => heard.push(write));
  return {api, heard, stop, dispose: () => { api.getUserMutationCoordinator()?.dispose(); coordinator?.dispose(); }};
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
    assert.equal((t.heard[0].response as {libraryId?: string}).libraryId, 'L');
    await t.api.request('/v1/me/recommendations:reset', 'POST', {operationId: 'x'}); await settle();
    assert.equal(t.heard.at(-1)?.route, 'post_me_recommendations_reset', 'a write outside the durable queue is told too');
    await t.api.request('/v1/items/one/detail', 'GET'); await t.api.request('/v1/jobs', 'POST', {}); await settle();
    assert.equal(t.heard.length, 2, 'a read, and a write the table does not name, tell nothing');
    t.stop();
    await t.api.request('/v1/preferences', 'PATCH', {}); await settle();
    assert.equal(t.heard.length, 2, 'a listener that stopped hears nothing more');
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

test('a write through the durable mutation queue is told when the server confirms it', async () => {
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
  for (const listener of listeners) listener({route: 'put_items_itemId_reading_mark', params: {itemId: 'ep'}, body: {}, response: undefined});
  watched.forEach((d, i) => assert.ok(hub.version(d) > was[i], d + ' moved'));
  queries.dispose();
  assert.equal(unsubscribed, 1); assert.equal(listeners.size, 0);
  hub.dispose();
});
