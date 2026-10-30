import test from 'node:test';
import assert from 'node:assert/strict';
import {parseOptions} from '../src/playback-v1/types.ts';
import {HttpLocalApi} from '../src/index.ts';
import {localV1Http} from '../src/playback-v1/local-http.ts';
import {PlaybackOptionsClient} from '../src/playback-v1/options.ts';
import {videoAudioFormatName, videoPictureName, videoTrackWords} from '../src/video-track-labels.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const options = (audio: object[]) => parseOptions({itemId: 'song', kind: 'song', versions: [{id: 'version', audio}]});
test('Options retains existing measured source audio fields without deriving lossless or object audio', () => {
  const track = options([{id: 'a1', codec: 'wavpack', sampleRate: 96000, bitDepth: 24, variant: 'hybrid', objectAudio: 'dtsx', dtsx: true}]).versions[0]!.audio[0]!;
  assert.equal(track.sampleRate, 96000);assert.equal(track.bitDepth, 24);assert.equal(track.variant, 'hybrid');assert.equal(track.objectAudio, 'dtsx');assert.equal(track.dtsx, true);
  assert.equal(track.atmos, false);assert.equal(Object.hasOwn(track, 'lossless'), false);
  const unknown = options([{id: 'a1', codec: 'flac', sampleRate: -1, bitDepth: NaN}]).versions[0]!.audio[0]!;
  assert.equal(unknown.sampleRate, undefined);assert.equal(unknown.bitDepth, undefined);assert.equal(unknown.objectAudio, undefined);assert.equal(unknown.dtsx, undefined);
});


/** Exercise the real HTTP/Options client boundary, not a renderer-made MediaVersion. */
async function readStreamFacts(video: readonly object[], audio: readonly object[]) {
  const requests: string[] = [];
  const api = new HttpLocalApi('https://server.example', '', async (input, init) => {
    requests.push(String(input));assert.equal(init?.method, 'GET');
    return new Response(JSON.stringify({itemId: 'movie', kind: 'movie', versions: [{id: 'actual_version', video, audio}]}),
      {status: 200, headers: {'Content-Type': 'application/json'}});
  });
  const result = await new PlaybackOptionsClient(localV1Http(api)).options('movie');
  assert.equal(requests.length, 1);assert.equal(new URL(requests[0]!).pathname, '/v1/items/movie/playback-options');
  return result.versions[0]!;
}
const words = videoTrackWords(defaultI18n.t, 'en-US');

test('OptionsClient retains supplied scan booleans and stored audio profile through HTTP into canonical chooser names', async () => {
  const version = await readStreamFacts([
    {id: 'progressive', codec: 'hevc', height: 1080, interlaced: false, hdr: 'hdr10', hdr10Plus: true},
    {id: 'interlaced', codec: 'hevc', height: 1080, interlaced: true, hdr: 'hdr10', hdr10Plus: false},
  ], [{id: 'audio', codec: 'dts', profile: 'DTS-HD MA', layout: '7.1', title: 'Atmos DTS:X'}]);
  const progressive = version.video[0]!, interlaced = version.video[1]!, audio = version.audio[0]!;
  assert.equal(progressive.interlaced, false);assert.equal(progressive.hdr10Plus, true);
  assert.equal(interlaced.interlaced, true);assert.equal(interlaced.hdr10Plus, false);
  assert.equal(videoPictureName(progressive, words), '1080p HDR10+');
  assert.equal(videoPictureName(interlaced, words), '1080i HDR10');
  assert.equal(audio.profile, 'DTS-HD MA');assert.equal(videoAudioFormatName(audio, words), 'DTS-HD Master Audio 7.1');
  assert.equal(audio.atmos, false);assert.equal(audio.objectAudio, undefined);assert.equal(audio.dtsx, undefined);
});

test('OptionsClient leaves absent, null and wrong-type scan/profile facts unknown without false defaults or label inference', async () => {
  for (const facts of [{}, {interlaced: null, hdr10Plus: null, profile: null}, {interlaced: 'false', hdr10Plus: 'true', profile: 7},
    {interlaced: 0, hdr10Plus: 1, profile: false}, {interlaced: [], hdr10Plus: {}, profile: ['DTS-HD MA']}]) {
    const version = await readStreamFacts([{id: 'video', codec: 'hevc', height: 1080, ...facts}],
      [{id: 'audio', codec: 'dts', title: 'DTS-HD MA', ...facts}]);
    const video = version.video[0]!, audio = version.audio[0]!;
    assert.equal(video.interlaced, undefined);assert.equal(video.hdr10Plus, undefined);assert.equal(audio.profile, undefined);
    assert.equal(Object.hasOwn(video, 'interlaced'), false);assert.equal(Object.hasOwn(video, 'hdr10Plus'), false);assert.equal(Object.hasOwn(audio, 'profile'), false);
    assert.equal(videoPictureName(video, words), '');assert.equal(videoAudioFormatName(audio, words), 'DTS');
  }
});

test('OptionsClient preserves explicit false HDR10+ and an actual empty profile without inventing a technical name', async () => {
  const version = await readStreamFacts([{id: 'video', codec: 'hevc', height: 1080, interlaced: false, hdr: 'hdr10', hdr10Plus: false}],
    [{id: 'audio', codec: 'dts', profile: ''}]);
  const video = version.video[0]!, audio = version.audio[0]!;
  assert.equal(Object.hasOwn(video, 'interlaced'), true);assert.equal(Object.hasOwn(video, 'hdr10Plus'), true);
  assert.equal(video.interlaced, false);assert.equal(video.hdr10Plus, false);assert.equal(videoPictureName(video, words), '1080p HDR10');
  assert.equal(Object.hasOwn(audio, 'profile'), true);assert.equal(audio.profile, '');assert.equal(videoAudioFormatName(audio, words), 'DTS');
});
