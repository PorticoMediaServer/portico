import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {audioGainLinear, audioTrimWindow, parseAudioRenderV2, parseSeekIndex, playableAudio, type PlayableAudioRender} from '../src/playback-v1/audio-render.ts';
import {parsePresentation} from '../src/playback-v1/types.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';

const FIXTURES = fileURLToPath(new URL('../../../fixtures/audio-gapless/', import.meta.url));
const direct = {version: 2, mode: 'direct', id: 'm1', url: '/v1/media/g/audio', container: 'mp3', codec: 'mp3', sampleRate: 44100, channels: 2, bytes: 100, prefetchBytes: 100,
  durationFrames: 221104, trim: {startFrames: 1105, endFrames: 127, source: 'lame'}, gain: {trackDb: -6, albumDb: -4, trackPeak: 0.9, source: 'tags'}};

test('§18.1 v2: a direct plan reads whole; unknown versions and malformed plans do not', () => {
  const p = parseAudioRenderV2(direct);
  assert.ok(playableAudio(p));
  assert.equal(p.trim!.startFrames, 1105);
  assert.equal(parseAudioRenderV2({...direct, version: 1}), undefined);
  assert.equal(parseAudioRenderV2({...direct, mode: 'pcm'}), undefined);
  // Missing what gapless needs: never guessed, read as unavailable (the reason is kept).
  const partial = parseAudioRenderV2({...direct, trim: undefined, reason: 'x'});
  assert.equal(partial?.mode, 'unavailable');
  assert.equal(partial?.reason, 'x');
  assert.ok(!playableAudio(partial));
  const unavailable = parseAudioRenderV2({version: 2, mode: 'unavailable', id: 'm2', reason: 'The owner doesn’t allow converting audio.'});
  assert.deepEqual(unavailable, {version: 2, mode: 'unavailable', id: 'm2', reason: 'The owner doesn’t allow converting audio.'});
});

test('a direct plan in a container with no index of its own names the server’s seek index; nothing to convert', () => {
  const seekIndex = {url: '/v1/media/g/audio-index'};
  assert.deepEqual(parseAudioRenderV2({...direct, seekIndex})?.seekIndex, seekIndex);
  assert.equal(parseAudioRenderV2({...direct, seekIndex: {url: 7}})?.seekIndex, undefined);
  assert.equal(parseAudioRenderV2({...direct, mode: 'converted', seekIndex})?.seekIndex, undefined);
  assert.equal((parseAudioRenderV2({...direct, deepStart: {url: '/x'}}) as Record<string, unknown>)?.deepStart, undefined, 'no converted part-way start any more');
  const index = parseSeekIndex({version: 1, offsets: [417, 52_100, 104_903], frames: [0, 132_480, 264_960]});
  assert.deepEqual(index, {offsets: [417, 52_100, 104_903], frames: [0, 132_480, 264_960]});
  for (const bad of [
    {version: 2, offsets: [1], frames: [0]},
    {version: 1, offsets: [], frames: []},
    {version: 1, offsets: [1, 2], frames: [0]},
    {version: 1, offsets: [5, 5], frames: [0, 10]},
    {version: 1, offsets: [5, 9], frames: [10, 0]},
    {version: 1, offsets: [-1], frames: [0]},
    {version: 1, offsets: [1.5], frames: [0]},
    {version: 1, offsets: Array.from({length: 4097}, (_, i) => i + 1), frames: Array.from({length: 4097}, (_, i) => i)},
  ]) assert.equal(parseSeekIndex(bad), undefined, JSON.stringify(bad).slice(0, 60));
});

test('§18.1 v2: a presentation carries the v2 plan as `audio`', () => {
  const p = parsePresentation({generation: 1, mode: 'direct', url: '/v1/media/g/file', startPositionMs: 0, subtitles: [], decision: {}, audioRender: direct});
  assert.equal(p.audio?.mode, 'direct');
});

test('§9.6 rule 7: gain with peak limiting; album falls back to track; off and unknown are unity', () => {
  const p = parseAudioRenderV2(direct)!;
  assert.equal(audioGainLinear(p, 'off'), 1);
  assert.ok(Math.abs(audioGainLinear(p, 'track') - Math.pow(10, -6 / 20)) < 1e-12);
  // Album −4 dB with only a track peak of 0.9: 0.631 × 0.9 < 1, unlimited.
  assert.ok(Math.abs(audioGainLinear(p, 'album') - Math.pow(10, -4 / 20)) < 1e-12);
  // +6 dB preamp on track: 10^(0/20)=1, and 0.9 × 1 ≤ 1, unlimited; +10 dB would clip, so limited to 1/0.9.
  assert.ok(Math.abs(audioGainLinear(p, 'track', 10) - 1 / 0.9) < 1e-12);
  assert.equal(audioGainLinear(parseAudioRenderV2({...direct, gain: {source: 'tags'}}), 'track'), 1);
});

test('§18.1 trim: the window over raw frames, whether or not the platform decoder already skipped', () => {
  const p = parseAudioRenderV2(direct) as PlayableAudioRender;
  assert.deepEqual(audioTrimWindow(p, 0), {from: 1105, to: 1105 + 221104});
  assert.deepEqual(audioTrimWindow(p, 1105), {from: 0, to: 221104});
});

test('§18.8 fixtures: every sidecar is self-consistent (raw = start + duration + end) and its file exists', () => {
  const sidecars = readdirSync(FIXTURES).filter(f => f.endsWith('.json') && f !== 'album.json');
  assert.ok(sidecars.length >= 7 + 36, `${sidecars.length} sidecars`);
  for (const f of sidecars) {
    const s = JSON.parse(readFileSync(join(FIXTURES, f), 'utf8'));
    assert.equal(s.rawFrames, s.trim.startFrames + s.durationFrames + s.trim.endFrames, f);
    assert.ok(readFileSync(join(FIXTURES, s.file)).length > 0, s.file);
  }
  const album = JSON.parse(readFileSync(join(FIXTURES, 'album.json'), 'utf8'));
  for (const names of Object.values(album.tracks) as string[][]) {
    let offset = 0;
    for (const name of names) {
      const s = JSON.parse(readFileSync(join(FIXTURES, name + '.json'), 'utf8'));
      assert.equal(s.signal.offset, offset, `${name} continues the album signal`);
      offset += s.durationFrames;
    }
  }
});

test('fake server v2: the plan comes from the item’s file, served with Range (206, suffix, 416)', async () => {
  const side = JSON.parse(readFileSync(join(FIXTURES, 'single-mp3-lame.json'), 'utf8'));
  const bytes = new Uint8Array(readFileSync(join(FIXTURES, side.file)));
  const server = new FakePlaybackServer({audioSource: id => id.startsWith('song') ? {bytes, container: side.container, codec: side.codec, sampleRate: side.sampleRate, channels: side.channels, durationFrames: side.durationFrames, trim: side.trim} : undefined});
  const http = fakeHttp(server);
  const started = await http.send({method: 'POST', path: '/v1/playback/sessions', headers: {'Idempotency-Key': 'k-000000000001'}, body: {itemId: 'song-1', state: 'playing'}});
  assert.equal(started.status, 201);
  const plan = parseAudioRenderV2((started.body as any).presentation.audioRender);
  assert.ok(playableAudio(plan));
  assert.equal(plan.durationFrames, 221104);
  assert.deepEqual(plan.trim, {startFrames: 1105, endFrames: 127, source: 'lame'});
  const part = await http.send({method: 'GET', path: plan.url, headers: {Range: 'bytes=0-9'}});
  assert.equal(part.status, 206);
  assert.equal(part.headers['Content-Range'], `bytes 0-9/${bytes.length}`);
  assert.deepEqual([...(part.body as Uint8Array)], [...bytes.subarray(0, 10)]);
  const tail = await http.send({method: 'GET', path: plan.url, headers: {Range: 'bytes=-4'}});
  assert.deepEqual([...(tail.body as Uint8Array)], [...bytes.subarray(bytes.length - 4)]);
  assert.equal((await http.send({method: 'GET', path: plan.url, headers: {Range: `bytes=${bytes.length}-`}})).status, 416);
});

test('normalization uses unity without gain and cannot boost an unknown peak',()=>{
 assert.equal(audioGainLinear(undefined,'track'),1);
 assert.equal(audioGainLinear({gain:{trackDb:6,source:'tag'}} as any,'track'),1);
 assert.ok(Math.abs(audioGainLinear({gain:{trackDb:-6,source:'tag'}} as any,'track')-Math.pow(10,-6/20))<1e-12);
 assert.equal(audioGainLinear({gain:{trackDb:6,trackPeak:0.25,source:'tag'}} as any,'track'),Math.pow(10,6/20));
 assert.equal(audioGainLinear({gain:{trackDb:30,trackPeak:0.1,source:'tag'}} as any,'track'),10);
});
