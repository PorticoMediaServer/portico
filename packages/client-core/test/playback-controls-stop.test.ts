import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseRemotePlaybackControls, playbackControlsService} from '../src/playback-controls.ts';

// At a stream limit a person sees the account's streams and stops one (07 rule 38).
test("the account's streams are read from the rebuilt server and one is stopped there", async () => {
  const calls: unknown[][] = [];
  const answer = {limit: 2, items: [
    {id: 'pb1', deviceId: 'd1', deviceName: 'Living room TV', kind: 'vod', state: 'paused', itemId: 'it1', title: 'Alien', positionMs: 1000, durationMs: 7_000_000, startedAt: '2026-11-16T10:00:00Z', current: false},
    {id: 'pb2', deviceId: 'd2', deviceName: 'Phone', kind: 'live', state: 'playing', positionMs: 0, startedAt: '2026-11-16T10:00:00Z', current: true},
  ]};
  const service = playbackControlsService(async <T>(...args: unknown[]) => {calls.push(args); return (args[1] === 'DELETE' ? undefined : answer) as T;});
  const read = await service.remote();
  assert.deepEqual(calls[0], ['/v1/playback/sessions']);
  assert.deepEqual(read.streamLimit, {scope: 'account', limit: 2, playing: 2});
  assert.deepEqual(read.items.map(row => [row.sessionId, row.device.name, row.item?.title, row.actions]), [
    ['pb1', 'Living room TV', 'Alien', ['resume', 'seek', 'stop']],
    // A title the profile may not see is listed without its name; a channel has no seeking.
    ['pb2', 'Phone', undefined, ['pause', 'stop']],
  ]);
  await service.stopAccount(read.items[0]!);
  assert.deepEqual(calls[1], ['/v1/playback/sessions/pb1', 'DELETE', {}]);
  assert.equal(parseRemotePlaybackControls({items: []}).streamLimit, undefined, 'no limit set, none drawn');
});
