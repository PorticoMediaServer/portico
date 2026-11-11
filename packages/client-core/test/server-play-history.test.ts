import {test} from 'node:test';
import assert from 'node:assert/strict';
import {periodStart, readServerAccounts, readServerPlayHistory} from '../src/admin/server-play-history.ts';

const play = {playedAt: '2026-11-11T20:00:00Z', username: 'ada', profile: 'Kids', deviceName: 'Living room', accountId: 'acc_1', libraryId: 'lib_1', libraryName: 'TV',
  itemId: 'itm_1', kind: 'episode', title: 'Pilot', subtitle: 'The Show', watchedMs: 1_200_000, away: true, delivery: 'converted'};

test('Play history asks the rebuilt server with its own filters and shows each play as a row', async () => {
  const paths: string[] = [];
  const api = {request: async <T,>(path: string) => {paths.push(path); return {items: [play, {...play, kind: 'track', title: 'Song', accountId: undefined, itemId: undefined, libraryId: undefined, delivery: undefined}], nextCursor: '41'} as T;}};
  const page = await readServerPlayHistory(api, {period: '7d', accountId: 'acc_1', media: 'tv', search: ' pilot '}, '57');
  const url = new URL(paths[0], 'https://server.invalid');
  assert.equal(url.pathname, '/v1/admin/play-history');
  assert.deepEqual([...url.searchParams.keys()].sort(), ['cursor', 'from', 'kind', 'person', 'text']);
  assert.equal(url.searchParams.get('kind'), 'episode');
  assert.equal(url.searchParams.get('text'), 'pilot');
  assert.equal(url.searchParams.get('cursor'), '57');
  assert.ok(Math.abs(Date.parse(url.searchParams.get('from')!) - (Date.now() - 7 * 86_400_000)) < 60_000);
  assert.equal(page.nextCursor, '41');
  const [episode, song] = page.entries;
  assert.equal(episode.user, 'ada'); assert.equal(episode.profile, 'Kids'); assert.equal(episode.device, 'Living room');
  assert.equal(episode.parentTitle, 'The Show'); assert.equal(episode.libraryName, 'TV'); assert.equal(episode.delivery, 'converted'); assert.equal(episode.location, 'remote'); assert.equal(episode.watchedMs, 1_200_000);
  assert.notEqual(episode.id, song.id);
  // A removed account or title still lists the play; a song is drawn square.
  assert.equal(song.accountId, ''); assert.equal(song.itemId, undefined); assert.equal(song.kind, 'song');
  assert.equal(song.libraryName, undefined, 'a removed library has no name to show'); assert.equal(song.delivery, undefined);
  await assert.rejects(readServerPlayHistory({request: async <T,>() => ({items: [{title: 'no time'}]}) as T}, {period: 'all'}));
  assert.equal(periodStart('all'), undefined);
});

test('the person filter lists every account, page after page', async () => {
  const cursors: (string | null)[] = [];
  const api = {request: async <T,>(path: string) => {
    const cursor = new URL(path, 'https://server.invalid').searchParams.get('cursor'); cursors.push(cursor);
    return (cursor ? {items: [{id: 'acc_2', username: 'bo'}]} : {items: [{id: 'acc_1', username: 'ada'}], nextCursor: 'p2'}) as T;
  }};
  assert.deepEqual(await readServerAccounts(api), [{accountId: 'acc_1', username: 'ada'}, {accountId: 'acc_2', username: 'bo'}]);
  assert.deepEqual(cursors, [null, 'p2']);
});
