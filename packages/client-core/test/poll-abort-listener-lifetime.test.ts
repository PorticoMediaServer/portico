import test from 'node:test';
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
import {pollJob, runBulkRequests} from '../src/bulk-jobs.ts';
import {pollDownloadRequest} from '../src/download-requests.ts';
import {QueueClient} from '../src/playback-v1/queue.ts';
import type {V1Http, V1Request} from '../src/playback-v1/http.ts';

// PERF-WEB-007: the polling waits in bulk-jobs (pollJob, sleep/runBulkRequests),
// download-requests and playback-v1 queue.awaitCurrent used to add one
// `{once:true}` abort listener per iteration on the caller's signal and only
// ever dropped it when the abort fired. Each wait now detaches its own listener
// on the timer/wake path as well. These tests count the listeners the public
// helpers really add and remove, and pin the surrounding behaviour: the same
// requests, the same delays, the same cancellation errors, one timer per wait.

function trackedSignal() {
  const controller = new AbortController(), signal = controller.signal;
  const live = new Map<EventListenerOrEventListenerObject, EventListener>();
  let added = 0, peak = 0;
  const add = signal.addEventListener.bind(signal), remove = signal.removeEventListener.bind(signal);
  Object.defineProperty(signal, 'addEventListener', {configurable: true, value: (type: string, listener: EventListenerOrEventListenerObject, options?: AddEventListenerOptions) => {
    if (type !== 'abort') { add(type, listener, options); return; }
    added++;
    const once = options?.once === true, call = listener as EventListener;
    const wrapped = (event: Event) => { if (once) live.delete(listener); call(event); };
    live.set(listener, wrapped); peak = Math.max(peak, live.size);
    add(type, wrapped, options);
  }});
  Object.defineProperty(signal, 'removeEventListener', {configurable: true, value: (type: string, listener: EventListenerOrEventListenerObject) => {
    if (type !== 'abort') { remove(type, listener); return; }
    const wrapped = live.get(listener);
    if (wrapped) { live.delete(listener); remove(type, wrapped); }
  }});
  return {controller, signal, live: () => live.size, peak: () => peak, added: () => added};
}

// A wait is installed after the helper's own read resolves, which is a different
// number of microtasks per client; wait for the real listener count instead.
async function waitForListeners(tracked: ReturnType<typeof trackedSignal>, count: number) {
  for (let attempt = 0; attempt < 200 && tracked.live() !== count; attempt++) await delay(5);
  return tracked.live();
}

function timerAudit() {
  const realSet = globalThis.setTimeout, realClear = globalThis.clearTimeout;
  const created: {delay: number; handle: ReturnType<typeof setTimeout>}[] = [];
  const cleared: ReturnType<typeof setTimeout>[] = [];
  globalThis.setTimeout = ((callback: (...args: unknown[]) => void, delay?: number, ...args: unknown[]) => {
    const handle = realSet(callback, delay, ...args);
    created.push({delay: delay ?? 0, handle});
    return handle;
  }) as typeof setTimeout;
  globalThis.clearTimeout = ((handle?: ReturnType<typeof setTimeout>) => {cleared.push(handle as ReturnType<typeof setTimeout>); realClear(handle);}) as typeof clearTimeout;
  return {
    delays: () => created.map(entry => entry.delay),
    handles: () => created.map(entry => entry.handle),
    clears: () => cleared,
    restore() {globalThis.setTimeout = realSet; globalThis.clearTimeout = realClear;},
  };
}

const job = (state: string, done: number) => ({jobId: 'job-1', command: 'personal-state', state, total: 4, totalKnown: true, done, failed: 0});
const download = (state: string) => ({requestId: 'req-1', total: 2, totalKnown: true, state, ready: 0, preparing: 0, queued: 2, failed: 0, paused: 0, items: [], nextCursor: ''});

test('pollJob detaches every wait listener on the timer path and keeps the same requests and delays', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  const seen: number[] = [];
  let reads = 0;
  const api = {async request<T>(): Promise<T> {
    seen.push(tracked.live());
    reads++;
    return (reads < 3 ? job('running', reads) : job('complete', 4)) as T;
  }};
  try {
    const finished = await pollJob(api, 'job-1', {intervalMs: 250, timeoutMs: 10000, signal: tracked.signal});
    assert.equal(finished.state, 'complete');
  } finally {audit.restore();}
  assert.equal(reads, 3);
  assert.deepEqual(seen, [0, 0, 0]);
  assert.equal(tracked.added(), 2);
  assert.ok(tracked.peak() <= 1, 'at most one wait listener is active at a time');
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [250, 250]);
});

test('pollJob detaches the finished wait before a later failure, leaving no listener behind', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  let reads = 0;
  const api = {async request<T>(): Promise<T> {
    reads++;
    if (reads === 1) return job('running', 0) as T;
    throw new Error('offline');
  }};
  try {
    await assert.rejects(pollJob(api, 'job-1', {intervalMs: 250, timeoutMs: 10000, signal: tracked.signal}), /offline/);
  } finally {audit.restore();}
  assert.equal(reads, 2);
  assert.equal(tracked.added(), 1);
  assert.ok(tracked.peak() <= 1);
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [250]);
});

test('pollJob abort rejects with the same code and message, clears that timer once and starts no new request', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  let reads = 0;
  const api = {async request<T>(): Promise<T> { reads++; return job('running', 0) as T; }};
  try {
    const running = pollJob(api, 'job-1', {intervalMs: 250, timeoutMs: 10000, signal: tracked.signal});
    const rejected = assert.rejects(running, (error: {code?: string; message?: string}) => error.code === 'cancelled' && error.message === 'The bulk action was cancelled.');
    assert.equal(await waitForListeners(tracked, 1), 1);
    tracked.controller.abort();
    await rejected;
    assert.equal(tracked.live(), 0);
  } finally {audit.restore();}
  await delay(300);
  assert.equal(reads, 1);
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [250]);
  assert.deepEqual(audit.clears(), audit.handles());
});

test('runBulkRequests detaches the sleep listener on completion, delay unchanged', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  const seen: number[] = [];
  let polls = 0;
  const api = {async request<T>(path: string, method = 'GET'): Promise<T> {
    seen.push(tracked.live());
    if (method === 'POST') return {jobId: 'job-1', command: 'metadata-edit', state: 'running', total: 0, totalKnown: false, done: 0, failed: 0} as T;
    polls++;
    return (polls < 2 ? {jobId: 'job-1', command: 'metadata-edit', state: 'running', total: 2, totalKnown: true, done: 1, failed: 0}
      : {jobId: 'job-1', command: 'metadata-edit', state: 'complete', total: 2, totalKnown: true, done: 2, failed: 0}) as T;
  }};
  try {
    const outcome = await runBulkRequests(api, 'metadata-edit', {lockEdited: true}, [{selector: {items: {ids: ['a', 'b']}}, operationId: 'op-1', ids: ['a', 'b']}], undefined, tracked.signal);
    assert.deepEqual(outcome, {ok: 2, failed: [], jobs: 1});
  } finally {audit.restore();}
  assert.equal(polls, 2);
  assert.deepEqual(seen, [0, 0, 0]);
  assert.equal(tracked.added(), 1);
  assert.ok(tracked.peak() <= 1);
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [1500]);
});

test('runBulkRequests detaches the sleep listener when the next read fails and on abort', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  let polls = 0;
  const failing = {async request<T>(_path: string, method = 'GET'): Promise<T> {
    if (method === 'POST') return {jobId: 'job-1', command: 'metadata-edit', state: 'running', total: 0, totalKnown: false, done: 0, failed: 0} as T;
    if (++polls === 1) return {jobId: 'job-1', command: 'metadata-edit', state: 'running', total: 2, totalKnown: true, done: 1, failed: 0} as T;
    throw new Error('offline');
  }};
  try {
    await assert.rejects(runBulkRequests(failing, 'metadata-edit', {lockEdited: true}, [{selector: {items: {ids: ['a']}}, operationId: 'op-1', ids: ['a']}], undefined, tracked.signal), /offline/);
  } finally {audit.restore();}
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [1500]);

  const cancelled = trackedSignal();
  const slow = {async request<T>(_path: string, method = 'GET'): Promise<T> {
    return (method === 'POST' ? {jobId: 'job-2', command: 'metadata-edit', state: 'running', total: 0, totalKnown: false, done: 0, failed: 0}
      : {jobId: 'job-2', command: 'metadata-edit', state: 'running', total: 2, totalKnown: true, done: 0, failed: 0}) as T;
  }};
  let reads = 0;
  const counted = {async request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> { reads++; return slow.request<T>(path, method, body, signal); }};
  const running = runBulkRequests(counted, 'metadata-edit', {lockEdited: true}, [{selector: {items: {ids: ['a']}}, operationId: 'op-2', ids: ['a']}], undefined, cancelled.signal);
  const rejected = assert.rejects(running, (error: {code?: string; message?: string}) => error.code === 'cancelled' && error.message === 'The bulk action was cancelled.');
  await delay(100);
  assert.equal(cancelled.live(), 1);
  cancelled.controller.abort();
  await rejected;
  assert.equal(cancelled.live(), 0);
  const afterAbort = reads;
  await delay(200);
  assert.equal(reads, afterAbort);
});

test('pollDownloadRequest detaches every wait listener and keeps the same wait and errors', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  const seen: number[] = [];
  let reads = 0;
  const api = {async request<T>(): Promise<T> {
    seen.push(tracked.live());
    reads++;
    return (reads < 2 ? download('preparing') : download('complete')) as T;
  }};
  try {
    const finished = await pollDownloadRequest(api, 'req-1', {intervalMs: 500, timeoutMs: 10000, signal: tracked.signal});
    assert.equal(finished.state, 'complete');
  } finally {audit.restore();}
  assert.equal(reads, 2);
  assert.deepEqual(seen, [0, 0]);
  assert.equal(tracked.added(), 1);
  assert.ok(tracked.peak() <= 1);
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [500]);

  const cancelled = trackedSignal();
  const slow = {async request<T>(): Promise<T> { return download('preparing') as T; }};
  const running = pollDownloadRequest(slow, 'req-1', {intervalMs: 500, timeoutMs: 10000, signal: cancelled.signal});
  const rejected = assert.rejects(running, (error: {code?: string; message?: string}) => error.code === 'cancelled' && error.message === 'The download was cancelled.');
  await delay(100);
  assert.equal(cancelled.live(), 1);
  cancelled.controller.abort();
  await rejected;
  assert.equal(cancelled.live(), 0);
});

const queueHeader = (over: Record<string, unknown> = {}) => ({id: 'q_1', revision: '1', total: 1024, repeat: 'off', segments: [{id: 'g1', kind: 'selector', label: 'Everything', count: 1024, state: 'building'}], ...over});
const readyHeader = queueHeader({revision: '4', total: 20000, current: {entryId: 's1-731', position: 0}, shuffle: {seed: '5', lap: 0}, segments: [{id: 'g1', kind: 'selector', label: 'Everything', count: 20000, state: 'ready'}]});

test('awaitCurrent detaches each wait listener on the poll timer, keeping the interval floor and reads', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  const seen: number[] = [];
  let reads = 0;
  const client = new QueueClient({async send(request: V1Request) {
    assert.equal(request.path, '/v1/queues/q_1');
    seen.push(tracked.live());
    reads++;
    return {status: 200, headers: {}, body: reads < 4 ? queueHeader() : readyHeader};
  }});
  try {
    const ready = await client.awaitCurrent('q_1', {signal: tracked.signal, intervalMs: 1}).done;
    assert.equal(ready.current?.entryId, 's1-731');
  } finally {audit.restore();}
  assert.equal(reads, 4);
  assert.deepEqual(seen, [0, 0, 0, 0]);
  assert.equal(tracked.added(), 3);
  assert.ok(tracked.peak() <= 1);
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [50, 50, 50]);
});

test('awaitCurrent wake() detaches the waiting listener and checks once, as before', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  let reads = 0;
  const client = new QueueClient({async send() {
    reads++;
    return {status: 200, headers: {}, body: reads < 2 ? queueHeader() : readyHeader};
  }});
  const waiting = client.awaitCurrent('q_1', {signal: tracked.signal, intervalMs: 60000});
  try {
    assert.equal(await waitForListeners(tracked, 1), 1);
    waiting.wake();
    const ready = await waiting.done;
    assert.equal(ready.current?.entryId, 's1-731');
  } finally {tracked.controller.abort(); await waiting.done.catch(() => {}); audit.restore();}
  assert.equal(reads, 2);
  assert.equal(tracked.added(), 1);
  assert.equal(tracked.live(), 0);
  assert.deepEqual(audit.delays(), [60000]);
  assert.deepEqual(audit.clears(), audit.handles());
});

test('awaitCurrent abort still resolves the wait then rejects with AbortError, detaching first', async () => {
  const tracked = trackedSignal(), audit = timerAudit();
  let reads = 0;
  const client = new QueueClient({async send() { reads++; return {status: 200, headers: {}, body: queueHeader()}; }});
  try {
    const waiting = client.awaitCurrent('q_1', {signal: tracked.signal, intervalMs: 60000});
    const rejected = assert.rejects(waiting.done, (error: Error) => error.name === 'AbortError');
    assert.equal(await waitForListeners(tracked, 1), 1);
    tracked.controller.abort();
    await rejected;
    assert.equal(tracked.live(), 0);
    await delay(100);
    assert.equal(reads, 1);
    assert.equal(tracked.live(), 0);
    assert.deepEqual(audit.clears(), audit.handles());
  } finally {tracked.controller.abort(); audit.restore();}
});

test('awaitCurrent still ends at once when no segment is building, adding no listener', async () => {
  const tracked = trackedSignal();
  let reads = 0;
  const client = new QueueClient({async send() {
    reads++;
    return {status: 200, headers: {}, body: queueHeader({segments: [{id: 'g1', kind: 'selector', label: 'x', count: 0, state: 'failed'}]})};
  }});
  await assert.rejects(client.awaitCurrent('q_1', {signal: tracked.signal}).done, (error: {code?: string}) => error.code === 'queue_ended');
  assert.equal(reads, 1);
  assert.equal(tracked.added(), 0);
  assert.equal(tracked.live(), 0);
});
