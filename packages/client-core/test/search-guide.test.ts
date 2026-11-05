import test from 'node:test';
import assert from 'node:assert/strict';
import {readSearchLiveGuideResponse, type SearchGuideRoute} from '../src/query/search-guide.ts';

const route: SearchGuideRoute = {kind: 'live-source', sourceId: 'source', timezone: 'UTC', search: '', start: '2026-11-04T12:00:00Z', end: '2026-11-04T15:00:00Z'};
test('Search guide batches retain exact channel membership, window, limit and signal', async () => {
  const signal = new AbortController().signal, calls: unknown[][] = [], raw = {};
  const api = {request(...args: unknown[]) {calls.push(args);return Promise.resolve(raw);}};
  const channels = Array.from({length: 51}, (_, index) => `channel-${index}`);
  for (const ids of [channels.slice(0, 50), channels.slice(50)]) {
    assert.equal(await readSearchLiveGuideResponse(api, route, ids, signal), raw);
  }
  assert.deepEqual(calls, [channels.slice(0, 50), channels.slice(50)].map(ids => ['/v1/guide?' + new URLSearchParams({...route, channels: ids.join(','), limit: '50'}), 'GET', undefined, signal]));
});
test('Search decoration preserves transport promise identity and leaves guards and parsing at the original caller point', () => {
  const pending = new Promise<unknown>(() => {});
  assert.equal(readSearchLiveGuideResponse({request: () => pending}, route, ['channel'], new AbortController().signal), pending);
});
test('Search guide failures are surfaced once without retrying', async () => {
  let calls = 0;const error = new TypeError('offline');
  await assert.rejects(readSearchLiveGuideResponse({async request() {calls++;throw error;}}, route, ['channel'], new AbortController().signal), value => value === error);
  assert.equal(calls, 1);
});
