import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDashboardBindings, type DashboardBindingOptions} from '../src/screens/dashboard/bindings.ts';
import {FreshnessHub} from '../../packages/client-core/src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../../packages/client-core/src/query/viewer-queries.ts';
import {captureReportContext} from '../../packages/client-core/src/report-context.ts';
import {METRIC_NAMES} from '../../packages/client-core/src/telemetry.ts';
const context = captureReportContext('UTC', Date.UTC(2026, 9, 22, 12, 17, 31));
const interval = (period = 'all') => ({period, ...context, from: context.until, buckets: []});
const emptyHistory = () => ({...interval(), entries: [], items: [], total: 0, nextCursor: ''});
function report() {
  const bounds = interval();
  return {context, history: {...bounds, topKind: 'movies', top: [], busiestTimes: Array.from({length: 7}, () => Array(24).fill(0)), quietestStartHour: null}, people: {...bounds, items: [], nextCursor: ''}, summary: {...bounds, plays: 0, people: 0, watchedMs: 0, converted: 0, mostPlayed: [], mostActive: []}, topPlayed: {...bounds, topKind: 'movies', items: []}, table: emptyHistory(), load: {...bounds, window: 'all', observedAt: Date.parse(context.until), series: Object.fromEntries(METRIC_NAMES.map(k => [k, []])), status: Object.fromEntries(METRIC_NAMES.map(k => [k, {status: 'unavailable', value: 0}]))}};
}
const reportQuery = {period: 'all', window: 'all', peoplePeriod: 'all', topPeriod: 'all', topKind: 'movies'} as const;
const page = () => ({scope: {serverId: 'server', viewerFence: 'a'.repeat(64)}, observedAt: '2026-10-23T00:00:00Z', items: [], page: {limit: 10, start: 0, total: 0, revision: '1'}, totals: {streams: 0, atHome: 0, away: 0, paused: 0, converting: 0, people: 0, bandwidthKbps: 0}});
function options(overrides: Partial<DashboardBindingOptions> = {}): DashboardBindingOptions {
  return {api: {request: async (path: string) => (path.startsWith('/v1/admin/sessions') ? {items: []} : emptyHistory()) as never, requestRaw: async () => ({status: 200, headers: {}, body: page()})}, context, scope: {serverId: 'server', viewerId: 'viewer'}, role: 'owner', isCurrent: () => true, isMainProfile: () => true, artwork: () => undefined, effects: {openHome() {}, openHistory() {}, openSettings() {}, reportError() {}}, ...overrides};
}
test('Now playing reads every stream at once from the rebuilt server, in the card\'s shape, with its totals', async () => {
  const paths: string[] = [];
  const stream = (id: string, more: Record<string, unknown>) => ({id, kind: 'vod', state: 'playing', positionMs: 1000, username: 'max', deviceName: 'Living room TV', away: false,
    route: 'original', delivery: {video: {action: 'direct'}, audio: {action: 'direct'}}, startedAt: '2026-11-11T10:00:00Z', ...more});
  const answer = {items: [
    stream('a', {title: 'Heat', itemId: 'itm_heat', durationMs: 60000, profile: 'Kids', away: true, bitrateKbps: 8000, route: 'converted', delivery: {video: {action: 'transcode', reasons: ['bitrate']}, audio: {action: 'copy'}}}),
    stream('b', {username: 'ada', state: 'paused', bitrateKbps: 2000}),
  ]};
  const b = createDashboardBindings(options({api: {...options().api, request: async path => {paths.push(path); return answer as never;}}}));
  try {
    const out = await b.read.sessions();
    assert.deepEqual(paths, ['/v1/admin/sessions']); assert.equal(out.nextCursor, '');
    const [heat, other] = out.items;
    assert.equal(heat.user, 'max'); assert.equal(heat.profile, 'Kids'); assert.equal(heat.device, 'Living room TV'); assert.equal(heat.title, 'Heat');
    assert.equal(heat.itemId, 'itm_heat'); assert.equal(heat.location, 'remote'); assert.equal(heat.durationMs, 60000); assert.equal(heat.decision.video?.action, 'transcode');
    assert.equal(other.user, 'ada'); assert.equal(other.profile, 'ada', 'no profile name of its own: the account\'s'); assert.equal(other.location, 'local'); assert.equal(other.title, '');
    assert.deepEqual(out.totals, {streams: 2, paused: 1, converting: 1, bandwidthKbps: 10000, people: 2, atHome: 1, away: 1});
  } finally {b.dispose();}
  // A stream whose profile was just deleted has no name for its last seconds: its card shows none, and the list still shows.
  const nameless = createDashboardBindings(options({api: {...options().api, request: async () => ({items: [stream('a', {title: 'Heat'}), stream('b', {username: ''})]}) as never}}));
  try {
    const out = await nameless.read.sessions();
    assert.deepEqual(out.items.map(s => s.user), ['max', '']); assert.equal(out.totals.streams, 2);
  } finally {nameless.dispose();}
  const broken = createDashboardBindings(options({api: {...options().api, request: async () => ({items: [{id: 'a'}]}) as never}}));
  await assert.rejects(broken.read.sessions(), /invalid_now_playing/); broken.dispose();
});
test('an administrator reads streams and Play history only; a secondary profile and a member dispatch nothing', async () => {
  for (const change of [{role: 'member' as const}, {isMainProfile: () => false}]) {let calls = 0; const b = createDashboardBindings(options({...change, api: {request: async () => {calls++; return {} as never;}, requestRaw: async () => {calls++; return {} as never;}}})); await assert.rejects(async () => b.read.sessions()); await assert.rejects(async () => b.read.status()); assert.equal(calls, 0); b.dispose();}
  const b = createDashboardBindings(options({role: 'admin'})); assert.equal((await b.read.sessions()).totals.streams, 0); assert.deepEqual((await b.read.plays({period: 'all'})).entries, []);
  for (const read of [() => b.read.status(), () => b.read.people({period: 'all'}), () => b.read.report(reportQuery), () => b.read.telemetry('live'), () => b.effects.openHistory()]) await assert.rejects(async () => read()); b.dispose();
});
test('an administrator may stop a stream (console definition, rule 1); a member and a secondary profile may not', async () => {
  const stops: unknown[] = [];
  const api = {...options().api, request: async (path: string, method?: string, body?: unknown) => {
    if (method === 'DELETE') {stops.push({path, body}); return undefined as never;}
    return {items: []} as never;}};
  const admin = createDashboardBindings(options({role: 'admin', api}));
  await admin.effects.stop('session', 'Closing for the night');
  assert.deepEqual(stops, [{path: '/v1/admin/sessions/session', body: {message: 'Closing for the night'}}]);
  admin.dispose();
  for (const change of [{role: 'member' as const}, {isMainProfile: () => false}]) {
    const b = createDashboardBindings(options({...change, api}));
    await assert.rejects(async () => b.effects.stop('session'));
    b.dispose();
  }
  assert.equal(stops.length, 1, 'only the administrator\'s stop was sent');
});
test('late private response after authority replacement is rejected and disposed source cannot read', async () => {
  let current = true, release!: (value: never) => void; const b = createDashboardBindings(options({isCurrent: () => current, api: {...options().api, request: async () => new Promise(resolve => {release = resolve;})}}));
  const pending = b.read.plays({period: 'all'}); current = false; release({entries: [], nextCursor: ''} as never); await assert.rejects(pending); b.dispose(); await assert.rejects(async () => b.read.sessions());
});
test('history passes exact server query and opaque cursor', async () => {
  let path = ''; const c = createDashboardBindings(options({api: {...options().api, request: async request => {path = request; return emptyHistory() as never;}}})); await c.read.plays({period: 'all', media: 'audiobooks', accountId: 'account', sort: 'watched', direction: 'asc'}, 'opaque');
  const url = new URL(path, 'https://server.invalid'), q = url.searchParams; assert.equal(url.pathname, '/v1/admin/play-history');
  assert.equal(q.get('cursor'), 'opaque'); assert.equal(q.get('person'), 'account'); assert.equal(q.get('kind'), 'audiobook'); assert.equal(q.get('sort'), null); c.dispose();
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
  const raw = createDashboardBindings({...options(), effects: effects(options()), api: {...options().api, request: async (path: string) => {if (path.startsWith('/v1/admin/sessions')) throw {status: 403, code: 'forbidden'}; return emptyHistory() as never;}}});
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
test('Today starts at midnight and See full history carries the captured context', async () => {
  const paths: string[] = []; let carried: unknown;
  const today = {items: []};
  const b = createDashboardBindings(options({effects: {...options().effects, openHistory: value => {carried = value;}}, api: {...options().api, request: async path => {paths.push(path); return today as never;}}}));
  await b.read.plays({period: 'today'}); const url = new URL(paths[0], 'https://server.invalid'); const midnight = new Date(); midnight.setHours(0, 0, 0, 0);
  assert.equal(Date.parse(url.searchParams.get('from')!), midnight.getTime());
  b.effects.openHistory(); assert.equal(carried, b.context); assert.deepEqual(carried, context); assert.equal(Object.isFrozen(b.context), true); b.dispose();
});
test('a fresh read of the history shows a play that ended since', async () => {
  let calls = 0;
  const play = {playedAt: '2026-10-22T11:59:00Z', username: 'person', accountId: 'actual-account', kind: 'movie', title: 'Late known fact', libraryId: 'library', watchedMs: 1000, away: false};
  const b = createDashboardBindings(options({api: {...options().api, request: async () => {calls++; return {items: calls === 1 ? [] : [play]} as never;}}}));
  assert.equal((await b.read.plays({period: 'all'})).entries.length, 0); assert.equal((await b.read.plays({period: 'all'})).entries.length, 1); assert.equal(calls, 2); b.dispose();
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

test('the rebuilt server offers no Move to, Pause or Resume of someone\'s stream: none is bound', () => {
  const b = createDashboardBindings(options());
  assert.equal(b.effects.seek, undefined); assert.equal(b.effects.transport, undefined); assert.equal(b.read.controls, undefined); b.dispose();
});
test('Dashboard actual session/history GETs share resync admission and queued reads retain their original lifetime', async () => {
  const {EventsClient} = await import('../../packages/client-core/src/playback-v1/events.ts');
  let release!: () => void; const held = new Promise<void>(resolve => {release = resolve;});
  const events = new EventsClient({http: {send: async () => {throw new Error('No event reads');}}});
  const occupied = Array.from({length: 4}, () => events.resyncReads.run(new AbortController().signal, () => held));
  let requests = 0;
  const b = createDashboardBindings(options({events, api: {...options().api, request: async () => {requests++; return {items: []} as never;}}}));
  try {
    const pending = b.read.sessions();
    for (let i = 0; i < 30; i++) await Promise.resolve();
    assert.equal(requests, 0);
    release(); await Promise.all(occupied); await pending; assert.equal(requests, 1);
  } finally {release(); b.dispose(); events.resyncReads.dispose();}
});
// Client data step 6: the Dashboard's status, report and first page of plays are read through the viewer's cache.
const status = () => ({alerts: [], observedAt: '2026-10-22T12:00:00Z', scans: [], tasks: {count: 0, items: []}});
function cached() {
  const paths: string[] = []; let body: unknown = emptyHistory(), fail: unknown, gate: Promise<void> | undefined;
  const hub = new FreshnessHub({settleMs: 0});
  // One server behind both: the cache's own requester and each Dashboard's (which ends when that Dashboard closes).
  const serve = async (path: string, signal?: AbortSignal) => {paths.push(path); if (gate) await gate; signal?.throwIfAborted(); if (fail) throw fail; return (path === '/v1/admin/dashboard' ? status() : path.includes('/report') ? report() : body) as never;};
  const queries: ViewerQueries = createViewerQueries({scope: {serverId: 'server', viewerId: 'viewer'}, hub, api: {request: async (path, _method, _body, signal) => serve(path, signal)}, gcTimeMs: Infinity});
  const open = () => createDashboardBindings(options({queries, api: {...options().api, request: async (path, _method, _body, signal) => serve(path, signal)}}));
  return {paths, hub, queries, open, set: (next: unknown) => {body = next;}, refuse: (error: unknown) => {fail = error;}, hold: () => {let release!: () => void; gate = new Promise(resolve => {release = resolve;}); return () => {gate = undefined; release();};}};
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
test('two Dashboards of one viewer share one read; another range is its own document', async () => {
  const c = cached(); const a = c.open(), b = c.open();
  await Promise.all([a.read.plays({period: 'all'}, undefined, undefined, false), b.read.plays({period: 'all'}, undefined, undefined, false)]);
  assert.equal(c.paths.length, 1);
  await a.read.plays({period: 'all', sort: 'watched', direction: 'asc'}, undefined, undefined, false); assert.equal(c.paths.length, 1, 'the server has no sort');
  await a.read.plays({period: '7d'}, undefined, undefined, false); assert.equal(c.paths.length, 2);
  a.dispose(); b.dispose();
});
test('the Dashboard that started a shared read closes, and another one waiting on it still gets the status, report and plays', async () => {
  const c = cached(), release = c.hold(); const first = c.open(), second = c.open();
  const started = [first.read.status(undefined, false), first.read.report(reportQuery, undefined, false), first.read.plays({period: 'all'}, undefined, undefined, false)];
  await new Promise(resolve => setTimeout(resolve, 5));
  const waiting = [second.read.status(undefined, false), second.read.report(reportQuery, undefined, false), second.read.plays({period: 'all'}, undefined, undefined, false)];
  first.dispose(); release();
  await Promise.allSettled(started);
  const [status, report, plays] = await Promise.all(waiting);
  assert.ok(status && report && plays, 'the second Dashboard is answered');
  assert.equal(c.paths.length, 3, 'one read of each, shared');
  assert.equal(second.isCurrent(), true);
  second.dispose();
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
test('on the cached reads, a 401 ends the Dashboard without a refusal page, and the management PIN ends nothing', async () => {
  for (const read of ['status', 'report', 'plays'] as const) {
    const go = (b: ReturnType<typeof createDashboardBindings>) => read === 'status' ? b.read.status() : read === 'report' ? b.read.report(reportQuery) : b.read.plays({period: 'all'});
    const signedOut = cached(), reported: unknown[] = [];
    signedOut.refuse({status: 401, code: 'unauthorized'});
    const a = createDashboardBindings({...options(), queries: signedOut.queries, effects: {...options().effects, reportError: error => {reported.push(error);}}});
    await assert.rejects(go(a));
    assert.equal(a.isCurrent(), false, read + ': a 401 ends the Dashboard'); assert.deepEqual(reported, [], read + ': and draws no refusal');
    a.dispose();
    const locked = cached(), told: unknown[] = [];
    locked.refuse({status: 403, code: 'profile_pin_manage'});
    const b = createDashboardBindings({...options(), queries: locked.queries, effects: {...options().effects, reportError: error => {told.push(error);}}});
    await assert.rejects(go(b));
    assert.equal(b.isCurrent(), true, read + ': the PIN closes nothing'); assert.deepEqual(told, [], read + ': and is the shell\'s to ask for');
    b.dispose();
  }
});
test('stopping a session here marks what the Dashboard was built from owed', async () => {
  const stops: unknown[] = [];
  const c = cached(); const b = createDashboardBindings(options({queries: c.queries, api: {...options().api, request: async (path, method, body) => {
    if (method === 'DELETE') {stops.push({path, body}); return undefined as never;}
    c.paths.push(path); return status() as never;}}}));
  await b.read.status(undefined, false); await b.read.status(undefined, false); assert.equal(c.paths.length, 1);
  await b.effects.stop('session', '  Closing for the night ');
  assert.deepEqual(stops, [{path: '/v1/admin/sessions/session', body: {message: 'Closing for the night'}}]);
  await b.read.status(undefined, false); assert.equal(c.paths.length, 2, 'the held status is read again');
  b.dispose();
});
test('stopping a stream that has already ended is not an error', async () => {
  const b = createDashboardBindings(options({api: {...options().api, request: async (_path, method) => {if (method === 'DELETE') throw {status: 404, code: 'not_found'}; return {items: []} as never;}}}));
  await b.effects.stop('gone'); b.dispose();
});
