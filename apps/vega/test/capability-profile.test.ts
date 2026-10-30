/**
 * The Vega app's capability document: one player engine (the platform's media element), plain
 * text subtitles only, no HDR claimed, and files from their address only over HTTPS. What the
 * device itself answers replaces the declared table's codecs and transports.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {capabilityDocumentProblem, probeWebProfile, type CapabilityDocument} from '../../../packages/client-core/src/client-profile.ts';
import {probedSettings, vegaCapabilityDocument} from '../src/bridge/capability-profile.ts';

const facts = {model: 'AFTKRT', osVersion: '1.2', deviceName: 'Living Room', appVersion: '0.1.0'};

test('with nothing asked of the device, the declared table is the document', () => {
  const document = vegaCapabilityDocument(facts, 'https://media.example.test');
  assert.equal(capabilityDocumentProblem(document), null);
  assert.equal(document.client.family, 'vega');
  assert.equal(document.client.form, 'tv');
  assert.equal(document.client.appVersion, '0.1.0');
  assert.equal(document.client.model, 'AFTKRT');
  assert.equal(document.engineSetting, 'automatic');
  assert.deepEqual(Object.keys(document.engines), ['native']);
  assert.deepEqual(document.engines.native.video?.map(v => v.codec), ['h264', 'hevc']);
});

test('missing or unknown system identity is omitted without inventing a model', () => {
  for (const model of [undefined, '', '   ', 'unknown', ' UNKNOWN ', 'x'.repeat(65)]) {
    const document = vegaCapabilityDocument({model});
    assert.equal(capabilityDocumentProblem(document), null);
    assert.equal(document.client.model, undefined);
  }
  assert.equal(vegaCapabilityDocument({model: ' AFTKRT '}).client.model, 'AFTKRT');
});

test('a bounded SDK model describes identity without changing decoder or output facts', () => {
  const unnamed = vegaCapabilityDocument({});
  const named = vegaCapabilityDocument({model: `  ${'x'.repeat(64)}  `});
  assert.equal(capabilityDocumentProblem(named), null);
  assert.equal(named.client.model, 'x'.repeat(64));
  assert.deepEqual(named.engines, unnamed.engines);
  assert.deepEqual(named.display, unnamed.display);
  assert.deepEqual(named.audioOutput, unnamed.audioOutput);
});

test('one engine, text subtitles only, no track switching inside a file, no HDR', () => {
  for (const origin of ['https://media.example.test', 'http://192.168.1.20:32500', null]) {
    const native = vegaCapabilityDocument(facts, origin).engines.native;
    // Styled and image subtitles are not listed, so the server burns them in for this device.
    assert.deepEqual(native.subtitleRendering, ['text']);
    assert.equal(native.embeddedAudioSwitching, false);
    assert.equal(native.toneMapsHdr, false);
    assert.ok(native.video?.every(v => (v.dynamicRanges ?? ['sdr']).every(r => r === 'sdr')));
  }
  const document = vegaCapabilityDocument(facts, 'https://media.example.test');
  assert.deepEqual(document.display?.dynamicRanges, ['sdr']);
  assert.equal(document.audioOutput?.maxChannels, 2);
});

test('all server media uses the reader that can inspect final response addresses', () => {
  const kinds = (origin: string | null) => vegaCapabilityDocument(facts, origin).engines.native.transports?.map(t => t.transport);
  assert.ok(!kinds('https://media.example.test')?.includes('direct'));
  assert.deepEqual(kinds('http://192.168.1.20:32500'), ['hls_ts', 'hls_fmp4']);
  assert.deepEqual(kinds(null), ['hls_ts', 'hls_fmp4']);
});

test('the viewer\'s overrides are stamped on, and the Player setting stays Automatic', () => {
  const document = vegaCapabilityDocument(facts, null, {forceStereo: true});
  assert.deepEqual(document.overrides, {forceStereo: true, disablePassthrough: false, disableHdr: false});
  assert.equal(document.engineSetting, 'automatic');
});

/** A device that answers: `types` are what its media source takes. */
async function asked(types: readonly string[], smooth4k: readonly string[] = []): Promise<CapabilityDocument> {
  const takes = (type: string) => types.some(t => type.includes(t));
  return probeWebProfile({
    canPlayType: type => (type === 'application/vnd.apple.mpegurl' ? '' : takes(type) ? 'probably' : ''),
    mediaSourceSupports: takes,
    decodingInfo: async question => { const ok = question.height <= 1080 ? takes(question.contentType) : smooth4k.some(t => question.contentType.includes(t)); return {supported: ok, smooth: ok, powerEfficient: ok}; },
    identity: {engine: 'w3cmedia'},
  });
}

test('what the device answers replaces the table: more codecs where it has them', async () => {
  const probed = await asked(['avc1', 'hvc1', 'av01', 'mp4a.40.2', 'ac-3', 'ec-3', 'opus', 'flac', 'fLaC'], ['hvc1', 'av01']);
  const document = vegaCapabilityDocument(facts, 'http://192.168.1.20:32500', {}, probed);
  assert.equal(capabilityDocumentProblem(document), null);
  const native = document.engines.native;
  assert.equal(native.evidence, 'probed');
  assert.equal(native.name, 'w3cmedia');
  assert.deepEqual(native.video?.map(v => v.codec), ['h264', 'hevc', 'av1']);
  // Canonical probeWebProfile tries 7680x4320 before 3840x2160 and grants it only for a smooth
  // answer; this mock answers smooth for hvc1, so the truthful ceiling is now 4320, not 2160.
  assert.equal(native.video?.find(v => v.codec === 'hevc')?.maxHeight, 4320);
  assert.equal(native.video?.find(v => v.codec === 'h264')?.maxHeight, 1080);
  // SDK output answers describe playable audio, without inventing a passthrough route.
  assert.deepEqual(native.audio?.map(a => a.codec), ['aac', 'mp3', 'ac3', 'eac3', 'flac', 'opus']);
  assert.deepEqual(native.audioPassthrough ?? [], []);
  const fmp4 = native.transports?.find(t => t.transport === 'hls_fmp4');
  assert.deepEqual(fmp4?.video, ['h264', 'hevc', 'av1']);
  assert.ok(fmp4?.audio?.includes('eac3') && fmp4.audio.includes('flac'));
  // Plain HTTP: nothing from its address. Still text only, still no HDR, still the declared identity.
  assert.ok(!native.transports?.some(t => t.transport === 'direct'));
  assert.deepEqual(native.subtitleRendering, ['text']);
  assert.ok(native.video?.every(v => v.dynamicRanges?.join() === 'sdr'));
  assert.equal(document.client.family, 'vega');
  assert.deepEqual(document.display?.dynamicRanges, ['sdr']);
});

test('what the device answers replaces the table: fewer where it has less', async () => {
  const probed = await asked(['avc1', 'mp4a.40.2']);
  const native = vegaCapabilityDocument(facts, 'https://media.example.test', {}, probed).engines.native;
  assert.deepEqual(native.video?.map(v => v.codec), ['h264']);
  assert.deepEqual(native.transports?.find(t => t.transport === 'hls_fmp4')?.video, ['h264']);
  assert.ok(!native.transports?.some(t => t.transport === 'direct'));
});

test('an answer the server would refuse is left out, and the table stands', () => {
  const broken = {...vegaCapabilityDocument(facts, null), engines: {native: {evidence: 'probed' as const, video: [{codec: 'NOT A CODEC'}], audio: [], transports: []}}} as CapabilityDocument;
  const document = vegaCapabilityDocument(facts, null, {}, broken);
  assert.equal(capabilityDocumentProblem(document), null);
  assert.deepEqual(document.engines.native.video?.map(v => v.codec), ['h264', 'hevc']);
  assert.deepEqual(document.engines.native.subtitleRendering, ['text']);
});

test('Settings offers Turn off HDR and Don\'t pass sound through only where they do something', () => {
  const document = vegaCapabilityDocument(facts, null);
  // Nothing HDR is claimed, and no receiver is reported: neither switch is offered.
  assert.deepEqual(probedSettings(document), {audioPassthrough: false, hdr: false});
  assert.deepEqual(probedSettings({...document, audioOutput: {route: 'hdmi', maxChannels: 6}}), {audioPassthrough: true, hdr: false});
  assert.deepEqual(probedSettings({...document, display: {dynamicRanges: ['sdr', 'hdr10']}}), {audioPassthrough: false, hdr: true});
});

test('the viewer says the TV shows HDR and plays surround: claimed then, never before', () => {
  const plain = vegaCapabilityDocument({model: 'AFTKRT'});
  assert.deepEqual(plain.display?.dynamicRanges, ['sdr']);
  assert.equal(plain.audioOutput?.maxChannels, 2);
  const hdr = vegaCapabilityDocument({model: 'AFTKRT'}, null, {}, null, {hdr: true, surround: false});
  assert.deepEqual(hdr.display?.dynamicRanges, ['sdr', 'hdr10', 'hlg']);
  assert.deepEqual(hdr.engines.native.video?.find(codec => codec.codec === 'hevc')?.dynamicRanges, ['sdr', 'hdr10', 'hlg']);
  assert.equal(hdr.audioOutput?.maxChannels, 2);
  const surround = vegaCapabilityDocument({model: 'AFTKRT'}, null, {}, null, {hdr: false, surround: true});
  assert.equal(surround.audioOutput?.maxChannels, 8);
  assert.equal(surround.engines.native.audio?.find(codec => codec.codec === 'eac3')?.maxChannels, 8);
  assert.equal(surround.engines.native.audio?.find(codec => codec.codec === 'aac')?.maxChannels, 6);
  assert.deepEqual(surround.display?.dynamicRanges, ['sdr']);
});
