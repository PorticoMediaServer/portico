import test from 'node:test';
import assert from 'node:assert/strict';
import {routePath} from '../../contracts/src/routes.ts';
import {ActivitySummaryService,DashboardSessionsService} from '../src/admin/activity-summary/service.ts';
import type {ActivitySummaryScope, ActivitySummarySource, ActivitySummaryTimers} from '../src/admin/activity-summary/types.ts';
import type {ServerEvent} from '../src/playback-v1/types.ts';
import type {V1Http, V1Request, V1Response} from '../src/playback-v1/http.ts';

const scope: ActivitySummaryScope = {serverId: 'server', accountId: 'owner-account', profileId: 'owner-profile', authority: 'local', role: 'owner', sessionFamilyId: 'family', authorizationEpoch: '1'};
const wire = (extra: Record<string, unknown> = {}): any => ({scope: {serverId: 'server', viewerFence: 'a'.repeat(64)}, observedAt: '2026-10-22T07:00:00.000000001Z', serverName: 'Private server', streams: {count: 0, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: []}, scans: [{libraryId: 'movies', libraryName: 'Movies', kind: 'movie', phase: 'discovering', discovered: 12, analyzed: 4, current: 'private-film.mkv'}], tasks: {count: 0, items: []}, update: {available: false, state: 'unconfigured', current: '0.1.0-dev'}, majorIssues:[], health: {degraded: false}, ...extra});
const ok = (body = wire()): V1Response => ({status: 200, headers: {}, body});
function deferred<T>() { let resolve!: (value: T) => void, reject!: (error: unknown) => void; const promise = new Promise<T>((a, b) => {resolve = a; reject = b;}); return {promise, resolve, reject}; }
async function flush() { for (let n = 0; n < 12; n++) await Promise.resolve(); }
class Clock implements ActivitySummaryTimers {
  value = 0; sequence = 0; tasks = new Map<number, {at: number; fn: () => void}>();
  now = () => this.value;
  setTimer = (fn: () => void, ms: number) => {const id = ++this.sequence; this.tasks.set(id, {at: this.value + ms, fn}); return id;};
  clearTimer = (id: unknown) => {this.tasks.delete(id as number);};
  async advance(ms: number) {
    const end = this.value + ms;
    for (let guard = 0; guard < 100; guard++) {
      const next = [...this.tasks.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) {this.value = end; await flush(); return;}
      this.value = next[1].at; this.tasks.delete(next[0]); next[1].fn(); await flush();
    }
    throw new Error('unexpected timer loop');
  }
}
class Source {
  value: ActivitySummarySource | null;
  listeners = new Set<() => void>();
  constructor(value: ActivitySummarySource | null) {this.value = value;}
  getSnapshot = () => this.value;
  subscribe = (fn: () => void) => {this.listeners.add(fn); return () => {this.listeners.delete(fn);};};
  set(value: ActivitySummarySource | null) {this.value = value; for (const fn of [...this.listeners]) fn();}
}
class Feed {
  listeners = new Map<string, Set<(event: ServerEvent) => void>>(); resync = new Set<() => void>();
  on(type: string, fn: (event: ServerEvent) => void) {const set = this.listeners.get(type) ?? new Set(); set.add(fn); this.listeners.set(type, set); return () => {set.delete(fn);};}
  onResync(fn: () => void) {this.resync.add(fn); return () => {this.resync.delete(fn);};}
  emit(type: string) {for (const fn of [...this.listeners.get(type) ?? []]) fn({id: 'event', type, revision: '1'});}
  count() {return [...this.listeners.values()].reduce((n, s) => n + s.size, this.resync.size);}
}
function harness(handler: (request: V1Request, index: number) => V1Response | Promise<V1Response> = () => ok(), initial: ActivitySummaryScope | null = scope) {
  const requests: V1Request[] = [], clock = new Clock(), feed = new Feed();
  const http: V1Http = {send: async request => {requests.push(request); return handler(request, requests.length);}};
  const source = new Source(initial ? {http, scope: {...initial}} : null);
  const service = new ActivitySummaryService({source, timers: clock, timeoutMs: 5000});
  return {service, source, http, clock, requests, feed, activate() {service.setActive(true); service.attach(feed);}};
}

test('inactive, anonymous, member and administrator stores never read or subscribe to owner hints', async () => {
  for (const initial of [null, {...scope, role: 'member'}, {...scope, role: 'admin'}, {...scope, role: 'administrator'}]) {
    const h = harness(undefined, initial); h.activate(); await h.service.load(); await h.service.revalidate(); h.feed.emit('job.updated'); await h.clock.advance(2000);
    assert.equal(h.requests.length, 0); assert.equal(h.feed.count(), 0); assert.deepEqual(h.service.getSnapshot(), {phase: 'unavailable'}); h.service.dispose();
  }
  const h = harness(); await h.service.load(); h.service.attach(h.feed);
  assert.equal(h.requests.length, 0); assert.equal(h.feed.count(), 0); h.service.dispose();
});
test('owner uses the actual generated route and canonical HTTP request with private revalidation, without fanout', async () => {
  const h = harness(); h.activate(); const first = h.service.load(), second = h.service.load(); assert.equal(first, second); await first;
  assert.equal(h.requests.length, 1); assert.equal(h.requests[0].path, routePath('get_admin_activity')); assert.equal(h.requests[0].method, 'GET');
  assert.deepEqual(h.requests[0].headers, {'Cache-Control': 'no-cache, no-store'}); assert.equal(h.requests[0].body, undefined);
  assert.equal(h.service.getSnapshot().phase, 'ready'); assert.equal(h.service.getSnapshot().document?.scans[0].current, 'private-film.mkv');
  await h.clock.advance(20_000); assert.equal(h.requests.length, 1); assert.equal(h.clock.tasks.size, 0); h.service.dispose();
});
test('every source identity dimension synchronously clears private facts and aborts an outstanding read', async () => {
  const changes: Partial<ActivitySummaryScope>[] = [
    {serverId: 'other-server'}, {accountId: 'other-account'}, {profileId: 'other-profile'}, {authority: 'hosted'},
    {sessionFamilyId: 'other-family'}, {authorizationEpoch: '2'}, {role: 'admin'},
  ];
  for (const change of changes) {
    const pending = deferred<V1Response>(); const h = harness((_r, n) => n === 1 ? ok() : pending.promise); h.activate(); await h.service.load();
    const read = h.service.load(); await flush(); h.source.set({http: h.http, scope: {...scope, ...change}});
    assert.equal(h.service.getSnapshot().document, undefined); assert.equal(h.requests[1].signal?.aborted, true); assert.equal(h.feed.count(), 0);
    pending.resolve(ok()); await read;
    assert.equal(h.service.getSnapshot().document, undefined); assert.notEqual(h.service.getSnapshot().phase, 'ready'); h.service.dispose();
  }
});
test('silent in-place source mutation is detected before snapshot access and before completion', async () => {
  const pending = deferred<V1Response>(), h = harness((_r, n) => n === 1 ? ok() : pending.promise); h.activate(); await h.service.load();
  const read = h.service.load(); await flush(); (h.source.value!.scope as {authorizationEpoch: string}).authorizationEpoch = '2';
  assert.equal(h.service.getSnapshot().document, undefined); pending.resolve(ok()); await read; assert.equal(h.service.getSnapshot().document, undefined); h.service.dispose();
  const other = deferred<V1Response>(), j = harness(() => other.promise); j.activate(); const loading = j.service.load(); await flush(); j.source.value = null; other.resolve(ok()); await loading;
  assert.deepEqual(j.service.getSnapshot(), {phase: 'unavailable'}); j.service.dispose();
});
test('source transport replacement and sign-out clear retained body and ignore old feed callbacks', async () => {
  const h = harness(); h.activate(); await h.service.load();
  const oldHint = [...h.feed.listeners.get('admin.sessions')!][0];
  h.source.set({http: {send: async () => ok()}, scope: {...scope}});
  assert.equal(h.service.getSnapshot().document, undefined); oldHint({id: 'stale', type: 'admin.sessions'}); assert.equal(h.feed.count(), 0);
  h.source.set(null); await h.clock.advance(2000);
  assert.equal(h.requests.length, 1); assert.deepEqual(h.service.getSnapshot(), {phase: 'unavailable'}); h.service.dispose();
});
test('denial never retains private owner body or error current payload and blocks further hint reads', async () => {
  for (const status of [401, 403]) {
    const h = harness((_r, n) => n === 1 ? ok() : {status, headers: {}, body: {error: {code: 'not_permitted', current: wire(), message: 'private-film.mkv'}}});
    h.activate(); await h.service.load(); await h.service.revalidate();
    assert.deepEqual(h.service.getSnapshot(), {phase: 'error', error: {code: 'not_permitted'}});
    assert.equal(JSON.stringify(h.service.getSnapshot()).includes('private'), false);
    h.feed.emit('job.updated'); await h.service.load(); await h.clock.advance(10_000); assert.equal(h.requests.length, 2); assert.equal(h.feed.count(), 0);
    h.source.set({http: h.http, scope: {...scope, authorizationEpoch: '2'}}); assert.equal(h.service.getSnapshot().document, undefined); h.service.dispose();
  }
});
test('wrong server or changed viewer authority fence erases the body without accepting the new scope', async () => {
  for (const changed of [{serverId: 'other-server', viewerFence: 'a'.repeat(64)}, {serverId: 'server', viewerFence: 'b'.repeat(64)}]) {
    const h = harness((_r, n) => ok(n === 1 ? wire() : wire({scope: changed}))); h.activate(); await h.service.load(); await h.service.load();
    assert.deepEqual(h.service.getSnapshot(), {phase: 'error', error: {code: 'permission_changed'}}); h.service.dispose();
  }
});
test('observation revisions compare all nanoseconds and never replace a newer summary with an older completion', async () => {
  const h = harness((_r, n) => ok(wire({observedAt: n === 1 ? '2026-10-22T07:00:00.000000002Z' : '2026-10-22T07:00:00.000000001Z'})));
  h.activate(); await h.service.load(); await h.service.load();
  assert.deepEqual(h.service.getSnapshot(), {phase: 'error', error: {code: 'invalid_activity_summary'}}); h.service.dispose();
});
test('cancel and deactivate erase the snapshot and prevent a transport ignoring abort from releasing its completion', async () => {
  for (const stop of [(s: ActivitySummaryService) => s.cancel(), (s: ActivitySummaryService) => s.setActive(false), (s: ActivitySummaryService) => s.dispose()]) {
    const pending = deferred<V1Response>(), h = harness((_r, n) => n === 1 ? ok() : pending.promise); h.activate(); await h.service.load(); const read = h.service.load(); await flush(); stop(h.service);
    assert.equal(h.service.getSnapshot().document, undefined); assert.equal(h.requests[1].signal?.aborted, true); pending.resolve(ok()); await read;
    assert.equal(h.service.getSnapshot().document, undefined); assert.equal(h.feed.count(), 0); h.service.dispose();
  }
});
test('external cancellation and read deadline clear previous owner facts without retaining raw transport errors', async () => {
  const pending = deferred<V1Response>(), h = harness((_r, n) => n === 1 ? ok() : pending.promise); h.activate(); await h.service.load();
  const controller = new AbortController(), read = h.service.revalidate({signal: controller.signal}); await flush(); controller.abort(); await read;
  assert.equal(h.service.getSnapshot().document, undefined); pending.resolve(ok()); await flush(); assert.equal(h.service.getSnapshot().document, undefined); h.service.dispose();
  const never = deferred<V1Response>(), j = harness(() => never.promise); j.activate(); const timed = j.service.load(); await flush(); await j.clock.advance(5000); await timed;
  assert.deepEqual(j.service.getSnapshot(), {phase: 'error', error: {code: 'request_timeout'}}); assert.equal(j.requests[0].signal?.aborted, true);
  never.resolve(ok()); await flush(); assert.equal(j.service.getSnapshot().document, undefined); j.service.dispose();
});
test('settled quiet completion rechecks actual source after waiting for input', async () => {
  const waiting = deferred<void>(), h = harness(); h.activate(); await h.service.load();
  const read = h.service.revalidate({settle: () => waiting.promise}); await flush(); h.source.set(null); waiting.resolve(); await read;
  assert.deepEqual(h.service.getSnapshot(), {phase: 'unavailable'}); h.service.dispose();
});
test('real administrative hint types coalesce at most once per second and resync rereads without opening a feed', async () => {
  const h = harness(); h.activate(); await h.service.load();
  for (const type of ['admin.sessions', 'library.scan.updated', 'job.updated', 'operation.updated', 'server.updated', 'list.changed']) h.feed.emit(type);
  await h.clock.advance(999); assert.equal(h.requests.length, 1);
  await h.clock.advance(1); assert.equal(h.requests.length, 2);
  for (const fn of h.feed.resync) fn(); await h.clock.advance(999); assert.equal(h.requests.length, 2);
  await h.clock.advance(1); assert.equal(h.requests.length, 3);
  h.feed.emit('personal.changed'); await h.clock.advance(20_000); assert.equal(h.requests.length, 3); assert.equal(h.clock.tasks.size, 0); h.service.dispose();
});
test('hints during a slow read produce one trailing current-source read, with no burst or polling', async () => {
  const pending = deferred<V1Response>(), h = harness((_r, n) => n === 1 ? pending.promise : ok()); h.activate(); const read = h.service.load(); await flush();
  for (let n = 0; n < 20; n++) h.feed.emit('job.updated'); await h.clock.advance(600); assert.equal(h.requests.length, 1);
  pending.resolve(ok()); await read; await h.clock.advance(399); assert.equal(h.requests.length, 1);
  await h.clock.advance(1); assert.equal(h.requests.length, 2); await h.clock.advance(20_000); assert.equal(h.requests.length, 2); h.service.dispose();
});
test('actual Retry-After limits subsequent hint refresh and creates no automatic retry loop', async () => {
  const h = harness((_r, n) => n === 1 ? {status: 429, headers: {'Retry-After': '3'}, body: {error: {code: 'busy', current: wire()}}} : ok());
  h.activate(); await h.service.load(); assert.deepEqual(h.service.getSnapshot(), {phase: 'error', error: {code: 'request_failed', retryAfterMs: 3000}});
  await h.clock.advance(1000); assert.equal(h.requests.length, 1); h.feed.emit('operation.updated'); await h.clock.advance(1999); assert.equal(h.requests.length, 1);
  await h.clock.advance(1); assert.equal(h.requests.length, 2); await h.clock.advance(20_000); assert.equal(h.requests.length, 2); h.service.dispose();
});
test('malformed refreshes and generic failures clear old body and expose only a safe error code', async () => {
  for (const response of [ok(wire({streams: {count: 1, items: []}})), {status: 500, headers: {}, body: {error: {code: 'internal', message: 'private-film.mkv', current: wire()}}}]) {
    const h = harness((_r, n) => n === 1 ? ok() : response); h.activate(); await h.service.load(); await h.service.revalidate();
    assert.equal(h.service.getSnapshot().document, undefined); assert.equal(JSON.stringify(h.service.getSnapshot()).includes('private'), false); h.service.dispose();
  }
});

test('cancelling before dispatch and cancelling while input settles release no private completion', async () => {
  const h = harness(); h.activate(); const controller = new AbortController(); const read = h.service.revalidate({signal: controller.signal}); controller.abort(); await read;
  assert.equal(h.requests.length, 0); assert.equal(h.service.getSnapshot().document, undefined); h.service.dispose();
  const j = harness(), waiting = deferred<void>(), stop = new AbortController(); j.activate(); await j.service.load();
  const settled = j.service.revalidate({signal: stop.signal, settle: () => waiting.promise}); await flush(); stop.abort();
  assert.equal(j.service.getSnapshot().document, undefined); await settled;
  assert.equal(j.service.getSnapshot().document, undefined); waiting.resolve(); j.service.dispose();
});
test('a completed cancelled read cannot overwrite a new current-source read', async () => {
  const old = deferred<V1Response>(), h = harness((_r, n) => n === 1 ? old.promise : ok(wire({serverName: 'Current source'}))); h.activate();
  const first = h.service.load(); await flush(); h.service.cancel(); await h.service.load();
  assert.equal(h.service.getSnapshot().document?.serverName, 'Current source');
  old.resolve(ok(wire({serverName: 'Old source'}))); await first;
  assert.equal(h.service.getSnapshot().document?.serverName, 'Current source'); h.service.dispose();
});
test('a listener changing authority during loading prevents even the old transport dispatch', async () => {
  const h = harness(); h.activate(); const release = h.service.subscribe(() => {if (h.service.getSnapshot().phase === 'loading') h.source.set(null);});
  await h.service.load(); assert.equal(h.requests.length, 0); assert.deepEqual(h.service.getSnapshot(), {phase: 'unavailable'}); release(); h.service.dispose();
});

const dashboardWire=(extra:Record<string,unknown>={}):any=>({scope:{serverId:'server',viewerFence:'a'.repeat(64)},observedAt:'2026-10-22T07:00:00Z',items:[],page:{limit:10,total:58,start:0,revision:'',nextCursor:'cursor'},totals:{streams:58,paused:4,converting:7,bandwidthKbps:312000,people:20,atHome:10,away:48},...extra});
function dashboardHarness(handler:(request:V1Request,index:number)=>V1Response|Promise<V1Response>=()=>ok(dashboardWire()),role='admin'){
 const source=new Source({http:{send:async request=>{requests.push(request);return handler(request,requests.length-1);}},scope:{...scope,role}}),clock=new Clock(),feed=new Feed(),requests:V1Request[]=[];
 const service=new DashboardSessionsService({source,timers:clock});service.setActive(true);service.attach(feed);
 return {service,source,clock,feed,requests};
}
test('Dashboard admin reads only a bounded current page with global totals and explicit Show all paging',async()=>{
 const h=dashboardHarness();await h.service.load();assert.equal(h.requests.length,1);assert.equal(h.requests[0].path,routePath('list_admin_sessions')+'?limit=10');
 assert.equal(h.service.getSnapshot().document?.totals.streams,58);assert.equal(h.service.getSnapshot().document?.totals.bandwidthKbps,312000);
 await h.service.load('cursor');assert.equal(h.requests.length,2);assert.ok(h.requests[1].path.endsWith('?limit=10&cursor=cursor'));
 assert.equal(h.service.getSnapshot().document?.page.nextCursor,'cursor');h.service.dispose();assert.equal(h.feed.count(),0);assert.equal(h.clock.tasks.size,0);
});
test('Dashboard member makes no read, no owner panels, no private error body or stale authority completion',async()=>{
 const member=dashboardHarness(undefined,'member');await member.service.load();assert.equal(member.requests.length,0);assert.equal(member.feed.count(),0);member.service.dispose();
 for(const role of ['owner','admin']){
  const pending=deferred<V1Response>(),h=dashboardHarness(()=>pending.promise,role);const read=h.service.load();
  h.source.set({...h.source.value!,scope:{...h.source.value!.scope,profileId:'other-profile'}});
  assert.equal(h.requests[0].signal?.aborted,true);assert.equal(h.service.getSnapshot().document,undefined);pending.resolve(ok(dashboardWire()));await read;
  assert.equal(h.service.getSnapshot().document,undefined);h.service.dispose();assert.equal(h.clock.tasks.size,0);
 }
 const denied=dashboardHarness(()=>({status:403,headers:{},body:{current:'private'}}));await denied.service.load();assert.deepEqual(denied.service.getSnapshot(),{phase:'error',error:{code:'not_permitted'}});denied.feed.emit('admin.sessions');await denied.clock.advance(10000);assert.equal(denied.requests.length,1);denied.service.dispose();
});
test('Dashboard uses the existing feed and one-second cadence after loading; no all-page fanout or polling',async()=>{
 const h=dashboardHarness();await h.service.load();for(let n=0;n<20;n++)h.feed.emit('admin.sessions');await h.clock.advance(999);assert.equal(h.requests.length,1);await h.clock.advance(1);assert.equal(h.requests.length,2);
 await h.clock.advance(10000);assert.equal(h.requests.length,2);h.service.setActive(false);assert.equal(h.feed.count(),0);assert.equal(h.service.getSnapshot().document,undefined);h.service.dispose();
});
test('Dashboard read deadline and listener-triggered sign-out release no private completion',async()=>{
 const pending=deferred<V1Response>(),h=dashboardHarness(()=>pending.promise);const read=h.service.load();await h.clock.advance(10000);await read;
 assert.deepEqual(h.service.getSnapshot(),{phase:'error',error:{code:'request_timeout'}});pending.resolve(ok(dashboardWire()));await flush();assert.equal(h.service.getSnapshot().document,undefined);h.service.dispose();assert.equal(h.clock.tasks.size,0);
 const signedOut=dashboardHarness();signedOut.service.subscribe(()=>{if(signedOut.service.getSnapshot().phase==='loading')signedOut.source.set(null);});await signedOut.service.load();assert.equal(signedOut.requests.length,0);assert.equal(signedOut.service.getSnapshot().document,undefined);signedOut.service.dispose();
});
test('Dashboard rejects owner-only payload additions, inconsistent totals, wrong server and fence replacement',async()=>{
 for(const body of [dashboardWire({serverName:'private'}),dashboardWire({totals:{streams:59,paused:0,converting:0,bandwidthKbps:0,people:1,atHome:0,away:0}}),dashboardWire({scope:{serverId:'other-server',viewerFence:'a'.repeat(64)}})]){
  const h=dashboardHarness(()=>ok(body));await h.service.load();assert.equal(h.service.getSnapshot().phase,'error');assert.equal(h.service.getSnapshot().document,undefined);h.service.dispose();
 }
 const h=dashboardHarness((_,n)=>ok(dashboardWire(n?{scope:{serverId:'server',viewerFence:'b'.repeat(64)}}:{})));await h.service.load();await h.service.load();assert.equal(h.service.getSnapshot().error?.code,'permission_changed');assert.equal(h.service.getSnapshot().document,undefined);h.service.dispose();
});

test('actual Activity resync owner shares feed admission and recover after permits release', async () => {
  const {EventsClient} = await import('../src/playback-v1/events.ts');
  const h = harness(), feed = new EventsClient({http: h.http, now: h.clock.now, setTimer: h.clock.setTimer, clearTimer: h.clock.clearTimer, random: () => 0});
  h.service.setActive(true); h.service.attach(feed);
  const held = deferred<void>(), controller = new AbortController();
  const occupied = Array.from({length: 4}, () => feed.resyncReads.run(controller.signal, () => held.promise));
  await flush();
  feed.deliver({id: 'activity-gap', type: 'stream.resync'}); await flush(); assert.equal(h.requests.length, 0);
  held.resolve(); await Promise.all(occupied); await flush(); await flush();
  assert.equal(h.requests.length, 1); assert.equal(h.service.getSnapshot().phase, 'ready');
  h.service.dispose(); feed.resyncReads.dispose();
});

test('actual Dashboard sessions resync waits for shared admission and retains current authority', async () => {
  const {EventsClient} = await import('../src/playback-v1/events.ts');
  const h = dashboardHarness(), http = h.source.getSnapshot()!.http;
  const feed = new EventsClient({http, now: h.clock.now, setTimer: h.clock.setTimer, clearTimer: h.clock.clearTimer, random: () => 0});
  h.service.attach(feed);
  const held = deferred<void>(), controller = new AbortController();
  const occupied = Array.from({length: 4}, () => feed.resyncReads.run(controller.signal, () => held.promise));
  await flush(); feed.deliver({id: 'dashboard-gap', type: 'stream.resync'}); await flush(); assert.equal(h.requests.length, 0);
  held.resolve(); await Promise.all(occupied); await flush(); await flush();
  assert.equal(h.requests.length, 1); assert.equal(h.service.getSnapshot().phase, 'ready');
  h.service.dispose(); feed.resyncReads.dispose();
});
