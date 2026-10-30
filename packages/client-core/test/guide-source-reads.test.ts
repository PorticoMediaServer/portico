import test from 'node:test';
import assert from 'node:assert/strict';
import type {ChannelApi} from '../src/channel-guide.ts';
import type {ChannelSource} from '../src/guide/types.ts';
import {ChannelSourcesStore} from '../src/guide/source-reads.ts';

const api: ChannelApi = {request: async <T,>() => undefined as T};
const source = (id: string, position = 0): ChannelSource => ({id, position, name: id, type: 'library', recordAvailable: false});
function held() {
  const calls: {api: ChannelApi; signal: AbortSignal; resolve: (sources: readonly ChannelSource[]) => void; reject: (error: unknown) => void}[] = [];
  const read = (api: ChannelApi, signal: AbortSignal) => new Promise<readonly ChannelSource[]>((resolve, reject) => calls.push({api, signal, resolve, reject}));
  return {calls, read};
}
const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };

test('rail and screen share one probe; only the last unsubscribe aborts transport', async () => {
  const {calls, read} = held(), store = new ChannelSourcesStore(api, read);
  const offRail = store.subscribe(() => {}), offScreen = store.subscribe(() => {});
  await settle(); assert.equal(calls.length, 1);
  offRail(); assert.equal(calls[0].signal.aborted, false);
  offScreen(); assert.equal(calls[0].signal.aborted, true);
  const offNew = store.subscribe(() => {}); await settle();
  assert.equal(calls.length, 2);
  calls[0].resolve([source('old')]); calls[1].resolve([source('current')]); await settle();
  assert.deepEqual(store.get().sources.map(s => s.id), ['current']);
  assert.equal(store.get().loading, false); offNew();
});

test('API replacement aborts old credentials and fences old completion even if transport ignores abort', async () => {
  const {calls, read} = held(), store = new ChannelSourcesStore(api, read), off = store.subscribe(() => {});
  await settle();
  const nextApi: ChannelApi = {request: async <T,>() => 'new credentials' as T};
  store.bind(nextApi); await settle();
  assert.equal(calls[0].signal.aborted, true);
  assert.equal(calls[1].api, nextApi);
  calls[1].resolve([source('current')]); await settle();
  calls[0].resolve([source('stale')]); await settle();
  assert.deepEqual(store.get().sources.map(s => s.id), ['current']); off();
});

test('successful probes reuse for a minute across mounts, then refresh without losing retained sources', async () => {
  const {calls, read} = held(); let at = 0;
  const store = new ChannelSourcesStore(api, read, () => at), off = store.subscribe(() => {});
  await settle(); calls[0].resolve([source('second', 2), source('first', 1)]); await settle(); off();
  assert.deepEqual(store.get().sources.map(s => s.id), ['first', 'second']);
  at = 59_999; const offCached = store.subscribe(() => {}); await settle();
  assert.equal(calls.length, 1); offCached();
  at = 60_000; const offRefresh = store.subscribe(() => {}); await settle();
  assert.equal(calls.length, 2); assert.equal(store.get().loading, true);
  assert.equal(store.get().sources.length, 2);
  calls[1].resolve([]); await settle(); assert.equal(store.get().loading, false); offRefresh();
});

test('refresh cancels prior probe, failed probes retry, and an unmounted refresh starts no work', async () => {
  const {calls, read} = held(), store = new ChannelSourcesStore(api, read), off = store.subscribe(() => {});
  await settle(); store.load(); await settle();
  assert.equal(calls[0].signal.aborted, true);
  calls[0].reject(new Error('late error')); calls[1].reject(new Error('unavailable')); await settle();
  assert.equal(store.get().failed, true); off();
  store.load(); await settle(); assert.equal(calls.length, 2);
  const offRetry = store.subscribe(() => {}); await settle();
  assert.equal(calls.length, 3); calls[2].resolve([source('recovered')]); await settle();
  assert.equal(store.get().failed, false); offRetry();
});
