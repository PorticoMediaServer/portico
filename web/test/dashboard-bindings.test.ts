import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDashboardBindings, type DashboardBindingOptions} from '../src/screens/dashboard/bindings.ts';
import {FreshnessHub} from '../../packages/client-core/src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../../packages/client-core/src/query/viewer-queries.ts';
import {captureReportContext} from '../../packages/client-core/src/report-context.ts';
import {METRIC_NAMES} from '../../packages/client-core/src/telemetry.ts';
const context = captureReportContext('UTC', Date.UTC(2026, 9, 22, 12, 17, 31));
const interval = (period = 'all') => ({period, ...context, from: context.until, buckets: []});
const emptyHistory = () => ({...interval(), entries: [], total: 0, nextCursor: ''});
function report() {
  const bounds = interval();
  return {context, history: {...bounds, topKind: 'movies', top: [], busiestTimes: Array.from({length: 7}, () => Array(24).fill(0)), quietestStartHour: null}, people: {...bounds, items: [], nextCursor: ''}, summary: {...bounds, plays: 0, people: 0, watchedMs: 0, converted: 0, mostPlayed: [], mostActive: []}, topPlayed: {...bounds, topKind: 'movies', items: []}, table: emptyHistory(), load: {...bounds, window: 'all', observedAt: Date.parse(context.until), series: Object.fromEntries(METRIC_NAMES.map(k => [k, []])), status: Object.fromEntries(METRIC_NAMES.map(k => [k, {status: 'unavailable', value: 0}]))}};
}
const reportQuery = {period: 'all', window: 'all', peoplePeriod: 'all', topPeriod: 'all', topKind: 'movies'} as const;
const page = () => ({scope: {serverId: 'server', viewerFence: 'a'.repeat(64)}, observedAt: '2026-10-23T00:00:00Z', items: [], page: {limit: 10, start: 0, total: 0, revision: '1'}, totals: {streams: 0, atHome: 0, away: 0, paused: 0, converting: 0, people: 0, bandwidthKbps: 0}});
function options(overrides: Partial<DashboardBindingOptions> = {}): DashboardBindingOptions {
  return {api: {request: async () => emptyHistory() as never, requestRaw: async () => ({status: 200, headers: {}, body: page()})}, context, scope: {serverId: 'server', viewerId: 'viewer'}, role: 'owner', isCurrent: () => true, isMainProfile: () => true, artwork: () => undefined, effects: {openHome() {}, openHistory() {}, openSettings() {}, reportError() {}}, ...overrides};
}
test('Now playing requests 100 rows and retains an opaque continuation cursor', async () => {
  const paths: string[] = [];
  const b = createDashboardBindings(options({api: {...options().api, requestRaw: async path => {paths.push(path);return {status: 200, headers: {}, body: page()};}}}));
  try {
    await b.read.sessions();await b.read.sessions('next/+?=&');
    const queries = paths.map(path => new URL(path, 'https://server.invalid').searchParams);
    assert.equal(queries[0].get('limit'), '100');assert.equal(queries[0].get('cursor'), null);
    assert.equal(queries[1].get('limit'), '100');assert.equal(queries[1].get('cursor'), 'next/+?=&');
  } finally {b.dispose();}
});
test('administrator reads sessions only; secondary profile and member dispatch nothing', async () => {
  for (const change of [{role: 'member' as const}, {isMainProfile: () => false}]) {let calls = 0; const b = createDashboardBindings(options({...change, api: {request: async () => {calls++; return {} as never;}, requestRaw: async () => {calls++; return {} as never;}}})); await assert.rejects(async () => b.read.sessions()); await assert.rejects(async () => b.read.status()); assert.equal(calls, 0); b.dispose();}
  const b = createDashboardBindings(options({role: 'admin'})); assert.equal((await b.read.sessions()).totals.streams, 0);
  for (const read of [() => b.read.status(), () => b.read.people({period: 'all'}), () => b.read.report(reportQuery), () => b.read.telemetry('live'), () => b.read.plays({period: 'all'}), () => b.effects.stop('id'), () => b.effects.openHistory()]) await assert.rejects(async () => read()); b.dispose();
});
test('late private response after authority replacement is rejected and disposed source cannot read', async () => {
  let current = true, release!: (value: never) => void; const b = createDashboardBindings(options({isCurrent: () => current, api: {...options().api, request: async () => new Promise(resolve => {release = resolve;})}}));
  const pending = b.read.plays({period: 'all'}); current = false; release({entries: [], nextCursor: ''} as never); await assert.rejects(pending); b.dispose(); await assert.rejects(async () => b.read.sessions());
});
test('session scope mismatch rejects; history passes exact server query and opaque cursor', async () => {
  const b = createDashboardBindings(options({api: {...options().api, requestRaw: async () => ({status: 200, headers: {}, body: {...page(), scope: {...page().scope, serverId: 'wrong'}}})}})); await assert.rejects(b.read.sessions()); b.dispose();
  let path = ''; const c = createDashboardBindings(options({api: {...options().api, request: async request => {path = request; return emptyHistory() as never;}}})); await c.read.plays({period: 'all', media: 'audiobooks', accountId: 'account', sort: 'watched', direction: 'asc'}, 'opaque');
  const q = new URL(path, 'https://server.invalid').searchParams; assert.equal(q.get('limit'), '100'); assert.equal(q.get('sort'), 'watched'); assert.equal(q.get('cursor'), 'opaque'); assert.equal(q.get('accountId'), 'account'); assert.equal(q.get('media'), 'audiobooks'); assert.equal(q.get('timeZone'), context.timeZone); assert.equal(q.get('until'), context.until); c.dispose();
});
test('terminal private-read refusal synchronously retires every mounted consumer', async () => {
  let changed = 0;
  const b = createDashboardBindings(options({api: {...options().api, request: async () => {throw {status: 403};}}}));
  const off = b.subscribeAuthority!(() => changed++); await assert.rejects(b.read.plays({period: 'all'})); assert.equal(b.isCurrent(), false); assert.equal(changed, 1); off(); b.dispose();
});
test('a refused read tells the page once, so it draws the refusal; the management PIN closes nothing', async () => {
  const reported: unknown[] = [];
  const effects = (o: DashboardBindingOptions) => ({...o.effects, reportError: (error: unknown) => {reported.push(error);}});
  const refused = createDashboardBindings({...options(), effects: effects(options()), api: {request: async () => {throw {status: 403, code: 'forbidden'};}, requestRaw: async () => ({status: 403, headers: {}, body: {error: {code: 'forbidden'}}})}});
  await Promise.allSettled([refused.read.plays({period: 'all'}), refused.read.status(), refused.read.sessions()]);
  assert.equal(refused.isCurrent(), false);
  assert.deepEqual(reported, [{status: 403, code: 'forbidden'}]);
  refused.dispose(); reported.length = 0;
  const raw = createDashboardBindings({...options(), effects: effects(options()), api: {...options().api, requestRaw: async () => ({status: 403, headers: {}, body: {error: {code: 'forbidden'}}})}});
  await assert.rejects(raw.read.sessions()); assert.equal(raw.isCurrent(), false); assert.deepEqual(reported, [{status: 403, code: 'forbidden'}]);
  raw.dispose(); reported.length = 0;
  const signedOut = createDashboardBindings({...options(), effects: effects(options()), api: {...options().api, request: async () => {throw {status: 401, code: 'unauthorized'};}}});
  await assert.rejects(signedOut.read.plays({period: 'all'})); assert.equal(signedOut.isCurrent(), false); assert.deepEqual(reported, []);
  signedOut.dispose();
  const pin = {status: 403, code: 'profile_pin_manage'};
  const locked = createDashboardBindings({...options(), effects: effects(options()), api: {request: async () => {throw pin;}, requestRaw: async () => ({status: 403, headers: {}, body: {error: pin}})}});
  await assert.rejects(locked.read.plays({period: 'all'}), (error: unknown) => error === pin);
  await assert.rejects(locked.read.sessions(), (error: {code?: unknown}) => error.code === 'profile_pin_manage');
  assert.equal(locked.isCurrent(), true); assert.deepEqual(reported, []);
  locked.dispose();
});
test('shell authority notification clears current bindings before another request', () => {
  let notify!: () => void, current = true;
  const b = createDashboardBindings(options({isCurrent: () => current, subscribeAuthority: listener => {notify = listener; return () => {};}}));
  let changed = 0; b.subscribeAuthority!(() => changed++); current = false; notify(); assert.equal(changed, 1); assert.equal(b.isCurrent(), false); b.dispose();
});
test('one checked report dispatch supplies all aggregate panels and cannot override shell context', async () => {
  const paths: string[] = [];
  const b = createDashboardBindings(options({api: {...options().api, request: async path => {paths.push(path); return report() as never;}}}));
  const attemptedOverride = {...reportQuery, timeZone: 'Pacific/Auckland', until: '2026-10-22T13:00:00Z'};
  const out = await b.read.report(attemptedOverride);
  assert.equal(paths.length, 1); const url = new URL(paths[0], 'https://server.invalid'); assert.equal(url.pathname, '/v1/admin/dashboard/report');
  assert.equal(url.searchParams.get('timeZone'), context.timeZone); assert.equal(url.searchParams.get('until'), context.until); assert.equal(out.context.until, context.until);
  assert.equal(out.history.until, out.people.until); assert.equal(out.history.until, out.load.until); assert.equal(out.history.until, out.table.until); b.dispose();
});
test('Today stays distinct from24h and See full history carries the captured context', async () => {
  const paths: string[] = []; let carried: unknown;
  const today = {...context, period: 'today', from: '2026-10-22T00:00:00Z', buckets: [{start: '2026-10-22T00:00:00Z', end: context.until, partial: true}], entries: [], total: 0, nextCursor: ''};
  const b = createDashboardBindings(options({effects: {...options().effects, openHistory: value => {carried = value;}}, api: {...options().api, request: async path => {paths.push(path); return today as never;}}}));
  await b.read.plays({period: 'today'}); const url = new URL(paths[0], 'https://server.invalid'); assert.equal(url.searchParams.get('period'), 'today');
  b.effects.openHistory(); assert.equal(carried, b.context); assert.deepEqual(carried, context); assert.equal(Object.isFrozen(b.context), true); b.dispose();
});
test('fresh same-context history may include late facts; no immutable count is promised', async () => {  let calls = 0;
  const bounds = {...interval(), from: '2026-10-01T00:00:00Z', buckets: [{start: '2026-10-01T00:00:00Z', end: context.until, partial: true}]};
  const entry = {id: 'late', user: 'person', accountId: 'actual-account', kind: 'movie', title: 'Late known fact', libraryId: 'library', startedAt: '2026-10-22T11:59:00Z', positionMs: 1000, durationMs: 60000, watchedMs: 1000, completed: false};
  const b = createDashboardBindings(options({api: {...options().api, request: async () => {calls++; return {...bounds, entries: calls === 1 ? [] : [entry], total: calls === 1 ? 0 : 1, nextCursor: ''} as never;}}}));
  assert.equal((await b.read.plays({period: 'all'})).total, 0); assert.equal((await b.read.plays({period: 'all'})).total, 1); assert.equal(calls, 2); assert.equal(b.context.until, context.until); b.dispose();
});
const certificate = () => ({configured: false, authorityId: '', config: {enabled: false, publicPort: 443, publicAddress: '', revision: '0'}, state: 'unconfigured', environment: 'staging', tlsReady: false, publiclyTrusted: false, listenerBound: false, listenPort: 0, reachability: 'probe_required'});
test('the certificate alert retries through the canonical owner route and refuses after authority loss', async () => {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const b = createDashboardBindings(options({api: {...options().api, request: async (path, method, body) => {calls.push({path, method, body}); return certificate() as never;}}}));
  await b.effects.retryCertificate!();
  assert.deepEqual(calls.map(call => [call.path, call.method, call.body]), [['/v1/networking/certificate/retry', 'POST', {}]]);
  b.dispose();
  let current = true;
  const late = createDashboardBindings(options({isCurrent: () => current, api: {...options().api, request: async () => certificate() as never}}));
  current = false;
  await assert.rejects(async () => late.effects.retryCertificate!());
});

// FX114 regression source, UNRUN: a held canonical receipt cannot lend a write to a retired dialog.
test('retiring the original Move opening between GET and POST refuses the command', async () => {
  let release!: (value: never) => void; const calls: string[] = [], life = new AbortController();
  const receipt = {sessionId: 'original', revision: '7', state: 'playing', positionMs: 1000, durationMs: 60000, actions: ['seek']};
  const b = createDashboardBindings(options({api: {...options().api, request: async (path, method) => {
    calls.push(`${method ?? 'GET'} ${path}`);
    return new Promise(resolve => {release = resolve;});
  }}}));
  const pending = b.effects.seek!('original', 25000, life.signal);
  life.abort(); release(receipt as never); await assert.rejects(pending);
  assert.equal(calls.length, 1); assert.match(calls[0], /^GET /); b.dispose();
});
test('Move keeps the real session revision and absolute target on the canonical route', async () => {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const receipt = {sessionId: 'original', revision: '7', state: 'playing', positionMs: 1000, durationMs: 60000, actions: ['seek']};
  const b = createDashboardBindings(options({api: {...options().api, request: async (path, method, body) => {calls.push({path, method, body}); return receipt as never;}}}));
  await b.effects.seek!('original', 25000, new AbortController().signal);
  assert.equal(calls.length, 2); assert.equal(calls[0].path, calls[1].path);
  assert.deepEqual(calls[1].body, {action: 'seek', expectedRevision: '7', positionMs: 25000}); b.dispose();
});

test('a valid controls document for another session cannot redirect a Move write', async () => {
  const calls: string[] = [];
  const other = {sessionId: 'replacement', revision: '8', state: 'playing', positionMs: 1000, durationMs: 60000, actions: ['seek']};
  const b = createDashboardBindings(options({api: {...options().api, request: async (path, method) => {calls.push(`${method ?? 'GET'} ${path}`); return other as never;}}}));
  await assert.rejects(b.effects.seek!('original', 25000, new AbortController().signal), /dashboard_controls_session_mismatch/);
  assert.equal(calls.length, 1); assert.match(calls[0], /^GET /); b.dispose();
});

test('Dashboard actual session/history GETs share resync admission and queued reads retain their original lifetime', async () => {
  const {EventsClient} = await import('../../packages/client-core/src/playback-v1/events.ts');
  let release!: () => void; const held = new Promise<void>(resolve => {release = resolve;});
  const events = new EventsClient({http: {send: async () => {throw new Error('No event reads');}}});
  const occupied = Array.from({length: 4}, () => events.resyncReads.run(new AbortController().signal, () => held));
  let requests = 0;
  const b = createDashboardBindings(options({events, api: {...options().api, requestRaw: async () => {requests++; return {status: 200, headers: {}, body: page()};}}}));
  try {
    const pending = b.read.sessions();
    for (let i = 0; i < 30; i++) await Promise.resolve();
    assert.equal(requests, 0);
    release(); await Promise.all(occupied); await pending; assert.equal(requests, 1);
  } finally {release(); b.dispose(); events.resyncReads.dispose();}
});
test('a moved viewer fence on a later session read answers and retires nothing; another server still ends the lifetime', async () => {
  let reads = 0, changed = 0;
  const b = createDashboardBindings(options({api: {...options().api, requestRaw: async () => ({status: 200, headers: {}, body: {...page(), scope: {serverId: 'server', viewerFence: (reads++ ? 'b' : 'a').repeat(64)}}})}}));
  b.subscribeAuthority!(() => changed++);
  await b.read.sessions(); const moved = await b.read.sessions();
  assert.equal(moved.scope.viewerFence, 'b'.repeat(64)); assert.equal(b.isCurrent(), true); assert.equal(changed, 0); b.dispose();
  let n = 0; const other = createDashboardBindings(options({api: {...options().api, requestRaw: async () => ({status: 200, headers: {}, body: {...page(), scope: {serverId: n++ ? 'wrong' : 'server', viewerFence: 'a'.repeat(64)}}})}}));
  await other.read.sessions(); await assert.rejects(other.read.sessions()); assert.equal(other.isCurrent(), false); other.dispose();
});

// Client data step 6: the Dashboard's status, report and first page of plays are read through the viewer's cache.
const status = () => ({alerts: [], observedAt: '2026-10-22T12:00:00Z', scans: [], tasks: {count: 0, items: []}});
function cached() {
  const paths: string[] = []; let body: unknown = emptyHistory(), fail: unknown;
  const hub = new FreshnessHub({settleMs: 0});
  const queries: ViewerQueries = createViewerQueries({scope: {serverId: 'server', viewerId: 'viewer'}, hub, api: {request: async () => { throw new Error('unused'); }}, gcTimeMs: Infinity});
  const open = () => createDashboardBindings(options({queries, api: {...options().api, request: async path => {paths.push(path); if (fail) throw fail; return (path.includes('/status') ? status() : path.includes('/report') ? report() : body) as never;}}}));
  return {paths, hub, queries, open, set: (next: unknown) => {body = next;}, refuse: (error: unknown) => {fail = error;}};
}
test('a Dashboard opened again shows its status, report and first page of plays at once; a later read, a hint or a write here asks the server', async () => {
  const c = cached(); const first = c.open();
  await first.read.status(undefined, false); await first.read.report(reportQuery, undefined, false); await first.read.plays({period: 'all'}, undefined, undefined, false);
  assert.equal(c.paths.length, 3); first.dispose();
  const again = c.open();
  await again.read.status(undefined, false); await again.read.report(reportQuery, undefined, false); await again.read.plays({period: 'all'}, undefined, undefined, false);
  assert.equal(c.paths.length, 3, 'a reopened Dashboard reads nothing while the copies are current');
  await again.read.status(); assert.equal(c.paths.length, 4, 'a poll or an event asks the server');
  c.hub.accept({id: 'e1', type: 'playback.history', revision: '1'} as never);
  await again.read.plays({period: 'all'}, undefined, undefined, false); assert.equal(c.paths.length, 5, 'a hint makes the held page owed');
  await again.read.plays({period: 'all'}, undefined, undefined, false); assert.equal(c.paths.length, 5);
  await again.read.plays({period: 'all'}, 'cursor', undefined, false); assert.equal(c.paths.length, 6, 'a later page is a direct read');
  await again.read.plays({period: 'all'}, 'cursor', undefined, false); assert.equal(c.paths.length, 7, 'and never held');
  c.queries.moved([]); again.dispose();
});
test('two Dashboards of one viewer share one read; another range or sort is its own document', async () => {
  const c = cached(); const a = c.open(), b = c.open();
  await Promise.all([a.read.plays({period: 'all'}, undefined, undefined, false), b.read.plays({period: 'all'}, undefined, undefined, false)]);
  assert.equal(c.paths.length, 1);
  await a.read.plays({period: 'all', sort: 'watched', direction: 'asc'}, undefined, undefined, false); assert.equal(c.paths.length, 2);
  await a.read.plays({period: '7d'} as never, undefined, undefined, false).catch(() => undefined); assert.equal(c.paths.length, 3);
  a.dispose(); b.dispose();
});
test('a refused read shows no held copy: the 403 still ends the Dashboard and reaches the page', async () => {
  const c = cached(); const reported: unknown[] = [];
  const first = c.open(); await first.read.status(undefined, false); first.dispose();
  c.refuse({status: 403, code: 'forbidden'});
  const refused = createDashboardBindings({...options(), queries: c.queries, effects: {...options().effects, reportError: error => {reported.push(error);}}, api: {...options().api, request: async () => {throw {status: 403, code: 'forbidden'};}}});
  await assert.rejects(refused.read.status());
  assert.equal(refused.isCurrent(), false); assert.deepEqual(reported, [{status: 403, code: 'forbidden'}]);
  refused.dispose();
});
test('stopping a session here marks what the Dashboard was built from owed', async () => {
  const c = cached(); const b = createDashboardBindings(options({queries: c.queries, api: {...options().api, request: async path => {c.paths.push(path); return status() as never;}, requestRaw: async () => ({status: 204, headers: {}, body: undefined})}}));
  await b.read.status(undefined, false); await b.read.status(undefined, false); assert.equal(c.paths.length, 1);
  await b.effects.stop('session');
  await b.read.status(undefined, false); assert.equal(c.paths.length, 2, 'the held status is read again');
  b.dispose();
});
