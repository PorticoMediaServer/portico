import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeModules} from './react-native-stub.mjs';
import {NativeQueueStore} from '../src-next/platform/queue-storage';
import {NativeDeviceSettings, bindNativeDeviceSettings, nativeDeviceSettings} from '../src-next/platform/device-settings';

const key = 'preferences:v1';
const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};
async function records(run: (state: {raw: string | null; saves: number; beforeSave?: () => Promise<void>; beforeRead?: () => Promise<void>}) => Promise<void>) {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>, old = {read: runtime.loadQueueRecord, write: runtime.saveQueueRecord};
  const state: Parameters<typeof run>[0] = {raw: JSON.stringify({playerEngine: 'automatic', forceStereo: false, cardScale: 1.25, serverCache: {preserved: true}}), saves: 0};
  runtime.loadQueueRecord = async (recordKey: string) => {assert.equal(recordKey, key); await state.beforeRead?.(); return state.raw;};
  runtime.saveQueueRecord = async (recordKey: string, raw: string) => {assert.equal(recordKey, key); await state.beforeSave?.(); state.raw = raw; state.saves++;};
  try {await run(state);} finally {runtime.loadQueueRecord = old.read; runtime.saveQueueRecord = old.write;}
}
async function fixture() {
  const storage = new NativeQueueStore(), listeners = new Set<() => void>(); let current = true, publications = 0;
  let observed = await storage.change(key, raw => ({record: raw, value: raw}));
  const owner = {current: () => current, subscribePreferences: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const ports = {storage, readRecord: () => observed, acceptRecord: (raw: unknown) => {observed = raw; publications++; for (const listener of listeners) listener();}};
  return {owner, ports, source: bindNativeDeviceSettings(owner, ports), retire: () => {current = false;}, publications: () => publications};
}

test('device setting resolves after durable save/readback, then publishes the same committed snapshot without replacing caches', () => records(async state => {
  const f = await fixture(), begun = deferred(), durable = deferred(), values: unknown[] = [];
  state.beforeSave = async () => {begun.resolve(); await durable.promise;};
  const off = f.source.subscribe(() => values.push(f.source.read('forceStereo'))); let acknowledged = false;
  const write = f.source.write('forceStereo', true).then(() => {acknowledged = true;});
  await begun.promise; assert.equal(f.source.read('forceStereo'), false); assert.equal(acknowledged, false); assert.deepEqual(values, []);
  durable.resolve(); await write;
  assert.equal(f.source.read('forceStereo'), true); assert.deepEqual(values, [true]); assert.equal(f.publications(), 1);
  assert.deepEqual(JSON.parse(state.raw!), {playerEngine: 'automatic', forceStereo: true, cardScale: 1.25, serverCache: {preserved: true}});
  assert.equal(nativeDeviceSettings(f.owner), f.source); assert.throws(() => bindNativeDeviceSettings(f.owner, f.ports)); off();
}));

test('concurrent native transactions merge only their actual playback keys and expose no registry or unbound device writer', () => records(async state => {
  const f = await fixture(), other = new NativeDeviceSettings(f.owner, f.ports);
  await Promise.all([f.source.write('playerEngine', 'native'), other.write('disableHdr', true)]);
  assert.equal(f.source.read('playerEngine'), 'native'); assert.equal(f.source.read('disableHdr'), true);
  const count = state.saves;
  for (const id of ['cardSize', 'playbackMethod', 'layout', 'reduceMotion', 'textSize'] as const) {
    assert.equal(f.source.read(id), undefined); await assert.rejects(f.source.write(id, 'medium'), {code: 'unsupported'});
  }
  await assert.rejects(f.source.write('playerEngine', 'unknown'));
  await assert.rejects(f.source.write('forceStereo', 'true'));
  assert.equal(state.saves, count); assert.deepEqual(JSON.parse(state.raw!).serverCache, {preserved: true});
}));

test('native save refusal and authority retirement never publish optimistic success or erase an unreadable record', () => records(async state => {
  const f = await fixture(); state.beforeSave = async () => {throw new Error('save refused');};
  await assert.rejects(f.source.write('disablePassthrough', true)); assert.equal(f.publications(), 0); assert.equal(f.source.read('disablePassthrough'), false);
  state.beforeSave = undefined; const begun = deferred(), read = deferred();
  state.beforeRead = async () => {begun.resolve(); await read.promise;};
  const pending = f.source.write('forceStereo', true); await begun.promise; f.retire(); read.resolve();
  await assert.rejects(pending, {name: 'AbortError'}); assert.equal(state.saves, 0); assert.equal(f.publications(), 0);
  assert.throws(() => f.source.read('playerEngine'), {name: 'AbortError'});
  state.beforeRead = undefined; state.raw = JSON.stringify(['not a preferences record']);
  const next = await fixture(); await assert.rejects(next.source.write('forceStereo', true)); assert.equal(state.raw, JSON.stringify(['not a preferences record']));
}));
