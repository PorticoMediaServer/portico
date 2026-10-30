import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {createHash, createPublicKey, generateKeyPairSync, sign, verify} from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {offlineModule} from './helpers/offline-module.mjs';
import {device, emit, NativeModules, NativeEventEmitter, Platform} from './react-native-stub.mjs';

// This work package omits generated contracts and i18n. Those decoder/catalogue
// boundaries are explicit fixtures; the actual receipt parser, signature checks,
// publication binding, device persistence and reading coordinator all execute.
const services = {serviceText: (id: string) => id};
const core = await offlineModule(new URL('../../../packages/client-core/src/downloads.ts', import.meta.url), {
  '../../contracts/src/types.ts': {canonicalQualityLadder: []},
  '../../contracts/src/routes.ts': {
    routePrefix: () => {throw new Error('unexpected artifact route');},
    encodedRoutePath: () => {throw new Error('unexpected artifact route');},
  },
  './presentation/service-text.ts': services,
});
const progressResponse = (raw: any) => {
  assert.ok(raw && Object.hasOwn(raw, 'progress'));
  return structuredClone(raw);
};
const contracts = {decodeGetReadingSessionsSessionIdProgressResponse: progressResponse,
  decodePutReadingSessionsSessionIdProgressResponse: progressResponse};
const key = generateKeyPairSync('ed25519');
const rawKey = key.publicKey.export({type: 'spki', format: 'der'}).subarray(-32);
const routeCrypto = {
  random: (length: number) => new Uint8Array(length),
  sha256: async (value: Uint8Array) => new Uint8Array(createHash('sha256').update(value).digest()),
  verify: async (publicKey: Uint8Array, signature: Uint8Array, payload: Uint8Array) => verify(null, payload,
    createPublicKey({key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), publicKey]), type: 'spki', format: 'der'}), signature),
};
const native = NativeModules.PorticoDownloads as Record<string, any>;
const boundary = {NativeModules, NativeEventEmitter, Platform};
for (const name of ['publicationOpen', 'publicationRead', 'publicationMember', 'publicationMemberRead']) native[name] = async () => {throw new Error('unexpected publication byte read');};
native.publicationCancel = () => {};
native.publicationClose = () => {};
native.removeGeneration = async (id: string, generation: string) => {
  assert.equal(device.entries.get(id)?.publicationGeneration, generation);
  await native.remove(id);
};
const baseRemove = native.remove;
native.remove = async (id: string) => {await baseRemove(id); emit('PorticoDownloadChanged', {id, state: 'removed'});};
const baseWrite = native.writeState;
const reading = await offlineModule(new URL('../platform/offline-reading.ts', import.meta.url), {
  'react-native': boundary,
  '../../../packages/contracts/src': contracts,
  '../../../packages/client-core/src/reading': {ReadingError: class extends Error {}, parseReadingManifest: (value: unknown) => value},
  '../src-next/platform/server-connections': {nativeRouteCrypto: routeCrypto},
});
const subtitles = await offlineModule(new URL('../platform/offline-subtitle-files.ts', import.meta.url));
const clock = await offlineModule(new URL('../platform/receipt-clock.ts', import.meta.url));
const units = await offlineModule(new URL('../../../packages/client-core/src/wire-units.ts', import.meta.url));
const target = process.env.PORTICO_OFFLINE_TEST_TARGET ? pathToFileURL(process.env.PORTICO_OFFLINE_TEST_TARGET) : new URL('../platform/offline-downloads.ts', import.meta.url);
const {OfflineDownloads, offlineOwnerKey} = await offlineModule(target, {
  './offline-reading': reading,
  '../../../packages/contracts/src': contracts,
  './offline-subtitle-files': subtitles,
  './receipt-clock': clock,
  'react-native': boundary,
  '../src-next/platform/server-connections': {nativeRouteCrypto: routeCrypto},
  '../../../packages/client-core/src/index': {...core, DownloadsService: class {}, getDownloadRequest: () => {throw new Error('unexpected container request');}},
  '../../../packages/client-core/src/presentation/service-text': services,
  '../../../packages/client-core/src/wire-units': units,
});

const A = {authority: 'local', accountId: 'account', profileId: 'profile-a', serverId: 'server'};
const B = {...A, profileId: 'profile-b'};
const sourceRevision = 'a'.repeat(64), documentSha256 = 'b'.repeat(64), artifactSha256 = 'c'.repeat(64);
const at = Date.now() - 10000;
function record(id: string, pending = true, viewer = A, keyId = 'key') {
  const publication = {sourceId: 'source-' + id, revision: sourceRevision, format: 'pdf', documentSha256};
  const claims = {kind: 'portico.download-receipt', version: '1', receiptId: 'receipt-' + id, keyId,
    viewer, itemId: 'item-' + id, preparationId: id, quality: 'original', qualityLabel: 'Original',
    artifact: {sha256: artifactSha256, bytes: 10}, publication,
    issuedAt: '2026-01-01T00:00:00.000Z', expiresAt: '2099-01-01T00:00:00.000Z'};
  const bytes = Buffer.from(JSON.stringify(claims));
  const receipt = core.decodeDownloadReceipt({receiptId: claims.receiptId, algorithm: 'ed25519', keyId,
    payload: bytes.toString('base64url'), signature: sign(null, bytes, key.privateKey).toString('base64url'), claims, revision: 1}).receipt;
  const location = {format: 'pdf', pageIndex: 7, progression: .2};
  return {itemId: claims.itemId, sourceId: publication.sourceId, revision: sourceRevision, documentSha256,
    artifactSha256, artifactBytes: 10, receipt, serverRevision: 1,
    progress: {progress: {sourceId: publication.sourceId, sourceRevision, location, revision: 2, observedAt: at, stale: false}},
    ...(pending ? {pending: {id: 'observation-' + id, observedAt: at, startedAt: at - 1000, location}} : {})};
}
const active = new Set<any>();
afterEach(() => {
  for (const store of active) store.dispose(); active.clear();
  native.writeState = baseWrite;
  device.reset();
});
function stored(records: Record<string, any>, viewer = A) {
  return {version: 3, settings: {wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0},
    reading: {[offlineOwnerKey(viewer)]: records}, plays: {}, resume: {}, keys: {key: rawKey.toString('base64url')},
    wanted: {}, wantedPublications: {}, nextAfter: {}, renewed: {}, clockFloor: Date.now()};
}
async function open(records: Record<string, any>, viewer = A, recordOwner = viewer) {
  device.reset();
  device.state = JSON.stringify(stored(records, recordOwner));
  for (const [id, value] of Object.entries(records)) device.seed({id, itemId: value.itemId, bytes: 10, bytesDone: 10,
    publicationGeneration: 'native-' + id, meta: {receipt: value.receipt, kind: 'ebook'}});
  const store = new OfflineDownloads(); active.add(store); store.adopt(viewer); await store.open();
  return store;
}
const read = (store: any, owner = A) => Reflect.get(store, 'stored').reading[offlineOwnerKey(owner)] as Record<string, any>;
const sync = (store: any) => Reflect.get(store, 'syncReadingProgress').call(store) as Promise<void>;
const saved = (viewer = A) => JSON.parse(device.state).reading[offlineOwnerKey(viewer)] as Record<string, any>;
function applied(body: any) {
  return {outcome: 'applied', saved: true, progress: {sourceId: body.receipt.claims.publication.sourceId,
    sourceRevision, location: body.location, revision: body.expectedRevision + 1, observedAt: body.observedAt, stale: false}};
}
function server(store: any, handler: (body: any) => unknown = applied) {
  const calls: {path: string; body: any}[] = [];
  Reflect.set(store, 'api', {baseUrl: 'http://server.test', mediaUrl: (path: string) => 'http://server.test' + path,
    request: async (path: string, _method: string, body: unknown) => {
      calls.push({path, body});
      if (path === '/v1/downloads/receipts/revoke') return {};
      assert.equal(path, '/v1/reading/offline/progress'); return handler(body);
    }});
  return calls;
}
const pendingOf = (store: any, owner = A) => Object.values(read(store, owner)).filter(value => value.pending);
function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};}

test('OFF-11: settled records do not hide the 101st pending book', async () => {
  const records = Object.fromEntries(Array.from({length: 100}, (_, index) => ['settled-' + index, record('settled-' + index, false)]));
  records.later = record('later');
  const store = await open(records), calls = server(store);
  await sync(store); await sync(store);
  assert.deepEqual(calls.map(call => call.body.receipt.claims.itemId), ['item-later']);
  assert.equal(pendingOf(store).length, 0); assert.equal(saved().later.pending, undefined);
});

test('OFF-11: successive pending batches and records added during a pass all drain', async () => {
  const records = Object.fromEntries(Array.from({length: 150}, (_, index) => ['pending-' + index, record('pending-' + index)]));
  const store = await open(records);
  const calls = server(store, body => {read(store).newer ??= record('newer'); return applied(body);});
  await sync(store);
  assert.equal(calls.length, 151); assert.equal(new Set(calls.map(call => call.body.receipt.claims.itemId)).size, 151);
  assert.equal(pendingOf(store).length, 0);
});

test('OFF-11: unavailable signing keys cannot starve a later verifiable book or spin', async () => {
  const records = Object.fromEntries(Array.from({length: 100}, (_, index) => ['unknown-' + index, record('unknown-' + index, true, A, 'unknown')]));
  records.later = record('later');
  const store = await open(records), calls = server(store);
  await sync(store);
  assert.deepEqual(calls.map(call => call.body.receipt.claims.itemId), ['item-later']);
  assert.equal(pendingOf(store).length, 100);
});

for (const [status, code] of [[409, 'revision_mismatch'], [404, 'not_found']] as const) {
  test(`OFF-12: ${status} blocks only that observation and survives restart`, async () => {
    const store = await open({first: record('first'), later: record('later')});
    const calls = server(store, body => {
      if (body.receipt.claims.itemId === 'item-first') throw Object.assign(new Error(code), {status, code});
      return applied(body);
    });
    await sync(store); await sync(store);
    assert.deepEqual(calls.map(call => call.body.receipt.claims.itemId), ['item-first', 'item-later']);
    assert.equal(read(store).first.syncBlocked, code); assert.ok(read(store).first.pending);
    assert.equal(saved().first.syncBlocked, code); assert.equal(saved().later.pending, undefined);
    const restarted = new OfflineDownloads(); active.add(restarted); restarted.adopt(A); await restarted.open();
    const retry = server(restarted); await sync(restarted);
    assert.deepEqual(retry, []); assert.ok(read(restarted).first.pending);
  });
}

for (const failure of [new TypeError('network unavailable'), Object.assign(new Error('sign in'), {status: 401, code: 'unauthorized'})]) {
  test(`OFF-12: ${failure.name === 'TypeError' ? 'transport' : 'authentication'} stops the pass without acknowledging or blocking books`, async () => {
    const store = await open({first: record('first'), later: record('later')});
    let refusing = true;
    const calls = server(store, body => {if (refusing) throw failure; return applied(body);});
    await sync(store);
    assert.equal(calls.length, 1); assert.equal(pendingOf(store).length, 2);
    assert.equal(read(store).first.syncBlocked, undefined);
    refusing = false; await sync(store);
    assert.equal(calls.length, 3); assert.equal(pendingOf(store).length, 0);
  });
}

test('OFF-12: an old refusal cannot block a newer deliberate observation', async () => {
  const store = await open({book: record('book')});
  let attempted = false;
  const calls = server(store, body => {
    if (!attempted) {
      attempted = true;
      const previous = read(store).book;
      read(store).book = {...previous, pending: {...previous.pending, id: 'new-observation', observedAt: at + 1}};
      throw Object.assign(new Error('old source refusal'), {status: 409, code: 'revision_mismatch'});
    }
    return applied(body);
  });
  await sync(store);
  assert.equal(calls.length, 2); assert.equal(calls[1].body.observedAt, at + 1);
  assert.equal(read(store).book.pending, undefined); assert.equal(read(store).book.syncBlocked, undefined);
});

test('OFF-12: an explicitly revoked receipt affects one book, while a global forbidden response stops the pass', async () => {
  const store = await open({first: record('first'), later: record('later')});
  const calls = server(store, body => {
    if (body.receipt.claims.itemId === 'item-first') throw Object.assign(new Error('revoked'), {status: 403, code: 'receipt_revoked'});
    return applied(body);
  });
  await sync(store);
  assert.equal(calls.length, 2); assert.equal(read(store).first.syncBlocked, 'receipt_revoked');
  assert.equal(read(store).later.pending, undefined);
  const other = await open({first: record('first'), later: record('later')});
  const refused = server(other, () => {throw Object.assign(new Error('forbidden'), {status: 403, code: 'forbidden'});});
  await sync(other);
  assert.equal(refused.length, 1); assert.equal(pendingOf(other).length, 2); assert.equal(read(other).first.syncBlocked, undefined);
});

test('reading sync cannot acknowledge or continue an old owner after a viewer change', async () => {
  const store = await open({first: record('first'), later: record('later')});
  const calls = server(store, body => {store.adopt(B); return applied(body);});
  await sync(store);
  assert.equal(calls.length, 1); assert.equal(pendingOf(store, A).length, 2);
  assert.equal(store.getSnapshot().entries.length, 0);
});

test('OFF-13: a permanently refused observation does not hold a deliberate removal forever', async () => {
  const store = await open({book: record('book')});
  const calls = server(store, body => {
    if (body.receipt) throw Object.assign(new Error('revision_mismatch'), {status: 409, code: 'revision_mismatch'});
    return applied(body);
  });
  await sync(store);
  assert.equal(read(store).book.syncBlocked, 'revision_mismatch');
  await store.remove(store.getSnapshot().entries[0]);
  assert.deepEqual(device.removed, ['book']); assert.equal(saved().book, undefined);
  assert.deepEqual(calls.map(call => call.path), ['/v1/reading/offline/progress', '/v1/downloads/receipts/revoke']);
});

test('OFF-13: offline removal preserves the file and its pending evidence across restart', async () => {
  const store = await open({book: record('book')});
  await assert.rejects(store.remove(store.getSnapshot().entries[0]), {code: 'reading_progress_pending'});
  assert.deepEqual(device.removed, []); assert.ok(device.entries.has('book')); assert.ok(saved().book.pending);
  const restarted = new OfflineDownloads(); active.add(restarted); restarted.adopt(A); await restarted.open();
  const calls = server(restarted); await sync(restarted);
  assert.equal(calls.length, 1); assert.equal(read(restarted).book.pending, undefined);
});

test('OFF-13: online removal waits for reading sync and fences new reader sessions before deleting or revoking', async () => {
  const store = await open({book: record('book')}), entry = store.getSnapshot().entries[0];
  const lease = store.publicationLease(entry, {signal: new AbortController().signal, current: () => true});
  const waiting = deferred<any>(), requested = deferred<void>();
  const calls = server(store, body => {requested.resolve(); return waiting.promise.then(() => applied(body));});
  const removal = store.remove(entry); await requested.promise;
  assert.deepEqual(device.removed, []); assert.equal(calls.length, 1);
  assert.equal(lease.current(), false);
  assert.throws(() => store.publicationLease(entry, {signal: new AbortController().signal, current: () => true}), /reading_unavailable/);
  waiting.resolve(undefined); await removal;
  assert.deepEqual(device.removed, ['book']); assert.equal(saved().book, undefined);
  assert.deepEqual(calls.map(call => call.path), ['/v1/reading/offline/progress', '/v1/downloads/receipts/revoke']);
  lease.dispose();
});

test('OFF-13: failed durable acknowledgement refuses removal and preserves the pending record', async () => {
  const store = await open({book: record('book')}), calls = server(store);
  native.writeState = async () => {throw new Error('disk full');};
  await assert.rejects(store.remove(store.getSnapshot().entries[0]), {code: 'reading_progress_pending'});
  assert.deepEqual(device.removed, []); assert.ok(read(store).book.pending); assert.ok(saved().book.pending);
  assert.equal(calls.length, 1); assert.equal(store.getSnapshot().error, 'downloads.deviceUnwritable');
  assert.equal(store.getSnapshot().pendingProgress, 1);
});

test('OFF-13: a concurrent failed withdrawal cannot release another removal\'s reader fence', async () => {
  const store = await open({book: record('book')}), entry = store.getSnapshot().entries[0];
  const waiting = deferred<any>(), requested = deferred<void>();
  server(store, body => {requested.resolve(); return waiting.promise.then(() => applied(body));});
  const removal = store.remove(entry); await requested.promise;
  const originalRemove = native.remove;
  try {
    native.remove = async () => {throw new Error('native write failed');};
    await assert.rejects(store.remove(entry, true), /native write failed/);
    assert.throws(() => store.publicationLease(entry, {signal: new AbortController().signal, current: () => true}), /reading_unavailable/);
  } finally {native.remove = originalRemove; waiting.resolve(undefined);}
  await removal;
  assert.deepEqual(device.removed, ['book']);
});

test('OFF-13: removal tries to sync first; a permanent refusal then lets the file go while other progress syncs', async () => {
  const store = await open({first: record('first'), later: record('later')});
  server(store, body => {
    if (body.receipt.claims.itemId === 'item-first') throw Object.assign(new Error('changed'), {code: 'revision_mismatch', status: 409});
    return applied(body);
  });
  await store.remove(store.getSnapshot().entries.find((entry: any) => entry.id === 'first'));
  assert.deepEqual(device.removed, ['first']); assert.equal(saved().first, undefined); assert.equal(saved().later.pending, undefined);
});

test('OFF-13: both other-profile cleanup paths preflight pending reading before any file is deleted', async () => {
  const store = await open({settled: record('settled', false, B), pending: record('pending', true, B)}, A, B);
  const calls = server(store);
  await assert.rejects(store.removeOthers(), {code: 'reading_progress_pending'});
  const snapshot = store.getSnapshot();
  await assert.rejects(store.removeOthersConfirmed({revision: snapshot.othersRevision, bytes: snapshot.otherBytes}), {code: 'reading_progress_pending'});
  assert.deepEqual(device.removed, []); assert.deepEqual(calls, []); assert.equal(device.entries.size, 2); assert.ok(saved(B).pending.pending);
});

test('OFF-13: a real server revocation still removes bytes immediately and retains blocked evidence', async () => {
  const store = await open({book: record('book')}), calls = server(store);
  await store.remove(store.getSnapshot().entries[0], true);
  assert.deepEqual(device.removed, ['book']); assert.deepEqual(calls, []);
  assert.ok(saved().book.pending); assert.equal(saved().book.syncBlocked, 'access_revoked');
  await sync(store); assert.deepEqual(calls, []);
});

test('OFF-13: a failed local reading save restores the pending badge after rollback', async () => {
  const value = record('book', false);
  const doc = JSON.stringify({itemId: value.itemId, sourceId: value.sourceId, revision: value.revision,
    format: 'pdf', size: 10, metadata: {title: 'Book'}, resources: [], spine: [], pageNames: [], contents: [], progress: value.progress.progress});
  const digest = createHash('sha256').update(doc).digest('hex');
  const claims = {...value.receipt.claims, publication: {...value.receipt.claims.publication, documentSha256: digest}};
  const bytes = Buffer.from(JSON.stringify(claims));
  const receipt = core.decodeDownloadReceipt({...value.receipt, payload: bytes.toString('base64url'), claims,
    signature: sign(null, bytes, key.privateKey).toString('base64url')}).receipt;
  const store = await open({book: {...value, documentSha256: digest, receipt}}), entry = store.getSnapshot().entries[0];
  const oldRead = native.readExtra, oldOpen = native.publicationOpen;
  native.readExtra = async () => doc;
  native.publicationOpen = async () => 10;
  let session;
  try {
    session = await store.readingSession(entry, {signal: new AbortController().signal, current: () => true, paused: () => false});
    native.writeState = async () => {throw new Error('disk full');};
    await assert.rejects(session.saveProgress({expectedRevision: 2, location: {format: 'pdf', pageIndex: 8, progression: .3}}), /reading_progress_not_saved/);
    assert.equal(read(store).book.pending, undefined); assert.equal(saved().book.pending, undefined);
    assert.equal(store.getSnapshot().pendingProgress, 0);
  } finally {await session?.close(); native.readExtra = oldRead; native.publicationOpen = oldOpen;}
});

// OFF-10: settled downloaded books adopt a newer position saved elsewhere.
const refresh = (store: any, only?: string[]) => Reflect.get(store, 'refreshReadingProgress').call(store, Reflect.get(store, 'generation'), only) as Promise<void>;
function refreshServer(store: any, answer: (receipt: any, index: number) => unknown, during?: () => void) {
  const calls: any[] = [];
  Reflect.set(store, 'api', {baseUrl: 'http://server.test', mediaUrl: (path: string) => 'http://server.test' + path,
    request: async (path: string, _method: string, body: any) => {
      assert.equal(path, '/v1/reading/offline/progress/read'); calls.push(body); during?.();
      return {results: body.receipts.map((receipt: any, index: number) => answer(receipt, index))};
    }});
  return calls;
}
const current = (receipt: any, progress: unknown) => ({receiptId: receipt.receiptId, outcome: 'current', itemId: receipt.claims.itemId,
  sourceId: receipt.claims.publication.sourceId, sourceRevision, progress});
const elsewhere = (receipt: any, revision: number, stale = false) => ({sourceId: receipt.claims.publication.sourceId, sourceRevision: stale ? 'd'.repeat(64) : sourceRevision,
  location: {format: 'pdf', pageIndex: 40, progression: .8}, revision, observedAt: at + 5000, stale});

test('OFF-10: a settled book adopts the newer server position; pending books are never sent', async () => {
  const store = await open({settled: record('settled', false), pending: record('pending')});
  const calls = refreshServer(store, receipt => current(receipt, elsewhere(receipt, 5)));
  await refresh(store);
  assert.deepEqual(calls.map(body => body.receipts.map((receipt: any) => receipt.claims.itemId)), [['item-settled']]);
  assert.equal(read(store).settled.serverRevision, 5);
  assert.equal(read(store).settled.progress.progress.location.pageIndex, 40);
  assert.equal(saved().settled.serverRevision, 5, 'the adopted position is persisted');
  assert.equal(read(store).pending.pending.id, 'observation-pending');
});

test('OFF-10: stale, absent, older and refused answers change nothing', async () => {
  for (const answer of [
    (receipt: any) => current(receipt, elsewhere(receipt, 5, true)),
    (receipt: any) => current(receipt, null),
    (receipt: any) => current(receipt, elsewhere(receipt, 1)),
    (receipt: any) => ({receiptId: receipt.receiptId, outcome: 'refused', code: 'source_unavailable'}),
    (receipt: any) => ({receiptId: receipt.receiptId, outcome: 'refused', code: 'receipt_rejected'}),
  ]) {
    const store = await open({settled: record('settled', false)}), before = read(store).settled;
    refreshServer(store, answer);
    await refresh(store);
    assert.equal(read(store).settled, before);
    assert.ok(device.entries.has('settled'), 'a refusal never removes the copy');
  }
});

test('OFF-10: a local observation saved while the refresh is out wins', async () => {
  const store = await open({settled: record('settled', false)});
  const local = {...record('settled'), serverRevision: 1};
  refreshServer(store, receipt => current(receipt, elsewhere(receipt, 9)), () => {read(store).settled = local;});
  await refresh(store);
  assert.equal(read(store).settled, local);
});
