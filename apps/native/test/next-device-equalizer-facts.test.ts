import test from 'node:test';
import assert from 'node:assert/strict';
import {createDeviceEqualizer, type DeviceEqualizer} from '../platform/equalizer-store.ts';
import type {DeviceEqualizerSnapshot} from '../src-next/platform/playback/equalizer.ts';
import {nativeSettingsEqualizerFacts} from './fixtures/superseded/device-equalizer-facts.ts';

const flat = (on = false): DeviceEqualizer => ({on, preamp: 0, gains: Array(10).fill(0)});

function fixture() {
  const calls = {read: 0, write: 0, reset: 0};
  let held = flat(), gate: Promise<void> | undefined, readError: Error | undefined, acknowledge: ((asked: DeviceEqualizer) => Promise<DeviceEqualizer>) | undefined;
  const port = {
    read: async () => {calls.read++; if (gate) await gate; if (readError) throw readError; return held;},
    write: async (asked: DeviceEqualizer) => {calls.write++; return acknowledge ? acknowledge(asked) : (held = asked);},
    reset: async () => {calls.reset++; return held = flat(held.on);},
  };
  return {
    store: createDeviceEqualizer(port), calls,
    holdRead: () => {let release!: () => void; gate = new Promise<void>(resolve => {release = resolve;}); return release;},
    failRead: (error: Error) => {readError = error;},
    answerWith: (next: (asked: DeviceEqualizer) => Promise<DeviceEqualizer>) => {acknowledge = next;},
    get held() {return held;},
  };
}

function confirmed(snapshot: DeviceEqualizerSnapshot) {
  const facts = nativeSettingsEqualizerFacts(snapshot);
  assert.equal(facts.state, 'confirmed');
  if (facts.state !== 'confirmed') assert.fail('expected a confirmed fact');
  return facts;
}

test('absent native support reports only the unsupported fact, never a default or held record', async () => {
  const store = createDeviceEqualizer();
  assert.deepEqual(nativeSettingsEqualizerFacts(store.getSnapshot()), {state: 'unsupported'});
  await assert.rejects(store.refresh(), /audio_eq_unavailable/);
  const facts = nativeSettingsEqualizerFacts(store.getSnapshot());
  assert.deepEqual(facts, {state: 'unsupported'});
  assert.deepEqual(Object.keys(facts), ['state']);
  assert.deepEqual(nativeSettingsEqualizerFacts({supported: false, ready: true, pending: true, value: flat(true)}), {state: 'unsupported'});
});

test('the facts truth table is exhaustive over support, readiness, pending and value', () => {
  const held = flat(true);
  // Every input combination, with only the state the contract permits for it.
  const cases: Array<[boolean, boolean, boolean, boolean, 'unsupported' | 'unconfirmed' | 'confirmed']> = [
    [false, false, false, false, 'unsupported'],
    [false, false, false, true, 'unsupported'],
    [false, false, true, false, 'unsupported'],
    [false, false, true, true, 'unsupported'],
    [false, true, false, false, 'unsupported'],
    [false, true, false, true, 'unsupported'],
    [false, true, true, false, 'unsupported'],
    [false, true, true, true, 'unsupported'],
    [true, false, false, false, 'unconfirmed'],
    [true, false, false, true, 'unconfirmed'],
    [true, false, true, false, 'unconfirmed'],
    [true, false, true, true, 'unconfirmed'],
    [true, true, false, false, 'unconfirmed'],
    [true, true, false, true, 'confirmed'],
    [true, true, true, false, 'unconfirmed'],
    [true, true, true, true, 'confirmed'],
  ];
  assert.equal(cases.length, 16);
  for (const [supported, ready, pending, hasValue, state] of cases) {
    const input = JSON.stringify({supported, ready, pending, hasValue});
    const snapshot: DeviceEqualizerSnapshot = {supported, ready, pending, ...(hasValue ? {value: held} : {})};
    const facts = nativeSettingsEqualizerFacts(snapshot);
    assert.equal(facts.state, state, input);
    if (facts.state === 'unsupported') {
      assert.deepEqual(Object.keys(facts), ['state'], input);
      continue;
    }
    assert.equal(facts.pending, pending, input);
    assert.deepEqual(Object.keys(facts).sort(), facts.state === 'confirmed' ? ['pending', 'state', 'value'] : ['pending', 'state'], input);
    if (facts.state === 'confirmed') assert.equal(facts.value, held, input);
    else assert.equal('value' in facts, false, input);
  }
});

test('the fact is unconfirmed before and during refresh, then exposes the exact confirmed receipt', async () => {
  const f = fixture();
  assert.deepEqual(nativeSettingsEqualizerFacts(f.store.getSnapshot()), {state: 'unconfirmed', pending: false});
  const release = f.holdRead(), refreshing = f.store.refresh();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(nativeSettingsEqualizerFacts(f.store.getSnapshot()), {state: 'unconfirmed', pending: true});
  assert.equal('value' in nativeSettingsEqualizerFacts(f.store.getSnapshot()), false);
  release(); await refreshing;
  const receipt = f.store.getSnapshot().value!;
  const facts = confirmed(f.store.getSnapshot());
  assert.equal(facts.pending, false);
  assert.equal(facts.value, receipt);
  assert.equal(facts.value, f.store.getSnapshot().value);
  assert.deepEqual(facts.value, flat());
  const laterRelease = f.holdRead(), laterRefresh = f.store.refresh();
  await new Promise(resolve => setImmediate(resolve));
  const duringLater = confirmed(f.store.getSnapshot());
  assert.equal(duringLater.pending, true);
  assert.equal(duringLater.value, receipt);
  laterRelease(); await laterRefresh;
  const reread = f.store.getSnapshot().value!;
  const afterLater = confirmed(f.store.getSnapshot());
  assert.equal(afterLater.pending, false);
  assert.equal(afterLater.value, reread);
  assert.deepEqual(afterLater.value, receipt);
});

test('a rejected refresh keeps the last confirmed receipt through pending and failure', async () => {
  const f = fixture();
  await f.store.refresh();
  const last = f.store.getSnapshot().value!;
  f.failRead(new Error('audio_eq_invalid'));
  const release = f.holdRead(), refreshing = f.store.refresh();
  await new Promise(resolve => setImmediate(resolve));
  const during = confirmed(f.store.getSnapshot());
  assert.equal(during.pending, true);
  assert.equal(during.value, last);
  release();
  await assert.rejects(refreshing, /audio_eq_invalid/);
  const after = confirmed(f.store.getSnapshot());
  assert.equal(after.pending, false);
  assert.equal(after.value, last);
  assert.equal(f.store.getSnapshot().ready, true);
});

test('a pending update keeps the last confirmed value by identity until the native acknowledgement', async () => {
  const f = fixture();
  await f.store.refresh();
  const last = f.store.getSnapshot().value!;
  let acknowledge!: (next: DeviceEqualizer) => void;
  f.answerWith(() => new Promise<DeviceEqualizer>(resolve => {acknowledge = resolve;}));
  const writing = f.store.update({on: true, preamp: -3});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.store.getSnapshot().pending, true);
  const during = confirmed(f.store.getSnapshot());
  assert.equal(during.pending, true);
  assert.equal(during.value, last);
  assert.equal(during.value.on, false);
  acknowledge({on: true, preamp: -3, gains: Array(10).fill(0)});
  const acknowledged = await writing;
  const after = confirmed(f.store.getSnapshot());
  assert.equal(after.pending, false);
  assert.equal(after.value, acknowledged);
  assert.equal(after.value.on, true);
});

test('a rejected or contradictory native acknowledgement retains the last confirmed value', async () => {
  const f = fixture();
  await f.store.refresh();
  const last = f.store.getSnapshot().value!;
  let rejectWrite!: (error: Error) => void;
  f.answerWith(() => new Promise<DeviceEqualizer>((_, reject) => {rejectWrite = reject;}));
  const rejecting = f.store.update({on: true});
  await new Promise(resolve => setImmediate(resolve));
  const during = confirmed(f.store.getSnapshot());
  assert.equal(during.pending, true);
  assert.equal(during.value, last);
  rejectWrite(new Error('audio_eq_invalid'));
  await assert.rejects(rejecting, /audio_eq_invalid/);
  let facts = confirmed(f.store.getSnapshot());
  assert.equal(facts.pending, false);
  assert.equal(facts.value, last);
  f.answerWith(async () => flat());
  await assert.rejects(f.store.update({on: true}), /audio_eq_invalid/);
  facts = confirmed(f.store.getSnapshot());
  assert.equal(facts.pending, false);
  assert.equal(facts.value, last);
  assert.equal(facts.value.on, false);
});

test('the projection keeps the frozen actual snapshot and value identity without a port call or mutation', async () => {
  const f = fixture();
  assert.deepEqual(nativeSettingsEqualizerFacts(f.store.getSnapshot()), {state: 'unconfirmed', pending: false});
  assert.deepEqual(f.calls, {read: 0, write: 0, reset: 0});
  await f.store.refresh();
  const snapshot = f.store.getSnapshot(), value = snapshot.value!, calls = {...f.calls}, before = JSON.stringify(snapshot);
  assert.ok(Object.isFrozen(snapshot) && Object.isFrozen(value) && Object.isFrozen(value.gains));
  const projected = Array.from({length: 5}, () => confirmed(snapshot));
  assert.ok(projected.every(facts => facts.value === value));
  assert.deepEqual(f.calls, calls);
  assert.equal(JSON.stringify(snapshot), before);
  assert.equal(f.store.getSnapshot(), snapshot);
  assert.equal(projected[0].value, f.store.getSnapshot().value);
});
