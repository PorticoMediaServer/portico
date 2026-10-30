import test from 'node:test';
import assert from 'node:assert/strict';
import type {ChannelApi} from '../src/channel-guide.ts';
import {ApiError} from '../src/index.ts';
import {dedupedGuideApi, forgetGuideReads, GUIDE_READ_LIMIT} from '../src/guide/reads.ts';

function held() {
  const calls: {path: string; signal?: AbortSignal; resolve: (value: unknown) => void; reject: (error: unknown) => void}[] = [];
  const api: ChannelApi = {request<T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) {
    return new Promise<T>((resolve, reject) => calls.push({path, signal, resolve: value => resolve(value as T), reject}));
  }};
  return {api, calls};
}
const started = () => Promise.resolve();

test('held viewport requests share one actual transport until both subscribers complete', async () => {
  forgetGuideReads();
  const {api, calls} = held(), guide = dedupedGuideApi(api);
  const first = guide.request('/v1/guide?a=1');
  const second = dedupedGuideApi(api).request('/v1/guide?a=1');
  await started();
  assert.equal(calls.length, 1);
  assert.ok(calls[0].signal);
  calls[0].resolve({window: 1});
  assert.deepEqual(await Promise.all([first, second]), [{window: 1}, {window: 1}]);
});

test('one caller aborts alone; the last caller aborts the real transport and a retry gets a new flight', async () => {
  forgetGuideReads();
  const {api, calls} = held(), guide = dedupedGuideApi(api);
  const a = new AbortController(), b = new AbortController();
  const first = guide.request('/v1/guide?a=1', 'GET', undefined, a.signal);
  const second = guide.request('/v1/guide?a=1', 'GET', undefined, b.signal);
  await started();
  a.abort(new Error('left A'));
  await assert.rejects(first, /left A/);
  assert.equal(calls[0].signal?.aborted, false);
  b.abort(new Error('left B'));
  await assert.rejects(second, /left B/);
  assert.equal(calls[0].signal?.aborted, true);
  const retry = guide.request('/v1/guide?a=1');
  await started();
  assert.equal(calls.length, 2);
  calls[0].resolve('stale result'); // A transport ignoring cancellation cannot publish into the retry.
  calls[1].resolve('new result');
  assert.equal(await retry, 'new result');
  const fresh=guide.request('/v1/guide?a=1');await started();assert.equal(calls.length,3);calls[2].resolve('fresh result');assert.equal(await fresh,'fresh result');
});

test('one caller aborting leaves another caller able to receive the result', async () => {
  forgetGuideReads();
  const {api, calls} = held(), guide = dedupedGuideApi(api), controller = new AbortController();
  const abandoned = guide.request('/v1/guide', undefined, undefined, controller.signal);
  const retained = guide.request('/v1/guide');
  await started(); controller.abort(); await assert.rejects(abandoned);
  calls[0].resolve('retained');
  assert.equal(await retained, 'retained');
});

test('completed reads remove abort listeners and do not attach listeners on cached results', async () => {
  forgetGuideReads();
  const {api, calls} = held(), guide = dedupedGuideApi(api), controller = new AbortController();
  let listeners = 0;
  const signal = controller.signal;
  const add = signal.addEventListener.bind(signal), remove = signal.removeEventListener.bind(signal);
  signal.addEventListener = (...args: Parameters<typeof signal.addEventListener>) => { listeners++; add(...args); };
  signal.removeEventListener = (...args: Parameters<typeof signal.removeEventListener>) => { listeners--; remove(...args); };
  const result = guide.request('/v1/guide', undefined, undefined, signal);
  await started(); assert.equal(listeners, 1);
  calls[0].resolve('done'); await result;
  assert.equal(listeners, 0);
  const next=guide.request('/v1/guide',undefined,undefined,signal);await started();calls[1].resolve('fresh');assert.equal(await next,'fresh');
  assert.equal(listeners, 0);
  controller.abort(); assert.equal(calls[0].signal?.aborted, false);
});

test('completed reads fetch again immediately and rejected reads can retry',async()=>{
 forgetGuideReads();let requests=0,fail=false;
 const api:ChannelApi={async request<T>(path:string){requests++;if(fail)throw new Error('retry');return path as T}};
 const guide=dedupedGuideApi(api);
 await guide.request('/v1/guide');await guide.request('/v1/guide');assert.equal(requests,2);
 fail=true;await assert.rejects(guide.request('/v1/guide'),/retry/);fail=false;await guide.request('/v1/guide');assert.equal(requests,4);
});

test('distinct held reads are capped, duplicates still join, and freed capacity admits retry', async () => {
  forgetGuideReads();
  const {api, calls} = held(), guide = dedupedGuideApi(api);
  const flights = Array.from({length: GUIDE_READ_LIMIT}, (_, i) => guide.request(`/v1/guide?i=${i}`));
  const outcomes = Promise.allSettled(flights);
  const duplicate = guide.request('/v1/guide?i=0');
  await started();
  assert.equal(calls.length, GUIDE_READ_LIMIT);
  await assert.rejects(guide.request('/v1/guide?overflow'), error => {
    assert.ok(error instanceof ApiError);
    assert.equal(error.status, 503);
    assert.equal(error.code, 'server_busy');
    assert.equal(error.retryable, true);
    assert.equal(error.retryAfterSeconds, 1);
    return true;
  });
  calls[0].resolve('done'); assert.equal(await duplicate, 'done');
  const retry = guide.request('/v1/guide?overflow');
  await started(); assert.equal(calls.length, GUIDE_READ_LIMIT + 1);
  calls.at(-1)!.resolve('retry'); assert.equal(await retry, 'retry');
  forgetGuideReads(); await outcomes;
});

test('already-aborted calls never start server work; forgetting rejects old subscribers and fences late results', async () => {
  forgetGuideReads();
  const {api, calls} = held(), guide = dedupedGuideApi(api), controller = new AbortController();
  controller.abort(); await assert.rejects(guide.request('/v1/guide', undefined, undefined, controller.signal));
  assert.equal(calls.length, 0);
  const old = guide.request('/v1/guide'); await started();
  forgetGuideReads(); await assert.rejects(old, {name: 'AbortError'});
  assert.equal(calls[0].signal?.aborted, true);
  const fresh = guide.request('/v1/guide'); await started();
  calls[0].resolve('old credentials'); calls[1].resolve('current credentials');
  assert.equal(await fresh, 'current credentials');
  const reread=guide.request('/v1/guide');await started();calls[2].resolve('reread credentials');assert.equal(await reread,'reread credentials');
});

test('API instances isolate credentials and writes/non-guide paths are never cached', async () => {
  forgetGuideReads();
  let calls = 0;
  const first: ChannelApi = {request: async <T,>() => { calls++; return 'first viewer' as T; }};
  const second: ChannelApi = {request: async <T,>() => { calls++; return 'second viewer' as T; }};
  const guide = dedupedGuideApi(first);
  assert.equal(await guide.request('/v1/guide'), 'first viewer');
  assert.equal(await dedupedGuideApi(second).request('/v1/guide'), 'second viewer');
  await guide.request('/v1/guide', 'POST', {}); await guide.request('/v1/guide', 'GET', {});
  await guide.request('/v1/items'); await guide.request('/v1/items');
  assert.equal(calls, 6);
});
