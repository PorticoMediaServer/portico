import test from 'node:test';
import assert from 'node:assert/strict';
import {appleAudioDecodeFor, appleSubtitleRendering} from '../src-next/platform/playback/audio-decode.ios.ts';
import {deviceCapabilityDocument} from '../src-next/platform/playback/capability-document.ios.ts';

const decoder = {codec: 'pcm', containers: ['wav', 'aiff'], sampleRates: [44100, 96000, 192000], maxSampleRate: 192000, maxChannels: 8, maxBitDepth: 24, via: 'AudioConverter+ExtAudioFile'};
test('Apple publishes native decoder rates/layout limits without the obsolete stereo ceiling', () => {
  const audioDecode = appleAudioDecodeFor({audioDecode: [decoder, {...decoder, codec: 'opus', containers: ['ogg'], sampleRates: [48000], maxSampleRate: 48000, via: 'FFmpeg Opus raw-trim'}]});
  assert.equal(audioDecode.length, 2);
  assert.deepEqual(audioDecode[0], decoder);
  assert.deepEqual(audioDecode[1].containers, ['ogg']);
  assert.equal(audioDecode[0].maxChannels, 8);
  const document = deviceCapabilityDocument({native: {audioDecode: [decoder], subtitleRendering: ['text', 'ass', 'pgs', 'vobsub', 'dvb']} as never, appVersion: '1', device: {television: true, pad: false}});
  assert.equal(document.engineSetting, 'native');
  assert.deepEqual(Object.keys(document.engines), ['native']);
  assert.equal(document.audioDecode?.[0].maxSampleRate, 192000);
  assert.deepEqual(document.engines.native.subtitleRendering, ['text', 'ass', 'pgs', 'vobsub', 'dvb']);
});
test('missing or malformed native decoder facts never fabricate output capability', () => {
  assert.deepEqual(appleAudioDecodeFor(null), []);
  for (const row of [{...decoder, maxChannels: 0}, {...decoder, maxChannels: true}, {...decoder, maxSampleRate: 384000}, {...decoder, containers: ['mkv']}, {...decoder, sampleRates: [44100, NaN]}, {...decoder, codec: 'unknown'}, {...decoder, via: '\n'}]) assert.deepEqual(appleAudioDecodeFor({audioDecode: [row]}), []);
  assert.equal(appleAudioDecodeFor({audioDecode: [decoder, decoder]}).length, 1);
  assert.deepEqual(appleSubtitleRendering({subtitleRendering: ['ass', 'unknown']}), ['text']);
  assert.deepEqual(appleSubtitleRendering(null), ['text']);
});
