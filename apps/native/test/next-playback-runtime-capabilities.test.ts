/** The mpv engine's capability profile, and the capability document that carries it beside the
 * platform player's. The checks at the end are the server's own admission rules
 * (`server/internal/playback/capability_document.go`), so a document that passes here is one the
 * server accepts. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {mpvDeviceClass, mpvDisplayRanges, mpvEngineCapabilities, type EngineCapabilities, type MpvDeviceFacts} from '../src-next/platform/playback/mpv-profile.ts';
import {deviceCapabilityDocument, probedDevice} from '../src-next/platform/playback/capability-document.ts';
import {capabilityDocumentProblem} from '../../../packages/client-core/src/client-profile.ts';

const GiB = 1024 ** 3;
const appleTV4K: MpvDeviceFacts = {available: true, memoryBytes: 4 * GiB, processorCount: 6, hardwareDecode: {h264: true, hevc: true, vp9: false, av1: false}, hdrOutput: false};
const appleTVHD: MpvDeviceFacts = {available: true, memoryBytes: 2 * GiB, processorCount: 2, hardwareDecode: {h264: true, hevc: false, vp9: false, av1: false}, hdrOutput: false};
const iPhonePro: MpvDeviceFacts = {available: true, memoryBytes: 8 * GiB, processorCount: 6, hardwareDecode: {h264: true, hevc: true, vp9: true, av1: true}, hdrOutput: true};
const codec = (e: EngineCapabilities, name: string) => e.video.find(v => v.codec === name);

test('the device class comes from memory and processor count', () => {
  assert.equal(mpvDeviceClass(appleTVHD), 'light');
  assert.equal(mpvDeviceClass(appleTV4K), 'standard');
  assert.equal(mpvDeviceClass(iPhonePro), 'strong');
  assert.equal(mpvDeviceClass({memoryBytes: 8 * GiB, processorCount: 4}), 'standard', 'much memory but few cores');
  assert.equal(mpvDeviceClass(null), 'light');
});

test('mpv opens what the platform player refuses, draws every subtitle kind and tone-maps HDR itself', () => {
  const e = mpvEngineCapabilities(appleTV4K);
  assert.equal(e.name, 'mpv');
  const direct = e.transports.find(t => t.transport === 'direct')!;
  for (const container of ['mkv', 'webm', 'avi', 'ts', 'm2ts', 'mp4']) assert.ok(direct.containers!.includes(container), container);
  assert.deepEqual([...e.subtitleRendering], ['text', 'ass', 'pgs', 'vobsub', 'dvb']);
  assert.equal(e.toneMapsHdr, true);
  assert.equal(e.embeddedAudioSwitching, true);
  assert.deepEqual([...e.audioPassthrough], [], 'nothing is passed through as a bitstream');
  for (const name of ['dts', 'truehd', 'flac', 'eac3', 'opus']) assert.ok(e.audio.some(a => a.codec === name), name);
  assert.equal(e.audio.find(a => a.codec === 'truehd')!.maxChannels, 8);
  assert.ok(e.audio.every(a => !a.objectAudio), 'object audio plays as its channel bed, so none is claimed');
  assert.deepEqual(codec(e, 'hevc')!.dolbyVisionProfiles, [5, 7, 8]);
  assert.ok(codec(e, 'hevc')!.dynamicRanges!.includes('hdr10plus'));
  assert.equal(codec(e, 'mpeg2video')!.interlaced, true);
  assert.ok(e.video.every(v => direct.video!.includes(v.codec)));
});

test('hardware codecs play at the chip\'s limits; the rest within what software keeps up with on that class of device', () => {
  const standard = mpvEngineCapabilities(appleTV4K);
  assert.equal(codec(standard, 'hevc')!.maxWidth, 4096);
  assert.deepEqual(codec(standard, 'h264')!.bitDepths, [8, 10], '10-bit H.264 in software');
  assert.equal(codec(standard, 'h264')!.maxWidth, 4096, '8-bit H.264 keeps the chip\'s limit');
  assert.deepEqual(codec(standard, 'h264')!.limits, [
    {bitDepths: [8], maxWidth: 4096, maxHeight: 2304, maxFrameRate: 60},
    {bitDepths: [10], maxWidth: 1920, maxHeight: 1080, maxFrameRate: 30},
  ], 'software Hi10P has its own operating point');
  assert.equal(codec(standard, 'av1')!.maxHeight, 1080, 'software AV1 stops at 1080p here');
  assert.equal(codec(standard, 'av1')!.maxFrameRate, 30);
  assert.equal(codec(standard, 'vp9')!.maxHeight, 1080);

  const light = mpvEngineCapabilities(appleTVHD);
  assert.deepEqual(codec(light, 'h264')!.bitDepths, [8]);
  assert.equal(codec(light, 'h264')!.maxWidth, 1920, 'Apple TV HD does not have a 4K H.264 decoder');
  assert.equal(codec(light, 'hevc'), undefined, 'no hardware HEVC and no headroom for software: left to the server');
  assert.equal(codec(light, 'av1'), undefined);
  assert.equal(codec(light, 'mpeg2video')!.maxFrameRate, 30);
  assert.ok(!light.transports.find(t => t.transport === 'hls_fmp4')!.video!.includes('av1'));

  const strong = mpvEngineCapabilities(iPhonePro);
  assert.equal(codec(strong, 'av1')!.maxWidth, 4096);
  assert.equal(codec(strong, 'av1')!.maxFrameRate, 60, 'hardware AV1');
  assert.equal(codec(strong, 'vp9')!.maxFrameRate, 60);

  const unknown = mpvEngineCapabilities(null);
  assert.equal(unknown.evidence, 'declared');
  assert.equal(codec(unknown, 'hevc'), undefined, 'HEVC hardware is not assumed when a probe failed');
  assert.equal(codec(unknown, 'h264')!.maxWidth, 1920);
});

test('what the screen shows from mpv: HDR on an HDR phone or tablet, standard range on a television', () => {
  assert.deepEqual([...mpvDisplayRanges(iPhonePro)], ['sdr', 'hdr10', 'hlg']);
  assert.deepEqual([...mpvDisplayRanges(appleTV4K)], ['sdr']);
  assert.deepEqual([...mpvDisplayRanges(null)], ['sdr']);
});

// The server's admission rules are the shared package's own check (`capabilityDocumentProblem`).
const documentProblem = capabilityDocumentProblem;

const nativeFacts = {model: 'AppleTV14,1', osVersion: '26.1', hardwareDecode: {hevc: true, av1: false, dolbyVision: true}, hdrModes: {hdr10: true, hlg: true, dolbyVision: true}, eligibleForHDRPlayback: true, screen: {width: 3840, height: 2160, maxFrameRate: 60}, audio: {maxOutputChannels: 8, route: 'hdmi', spatial: false}};
const tv = {television: true, pad: false, osVersion: '26.1'};

test('the capability document carries one profile per engine, the setting, the screen and the output', () => {
  const d = deviceCapabilityDocument({setting: 'automatic', native: nativeFacts, mpv: appleTV4K, mpvAvailable: true, appVersion: '0.1.0', device: tv});
  assert.equal(documentProblem(d), null);
  assert.equal(d.version, 2);
  assert.equal(d.engineSetting, 'automatic');
  assert.equal(d.client.form, 'tv');
  assert.equal(d.client.family, 'apple');
  assert.equal(d.engines.native.name, 'avplayer');
  assert.equal(d.engines.mpv?.name, 'mpv');
  assert.deepEqual([...d.engines.native.subtitleRendering!], ['text']);
  assert.equal(d.engines.native.embeddedAudioSwitching, false);
  assert.equal(d.engines.native.toneMapsHdr, true, 'AVPlayer with hardware HEVC tone-maps for a standard-range screen');
  assert.deepEqual(d.engines.native.video!.find(v => v.codec === 'hevc')!.codecTags, ['hvc1', 'dvh1']);
  assert.ok(!d.engines.native.transports!.find(t => t.transport === 'direct')!.containers!.includes('mkv'));
  assert.ok(d.engines.mpv!.transports!.find(t => t.transport === 'direct')!.containers!.includes('mkv'));
  assert.deepEqual(d.display, {width: 3840, height: 2160, dynamicRanges: ['sdr', 'hdr10', 'hlg', 'dolby_vision'], maxFrameRate: 60});
  assert.deepEqual(d.audioOutput, {route: 'hdmi', maxChannels: 8, spatial: false});
  assert.ok(d.audioDecode!.some(a => a.codec === 'flac'));
  assert.deepEqual(d.overrides, {forceStereo: false, disablePassthrough: false, disableHdr: false});
  assert.deepEqual(probedDevice(d), {audioPassthrough: false, hdr: true});
});

test('the viewer\'s overrides travel in the document, and a setting the device cannot honour reads as Automatic', () => {
  const d = deviceCapabilityDocument({setting: 'mpv', overrides: {forceStereo: true, disableHdr: true}, native: nativeFacts, mpv: appleTV4K, mpvAvailable: true, appVersion: '0.1.0', device: tv});
  assert.equal(documentProblem(d), null);
  assert.equal(d.engineSetting, 'mpv');
  assert.deepEqual(d.overrides, {forceStereo: true, disablePassthrough: false, disableHdr: true});
  const without = deviceCapabilityDocument({setting: 'mpv', native: nativeFacts, mpv: null, mpvAvailable: false, appVersion: '0.1.0', device: tv});
  assert.equal(without.engineSetting, 'automatic', 'there is no second player to choose');
  assert.equal(without.engines.mpv, undefined);
});

test('a build without mpv publishes the native profile only, and its setting reads as Automatic', () => {
  const d = deviceCapabilityDocument({setting: 'mpv', native: null, mpv: null, mpvAvailable: false, appVersion: '0.1.0', device: {television: false, pad: true}});
  assert.equal(documentProblem(d), null);
  assert.equal(d.engines.mpv, undefined);
  assert.equal(d.engineSetting, 'automatic');
  assert.equal(d.client.form, 'tablet');
  assert.ok(!d.engines.native.toneMapsHdr, 'nothing probed: nothing claimed');
});

test('an Android device describes its own player and, with mpv, the same second profile', () => {
  const d = deviceCapabilityDocument({setting: 'native', native: null, mpv: null, mpvAvailable: true, appVersion: '0.1.0', device: {television: true, pad: false, family: 'firetv'}});
  assert.equal(documentProblem(d), null);
  assert.equal(d.client.family, 'firetv');
  assert.equal(d.engines.native.name, 'exoplayer');
  assert.deepEqual([...d.engines.native.subtitleRendering!], ['text'], 'the app draws plain text only, whatever ExoPlayer could draw');
  assert.equal(d.engineSetting, 'native');
  assert.equal(d.engines.mpv?.name, 'mpv');
});
