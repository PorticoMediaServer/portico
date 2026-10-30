/** The mpv engine's capability profile on Android, where the module reports each hardware
 * decoder's own limits and what this build of mpv does not do, and the corrections the capability
 * document makes to the Android platform player's profile. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {mpvDeviceClass, mpvDisplayRanges, mpvEngineCapabilities, softwareLimit, type EngineCapabilities, type MpvDeviceFacts} from '../src-next/platform/playback/mpv-profile.ts';
import {deviceCapabilityDocument} from '../src-next/platform/playback/capability-document.ts';

const GiB = 1024 ** 3;
const codec = (e: EngineCapabilities, name: string) => e.video.find(v => v.codec === name);
const android = {engine: 'mpv', available: true, dolbyVisionProfiles: [7, 8], softwareDeinterlace: false} as const;

/** A 4K streaming stick: little memory, a chip that decodes the modern codecs and MPEG-2, an HDR television behind it. */
const stick: MpvDeviceFacts = {
  ...android, memoryBytes: 2 * GiB, processorCount: 4, hdrOutput: true, hdrOutputRanges: ['hdr10', 'hlg'],
  hardwareDecode: {h264: true, hevc: true, vp9: true, av1: true},
  hardwareLimits: {
    h264: {maxWidth: 4096, maxHeight: 2160, maxFrameRate: 30, tenBit: false},
    hevc: {maxWidth: 4096, maxHeight: 2160, maxFrameRate: 60, tenBit: true},
    vp9: {maxWidth: 4096, maxHeight: 2160, maxFrameRate: 60, tenBit: true},
    av1: {maxWidth: 4096, maxHeight: 2160, maxFrameRate: 60, tenBit: true},
    mpeg2video: {maxWidth: 1920, maxHeight: 1088, maxFrameRate: 60, tenBit: false},
  },
  screen: {width: 3840, height: 2160, maxFrameRate: 60}, audio: {maxOutputChannels: 8},
};
/** An older phone: hardware H.264 and 8-bit HEVC only, a standard-range screen. */
const olderPhone: MpvDeviceFacts = {
  ...android, memoryBytes: 4 * GiB, processorCount: 8, hdrOutput: false, hdrOutputRanges: [],
  hardwareDecode: {h264: true, hevc: true, vp9: false, av1: false},
  hardwareLimits: {h264: {maxWidth: 3840, maxHeight: 2160, maxFrameRate: 30, tenBit: false}, hevc: {maxWidth: 1920, maxHeight: 1088, maxFrameRate: 60, tenBit: false}},
};

test('a hardware codec plays at its own decoder\'s limits, as the device reports them', () => {
  const e = mpvEngineCapabilities(stick);
  assert.equal(mpvDeviceClass(stick), 'light');
  assert.deepEqual([codec(e, 'hevc')!.maxWidth, codec(e, 'hevc')!.maxHeight, codec(e, 'hevc')!.maxFrameRate], [4096, 2160, 60]);
  assert.equal(codec(e, 'h264')!.maxFrameRate, 30, 'the chip\'s own H.264 rate at its largest picture');
  assert.deepEqual(codec(e, 'h264')!.bitDepths, [8], 'no headroom for 10-bit H.264 in software on this class');
  assert.equal(codec(e, 'av1')!.evidence, 'mixed', 'the decoder is probed; that it keeps up is not measured');
  assert.deepEqual([codec(e, 'mpeg2video')!.maxWidth, codec(e, 'mpeg2video')!.maxHeight], [1920, 1088], 'the chip\'s MPEG-2 decoder');
  assert.equal(codec(e, 'mpeg2video')!.evidence, 'mixed');
  assert.equal(codec(e, 'vc1')!.evidence, 'declared', 'software: an estimate');
  assert.equal(codec(e, 'vc1')!.maxFrameRate, 30);
});

test('what this build of mpv does not do on Android is not offered', () => {
  const e = mpvEngineCapabilities(stick);
  assert.deepEqual(codec(e, 'hevc')!.dolbyVisionProfiles, [7, 8], 'profile 5 would come out in the wrong colours');
  assert.ok(codec(e, 'hevc')!.dynamicRanges!.includes('dolby_vision'));
  assert.equal(codec(e, 'mpeg2video')!.interlaced, undefined, 'no deinterlacing filter');
  assert.equal(codec(e, 'vc1')!.interlaced, undefined);
  assert.deepEqual([...e.audioPassthrough], [], 'sound is decoded to PCM');
  assert.deepEqual([...e.subtitleRendering], ['text', 'ass', 'pgs', 'vobsub', 'dvb']);
  assert.equal(e.embeddedAudioSwitching, true);
});

test('a chip that decodes only 8-bit leaves 10-bit and HDR to software where software keeps up', () => {
  const standard = mpvEngineCapabilities(olderPhone);
  assert.equal(mpvDeviceClass(olderPhone), 'standard');
  assert.deepEqual(codec(standard, 'hevc')!.bitDepths, [8, 10]);
  assert.deepEqual(codec(standard, 'hevc')!.limits, [
    {bitDepths: [8], maxWidth: 1920, maxHeight: 1088, maxFrameRate: 60},
    {bitDepths: [10], maxWidth: 1920, maxHeight: 1080, maxFrameRate: 30},
  ], 'the chip keeps 8-bit at its limit; 10-bit is software\'s');
  assert.deepEqual(codec(standard, 'h264')!.bitDepths, [8, 10]);
  assert.equal(codec(standard, 'h264')!.limits!.find(l => l.bitDepths?.includes(10))!.maxHeight, 1080, '10-bit H.264 is software\'s');
  assert.equal(codec(standard, 'vp9')!.maxHeight, 1080, 'software VP9 on a standard device');
  assert.equal(codec(standard, 'vp9')!.maxFrameRate, 30);

  const light = mpvEngineCapabilities({...olderPhone, memoryBytes: 2 * GiB});
  assert.deepEqual(codec(light, 'hevc')!.bitDepths, [8]);
  assert.equal(codec(light, 'hevc')!.maxHeight, 1088, 'the chip\'s own limit, 8-bit only');
  assert.deepEqual(codec(light, 'hevc')!.dynamicRanges, ['sdr'], 'no 10-bit, so no HDR title');
  assert.equal(codec(light, 'hevc')!.dolbyVisionProfiles, undefined);
  assert.equal(codec(light, 'vp9'), undefined, 'left to the server');
});

test('an HDR screen shows the HDR kinds the module names; a standard-range one shows none', () => {
  assert.deepEqual([...mpvDisplayRanges(stick)], ['sdr', 'hdr10', 'hlg']);
  assert.deepEqual([...mpvDisplayRanges({...stick, hdrOutputRanges: ['hdr10']})], ['sdr', 'hdr10']);
  assert.deepEqual([...mpvDisplayRanges(olderPhone)], ['sdr']);
});

test('software limits are by device class', () => {
  assert.equal(softwareLimit('av1', 'light'), null);
  assert.equal(softwareLimit('av1', 'standard')!.maxHeight, 1080);
  assert.equal(softwareLimit('hevc', 'strong')!.maxWidth, 4096);
  assert.equal(softwareLimit('mpeg2video', 'light')!.maxFrameRate, 30);
  assert.equal(softwareLimit('h264', 'standard')!.maxFrameRate, 60);
});

/** What `PorticoCapabilities.facts()` answers on a television box with a receiver: a hardware HEVC
 * decoder, only a software AV1 decoder, AC-3 both decoded on the device and taken by the receiver. */
const boxFacts = {
  model: 'Box', osVersion: '14',
  decoders: [
    {codec: 'h264', hardware: true, maxWidth: 4096, maxHeight: 2160, maxFrameRate: 60, profiles: ['high'], maxLevel: 52, tenBit: false, hdr: []},
    {codec: 'hevc', hardware: true, maxWidth: 4096, maxHeight: 2160, maxFrameRate: 60, profiles: ['main', 'main_10'], maxLevel: 153, tenBit: true, hdr: ['hdr10', 'hlg']},
    {codec: 'av1', hardware: false, maxWidth: 4096, maxHeight: 4096, maxFrameRate: 120, profiles: ['main'], tenBit: true, hdr: ['hdr10']},
  ],
  audioDecoders: [{codec: 'aac', maxChannels: 8}, {codec: 'ac3', maxChannels: 6}, {codec: 'eac3', maxChannels: 8}],
  displayHdr: ['hdr10', 'hlg'], screen: {width: 3840, height: 2160, maxFrameRate: 60},
  passthrough: [{codec: 'ac3', maxChannels: 6}, {codec: 'dts', maxChannels: 6}],
  audio: {maxOutputChannels: 8, route: 'hdmi'},
} as const;
const box = {television: true, pad: false, family: 'androidtv', osVersion: '14'} as const;

test('the Android platform player declares only the subtitles it draws: plain text', () => {
  const d = deviceCapabilityDocument({setting: 'automatic', native: boxFacts as never, mpv: stick, mpvAvailable: true, appVersion: '0.1.0', device: box});
  assert.deepEqual([...d.engines.native.subtitleRendering!], ['text']);
  assert.equal(d.engines.native.embeddedAudioSwitching, false);
  assert.deepEqual([...d.engines.mpv!.subtitleRendering!], ['text', 'ass', 'pgs', 'vobsub', 'dvb']);
  assert.ok(JSON.stringify(d).length < 64 * 1024);
});

test('a codec Android decodes only in software is declared at what software keeps up with, or left out', () => {
  const light = deviceCapabilityDocument({setting: 'automatic', native: boxFacts as never, mpv: stick, mpvAvailable: true, appVersion: '0.1.0', device: box});
  assert.equal(light.engines.native.video!.find(v => v.codec === 'av1'), undefined, 'a 2 GB box does not keep up with software AV1');
  assert.ok(light.engines.native.transports!.every(t => !(t.video ?? []).includes('av1')));
  assert.equal(light.engines.native.video!.find(v => v.codec === 'hevc')!.maxWidth, 4096, 'the hardware decoder is untouched');

  const standard = deviceCapabilityDocument({setting: 'automatic', native: boxFacts as never, mpv: {...stick, memoryBytes: 4 * GiB}, mpvAvailable: true, appVersion: '0.1.0', device: box});
  const av1 = standard.engines.native.video!.find(v => v.codec === 'av1')!;
  assert.deepEqual([av1.maxWidth, av1.maxHeight, av1.maxFrameRate], [1920, 1080, 30]);
  assert.equal(av1.evidence, 'declared', 'an estimate, and marked as one');
});

test('a codec the receiver takes as a bitstream is still declared as decoded when the device decodes it', () => {
  const d = deviceCapabilityDocument({setting: 'automatic', native: boxFacts as never, mpv: stick, mpvAvailable: true, appVersion: '0.1.0', device: box});
  assert.ok(d.engines.native.audioPassthrough!.some(a => a.codec === 'ac3'));
  assert.ok(d.engines.native.audioPassthrough!.some(a => a.codec === 'dts'));
  assert.equal(d.engines.native.audio!.find(a => a.codec === 'ac3')?.maxChannels, 6, 'the device\'s own AC-3 decoder');
  assert.equal(d.engines.native.audio!.find(a => a.codec === 'dts'), undefined, 'no DTS decoder on the device: bitstream only');
  assert.ok(d.engines.native.audio!.every(a => !('passthrough' in a)));
});
