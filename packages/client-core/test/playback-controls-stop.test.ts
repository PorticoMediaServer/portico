import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseRemotePlaybackControls, playbackControlsService} from '../src/playback-controls.ts';

const row = (generation: number) => ({sessionId: 'original', generation, revision: '3', state: 'playing', positionMs: 1000, actions: ['stop'], device: {id: 'device', name: 'TV'}, kind: 'video'});
test('account Stop captures the selected generation before quota retry or newer controls read', async () => {
  const bodies: unknown[] = [];
  const service = playbackControlsService(async <T>(_path: string, _method?: string, body?: unknown) => {
    bodies.push(body);
    if (bodies.length === 1) throw new Error('writer_capacity');
    return undefined as T;
  });
  const selected = parseRemotePlaybackControls({items: [row(1)], nextCursor: ''}).items[0]!;
  await assert.rejects(service.stopAccount(selected), /writer_capacity/);
  const successor = parseRemotePlaybackControls({items: [row(2)], nextCursor: ''}).items[0]!;
  assert.equal(successor.sessionId, selected.sessionId);
  await service.stopAccount(selected);
  assert.deepEqual(bodies, [{generation: 1}, {generation: 1}]);
});
test('remote Stop refuses controls without an exact generation and sends nothing', async () => {
  const {generation: _generation, ...legacy} = row(1);
  for (const value of [legacy, {...row(1), generation: null}, row(0), row(1.5), row(2147483648)]) {
    assert.throws(() => parseRemotePlaybackControls({items: [value], nextCursor: ''}));
  }
  let sent = 0;
  const service = playbackControlsService(async <T>() => {sent++;return undefined as T;});
  await assert.rejects(service.stopAccount({sessionId: 'original', generation: undefined as unknown as number}));
  assert.equal(sent, 0);
});
