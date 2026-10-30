import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
import {makeRecordingFetch, summarize, walkProbe, type ProbeInput} from '../tools/parser-probe.ts';

const TOKEN = 'probe-secret-token';
const ORIGIN = 'http://127.0.0.1:19731';

type LoggedRequest = {method: string; path: string; auth: string | null};
type RouteBody = {status: number; json: unknown} | {raw: string};

function responseFor(route: RouteBody): Response {
  if ('raw' in route) {
    return new Response(route.raw, {status: 200, headers: {'Content-Type': 'application/json'}});
  }
  return new Response(JSON.stringify(route.json), {status: route.status, headers: {'Content-Type': 'application/json'}});
}

/** In-memory path → JSON map reusing the shapes client-core tests already assert. */
function fakeFetch(routes: Map<string, RouteBody>, log: LoggedRequest[]): typeof fetch {
  return (async (input: unknown, init?: unknown): Promise<Response> => {
    const url = typeof input === 'string' ? input : String((input as {url?: unknown}).url ?? '');
    const initMethod = (init as {method?: unknown} | undefined)?.method;
    const method = (typeof initMethod === 'string' ? initMethod : (input as {method?: unknown}).method as string ?? 'GET').toUpperCase();
    const path = url.replace(/^https?:\/\/[^/]+/, '');
    const headers = (init as {headers?: unknown} | undefined)?.headers;
    const auth = headers instanceof Headers ? headers.get('authorization') : (headers as Record<string, string> | undefined)?.['Authorization'] ?? (headers as Record<string, string> | undefined)?.['authorization'] ?? null;
    log.push({method, path, auth});
    // The probe is read-only: GET, plus POST to the browse-query read route.
    if (method !== 'GET' && !(method === 'POST' && /\/browse$/.test(path.split('?')[0]))) {
      throw new Error(`write blocked: ${method} ${path}`);
    }
    const route = routes.get(`${method} ${path}`);
    if (!route) {
      return new Response(JSON.stringify({error: {code: 'not_found', message: 'No such fixture.'}}), {status: 404, headers: {'Content-Type': 'application/json'}});
    }
    return responseFor(route);
  }) as typeof fetch;
}

function row(id: string, entries: unknown[]) {
  return {
    id, title: `Row ${id}`, kind: 'recent', artworkShape: 'poster', endpoint: `/v1/home/rows/${id}`,
    libraryId: 'lib-0', viewerFence: 'fence', privacySensitivity: 'catalog', policyState: 'available', priority: 100, cacheTtlMs: 120000,
    required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
    entries, total: entries.length, start: 0, limit: entries.length, hasMore: false, nextCursor: '',
    revision: {catalog: 1, viewer: 1},
  };
}

function movieEntry(id: string, libraryId = 'lib-0') {
  return {id, kind: 'movie', title: `Title ${id}`, libraryId, available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}};
}

function homeDocument(entryCount: number) {
  const entries = Array.from({length: entryCount}, (_, index) => movieEntry(`m-home-${index}`));
  return {
    serverId: 'srv', viewerFence: 'fence', rows: [row('recent', entries)],
    layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []},
    revision: {catalog: 1, viewer: 1}, generatedAt: '2026-09-16T12:00:00.000Z',
  };
}

function projection(libraryId: string, view: string, entryCount: number) {
  const entries = Array.from({length: entryCount}, (_, index) => movieEntry(`m-${libraryId}-${index}`, libraryId));
  return {
    scope: {serverId: 'srv', libraryId, libraryKind: 'movie', view, entityId: '', viewerFence: 'fence'},
    revision: {catalog: 1, viewer: 1},
    query: {sort: 'title', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'},
    heading: {key: 'h', fallback: 'H'},
    navigation: [], sorts: [], filters: [],
    sections: [{id: 's1', type: 'grid', heading: {key: 'h', fallback: 'H'}, entries, totalCount: entryCount, nextCursor: ''}],
  };
}

function capabilities(libraryId: string) {
  return {
    library: {id: libraryId, name: libraryId, kind: 'movie', defaultView: 'discover'},
    queryLimits: {maximumDepth: 4, maximumClauses: 8, maximumBytes: 8192, maximumSorts: 3, defaultLimit: 20, maximumLimit: 60, cursorTtlMs: 300000},
    pivots: [{id: 'movies', labelKey: 'k', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['browse'], browsable: true}],
    resolvedPivot: null,
    fields: [{id: 'title', labelKey: 'k', type: 'string', operators: ['contains'], controlHint: 'text', complexity: 'standard', cost: 'indexed', applicableKinds: ['movie']}],
    sorts: [], quickFilters: [],
  };
}

function browseResult() {
  return {
    pivot: 'movies', applied: {query: null, sort: [], seek: null}, entries: [],
    pageInfo: {start: 0, total: 0, revision: 'r', hasMore: false, nextCursor: ''},
  };
}

function routesFor(libraryCount: number, role: string): Map<string, RouteBody> {
  const routes = new Map<string, RouteBody>();
  const get = (path: string, json: unknown, status = 200) => routes.set(`GET ${path}`, {status, json});
  const post = (path: string, json: unknown, status = 200) => routes.set(`POST ${path}`, {status, json});
  get('/v1/system', {id: 'srv', name: 't', setupRequired: false, hostedConfigured: false, version: '1'});
  get('/v1/me', {viewer: {accountId: 'a', profileId: 'p', serverId: 'srv', authority: 'local', role}});
  get('/v1/capabilities', {features: {playback_v1: true}});
  get('/v1/home?limit=12', homeDocument(2));
  get('/v1/home/layout', {revision: 1, rowOrder: ['recent'], hiddenRowIds: [], rows: [{id: 'recent', title: 'Recent', kind: 'recent', artworkShape: 'poster', libraryId: 'lib-0', required: false, hideable: true, reorderable: true, hidden: false}]});
  get('/v1/home/rows/recent', row('recent', [movieEntry('m-home-0'), movieEntry('m-home-1')]));
  get('/v1/suggestions?limit=12', {items: [], total: 0, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-09-16T12:00:00.000Z'});
  get('/v1/libraries', {items: Array.from({length: libraryCount}, (_, index) => ({id: `lib-${index}`, name: `L${index}`, kind: 'movie'}))});
  for (let index = 0; index < libraryCount; index++) {
    get(`/v1/libraries/lib-${index}/content?view=discover&limit=40`, projection(`lib-${index}`, 'discover', 5));
    get(`/v1/libraries/lib-${index}/browse-capabilities`, capabilities(`lib-${index}`));
    post(`/v1/libraries/lib-${index}/browse`, browseResult());
    get(`/v1/libraries/lib-${index}/facets?field=title&limit=20`, {field: 'title', values: []});
    get(`/v1/libraries/lib-${index}/inventory-status`, {serverId: 'srv', inventory: {libraryId: `lib-${index}`, revision: 1, sources: []}});
  }
  return routes;
}

async function walk(routes: Map<string, RouteBody>, log: LoggedRequest[], limit: number, role = 'owner') {
  const seen = new Map<string, {status: number; body: string}>();
  const api = new HttpLocalApi(ORIGIN, TOKEN, makeRecordingFetch(fakeFetch(routes, log), seen));
  const input: ProbeInput = {origin: ORIGIN, serverId: 'srv', viewerId: 'signed-out', authority: '', accountId: '', profileId: '', role: '', limit, seen, token: TOKEN};
  const report = await walkProbe(api, input);
  return {report, seen};
}

test('a malformed response is reported as a failure with its path and excerpt and the walk continues', async () => {
  const routes = routesFor(1, 'owner');
  routes.set('GET /v1/home?limit=12', {raw: '{"broken":true}'});
  const log: LoggedRequest[] = [];
  const {report} = await walk(routes, log, 2);
  const home = report.calls.find(call => call.name === 'home document');
  assert.ok(home, 'the home document was attempted');
  assert.equal(home.ok, false);
  assert.equal(home.path, '/v1/home?limit=12');
  assert.equal(home.error?.code, 'invalid_home');
  assert.equal(home.excerpt, '{"broken":true}');
  // The walk continued past the malformed home into libraries and beyond.
  assert.ok(report.calls.some(call => call.name === 'library list'), 'the library list was still attempted');
  assert.ok(report.finishedAt >= report.startedAt);
});

test('no write method is ever sent', async () => {
  const log: LoggedRequest[] = [];
  // The fake throws on anything but GET and the allow-listed read POSTs, so a
  // write would surface here as a rejected walk.
  const {report} = await walk(routesFor(1, 'owner'), log, 2);
  assert.ok(report.calls.length > 0);
  for (const entry of log) {
    const browsePost = entry.method === 'POST' && /\/browse$/.test(entry.path.split('?')[0]);
    assert.ok(entry.method === 'GET' || browsePost, `unexpected write: ${entry.method} ${entry.path}`);
  }
  assert.ok(log.some(entry => entry.method === 'POST' && /\/browse$/.test(entry.path.split('?')[0])), 'the browse read POST was exercised');
});

test('the token never appears in the report or stdout', async () => {
  const log: LoggedRequest[] = [];
  const {report, seen} = await walk(routesFor(1, 'owner'), log, 2);
  assert.equal(JSON.stringify(report).includes(TOKEN), false);
  assert.equal(summarize(report).includes(TOKEN), false);
  for (const entry of seen.values()) {
    assert.equal(entry.body.includes(TOKEN), false);
  }
  assert.ok(log.every(entry => entry.auth === `Bearer ${TOKEN}`), 'the bearer was sent on requests');
});

test('per-list bounds hold: at most 8 libraries and PROBE_LIMIT entries per list', async () => {
  const log: LoggedRequest[] = [];
  const {report} = await walk(routesFor(10, 'owner'), log, 2);
  const discovers = log.filter(entry => /\/content\?view=discover/.test(entry.path));
  assert.equal(discovers.length, 8);
  assert.ok(!log.some(entry => entry.path.includes('lib-8') || entry.path.includes('lib-9')), 'libraries past the cap are never requested');
  const lib0Details = log.filter(entry => entry.path.includes('/v1/items/m-lib-0-') && entry.path.endsWith('/detail?related=all'));
  assert.equal(lib0Details.length, 2);
  const failures = report.failures.filter(failure => failure.area === 'libraries' && failure.name.startsWith('content discover'));
  assert.equal(failures.length, 0);
});

test('owner console reads are skipped for non-owners and attempted for owners', async () => {
  const memberLog: LoggedRequest[] = [];
  const member = await walk(routesFor(1, 'member'), memberLog, 1);
  assert.ok(!memberLog.some(entry => entry.path.startsWith('/v1/admin/console')), 'no console reads for a member');
  assert.ok(!memberLog.some(entry => entry.path.startsWith('/v1/dvr')), 'no DVR reads for a member');
  assert.ok(member.report.notProbed.some(entry => entry.name === 'owner console reads'));
  const ownerLog: LoggedRequest[] = [];
  const owner = await walk(routesFor(1, 'owner'), ownerLog, 1);
  assert.ok(ownerLog.some(entry => entry.path.startsWith('/v1/admin/console')), 'console reads attempted for an owner');
  assert.ok(ownerLog.some(entry => entry.path.startsWith('/v1/dvr')), 'DVR reads attempted for an owner');
});
