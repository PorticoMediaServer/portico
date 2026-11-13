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

test('summary reads the server aggregates once, without scanning groups, recordings or rules', async () => {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    assert.equal(path, '/v1/dvr');
    return {upcoming: 2, recorded: 9000, unwatched: 7, rules: 2345, groups: 3000, conflicts: 0, canRecord: true} as T;
  }};
  const summary = await new NextDVRClient(api).summary();
  assert.deepEqual([summary.rules, summary.upcoming, summary.groups, summary.recorded], [2345, 2, 3000, 9000]);
  assert.deepEqual(paths, ['/v1/dvr']);
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
    if (new URL(path,'https://server').pathname === '/v1/dvr') return {upcoming: 1, recorded: 0, unwatched: 0, rules: 0, groups: 0, conflicts: 0, freeBytes: 1, hoursLeft: 1, keepRule: '', folderOk: true, canRecord: false} as T;
    return {items: []} as T;
  }};
  const client = new NextDVRClient(api);
  client.everyone = true;
  await client.list('upcoming');
  assert.ok(paths.includes('/v1/dvr/recordings?scope=everyone'));
  assert.equal((await client.summary()).rules, 0);
  assert.ok(!paths.some(path => path.startsWith('/v1/dvr/rules')), 'the rules read a profile may not make is never sent');
  assert.ok(paths.includes('/v1/dvr?scope=everyone'));
  assert.equal(paths.length,2,'the summary adds one read, not a recordings scan');
});

// Review desk B, #2896 round two.
test('a command the server refuses on someone else’s recording is not read as losing access to Recordings', async () => {
  // Everyone's: a member cancels a booking another profile made. The server answers 403 not_permitted
  // (dvr.go deleteRecording), which is about that one recording, not about this profile's access.
  const api = {async request<T>(_path: string, method?: string): Promise<T> {
    if (method === 'DELETE') throw Object.assign(new Error('This action is not permitted.'), {status: 403, code: 'not_permitted'});
    return {items: []} as T;
  }};
  const client = new NextDVRClient(api as never);
  client.everyone = true;
  let lostAccess = false;
  client.onAccessChanged(() => {lostAccess = true;});
  const theirs = nextRecording({id: 'rec_1', channel: 'BBC One', title: 'News', startsAt: '2026-11-12T20:00:00Z', endsAt: '2026-11-12T21:00:00Z',
    padBeforeMs: 0, padAfterMs: 0, priority: 0, state: 'scheduled', inGuide: true, keep: false, fromRule: false, bookedBy: 'Ana'} as DvrRecording);
  await assert.rejects(client.cancel(theirs));
  assert.equal(lostAccess, false, 'the whole Recordings screen was retired as "no access"');
});
test('a refused read still retires what is shown', async () => {
  const api = {async request<T>(): Promise<T> { throw Object.assign(new Error('forbidden'), {status: 403}); }};
  const client = new NextDVRClient(api as never);
  let lostAccess = false;
  client.onAccessChanged(() => {lostAccess = true;});
  await assert.rejects(client.list('upcoming'));
  assert.equal(lostAccess, true);
});

// The recording facts the server sends (#2947): size, this profile's place and watched mark, the library, who may change it,
// and at a conflict how many tuners there are and what holds them.
test('a booking carries the server\'s size, place, watched mark, library, permissions and tuner facts', () => {
  const shown = nextRecording(booking({state: 'recorded', itemId: 'itm_1', channelId: 'chn_1', channel: 'Harbour News', libraryId: 'lib_rec', bytes: 2_500_000_000,
    positionMs: 600_000, durationMs: 1_800_000, watched: false, mayChange: true, mayCancel: false} as Partial<DvrRecording>));
  assert.deepEqual([shown.bytes, shown.libraryId, shown.positionMs, shown.durationMs, shown.watched, shown.mayChange, shown.mayCancel, shown.channel?.id, shown.channel?.name],
    [2_500_000_000, 'lib_rec', 600_000, 1_800_000, false, true, false, 'chn_1', 'Harbour News']);
  const clash = nextRecording(booking({conflict: true, tuners: 2, tunersUsedBy: ['Evening Report', 'A recording'], mayChange: false, mayCancel: true} as Partial<DvrRecording>));
  assert.equal(clash.conflicts[0]?.capacity, 2);
  assert.deepEqual(clash.tunersUsedBy, ['Evening Report', 'A recording']);
  assert.deepEqual([clash.mayChange, clash.mayCancel], [false, true]);
  // A server that does not send them: nothing is made up.
  const plain = nextRecording(booking({itemId: 'itm_2'}));
  assert.deepEqual([plain.bytes, plain.watched, plain.mayChange, plain.conflicts.length], [0, undefined, undefined, 0]);
});

test('the library comes with the title: no item is read to find it', async () => {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    return {items: [booking({id: 'r1', state: 'recorded', itemId: 'itm_1', libraryId: 'lib_rec', mayChange: true} as Partial<DvrRecording>)]} as T;
  }};
  const page = await new NextDVRClient(api).list('history');
  assert.equal(page.recordings[0]!.libraryId, 'lib_rec');
  assert.ok(!paths.some(path => path.startsWith('/v1/items/')));
});

test('Delete says what it removes as the server has it now: its size and whether anyone is playing it', async () => {
  const api = {async request<T>(): Promise<T> { return booking({id: 'r1', state: 'recorded', bytes: 3_000_000_000, playingNow: 2, keep: true} as Partial<DvrRecording>) as T; }};
  const preview = await new NextDVRClient(api).deletePreview(nextRecording(booking({id: 'r1', state: 'recorded'})));
  assert.deepEqual([preview.bytes, preview.activeReaders, preview.keep], [3_000_000_000, true, true]);
});

test('Storage and the counts carry the server\'s space facts and whether everyone\'s recordings may be seen', async () => {
  const dvr = {upcoming: 1, recorded: 0, unwatched: 0, rules: 0, groups: 0, conflicts: 0, freeBytes: 1e12, hoursLeft: 300, keepRule: 'Until space is needed', folderOk: true, canRecord: false,
    usedBytes: 2e11, floorBytes: 5e10, capBytes: 0, keepDays: 30, keepNewest: 0, upcomingBytes: 4e10, upcomingHours: 12, everyonesRecordings: false};
  const api = {async request<T>(path: string): Promise<T> { return (path === '/v1/dvr' ? dvr : {items: []}) as T; }};
  const client = new NextDVRClient(api);
  const space = await client.storage();
  assert.deepEqual([space.usedBytes, space.floorBytes, space.capBytes, space.upcomingBytes, space.upcomingHours], [2e11, 5e10, 0, 4e10, 12]);
  assert.equal((await client.summary()).everyonesRecordings, false);
});


test('groups keep the server counts and cursor with one scoped page read', async () => {
  const paths: string[] = [];
  const latest = booking({id: 'latest', state: 'recorded', itemId: 'item', libraryId: 'lib', mayChange: true} as Partial<DvrRecording>);
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    const query = new URL(path, 'https://server');
    assert.equal(query.pathname, '/v1/dvr/groups');
    assert.equal(query.searchParams.get('sort'), 'oldest');
    assert.equal(query.searchParams.get('cursor'), 'group/2');
    assert.equal(query.searchParams.get('scope'), 'everyone');
    return {items: [{key: 'News', title: 'News', recordings: 2300, unwatched: 12, kept: 8, exceptions: 4, latest: latest.startsAt, recording: latest}], nextCursor: 'group/3'} as T;
  }};
  const client = new NextDVRClient(api);client.everyone = true;
  const page = await client.groups('oldest', 'group/2');
  assert.deepEqual([page.groups[0].recordings, page.groups[0].unwatched, page.groups[0].exceptions], [2300, 12, 4]);
  assert.equal(page.groups[0].recording.libraryId, 'lib');
  assert.equal(page.nextCursor, 'group/3');assert.equal(paths.length, 1);
});

test('recorded group members use the exact server filter and its next page, without a directory scan', async () => {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    const query = new URL(path, 'https://server');
    assert.equal(query.pathname, '/v1/dvr/recordings');
    assert.equal(query.searchParams.get('state'), 'recorded');
    assert.equal(query.searchParams.get('group'), 'News & Sport/Today');
    assert.equal(query.searchParams.get('cursor'), 'members/2');
    assert.equal(query.searchParams.get('scope'), 'everyone');
    return {items: [booking({state: 'recorded', itemId: 'item', libraryId: 'lib'} as Partial<DvrRecording>)], nextCursor: 'members/3'} as T;
  }};
  const client = new NextDVRClient(api);client.everyone = true;
  const page = await client.list('recorded', 'members/2', 'recent', 'News & Sport/Today');
  assert.equal(page.nextCursor, 'members/3');assert.equal(page.recordings.length, 1);assert.equal(paths.length, 1);
});
