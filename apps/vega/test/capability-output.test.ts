import test from 'node:test';
import assert from 'node:assert/strict';
import {capabilityDocumentProblem} from '../../../packages/client-core/src/client-profile.ts';
import {probeVegaOutputs} from '../src/bridge/capability-output.ts';
import {vegaCapabilityDocument} from '../src/bridge/capability-profile.ts';

const base = () => ({...vegaCapabilityDocument(null), engines: {native: {name: 'w3cmedia', evidence: 'probed' as const,
  video: [{codec: 'hevc', bitDepths: [8,10], dynamicRanges: ['sdr' as const], maxWidth: 3840, maxHeight: 2160}],
  audio: [{codec: 'aac', maxChannels: 6}, {codec: 'flac', maxChannels: 8}, {codec: 'eac3', objectAudio: ['atmos' as const], maxChannels: 8}],
  transports: [{transport: 'hls_fmp4' as const, containers: ['fmp4'], video: ['hevc'], audio: ['aac','flac','eac3']}],
}}});

test('SDK screen/AVR answers reach the capability publisher input without SDR/stereo stripping', async () => {
  const calls: unknown[] = [];
  const result = await probeVegaOutputs(base(), async q => {
    calls.push(q);
    return {supported: q.video ? q.video.transferFunction === 'pq' || q.video.contentType.includes('dvh1.05') : q.audio?.channels === '6' && q.audio.samplerate <= 96000};
  });
  const document = vegaCapabilityDocument({model:'AFTKRT'}, null, {}, result);
  assert.equal(capabilityDocumentProblem(document), null);
  assert.deepEqual(document.display?.dynamicRanges, ['sdr','hdr10','dolby_vision']);
  assert.deepEqual(document.engines.native.video?.[0].dolbyVisionProfiles, [5]);
  assert.equal(document.audioOutput?.maxChannels, 6);
  assert.equal(document.engines.native.audio?.find(a => a.codec === 'flac')?.maxSampleRate, 96000);
  assert.equal(document.audioOutput?.route, undefined);
  assert.ok(document.engines.native.audio?.every(a => !a.objectAudio?.length));
  assert.deepEqual(document.engines.native.audioPassthrough, []);
  assert.ok(calls.length > 10);
});

test('negative or throwing SDK answers never manufacture HDR, DV, objects or surround', async () => {
  for (const answer of [async () => ({supported:false}), async () => { throw new Error('SDK unavailable'); }]) {
    const document = await probeVegaOutputs(base(), answer);
    assert.equal(capabilityDocumentProblem(document), null);
    assert.deepEqual(document.display?.dynamicRanges, ['sdr']);
    assert.deepEqual(document.engines.native.video?.[0].dolbyVisionProfiles, []);
    assert.equal(document.audioOutput?.maxChannels, 2);
    assert.ok(document.engines.native.audio?.every(a => !a.objectAudio?.length && a.maxSampleRate === undefined));
  }
});

test('a stereo high-rate answer cannot raise the surround rate ceiling', async () => {
  const document = await probeVegaOutputs(base(), async q => ({supported: q.video ? false : q.audio?.channels === '2' || q.audio?.samplerate === 48000}));
  assert.equal(document.audioOutput?.maxChannels, 8);
  assert.ok(document.engines.native.audio?.every(a => a.maxSampleRate === 48000));
});
