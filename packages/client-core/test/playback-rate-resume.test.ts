import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService, type PlaybackApi} from '../src/index.ts';
import {parseAudioRenderV2} from '../src/playback-v1/audio-render.ts';

// A book started part-way on the device's own decoder: the start is a seek to the saved position.
const audio = parseAudioRenderV2({version: 2, mode: 'direct', id: 'm1', url: '/v1/media/g/audio', container: 'mp3', codec: 'mp3', sampleRate: 44100, channels: 1, bytes: 5_000_000, prefetchBytes: 100,
  durationFrames: 26_460_000, trim: {startFrames: 1105, endFrames: 127, source: 'lame'}, seekIndex: {url: '/v1/media/g/audio-index'}});

async function resumed() {
  const api: PlaybackApi = {createPlayback: async () => { throw new Error('queue only'); }, progressPlayback: async () => {}, stopPlayback: async () => {}};
  const s = new PlaybackService(api, () => 'device');
  // As a v1 queue starts a book: the session arrives with the saved position as its resume point.
  const intent = s.beginQueueIntent('chapter', true);
  assert.equal(await s.prepareQueueIntent(intent), true);
  await s.adoptQueueSession('chapter', {id: 's1', generation: 1, mode: 'direct', streamUrl: '/media', duration: 600, resumeSeconds: 377.4, audio} as never, intent);
  const snap = s.getSnapshot();
  assert.ok(snap.audioEffects.rendering, 'decoded on the device');
  assert.equal(snap.pendingSeek?.positionSeconds, 377.4);
  assert.equal(snap.positionSeconds, 0, 'nothing reported yet');
  return s;
}

test('the speed applied as a resume starts never sends it back to the start', async () => {
  const s = await resumed();
  // The listening service applies the held speed to every new session: the same speed restarts nothing.
  const revision = s.getSnapshot().pendingSeek!.revision;
  s.setPlaybackRate(1);
  assert.equal(s.getSnapshot().pendingSeek?.revision, revision);
  assert.equal(s.getSnapshot().pendingSeek?.positionSeconds, 377.4);
  // Another speed restarts the decoder where the resume is going, not at the last reported position.
  s.setPlaybackRate(1.5);
  assert.equal(s.getSnapshot().audioEffects.rate, 1.5);
  assert.equal(s.getSnapshot().pendingSeek?.positionSeconds, 377.4);
  s.leave();
});

test('once playing, a speed change restarts from where the listener is', async () => {
  const s = await resumed();
  const snap = s.getSnapshot();
  s.ready(snap.intentId);
  s.seekApplied(snap.intentId, snap.pendingSeek!.revision, 377.4, 'playing');
  s.fact(s.getSnapshot().intentId, 380, 'playing');
  assert.equal(s.getSnapshot().pendingSeek, undefined);
  s.setPlaybackRate(2);
  assert.equal(s.getSnapshot().pendingSeek?.positionSeconds, 380);
  s.leave();
});
