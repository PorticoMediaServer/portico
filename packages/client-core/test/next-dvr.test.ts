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

test('a recording that was made plays from the Recorded TV library, which is read once from one of its titles', async () => {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    if (path.startsWith('/v1/items/')) return {id: 'itm_1', libraryId: 'lib_recorded'} as T;
    return {items: [booking({id: 'r1', state: 'partial', itemId: 'itm_1'} as Partial<DvrRecording>), booking({id: 'r2', state: 'failed'})]} as T;
  }};
  const client = new NextDVRClient(api);
  const page = await client.list('history');
  assert.deepEqual(page.recordings.map(one => [one.itemId, one.libraryId]), [['itm_1', 'lib_recorded'], ['', '']]);
  await client.list('recorded');
  assert.equal(paths.filter(path => path.startsWith('/v1/items/')).length, 1);
});

// Review desk B, #2896: a conflict states no tuner count the server did not send, and a booking keeps its channel and who booked it.
test('a conflict states no tuner count the server did not send', () => {
  const shown = nextRecording(booking({conflict: true} as Partial<DvrRecording>));
  assert.equal(shown.conflicts[0]?.capacity, undefined);
  assert.equal(shown.conflicts[0]?.demand, undefined);
});
test('a booking keeps its channel name and who booked it for the row', () => {
  const shown = nextRecording(booking({channel: 'BBC One', bookedBy: 'Ana'} as Partial<DvrRecording>));
  assert.equal(shown.channel?.name, 'BBC One');
  assert.equal(shown.bookedBy, 'Ana');
});
test('a skipped airing reads as Skipped, never Cancelled', () => {
  assert.equal(nextRecording(booking({state: 'skipped'} as Partial<DvrRecording>)).state, 'skipped');
});
test('Everyone\'s asks the server for every profile\'s recordings; a profile that may not record reads no rules', async () => {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    if (path === '/v1/dvr') return {upcoming: 1, recorded: 0, unwatched: 0, conflicts: 0, freeBytes: 1, hoursLeft: 1, keepRule: '', folderOk: true, canRecord: false} as T;
    return {items: []} as T;
  }};
  const client = new NextDVRClient(api);
  client.everyone = true;
  await client.list('upcoming');
  assert.ok(paths.includes('/v1/dvr/recordings?scope=everyone'));
  assert.equal((await client.summary()).rules, 0);
  assert.ok(!paths.some(path => path.startsWith('/v1/dvr/rules')), 'the rules read a profile may not make is never sent');
  assert.ok(paths.includes('/v1/dvr/recordings?state=history&scope=everyone'));
});
