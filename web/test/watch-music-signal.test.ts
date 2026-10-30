import test from 'node:test';
import assert from 'node:assert/strict';
import {watchMusicSignal} from '../src/screens/watch/music/signal.ts';
import type {BrowserAudioFacts} from '../src/platform/playback/audio/output';

const recipe = (sampleRate: number, mode: 'direct' | 'converted') => ({id: 'plan-1', mode, codec: 'flac', sampleRate, channels: 2, bitDepth: 24});
const snapshot = (pinnedSampleRate: number, deliveredSampleRate: number, route: 'original' | 'repackaged' | 'converted', observed?: Record<string, unknown>) => ({
  session: {id: 'session-1', kind: 'audio', observedPlayback: {audio: observed}, playbackInfo: {
    route, losses: [], delivered: {audio: {codec: 'flac', bitDepth: 24, sampleRate: deliveredSampleRate, channels: 2}},
    source: {audio: {codec: 'flac', bitDepth: 24, sampleRate: 96000, channels: 2, layout: 'stereo', lossless: true}},
  }, presentation: {audio: recipe(pinnedSampleRate, route === 'converted' ? 'converted' : 'direct')}},
  playback: {audioEffects: {rendering: true, eqApplied: false, levelingApplied: false, settings: {normalization: 'off'}}, session: {id: 'session-1', audio: recipe(pinnedSampleRate, route === 'converted' ? 'converted' : 'direct')}, musicFallback: undefined},
} as never);
const facts = (over: Partial<BrowserAudioFacts> = {}): BrowserAudioFacts => ({
  engine: 'web-audio', bitPerfect: false, sourceRate: 96000, contextRate: 48000, resampledToContext: true,
  deviceRate: null, browserMayResample: true, sourceChannels: 2, outputChannels: 2, eqApplied: false, levelingApplied: false, ...over,
} as BrowserAudioFacts);
/** Every rate the drawn model states, from the catalogue key and its nested khz. */
const rates = (model: {pill?: unknown; steps?: unknown; state?: unknown} | undefined) => {
  const seen: number[] = [];
  const walk = (value: unknown) => {
    if (Array.isArray(value)) {for (const item of value) walk(item); return;}
    if (!value || typeof value !== 'object') return;
    for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
      if (key === 'khz' && typeof entry === 'number') seen.push(entry);
      else walk(entry);
    }
  };
  walk(model?.pill); walk(model?.steps); walk(model?.state);
  return seen;
};

test('96k into a 48k context is stated at the 48k the engine produced', () => {
  const model = watchMusicSignal(snapshot(96000, 96000, 'original'), null, facts());
  assert.ok(model, 'a drawn model is produced');
  assert.ok(rates(model).includes(48), 'the context rate appears in the drawn model');
});

test('a server conversion to 48k into a 48k context carries no browser resampling', () => {
  const model = watchMusicSignal(snapshot(48000, 48000, 'converted'), null, facts({sourceRate: 48000, contextRate: 48000, resampledToContext: false}));
  assert.equal(model?.state?.key, 'player.signal.server.converted');
  assert.equal(model?.steps.find(step => step.id === 'output')?.value, undefined, 'a matched context is not claimed as a physical output rate');
  assert.equal(JSON.stringify(model).includes('resampled'), false);
});

test('facts for another recipe or internally inconsistent facts are refused', () => {
  const refused = [
    facts({sourceChannels: 6}), facts({sourceRate: 44100}), facts({resampledToContext: false}),
    facts({contextRate: 0}), facts({deviceRate: 48000 as never}), facts({engine: 'other' as never}),
  ];
  for (const value of refused) assert.equal(rates(watchMusicSignal(snapshot(96000, 96000, 'original'), null, value)).includes(48), false);
});

test('a valid local pair replaces the whole output, dropping stale observed values', () => {
  const stale = {handling: 'decoded', sampleRate: 44100, bitDepth: 16, layout: '5.1'};
  const withLocal = watchMusicSignal(snapshot(96000, 96000, 'original', stale), null, facts());
  assert.equal(rates(withLocal).includes(48), true);
  assert.equal(rates(withLocal).includes(44.1), false);
  assert.equal(withLocal?.pill.key, 'player.signal.pill.notBitPerfect');
  const contradictory = watchMusicSignal(snapshot(96000, 96000, 'original', {...stale, handling: 'passthrough'}), null, facts());
  assert.equal(contradictory?.pill.key, 'player.signal.pill.notBitPerfect');
  assert.equal(JSON.stringify(contradictory).includes('player.signal.line.passthrough'), false);
  assert.equal(JSON.stringify(contradictory).includes('player.signal.output.decoded'), false);
});

test('absent local facts keep the committed observation', () => {
  const stale = {handling: 'decoded', sampleRate: 44100, bitDepth: 16, layout: '5.1'};
  const without = watchMusicSignal(snapshot(96000, 96000, 'original', stale), null);
  assert.equal(rates(without).includes(44.1), true);
});

test('source facts come from the committed comparison, never a default track or delivered format', () => {
  const shown: any = snapshot(48000, 48000, 'converted');
  shown.session.playbackInfo.source.audio = {codec: 'wavpack', sampleRate: 96000, bitDepth: 24, channels: 2};
  shown.options = {versions: [{id: 'default', audio: [{id: 'default-audio', default: true, codec: 'aac', sampleRate: 44100, bitDepth: 16}]}]};
  const model = watchMusicSignal(shown, null)!;
  assert.equal(model.steps.find(step => step.id === 'file')?.value, 'WavPack');
  assert.equal(model.source, 'WavPack', 'an unknown lossless fact is never guessed from a codec');
  shown.session.playbackInfo.source.audio.lossless = true;
  assert.ok(rates(watchMusicSignal(shown, null)).includes(96), 'a known lossless source keeps its own 96k rate separate from converted delivery');
  assert.equal(rates(model).includes(44.1), false, 'the default track never supplies missing facts');
  shown.session.playbackInfo = undefined;
  assert.equal(watchMusicSignal(shown, null), undefined, 'selected/default tracks cannot fabricate a missing committed comparison');
});

test('a replaced render recipe cannot keep the old music source comparison', () => {
  for (const change of [{id: 'replacement'}, {sampleRate: 44100}, {mode: 'unavailable'}]) {
    const shown: any = snapshot(48000, 48000, 'converted');
    Object.assign(shown.playback.session.audio, change);
    assert.equal(watchMusicSignal(shown, null), undefined);
  }
});
