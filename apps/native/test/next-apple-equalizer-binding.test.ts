import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as store from '../platform/equalizer-store.ts';

const flat = () => Array<number>(10).fill(0);
const nativeValue = (eqEnabled = false, eqPreampDb = 0, eqBands = flat()) => ({eqEnabled, eqPreampDb, eqBands, sampleRateMatching: 'strict', outputSampleRate: 48000, bitPerfectVerified: false});
type AppleEqualizer = typeof import('../platform/equalizer.ios').nativeEqualizer;
async function binding(native?: Record<string, unknown>): Promise<AppleEqualizer> {
  return (await screenModule(new URL('../platform/equalizer.ios.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoPlaybackModule: native}}, './equalizer-store': store,
  })).nativeEqualizer as AppleEqualizer;
}

test('Apple binds only all three installed native commands, without fabricating a saved record', async () => {
  for (const missing of ['module', 'audioProcessing', 'setAudioEqualizer', 'resetAudioEqualizer']) {
    const native: Record<string, unknown> = {audioProcessing: async () => nativeValue(), setAudioEqualizer: async () => nativeValue(), resetAudioEqualizer: async () => nativeValue()};
    if (missing !== 'module') delete native[missing];
    const eq = await binding(missing === 'module' ? undefined : native);
    assert.deepEqual(eq.getSnapshot(), {supported: false, ready: false, pending: false});
    await assert.rejects(eq.refresh(), /audio_eq_unavailable/);
  }
});

test('Apple reads only the committed native EQ and writes through the sole native SQLite command', async () => {
  let current = nativeValue(), writes = 0;
  const calls: unknown[] = [];
  const eq = await binding({audioProcessing: async () => {calls.push('read'); return current;},
    setAudioEqualizer: async (value: {enabled: boolean; preampDb: number; bands: number[]}) => {
      calls.push(value); writes++; current = nativeValue(value.enabled, value.preampDb, value.bands); return current;
    }, resetAudioEqualizer: async () => {calls.push('reset'); current = nativeValue(current.eqEnabled); return current;}});
  assert.deepEqual(eq.getSnapshot(), {supported: true, ready: false, pending: false});
  const first = await eq.refresh(); assert.deepEqual(first, {on: false, preamp: 0, gains: flat()});
  const gains = [-12, -.5, 0, .5, 12, 3.5, -2, 4, -4, 2];
  const confirmed = await eq.update({on: true, preamp: -6, gains});
  assert.deepEqual(confirmed, {on: true, preamp: -6, gains});
  assert.deepEqual(calls, ['read', {enabled: true, preampDb: -6, bands: gains}]); assert.equal(writes, 1);
  assert.equal(current.sampleRateMatching, 'strict'); assert.equal(Object.hasOwn(eq.getSnapshot(), 'eqApplied'), false);
  assert.equal(Object.hasOwn(eq.getSnapshot(), 'levelingApplied'), false);
  assert.deepEqual(await eq.reset(), {on: true, preamp: 0, gains: flat()}); assert.equal(calls.at(-1), 'reset');
  assert.equal(current.sampleRateMatching, 'strict', 'reset never writes the independently committed rate setting');
});

test('Apple exposes only a completed native write and keeps the last committed value on rejection', async () => {
  let finish: ((value: unknown) => void) | undefined, fail: ((error: Error) => void) | undefined;
  const eq = await binding({audioProcessing: async () => nativeValue(), resetAudioEqualizer: async () => nativeValue(),
    setAudioEqualizer: () => new Promise((resolve, reject) => {finish = resolve; fail = reject;})});
  await eq.refresh(); const held = eq.getSnapshot().value;
  const writing = eq.update({on: true}); await Promise.resolve();
  assert.equal(eq.getSnapshot().pending, true); assert.equal(eq.getSnapshot().value, held);
  finish!(nativeValue(true)); await writing; assert.equal(eq.getSnapshot().value?.on, true);
  const committed = eq.getSnapshot().value;
  const rejected = eq.update({preamp: -6}); await Promise.resolve(); fail!(new Error('audio_equalizer_storage_unavailable'));
  await assert.rejects(rejected, /audio_equalizer_storage_unavailable/);
  assert.equal(eq.getSnapshot().value, committed); assert.equal(eq.getSnapshot().pending, false);
});

test('Apple rejects malformed or unconfirmed native EQ records without inferring defaults or applied facts', async () => {
  for (const value of [null, [], {}, {...nativeValue(), eqEnabled: 1}, {...nativeValue(), eqBands: [0]}, {...nativeValue(), eqPreampDb: .25}, {...nativeValue(), eqBands: [...flat().slice(0, 9), 12.5]}]) {
    const eq = await binding({audioProcessing: async () => value, setAudioEqualizer: async () => value, resetAudioEqualizer: async () => value});
    await assert.rejects(eq.refresh(), /audio_eq_invalid/); assert.equal(eq.getSnapshot().ready, false); assert.equal(eq.getSnapshot().value, undefined);
  }
  const eq = await binding({audioProcessing: async () => nativeValue(), setAudioEqualizer: async () => nativeValue(false), resetAudioEqualizer: async () => nativeValue()});
  await eq.refresh(); const held = eq.getSnapshot().value;
  await assert.rejects(eq.update({on: true}), /audio_eq_invalid/); assert.equal(eq.getSnapshot().value, held);
});
