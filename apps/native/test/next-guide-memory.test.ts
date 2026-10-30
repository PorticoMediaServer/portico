import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules} from './react-native-stub.mjs';
import {NativeQueueStore} from '../src-next/platform/queue-storage';
import {NativeGuideMemory, nativeGuideEntryKey, nativeGuideMemory, type NativeGuideChoice} from '../src-next/screens/settings-live/guide-memory';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import type {ChannelApi} from '../../../packages/client-core/src/channel-guide';

const scope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'} as const;
const all = {kind: 'all', sourceId: ''} as const;
const source = {kind: 'live-source', sourceId: 'source'} as const;
const key = (value = scope) => 'next.guide.v1:' + JSON.stringify([value.serverId, value.authority, value.accountId, value.profileId]);
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
function owner() {
  let current = true;
  const authority = {...scope, epoch: 'session', viewerFence: 'home'};
  const listeners = new Set<() => void>();
  return {authority: () => authority, current: () => current, subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    revoke: () => {current = false; for (const listener of listeners) listener();}};
}
async function withRecords(run: (state: {records: Map<string, string>; writes: string[]; save?: () => Promise<void>; load?: () => Promise<void>}) => Promise<void>) {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>, before = {load: runtime.loadQueueRecord, save: runtime.saveQueueRecord};
  const state: Parameters<typeof run>[0] = {records: new Map(), writes: []};
  runtime.loadQueueRecord = async (recordKey: string) => {await state.load?.(); return state.records.get(recordKey) ?? null;};
  runtime.saveQueueRecord = async (recordKey: string, raw: string) => {await state.save?.(); state.records.set(recordKey, raw); state.writes.push(recordKey);};
  try {await run(state);} finally {runtime.loadQueueRecord = before.load; runtime.saveQueueRecord = before.save;}
}
const directory = (requests: string[]): ChannelApi => ({request: async <T>(path: string) => {
  requests.push(path); return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'guide', revision: '1', sources: []} as T;
}});

test('real native record transactions wait for durability, merge concurrent fields and preserve unrelated record data', () => withRecords(async state => {
  const begun = deferred<void>(), durable = deferred<void>();
  state.records.set(key(), JSON.stringify({unrelated: {preserved: true}, choices: []}));
  state.save = async () => {begun.resolve(); await durable.promise;};
  const first = new NativeGuideMemory(new NativeQueueStore(), scope, () => true), second = new NativeGuideMemory(new NativeQueueStore(), scope, () => true);
  await first.read(); assert.equal(state.writes.length, 0, 'reads never normalize or save the native record');
  let acknowledged = false;
  const view = first.write(all, {view: 'list'}).then(() => {acknowledged = true;});
  await begun.promise; const sort = second.write(all, {sort: 'name'});
  assert.equal(acknowledged, false); assert.equal(state.writes.length, 0);
  durable.resolve(); await Promise.all([view, sort]);
  assert.deepEqual((await first.read()).get(nativeGuideEntryKey(all)), {view: 'list', sort: 'name'});
  assert.deepEqual(JSON.parse(state.records.get(key())!).unrelated, {preserved: true});
}));

test('viewer, server, authority and source provenance stay separate; old viewer-only memory is never borrowed', () => withRecords(async state => {
  state.records.set('channels:v1:' + JSON.stringify(['local', 'account', 'profile']), JSON.stringify({views: {all: 'list'}}));
  const memory = new NativeGuideMemory(new NativeQueueStore(), scope, () => true);
  assert.equal((await memory.read()).size, 0);
  await memory.write(source, {view: 'guide', sort: 'name'});
  await memory.write({kind: 'library-channel', sourceId: 'source'}, {view: 'list'});
  assert.deepEqual((await memory.read()).get(nativeGuideEntryKey(source)), {view: 'guide', sort: 'name'});
  for (const other of [{...scope, serverId: 'other'}, {...scope, profileId: 'other'}, {...scope, authority: 'hosted' as const}]) {
    assert.equal((await new NativeGuideMemory(new NativeQueueStore(), other, () => true).read()).size, 0);
  }
  assert.equal(state.records.size, 2);
}));

test('known-value codec stays bounded and retirement while the native read is pending prevents a queued write', () => withRecords(async state => {
  const memory = new NativeGuideMemory(new NativeQueueStore(), scope, () => true);
  for (let index = 0; index < 51; index++) await memory.write({kind: 'live-source', sourceId: `source-${index}`}, {sort: 'name'});
  const choices = await memory.read(); assert.equal(choices.size, 50); assert.equal(choices.has(nativeGuideEntryKey({kind: 'live-source', sourceId: 'source-0'})), false);
  const badRecord = JSON.stringify({choices: [{entry: nativeGuideEntryKey(all), view: 'grid', sort: 'name'}, {entry: '__proto__', view: 'list'}]});
  state.records.set(key(), badRecord); const beforeRead = state.writes.length;
  assert.equal((await memory.read()).size, 0); assert.equal(state.records.get(key()), badRecord); assert.equal(state.writes.length, beforeRead);
  const invalid = {...all, sourceId: 'not-all'}; await assert.rejects(memory.write(invalid, {view: 'list'}));
  const before = state.writes.length, waiting = deferred<void>(), started = deferred<void>(); let current = true;
  state.load = async () => {started.resolve(); await waiting.promise;};
  const retired = new NativeGuideMemory(new NativeQueueStore(), scope, () => current);
  const pending = retired.write(all, {view: 'list'}); await started.promise; current = false; waiting.resolve();
  await assert.rejects(pending, {name: 'AbortError'}); assert.equal(state.writes.length, before);
}));

test('Guide restores committed source choices across real controller lifetimes without server preference writes', () => withRecords(async () => {
  const lifecycle = owner(), lifetime = new NativeScreenLifetime(lifecycle), errors: unknown[] = [], requests: string[] = [];
  const memory = new NativeGuideMemory(new NativeQueueStore(), scope, lifetime.current);
  const first = new NativeLiveController(lifetime, directory(requests), 'UTC', {port: memory, reportError: error => errors.push(error)});
  await first.loadSources(); first.chooseView('list'); first.select({sort: 'name'}); first.select(source); first.chooseView('guide');
  await tick(); await memory.read(); first.dispose();
  const second = new NativeLiveController(lifetime, directory(requests), 'UTC', {port: memory, reportError: error => errors.push(error)});
  await second.loadSources(); assert.equal(second.getSnapshot().view, 'list'); assert.equal(second.getSnapshot().options.sort, 'name');
  second.select(source); assert.equal(second.getSnapshot().view, 'guide'); assert.equal(second.getSnapshot().options.sort, 'number');
  assert.ok(requests.every(path => !path.includes('preferences'))); assert.deepEqual(errors, []);
  second.dispose(); lifetime.dispose();
}));

test('late initial memory merges a newer field choice on the current source; retired read cannot publish or request Guide', async () => {
  const lifecycle = owner(), lifetime = new NativeScreenLifetime(lifecycle), stored = deferred<ReadonlyMap<string, NativeGuideChoice>>(), requests: string[] = [], writes: unknown[] = [];
  const controller = new NativeLiveController(lifetime, directory(requests), 'UTC', {port: {read: () => stored.promise, write: async (entry, choice) => {writes.push({entry, choice});}}, reportError: (error: unknown) => {throw error;}});
  const loading = controller.loadSources(); controller.select(source); controller.chooseView('list'); await tick();
  assert.deepEqual(requests, []);
  stored.resolve(new Map([[nativeGuideEntryKey(source), {view: 'guide', sort: 'name'}]])); await loading;
  assert.equal(controller.getSnapshot().options.sourceId, 'source'); assert.equal(controller.getSnapshot().view, 'list'); assert.equal(controller.getSnapshot().options.sort, 'name');
  assert.equal(writes.length, 1);
  controller.dispose(); lifetime.dispose();
  const other = owner(), ended = new NativeScreenLifetime(other), late = deferred<ReadonlyMap<string, NativeGuideChoice>>(), after: string[] = [];
  const stale = new NativeLiveController(ended, directory(after), 'UTC', {port: {read: () => late.promise, write: async () => {}}, reportError: (error: unknown) => {throw error;}});
  const pending = stale.loadSources(); other.revoke(); late.resolve(new Map()); await pending;
  assert.deepEqual(after, []); assert.equal(stale.getSnapshot().view, undefined); stale.dispose();
});

test('memory failures are reported, never converted into a successful durable choice, and account-only contexts cannot bind', () => withRecords(async state => {
  const lifecycle = owner(), lifetime = new NativeScreenLifetime(lifecycle), errors: unknown[] = [];
  const memory = nativeGuideMemory({...scope, role: 'owner'}, lifetime);
  assert.throws(() => nativeGuideMemory({...scope, role: 'account'}, lifetime), {name: 'AbortError'});
  assert.throws(() => nativeGuideMemory({...scope, role: 'owner', serverId: 'other'}, lifetime), {name: 'AbortError'});
  const controller = new NativeLiveController(lifetime, directory([]), 'UTC', {port: memory, reportError: error => errors.push(error)});
  await controller.loadSources(); state.save = async () => {throw new Error('native save refused');};
  controller.chooseView('list'); await tick();
  assert.equal(errors.length, 1); assert.equal((await memory.read()).get(nativeGuideEntryKey(all)), undefined);
  controller.dispose(); lifetime.dispose();
}));
