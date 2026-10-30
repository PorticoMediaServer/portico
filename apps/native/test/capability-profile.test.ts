/** Spec §3 and §18.1: what the app's own audio engine decodes, as the capability document says
 * it, and the server's audio plan mode from it. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {androidAudioDecodeFor, appleAudioDecode} from '../src-next/platform/playback/audio-decode.ts';
import {deviceCapabilityDocument} from '../src-next/platform/playback/capability-document.ts';
import {capabilityDocumentProblem} from '../../../packages/client-core/src/client-profile.ts';
import {CapabilityPublisher} from '../../../packages/client-core/src/playback-v1/options.ts';
import {SessionsClient} from '../../../packages/client-core/src/playback-v1/sessions.ts';
import {FakePlaybackServer, fakeHttp, type FakeAudioSource} from '../../../packages/client-core/src/playback-v1/testing/fake-server.ts';
import type {V1Http} from '../../../packages/client-core/src/playback-v1/http.ts';

const phone = {television: false, pad: false, osVersion: '26.4'};
const document = (device: Parameters<typeof deviceCapabilityDocument>[0]['device'] = phone) => deviceCapabilityDocument({setting: 'automatic', native: null, mpv: null, mpvAvailable: false, appVersion: '1.0', device});

test('the document says what kind of device this is, what its player plays and what the audio engine decodes', () => {
  const d = document();
  assert.equal(capabilityDocumentProblem(d), null);
  assert.equal(d.client.form, 'phone');
  assert.equal(d.client.appVersion, '1.0');
  assert.deepEqual(d.audioDecode, appleAudioDecode.map(a => ({...a})));
  const native = d.engines.native;
  assert.ok(native.transports!.some(t => t.transport === 'direct' && t.containers!.includes('mp4')));
  assert.ok(native.transports!.some(t => t.transport === 'hls_fmp4') && native.transports!.some(t => t.transport === 'hls_ts'));
  assert.ok(native.video!.some(v => v.codec === 'hevc'));
  assert.ok(native.audio!.some(a => a.codec === 'eac3'), 'the declared EAC3 decoder remains available');
  assert.ok(native.audio!.every(a => !a.objectAudio?.length), 'without native eligibility and route observations, no object-audio grant');
  assert.equal(document({...phone, television: true}).client.form, 'tv');
  assert.equal(document({...phone, pad: true}).client.form, 'tablet');
  assert.equal(document({television: true, pad: false, family: 'firetv'}).client.family, 'firetv');
});

test('Atmos requires both observed EAC3-JOC eligibility and an observed spatial output route', () => {
  type AudioFacts = NonNullable<import('../../../packages/client-core/src/client-profile.ts').AppleNativeFacts['audio']>;
  const eac3For = (audio: AudioFacts) => {
    const d = deviceCapabilityDocument({setting: 'automatic', native: {audio}, mpv: null, mpvAvailable: false, appVersion: '1.0', device: phone});
    assert.equal(capabilityDocumentProblem(d), null);
    return d.engines.native.audio!.find(a => a.codec === 'eac3');
  };
  assert.deepEqual(eac3For({eac3JocEligible: true, spatial: true, route: 'HDMI'})?.objectAudio, ['atmos']);
  for (const audio of [
    {spatial: true, route: 'HDMI'},
    {eac3JocEligible: false, spatial: true, route: 'HDMI'},
    {eac3JocEligible: true, spatial: false, route: 'HDMI'},
    {eac3JocEligible: true, spatial: true},
  ]) assert.equal(eac3For(audio)?.objectAudio, undefined, 'partial/negative observations must not grant Atmos');
});

test('audioDecode declares only the fixture-proven pairs; Ogg Opus is left to the server on Apple', () => {
  const pairs = appleAudioDecode.flatMap(d => d.containers.map(c => `${c}/${d.codec}`)).sort();
  assert.deepEqual(pairs, ['adts/aac', 'flac/flac', 'mp3/mp3', 'mp4/aac', 'mp4/alac', 'wav/pcm']);
  assert.ok(appleAudioDecode.every(d => d.maxChannels === 2));
  assert.deepEqual(androidAudioDecodeFor({audioDecoders: [{codec: 'flac'}, {codec: 'opus'}]}).map(d => d.codec).sort(), ['flac', 'opus', 'pcm'], 'Android: only what the device has a decoder for (PCM needs none)');
});

test('Android audioDecode maps validated per-codec decoder counts and keeps conservative fallbacks', () => {
  const rows = (counts: readonly (number | undefined)[]) => androidAudioDecodeFor({audioDecoders: counts.map((maxChannels, index) => ({codec: ['flac', 'opus', 'mp3', 'pcm'][index]!, ...(maxChannels === undefined ? {} : {maxChannels})}))});
  const observed = rows([6, 1, 0, 8]);
  const by = new Map(observed.map(row => [row.codec, row]));
  assert.equal(by.get('flac')?.maxChannels, 6, '6-channel FLAC keeps the observed count');
  assert.equal(by.get('opus')?.maxChannels, 1, 'mono Opus is isolated to its own codec');
  assert.equal(by.get('mp3')?.maxChannels, 2, 'zero is invalid and keeps the conservative default');
  assert.equal(by.get('pcm')?.maxChannels, 2, 'PCM stays static and needs no MediaCodec');
  assert.deepEqual([...by.keys()].sort(), ['flac', 'mp3', 'opus', 'pcm']);
  for (const count of [undefined, 0, 3.5, 17, NaN, Infinity]) assert.equal(rows([count, 6, 2, 2]).find(row => row.codec === 'flac')?.maxChannels, 2, `invalid ${String(count)} does not widen FLAC`);
  assert.equal(androidAudioDecodeFor({audioDecoders: [{codec: 'flac', maxChannels: 4}, {codec: 'flac', maxChannels: 6}]}).find(row => row.codec === 'flac')?.maxChannels, 6, 'duplicates aggregate only validated same-codec counts');
  assert.deepEqual(androidAudioDecodeFor({audioDecoders: []}).map(row => row.codec), ['pcm'], 'empty decoders leave PCM only');
  assert.ok(androidAudioDecodeFor(null).every(row => row.maxChannels === 2), 'null facts keep the baseline');
  const baselineFlac = androidAudioDecodeFor(null).find(row => row.codec === 'flac')!, observedFlac = by.get('flac')!;
  assert.deepEqual(observedFlac.containers, baselineFlac.containers);
  assert.equal(observedFlac.maxSampleRate, baselineFlac.maxSampleRate);
  assert.equal(observedFlac.maxBitDepth, baselineFlac.maxBitDepth);
  assert.equal(observedFlac.via, baselineFlac.via);
  assert.ok(appleAudioDecode.every(row => row.maxChannels === 2), 'Apple table unchanged');
});

const source = (container: string, codec: string, sampleRate = 44100): FakeAudioSource => ({bytes: new Uint8Array(1024), container, codec, sampleRate, channels: 2, durationFrames: 441000, trim: {startFrames: 0, endFrames: 0, source: 'none'}});
const sources: Record<string, FakeAudioSource> = {
  'song-mp3': source('mp3', 'mp3'),
  'song-opus': source('ogg', 'opus', 48000),
  'song-flac-192': source('flac', 'flac', 192000),
};

let starts = 0;
async function planFor(server: FakePlaybackServer, itemId: string) {
  const sessions = new SessionsClient(fakeHttp(server) as unknown as V1Http);
  const s = await sessions.start({itemId}, {state: 'playing'}, `key-${itemId}-${++starts}-0000000000`);
  return s.presentation.audio;
}

test('with the document published, a declared pair plays direct and the rest is converted to FLAC; without it, unavailable', async () => {
  const server = new FakePlaybackServer({modeFromCapabilities: true, audioSource: id => sources[id]});
  assert.equal((await planFor(server, 'song-mp3'))?.mode, 'unavailable', 'no profile: nothing declared');
  const publisher = new CapabilityPublisher(fakeHttp(server) as unknown as V1Http);
  assert.equal(await publisher.publish(document()), 'accepted');
  assert.equal(await publisher.publish(document()), 'current', 'an unchanged document is not sent again');
  const mp3 = await planFor(server, 'song-mp3');
  assert.equal(mp3?.mode, 'direct');
  const opus = await planFor(server, 'song-opus');
  assert.equal(opus?.mode, 'converted');
  assert.equal(opus?.container, 'flac');
  assert.match(opus?.reason ?? '', /FLAC/);
  assert.equal((await planFor(server, 'song-flac-192'))?.mode, 'converted', 'past the declared sample rate');
});
