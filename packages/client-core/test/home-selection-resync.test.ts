import test from 'node:test';
import assert from 'node:assert/strict';
import {runSelectionJobs, type HomeJobTask} from '../src/home-selection/execution.ts';
import {EventsClient} from '../src/playback-v1/events.ts';
import type {HomeApi} from '../src/home.ts';

const tick = async () => {for (let i = 0; i < 60; i++) await Promise.resolve();};
const status = (state = 'running') => ({jobId: 'job-1', command: 'refresh', state, total: 1, totalKnown: true, done: state === 'complete' ? 1 : 0, failed: 0});
const task = (): HomeJobTask => ({request: {operationId: 'original-operation', command: 'refresh', selector: {items: {ids: ['title']}}, args: {}}, failures: [], observed: false});
const events = () => new EventsClient({http: {send: async () => {throw new Error('No feed requests');}}});

test('a resync wakes the actual job status wait immediately and never resubmits its operation', async () => {
  const feed = events(), tasks = [task()], controller = new AbortController();
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  let complete = false;
  const api: HomeApi = {request: async (path, method, body) => {calls.push({path, method, body}); return status(method === 'POST' ? 'queued' : complete ? 'complete' : 'running') as never;}};
  try {
    const running = runSelectionJobs(api, tasks, controller.signal, () => true, () => {}, undefined, feed);
    await tick(); assert.equal(calls.filter(c => c.method !== 'POST').length, 1);
    complete = true; feed.deliver({id: 'job-gap', type: 'stream.resync'}); await running;
    assert.equal(calls.filter(c => c.method === 'POST').length, 1);
    assert.equal(calls.filter(c => c.method !== 'POST').length, 2);
    assert.equal(tasks[0]!.request.operationId, 'original-operation'); assert.equal(tasks[0]!.job?.state, 'complete');
    assert.equal(tasks[0]!.observed, true);
  } finally {controller.abort(); feed.resyncReads.dispose();}
});

test('resync debt arriving during a held status GET is retained through its stale answer', async () => {
  const feed = events(), tasks = [task()], controller = new AbortController();
  let gets = 0, posts = 0, release!: () => void;
  const held = new Promise<void>(resolve => {release = resolve;});
  const api: HomeApi = {request: async (_path, method) => {
    if (method === 'POST') {posts++; return status('queued') as never;}
    if (++gets === 1) {await held; return status('running') as never;}
    return status('complete') as never;
  }};
  try {
    const running = runSelectionJobs(api, tasks, controller.signal, () => true, () => {}, undefined, feed);
    await tick(); assert.equal(gets, 1);
    for (let i = 0; i < 20; i++) feed.deliver({id: `gap-${i}`, type: 'stream.resync'});
    release(); await running;
    assert.equal(gets, 2); assert.equal(posts, 1); assert.equal(tasks[0]!.job?.state, 'complete');
  } finally {release(); controller.abort(); feed.resyncReads.dispose();}
});

test('cancelled job observation releases its resync wake and leaves the genuine job for later reconciliation', async () => {
  const feed = events(), tasks = [task()], controller = new AbortController(); let requests = 0;
  const api: HomeApi = {request: async () => {requests++; return status('running') as never;}};
  try {
    const running = runSelectionJobs(api, tasks, controller.signal, () => true, () => {}, undefined, feed);
    await tick(); controller.abort(); await assert.rejects(running, /cancelled/);
    const before = requests; feed.deliver({id: 'retired-job', type: 'stream.resync'}); await tick();
    assert.equal(requests, before); assert.equal(tasks[0]!.job?.jobId, 'job-1'); assert.equal(tasks[0]!.observed, false);
  } finally {controller.abort(); feed.resyncReads.dispose();}
});
