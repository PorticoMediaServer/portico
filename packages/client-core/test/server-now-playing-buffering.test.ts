import test from 'node:test';
import assert from 'node:assert/strict';
import {adminSessionFromServer} from '../src/admin/server-now-playing.ts';
import {stateId} from '../src/server-admin/now-playing.ts';

// A stream whose player is waiting for it (server-next #3055): Buffering, and a problem after ten seconds.
const stream = (over: Record<string, unknown> = {}) => ({id: 'pbs_1', kind: 'vod', state: 'playing', positionMs: 1000, username: 'sam', deviceName: 'TV', away: false,
  route: 'original', delivery: {}, startedAt: '2026-11-12T10:00:00Z', ...over});

test('a stream stuck buffering keeps the server\'s problem and since when; a short wait or a playing stream has none', () => {
  const stuck = adminSessionFromServer(stream({state: 'buffering', problem: true, bufferingSince: '2026-11-12T10:00:05Z'}));
  assert.deepEqual([stuck.state, stuck.problem, stuck.bufferingSince], ['buffering', 'buffering', '2026-11-12T10:00:05Z']);
  const waiting = adminSessionFromServer(stream({state: 'buffering', bufferingSince: '2026-11-12T10:00:05Z'}));
  assert.equal(waiting.problem, undefined);
  assert.equal(adminSessionFromServer(stream({problem: true})).problem, undefined, 'only a buffering stream is a buffering problem');
});

test('Now playing names a buffering stream Buffering, and one that is starting Starting', () => {
  assert.equal(stateId('buffering'), 'web.nowPlaying.state.buffering');
  assert.equal(stateId('preparing'), 'web.nowPlaying.state.starting');
});
