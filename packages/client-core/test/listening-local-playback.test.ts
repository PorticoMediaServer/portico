import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService, type PlaybackSession} from '../src/index.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import type {V1Http} from '../src/playback-v1/http.ts';

test('real local PlaybackService and V1QueuePlayer never request a server listening source', async t => {
  const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
  const paths: string[] = [];
  const http: V1Http = {send: async request => {
    paths.push(request.path);
    assert.equal(request.path, '/v1/listening/preferences', 'a local source ID reached a server request');
    return {status: 200, headers: {}, body: {scope: viewer, data: {revision: 0, bookRate: 1.5, passoutMinutes: 30}}};
  }};
  const api = new V1PlaybackApi(http);
  let serial = 0;
  const playback = new PlaybackService(api, () => `local-intent-${++serial}`);
  const player = new V1QueuePlayer({http, api, playback, viewer});
  t.after(() => { player.dispose(); playback.leave(); });
  player.connect();
  const make = (id: string): PlaybackSession => ({id, generation: 1, streamUrl: 'app-private-source', mode: 'local', duration: 120, resumeSeconds: 0});
  await playback.playLocal('private-file-id', () => make('local:session:one'));
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(playback.getSnapshot().session?.mode, 'local');
  assert.equal(player.listening.getSnapshot().context, null);
  await playback.playLocal('file:another-private-source', () => make('local:session:two'));
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(paths, ['/v1/listening/preferences']);
  assert.equal(player.listening.mayAdvance(), true);
  assert.equal(player.listening.mayTransition(3600), true);
  player.dispose(); playback.leave();
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(paths, ['/v1/listening/preferences']);
});
