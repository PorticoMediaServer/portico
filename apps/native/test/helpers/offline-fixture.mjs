import {offlineModule} from './offline-module.mjs';
import {downloaded} from '../../../../packages/client-core/test/helpers/downloads-next.ts';

export const viewer = {authority: 'local', accountId: 'account', profileId: 'profile', serverId: 'server'};
export const owner = JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId, viewer.serverId]);
export const digest = 'a'.repeat(64);
export const title = {kind: 'movie', title: 'Title', year: 2026, durationSeconds: 100,
  parentId: '', parentTitle: '', groupId: '', groupTitle: '', groupNumber: null, number: null, artist: '', onServer: true};
export const artifact = {kind: 'prepared', ref: 'file', fileName: 'file.mp4', bytes: 100, sha256: digest, estimated: false, container: 'mp4', contentType: 'video/mp4'};
export function preparation(id, itemId = 'item', quality = 'original', state = 'ready') {
  return {id, downloadId: 'dl_' + id, itemId, quality, state, title, artifact, libraryId: 'library', profileId: 'profile', origin: 'item',
    batchId: '', reason: '', revision: 1, actions: ['remove'], createdAt: '', updatedAt: '', readyAt: '', expiresAt: '',
    progress: {bytesDone: 0, bytesTotal: artifact.bytes, bytesTotalEstimated: false, percent: 0, etaSeconds: null}};
}
/** @returns {{promise: Promise<any>, resolve: (value?: any) => void, reject: (error?: unknown) => void}} */
export const deferred = () => {let resolve = /** @type {(value?: any) => void} */ (() => {}), reject = /** @type {(error?: unknown) => void} */ (() => {}); const promise = new Promise((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
export const settle = async () => {for (let i = 0; i < 12; i++) await new Promise(done => setImmediate(done));};

// The native filesystem and server are explicit test ports.
export async function offlineFixture(options = {}) {
  const words = {serviceText: id => id};
  const routes = {
    routePrefix: name => {if (name !== 'get_download_artifact') throw new Error('Unexpected route ' + name); return '/v1/downloads/artifacts/';},
    encodedRoutePath: (name, params) => {if (name !== 'get_download_artifact') throw new Error('Unexpected route ' + name); return '/v1/downloads/artifacts/' + params.grant;},
  };
  const downloads = await offlineModule(new URL('../../../../packages/client-core/src/downloads.ts', import.meta.url), {
    '../../contracts/src/types.ts': {canonicalQualityLadder: [{id: 'original'}, {id: 'small'}]},
    '../../contracts/src/routes.ts': routes, './presentation/service-text.ts': words,
  });
  /** @type {Set<(entry: any) => void>} */
  const listeners = new Set();
  /** @type {Map<string, any>} */
  const entries = new Map();
  /** @type {any[]} */ const calls = [];
  /** @type {any[]} */ const started = [];
  /** @type {string[]} */ const removed = [];
  /** @type {string[]} */ const writes = [];
  const disk = {state: options.state ?? '', freeBytes: 2 ** 40, usedBytes: 0, failRead: false, failWrite: false};
  let nativeGeneration = 0;
  const emit = entry => {for (const listener of listeners) listener({...entry});};
  const native = {
    async readState() {if (disk.failRead) throw new Error('read_failed'); return disk.state;},
    async writeState(json) {if (disk.failWrite) throw new Error('write_failed'); writes.push(json); disk.state = json;},
    async list() {return [...entries.values()].map(value => ({...value}));},
    async storage() {return {freeBytes: disk.freeBytes, usedBytes: disk.usedBytes};},
    async setCellular() {},
    async start(request) {
      started.push(request);
      const entry = {...request, state: 'downloading', intent: 'run', bytesDone: 0, addedAt: 1, publicationGeneration: String(++nativeGeneration)};
      entries.set(entry.id, entry); emit(entry); return entry;
    },
    async resume(request) {
      const entry = entries.get(request.id);
      if (!entry || entry.publicationGeneration !== request.expectedGeneration) throw Object.assign(new Error('generation_changed'), {code: 'generation_changed'});
      return native.start(request);
    },
    async pause(id) {const entry = entries.get(id); if (entry) {Object.assign(entry, {state: 'paused', intent: 'hold', publicationGeneration: String(++nativeGeneration)}); emit(entry);}},
    async remove(id) {removed.push(id); entries.delete(id); emit({id, state: 'removed'});},
    async removeGeneration(id, generation) {if (entries.get(id)?.publicationGeneration !== generation) throw Object.assign(new Error('generation_changed'), {code: 'generation_changed'}); await native.remove(id);},
    async setMeta(id, meta) {const entry = entries.get(id); if (!entry) throw new Error('not_found'); entry.meta = meta; emit(entry);},
    async readExtra(_id, _name) {return '';}, async writeExtra(_id, _name, _value) {}, async fetchExtra(_id, _name, _url) {return 'missing';},
    async localFile(id) {if (!entries.has(id)) throw new Error('not_found'); return {url: 'file:///downloads/' + id, durationSeconds: 100};},
  };
  const seed = (id, itemId = 'item', extra = {}) => {
    const entry = {id, itemId, title: 'Title', subtitle: '', quality: 'original', bytes: 100, bytesDone: 100,
      addedAt: 1, state: 'ready', intent: 'run', publicationGeneration: String(++nativeGeneration),
      ...extra, meta: JSON.stringify(extra.meta ?? {owner, downloadId: 'dl_' + id, bundle: 4})};
    entries.set(id, entry); return entry;
  };
  const crypto = {verify: async () => true, random: n => new Uint8Array(n), sha256: async () => new Uint8Array(32)};
  const contracts = options.contracts ?? {
    decodeGetReadingSessionsSessionIdProgressResponse: value => value,
    decodePutReadingSessionsSessionIdProgressResponse: value => value,
  };
  const reading = await offlineModule(new URL('../../platform/offline-reading.ts', import.meta.url), {
    'react-native': {NativeModules: {PorticoDownloads: native}, Platform: {OS: 'android', isTV: false}},
    '../../../packages/contracts/src': contracts,
    '../../../packages/client-core/src/reading': {ReadingError: class extends Error {}, parseReadingManifest: value => value, validateReadingLocation: () => {}},
    '../src-next/platform/server-connections': {nativeRouteCrypto: crypto},
  });
  const serviceListeners = new Set();
  /** @type {Map<string, (body: any) => unknown>} */
  const handlers = new Map();
  /** @type {{items: any[], requests: any[], phase: string}} */
  const snapshot = {items: [], requests: [], phase: 'ready'};
  const service = {
    getSnapshot: () => snapshot, subscribe: fn => {serviceListeners.add(fn); return () => serviceListeners.delete(fn);},
    publish() {for (const listener of serviceListeners) listener();},
    async downloaded(id) {
      calls.push({path: 'downloaded', id});
      const handler = handlers.get('downloaded');
      if (handler) return handler(id);
      const itemId = entries.get(id)?.itemId ?? snapshot.items.find(value => value.id === id)?.itemId
        ?? snapshot.requests.flatMap(value => value.request.items).find(value => value.preparation?.id === id)?.itemId ?? 'item';
      return {...downloaded, item: {...downloaded.item, id: itemId, title: title.title, state: {...downloaded.item.state, positionMs: 0}}};
    },
    async grant(_target) {return {url: downloaded.files[0].url, artifact};},
    async request() {return {items: [], accepted: 0, rejected: [], duplicate: false};},
    /** Exact preparation reads (OFF-04): routed through `handlers` like the API. */
    async lookupPreparations(ids) {calls.push({path: 'lookupPreparations', ids: [...ids]}); const handler = handlers.get('lookupPreparations'); return handler ? handler(ids) : [];},
  };
  const api = {
    baseUrl: 'https://server.invalid', mediaUrl: path => 'https://server.invalid' + path,
    async request(path, method, body) {
      calls.push({path, method, body});
      const handler = handlers.get(path); if (handler) return handler(body);
      if (path === '/v1/downloads/sync') return {playableUntil: new Date(Date.now() + 86400000).toISOString(), playableForMs: 86400000, fetch: [], remove: []};
      if (method === 'DELETE') return undefined;
      throw new Error('No test route: ' + path);
    },
  };
  const implementation = await offlineModule(new URL('../../platform/offline-downloads.ts', import.meta.url), {
    'react-native': {NativeModules: {PorticoDownloads: native}, Platform: {OS: 'android', isTV: false}, NativeEventEmitter: class {addListener(_name, fn) {listeners.add(fn); return {remove: () => listeners.delete(fn)};}}},
    './offline-reading': {...reading, ...options.reading},
    '../../../packages/client-core/src/index': {...downloads, DownloadsService: class {}, getDownloadRequest: (port, id, input = {}) => port.request('/v1/downloads/requests/' + id + '?limit=100' + (input.cursor ? '&cursor=' + encodeURIComponent(input.cursor) : ''))},
    '../../../packages/client-core/src/presentation/service-text': words,
  });
  const offline = implementation.offlineDownloads;
  return {offline, implementation, native, seed, emit, entries, disk, writes, started, removed, calls, handlers, api, service, snapshot, listeners};
}
