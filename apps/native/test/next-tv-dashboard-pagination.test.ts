import test from 'node:test';
import assert from 'node:assert/strict';
import {AdminSessionsClient} from '../../../packages/client-core/src/playback-v1/admin-sessions';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeDashboardController, type NativeDashboardReaders} from '../src-next/screens/settings-live/dashboard-controller';

const authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'fence'};
const totals = {streams: 21, paused: 0, converting: 0, bandwidthKbps: 0, people: 21, atHome: 21, away: 0};
const page = (prefix: string, count: number, cursor?: string) => ({items: Array.from({length: count}, (_, i) => ({id: `${prefix}${i}`})) as never[], nextCursor: cursor, totals, revision: 'rev-1', scope: {serverId: 'server', viewerFence: 'fence'}});

test('TV Dashboard continuation coalesces, keeps prior rows on error and retries its offered cursor', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let finish!: (value: ReturnType<typeof page>) => void, fail!: (error: Error) => void;
  const sessionCalls: (string | undefined)[] = [], jobCalls: string[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async cursor => {sessionCalls.push(cursor); return cursor === undefined ? page('first', 10, 'offered') : new Promise((resolve, reject) => {finish = resolve; fail = reject;});}},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async cursor => {jobCalls.push(cursor ?? ''); return {items: [], nextCursor: ''};}, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh();
  const original = controller.getSnapshot().sessionWindow!;
  const pending = controller.nextSessions(original); const duplicate = controller.nextSessions(original); assert.deepEqual(sessionCalls, [undefined, 'offered']);
  fail(new Error('offline')); await Promise.all([pending, duplicate]);
  assert.deepEqual(controller.getSnapshot().sessions.map(item => item.id), Array.from({length: 10}, (_, i) => `first${i}`));
  const retry = controller.retrySessions(controller.getSnapshot().sessionWindow!); finish(page('last', 1)); await retry;
  assert.equal(controller.getSnapshot().sessions.at(-1)?.id, 'last0');
  assert.deepEqual(sessionCalls, [undefined, 'offered', 'offered']); assert.deepEqual(jobCalls, []);
  controller.dispose(); lifetime.dispose();
});

test('Dashboard refresh retires a pending continuation without publishing stale sessions', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let finish!: (value: ReturnType<typeof page>) => void, reads = 0;
  const readers: NativeDashboardReaders = {
    sessions: {list: async cursor => {reads++; return cursor ? new Promise(resolve => {finish = resolve;}) : page(reads === 1 ? 'old' : 'fresh', 10, 'next');}},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async () => ({items: [], nextCursor: ''}), dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh(); const stale = controller.nextSessions(controller.getSnapshot().sessionWindow!); await controller.refresh();
  finish(page('stale', 1)); await stale;
  assert.equal(controller.getSnapshot().sessions.some(item => item.id.startsWith('stale')), false);
  assert.equal(controller.getSnapshot().sessions[0]?.id, 'fresh0');
  controller.dispose(); lifetime.dispose();
});

test('owner jobs use their own offered cursor and retirement denies later reads', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let role: 'owner' | 'administrator' = 'owner';
  const jobs: string[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async () => page('session', 1)},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async cursor => {
      jobs.push(cursor ?? ''); return {items: [{id: cursor ? 'second' : 'first'}] as never[], nextCursor: cursor ? '' : 'jobs-next'};
    }, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => role, readers);
  await controller.refresh(); await controller.nextJobs(controller.getSnapshot().jobWindow!);
  assert.deepEqual(jobs, ['', 'jobs-next']); assert.deepEqual(controller.getSnapshot().jobs.map(job => job.id), ['first', 'second']);
  role = 'administrator'; await controller.refresh(); assert.deepEqual(controller.getSnapshot().jobs, []);
  await controller.nextJobs(controller.getSnapshot().jobWindow!); assert.deepEqual(jobs, ['', 'jobs-next']);
  controller.dispose(); lifetime.dispose();
});

test('Dashboard reaches 21 sessions in producer order and retains the server totals', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const calls: (string | undefined)[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async cursor => {calls.push(cursor); return cursor === undefined ? page('a', 10, 'p2') : cursor === 'p2' ? page('b', 10, 'p3') : page('c', 1);}},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async () => ({items: [], nextCursor: ''}), dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh(); await controller.nextSessions(controller.getSnapshot().sessionWindow!); await controller.nextSessions(controller.getSnapshot().sessionWindow!);
  assert.deepEqual(controller.getSnapshot().sessions.map(row => row.id), [...Array.from({length: 10}, (_, i) => `a${i}`), ...Array.from({length: 10}, (_, i) => `b${i}`), 'c0']);
  assert.equal(controller.getSnapshot().totals?.streams, 21);
  await controller.nextSessions(controller.getSnapshot().sessionWindow!); assert.deepEqual(calls, [undefined, 'p2', 'p3']);
  controller.dispose(); lifetime.dispose();
});

test('a changed session page revision refuses the continuation and preserves prior rows', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const readers: NativeDashboardReaders = {
    sessions: {list: async cursor => cursor ? {...page('changed', 1), revision: 'rev-2'} : page('original', 10, 'next')},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async () => ({items: [], nextCursor: ''}), dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh(); await controller.nextSessions(controller.getSnapshot().sessionWindow!);
  assert.equal(controller.getSnapshot().sessionWindow?.refreshRequired, true);
  assert.equal(controller.getSnapshot().sessions[0]?.id, 'original0');
  controller.dispose(); lifetime.dispose();
});

test('owner job continuation is fenced when admission becomes administrator', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let role: 'owner' | 'administrator' = 'owner', finish!: (value: {items: never[]; nextCursor: string}) => void, jobReads = 0;
  const readers: NativeDashboardReaders = {
    sessions: {list: async () => page('session', 1)},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async cursor => {
      jobReads++; return cursor ? new Promise(resolve => {finish = resolve;}) : {items: [{id: 'owner-job', revision: 1}] as never[], nextCursor: 'next'};
    }, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => role, readers);
  await controller.refresh(); const pending = controller.nextJobs(controller.getSnapshot().jobWindow!);
  role = 'administrator'; await controller.refresh();
  assert.deepEqual(controller.getSnapshot().jobs, []); assert.equal(controller.getSnapshot().health, undefined);
  finish({items: [{id: 'late-owner-job', revision: 1}] as never[], nextCursor: ''}); await pending;
  assert.deepEqual(controller.getSnapshot().jobs, []); assert.equal(jobReads, 2);
  controller.dispose(); lifetime.dispose();
});

test('coalesced refresh cannot reset the session revision fence between visible pages', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let refreshing = false, finish!: (value: ReturnType<typeof page>) => void;
  const readers: NativeDashboardReaders = {
    sessions: {list: async cursor => {
      if (!refreshing) return cursor ? page('old2', 1) : page('old1', 1, 'next');
      return cursor ? new Promise(resolve => {finish = resolve;}) : {...page('fresh1', 1, 'next'), revision: 'rev-2'};
    }},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async () => ({items: [], nextCursor: ''}), dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh(); await controller.nextSessions(controller.getSnapshot().sessionWindow!);
  refreshing = true; const first = controller.refresh(); await new Promise<void>(resolve => setImmediate(resolve));
  const duplicate = controller.refresh(); finish({...page('wrong', 1), revision: 'rev-3'}); await Promise.all([first, duplicate]);
  assert.equal(controller.getSnapshot().sessionWindow?.refreshRequired, true); assert.deepEqual(controller.getSnapshot().sessions.map(row => row.id), ['old10', 'old20']);
  controller.dispose(); lifetime.dispose();
});

test('a retained same-generation row may acquire focus while its next page waits', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let finish!: (value: ReturnType<typeof page>) => void;
  const readers: NativeDashboardReaders = {
    sessions: {list: async cursor => cursor === 'p4' ? new Promise(resolve => {finish = resolve;}) : cursor === undefined ? page('a', 1, 'p2') : cursor === 'p2' ? page('b', 1, 'p3') : page('c', 1, 'p4')},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async () => ({items: [], nextCursor: ''}), dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh(); await controller.nextSessions(controller.getSnapshot().sessionWindow!); await controller.nextSessions(controller.getSnapshot().sessionWindow!);
  const original = controller.getSnapshot().sessionWindow!, pending = controller.nextSessions(original);
  const token = controller.focusSession('a0', original); assert.ok(token);
  finish(page('d', 1)); await pending;
  assert.equal(controller.getSnapshot().sessionWindow?.pending, true); assert.equal(controller.getSnapshot().sessions[0]?.id, 'a0');
  controller.blurSession('a0', token!, original); assert.equal(controller.getSnapshot().sessions[0]?.id, 'b0');
  const refreshed = controller.refresh(); assert.equal(controller.focusSession('b0', original), undefined);
  controller.dispose(); await refreshed; lifetime.dispose();
});

test('real AdminSessionsClient accepts the producer empty revision across continuation', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const fence = 'a'.repeat(64), calls: string[] = [], stamp = '2026-10-22T07:00:00Z';
  const session = (id: string) => ({id, protocol: 'v1', user: {id: 'account', name: 'Account'}, profile: {id: 'primary', name: 'Primary'}, device: {id: 'device', name: 'Device'}, kind: 'vod', state: 'playing', positionMs: 0, decision: {}, location: 'local', startedAt: stamp});
  const client = new AdminSessionsClient({send: async request => {
    calls.push(request.path); const cursor = new URL(request.path, 'https://server.invalid').searchParams.get('cursor');
    return {status: 200, headers: {}, body: {scope: {serverId: 'server', viewerFence: fence}, observedAt: stamp, items: [session(cursor ? 'second' : 'first')], page: {limit: 10, total: 2, start: cursor ? 1 : 0, revision: '', ...(cursor ? {} : {nextCursor: 'offered'})}, totals: {streams: 2, paused: 0, converting: 0, bandwidthKbps: 0, people: 1, atHome: 2, away: 0}}};
  }});
  const readers: NativeDashboardReaders = {sessions: client, console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async () => ({items: [], nextCursor: ''}), dispose() {}}};
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh(); await controller.nextSessions(controller.getSnapshot().sessionWindow!);
  assert.deepEqual(controller.getSnapshot().sessions.map(row => row.id), ['first', 'second']);
  assert.equal(controller.getSnapshot().sessionWindow?.refreshRequired, false); assert.equal(calls.length, 2);
  controller.dispose(); lifetime.dispose();
});

test('real AdminSessionsClient keeps the canonical absent-revision decoder refusal', async () => {
  const client = new AdminSessionsClient({send: async () => ({status: 200, headers: {}, body: {scope: {serverId: 'server', viewerFence: 'a'.repeat(64)}, observedAt: '2026-10-22T07:00:00Z', items: [], page: {limit: 10, total: 0, start: 0}, totals: {streams: 0, paused: 0, converting: 0, bandwidthKbps: 0, people: 0, atHome: 0, away: 0}}})});
  await assert.rejects(client.list(), /invalid_activity_summary/);
});

for (const field of ['serverId', 'profileId'] as const) test(`Dashboard retires a pending session continuation on ${field} authority replacement`, async () => {
  let principal = {...authority}, listener: () => void = () => {};
  const lifetime = new NativeScreenLifetime({authority: () => principal, current: () => true, subscribeAuthority: fn => {listener = fn; return () => {listener = () => {};};}});
  let finish!: (value: ReturnType<typeof page>) => void; const calls: (string | undefined)[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async cursor => {calls.push(cursor); return cursor ? new Promise(resolve => {finish = resolve;}) : page('original', 1, 'next');}},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async () => ({items: [], nextCursor: ''}), dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'administrator', readers);
  await controller.refresh(); const original = controller.getSnapshot().sessionWindow!;
  const pending = controller.nextSessions(original);
  principal = {...principal, [field]: 'successor'}; listener();
  assert.equal(lifetime.signal.aborted, true); assert.deepEqual(controller.getSnapshot().sessions, []);
  assert.equal(controller.focusSession('original0', original), undefined);
  finish(page('late', 1)); await pending;
  assert.deepEqual(controller.getSnapshot().sessions, []); assert.equal(controller.getSnapshot().admission, 'denied');
  await controller.nextSessions(original); assert.deepEqual(calls, [undefined, 'next']);
  controller.dispose(); lifetime.dispose();
});

test('Dashboard loading notification disposal refuses every old owner read', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const calls: string[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async () => {calls.push('sessions'); return page('row', 1);}},
    console: {measurement: async name => {calls.push(name); return {name, observedAt: 1, freshUntil: 30001, facts: {}};}, alerts: async () => {calls.push('alerts'); return [];}, jobs: async () => {calls.push('jobs'); return {items: [], nextCursor: ''};}, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'owner', readers);
  const off = controller.subscribe(() => {if (controller.getSnapshot().loading) controller.dispose();});
  await controller.refresh(); assert.deepEqual(calls, []); assert.equal(controller.getSnapshot().admission, 'denied');
  off(); controller.dispose(); lifetime.dispose();
});

test('Dashboard loading notification installs an administrator successor before owner dispatch', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let role: 'owner' | 'administrator' = 'owner', successor: Promise<void> | undefined; const calls: string[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async () => {calls.push(`sessions:${role}`); return page('admin', 1);}},
    console: {measurement: async name => {calls.push(name); return {name, observedAt: 1, freshUntil: 30001, facts: {}};}, alerts: async () => {calls.push('alerts'); return [];}, jobs: async () => {calls.push('jobs'); return {items: [], nextCursor: ''};}, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => role, readers);
  const off = controller.subscribe(() => {if (role === 'owner' && controller.getSnapshot().loading) {role = 'administrator'; successor = controller.refresh();}});
  const old = controller.refresh(); await old; assert.ok(successor); await successor;
  assert.deepEqual(calls, ['sessions:administrator']); assert.equal(controller.getSnapshot().admission, 'administrator');
  assert.equal(controller.getSnapshot().sessions[0]?.id, 'admin0'); assert.deepEqual(controller.getSnapshot().jobs, []);
  off(); controller.dispose(); lifetime.dispose();
});

test('Dashboard disposal notification cannot reenter refresh while the lifetime remains live', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const calls: string[] = []; let reentered: Promise<void> | undefined;
  const readers: NativeDashboardReaders = {
    sessions: {list: async () => {calls.push('sessions'); return page('row', 1);}},
    console: {measurement: async name => {calls.push(name); return {name, observedAt: 1, freshUntil: 30001, facts: {}};}, alerts: async () => {calls.push('alerts'); return [];}, jobs: async () => {calls.push('jobs'); return {items: [], nextCursor: ''};}, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'owner', readers);
  controller.subscribe(() => {if (controller.getSnapshot().admission === 'denied') reentered = controller.refresh();});
  controller.dispose(); assert.ok(reentered); await reentered; assert.equal(lifetime.current(), true); assert.deepEqual(calls, []);
  lifetime.dispose();
});

test('Dashboard current owner refresh dispatches each authorized section once', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const calls: string[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async () => {calls.push('sessions'); return page('owner', 1);}},
    console: {measurement: async name => {calls.push(name); return {name, observedAt: 1, freshUntil: 30001, facts: {}};}, alerts: async () => {calls.push('alerts'); return [];}, jobs: async () => {calls.push('jobs'); return {items: [], nextCursor: ''};}, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => 'owner', readers); await controller.refresh();
  assert.deepEqual(calls, ['sessions', 'health', 'storage', 'alerts', 'jobs']); assert.equal(controller.getSnapshot().loading, false);
  controller.dispose(); lifetime.dispose();
});

test('Dashboard role-change abort fanout retires both old windows without aborting the successor', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let role: 'owner' | 'administrator' = 'owner', successor: Promise<void> | undefined;
  let finishSession: ((value: ReturnType<typeof page>) => void) | undefined, finishJob: ((value: {items: never[]; nextCursor: string}) => void) | undefined;
  const pending: Promise<unknown>[] = [];
  const readers: NativeDashboardReaders = {
    sessions: {list: async (cursor, _limit, signal) => {
      if (!cursor) return page('current', 1, 'next');
      return new Promise(resolve => {finishSession = resolve; signal?.addEventListener('abort', () => {role = 'owner'; successor = controller.refresh();}, {once: true});});
    }},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async cursor => cursor ? new Promise(resolve => {finishJob = resolve;}) : {items: [{id: 'job', revision: 1}] as never[], nextCursor: 'next'}, dispose() {}},
  };
  const controller = new NativeDashboardController(lifetime, () => role, readers); await controller.refresh();
  const OriginalAbort = globalThis.AbortController, created: AbortController[] = [];
  class ObservedAbort extends OriginalAbort {constructor() {super(); created.push(this);}}
  globalThis.AbortController = ObservedAbort;
  try {
    const oldSession = controller.nextSessions(controller.getSnapshot().sessionWindow!); pending.push(oldSession);
    const oldJob = controller.nextJobs(controller.getSnapshot().jobWindow!); pending.push(oldJob);
    assert.equal(created.length, 2); const [sessionAbort, jobAbort] = created;
    role = 'administrator'; await controller.refresh(); assert.ok(successor); await successor;
    assert.equal(sessionAbort.signal.aborted, true); assert.equal(jobAbort.signal.aborted, true);
    assert.ok(created.length >= 4); assert.equal(created[2].signal.aborted, false); assert.equal(created[3].signal.aborted, false);
    assert.ok(finishSession); assert.ok(finishJob); finishSession(page('old', 1)); finishJob({items: [{id: 'old-job', revision: 1}] as never[], nextCursor: ''}); await Promise.all([oldSession, oldJob]);
    assert.equal(controller.getSnapshot().admission, 'owner'); assert.equal(controller.getSnapshot().sessions[0]?.id, 'current0');
    assert.equal(controller.getSnapshot().jobs[0]?.id, 'job');
  } finally {
    globalThis.AbortController = OriginalAbort; controller.dispose(); lifetime.dispose();
    finishSession?.(page('old', 1)); finishJob?.({items: [], nextCursor: ''});
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {await Promise.race([Promise.allSettled(pending), new Promise<never>((_, reject) => {timer = setTimeout(() => reject(new Error('held dashboard readers did not settle')), 1000);})]);}
    finally {if (timer) clearTimeout(timer);}
  }
});
