import test from 'node:test';
import assert from 'node:assert/strict';
import {createDeviceEqualizer, deviceEqualizerValue, type DeviceEqualizer} from '../platform/equalizer-store.ts';
import {NativeModules} from './react-native-stub.mjs';

const flat = (on = false): DeviceEqualizer => ({on, preamp: 0, gains: Array(10).fill(0)});
function fixture() {
  let value = flat(), fail = false;
  const writes: DeviceEqualizer[] = [];
  const port = {
    read: async () => value,
    write: async (next: DeviceEqualizer) => { if (fail) throw new Error('audio_eq_invalid'); writes.push(next); value = next; return value; },
    reset: async () => { value = flat(value.on); return value; },
  };
  return {store: createDeviceEqualizer(port), port, writes, get value() { return value; }, fail: (next: boolean) => { fail = next; }};
}

test('EQ support and value come from installed native persistence, without an optimistic default', async () => {
  const f = fixture();
  assert.deepEqual(f.store.getSnapshot(), {supported: true, ready: false, pending: false});
  await f.store.refresh();
  assert.deepEqual(f.store.getSnapshot().value, flat());
  assert.ok(Object.isFrozen(f.store.getSnapshot().value?.gains));
  assert.ok(!('eqApplied' in f.store.getSnapshot()), 'saved intent is not a renderer observation');
});

test('a setting is published only after the native write acknowledges the exact durable value', async () => {
  let finish!: (value: DeviceEqualizer) => void;
  const store = createDeviceEqualizer({read: async () => flat(), write: () => new Promise(resolve => { finish = resolve; }), reset: async () => flat()});
  await store.refresh();
  const writing = store.update({on: true, preamp: -3});
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(store.getSnapshot().value, flat());
  assert.equal(store.getSnapshot().pending, true);
  finish({...flat(true), preamp: -3}); await writing;
  assert.deepEqual(store.getSnapshot().value, {...flat(true), preamp: -3});
  assert.equal(store.getSnapshot().pending, false);
});

test('Settings and player updates serialize one record and capture bands before a queued write', async () => {
  const f = fixture(), bands = Array(10).fill(0); bands[9] = 0.5;
  const a = f.store.update({on: true}), b = f.store.update({preamp: -12}), c = f.store.update({gains: bands});
  bands[9] = 12;
  await Promise.all([a, b, c]);
  assert.equal(f.writes.length, 3);
  assert.equal(f.value.on, true); assert.equal(f.value.preamp, -12); assert.equal(f.value.gains[9], 0.5);
  assert.deepEqual(Object.keys(f.value).sort(), ['gains', 'on', 'preamp']);
});

test('Reset to flat retains the current switch, resets all ten bands and publishes one native acknowledgement', async () => {
  const f = fixture(); await f.store.update({on: true, preamp: 12, gains: Array(10).fill(-12)});
  const snapshots: unknown[] = [], off = f.store.subscribe(() => { snapshots.push(f.store.getSnapshot().value); });
  try { await f.store.reset(); } finally { off(); }
  assert.deepEqual(f.value, flat(true)); assert.deepEqual(f.store.getSnapshot().value, flat(true));
  assert.ok(snapshots.every(value => value === undefined || (value as DeviceEqualizer).on), 'reset never toggles the switch');
});

test('invalid half-step/domain/shape inputs never write and no output/preset aliases are accepted', async () => {
  const f = fixture();
  for (const patch of [{preamp: 0.25}, {preamp: NaN}, {preamp: 12.5}, {gains: Array(9).fill(0)}, {gains: Array(10).fill(Infinity)}, {outputId: 'dac'}, {preset: 'flat'}, {on: 'yes'}, {}]) {
    await assert.rejects(f.store.update(patch as Partial<DeviceEqualizer>), /audio_eq_invalid/);
  }
  assert.equal(f.writes.length, 0);
  assert.throws(() => deviceEqualizerValue({...flat(), outputId: 'headphones'}), /audio_eq_invalid/);
  assert.throws(() => deviceEqualizerValue({enabled: false, preampDb: 0, bands: Array(10).fill(0)}), /audio_eq_invalid/);
});

test('failed or contradictory acknowledgement preserves the last confirmed record and permits retry', async () => {
  const f = fixture(); await f.store.refresh(); f.fail(true);
  await assert.rejects(f.store.update({on: true}), /audio_eq_invalid/);
  assert.deepEqual(f.store.getSnapshot().value, flat());
  assert.equal(f.store.getSnapshot().pending, false);
  f.fail(false); await f.store.update({on: true}); assert.equal(f.store.getSnapshot().value?.on, true);
  const store = createDeviceEqualizer({read: async () => flat(), write: async () => flat(), reset: async () => flat()});
  await store.refresh(); await assert.rejects(store.update({on: true}), /audio_eq_invalid/);
  assert.deepEqual(store.getSnapshot().value, flat());
});

test('absent native support stays absent with no local persistence or simulated success', async () => {
  const store = createDeviceEqualizer();
  assert.equal(store.getSnapshot().supported, false);
  await assert.rejects(store.refresh(), /audio_eq_unavailable/);
  await assert.rejects(store.update({on: true}), /audio_eq_unavailable/);
  await assert.rejects(store.reset(), /audio_eq_unavailable/);
  assert.equal(store.getSnapshot().value, undefined);
});

test('Android binding uses the genuine native read/write/reset DTO and shares the durable ack semantics', async () => {
  const modules = NativeModules as unknown as Record<string, unknown>, previous = modules.PorticoPlaybackModule;
  let held = {enabled: false, preampDb: 0, bands: Array(10).fill(0)};
  const calls: unknown[] = [];
  modules.PorticoPlaybackModule = {equalizer: async (value: typeof held | {reset: true} | null) => {
    calls.push(value);
    if (value && 'reset' in value) held = {...held, preampDb: 0, bands: Array(10).fill(0)};
    else if (value) held = value;
    return held;
  }};
  try {
    const path = '../platform/equalizer.android.ts?native-eq';
    const {nativeEqualizer} = await import(path) as typeof import('../platform/equalizer.android');
    await nativeEqualizer.refresh(); await nativeEqualizer.update({on: true, preamp: -0.5}); await nativeEqualizer.reset();
    assert.deepEqual(calls, [null, {enabled: true, preampDb: -0.5, bands: Array(10).fill(0)}, {reset: true}]);
    assert.deepEqual(nativeEqualizer.getSnapshot().value, flat(true));
  } finally {
    if (previous === undefined) delete modules.PorticoPlaybackModule; else modules.PorticoPlaybackModule = previous;
  }
});
