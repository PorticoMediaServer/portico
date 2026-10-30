import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService, type PlaybackApi, type PlaybackSession} from '../src/index.ts';

function setup(options: {seekTimeoutMs?: number; startupTimeoutMs?: number} = {}) {
  let created = 0;
  const api: PlaybackApi = {
    createPlayback: async () => ({id: 's' + (++created), generation: 1, streamUrl: '/v1/media/a/master.m3u8', mode: 'hls', duration: 600, resumeSeconds: 30}),
    stopPlayback: async () => {}, progressPlayback: async () => {},
  };
  return new PlaybackService(api, () => 'r' + (++created), options);
}

test('a seek is not failed while the engine says media is still arriving; silence still fails it', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const service = setup({seekTimeoutMs: 40});
  await service.play('movie');
  const s = service.getSnapshot();
  service.ready(s.intentId);
  assert.ok(service.seekApplied(s.intentId, s.pendingSeek!.revision, 30, 'playing'));
  service.seek(300);
  // A weak server restarting a conversion: bytes trickle in for four times the window.
  for (let i = 0; i < 8; i++) {
    t.mock.timers.tick(20);
    assert.ok(service.getSnapshot().pendingSeek, 'progress keeps the seek pending beyond its original deadline');
    service.engineProgress(service.getSnapshot().intentId);
  }
  assert.ok(service.getSnapshot().pendingSeek, 'slow is not failed');
  assert.equal(service.getSnapshot().seekError, undefined);
  // Then nothing at all for a full window.
  t.mock.timers.tick(39);
  assert.ok(service.getSnapshot().pendingSeek, 'silence just short of a full window is allowed');
  assert.equal(service.getSnapshot().seekError, undefined);
  t.mock.timers.tick(1);
  assert.equal(service.getSnapshot().pendingSeek, undefined);
  assert.ok(service.getSnapshot().seekError);
  assert.equal(service.getSnapshot().failedSeek?.positionSeconds, 300);
  service.leave();
});

test('a start is not failed while media is still arriving either, and progress for another play changes nothing', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const service = setup({startupTimeoutMs: 40, seekTimeoutMs: 60_000});
  await service.play('movie');
  const intent = service.getSnapshot().intentId;
  for (let i = 0; i < 6; i++) {
    t.mock.timers.tick(20);
    assert.equal(service.getSnapshot().phase, 'starting');
    service.engineProgress(intent);
  }
  assert.equal(service.getSnapshot().phase, 'starting');
  t.mock.timers.tick(20); service.engineProgress(intent - 1);
  t.mock.timers.tick(19); service.engineProgress(intent - 1);
  assert.equal(service.getSnapshot().phase, 'starting', 'the current window has not expired yet');
  t.mock.timers.tick(1);
  assert.equal(service.getSnapshot().phase, 'error', 'a stale intent cannot hold the window open');
  service.leave();
});

test('a re-planned presentation replaces the old one in place: same session, same intent, at the picture’s position', async () => {
  const service = setup({seekTimeoutMs: 60_000, startupTimeoutMs: 60_000});
  await service.play('movie');
  const s = service.getSnapshot();
  service.ready(s.intentId);
  service.seekApplied(s.intentId, s.pendingSeek!.revision, 30, 'playing');
  service.fact(s.intentId, 95, 'playing');
  service.pause();
  const next: PlaybackSession = {...service.getSnapshot().session!, generation: 2, streamUrl: '/v1/media/b/master.m3u8'};
  assert.equal(service.installPresentation(next), true);
  const after = service.getSnapshot();
  assert.equal(after.intentId, s.intentId, 'the same play');
  assert.equal(after.intent, 'paused', 'a change made while paused stays paused');
  assert.equal(after.session!.generation, 2);
  assert.equal(after.session!.streamUrl, '/v1/media/b/master.m3u8');
  assert.equal(after.phase, 'starting');
  assert.equal(after.pendingSeek!.positionSeconds, 95);
  assert.equal(after.positionSeconds, 95, 'the screen keeps showing where the viewer is');
  // The same generation again is not a change; an older one or another session is refused.
  assert.equal(service.installPresentation(next), true);
  assert.equal(service.getSnapshot().seekRevision, after.seekRevision);
  assert.equal(service.installPresentation({...next, generation: 1}), false);
  assert.equal(service.installPresentation({...next, id: 'other', generation: 3}), false);
  service.leave();
});
