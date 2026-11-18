import {test} from 'node:test';
import assert from 'node:assert/strict';
import {LibraryManagementService} from '../src/library-management.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../src/query/viewer-queries.ts';
import {queryKeys} from '../src/query/keys.ts';

const scope = {serverId: 'server', viewerId: 'owner'};
const fence = (value: string) => ({serverId: 'server', viewerFence: value});
// The rebuilt server's library (`AdminLibrary`): the management service maps it to the page's shape.
const library = (id = 'one', name = 'Named library', revision = 1): any => ({id, name, kind: 'movies', revision: String(revision), folders: [{path: '/owner/source/' + id, available: true, watched: true, scanEveryMinutes: 0}], options: {depth: 'basic', trash: {mode: 'days', days: 30}, allowDelete: false, rescanMinutes: 0}, scanning: false});
const job = (id = 'job-one', libraryId = 'one'): any => ({id, libraryId, status: 'running', processed: 12, createdAt: '2026-09-05T00:00:00Z', updatedAt: null, finishedAt: null, actions: ['cancel']});
type Handler = (path: string, method: string, body: unknown) => unknown;
function server(handler: Handler = () => undefined) {
  const reads: string[] = [], writes: string[] = [];
  const state = {listFence: 'list', name: 'Named library', revision: 1};
  const api = {request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    const custom = handler(path, method, body);
    if (custom !== undefined) return custom as T;
    const u = new URL(path, 'https://x');
    if (method !== 'GET') { writes.push(method + ' ' + u.pathname); if (method === 'PATCH') { state.name = (body as any).name; state.revision++; return library('one', state.name, state.revision) as T; } return {id: 'scan-one', processed: 0} as T; }
    reads.push(u.pathname + u.search);
    if (u.pathname === '/v1/admin/libraries') return {items: [library()]} as T;
    if (u.pathname === '/v1/admin/jobs') return {scope: fence(state.listFence), revision: 1, items: [job('job-one', u.searchParams.get('libraryId') ?? 'one')], nextCursor: ''} as T;
    return library(decodeURIComponent(u.pathname.split('/').at(-1)!), state.name, state.revision) as T;
  }};
  return {api, reads, writes};
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
const tick = () => new Promise(resolve => setTimeout(resolve, 5));
const libraryReads = (reads: string[]) => reads.filter(p => p === '/v1/admin/libraries/one').length;
const scanned = (libraryId: string, revision: number) => ({id: 'e' + revision, type: 'library.scan.updated', revision: String(revision), resource: {kind: 'library', id: libraryId}, data: {state: 'progress', found: revision}});

test('management: a refused owner ends the view', async () => {
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

test('management: a service reading from one server\'s cache refuses to be moved to another server', () => {
  const s = server(), {queries} = cache(s.api), service = management(s.api, queries);
  assert.throws(() => service.setScope({serverId: 'other', viewerId: 'owner'} as any, s.api), /own server/);
  service.setScope({serverId: 'server', viewerId: 'owner-2'} as any, s.api);
  service.dispose();
});

test('privacy: owner documents of an ended viewer\'s cache are not shown and read nothing more', async () => {
  const s = server(), {queries} = cache(s.api);
  const m = management(s.api, queries);
  await m.selectLibrary('one');
  queries.dispose();
  const reads = libraryReads(s.reads);
  await m.selectLibrary('one').catch(() => undefined);
  assert.equal(libraryReads(s.reads), reads, 'no library request after the viewer ended');
  assert.equal(m.getSnapshot().selected.data, null);
  m.dispose();
});
