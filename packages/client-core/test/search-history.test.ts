import test from 'node:test';
import assert from 'node:assert/strict';
import {clearSearchHistory, readSearchHistory} from '../src/search.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};

test('recent searches are read from the server and cleared there', async () => {
  const calls: string[] = [];
  const api = {request: async <T>(path: string, method = 'GET') => {
    calls.push(method + ' ' + path);
    return (method === 'GET' ? {serverId: 'server', viewerFence: 'fence', maxEntries: 20, entries: [{q: 'harbor', updatedAt: '2026-10-13T10:00:00Z', uses: 2}, {q: 'dune', updatedAt: '2026-10-12T10:00:00Z', uses: 1}]} : {cleared: 2}) as T;
  }};
  const page = await readSearchHistory(api, scope);
  assert.deepEqual(page.entries.map(entry => entry.q), ['harbor', 'dune']);
  await clearSearchHistory(api);
  assert.deepEqual(calls, ['GET /v1/search/history', 'DELETE /v1/search/history']);
});

test('another server’s history is refused', async () => {
  const api = {request: async <T>() => ({serverId: 'other', viewerFence: 'fence', maxEntries: 20, entries: []}) as T};
  await assert.rejects(readSearchHistory(api, scope));
});
