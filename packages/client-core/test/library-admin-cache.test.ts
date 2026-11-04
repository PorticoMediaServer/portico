import {test} from 'node:test';
import assert from 'node:assert/strict';
import {LibraryManagementService, LibraryInventoryService} from '../src/library-management.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../src/query/viewer-queries.ts';
import {queryKeys} from '../src/query/keys.ts';

// Client data step 6: an owner's documents about a library (its management view and its inventory's
// first page) are read through the viewer's cache, and the client no longer compares viewer fences
// across reads. The #308 cases (a library administrator's list and library reads carry different
// fences) stay in library-management.test.ts and library-inventory.test.ts.
const scope = {serverId: 'server', viewerId: 'owner'};
const fence = (value: string) => ({serverId: 'server', viewerFence: value});
const library = (id = 'one', name = 'Named library', revision = 1): any => ({id, name, kind: 'movie', revision, source: {path: '/owner/source/' + id, availability: 'unknown', basis: 'not_checked'}, lastScan: null, actions: ['rename', 'scan']});
const job = (id = 'job-one', libraryId = 'one'): any => ({id, libraryId, status: 'running', processed: 12, createdAt: '2026-09-05T00:00:00Z', updatedAt: null, finishedAt: null, actions: ['cancel']});
const config = (viewerFence = 'plain', revision = 1): any => ({scope: fence(viewerFence), libraryId: 'one', revision, sources: [], policy: {tier: 'file_list_only', revision: 1, operations: []}});
const objects = (viewerFence = 'list', revision = 1, nextCursor = '', sourceId = ''): any => ({scope: fence(viewerFence), libraryId: 'one', sourceId, revision, items: [], nextCursor});

type Handler = (path: string, method: string, body: unknown) => unknown;
function server(handler: Handler = () => undefined) {
  const reads: string[] = [], writes: string[] = [];
  const state = {fence: 'plain', listFence: 'list', name: 'Named library', revision: 1, inventoryRevision: 1, nextCursor: 'c2'};
  const api = {request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    const custom = handler(path, method, body);
    if (custom !== undefined) return custom as T;
    const u = new URL(path, 'https://x');
    if (method !== 'GET') { writes.push(method + ' ' + u.pathname); if (method === 'PATCH') { state.name = (body as any).name; state.revision++; return {scope: fence(state.fence), library: library('one', state.name, state.revision)} as T; } return {id: 'scan-one', processed: 0} as T; }
    reads.push(u.pathname + u.search);
    if (u.pathname === '/v1/admin/libraries') return {scope: fence(state.listFence), revision: 1, items: [library()], nextCursor: ''} as T;
    if (u.pathname === '/v1/admin/jobs') return {scope: fence(state.listFence), revision: 1, items: [job('job-one', u.searchParams.get('libraryId') ?? 'one')], nextCursor: ''} as T;
    if (u.pathname.endsWith('/inventory-config')) return config(state.fence, state.inventoryRevision) as T;
    if (u.pathname.endsWith('/inventory')) return objects(state.listFence, state.inventoryRevision, u.searchParams.get('cursor') ? '' : state.nextCursor, u.searchParams.get('sourceId') ?? '') as T;
    return {scope: fence(state.fence), library: library(decodeURIComponent(u.pathname.split('/').at(-1)!), state.name, state.revision)} as T;
  }};
  return {api, reads, writes, state};
}
const made: ViewerQueries[] = [];
test.afterEach(() => { for (const q of made.splice(0)) q.dispose(); });
function cache(api: ReturnType<typeof server>['api']) {
  const hub = new FreshnessHub({settleMs: 0});
  const queries = createViewerQueries({scope: {serverId: 'server', viewerId: 'viewer'}, hub, api, gcTimeMs: Infinity});
  made.push(queries);
  return {hub, queries};
}
const management = (api: ReturnType<typeof server>['api'], queries?: ViewerQueries) => new LibraryManagementService({scope, api, queries});
const inventory = (api: ReturnType<typeof server>['api'], queries?: ViewerQueries, libraryId = 'one') => new LibraryInventoryService({scope, api, libraryId, queries});
const tick = () => new Promise(resolve => setTimeout(resolve, 5));
const libraryReads = (reads: string[]) => reads.filter(p => p === '/v1/admin/libraries/one').length;
const inventoryReads = (reads: string[]) => reads.filter(p => p.startsWith('/v1/admin/libraries/one/inventory?')).length;
const configReads = (reads: string[]) => reads.filter(p => p === '/v1/admin/libraries/one/inventory-config').length;
const scanned = (libraryId: string, revision: number) => ({id: 'e' + revision, type: 'library.scan.updated', revision: String(revision), resource: {kind: 'library', id: libraryId}, data: {state: 'progress', found: revision}});

// ── No client fence comparison ─────────────────────────────────────────────

test('management: a viewer fence that moved between reads answers and retires nothing', async () => {
  const s = server(), service = management(s.api);
  await service.loadLibraries(); await service.selectLibrary('one');
  s.state.listFence = 'admin-list-fence-2'; s.state.fence = 'plain-2';
  await service.reconcile();
  const snap = service.getSnapshot();
  assert.equal(snap.directory.phase, 'ready'); assert.equal(snap.jobs.phase, 'ready'); assert.equal(snap.selected.phase, 'ready');
  assert.equal(snap.directory.data?.scope.viewerFence, 'admin-list-fence-2');
  assert.equal(snap.selected.data?.scope.viewerFence, 'plain-2');
  assert.ok(snap.directory.data && snap.selected.data && snap.jobs.data, 'nothing was cleared');
  service.dispose();
});

test('management: a page continued under a moved viewer fence is the server\'s to refuse, not the client\'s', async () => {
  let fenceNow = 'one';
  const s = server((path) => {
    const u = new URL(path, 'https://x');
    if (u.pathname === '/v1/admin/libraries') return {scope: fence(fenceNow), revision: 1, items: [library(u.searchParams.get('cursor') ? 'two' : 'one')], nextCursor: u.searchParams.get('cursor') ? '' : 'next'};
    return undefined;
  });
  const service = management(s.api);
  await service.loadLibraries();
  fenceNow = 'two';
  await service.nextLibraries();
  assert.equal(service.getSnapshot().directory.phase, 'ready');
  assert.equal(service.getSnapshot().directory.data?.items[0]?.id, 'two');
  // The server's own refusal of the cursor still reads as "refresh this list".
  const refused = server((path) => {
    const u = new URL(path, 'https://x');
    if (u.pathname !== '/v1/admin/libraries') return undefined;
    if (u.searchParams.get('cursor')) throw Object.assign(new Error('Changed'), {code: 'stale_continuation', status: 412});
    return {scope: fence('one'), revision: 1, items: [library()], nextCursor: 'next'};
  });
  const again = management(refused.api);
  await again.loadLibraries(); await again.nextLibraries();
  assert.equal(again.getSnapshot().directory.phase, 'refresh-required');
  service.dispose(); again.dispose();
});

test('management: another server\'s answer or a refused owner still ends the view', async () => {
  const other = server((path) => { if (new URL(path, 'https://x').pathname === '/v1/admin/jobs') return {scope: {serverId: 'another-server', viewerFence: 'f'}, revision: 1, items: [], nextCursor: ''}; return undefined; });
  const service = management(other.api);
  await service.loadLibraries(); await service.selectLibrary('one');
  assert.equal(service.getSnapshot().jobs.phase, 'error', 'a different server\'s answer is never adopted');
  assert.equal(service.getSnapshot().jobs.data, null);
  service.dispose();

  let denied = false;
  const s = server((path) => { if (denied) throw Object.assign(new Error('Denied'), {status: 403, code: 'forbidden'}); return undefined; });
  const {queries} = cache(s.api);
  const owner = management(s.api, queries);
  await owner.loadLibraries(); await owner.selectLibrary('one');
  assert.ok(queries.client.getQueryData(queryKeys.adminLibraryManagement('one')));
  denied = true;
  await owner.reconcile().catch(() => undefined);
  assert.equal(owner.getSnapshot().directory.data, null);
  assert.equal(owner.getSnapshot().selected.data, null);
  assert.equal(queries.client.getQueryData(queryKeys.adminLibraryManagement('one')), undefined, 'the refused owner\'s documents leave the viewer\'s cache');
  owner.dispose();
});

test('inventory: a viewer fence that moved between reads answers and retires nothing', async () => {
  const s = server(), service = inventory(s.api);
  await service.refresh();
  s.state.fence = 'plain-2'; s.state.listFence = 'list-2';
  await service.refresh();
  assert.equal(service.getSnapshot().error, null);
  assert.equal(service.getSnapshot().config?.scope.viewerFence, 'plain-2');
  assert.equal(service.getSnapshot().objects?.scope.viewerFence, 'list-2');
  service.dispose();
});

test('inventory: another server\'s answer or a refused owner still ends the view', async () => {
  const other = server((path) => { if (new URL(path, 'https://x').pathname.endsWith('/inventory')) return {...objects(), scope: {serverId: 'another-server', viewerFence: 'f'}}; return undefined; });
  const service = inventory(other.api);
  await service.refresh();
  assert.ok(service.getSnapshot().error); assert.equal(service.getSnapshot().objects, null);
  service.dispose();

  let denied = false;
  const s = server((path) => { if (denied) throw Object.assign(new Error('Denied'), {code: 'forbidden'}); return undefined; });
  const {queries} = cache(s.api);
  const owner = inventory(s.api, queries);
  await owner.refresh();
  assert.ok(queries.client.getQueryData(queryKeys.adminLibraryInventory('one', '', '')));
  denied = true;
  await owner.refresh();
  assert.equal(owner.getSnapshot().config, null); assert.equal(owner.getSnapshot().objects, null);
  assert.equal(queries.client.getQueryData(queryKeys.adminLibraryInventory('one', '', '')), undefined);
  owner.dispose();
});

// ── The viewer's cache ──────────────────────────────────────────────────────

test('management: two owners share one read of a library, and a view opened again reads nothing while current', async () => {
  const s = server(), {queries} = cache(s.api);
  const first = management(s.api, queries), second = management(s.api, queries);
  await Promise.all([first.selectLibrary('one'), second.selectLibrary('one')]);
  assert.equal(libraryReads(s.reads), 1, 'two owners, one read');
  assert.equal(second.getSnapshot().selected.data?.library.name, 'Named library');
  const phases: string[] = []; const third = management(s.api, queries);
  third.subscribe(() => phases.push(third.getSnapshot().selected.phase));
  const opened = third.selectLibrary('one');
  assert.equal(third.getSnapshot().selected.phase, 'ready', 'shown in the same turn');
  await opened;
  assert.ok(!phases.includes('loading'), 'no loading state for a library the cache holds');
  assert.equal(libraryReads(s.reads), 1, 'reopened: nothing read');
  assert.equal(third.getSnapshot().selected.phase, 'ready');
  assert.equal(third.getSnapshot().selected.data?.library.id, 'one');
  first.dispose(); second.dispose(); third.dispose();
});

test('management: a scan hint for that library, or the server\'s settings, makes the next opening read again; another library\'s does not', async () => {
  const s = server(), {hub, queries} = cache(s.api);
  const a = management(s.api, queries);
  await a.selectLibrary('one');
  hub.accept(scanned('other', 3) as never);
  await a.selectLibrary('one');
  assert.equal(libraryReads(s.reads), 1, 'a different library\'s scan moves nothing here');
  hub.accept(scanned('one', 4) as never);
  await a.selectLibrary('one'); await tick();
  assert.equal(libraryReads(s.reads), 2, 'this library\'s scan: read again');
  hub.accept({id: 's1', type: 'settings.changed', revision: '5', resource: {kind: 'settings', id: 'server'}} as never);
  await a.selectLibrary('one'); await tick();
  assert.equal(libraryReads(s.reads), 3, 'the server\'s settings: read again');
  a.dispose();
});

test('management: a rename here reads every cached view of the library again, and shows the new name', async () => {
  const s = server(), {queries} = cache(s.api);
  const page = management(s.api, queries), other = management(s.api, queries);
  await page.loadLibraries(); await page.selectLibrary('one');
  await other.selectLibrary('one');
  await page.rename('Renamed', 1); await tick();
  assert.deepEqual(s.writes, ['PATCH /v1/admin/libraries/one']);
  await other.selectLibrary('one');
  assert.equal(other.getSnapshot().selected.data?.library.name, 'Renamed', 'not the document cached before the write');
  page.dispose(); other.dispose();
});

test('a command on a library\'s inventory reads its cached management view again, and a rename reads its cached inventory again', async () => {
  const s = server(), {queries} = cache(s.api);
  const m = management(s.api, queries), i = inventory(s.api, queries);
  await m.selectLibrary('one'); await i.loadObjects();
  const library = libraryReads(s.reads), pages = inventoryReads(s.reads);
  // A command made through the inventory: the management view of the same library is owed a read.
  await i.scanSource('source'); await tick();
  const afterCommand = libraryReads(s.reads);
  const other = management(s.api, queries);
  await other.selectLibrary('one'); await tick();
  assert.equal(libraryReads(s.reads), afterCommand + 1, 'the management view cached before the command is read again');
  assert.ok(afterCommand >= library);
  // A rename made through management: the inventory's cached first page is owed a read.
  await i.refresh(); const before = inventoryReads(s.reads);
  await other.rename('Renamed', 1); await tick();
  const afterRename = inventoryReads(s.reads);
  await inventory(s.api, queries).loadObjects(); await tick();
  assert.equal(inventoryReads(s.reads), afterRename + 1, 'the inventory page cached before the rename is read again');
  assert.ok(afterRename >= before && pages >= 1);
  m.dispose(); i.dispose(); other.dispose();
});

test('management: a service without a cache reads the server each time', async () => {
  const s = server(), service = management(s.api);
  await service.selectLibrary('one'); await service.selectLibrary('one');
  assert.equal(libraryReads(s.reads), 2);
  service.dispose();
});

test('management: a cache of another server is refused', () => {
  const s = server(), {queries} = cache(s.api);
  assert.throws(() => new LibraryManagementService({scope: {serverId: 'elsewhere', viewerId: 'owner'}, api: s.api, queries}));
});

/** A view's own requester, as a screen gives it: everything it sends ends when the view closes. */
function screenApi(base: ReturnType<typeof server>['api']) {
  const life = new AbortController();
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => new Promise<T>((resolve, reject) => {
    const both = new AbortController(), stop = () => { both.abort(); reject(Object.assign(new Error('closed'), {name: 'AbortError'})); };
    for (const s of [life.signal, signal]) if (s) { if (s.aborted) stop(); else s.addEventListener('abort', stop, {once: true}); }
    base.request<T>(path, method, body, both.signal).then(resolve, reject);
  })};
  return {api, close: () => life.abort()};
}
/** Holds the server's answer to reads matching `held` until `release()`. */
function gated(base: ReturnType<typeof server>['api'], held: (path: string) => boolean) {
  let release!: () => void; const open = new Promise<void>(resolve => { release = resolve; });
  const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    if ((method ?? 'GET') === 'GET' && held(path)) { await open; if (signal?.aborted) throw Object.assign(new Error('aborted'), {name: 'AbortError'}); }
    return base.request<T>(path, method, body, signal);
  }};
  return {api, release};
}

test('management: the view that started a shared read closes, and the other view waiting on it still gets the library', async () => {
  const s = server(), g = gated(s.api, path => path === '/v1/admin/libraries/one'), {queries} = cache(g.api);
  const screenA = screenApi(g.api), screenB = screenApi(g.api);
  const first = management(screenA.api, queries), second = management(screenB.api, queries);
  const a = first.selectLibrary('one'); await tick();
  const b = second.selectLibrary('one'); await tick();
  first.dispose(); screenA.close();
  g.release(); await Promise.allSettled([a, b]); await tick();
  assert.equal(second.getSnapshot().selected.phase, 'ready', 'the second view is answered');
  assert.equal(second.getSnapshot().selected.data?.library.id, 'one');
  assert.equal(libraryReads(s.reads), 1, 'one read, shared');
  second.dispose();
});

test('inventory: the view that started a shared first page closes, and the other view waiting on it still gets it', async () => {
  const s = server(), g = gated(s.api, path => path.startsWith('/v1/admin/libraries/one/inventory')), {queries} = cache(g.api);
  const screenA = screenApi(g.api), screenB = screenApi(g.api);
  const first = inventory(screenA.api, queries), second = inventory(screenB.api, queries);
  const a = first.loadObjects(); await tick();
  const b = second.loadObjects(); await tick();
  first.dispose(); screenA.close();
  g.release(); await Promise.allSettled([a, b]); await tick();
  assert.ok(second.getSnapshot().objects && second.getSnapshot().config, 'the second view is answered');
  assert.equal(second.getSnapshot().error ?? null, null);
  assert.equal(inventoryReads(s.reads), 1, 'one read, shared');
  second.dispose();
});

test('management: a service reading from one server\'s cache refuses to be moved to another server', () => {
  const s = server(), {queries} = cache(s.api), service = management(s.api, queries);
  assert.throws(() => service.setScope({serverId: 'other', viewerId: 'owner'} as any, s.api), /own server/);
  service.setScope({serverId: 'server', viewerId: 'owner-2'} as any, s.api);
  service.dispose();
});

test('inventory: views share the first page; reopening reads nothing while current; each filter is its own document', async () => {
  const s = server(), {queries} = cache(s.api);
  const a = inventory(s.api, queries), b = inventory(s.api, queries);
  await Promise.all([a.loadObjects(), b.loadObjects()]);
  assert.equal(inventoryReads(s.reads), 1); assert.equal(configReads(s.reads), 1);
  const c = inventory(s.api, queries);
  await c.loadObjects();
  assert.equal(inventoryReads(s.reads), 1, 'reopened: nothing read'); assert.ok(c.getSnapshot().objects && c.getSnapshot().config);
  await c.loadObjects({state: 'missing'});
  assert.equal(inventoryReads(s.reads), 2, 'another filter is another document');
  a.dispose(); b.dispose(); c.dispose();
});

test('inventory: only the first page is cached; later pages and Back are direct reads', async () => {
  const s = server(), {queries} = cache(s.api);
  const a = inventory(s.api, queries);
  await a.loadObjects();
  await a.next();
  assert.equal(inventoryReads(s.reads), 2);
  assert.equal(a.getSnapshot().canPrevious, true);
  await a.next().catch(() => undefined);
  await a.previous();
  assert.equal(inventoryReads(s.reads), 2, 'Back to the first page: the cached page, nothing read');
  assert.equal(a.getSnapshot().cursor, '');
  assert.equal(queries.client.getQueryCache().findAll({queryKey: ['admin', 'library', 'one', 'inventory']}).length, 1, 'one cached page, not one per cursor');
  a.dispose();
});

test('inventory: this library\'s scan hint reads the first page again when next asked for; another library\'s does not', async () => {
  const s = server(), {hub, queries} = cache(s.api);
  const a = inventory(s.api, queries);
  await a.loadObjects();
  hub.accept(scanned('other', 3) as never);
  await a.loadObjects();
  assert.equal(inventoryReads(s.reads), 1);
  hub.accept(scanned('one', 4) as never);
  await a.loadObjects(); await tick();
  assert.equal(inventoryReads(s.reads), 2);
  a.dispose();
});

test('inventory: a command here reads every cached document of the library again, and a refresh asks the server', async () => {
  const s = server(), {queries} = cache(s.api);
  const a = inventory(s.api, queries), b = inventory(s.api, queries);
  await a.loadObjects(); await b.loadObjects();
  const before = inventoryReads(s.reads);
  await a.scanSource('source'); await tick();
  assert.equal(inventoryReads(s.reads), before + 1, 'the command\'s own reconcile read the server');
  await b.loadObjects(); await tick();
  assert.equal(inventoryReads(s.reads), before + 1, 'and that answer is what the other view takes');
  await b.refresh();
  assert.equal(inventoryReads(s.reads), before + 2, 'refresh() always asks the server');
  a.dispose(); b.dispose();
});

test('inventory: a continuation the server no longer continues starts that list again at its first page', async () => {
  let refuse = false;
  const s = server((path) => { if (refuse && new URL(path, 'https://x').searchParams.get('cursor')) throw Object.assign(new Error('Changed'), {code: 'stale_continuation', status: 412}); return undefined; });
  const service = inventory(s.api);
  await service.loadObjects(); await service.next();
  assert.equal(service.getSnapshot().cursor, 'c2');
  refuse = true;
  await service.refresh();
  assert.equal(service.getSnapshot().cursor, '', 'reset to the first page');
  assert.equal(service.getSnapshot().canPrevious, false);
  assert.equal(service.getSnapshot().error, null);
  assert.ok(service.getSnapshot().objects);
  service.dispose();
});

test('privacy: owner documents of an ended viewer\'s cache are not shown and read nothing more', async () => {
  const s = server(), {queries} = cache(s.api);
  const m = management(s.api, queries), i = inventory(s.api, queries);
  await m.selectLibrary('one'); await i.loadObjects();
  queries.dispose();
  const owned = () => libraryReads(s.reads) + inventoryReads(s.reads) + configReads(s.reads), reads = owned();
  await m.selectLibrary('one').catch(() => undefined); await i.loadObjects().catch(() => undefined);
  assert.equal(owned(), reads, 'no request for these documents after the viewer ended');
  assert.equal(m.getSnapshot().selected.data, null);
  assert.equal(i.getSnapshot().objects, null);
  m.dispose(); i.dispose();
});
