import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDashboardBindings, type DashboardBindingOptions} from '../src/screens/dashboard/bindings.ts';
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
