import test from 'node:test';
import assert from 'node:assert/strict';
import {parseObservedPlayback} from '../src/playback-v1/observed-playback.ts';
import {ObservationReporter, type ObservationSample} from '../src/playback-v1/observation.ts';

const measured = {video: {width: 1920, height: 1080, frameRateHz: 23.976}, audio: {channels: 2, sampleRate: 44100}};
test('output measurements stay sparse, immutable and free of bridge URLs and capabilities', () => {
  const bridge = {...measured, url: 'https://private.invalid/token', screenModes: ['hdr10'], audio: {...measured.audio, receiver: 'private-name'}};
  const output = parseObservedPlayback(bridge)!;
  assert.deepEqual(output, measured);
  assert.equal(output.screen, undefined); assert.equal(output.audio?.handling, undefined);
  assert.equal(output.audio?.codec, undefined); assert.equal(output.audio?.bitDepth, undefined);
  bridge.audio.sampleRate = 48000;
  assert.equal(output.audio?.sampleRate, 44100);
  assert.ok(Object.isFrozen(output)); assert.ok(Object.isFrozen(output.audio));
  assert.equal(parseObservedPlayback(undefined), undefined); assert.deepEqual(parseObservedPlayback({}), {});
  assert.deepEqual(parseObservedPlayback({video: {width: 0, height: 0}, audio: {sampleRate: 0}}), {video: {}, audio: {}});
});
test('output values cannot be quality ceilings, guessed handling or unbounded/private strings', () => {
  for (const invalid of [null, [], {video: {height: -1}}, {video: {frameRateHz: NaN}}, {screen: {refreshRateHz: Infinity}}, {video: {width: 32769}}, {audio: {channels: 1.5}}, {audio: {channels: 65}}, {audio: {sampleRate: 1536001}}, {audio: {codec: 'https://private.invalid'}}, {audio: {handling: 'probably_passthrough'}}, {audio: {objectAudio: 'eac3_implies_atmos'}}, {audio: {bitDepth: 64}}, {screen: {dynamicRange: 'supported_hdr10'}}, {subtitles: {}}, {deliveredBitrateKbps: 1000001}]) {
    assert.throws(() => parseObservedPlayback(invalid), /Invalid playback observation/);
  }
});
test('observations carry only the current generation output and explicit clears, without inheriting a prior reading', async () => {
  const sent: ObservationSample[] = [];
  const reporter = new ObservationReporter({claim: async () => ({epoch: 'e'.repeat(22), everyMs: 1000}), send: async sample => {sent.push(sample); return {acceptedSeq: sample.seq};}, setTimer: () => 1, clearTimer: () => {}});
  reporter.setGeneration(1);
  const reading = {generation: 1, state: 'playing' as const, positionMs: 1000, rate: 1};
  reporter.observe({...reading, observedPlayback: measured}); await reporter.flush();
  assert.deepEqual(sent.at(-1)?.observedPlayback, measured);
  reporter.observe({...reading, observedPlayback: {}}); await reporter.flush();
  assert.deepEqual(sent.at(-1)?.observedPlayback, {});
  reporter.observe(reading); await reporter.flush();
  assert.equal('observedPlayback' in sent.at(-1)!, false);
  reporter.observe({...reading, positionMs: 2000, observedPlayback: {audio: {sampleRate: NaN}}}); await reporter.flush();
  assert.equal(sent.at(-1)?.positionMs, 2000); assert.deepEqual(sent.at(-1)?.observedPlayback, {});
  reporter.setGeneration(2);
  const count = sent.length;
  reporter.observe({...reading, observedPlayback: measured}); await reporter.flush();
  assert.equal(sent.length, count);
  reporter.observe({...reading, generation: 2}); await reporter.flush();
  assert.equal(sent.at(-1)?.generation, 2); assert.equal('observedPlayback' in sent.at(-1)!, false);
  reporter.stop();
});
