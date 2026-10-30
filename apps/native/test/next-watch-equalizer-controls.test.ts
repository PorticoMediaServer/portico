import test from 'node:test';
import assert from 'node:assert/strict';
import {createDeviceEqualizer} from '../platform/equalizer-store.ts';
import {nativeEqualizerControls, nativeEqualizerGain} from '../src-next/screens/watch/equalizer-controls.ts';

test('EQ editor uses the one actual device store, preserving concurrent band edits and reset switch state', async () => {
  let native = {on: true, preamp: -6, gains: Array(10).fill(0)}, writes = 0;
  const store = createDeviceEqualizer({read: async () => native, write: async value => {writes++; native = {...value, gains: [...value.gains]}; return native;},
    reset: async () => {native = {...native, preamp: 0, gains: Array(10).fill(0)}; return native;}});
  const editor = nativeEqualizerControls(store);
  await Promise.all([editor.gain(0, 5.5), editor.gain(1, -2), editor.gain('preamp', -4.5)]);
  assert.deepEqual(store.getSnapshot().value, {on: true, preamp: -4.5, gains: [5.5, -2, 0, 0, 0, 0, 0, 0, 0, 0]});
  await editor.reset(); assert.equal(store.getSnapshot().value!.on, true); assert.equal(store.getSnapshot().value!.preamp, 0);
  assert.ok(store.getSnapshot().value!.gains.every(value => value === 0));
  await editor.enable(false); const before = writes; await editor.gain(3, 7); await editor.reset();
  assert.equal(writes, before); assert.equal(store.getSnapshot().value!.on, false);
});
test('unsupported producer and invalid values never become a synthetic flat EQ record', async () => {
  const store = createDeviceEqualizer(), editor = nativeEqualizerControls(store);
  await editor.enable(true); await editor.gain(0, 12); await editor.reset();
  assert.equal(store.getSnapshot().value, undefined); assert.equal(store.getSnapshot().supported, false);
  assert.equal(nativeEqualizerGain(NaN), undefined); assert.equal(nativeEqualizerGain(2.3), 2.5); assert.equal(nativeEqualizerGain(20), 12);
});
test('held TV Up events each advance the confirmed native band, stop at the limit and preserve other bands', async () => {
  let native = {on: true, preamp: -6, gains: [0, 3, 0, 0, 0, 0, 2.5, 0, 0, 0]}, writes = 0;
  const store = createDeviceEqualizer({read: async () => native, write: async value => {writes++; await Promise.resolve(); native = {...value, gains: [...value.gains]}; return native;}, reset: async () => assert.fail()});
  const editor = nativeEqualizerControls(store);
  await Promise.all([editor.nudge(6, 1), editor.nudge(6, 1), editor.nudge(6, 1)]);
  assert.equal(store.getSnapshot().value!.gains[6], 4);
  assert.equal(store.getSnapshot().value!.gains[1], 3);
  await editor.gain(6, 12); const before = writes; await editor.nudge(6, 1);
  assert.equal(writes, before);
  await editor.nudge(6, -1); assert.equal(store.getSnapshot().value!.gains[6], 11.5);
});
