import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {device, emit, NativeEventEmitter, NativeModules} from './react-native-stub.mjs';
import {OfflineDownloads, type Viewer} from '../platform/offline-downloads';

const viewer: Viewer = {authority: 'local', accountId: 'account-a', profileId: 'profile-a', serverId: 'server-a'};
const otherViewer: Viewer = {...viewer, profileId: 'profile-b'};
const publicKey = Buffer.alloc(32).toString('base64url');
const native = NativeModules.PorticoDownloads;
const network = NativeModules.PorticoNetwork as unknown as {verify(key: string, signature: string, payload: string): Promise<boolean>};
const stores: OfflineDownloads[] = [];
const turn = () => new Promise<void>(resolve => setImmediate(resolve));
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {resolve = done;});
  return {promise, resolve};
}
function receipt(id: string, revision = 1, signatureByte = 0, owner = viewer) {
  const claims = {
    kind: 'portico.download-receipt', version: '1', receiptId: 'receipt-' + id, keyId: 'key-a',
    viewer: owner, itemId: 'item-' + id, preparationId: id, quality: 'original', qualityLabel: 'Original',
    artifact: {sha256: 'a'.repeat(64), bytes: 10},
    issuedAt: '2026-01-01T00:00:00.000Z', expiresAt: `2099-01-${String(revision).padStart(2, '0')}T00:00:00.000Z`,
  };
  return {receiptId: claims.receiptId, algorithm: 'ed25519', keyId: claims.keyId,
    payload: Buffer.from(JSON.stringify(claims)).toString('base64url'),
    signature: Buffer.alloc(64, signatureByte).toString('base64url'), claims, revision};
}
function entry(id: string, signed = receipt(id)) {
  return {id, itemId: signed.claims.itemId, state: 'ready', bytes: 10, bytesDone: 10,
    meta: JSON.stringify({receipt: signed, owner: JSON.stringify(Object.values(signed.claims.viewer))})};
}
function changed(id: string, signed = receipt(id)) {emit('PorticoDownloadChanged', entry(id, signed));}
function fresh() {
  device.reset();
  device.state = JSON.stringify({version: 3, keys: {'key-a': publicKey}, clockFloor: Date.now()});
  const store = new OfflineDownloads(); stores.push(store);
  return store;
}
function cache(store: OfflineDownloads) {
  // Retained signed payloads and outstanding bridge work are the memory/performance outcomes
  // under test; no diagnostic API is added to the production controller just to expose them.
  return store as unknown as {verified: Set<string>; verifying: Map<string, Promise<boolean>>};
}
function waitFor(store: OfflineDownloads, predicate: () => boolean) {
  if (predicate()) return Promise.resolve();
  return new Promise<void>(resolve => {
    const stop = store.subscribe(() => {if (predicate()) {stop(); resolve();}});
  });
}
afterEach(() => {for (const store of stores.splice(0)) store.dispose(); device.reset();});

test('a 100-receipt renewal burst verifies each signed identity once, serially, and publishes two coherent snapshots', {timeout: 5000}, async t => {
  let calls = 0, outstanding = 0, peak = 0;
  t.mock.method(network, 'verify', async () => {
    calls++; outstanding++; peak = Math.max(peak, outstanding);
    await turn(); outstanding--; return true;
  });
  const store = fresh(), ids = Array.from({length: 100}, (_, i) => 'download-' + i);
  for (const id of ids) device.entries.set(id, entry(id));
  store.adopt(viewer); await store.open();
  assert.equal(calls, 100);
  calls = 0; peak = 0;
  let publications = 0;
  store.subscribe(() => {publications++;});
  const renewed = new Map(ids.map(id => [id, receipt(id, 2)]));
  const finished = waitFor(store, () => store.getSnapshot().entries.length === 100 && store.getSnapshot().entries.every(value =>
    value.receipt?.payload === renewed.get(value.id)?.payload && store.playabilityReason(value) === null));
  for (const id of ids) changed(id, renewed.get(id)!);
  await finished; await turn();
  assert.equal(calls, 100, 'one bridge verification per renewed signed payload');
  assert.equal(peak, 1, 'a renewal cannot flood the native crypto bridge');
  assert.equal(publications, 2, 'one snapshot for the burst, then one for completed verification');
  assert.equal(cache(store).verified.size, 100);
  assert.equal(cache(store).verifying.size, 0);
});

test('a changed signature under the same receipt id never inherits verified playability', {timeout: 5000}, async t => {
  let calls = 0;
  t.mock.method(network, 'verify', async (_key: string, signature: string) => {calls++; return Buffer.from(signature, 'base64')[0] === 0;});
  const store = fresh(); device.entries.set('one', entry('one'));
  store.adopt(viewer); await store.open();
  assert.equal(store.playabilityReason(store.getSnapshot().entries[0]!), null);
  changed('one', receipt('one', 1, 1));
  await turn();
  assert.equal(calls, 2);
  assert.equal(store.playabilityReason(store.getSnapshot().entries[0]!), 'needs_setup');
  assert.equal(cache(store).verified.size, 0, 'the retired successful signature is pruned');
});

test('duplicate signed receipts share verification and stay cached until their last reference is removed', {timeout: 5000}, async t => {
  let calls = 0;
  t.mock.method(network, 'verify', async () => {calls++; return true;});
  const store = fresh(), signed = receipt('shared');
  device.entries.set('first', entry('first', signed)); device.entries.set('second', entry('second', signed));
  store.adopt(viewer); await store.open();
  assert.equal(calls, 1); assert.equal(cache(store).verified.size, 1);
  emit('PorticoDownloadChanged', {id: 'first', state: 'removed'}); await turn();
  assert.equal(cache(store).verified.size, 1);
  assert.equal(store.playabilityReason(store.getSnapshot().entries[0]!), null);
  emit('PorticoDownloadChanged', {id: 'second', state: 'removed'}); await turn();
  assert.equal(cache(store).verified.size, 0); assert.equal(store.getSnapshot().entries.length, 0);
});

test('successive renewals retain only the current signed payload', {timeout: 5000}, async t => {
  t.mock.method(network, 'verify', async () => true);
  const store = fresh(); store.adopt(viewer); await store.open();
  for (let revision = 1; revision <= 20; revision++) {
    const signed = receipt('one', revision);
    const finished = waitFor(store, () => {
      const current = store.getSnapshot().entries[0];
      return current?.receipt?.payload === signed.payload && store.playabilityReason(current) === null;
    });
    changed('one', signed); await finished;
    assert.equal(cache(store).verified.size, 1);
  }
  emit('PorticoDownloadChanged', {id: 'one', state: 'removed'}); await turn();
  assert.equal(cache(store).verified.size, 0);
});

test('removing an entry during verification cannot retain its late success', {timeout: 5000}, async t => {
  const gate = deferred<boolean>(), started = deferred<void>();
  let calls = 0;
  t.mock.method(network, 'verify', async () => {calls++; started.resolve(); return calls === 1 ? gate.promise : true;});
  const store = fresh(); store.adopt(viewer); await store.open();
  changed('one'); await started.promise;
  emit('PorticoDownloadChanged', {id: 'one', state: 'removed'});
  gate.resolve(true); await turn();
  assert.equal(cache(store).verified.size, 0);
  assert.equal(cache(store).verifying.size, 0);
  assert.equal(store.getSnapshot().entries.length, 0);
  const finished = waitFor(store, () => store.getSnapshot().entries.some(value => store.playabilityReason(value) === null));
  changed('one'); await finished;
  assert.equal(calls, 2, 're-adding the same signed content requires a fresh check after retirement');
});

test('a replacement received during a pending sweep is verified and never accepts the retired payload', {timeout: 5000}, async t => {
  const gate = deferred<boolean>(), started = deferred<void>();
  let calls = 0;
  t.mock.method(network, 'verify', async () => {calls++; started.resolve(); return calls === 1 ? gate.promise : false;});
  const store = fresh(); store.adopt(viewer); await store.open();
  changed('one'); await started.promise;
  changed('one', receipt('one', 2));
  gate.resolve(true); await turn();
  assert.equal(calls, 2);
  assert.equal(cache(store).verified.size, 0);
  assert.equal(store.getSnapshot().entries[0]!.receipt?.claims.expiresAt, receipt('one', 2).claims.expiresAt);
  assert.equal(store.playabilityReason(store.getSnapshot().entries[0]!), 'needs_setup');
});

test('a failed signature in a live batch is not retried for every later event', {timeout: 5000}, async t => {
  const gate = deferred<boolean>(), started = deferred<void>();
  let calls = 0;
  t.mock.method(network, 'verify', async () => {calls++; started.resolve(); return calls === 1 ? gate.promise : false;});
  const store = fresh(); store.adopt(viewer); await store.open();
  changed('first'); await started.promise;
  for (let i = 0; i < 30; i++) changed('next-' + i);
  gate.resolve(false); await turn();
  assert.equal(calls, 31);
  assert.equal(cache(store).verified.size, 0);
  assert.equal(store.getSnapshot().entries.length, 31);
  assert.ok(store.getSnapshot().entries.every(value => store.playabilityReason(value) === 'needs_setup'));
});

test('a profile change during signature checking cannot publish or authorize the prior viewer as the new viewer', {timeout: 5000}, async t => {
  const gate = deferred<boolean>(), started = deferred<void>();
  t.mock.method(network, 'verify', async () => {started.resolve(); return gate.promise;});
  const store = fresh(); store.adopt(viewer); await store.open();
  changed('one'); await started.promise;
  const prior = store.getSnapshot().entries[0]!;
  store.adopt(otherViewer); gate.resolve(true); await turn();
  assert.equal(store.getSnapshot().entries.length, 0);
  assert.equal(store.playabilityReason(prior), 'other_profile');
  store.adopt(viewer);
  assert.equal(store.playabilityReason(store.getSnapshot().entries[0]!), null, 'signature truth is device-wide; viewer authorization remains separate');
});

test('disposal prevents late crypto completion from republishing or repopulating caches', {timeout: 5000}, async t => {
  const gate = deferred<boolean>(), started = deferred<void>();
  t.mock.method(network, 'verify', async () => {started.resolve(); return gate.promise;});
  const store = fresh(); store.adopt(viewer); await store.open();
  changed('one'); await started.promise;
  const snapshot = store.getSnapshot();
  store.dispose(); gate.resolve(true); await turn();
  changed('two'); await turn();
  assert.equal(store.getSnapshot(), snapshot);
  assert.equal(cache(store).verified.size, 0); assert.equal(cache(store).verifying.size, 0);
});

test('failed list attempts remove their listeners and a successful retry handles each event once', {timeout: 5000}, async t => {
  const add = NativeEventEmitter.prototype.addListener;
  let active = 0, attempts = 0;
  t.mock.method(NativeEventEmitter.prototype, 'addListener', function (this: NativeEventEmitter, name: string, callback: (entry: any) => void) {
    const subscription = add.call(this, name, callback); active++;
    let removed = false;
    return {remove() {if (!removed) {removed = true; active--; subscription.remove();}}};
  });
  t.mock.method(native, 'list', async () => {if (++attempts <= 3) throw new Error('unreadable store'); return [];});
  const store = fresh();
  for (let i = 0; i < 3; i++) {await assert.rejects(store.open(), /unreadable store/); assert.equal(active, 0);}
  await store.open(); store.adopt(viewer);
  assert.equal(active, 1);
  let publications = 0; store.subscribe(() => {publications++;});
  emit('PorticoDownloadChanged', {id: 'one', state: 'paused', intent: 'hold'}); await turn();
  assert.equal(publications, 1);
  store.dispose(); assert.equal(active, 0);
});

test('events delivered while list is pending override its older snapshot, including removals', {timeout: 5000}, async t => {
  const gate = deferred<any[]>(), started = deferred<void>();
  t.mock.method(native, 'list', () => {started.resolve(); return gate.promise;});
  t.mock.method(network, 'verify', async () => true);
  const store = fresh(); store.adopt(viewer);
  const opening = store.open(); await started.promise;
  changed('one', receipt('one', 2));
  emit('PorticoDownloadChanged', {id: 'one', state: 'invalid'});
  emit('PorticoDownloadChanged', {id: 'two', state: 'removed'});
  gate.resolve([null, {id: 7, state: 'ready'}, entry('one'), entry('two')]); await opening;
  assert.equal(store.getSnapshot().entries.length, 1);
  assert.equal(store.getSnapshot().entries[0]!.receipt?.claims.expiresAt, receipt('one', 2).claims.expiresAt);
  assert.equal(store.playabilityReason(store.getSnapshot().entries[0]!), null);
});

test('disposal while list is pending removes the listener and cannot resurrect ready state', {timeout: 5000}, async t => {
  const gate = deferred<any[]>(), started = deferred<void>();
  let active = 0;
  const add = NativeEventEmitter.prototype.addListener;
  t.mock.method(NativeEventEmitter.prototype, 'addListener', function (this: NativeEventEmitter, name: string, callback: (entry: any) => void) {
    const subscription = add.call(this, name, callback); active++;
    let removed = false;
    return {remove() {if (!removed) {removed = true; active--; subscription.remove();}}};
  });
  t.mock.method(native, 'list', () => {started.resolve(); return gate.promise;});
  const store = fresh(), opening = store.open(); await started.promise;
  assert.equal(active, 1); store.dispose(); assert.equal(active, 0);
  const snapshot = store.getSnapshot();
  gate.resolve([entry('one')]); await opening;
  assert.equal(store.getSnapshot(), snapshot); assert.equal(snapshot.ready, false);
  emit('PorticoDownloadChanged', entry('one')); await turn();
  assert.equal(store.getSnapshot(), snapshot);
});
