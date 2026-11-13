import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import type {V1Http} from '../src/playback-v1/http.ts';

// The ordinary element cannot consume a rendered edge. Its completion must release that
// edge so a later ready track can prepare its own successor (seen in the real cold-album walk).
test('an ordinary completion releases its unused audio preparation', async t => {
  const http: V1Http = {send: async () => { throw new Error('no server needed'); }};
  const api = new V1PlaybackApi(http);
  const playback = new PlaybackService(api, () => 'device');
  const player = new V1QueuePlayer({http, api, playback, transitions: true,
    viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}});
  t.after(() => { player.dispose(); playback.leave(); });
  await playback.playLocal('song1', () => ({id: 'first', generation: 1, mode: 'direct', streamUrl: 'file', duration: 8, resumeSeconds: 0}));
  Object.assign(player, {
    session: {id: 'first', kind: 'audio', itemId: 'song1', presentation: {generation: 1}},
    prepared: {token: 'unused', queueId: 'queue', itemId: 'song2', expiresAtMs: Date.now() + 60000, durationSeconds: 8},
  });
  player.connect();
  const first = playback.getSnapshot();
  playback.ready(first.intentId);
  if (first.pendingSeek) playback.seekApplied(first.intentId, first.pendingSeek.revision, 0, 'playing');
  assert.equal(playback.getSnapshot().audioEffects.rendering, false);
  playback.fact(first.intentId, 8, 'ended');
  assert.equal(Object.getOwnPropertyDescriptor(player, 'prepared')?.value, undefined,
    'the old edge must not block maybePrepareAudio on the next track');
});
