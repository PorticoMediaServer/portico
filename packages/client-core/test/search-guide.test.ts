import test from 'node:test';
import assert from 'node:assert/strict';
import {readSearchLiveGuides, type SearchGuideRoute} from '../src/query/search-guide.ts';

const route: SearchGuideRoute = {kind: 'live-source', sourceId: 'source', timezone: 'UTC', search: '', start: '2026-11-04T12:00:00Z', end: '2026-11-04T15:00:00Z'};
const response = () => ({protocolVersion: '1.0', serverId: 'server', guide: {state: 'no-channels', viewerFence: 'viewer', ...route,
  channels: [], sources: [], nextCursor: '', observedAt: route.start}});
test('Search guide decoration preserves sequential 50-channel requests and per-response delivery', async () => {
  const signal = new AbortController().signal, calls: unknown[][] = [], order: string[] = [];
  const channels = Array.from({length: 51}, (_, index) => `channel-${index}`);
  const api = {async request(...args: unknown[]) {calls.push(args);order.push('request');return response();}};
  const complete = await readSearchLiveGuides(api, 'server', route, channels, {signal, isCurrent: () => true, receive: () => {order.push('receive');}});
  assert.equal(complete, true);assert.deepEqual(order, ['request', 'receive', 'request', 'receive']);
  assert.deepEqual(calls, [channels.slice(0, 50), channels.slice(50)].map(ids => ['/v1/guide?' + new URLSearchParams({...route, channels: ids.join(','), limit: '50'}), 'GET', undefined, signal]));
});
test('a retired Search cannot parse/publish a late guide or issue the next batch', async () => {
  let calls = 0, received = 0;
  const complete = await readSearchLiveGuides({async request() {calls++;return {}; }}, 'server', route, Array.from({length: 51}, String), {
    signal: new AbortController().signal, isCurrent: () => false, receive: () => {received++;},
  });
  assert.equal(complete, false);assert.equal(calls, 1);assert.equal(received, 0);
});
test('Search guide failures are surfaced once without changing its retry budget', async () => {
  let calls = 0;const error = new TypeError('offline');
  await assert.rejects(readSearchLiveGuides({async request() {calls++;throw error;}}, 'server', route, ['channel'], {
    signal: new AbortController().signal, isCurrent: () => true, receive() {},
  }), value => value === error);
  assert.equal(calls, 1);
});
