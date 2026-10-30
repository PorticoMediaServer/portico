import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeModules} from './react-native-stub.mjs';
import {parsePreferenceSnapshot, type PreferenceSnapshot, type PreferencePatch} from '../../../packages/client-core/src/preferences';
import {bindNativeServerPreferences, nativeServerPreferences} from '../src-next/platform/server-preferences';
import {NativeQueueStore} from '../src-next/platform/queue-storage';
import {bindNativeDeviceSettings, nativeDeviceSettings} from '../src-next/platform/device-settings';
import {nativeDevicePreferences} from '../src-next/screens/settings-live/device-preference-consumer';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';

const key = 'preferences:v1';
const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};
const device = (extra: Record<string, unknown> = {}) => ({playerEngine: 'automatic', forceStereo: false, disableHdr: false, ...extra});
type State = {raw: string | null; saves: number; loads: number; beforeSave?: () => Promise<void>; beforeRead?: () => Promise<void>};

async function records(run: (state: State) => Promise<void>, initial = JSON.stringify(device({cardScale: 1.25, serverCache: {kept: true}}))) {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>, old = {read: runtime.loadQueueRecord, write: runtime.saveQueueRecord};
  const state: State = {raw: initial, saves: 0, loads: 0};
  runtime.loadQueueRecord = async (recordKey: string) => {assert.equal(recordKey, key); state.loads++; await state.beforeRead?.(); return state.raw;};
  runtime.saveQueueRecord = async (recordKey: string, next: string) => {assert.equal(recordKey, key); await state.beforeSave?.(); state.raw = next; state.saves++;};
  try {await run(state);} finally {runtime.loadQueueRecord = old.read; runtime.saveQueueRecord = old.write;}
}

async function fixture() {
  const storage = new NativeQueueStore(), listeners = new Set<() => void>();
  let current = true;
  let observed = await storage.change(key, raw => ({record: raw, value: raw}));
  const context = {current: () => current, subscribePreferences: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}} as unknown as NativeShellBootstrapContext;
  const ports = {storage, readRecord: () => observed, acceptRecord: (raw: unknown) => {observed = raw; for (const listener of [...listeners]) listener();}};
  const source = bindNativeDeviceSettings(context, ports);
  return {context, ports, source, retire: () => {current = false;}, listeners};
}

test('drawn device rows read through the canonical codecs and unbound rows read nothing', async () => records(async state => {
  const f = await fixture(), consumer = nativeDevicePreferences(f.context);
  assert.equal(nativeDevicePreferences(f.context), consumer, 'one stable facade for the bootstrap context');
  assert.equal(nativeDeviceSettings(f.context), f.source, 'the consumer borrows the installed source');
  assert.equal(consumer.read('forceStereo'), false);
  assert.equal(consumer.read('disableHdr'), false);
  const loads = state.loads;
  for (const id of ['reduceMotion', 'textSize', 'layout', 'libraryView'] as const) assert.equal(consumer.read(id), undefined, id);
  assert.equal(state.loads, loads, 'unbound rows never touch the device record');
  await f.source.write('forceStereo', true);
  await f.source.write('disableHdr', true);
  assert.equal(consumer.read('forceStereo'), true);
  assert.equal(consumer.read('disableHdr'), true);
}));

test('a device write resolves only after the canonical save and actual reread', async () => records(async state => {
  const f = await fixture(), consumer = nativeDevicePreferences(f.context), begun = deferred(), durable = deferred();
  state.beforeSave = async () => {begun.resolve(); await durable.promise;};
  const before = state.loads;
  let settled = false;
  const writing = consumer.write('forceStereo', true).then(value => {settled = true; return value;});
  await begun.promise;
  assert.equal(settled, false);
  assert.equal(consumer.read('forceStereo'), false);
  assert.equal(JSON.parse(state.raw!).forceStereo, false);
  durable.resolve();
  assert.equal(await writing, true);
  assert.equal(state.loads, before + 2, 'one transaction read and one committed reread');
  assert.equal(state.saves, 1);
  assert.deepEqual(JSON.parse(state.raw!), device({forceStereo: true, cardScale: 1.25, serverCache: {kept: true}}));
  assert.equal(await consumer.write('disableHdr', true), true);
  assert.deepEqual(JSON.parse(state.raw!), device({forceStereo: true, disableHdr: true, cardScale: 1.25, serverCache: {kept: true}}));
}));

test('a durable save refusal keeps the committed row and publishes nothing', async () => records(async state => {
  const f = await fixture(), consumer = nativeDevicePreferences(f.context), seen: string[] = [];
  consumer.subscribe(() => seen.push('notified'));
  state.beforeSave = async () => {throw new Error('save refused');};
  await assert.rejects(consumer.write('forceStereo', true), /save refused/);
  assert.equal(consumer.read('forceStereo'), false);
  assert.deepEqual(seen, []);
  assert.equal(state.saves, 0);
  assert.equal(JSON.parse(state.raw!).forceStereo, false);
}));

test('a saved write never echoes the asked value over the committed reread', async () => records(async state => {
  const f = await fixture(), consumer = nativeDevicePreferences(f.context);
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>, load = runtime.loadQueueRecord;
  let reads = 0;
  runtime.loadQueueRecord = async (recordKey: string) => {assert.equal(recordKey, key); reads++; return reads === 2 ? JSON.stringify(device()) : state.raw;};
  try {
    const written = await consumer.write('forceStereo', true);
    assert.equal(written, false, 'the returned value is the source reread, not the asked value');
    assert.equal(JSON.parse(state.raw!).forceStereo, true, 'the durable save still committed the asked value');
    assert.equal(reads, 2);
  } finally {runtime.loadQueueRecord = load;}
}));

test('the same source notifies every page subscriber and unsubscribing never retires it', async () => records(async state => {
  const f = await fixture(), a = nativeDevicePreferences(f.context), b = nativeDevicePreferences(f.context), seen: string[] = [];
  assert.equal(a, b);
  const offA = a.subscribe(() => seen.push('a')), offB = b.subscribe(() => seen.push('b'));
  await a.write('disableHdr', true);
  assert.deepEqual(seen, ['a', 'b']);
  seen.length = 0; offA();
  await b.write('forceStereo', true);
  assert.deepEqual(seen, ['b']);
  seen.length = 0; offB();
  await a.write('forceStereo', false);
  assert.deepEqual(seen, []);
  assert.equal(f.listeners.size, 0);
  assert.equal(nativeDeviceSettings(f.context), f.source);
  assert.throws(() => bindNativeDeviceSettings(f.context, f.ports), /already_bound/);
  assert.equal(state.saves, 3);
}));

test('unsupported and invalid writes never reach the installed source', async () => records(async state => {
  const f = await fixture(), consumer = nativeDevicePreferences(f.context), calls: unknown[] = [], write = f.source.write;
  f.source.write = async (id, value) => {calls.push([id, value]); return write(id, value);};
  try {
    for (const [id, value, expected] of [
      ['forceStereo', 'stereo', /Invalid setting value/],
      ['forceStereo', 'asis', /Invalid setting value/],
      ['disableHdr', 'yes', /Invalid setting value/],
      ['tvHdr', true, {code: 'unsupported'}],
      ['tvSurround', false, {code: 'unsupported'}],
      ['reduceMotion', true, {code: 'unsupported'}],
      ['textSize', 'large', {code: 'unsupported'}],
      ['layout', 'touch', {code: 'unsupported'}],
      ['libraryView', 'list', {code: 'unsupported'}],
    ] as const) await assert.rejects(consumer.write(id, value as string | boolean), expected, id);
    assert.deepEqual(calls, []);
    assert.equal(state.saves, 0);
  } finally {f.source.write = write;}
}));

test('authority retirement blocks late reads, writes and notifications', async () => records(async state => {
  const f = await fixture(), consumer = nativeDevicePreferences(f.context), seen: string[] = [];
  const off = consumer.subscribe(() => seen.push('late')), begun = deferred(), release = deferred();
  state.beforeRead = async () => {begun.resolve(); await release.promise;};
  const writing = consumer.write('forceStereo', true);
  await begun.promise;
  f.retire();
  release.resolve();
  await assert.rejects(writing, {name: 'AbortError'});
  assert.deepEqual(seen, []);
  assert.equal(state.saves, 0);
  assert.equal(JSON.parse(state.raw!).forceStereo, false);
  assert.throws(() => consumer.read('forceStereo'), {name: 'AbortError'});
  assert.throws(() => consumer.subscribe(() => {}), {name: 'AbortError'});
  await assert.rejects(consumer.write('disableHdr', true), {name: 'AbortError'});
  off();
}));

test('a replacement context gets its own source and facade, never the retired one', async () => records(async state => {
  const old = await fixture(), oldConsumer = nativeDevicePreferences(old.context);
  old.retire();
  assert.throws(() => oldConsumer.read('forceStereo'), {name: 'AbortError'});
  state.raw = JSON.stringify(device({forceStereo: true, disableHdr: true, cardScale: 1.25, serverCache: {kept: true}}));
  const next = await fixture(), nextConsumer = nativeDevicePreferences(next.context);
  assert.notEqual(nextConsumer, oldConsumer);
  assert.equal(nextConsumer.read('forceStereo'), true);
  assert.equal(nextConsumer.read('disableHdr'), true);
  assert.equal(await nextConsumer.write('forceStereo', false), false);
  assert.equal(nextConsumer.read('forceStereo'), false);
  assert.equal(JSON.parse(state.raw!).forceStereo, false);
  assert.throws(() => oldConsumer.read('forceStereo'), {name: 'AbortError'});
}));

function motionSnapshot(value: 'follow' | 'reduce', revision = 7, bound = true, stored = true): PreferenceSnapshot {
  const key = 'appearance.motion';
  return parsePreferenceSnapshot({deviceClass: 'mobile', deviceBound: bound, registryRevision: 'motion-row', registry: {revision: 'motion-row', fields: [
    {key, type: 'string', default: stored ? 'follow' : value, allowedValues: ['follow', 'reduce'], group: 'appearance', labelKey: `preferences.${key}`, scopes: ['profile-device']},
  ]}, documents: (bound ? ['profile-server', 'profile-device-class', 'profile-device'] : ['profile-server', 'profile-device-class']).map(scope => ({scope, revision, activeRevision: revision, digest: 'd'.repeat(64), values: scope === 'profile-device' && stored ? {[key]: value} : {}})),
  effective: {[key]: value}, effectiveSource: {[key]: bound && stored ? 'profile-device' : 'default'}, clampedFields: []});
}

async function motionFixture(initial = motionSnapshot('follow')) {
  const f = await fixture(); let snapshot = initial;
  const calls: {scope: string; deviceClass: string; revision: number; values: PreferencePatch}[] = [];
  let respond: (values: PreferencePatch) => Promise<PreferenceSnapshot> = async values => motionSnapshot(values['appearance.motion'] === 'reduce' ? 'reduce' : 'follow', snapshot.documents[0]!.revision + 1);
  Object.assign(f.context, {preferences: () => snapshot});
  const source = bindNativeServerPreferences(f.context, {
    read: async () => snapshot,
    apply: async (scope, deviceClass, revision, values) => {calls.push({scope, deviceClass, revision, values}); return respond(values);},
    accept: next => {snapshot = next; for (const listener of [...f.listeners]) listener(); return snapshot;},
  });
  return {...f, server: source, calls, respond: (next: typeof respond) => {respond = next;}, replace: (next: PreferenceSnapshot) => {snapshot = next;}};
}

test('motion borrows canonical profile-device values and waits for the exact CAS acknowledgement before rereading', async () => records(async state => {
  const f = await motionFixture(motionSnapshot('reduce', 7, true, false)), consumer = nativeDevicePreferences(f.context);
  assert.equal(consumer.read('reduceMotion'), true, 'effective default is not confused with a missing local override');
  assert.equal(nativeServerPreferences(f.context), f.server);
  const begun = deferred(), ack = deferred(), seen: boolean[] = [];
  const off = consumer.subscribe(() => seen.push(consumer.read('reduceMotion') === true));
  f.respond(async () => {begun.resolve(); await ack.promise; return motionSnapshot('follow', 8);});
  let settled = false;
  const writing = consumer.write('reduceMotion', true).then(value => {settled = true; return value;});
  await begun.promise;
  assert.deepEqual(f.calls, [{scope: 'profile-device', deviceClass: 'mobile', revision: 7, values: {'appearance.motion': 'reduce'}}]);
  assert.equal(settled, false); assert.deepEqual(seen, []); assert.equal(consumer.read('reduceMotion'), true);
  ack.resolve();
  assert.equal(await writing, false, 'return actual committed effective value, never echo the requested true');
  assert.deepEqual(seen, [false]); assert.equal(consumer.read('reduceMotion'), false);
  assert.equal(await consumer.write('forceStereo', true), true); assert.equal(consumer.read('reduceMotion'), false);
  assert.equal(state.saves, 1, 'only the separate forceStereo change writes the installation record');
  off(); assert.equal(f.listeners.size, 0); assert.equal(nativeServerPreferences(f.context), f.server);
}));

test('motion uses the latest revision, maps false to follow and retains confirmed state on refusal', async () => records(async state => {
  const f = await motionFixture(motionSnapshot('reduce')), consumer = nativeDevicePreferences(f.context);
  f.replace(motionSnapshot('reduce', 12));
  assert.equal(await consumer.write('reduceMotion', false), false);
  assert.deepEqual(f.calls[0], {scope: 'profile-device', deviceClass: 'mobile', revision: 12, values: {'appearance.motion': 'follow'}});
  let notifications = 0; const off = consumer.subscribe(() => {notifications++;});
  f.respond(async () => {throw new Error('motion CAS refused');});
  await assert.rejects(consumer.write('reduceMotion', true), /motion CAS refused/);
  assert.equal(consumer.read('reduceMotion'), false); assert.equal(notifications, 0); assert.equal(state.saves, 0);
  await assert.rejects(consumer.write('reduceMotion', 'reduce'), /Invalid setting value/);
  assert.equal(f.calls.length, 2, 'invalid input never reaches the canonical writer'); off();
}));

test('motion fails closed without the actual bound row and never changes installation preferences', async () => records(async state => {
  const f = await motionFixture(motionSnapshot('follow', 7, false, false)), consumer = nativeDevicePreferences(f.context);
  assert.equal(consumer.read('reduceMotion'), undefined);
  await assert.rejects(consumer.write('reduceMotion', true), {code: 'unsupported'});
  const full = motionSnapshot('follow');
  f.replace({...full, registry: {...full.registry, fields: []}});
  assert.equal(consumer.read('reduceMotion'), undefined);
  await assert.rejects(consumer.write('reduceMotion', false), {code: 'unsupported'});
  assert.deepEqual(f.calls, []); assert.equal(state.saves, 0);
}));

test('motion late acknowledgement cannot publish or return a value after authority retirement', async () => records(async state => {
  const f = await motionFixture(), consumer = nativeDevicePreferences(f.context), begun = deferred(), ack = deferred();
  let notifications = 0; const off = consumer.subscribe(() => {notifications++;});
  f.respond(async () => {begun.resolve(); await ack.promise; return motionSnapshot('reduce', 8);});
  const writing = consumer.write('reduceMotion', true); await begun.promise; f.retire(); ack.resolve();
  await assert.rejects(writing, {name: 'AbortError'});
  assert.throws(() => consumer.read('reduceMotion'), {name: 'AbortError'});
  await assert.rejects(consumer.write('reduceMotion', false), {name: 'AbortError'});
  assert.equal(notifications, 0); assert.equal(f.context.preferences().effective['appearance.motion'], 'follow'); assert.equal(state.saves, 0); off();
}));

function textSizeSnapshot(value: 'standard' | 'large', revision: number): PreferenceSnapshot {
  const key = 'appearance.textSize';
  return parsePreferenceSnapshot({deviceClass: 'television', deviceBound: true, registryRevision: 'text-size-row', registry: {revision: 'text-size-row', fields: [
    {key, type: 'string', default: 'standard', allowedValues: ['standard', 'large'], group: 'appearance', labelKey: `preferences.${key}`, scopes: ['profile-device']},
  ]}, documents: ['profile-server', 'profile-device-class', 'profile-device'].map(scope => ({scope, revision, activeRevision: revision, digest: 'd'.repeat(64), values: scope === 'profile-device' ? {[key]: value} : {}})),
    effective: {[key]: value}, effectiveSource: {[key]: 'profile-device'}, clampedFields: []});
}

test('TV text-size row borrows the same canonical source and returns its durable committed choice', async () => records(async state => {
  const f = await motionFixture(textSizeSnapshot('standard', 21)), consumer = nativeDevicePreferences(f.context), begun = deferred(), ack = deferred();
  assert.equal(consumer.read('textSize'), 'standard'); assert.equal(consumer.read('reduceMotion'), undefined);
  let notifications = 0; const off = consumer.subscribe(() => {notifications++;});
  f.respond(async () => {begun.resolve(); await ack.promise; return textSizeSnapshot('large', 22);});
  let settled = false; const writing = consumer.write('textSize', 'large').then(value => {settled = true; return value;});
  await begun.promise;
  assert.deepEqual(f.calls[0], {scope: 'profile-device', deviceClass: 'television', revision: 21, values: {'appearance.textSize': 'large'}});
  assert.equal(settled, false); assert.equal(consumer.read('textSize'), 'standard'); assert.equal(notifications, 0);
  ack.resolve(); assert.equal(await writing, 'large'); assert.equal(consumer.read('textSize'), 'large'); assert.equal(notifications, 1);
  f.respond(async () => textSizeSnapshot('large', 23));
  assert.equal(await consumer.write('textSize', 'standard'), 'large', 'the actual committed reread wins over the requested choice');
  assert.deepEqual(f.calls[1], {scope: 'profile-device', deviceClass: 'television', revision: 22, values: {'appearance.textSize': 'standard'}});
  const confirmed = f.server.getSnapshot();
  f.respond(async () => {throw new Error('text-size CAS refused');});
  await assert.rejects(consumer.write('textSize', 'standard'), /text-size CAS refused/);
  assert.equal(f.server.getSnapshot(), confirmed); assert.equal(consumer.read('textSize'), 'large');
  for (const invalid of [true, false, 'huge']) await assert.rejects(consumer.write('textSize', invalid), /Invalid setting value/);
  assert.equal(f.calls.length, 3, 'invalid choices do not reach the writer');
  assert.equal(state.saves, 0, 'appearance never adds an installation-record field');
  assert.equal(nativeServerPreferences(f.context), f.server); off();
  assert.equal(f.listeners.size, 0); assert.equal(nativeServerPreferences(f.context), f.server);
  f.retire(); assert.throws(() => consumer.read('textSize'), {name: 'AbortError'});
  await assert.rejects(consumer.write('textSize', 'standard'), {name: 'AbortError'});
}));
