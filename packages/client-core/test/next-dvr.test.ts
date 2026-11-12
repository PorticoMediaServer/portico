import test from 'node:test';
import assert from 'node:assert/strict';
import {NextDVRClient, nextRecording} from '../src/next-dvr.ts';
import type {DvrRecording} from '../../contracts/src/next/api.ts';

const booking = (patch: Partial<DvrRecording> = {}): DvrRecording => ({id: 'rec_1', channel: 'ch_1', title: 'News', startsAt: '2026-11-12T20:00:00Z', endsAt: '2026-11-12T21:00:00Z',
  padBeforeMs: 60_000, padAfterMs: 0, priority: 0, state: 'scheduled', inGuide: true, keep: false, fromRule: false, ...patch} as DvrRecording);

test('each new-server booking state reads as the state the recordings screens draw, and a conflict shows as one', () => {
  const states = (['scheduled', 'recording', 'recorded', 'partial', 'failed', 'cancelled'] as const).map(state => nextRecording(booking({state})).state);
  assert.deepEqual(states, ['scheduled', 'recording', 'completed', 'incomplete-playable', 'failed', 'cancelled']);
  const conflicted = nextRecording(booking({conflict: true} as Partial<DvrRecording>));
  assert.equal(conflicted.state, 'conflicted');assert.equal(conflicted.conflicts.length, 1);
  assert.equal(nextRecording(booking()).options.beforeSeconds, 60);
  assert.equal(nextRecording(booking()).bytes, 0, 'the server gives no size');
});

test('the rule count is every page of rules, since the server gives no count', async () => {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    const url = new URL(path, 'http://x');
    if (url.pathname === '/v1/dvr') return {upcoming: 2, recorded: 1, unwatched: 0, conflicts: 0, freeBytes: 1, hoursLeft: 1, keepRule: '', folderOk: true, canRecord: true} as T;
    if (url.pathname === '/v1/dvr/recordings') return {items: []} as T;
    const rule = (id: string) => ({id, title: id, source: 'lsr', byTitle: true, newOnly: true, priority: 0, paused: false});
    return (url.searchParams.get('cursor') ? {items: [rule('c')]} : {items: [rule('a'), rule('b')], nextCursor: 'p2'}) as T;
  }};
  const summary = await new NextDVRClient(api).summary();
  assert.deepEqual([summary.rules, summary.upcoming, summary.groups], [3, 2, 0]);
  assert.ok(paths.includes('/v1/dvr/rules?cursor=p2'));
});
