import test from 'node:test';
import assert from 'node:assert/strict';
import {ActivitySummaryService} from '../src/admin/activity-summary/service.ts';
import type {ActivitySummaryScope, ActivitySummarySource, ActivitySummaryTimers} from '../src/admin/activity-summary/types.ts';
import type {V1Http, V1Request, V1Response} from '../src/playback-v1/http.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../src/query/viewer-queries.ts';
import {queryKeys} from '../src/query/keys.ts';
import {heldDocument} from '../src/query/admin-documents.ts';

// Client data step 6: the owner's activity summary is read through the viewer's cache. Every popover
// of the viewer shares one read; one opened again shows at once with no request; a hint reads again,
// and the document on screen stays while it does (J-9).
const scope: ActivitySummaryScope = {serverId: 'server', accountId: 'owner-account', profileId: 'owner-profile', authority: 'local', role: 'owner', sessionFamilyId: 'family', authorizationEpoch: '1'};
const wire = (name: string, extra: Record<string, unknown> = {}): any => ({scope: {serverId: 'server', viewerFence: 'a'.repeat(64)}, observedAt: '2026-10-22T07:00:00.000000001Z', serverName: name, streams: {count: 0, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: []}, scans: [], tasks: {count: 0, items: []}, update: {available: false, state: 'unconfigured', current: '0.1.0-dev'}, health: {degraded: false}, majorIssues: [], ...extra});
const tick = () => new Promise(resolve => setTimeout(resolve, 5));
class Clock implements ActivitySummaryTimers {
  value = 0; sequence = 0; tasks = new Map<number, {at: number; fn: () => void}>();
  now = () => this.value;
  setTimer = (fn: () => void, ms: number) => {const id = ++this.sequence; this.tasks.set(id, {at: this.value + ms, fn}); return id;};
  clearTimer = (id: unknown) => {this.tasks.delete(id as number);};
  async advance(ms: number) {
    const end = this.value + ms;
    for (let guard = 0; guard < 100; guard++) {
      const next = [...this.tasks.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) {this.value = end; await tick(); return;}
      this.value = next[1].at; this.tasks.delete(next[0]); next[1].fn(); await tick();
    }
    throw new Error('unexpected timer loop');
  }
}
class Feed {
  listeners = new Map<string, Set<(event: any) => void>>(); resync = new Set<() => void>();
  on(type: string, fn: (event: any) => void) {const set = this.listeners.get(type) ?? new Set(); set.add(fn); this.listeners.set(type, set); return () => {set.delete(fn);};}
  onResync(fn: () => void) {this.resync.add(fn); return () => {this.resync.delete(fn);};}
  emit(type: string) {for (const fn of [...this.listeners.get(type) ?? []]) fn({id: 'event', type, revision: '1'});}
}
const made: ViewerQueries[] = [];
test.afterEach(() => {for (const q of made.splice(0)) q.dispose();});
function world() {
  const requests: V1Request[] = []; let name = 'First';
  const http: V1Http = {send: async (request): Promise<V1Response> => {requests.push(request); return {status: 200, headers: {}, body: wire(name)};}};
  const hub = new FreshnessHub({settleMs: 0});
  const queries = createViewerQueries({scope: {serverId: 'server', viewerId: 'owner'}, hub, api: {request: async () => { throw new Error('unused'); }}, gcTimeMs: Infinity});
  made.push(queries);
  const popover = () => {
    const clock = new Clock(), feed = new Feed();
    const source = {getSnapshot: (): ActivitySummarySource | null => ({http, scope: {...scope}}), subscribe: () => () => {}};
    const service = new ActivitySummaryService({source, timers: clock, timeoutMs: 5000, queries});
    service.setActive(true); service.attach(feed as never);
    return {service, clock, feed};
  };
  return {requests, hub, queries, popover, rename: (next: string) => { name = next; }};
}

test('two popovers of one viewer share one read, and one opened again shows at once with no request', async () => {
  const w = world();
  const first = w.popover(); await first.service.load();
  assert.equal(w.requests.length, 1); assert.equal(first.service.getSnapshot().document?.serverName, 'First');
  assert.ok(w.queries.client.getQueryData(queryKeys.adminActivity()));
  const second = w.popover(); const phases: string[] = []; second.service.subscribe(() => phases.push(second.service.getSnapshot().phase));
  const load = second.service.load();
  await load; await tick();
  assert.equal(second.service.getSnapshot().phase, 'ready'); assert.equal(second.service.getSnapshot().document?.serverName, 'First');
  assert.ok(!phases.includes('loading'), 'no loading state for a summary the cache holds');
  assert.equal(w.requests.length, 1, 'no second request');
  // Two cold popovers asking together share the one read.
  const cold = world(); const a = cold.popover(), b = cold.popover();
  await Promise.all([a.service.load(), b.service.load()]);
  assert.equal(cold.requests.length, 1);
  assert.equal(a.service.getSnapshot().document?.serverName, 'First'); assert.equal(b.service.getSnapshot().document?.serverName, 'First');
  first.service.dispose(); second.service.dispose(); a.service.dispose(); b.service.dispose();
});

test('a hint reads again while the document stays on screen, and a popover opened after the hint never shows the old summary', async () => {
  const w = world();
  const open = w.popover(); await open.service.load();
  const phases: string[] = []; open.service.subscribe(() => phases.push(open.service.getSnapshot().phase + ':' + (open.service.getSnapshot().document?.serverName ?? '')));
  w.rename('Second'); open.feed.emit('job.updated');
  await open.clock.advance(1500);
  assert.equal(w.requests.length, 2, 'the hint read the server');
  assert.equal(open.service.getSnapshot().document?.serverName, 'Second');
  assert.ok(!phases.some(p => p.startsWith('loading')), 'the shown document was never replaced by loading');
  assert.ok(phases.every(p => p.endsWith(':First') || p.endsWith(':Second')), 'a document was always on screen');
  // A hint nobody had open since: the next popover reads it again.
  w.rename('Third'); w.hub.accept({id: 'e2', type: 'server.updated', revision: '2'} as never);
  const later = w.popover(); await later.service.load();
  assert.equal(w.requests.length, 3); assert.equal(later.service.getSnapshot().document?.serverName, 'Third');
  open.service.dispose(); later.service.dispose();
});

test('a summary older than the most a held one may be is read again; a retry always asks the server', async () => {
  const w = world();
  const open = w.popover(); await open.service.load();
  const state = w.queries.client.getQueryState(queryKeys.adminActivity())!;
  assert.ok(heldDocument(w.queries, queryKeys.adminActivity(), 30_000, state.dataUpdatedAt + 29_000));
  assert.equal(heldDocument(w.queries, queryKeys.adminActivity(), 30_000, state.dataUpdatedAt + 31_000), undefined);
  await open.service.retry();
  assert.equal(w.requests.length, 2);
  open.service.dispose();
});

test('privacy: a popover of an ended viewer\'s cache shows nothing from it', async () => {
  const w = world();
  const open = w.popover(); await open.service.load();
  w.queries.dispose();
  const later = w.popover(); await later.service.load();
  assert.equal(later.service.getSnapshot().document, undefined);
  assert.notEqual(later.service.getSnapshot().phase, 'ready');
  open.service.dispose(); later.service.dispose();
});
