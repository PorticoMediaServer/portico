import test from 'node:test';
import assert from 'node:assert/strict';
import {clearSearchHistory, deleteSearchHistoryEntry, readSearchHistory} from '../src/search.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};

test('recent searches are read from the server, newest first, and cleared there', async () => {
  const calls: string[] = [];
  const api = {request: async <T>(path: string, method = 'GET') => {
    calls.push(method + ' ' + path);
    return (method === 'GET' ? {items: [{query: 'harbor'}, {query: 'dune'}, {query: 'harbor'}, {}]} : undefined) as T;
  }};
  const page = await readSearchHistory(api, scope);
  assert.deepEqual(page.entries.map(entry => entry.q), ['harbor', 'dune'], 'a repeat keeps its first place; an unreadable one is left out');
  await clearSearchHistory(api);
  assert.deepEqual(calls, ['GET /v1/search/history', 'DELETE /v1/search/history']);
});

test('removing one recent search names it, then reads the recents the server now holds', async () => {
  const calls: string[] = [];
  const api = {request: async <T>(path: string, method = 'GET') => {calls.push(method + ' ' + path); return (method === 'GET' ? {items: [{query: 'dune'}]} : undefined) as T;}};
  const page = await deleteSearchHistoryEntry(api, scope, ' harbor bay ');
  assert.deepEqual(calls, ['DELETE /v1/search/history?query=harbor+bay', 'GET /v1/search/history']);
  assert.deepEqual(page.entries.map(entry => entry.q), ['dune']);
});
