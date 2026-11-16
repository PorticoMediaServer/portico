import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {offlineModule} from './helpers/offline-module.mjs';
import {device, NativeModules, NativeEventEmitter, Platform} from './react-native-stub.mjs';

// Reading rule 46: a book downloaded from the rebuilt server reads with no server, and the place and
// the highlights made meanwhile reach the server later. The real reading and downloads modules run;
// only the native file reader and the server are stand-ins.
const services = {serviceText: (id: string) => id};
const core = await offlineModule(new URL('../../../packages/client-core/src/downloads.ts', import.meta.url), {
  '../../contracts/src/types.ts': {canonicalQualityLadder: []},
  '../../contracts/src/routes.ts': {routePrefix: () => '', encodedRoutePath: () => ''},
  './presentation/service-text.ts': services,
});
const routeCrypto = {random: (length: number) => new Uint8Array(length), sha256: async (value: Uint8Array) => new Uint8Array(createHash('sha256').update(value).digest())};
const boundary = {NativeModules, NativeEventEmitter, Platform};
const file = new TextEncoder().encode('PK the downloaded book, byte for byte.');
const native = NativeModules.PorticoDownloads as Record<string, any>;
const opened: string[] = [];
native.publicationOpen = async (_handle: string, id: string, _owner: string, digest: string) => {opened.push(`${id}:${digest}`); return file.length;};
native.publicationRead = async (_handle: string, _operation: number, offset: number, length: number) => Buffer.from(file.subarray(offset, offset + length)).toString('base64');
native.publicationCancel = () => {}; native.publicationClose = () => {};
// A whole-file book never asks for a ZIP member: the reader finds those in the bytes itself.
native.publicationMember = native.publicationMemberRead = async () => {throw new Error('unexpected member read');};
const reading = await offlineModule(new URL('../platform/offline-reading.ts', import.meta.url), {
  'react-native': boundary, '../src-next/platform/server-connections': {nativeRouteCrypto: routeCrypto},
});
const {OfflineDownloads, offlineOwnerKey} = await offlineModule(new URL('../platform/offline-downloads.ts', import.meta.url), {
  './offline-reading': reading,
  './offline-subtitle-files': await offlineModule(new URL('../platform/offline-subtitle-files.ts', import.meta.url)),
  './receipt-clock': await offlineModule(new URL('../platform/receipt-clock.ts', import.meta.url)),
  'react-native': boundary,
  '../src-next/platform/server-connections': {nativeRouteCrypto: routeCrypto},
  '../../../packages/client-core/src/index': {...core, DownloadsService: class {}, getDownloadRequest: () => {throw new Error('unexpected container request');}},
  '../../../packages/client-core/src/presentation/service-text': services,
});

const A = {authority: 'local', accountId: 'account', profileId: 'profile-a', serverId: 'server'};
const owner = offlineOwnerKey(A), digest = 'c'.repeat(64), fingerprint = '0123456789abcdef';
const active = new Set<any>();
afterEach(() => {for (const store of active) store.dispose(); active.clear(); device.reset(); opened.length = 0;});

async function downloaded() {
  device.reset();
  device.state = JSON.stringify({version: 3, settings: {wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0}, reading: {}, plays: {}, resume: {}, keys: {},
    wanted: {}, wantedPublications: {}, nextAfter: {}, renewed: {}, clockFloor: 0, permissions: {[owner]: Date.now() + 86_400_000}});
  device.seed({id: 'copy-1', itemId: 'book-1', title: 'A Book', bytes: file.length, bytesDone: file.length, sha256: digest,
    meta: {downloadId: 'dl-1', owner, kind: 'ebook', book: {format: 'epub', versionId: 'ver-1', revision: fingerprint}}});
  const store = new OfflineDownloads(); active.add(store); store.adopt(A); await store.open();
  return store;
}
const entryOf = (store: any) => store.getSnapshot().entries.find((entry: any) => entry.id === 'copy-1');
const scope = (paused = false) => ({signal: new AbortController().signal, current: () => true, paused: () => paused});
const location = {format: 'epub', cfi: 'epubcfi(/6/4!/4/2)', section: 'ch1.xhtml', progression: .4};

test('a book downloaded from the rebuilt server opens with no server, from its own file, as the version and copy places are made on', async () => {
  const store = await downloaded(), entry = entryOf(store);
  assert.deepEqual(store.publicationSource(entry), {sourceId: 'ver-1', revision: fingerprint});
  const session = await store.readingSession(entry, scope());
  assert.equal(session.manifest.wholeFile, true); assert.equal(session.manifest.format, 'epub'); assert.equal(session.manifest.revision, fingerprint);
  assert.deepEqual(opened, ['copy-1:' + digest]);
  assert.deepEqual(await session.readRange({start: 3, endExclusive: 6}), file.subarray(3, 6));
  await session.close();
});

test('a place and a highlight made with no server are kept, and reach the server later as of when they were made', async () => {
  const store = await downloaded(), session = await store.readingSession(entryOf(store), scope());
  assert.equal((await session.saveProgress({expectedRevision: 0, location})).saved, true);
  await session.mutateAnnotation({operation: 'upsert', expectedRevision: 0, id: 'note-1', kind: 'highlight', colour: 'green', location, text: 'marked words', note: ''});
  const listed = await session.annotations('');
  assert.deepEqual(listed.entries.map((entry: any) => [entry.id, entry.colour, entry.text]), [['note-1', 'green', 'marked words']]);
  // Opened again, still offline: the same place and the same highlight.
  await session.close();
  const again = await store.readingSession(entryOf(store), scope());
  assert.deepEqual((await again.progress()).progress?.location, location);
  assert.equal((await again.annotations('')).entries.length, 1);

  const calls: {path: string; method: string; body: any}[] = [];
  Reflect.set(store, 'api', {baseUrl: 'http://server.test', mediaUrl: (path: string) => path, request: async (path: string, method: string, body: any) => {
    calls.push({path, method, body});
    if (path === '/v1/annotations:batch') return {results: body.changes.map((change: any) => ({id: change.id, annotation: {...change, createdAt: '2026-11-16T00:00:00Z', updatedAt: '2026-11-16T00:00:00Z'}}))};
    return {};
  }});
  await Reflect.get(store, 'sendBookPlaces').call(store);
  await Reflect.get(store, 'sendBookNotes').call(store);
  const place = calls.find(call => call.path === '/v1/items/book-1/state')!;
  assert.equal(place.method, 'PUT'); assert.equal(typeof place.body.madeAgoMs, 'number');
  assert.deepEqual(JSON.parse(place.body.position), {...location, fingerprint});
  const [change] = calls.find(call => call.path === '/v1/annotations:batch')!.body.changes;
  assert.equal(change.itemId, 'book-1'); assert.equal(change.madeOn, fingerprint); assert.equal(change.delete, false); assert.equal(typeof change.madeAgoMs, 'number');
  // Sent once: nothing waits any more, and the highlight stays listed as the server now has it.
  calls.length = 0;
  await Reflect.get(store, 'sendBookPlaces').call(store); await Reflect.get(store, 'sendBookNotes').call(store);
  assert.deepEqual(calls, []);
  assert.equal((await again.annotations('')).entries.length, 1);
  await again.close();
});

test('with history paused, reading a downloaded book keeps no place', async () => {
  const store = await downloaded(), session = await store.readingSession(entryOf(store), scope(true));
  assert.equal((await session.saveProgress({expectedRevision: 0, location})).saved, false);
  assert.equal((await session.progress()).progress, null);
  await session.close();
});

test('a download from the rebuilt server, film or book, tells the server once it is whole here, and tries again when it could not', async () => {
  device.reset();
  device.state = JSON.stringify({version: 3, settings: {wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0}, reading: {}, plays: {}, resume: {}, keys: {},
    wanted: {}, wantedPublications: {}, nextAfter: {}, renewed: {}, clockFloor: 0, permissions: {[owner]: Date.now() + 86_400_000}});
  device.seed({id: 'film', itemId: 'film-1', bytes: 10, bytesDone: 10, sha256: digest, meta: {downloadId: 'dl-2', owner, kind: 'movie'}});
  device.seed({id: 'half', itemId: 'film-2', state: 'downloading', bytes: 10, bytesDone: 4, meta: {downloadId: 'dl-3', owner, kind: 'movie'}});
  const store = new OfflineDownloads(); active.add(store); store.adopt(A); await store.open();
  const sent: string[] = []; let reachable = false;
  Reflect.set(store, 'api', {baseUrl: 'http://server.test', mediaUrl: (path: string) => path, request: async (path: string, method: string) => {
    if (!reachable) throw Object.assign(new Error('offline'), {code: 'network_error'});
    sent.push(`${method} ${path}`); return undefined;
  }});
  const send = () => Reflect.get(store, 'sendReceived').call(store) as Promise<void>;
  await send();
  assert.deepEqual(sent, []); assert.equal(device.metaOf('film').received, undefined);
  reachable = true; await send(); await send();
  // Only the whole one, and only once; the one still arriving is not reported.
  assert.deepEqual(sent, ['POST /v1/downloads/dl-2/items/film-1/received']);
  assert.equal(device.metaOf('film').received, true);
});
