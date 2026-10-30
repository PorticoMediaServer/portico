import test from 'node:test';
import assert from 'node:assert/strict';
import {mpvEngineCapabilities, type MpvDeviceFacts} from '../src-next/platform/playback/mpv-profile.ts';

const stick: MpvDeviceFacts = {
  memoryBytes: 2 * 1024 ** 3, processorCount: 4,
  hardwareDecode: {h264: true, hevc: true, vp9: true, av1: true},
};
const hardware = {maxWidth: 3840, maxHeight: 2160, maxFrameRate: 30, tenBit: true,
  limits: [{maxWidth: 3840, maxHeight: 2160, maxFrameRate: 30}, {maxWidth: 1920, maxHeight: 1080, maxFrameRate: 60}]};

test('mpv declares DTS core, HRA and MA decoding without claiming DTS:X rendering or passthrough', () => {
  const profile = mpvEngineCapabilities(stick), dts = profile.audio.find(value => value.codec === 'dts');
  assert.ok(dts);
  assert.deepEqual(dts.variants, ['dts_core', 'dts_hd_hra', 'dts_hd_ma']);
  assert.equal(dts.maxChannels, 8);
  assert.equal(dts.objectAudio, undefined);
  assert.deepEqual(profile.audioPassthrough, []);
});

test('4K30 hardware keeps its independent 1080p60 operating point and a 60fps overall ceiling', () => {
  for (const codec of ['h264', 'hevc', 'vp9', 'av1']) {
    const video = mpvEngineCapabilities({...stick, hardwareLimits: {[codec]: hardware}}).video.find(value => value.codec === codec)!;
    assert.deepEqual([video.maxWidth, video.maxHeight, video.maxFrameRate], [3840, 2160, 60], codec);
    assert.deepEqual(video.limits?.map(({maxWidth, maxHeight, maxFrameRate}) => ({maxWidth, maxHeight, maxFrameRate})), hardware.limits, codec);
    assert.equal(video.limits?.some(point => point.maxWidth === 3840 && point.maxFrameRate === 60), false, 'the overall maxima never become a fictitious 4K60 operating point');
  }
});

test('a single valid operating point supplies the overall limit; malformed points fall back', () => {
  const single = mpvEngineCapabilities({...stick, hardwareLimits: {hevc: {...hardware, limits: [hardware.limits[1]!]}}}).video.find(value => value.codec === 'hevc')!;
  assert.deepEqual([single.maxWidth, single.maxHeight, single.maxFrameRate], [1920, 1080, 60]);
  assert.equal(single.limits, undefined, 'one point needs no additional restriction list');
  const invalid = mpvEngineCapabilities({...stick, hardwareLimits: {hevc: {...hardware, limits: [{maxWidth: 0, maxHeight: 1080, maxFrameRate: 60}, {maxWidth: 1920, maxHeight: 1080, maxFrameRate: Number.NaN}]}}}).video.find(value => value.codec === 'hevc')!;
  assert.deepEqual([invalid.maxWidth, invalid.maxHeight, invalid.maxFrameRate], [3840, 2160, 30]);
});

test('operating points stay inside reported geometry without inheriting its lower frame rate', () => {
  const video = mpvEngineCapabilities({...stick, hardwareLimits: {hevc: {...hardware, limits: [{maxWidth: 4096, maxHeight: 2304, maxFrameRate: 30}, {maxWidth: 1920, maxHeight: 1080, maxFrameRate: 120}]}}}).video.find(value => value.codec === 'hevc')!;
  assert.deepEqual(video.limits?.map(({maxWidth, maxHeight, maxFrameRate}) => ({maxWidth, maxHeight, maxFrameRate})), [
    {maxWidth: 3840, maxHeight: 2160, maxFrameRate: 30}, {maxWidth: 1920, maxHeight: 1080, maxFrameRate: 120},
  ]);
  assert.equal(video.maxFrameRate, 120);
});

test('overall limits cover the actual hardware and software points without expanding a software depth', () => {
  const profile = mpvEngineCapabilities({...stick, memoryBytes: 4 * 1024 ** 3,
    hardwareLimits: {h264: hardware, hevc: {...hardware, tenBit: false}}});
  for (const codec of ['h264', 'hevc']) {
    const video = profile.video.find(value => value.codec === codec)!;
    assert.equal(video.maxFrameRate, 60);
    assert.deepEqual(video.limits?.find(point => point.bitDepths?.includes(10)), {bitDepths: [10], maxWidth: 1920, maxHeight: 1080, maxFrameRate: 30});
    assert.deepEqual(video.limits?.filter(point => point.bitDepths?.includes(8)).map(({maxWidth, maxHeight, maxFrameRate}) => ({maxWidth, maxHeight, maxFrameRate})), hardware.limits);
  }
});
