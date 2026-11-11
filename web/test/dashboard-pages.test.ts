import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readDashboardAccounts, readNowPlaying} from '../src/screens/dashboard/pages.ts';
import type {DashboardSessions} from '../src/screens/dashboard/bindings.ts';

const session = (id: string): DashboardSessions['items'][number] => ({id, user: 'actual-account', title: 'Actual title', device: 'Actual device', kind: 'vod', state: 'playing', location: 'local', positionMs: 0, startedAt: '2026-10-23T01:00:00Z', decision: {}});
function page(ids: string[], nextCursor = ''): DashboardSessions {
  return {items: ids.map(session), nextCursor, scope: {serverId: 'server', viewerFence: 'a'.repeat(64)}, totals: {streams: 13, atHome: 13, away: 0, paused: 0, converting: 0, people: 1, bandwidthKbps: 0}};
}
test('Now playing follows opaque pages without truncating the drawn stack or inferring totals', async () => {
  const cursors: (string | undefined)[] = [], second = page(['9', '10', '11', '12']);
  const out = await readNowPlaying({read: {sessions: async cursor => {cursors.push(cursor); return cursor ? second : page(Array.from({length: 10}, (_, i) => String(i)), 'opaque-next');}}}, new AbortController().signal);
  assert.deepEqual(cursors, [undefined, 'opaque-next']); assert.equal(out.items.length, 13); assert.equal(out.totals, second.totals);
});
test('a moving page updates the same identity once and a repeated cursor fails closed', async () => {
  const out = await readNowPlaying({read: {sessions: async cursor => cursor ? {...page(['one']), items: [{...session('one'), state: 'paused'}]} : page(['one'], 'next')}}, new AbortController().signal);
  assert.equal(out.items.length, 1); assert.equal(out.items[0].state, 'paused');
  let calls = 0;
  await assert.rejects(readNowPlaying({read: {sessions: async () => {calls++; return page(['one'], 'repeated');}}}, new AbortController().signal), /invalid_dashboard_session_cursor/);
  assert.equal(calls, 2);
});
test('aborted paging releases neither a partial stack nor later private account options', async () => {
  const controller = new AbortController(); let calls = 0;
  await assert.rejects(readNowPlaying({read: {sessions: async () => {calls++; controller.abort(); return page(['one'], 'next');}}}, controller.signal), {name: 'AbortError'});
  assert.equal(calls, 1);
  await assert.rejects(readDashboardAccounts({read: {accounts: async () => {throw new Error('must not read');}}}, controller.signal), {name: 'AbortError'});
});
