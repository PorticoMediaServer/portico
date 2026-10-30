/**
 * APP-01, APP-02 and APP-09: downloads, deferred viewing and auto-next belong to
 * the viewer that produced them.
 *
 * Every test drives the real `OfflineDownloads` against the native stub, so the
 * assertions are about what happened to the device's files and to the requests
 * actually sent to a server — not about internal bookkeeping.
 */
import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {device, crypto, emit, NativeModules} from './react-native-stub.mjs';
import {serviceText} from '../../../packages/client-core/src/presentation/service-text.ts';
import type {DownloadsService} from '../../../packages/client-core/src/index';
import type * as OfflineModule from '../platform/offline-downloads.ts';

type Viewer = {authority: string; accountId: string; profileId: string; serverId: string};

const A: Viewer = {authority: 'local', accountId: 'account-a', profileId: 'profile-a', serverId: 'server-1'};
const B: Viewer = {authority: 'local', accountId: 'account-a', profileId: 'profile-b', serverId: 'server-1'};
const OTHER_SERVER: Viewer = {...A, serverId: 'server-2'};

/** Mirrors the bridge's own owner key, so each test states the expected value independently. */
const ownerKeyOf = (v: Viewer) => JSON.stringify([v.authority, v.accountId, v.profileId, v.serverId]);

/** The published signing key, so receipts actually verify in these tests. */
const KEYS = () => ({keys: [{id: 'key-1', algorithm: 'ed25519', publicKey: b64url(new Uint8Array(32)), createdAt: '2026-01-01T00:00:00.000Z', retiredAt: ''}]});
/** Acknowledges every submitted observation, as an applied server write would. */
const APPLIED = (body: any) => ({
  applied: body.entries.length, conflicts: 0,
  entries: body.entries.map((e: any) => ({playId: e.playId, itemId: e.itemId, outcome: 'applied', positionMs: e.positionMs, watched: !!e.watched, observedAt: e.observedAt})),
});

const b64url = (bytes: Uint8Array) =>
  Buffer.from(bytes).toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const digest64 = 'a'.repeat(64);

/** A receipt in exactly the envelope `decodeDownloadReceipt` accepts. */
function receipt(opts: {id: string; viewer: Viewer; itemId: string; preparationId: string; expiresAt?: string}) {
  const claims = {
    kind: 'portico.download-receipt', version: '1', receiptId: opts.id, keyId: 'key-1',
    viewer: opts.viewer, itemId: opts.itemId, preparationId: opts.preparationId,
    quality: 'original', qualityLabel: 'Original',
    artifact: {sha256: digest64, bytes: 10},
    issuedAt: '2026-01-01T00:00:00.000Z',
    expiresAt: opts.expiresAt ?? '2099-01-01T00:00:00.000Z',
  };
  const payload = b64url(new TextEncoder().encode(JSON.stringify(claims)));
  return {receiptId: opts.id, algorithm: 'ed25519', keyId: 'key-1', payload, signature: b64url(new Uint8Array(64)), claims, revision: 1};
}

/* ------------------------------------------------------------------------- *
 * The native module's newer contract: batched meta writes and the files that
 * travel beside a download. The shared stub predates the rewrite, so these
 * methods (and the change events `start` and `setMeta` emit) are added here.
 * ------------------------------------------------------------------------- */
const nativeDownloads = NativeModules.PorticoDownloads as unknown as Record<string, any>;

/** A download plays only in Portico's own player: it asks for the file and this viewer's resume
 * point (`localMedia`), and its positions come back through `recordProgress`. These two stand in
 * for that player: what it was asked to start at, and one observation of the play. */
async function playDownload(offline: {localMedia(entry: unknown): Promise<{startSeconds: number}>}, entry: {id: string}) {
  const media = await offline.localMedia(entry);
  device.played.push({id: entry.id, startSeconds: media.startSeconds});
}
function observe(offline: unknown, event: {id: string; playId?: string; positionSeconds: number; durationSeconds: number; ended: boolean}) {
  void (offline as {observed(e: unknown): Promise<void>}).observed({...event, final: event.ended});
}
const baseStart = nativeDownloads.start as (request: any) => Promise<void>;
/** What `writeExtra`/`fetchExtra` saved, per entry and name. */
const extraStore = new Map<string, Map<string, string>>();
const extra = (id: string): Map<string, string> => {
  let files = extraStore.get(id);
  if (!files) { files = new Map(); extraStore.set(id, files); }
  return files;
};
nativeDownloads.start = async (request: any) => {
  await baseStart(request);
  const entry = device.entries.get(request.id);
  if (entry) emit('PorticoDownloadChanged', {...entry});
};
nativeDownloads.setMetas = async (metas: Record<string, string>) => {
  for (const [id, meta] of Object.entries(metas)) {
    const entry = device.entries.get(id);
    if (!entry) continue;
    device.entries.set(id, {...entry, meta});
    emit('PorticoDownloadChanged', {...device.entries.get(id)});
  }
};
// As on both platforms, one entry's details being written is announced like any other change.
nativeDownloads.setMeta = async (id: string, meta: string) => {
  const entry = device.entries.get(id);
  if (!entry) throw Object.assign(new Error('This download no longer exists.'), {code: 'not_found'});
  device.entries.set(id, {...entry, meta});
  emit('PorticoDownloadChanged', {...device.entries.get(id)});
};
nativeDownloads.writeExtra = async (id: string, name: string, text: string) => { extra(id).set(name, text); };
nativeDownloads.readExtra = async (id: string, name: string) => extra(id).get(name) ?? '';
nativeDownloads.fetchExtra = async (id: string, name: string, url: string) => { extra(id).set(name, url); return 'saved'; };

type Call = {path: string; method?: string; body?: any};

function server() {
  const calls: Call[] = [];
  const routes = new Map<string, (body: any) => unknown>();
  const api = {
    baseUrl: 'http://server.test',
    mediaUrl: (p: string) => 'http://server.test' + p,
    async request<T>(path: string, method?: string, body?: unknown, _signal?: AbortSignal): Promise<T> {
      calls.push({path, method, body});
      const handler = routes.get(path);
      if (!handler) throw new Error('no route: ' + path);
      return handler(body) as T;
    },
  };
  return {
    api, calls,
    route(path: string, handler: (body: any) => unknown) { routes.set(path, handler); return this; },
    sent(path: string) { return calls.filter(c => c.path === path); },
  };
}

/** What the server says a title is; a valid `parseDownloadTitle` shape. */
const TITLE = () => ({
  kind: 'movie', title: 'Item one', year: 2020, durationMs: 1_200_000,
  parentId: '', parentTitle: '', groupId: '', groupTitle: '', groupNumber: null, number: null, artist: '', onServer: true,
});

/** Enough of DownloadsService for the bridge; `request`/`grant`/`bundle` are the calls it makes. */
function downloadsService(items: any[] = []) {
  const listeners = new Set<() => void>();
  const stub = {
    getSnapshot: () => ({items, requests: [] as any[], busy: [] as string[], error: undefined}),
    subscribe: (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn); },
    requested: [] as any[],
    granted: [] as any[],
    nextBatch: {batchId: 'b', items: [] as any[], rejected: [], accepted: 0, duplicate: false},
    async request(target: unknown, quality: string) { stub.requested.push({target, quality}); return stub.nextBatch; },
    async grant(preparation: {id: string; itemId?: string}, renew?: string) {
      stub.granted.push({id: preparation.id, renew});
      const token = 'tok_' + preparation.id;
      return {preparationId: preparation.id, itemId: preparation.itemId ?? '', url: '/v1/downloads/artifacts/' + token, token,
        issuedAt: '2026-01-01T00:00:00.000Z', expiresAt: '2099-01-01T00:00:00.000Z', renewAfterMs: 3_600_000, renewed: false,
        artifact: {kind: 'prepared', ref: 'r', fileName: 'f.mp4', bytes: 10, sha256: digest64, estimated: false, container: 'mp4', contentType: 'video/mp4'}};
    },
    async bundle(preparation: {id: string; itemId: string}) {
      return {preparationId: preparation.id, itemId: preparation.itemId, quality: 'original', qualityLabel: 'Original', title: TITLE(), overview: '',
        media: {container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080},
        artwork: [{role: 'poster', path: 'art/poster', contentType: 'image/jpeg'}],
        chapters: [], markers: [], audio: [],
        subtitles: [{id: 'sub1', revision: 1, language: 'en', title: 'English', default: false, forced: false, files: {vtt: {path: 'sub/en.vtt', contentType: 'text/vtt'}}}]};
    },
    publish() { for (const fn of listeners) fn(); },
    dispose() {},
  };
  return stub;
}

const preparation = (id: string, itemId: string) => ({
  id, itemId, libraryId: 'lib', profileId: 'profile-a', quality: 'original', origin: 'item', state: 'ready', reason: '',
  title: TITLE(), batchId: '', revision: 1, progress: {bytesDone: 10, bytesTotal: 10, bytesTotalEstimated: false, percent: 100, etaMs: null},
  actions: ['remove'], createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-01-01T00:00:00.000Z', readyAt: '', expiresAt: '',
  artifact: {kind: 'prepared', ref: 'r', fileName: 'f.mp4', bytes: 10, sha256: digest64, estimated: false, container: 'mp4', contentType: 'video/mp4'},
});

/** A fresh module instance per test: the bridge exports a singleton. */
let previousBridge: typeof OfflineModule | undefined;
async function bridge(): Promise<typeof OfflineModule> {
  previousBridge?.offlineDownloads.disconnect();
  previousBridge?.offlineDownloads.dispose();
  device.reset();
  extraStore.clear();
  crypto.accept = () => true;
  previousBridge = (await import('../platform/offline-downloads.ts?v=' + Math.random())) as typeof OfflineModule;
  return previousBridge;
}
/** `connect` starts a 60-second interval; nothing may be left running. */
afterEach(() => {
  previousBridge?.offlineDownloads.disconnect();
  previousBridge?.offlineDownloads.dispose();
  previousBridge = undefined;
});
const settle = () => new Promise(done => setTimeout(done, 20));

test('deferred progress drains successive 100-item batches and preserves failed batches for retry', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  const plays = Object.fromEntries(Array.from({length: 250}, (_, i) => [`play-${i}`, {playId: `play-${i}`, itemId: `item-${i}`, positionSeconds: i, observedAt: '2026-09-25T12:00:00.000Z'}]));
  device.state = JSON.stringify({version: 3, settings: {wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0}, plays: {[owner]: plays}, resume: {}, keys: {}, wanted: {}, nextAfter: {}, renewed: {}});
  let failSecond = true;
  let batch = 0;
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/progress', body => {
    batch++;
    if (batch === 2 && failSecond) throw new Error('network unavailable');
    return APPLIED(body);
  });
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.deepEqual(s.sent('/v1/downloads/progress').map(call => call.body.entries.length), [100, 100]);
  assert.equal(offlineDownloads.getSnapshot().pendingProgress, 150);
  failSecond = false;
  await offlineDownloads.syncProgress();
  assert.deepEqual(s.sent('/v1/downloads/progress').map(call => call.body.entries.length), [100, 100, 100, 50]);
  assert.equal(offlineDownloads.getSnapshot().pendingProgress, 0);
});

test('APP-01: a refusal that only means “not your receipt” never deletes the file', async () => {
  const {offlineDownloads} = await bridge();
  const recovered = receipt({id: 'r-a2', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'refused', code: 'unknown_receipt'}]}))
    .route('/v1/downloads/receipts', (body: any) => ({results: [{preparationId: body.preparationIds[0], outcome: 'issued', code: '', receipt: recovered, titleOnServer: true}]}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();

  assert.deepEqual(device.removed, [], 'the file must survive an unknown_receipt refusal');
  assert.ok(device.entries.has('prep-a'));
  assert.equal(s.sent('/v1/downloads/receipts').length, 1, 'an unknown receipt triggers a recovery attempt instead');
});

test('APP-01: a genuine owner-scoped revocation still removes the file', async () => {
  const {offlineDownloads} = await bridge();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'refused', code: 'revoked'}]}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();

  assert.deepEqual(device.removed, ['prep-a'], 'a revoked receipt must still take the file');
});

test('APP-01: removing a download asks the server to revoke its receipt', async () => {
  const {offlineDownloads} = await bridge();
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: rA}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: rA, code: ''}]}))
    .route('/v1/downloads/receipts/revoke', () => ({}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  await offlineDownloads.remove(offlineDownloads.getSnapshot().entries[0]!);
  await settle();

  assert.deepEqual(device.removed, ['prep-a']);
  const revoked = s.sent('/v1/downloads/receipts/revoke');
  assert.equal(revoked.length, 1);
  assert.deepEqual(revoked[0]!.body.receiptIds, ['r-a']);
});

test('APP-01: another viewer’s downloads are never submitted, listed or removed', async () => {
  const {offlineDownloads} = await bridge();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  const rB = receipt({id: 'r-b', viewer: B, itemId: 'item-2', preparationId: 'prep-b'});
  device.seed({id: 'prep-b', itemId: 'item-2', meta: {receipt: rB}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    // Answer as the real server would if A's receipt were ever submitted by B.
    .route('/v1/downloads/receipts/revalidate', (body: any) => ({
      results: body.receiptIds.map((id: string) => id === 'r-b'
        ? {receiptId: id, outcome: 'renewed', code: '', receipt: rB}
        : {receiptId: id, outcome: 'refused', code: 'unknown_receipt'}),
    }));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, B);
  await settle();

  const sent = s.sent('/v1/downloads/receipts/revalidate');
  assert.equal(sent.length, 1);
  assert.deepEqual(sent[0]!.body.receiptIds, ['r-b'], 'only the connected viewer’s receipts go to the server');
  assert.deepEqual(device.removed, [], 'A’s file is never touched while B is signed in');
  assert.ok(device.entries.has('prep-a'), 'A’s bytes are still on the device');
  assert.deepEqual(offlineDownloads.getSnapshot().entries.map((e: {id: string}) => e.id), ['prep-b'], 'B sees their own download and none of A’s');
});

test('APP-01: an entry with no owner at all is shown to nobody', async () => {
  const {offlineDownloads} = await bridge();
  // No receipt and no recorded owner: nothing says whose copy this is.
  device.seed({id: 'prep-orphan', itemId: 'item-1'});
  // A recorded owner with no receipt yet still belongs to that viewer.
  device.seed({id: 'prep-owned', itemId: 'item-2', meta: {owner: ownerKeyOf(A)}});

  offlineDownloads.adopt(A);
  await offlineDownloads.open();
  await settle();
  assert.deepEqual(offlineDownloads.getSnapshot().entries.map((e: {id: string}) => e.id), ['prep-owned']);

  offlineDownloads.adopt(B);
  await settle();
  assert.deepEqual(offlineDownloads.getSnapshot().entries, [], 'the unowned copy is not handed to anyone');
});

test('APP-01: a native entry is read as the module reports it: folder, waiting and a held intent', async () => {
  const {offlineDownloads} = await bridge();
  device.seed({id: 'prep-a', itemId: 'item-1', dir: 'file:///docs/prep-a/', waiting: 'wifi', intent: 'hold', meta: {owner: ownerKeyOf(A), extras: ['poster']}});

  offlineDownloads.adopt(A);
  await offlineDownloads.open();
  await settle();

  const entry = offlineDownloads.getSnapshot().entries[0]!;
  assert.equal(entry.artworkUrl, 'file:///docs/prep-a/extras/poster', 'artwork is named from the entry folder');
  assert.equal(entry.waiting, 'wifi');
  assert.equal(entry.held, true, 'a held intent is the person having paused it');
});

test('APP-01: the same account on a different server is a different owner', async () => {
  const {offlineDownloads} = await bridge();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'refused', code: 'unknown_receipt'}]}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, OTHER_SERVER);
  await settle();

  assert.equal(s.sent('/v1/downloads/receipts/revalidate').length, 0, 'nothing to renew for this owner');
  assert.deepEqual(device.removed, []);
});

test('APP-01: a renewal answered after the viewer changed cannot write back', async () => {
  const {offlineDownloads} = await bridge();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  let release!: (value: unknown) => void;
  const held = new Promise(done => { release = done; });
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', async () => {
      await held;
      return {results: [{receiptId: 'r-a', outcome: 'refused', code: 'revoked'}]};
    });

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  // B signs in while A's revalidation is still outstanding.
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, B);
  await settle();
  release(undefined);
  await settle();

  assert.deepEqual(device.removed, [], 'a result from the previous connection must not act on the device');
});

test('APP-01: receipts are renewed at most once a day for one viewer', async () => {
  const {offlineDownloads} = await bridge();
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: rA}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: rA, code: ''}]}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.equal(s.sent('/v1/downloads/receipts/revalidate').length, 1, 'the first connection renews');
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.equal(s.sent('/v1/downloads/receipts/revalidate').length, 1, 'a second connection the same day does not');
});

test('APP-01: a receipt close to expiry is renewed without waiting the day', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  const soon = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a', expiresAt: new Date(Date.now() + 3 * 24 * 60 * 60 * 1000).toISOString()});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: soon}});
  device.state = JSON.stringify({version: 3, settings: {wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0}, plays: {}, resume: {}, keys: {}, wanted: {}, nextAfter: {}, renewed: {[owner]: Date.now()}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: soon, code: ''}]}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.equal(s.sent('/v1/downloads/receipts/revalidate').length, 1, 'a receipt near its end is renewed at once');
});

test('APP-01: a transfer that already has an address keeps it alive by renewing its token', async () => {
  const {offlineDownloads} = await bridge();
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', state: 'downloading', meta: {receipt: rA, token: 'old-token', owner: ownerKeyOf(A)}});
  const service = downloadsService();
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: rA, code: ''}]}));

  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();
  assert.ok(service.granted.some(g => g.id === 'prep-a' && g.renew === 'old-token'), 'the address this device holds is renewed, not replaced');
});

test('APP-01: a renewal batch is written with one setMetas call and a viewer change deletes nothing', async () => {
  const {offlineDownloads} = await bridge();
  const first = receipt({id: 'r-a1', viewer: A, itemId: 'item-1', preparationId: 'prep-1'});
  const second = receipt({id: 'r-a2', viewer: A, itemId: 'item-2', preparationId: 'prep-2'});
  const third = receipt({id: 'r-a3', viewer: A, itemId: 'item-3', preparationId: 'prep-3'});
  device.seed({id: 'prep-1', itemId: 'item-1', meta: {receipt: first}});
  device.seed({id: 'prep-2', itemId: 'item-2', meta: {receipt: second}});
  device.seed({id: 'prep-3', itemId: 'item-3', meta: {receipt: third}});
  const renewed: Record<string, unknown> = {'r-a1': first, 'r-a2': second, 'r-a3': third};
  let release!: () => void;
  let entered!: () => void;
  const held = new Promise<void>(done => { release = done; });
  const started = new Promise<void>(done => { entered = done; });
  const batches: string[][] = [];
  const original = nativeDownloads.setMetas;
  nativeDownloads.setMetas = async (metas: Record<string, string>) => { batches.push(Object.keys(metas)); await original(metas); entered(); await held; };
  try {
    const s = server()
      .route('/v1/downloads/receipt-keys', KEYS)
      .route('/v1/downloads/receipts/revalidate', (body: any) => ({results: body.receiptIds.map((id: string) => ({receiptId: id, outcome: 'renewed', receipt: renewed[id], code: ''}))}));
    offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
    await started;
    // B signs in while the batch is being written to the device.
    offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, B);
    release();
    await settle();
    assert.equal(batches.length, 1, 'a batch of renewals is written with one setMetas call');
    assert.deepEqual([...batches[0]!].sort(), ['prep-1', 'prep-2', 'prep-3']);
    assert.deepEqual(device.removed, [], 'a viewer change during the write deletes nothing');
    assert.ok(device.entries.has('prep-1') && device.entries.has('prep-2') && device.entries.has('prep-3'));
  } finally {
    release();
    nativeDownloads.setMetas = original;
  }
});

test('APP-02: offline viewing is posted for its own viewer, not whoever connects next', async () => {
  const {offlineDownloads} = await bridge();
  const claims = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: claims}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: claims, code: ''}]}))
    .route('/v1/downloads/progress', APPLIED);

  // A watches it and stops; the observation is queued.
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  s.calls.length = 0;
  observe(offlineDownloads, {id: 'prep-a', positionSeconds: 120, durationSeconds: 1000, ended: false});
  await settle();

  // B signs in before it was ever sent.
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, B);
  await settle();
  assert.equal(s.sent('/v1/downloads/progress').length, 0, 'A’s viewing must not be submitted as B');

  // A comes back: it goes up once, attributed correctly, and is then cleared.
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  const posted = s.sent('/v1/downloads/progress');
  assert.equal(posted.length, 1);
  assert.deepEqual(posted[0]!.body.entries.map((e: {itemId: string}) => e.itemId), ['item-1']);
  assert.equal(posted[0]!.body.entries[0].positionMs, 120_000, 'the server takes positions in whole milliseconds');
  assert.equal('positionSeconds' in posted[0]!.body.entries[0], false);
  assert.ok(posted[0]!.body.entries[0].playId, 'every play is named by its own id');
  assert.equal('watched' in posted[0]!.body.entries[0], false, 'the device reports where the play stopped; the server decides if it counts');

  s.calls.length = 0;
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.equal(s.sent('/v1/downloads/progress').length, 0, 'an acknowledged observation is not resent');
});

test('APP-02: a resume position does not cross between profiles', async () => {
  const {offlineDownloads} = await bridge();
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  const rB = receipt({id: 'r-b', viewer: B, itemId: 'item-1', preparationId: 'prep-b'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: rA}});
  device.seed({id: 'prep-b', itemId: 'item-1', meta: {receipt: rB}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', (body: any) => ({results: body.receiptIds.map((id: string) => ({receiptId: id, outcome: 'renewed', receipt: id === 'r-a' ? rA : rB, code: ''}))}))
    .route('/v1/downloads/progress', APPLIED);

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  observe(offlineDownloads, {id: 'prep-a', positionSeconds: 300, durationSeconds: 1000, ended: false});
  await settle();

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, B);
  await settle();
  const entry = offlineDownloads.getSnapshot().entries.find(e => e.id === 'prep-b')!;
  assert.ok(entry, 'B sees their own copy');
  await playDownload(offlineDownloads, entry);
  assert.deepEqual(device.played, [{id: 'prep-b', startSeconds: 0}], 'B starts at the beginning, not A’s position');
});

test('APP-01: a renewal brings the server’s position and whether the title is still on the server', async () => {
  const {offlineDownloads} = await bridge();
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: rA}});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: rA, code: '', titleOnServer: false, viewing: {positionMs: 200000, watched: false}}]}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();

  const entry = offlineDownloads.getSnapshot().entries[0]!;
  assert.equal(entry.onServer, false, 'titleOnServer is stored and surfaced');
  assert.equal(offlineDownloads.note(entry), serviceText('downloads.offline.goneFromServer'));
  await playDownload(offlineDownloads, entry);
  assert.deepEqual(device.played, [{id: 'prep-a', startSeconds: 200}], 'the server’s position becomes this copy’s resume point');
});

test('APP-02: a renewal does not override a play this device has not sent yet', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: rA}});
  device.state = JSON.stringify({
    version: 3, settings: {wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0},
    plays: {[owner]: {'play-1': {playId: 'play-1', itemId: 'item-1', positionSeconds: 300, observedAt: '2026-09-01T00:00:00.000Z'}}},
    resume: {[owner]: {'item-1': 300}}, keys: {}, wanted: {}, nextAfter: {}, renewed: {},
  });
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/progress', () => { throw new Error('offline'); })
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: rA, code: '', titleOnServer: true, viewing: {positionMs: 50000, watched: false}}]}));

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();
  await playDownload(offlineDownloads, offlineDownloads.getSnapshot().entries[0]!);
  assert.deepEqual(device.played, [{id: 'prep-a', startSeconds: 300}], 'the unsent play is newer than the server’s figure');
});

test('APP-02: an old bookkeeping document keeps only its settings and keys', async () => {
  const {offlineDownloads} = await bridge();
  // A document laid out for an earlier contract: only settings and the server's keys are carried
  // over; the plays, resume points, wants and next-up intents were shaped for something else.
  device.state = JSON.stringify({
    version: 2,
    settings: {wifiOnly: false, deleteWatched: true, autoNext: true, limitBytes: 1234},
    progress: {'item-1': {itemId: 'item-1', positionSeconds: 90, observedAt: '2026-01-01T00:00:00.000Z'}},
    resume: {'item-1': 90},
    wanted: {[ownerKeyOf(A)]: {'item-9': {title: 'Old want'}}},
    nextAfter: {[ownerKeyOf(A)]: {'item-1': {quality: 'original'}}},
    legacy: {progress: {}, resume: {}, wanted: {}},
    keys: {'key-1': b64url(new Uint8Array(32))},
  });
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/progress', APPLIED);

  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, A);
  await settle();

  const saved = JSON.parse(device.state);
  assert.equal(saved.version, 3);
  assert.deepEqual(saved.settings, {wifiOnly: false, deleteWatched: true, autoNext: true, limitBytes: 1234}, 'settings are carried over');
  assert.equal(saved.keys['key-1'], b64url(new Uint8Array(32)), 'the server’s keys are carried over');
  assert.deepEqual(saved.plays, {}, 'old plays are not adopted by whoever signs in');
  assert.deepEqual(saved.resume, {}, 'nor are old resume points');
  assert.deepEqual(saved.wanted, {}, 'nor old wants');
  assert.deepEqual(saved.nextAfter, {}, 'nor old next-up intents');
  assert.equal(s.sent('/v1/downloads/progress').length, 0, 'an unowned record is never posted');
});

test('APP-09: finishing a title with auto-next on queues the next one for transfer', async () => {
  const {offlineDownloads} = await bridge();
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: rA, libraryId: 'lib'}});
  const next = preparation('prep-next', 'item-2');
  const service = downloadsService([next]);
  service.nextBatch = {batchId: 'b', items: [next], rejected: [], accepted: 1, duplicate: false};
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: rA, code: ''}]}))
    .route('/v1/downloads/progress', APPLIED)
    .route('/v1/downloads/receipts', () => ({results: []}));

  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();
  await offlineDownloads.setSettings({autoNext: true});
  observe(offlineDownloads, {id: 'prep-a', positionSeconds: 1000, durationSeconds: 1000, ended: true});
  await settle();

  assert.equal(service.requested.length, 1, 'the server is asked to prepare the next title');
  assert.deepEqual(service.requested[0].target, {nextAfterMediaId: 'item-1'});
  assert.deepEqual(device.started.map((r: {id: string}) => r.id), ['prep-next'], 'and the prepared next title is actually transferred');
  assert.equal(JSON.parse(device.started[0]!.meta).owner, ownerKeyOf(A), 'under the watching viewer');
});

test('APP-01: a transfer takes its receipt, address and title before any byte moves', async () => {
  const {offlineDownloads} = await bridge();
  const p = preparation('prep-new', 'item-9');
  const service = downloadsService([p]);
  const issued = receipt({id: 'r-new', viewer: A, itemId: 'item-9', preparationId: 'prep-new'});
  const s = server()
    .route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/preparations/prep-new/bundle', () => service.bundle(p))
    .route('/v1/downloads/receipts', (body: any) => ({results: [{preparationId: body.preparationIds[0], outcome: 'issued', code: '', receipt: issued, titleOnServer: true}]}));

  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();
  await offlineDownloads.fetch(p as any);
  await settle();

  const meta = JSON.parse(device.started[0]!.meta);
  assert.equal(meta.owner, ownerKeyOf(A), 'the owner is recorded before the bytes arrive');
  assert.equal(meta.onServer, true);
  assert.equal(meta.token, 'tok_prep-new', 'the address is the one the grant issued');
  assert.equal(meta.receipt.receiptId, 'r-new', 'the receipt is taken before the transfer starts');
  assert.equal(s.sent('/v1/downloads/receipts').length, 1);
  // What travels with the download is written beside it, too.
  assert.ok(extra('prep-new').has('bundle.json'), 'the bundle description is saved');
  assert.ok(extra('prep-new').has('poster'), 'artwork is fetched beside the file');
  assert.ok(extra('prep-new').has('sub-sub1-1.vtt'), 'subtitle files are fetched beside the file');

  // And an in-flight transfer is not listed for a different viewer.
  offlineDownloads.connect(s.api as any, downloadsService() as unknown as DownloadsService, B);
  await settle();
  assert.deepEqual(offlineDownloads.getSnapshot().entries.map((e: {id: string}) => e.id), []);
});

/* ------------------------------------------------------------------------- *
 * APP-03: a cold launch with no server still reaches this device's downloads.
 * ------------------------------------------------------------------------- */

/** What a device looks like after a previous online session: keys remembered. */
const storedWithKeys = () => JSON.stringify({
  version: 3,
  settings: {wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0},
  plays: {}, resume: {}, wanted: {}, nextAfter: {}, renewed: {},
  keys: {'key-1': b64url(new Uint8Array(32))},
});

test('APP-03: the saved viewer can list and play their downloads with no connection', async () => {
  const {offlineDownloads} = await bridge();
  device.state = storedWithKeys();
  device.seed({id: 'prep-a', itemId: 'item-1', title: 'Kept title', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});

  // No api, no service: exactly a cold launch behind a dead network.
  offlineDownloads.adopt(A);
  await offlineDownloads.open();
  await settle();

  const listed = offlineDownloads.getSnapshot().entries;
  assert.deepEqual(listed.map((e: {id: string}) => e.id), ['prep-a']);
  assert.equal(offlineDownloads.playable(listed[0]!), '', 'a valid receipt plays offline');
  await playDownload(offlineDownloads, listed[0]!);
  assert.deepEqual(device.played, [{id: 'prep-a', startSeconds: 0}]);
});

test('APP-03: the offline route exposes only the saved viewer’s downloads', async () => {
  const {offlineDownloads} = await bridge();
  device.state = storedWithKeys();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  device.seed({id: 'prep-b', itemId: 'item-2', meta: {receipt: receipt({id: 'r-b', viewer: B, itemId: 'item-2', preparationId: 'prep-b'})}});

  offlineDownloads.adopt(B);
  await offlineDownloads.open();
  await settle();

  assert.deepEqual(offlineDownloads.getSnapshot().entries.map((e: {id: string}) => e.id), ['prep-b']);
});

test('APP-03: an expired receipt is listed but refuses to play until the server is reached', async () => {
  const {offlineDownloads} = await bridge();
  device.state = storedWithKeys();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a', expiresAt: '2026-02-01T00:00:00.000Z'})}});

  offlineDownloads.adopt(A);
  await offlineDownloads.open();
  await settle();

  const entry = offlineDownloads.getSnapshot().entries[0]!;
  assert.equal(offlineDownloads.playable(entry), serviceText('downloads.offline.needsCheckIn'));
  await assert.rejects(() => playDownload(offlineDownloads, entry));
  assert.deepEqual(device.played, [], 'nothing was handed to the native player');
});

test('APP-03: a receipt whose signature does not verify never plays offline', async () => {
  const {offlineDownloads} = await bridge();
  crypto.accept = () => false;
  device.state = storedWithKeys();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});

  offlineDownloads.adopt(A);
  await offlineDownloads.open();
  await settle();

  const entry = offlineDownloads.getSnapshot().entries[0]!;
  assert.equal(offlineDownloads.playable(entry), serviceText('downloads.offline.needsSetup'));
  await assert.rejects(() => playDownload(offlineDownloads, entry));
});

test('APP-03: signing out withdraws the offline list without deleting the files', async () => {
  const {offlineDownloads} = await bridge();
  device.state = storedWithKeys();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});

  offlineDownloads.adopt(A);
  await offlineDownloads.open();
  await settle();
  assert.equal(offlineDownloads.getSnapshot().entries.length, 1);

  const heldEntry = offlineDownloads.getSnapshot().entries[0]!;
  offlineDownloads.adopt(undefined);
  await assert.rejects(playDownload(offlineDownloads, heldEntry), (e: Error) => e.message === serviceText('downloads.offline.signIn'));
  assert.deepEqual(device.played, []);
  assert.deepEqual(offlineDownloads.getSnapshot().entries, [], 'nobody is named, so nothing is listed');
  assert.ok(device.entries.has('prep-a'), 'and the bytes are still there for when they sign back in');
});

for (const change of ['server', 'signout'] as const) {
  test(`APP-02: a grant resolving after ${change} cannot start a transfer`, async () => {
    const {offlineDownloads} = await bridge();
    const first = server().route('/v1/downloads/receipt-keys', KEYS);
    const second = server().route('/v1/downloads/receipt-keys', KEYS);
    second.api.mediaUrl = p => 'http://other-server.test' + p;
    const service = downloadsService();
    const grant = service.grant;
    let release!: () => void;
    const held = new Promise<void>(done => { release = done; });
    service.grant = async p => { await held; return grant(p); };
    offlineDownloads.connect(first.api, service as unknown as DownloadsService, A);
    await settle();
    const pending = offlineDownloads.fetch(preparation('prep-held', 'item-held') as any);
    const refused = assert.rejects(pending, (e: Error) => e.message === serviceText('downloads.connectToStart'));
    if (change === 'server') offlineDownloads.connect(second.api, downloadsService() as unknown as DownloadsService, OTHER_SERVER);
    else offlineDownloads.adopt(undefined);
    release();
    await refused;
    assert.deepEqual(device.started, [], 'the old bearer grant is never dispatched after authority changes');
  });
}

test('APP-01: stale key discovery cannot publish keys after changing servers', async () => {
  const {offlineDownloads} = await bridge();
  let release!: () => void;
  const held = new Promise<void>(done => { release = done; });
  const oldKeys = {keys: [{...KEYS().keys[0]!, id: 'old-server-key'}]};
  const first = server().route('/v1/downloads/receipt-keys', async () => { await held; return oldKeys; });
  const second = server().route('/v1/downloads/receipt-keys', KEYS);
  offlineDownloads.connect(first.api, downloadsService() as unknown as DownloadsService, A);
  await settle();
  offlineDownloads.connect(second.api, downloadsService() as unknown as DownloadsService, OTHER_SERVER);
  await settle();
  release();
  await settle();
  assert.equal(JSON.parse(device.state).keys['old-server-key'], undefined);
});

test('APP-09: offline completion survives restart and only its owner retries auto-next', async () => {
  let {offlineDownloads} = await bridge();
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'episode-1', preparationId: 'prep-a'});
  device.state = storedWithKeys();
  device.seed({id: 'prep-a', itemId: 'episode-1', quality: 'original', meta: {receipt: rA, libraryId: 'lib'}});
  offlineDownloads.adopt(A);
  await offlineDownloads.open();
  await offlineDownloads.setSettings({autoNext: true});
  observe(offlineDownloads, {id: 'prep-a', positionSeconds: 100, durationSeconds: 100, ended: true});
  await settle();
  const saved = device.state;
  assert.ok(JSON.parse(saved).nextAfter[ownerKeyOf(A)]['episode-1']);
  offlineDownloads.dispose();
  ({offlineDownloads} = await bridge());
  device.state = saved;
  const next = preparation('prep-next', 'episode-2');
  const service = downloadsService([next]);
  service.nextBatch = {batchId: 'b', items: [next], rejected: [], accepted: 1, duplicate: false};
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/progress', APPLIED);
  offlineDownloads.connect(s.api, service as unknown as DownloadsService, B);
  await settle();
  assert.deepEqual(service.requested, []);
  offlineDownloads.connect(s.api, service as unknown as DownloadsService, A);
  await settle();
  assert.deepEqual(service.requested, [{target: {nextAfterMediaId: 'episode-1'}, quality: 'original'}]);
  assert.equal(device.started.length, 1);
  assert.equal(JSON.parse(device.started[0].meta).owner, ownerKeyOf(A));
  assert.deepEqual(JSON.parse(device.state).nextAfter[ownerKeyOf(A)], {});
});

test('APP-09: a failed next-episode request is retained and retried on reconnect', async () => {
  const {offlineDownloads} = await bridge();
  device.state = JSON.stringify({...JSON.parse(storedWithKeys()), settings: {autoNext: true}, nextAfter: {[ownerKeyOf(A)]: {'episode-1': {quality: 'original'}}}});
  const failing = downloadsService();
  failing.request = async () => { throw new Error('offline'); };
  const s = server().route('/v1/downloads/receipt-keys', KEYS);
  offlineDownloads.connect(s.api, failing as unknown as DownloadsService, A);
  await settle();
  assert.ok(JSON.parse(device.state).nextAfter[ownerKeyOf(A)]['episode-1']);
  const recovered = downloadsService();
  offlineDownloads.connect(s.api, recovered as unknown as DownloadsService, A);
  await settle();
  assert.equal(recovered.requested.length, 1);
  assert.deepEqual(JSON.parse(device.state).nextAfter[ownerKeyOf(A)], {});
});

test('APP-03: restoring the saved viewer loads the cold offline entry point without mounting a downloads screen', async () => {
  const {offlineDownloads} = await bridge();
  device.state = storedWithKeys();
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  offlineDownloads.adopt(A);
  await settle();
  assert.equal(offlineDownloads.getSnapshot().ready, true);
  assert.equal(offlineDownloads.getSnapshot().entries.some(e => e.state === 'ready'), true, 'the auth-screen card can now become visible without a network request');
  await playDownload(offlineDownloads, offlineDownloads.getSnapshot().entries[0]!);
  assert.equal(device.played.length, 1);
});

test('APP-02: a completed request from an old download sheet cannot enqueue for the new viewer', async () => {
  const {offlineDownloads} = await bridge();
  const first = downloadsService();
  const next = preparation('prep-held', 'item-held');
  const second = downloadsService([next]);
  const s = server().route('/v1/downloads/receipt-keys', KEYS);
  offlineDownloads.connect(s.api, first as unknown as DownloadsService, A);
  await settle();
  offlineDownloads.connect(s.api, second as unknown as DownloadsService, B);
  await settle();
  await offlineDownloads.want([next as any], {title: 'Old request'}, first as unknown as DownloadsService);
  await settle();
  assert.deepEqual(device.started, []);
  assert.equal(JSON.parse(device.state).wanted[ownerKeyOf(B)]?.['prep-held'], undefined);
});

test('an acknowledgement cannot erase a newer observation made in the same millisecond', async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.parse('2026-10-13T12:00:00Z')});
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A), observedAt = new Date().toISOString();
  device.state = JSON.stringify({...JSON.parse(storedWithKeys()), plays: {[owner]: {'play-one': {playId: 'play-one', itemId: 'item-1', positionSeconds: 10, observedAt}}}});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {owner, bundle: 2}});
  let answer!: (value: unknown) => void;
  const held = new Promise(resolve => { answer = resolve; });
  let posts = 0;
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/progress', () => {
    if (++posts === 1) return held;
    throw new Error('offline again');
  });
  offlineDownloads.connect(s.api, downloadsService() as unknown as DownloadsService, A);
  await settle();
  observe(offlineDownloads, {id: 'prep-a', playId: 'play-one', positionSeconds: 20, durationSeconds: 100, ended: false});
  await settle();
  answer(APPLIED(s.sent('/v1/downloads/progress')[0]!.body));
  await offlineDownloads.syncProgress();
  assert.equal(s.sent('/v1/downloads/progress')[1]?.body.entries[0].positionMs, 20000);
  assert.equal(JSON.parse(device.state).plays[owner]['play-one'].positionSeconds, 20);
});

test('a partial progress receipt settles only the observations it actually acknowledges', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A), observedAt = new Date().toISOString();
  device.state = JSON.stringify({...JSON.parse(storedWithKeys()), plays: {[owner]: {
    first: {playId: 'first', itemId: 'item-1', positionSeconds: 10, observedAt},
    second: {playId: 'second', itemId: 'item-2', positionSeconds: 20, observedAt},
  }}});
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/progress', body => {
    const result = APPLIED(body);
    return {...result, entries: [result.entries[0], {playId: 'second', itemId: 'item-2', outcome: 'unknown'}]};
  });
  offlineDownloads.connect(s.api, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.deepEqual(Object.keys(JSON.parse(device.state).plays[owner]), ['second']);
  assert.equal(s.sent('/v1/downloads/progress').length, 1, 'an incomplete answer does not loop');
});

test('a replacement transfer grant is handed to the native downloader', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  device.seed({id: 'prep-a', itemId: 'item-1', quality: 'original', state: 'downloading', meta: {owner, token: 'old-token', bundle: 2,
    receipt: receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'})}});
  device.state = JSON.stringify({...JSON.parse(storedWithKeys()), renewed: {[owner]: Date.now()}});
  const s = server().route('/v1/downloads/receipt-keys', KEYS);
  offlineDownloads.connect(s.api, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.equal(device.started.length, 1);
  assert.equal(device.started[0].url, 'http://server.test/v1/downloads/artifacts/tok_prep-a');
  assert.equal(JSON.parse(device.started[0].meta).token, 'tok_prep-a');
});

test('a transfer grant renewal cannot patch an entry after its viewer changes', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  device.seed({id: 'prep-a', itemId: 'item-1', state: 'downloading', meta: {owner, token: 'old-token', bundle: 2}});
  const service = downloadsService(), grant = service.grant;
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  service.grant = async (p, renew) => { await held; return grant(p, renew); };
  const s = server().route('/v1/downloads/receipt-keys', KEYS);
  offlineDownloads.connect(s.api, service as unknown as DownloadsService, A);
  await settle();
  offlineDownloads.adopt(B);
  release();
  await settle();
  assert.equal(device.metaOf('prep-a').token, 'old-token');
  assert.deepEqual(device.started, []);
});

test('network wake retries deferred progress and next-episode work without reconnecting', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A), observedAt = new Date().toISOString();
  device.state = JSON.stringify({...JSON.parse(storedWithKeys()), settings: {autoNext: true},
    plays: {[owner]: {pending: {playId: 'pending', itemId: 'episode-1', positionSeconds: 10, observedAt}}},
    nextAfter: {[owner]: {'episode-1': {quality: 'original'}}}});
  let offline = true, requestedOnline = 0;
  const service = downloadsService();
  service.request = async () => {if (offline) throw new Error('offline'); requestedOnline++; return service.nextBatch;};
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/progress', body => {if (offline) throw new Error('offline'); return APPLIED(body);});
  offlineDownloads.connect(s.api, service as unknown as DownloadsService, A);
  await settle();
  offline = false;
  offlineDownloads.wake();
  await settle();
  assert.deepEqual(JSON.parse(device.state).plays[owner], {});
  assert.deepEqual(JSON.parse(device.state).nextAfter[owner], {});
  assert.equal(requestedOnline, 1, 'one wake retries the next-episode request once');
});

test('a continuously connected viewer renews receipts on the next day, once per overlapping wake', async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.parse('2026-10-13T12:00:00Z')});
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A), r = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {owner, receipt: r, bundle: 2}});
  let resolve!: (value: unknown) => void, delayed = false;
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/receipts/revalidate', () => delayed ? new Promise(done => {resolve = done;}) : ({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: r, code: ''}]}));
  offlineDownloads.connect(s.api, downloadsService() as unknown as DownloadsService, A);
  await settle();
  assert.equal(s.sent('/v1/downloads/receipts/revalidate').length, 1);
  t.mock.timers.tick(24 * 60 * 60 * 1000);
  delayed = true;
  offlineDownloads.wake(); offlineDownloads.wake();
  await settle();
  const count = s.sent('/v1/downloads/receipts/revalidate').length;
  if (resolve) {resolve({results: [{receiptId: 'r-a', outcome: 'renewed', receipt: r, code: ''}]}); await settle();}
  assert.equal(count, 2);
});

test('successive observations of one title have an order the server can distinguish', async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.parse('2026-10-13T12:00:00Z')});
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A), at = new Date().toISOString();
  device.state = JSON.stringify({...JSON.parse(storedWithKeys()), plays: {[owner]: {play: {playId: 'play', itemId: 'item-1', positionSeconds: 10, observedAt: at}}}});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {owner, bundle: 2}});
  let resolve!: (value: unknown) => void;
  let previous = 0, serverPosition = 0;
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/progress', body => {
    const e = body.entries[0], stamp = Date.parse(e.observedAt);
    const stale = stamp <= previous;
    if (!stale) {previous = stamp; serverPosition = e.positionMs;}
    const answer = {applied: stale ? 0 : 1, conflicts: stale ? 1 : 0, entries: [{...e, outcome: stale ? 'stale_observation' : 'applied', watched: false, positionMs: serverPosition}]};
    if (s.sent('/v1/downloads/progress').length === 1) return new Promise(done => {resolve = done;});
    return answer;
  });
  offlineDownloads.connect(s.api, downloadsService() as unknown as DownloadsService, A);
  await settle();
  observe(offlineDownloads, {id: 'prep-a', playId: 'play', positionSeconds: 20, durationSeconds: 100, ended: false});
  await settle();
  resolve(APPLIED(s.sent('/v1/downloads/progress')[0]!.body));
  await offlineDownloads.syncProgress();
  assert.equal(serverPosition, 20000);
  assert.equal(JSON.parse(device.state).resume[owner]['item-1'], 20);
});

/* ------------------------------------------------------------------------- *
 * Looking after downloads while online (`tend`), and a receipt the server no
 * longer knows (`recover`).
 * ------------------------------------------------------------------------- */

const renewing = (r: ReturnType<typeof receipt>) => () => ({results: [{receiptId: r.receiptId, outcome: 'renewed', receipt: r, code: ''}]});

test('a transfer that stopped by itself starts again; one the person paused, or that cannot mend, does not', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', state: 'failed', error: 'network', meta: {owner, receipt: rA}});
  device.seed({id: 'prep-b', itemId: 'item-2', state: 'paused', intent: 'hold', meta: {owner}});
  device.seed({id: 'prep-c', itemId: 'item-3', state: 'failed', error: 'checksum', meta: {owner}});
  device.seed({id: 'prep-d', itemId: 'item-4', state: 'paused', meta: {owner}});
  const service = downloadsService();
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/receipts/revalidate', renewing(rA))
    .route('/v1/downloads/receipts', (body: any) => ({results: [{preparationId: body.preparationIds[0], outcome: 'refused', code: 'not_ready', receipt: null}]}));

  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();

  assert.deepEqual(device.started.map(r => r.id).sort(), ['prep-a', 'prep-d']);
  assert.equal(JSON.parse(device.started.find(r => r.id === 'prep-a')!.meta).token, 'tok_prep-a', 'it continues with a fresh address');
});

test('a transfer the server can no longer hand over is not asked for again for a quarter of an hour', async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.parse('2026-10-13T12:00:00Z')});
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', state: 'failed', error: 'grant_expired', meta: {owner, receipt: rA}});
  const service = downloadsService();
  let grants = 0;
  service.grant = async () => { grants++; throw Object.assign(new Error('gone'), {status: 410}); };
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/receipts/revalidate', renewing(rA));

  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();
  assert.equal(grants, 1);
  t.mock.timers.tick(60 * 1000);
  offlineDownloads.wake();
  await settle();
  assert.equal(grants, 1, 'not every half minute');
  t.mock.timers.tick(15 * 60 * 1000);
  offlineDownloads.wake();
  await settle();
  assert.equal(grants, 2, 'asked again later');
  assert.deepEqual(device.removed, [], 'the bytes already here are kept');
});

test('a download that arrived without what travels with it gets it on a later pass', async () => {
  const {offlineDownloads} = await bridge();
  const owner = ownerKeyOf(A);
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {owner, receipt: rA, onServer: true, bundle: 1}});
  const service = downloadsService();
  const s = server().route('/v1/downloads/receipt-keys', KEYS).route('/v1/downloads/receipts/revalidate', renewing(rA))
    .route('/v1/downloads/preparations/prep-a/bundle', () => service.bundle({id: 'prep-a', itemId: 'item-1'}));

  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();

  assert.ok(extra('prep-a').has('bundle.json') && extra('prep-a').has('poster') && extra('prep-a').has('sub-sub1-1.vtt'));
  assert.equal(device.metaOf('prep-a').bundle, 4, 'it is complete and not fetched again');
  assert.deepEqual(device.started, [], 'the file itself is not transferred again');
});

/** A device holding prep-a for item-1 whose receipt the server has forgotten, with the claim gone too. */
function forgotten(fresh: ReturnType<typeof preparation> | undefined) {
  const owner = ownerKeyOf(A);
  const rA = receipt({id: 'r-a', viewer: A, itemId: 'item-1', preparationId: 'prep-a'});
  device.seed({id: 'prep-a', itemId: 'item-1', meta: {owner, receipt: rA, onServer: true, bundle: 3}});
  const issued = receipt({id: 'r-fresh', viewer: A, itemId: 'item-1', preparationId: 'prep-fresh'});
  const service = downloadsService();
  service.nextBatch = {...service.nextBatch, items: fresh ? [fresh] : []};
  const s = server().route('/v1/downloads/receipt-keys', KEYS)
    .route('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'r-a', outcome: 'refused', code: 'unknown_receipt'}]}))
    .route('/v1/downloads/receipts', (body: any) => ({results: [body.preparationIds[0] === 'prep-fresh'
      ? {preparationId: 'prep-fresh', outcome: 'issued', code: '', receipt: issued, titleOnServer: true}
      : {preparationId: body.preparationIds[0], outcome: 'refused', code: 'unknown_preparation', receipt: null}]}));
  return {service, s};
}

test('a forgotten receipt is replaced by a fresh claim for the very file this device holds', async () => {
  const {offlineDownloads} = await bridge();
  const {service, s} = forgotten(preparation('prep-fresh', 'item-1'));
  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();

  assert.deepEqual(service.requested.map(r => r.quality), [''], 'the same title is asked for again');
  const meta = device.metaOf('prep-a');
  assert.equal(meta.receipt.receiptId, 'r-fresh');
  assert.equal(meta.claim, 'prep-fresh', 'later renewals use the new claim');
  assert.notEqual(meta.changed, true);
  assert.deepEqual(device.started, [], 'nothing is transferred again');
  assert.deepEqual(device.removed, []);
});

test('a forgotten receipt whose fresh claim names a different file keeps this copy and says the server’s changed', async () => {
  const {offlineDownloads} = await bridge();
  const fresh = {...preparation('prep-fresh', 'item-1'), artifact: {...preparation('prep-fresh', 'item-1').artifact, sha256: 'b'.repeat(64)}};
  const {service, s} = forgotten(fresh);
  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();

  const meta = device.metaOf('prep-a');
  assert.equal(meta.changed, true);
  assert.equal(meta.receipt.receiptId, 'r-a', 'the copy keeps the receipt it has until it runs out');
  assert.equal(s.sent('/v1/downloads/receipts').filter(c => c.body.preparationIds[0] === 'prep-fresh').length, 0, 'no receipt for another file');
  assert.equal(offlineDownloads.getSnapshot().entries[0]!.changedOnServer, true);
  assert.deepEqual(device.removed, []);
});

test('a forgotten receipt whose title is still being prepared is left for the next renewal', async () => {
  const {offlineDownloads} = await bridge();
  const {service, s} = forgotten({...preparation('prep-fresh', 'item-1'), state: 'preparing'});
  offlineDownloads.connect(s.api as any, service as unknown as DownloadsService, A);
  await settle();

  assert.equal(device.metaOf('prep-a').receipt.receiptId, 'r-a');
  assert.notEqual(device.metaOf('prep-a').changed, true);
  assert.deepEqual(device.removed, []);
});

async function offlineDisplayFixture(options:{expiresAt?:string;floor?:number}={}) {
  const {offlineDownloads}=await bridge(),owner=ownerKeyOf(A);
  device.state=JSON.stringify({version:3,keys:{'key-1':KEYS().keys[0]!.publicKey},resume:{[owner]:{'item-1':42}},clockFloor:options.floor??0});
  device.seed({id:'prep-a',itemId:'item-1',dir:'file:///downloads/prep-a/',quality:'opaque-quality-id',meta:{owner,receipt:receipt({id:'r-a',viewer:A,itemId:'item-1',preparationId:'prep-a',expiresAt:options.expiresAt}),extras:['backdrop']}});
  extra('prep-a').set('bundle.json',JSON.stringify({preparationId:'prep-a',itemId:'item-1',media:{container:'mkv',videoCodec:'hevc',audioCodec:'truehd',width:3840,height:2160}}));
  offlineDownloads.adopt(A);await offlineDownloads.open();
  return {offlineDownloads,entry:offlineDownloads.getSnapshot().entries[0]!};
}

test('typed offline facts use only the owned cached bundle and item bookmark, with no inferred completion', async () => {
  const {offlineDownloads,entry}=await offlineDisplayFixture();
  assert.equal(offlineDownloads.playabilityReason(entry),null);
  assert.equal(offlineDownloads.playable(entry),'');
  assert.deepEqual(await offlineDownloads.displayFacts(entry),{
    resumePositionSeconds:42,backdropUrl:'file:///downloads/prep-a/extras/backdrop',
    media:{container:'mkv',videoCodec:'hevc',audioCodec:'truehd',width:3840,height:2160},
  });
  extra('prep-a').set('bundle.json',JSON.stringify({preparationId:'prep-a',itemId:'other-item',media:{height:720}}));
  assert.equal((await offlineDownloads.displayFacts(entry)).media,undefined,'a bundle for another item supplies no quality facts');
  extra('prep-a').set('bundle.json','{broken');
  const unknown=await offlineDownloads.displayFacts(entry);
  assert.equal(unknown.media,undefined);assert.equal('completed'in unknown,false);
  offlineDownloads.adopt(B);
  await assert.rejects(offlineDownloads.displayFacts(entry));
});

test('typed needs-check-in uses the verified receipt clock floor, with the same legacy message decision', async () => {
  const {offlineDownloads,entry}=await offlineDisplayFixture({expiresAt:'2030-01-01T00:00:00.000Z',floor:Date.parse('2031-01-01T00:00:00.000Z')});
  assert.equal(offlineDownloads.playabilityReason(entry),'needs_check_in');
  assert.equal(offlineDownloads.playable(entry),serviceText('downloads.offline.needsCheckIn'));
});

test('cached display and local media reads reject an owner change while native reads are pending', async () => {
  const originalExtra=nativeDownloads.readExtra,originalFile=nativeDownloads.localFile;
  try {
    let release!:(value:string)=>void;
    const a=await offlineDisplayFixture();
    nativeDownloads.readExtra=()=>new Promise<string>(resolve=>{release=resolve;});
    const reading=a.offlineDownloads.displayFacts(a.entry),rejected=assert.rejects(reading);
    a.offlineDownloads.adopt(B);release('{}');await rejected;
    nativeDownloads.readExtra=originalExtra;
    const b=await offlineDisplayFixture();
    let releaseFile!:(value:{url:string;durationSeconds:number})=>void;
    nativeDownloads.localFile=()=>new Promise(resolve=>{releaseFile=resolve;});
    const media=b.offlineDownloads.localMedia(b.entry),mediaRejected=assert.rejects(media);
    b.offlineDownloads.adopt(B);releaseFile({url:'file:///downloads/prep-a/media',durationSeconds:1200});await mediaRejected;
  } finally {nativeDownloads.readExtra=originalExtra;nativeDownloads.localFile=originalFile;}
});

test('private cached title keeps typed duration and episode facts after native JSON readback', async () => {
  const {offlineDownloads}=await bridge(),owner=ownerKeyOf(A);
  const {durationMs:_wire,...title}=TITLE();
  const cached={...title,kind:'episode',durationSeconds:1200,parentTitle:'A series',groupNumber:2,number:3};
  device.seed({id:'prep-a',itemId:'item-1',meta:{owner,title:cached}});
  offlineDownloads.adopt(A);await offlineDownloads.open();
  const entry=offlineDownloads.getSnapshot().entries[0]!;
  assert.deepEqual(entry.info,cached);
  assert.equal(entry.info?.durationSeconds,1200);
  assert.equal(entry.info?.groupNumber,2);assert.equal(entry.info?.number,3);
});

test('unfinished audiobook progress keeps its resume and file even with delete-watched and auto-next enabled', async () => {
  const {offlineDownloads} = await bridge(), owner = ownerKeyOf(A);
  device.seed({id: 'book-copy', itemId: 'book-part', meta: {owner, kind: 'audiobook_file'}});
  offlineDownloads.adopt(A); await offlineDownloads.open();
  await offlineDownloads.setSettings({deleteWatched: true, autoNext: true});
  offlineDownloads.recordProgress({entryId: 'book-copy', itemId: 'book-part', playId: 'book-play', positionSeconds: 33480,
    durationSeconds: 36000, state: 'paused', ended: false});
  await settle();
  const saved = JSON.parse(device.state);
  assert.equal(saved.resume[owner]['book-part'], 33480);
  assert.equal(saved.plays[owner]['book-play'].ended, undefined);
  assert.equal(saved.nextAfter[owner]?.['book-part'], undefined);
  assert.deepEqual(device.removed, []);
  assert.ok(device.entries.has('book-copy'));
});

test('near-end video keeps its unfinished resume while an actual end resets it', async () => {
  const {offlineDownloads} = await bridge(), owner = ownerKeyOf(A);
  device.seed({id: 'movie-copy', itemId: 'movie', meta: {owner, kind: 'movie'}});
  offlineDownloads.adopt(A); await offlineDownloads.open();
  const progress = {entryId: 'movie-copy', itemId: 'movie', playId: 'movie-play', positionSeconds: 930, durationSeconds: 1000};
  offlineDownloads.recordProgress({...progress, state: 'paused', ended: false}); await settle();
  assert.equal(JSON.parse(device.state).resume[owner].movie, 930);
  offlineDownloads.recordProgress({...progress, positionSeconds: 1000, state: 'ended', ended: true}); await settle();
  const saved = JSON.parse(device.state);
  assert.equal(saved.resume[owner].movie, 0);
  assert.equal(saved.plays[owner]['movie-play'].ended, true);
  assert.deepEqual(device.removed, [], 'delete-watched remains opt-in');
});
