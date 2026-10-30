/**
 * A whole show or playlist asked for from this device: every member the server lists becomes
 * wanted here, however many there are. Members past the first page are read a bounded batch at a
 * time, each batch carrying on where the last one stopped, so a very large request is neither cut
 * short nor read from the start again each time its counts change.
 *
 * Drives the real `OfflineDownloads` against the native stub and a paging server.
 */
import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {device, emit, NativeModules} from './react-native-stub.mjs';
import type {DownloadsService} from '../../../packages/client-core/src/index';
import {parseDownloadPreparation} from '../../../packages/client-core/src/downloads.ts';
import {parseDownloadRequest} from '../../../packages/client-core/src/download-requests.ts';
import {OfflineDownloads} from '../platform/offline-downloads.ts';
import type * as OfflineModule from '../platform/offline-downloads.ts';

/** The native module's newer contract (batched meta writes, the files beside a download). The
 * shared stub predates the rewrite, so the four methods are added here. */
const nativeDownloads = NativeModules.PorticoDownloads as unknown as Record<string, any>;
const extraStore = new Map<string, Map<string, string>>();
const extra = (id: string): Map<string, string> => {
  let files = extraStore.get(id);
  if (!files) { files = new Map(); extraStore.set(id, files); }
  return files;
};
nativeDownloads.setMetas = async (metas: Record<string, string>) => {
  for (const [id, meta] of Object.entries(metas)) {
    const entry = device.entries.get(id);
    if (!entry) continue;
    device.entries.set(id, {...entry, meta});
    emit('PorticoDownloadChanged', {...device.entries.get(id)});
  }
};
nativeDownloads.writeExtra = async (id: string, name: string, text: string) => { extra(id).set(name, text); };
nativeDownloads.readExtra = async (id: string, name: string) => extra(id).get(name) ?? '';
nativeDownloads.fetchExtra = async (id: string, name: string, url: string) => { extra(id).set(name, url); return 'saved'; };

const VIEWER = {authority: 'local', accountId: 'account-a', profileId: 'profile-a', serverId: 'server-1'};
const OWNER = JSON.stringify([VIEWER.authority, VIEWER.accountId, VIEWER.profileId, VIEWER.serverId]);
const REQUEST = 'request-1';
const member = (n: number) => `item-${n}`;
const ARTIFACT = {kind: 'prepared', ref: 'file', fileName: 'f.mp4', bytes: 10, sha256: 'a'.repeat(64), contentType: 'video/mp4', container: 'mp4', estimated: false};
const preparation = (itemId: string, ready = false) => ({id: 'prep-' + itemId, itemId, libraryId: 'lib', profileId: 'profile-a', quality: 'original', origin: 'container',
  state: ready ? 'ready' : 'queued', reason: '', revision: 1, actions: ['remove'], batchId: '', artifact: ARTIFACT,
  title: {kind: 'episode', title: itemId, year: 2026, durationMs: 1000, parentId: '', parentTitle: '', groupId: '', groupTitle: '', groupNumber: null, number: null, artist: '', onServer: true},
  progress: {bytesDone: 0, bytesTotal: 10, bytesTotalEstimated: false, percent: 0, etaMs: null},
  createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-01-01T00:00:00.000Z', readyAt: '', expiresAt: ''});

/** The server's view of one container request: `total` members in order, read 100 at a time
 * after a member id, as `GET /v1/downloads/requests/{id}?limit&cursor` answers. */
function containerServer(initialTotal: number) {
  const state = {total: initialTotal, ready: 0, fail: undefined as ((cursor: string) => boolean) | undefined, stuck: false};
  const cursors: string[] = [];
  const page = (after: number) => {
    const items = Array.from({length: Math.min(100, Math.max(0, state.total - after))}, (_, i) => ({itemId: member(after + i), preparation: preparation(member(after + i), after + i < state.ready)}));
    const last = after + items.length;
    return {requestId: REQUEST, total: state.total, totalKnown: true, state: 'preparing', ready: state.ready, preparing: 0, queued: state.total - state.ready, failed: 0, paused: 0, items,
      ...(last < state.total ? {nextCursor: member(last - 1)} : {})};
  };
  const api = {
    baseUrl: 'http://server.test',
    mediaUrl: (p: string) => 'http://server.test' + p,
    async request<T>(path: string): Promise<T> {
      if (path === '/v1/downloads/receipt-keys') return {keys: []} as T;
      const url = new URL('http://server.test' + path);
      if (url.pathname.startsWith('/v1/downloads/preparations/prep-item-')) {
        const item = url.pathname.slice('/v1/downloads/preparations/prep-'.length), index = Number(item.slice('item-'.length));
        return preparation(item, index < state.ready) as T;
      }
      if (url.pathname !== '/v1/downloads/requests/' + REQUEST) throw new Error('no route: ' + path);
      const cursor = url.searchParams.get('cursor') ?? '';
      cursors.push(cursor);
      if (state.fail?.(cursor)) throw new Error('network unavailable');
      const answer = page(cursor ? Number(cursor.slice('item-'.length)) + 1 : 0);
      return (state.stuck && answer.nextCursor ? {...answer, nextCursor: cursor} : answer) as T;
    },
  };
  const listeners = new Set<() => void>();
  let prepared: unknown[] = [];
  const service = {
    getSnapshot: () => ({items: prepared, requests: [{request: parseDownloadRequest(page(0))}], busy: [] as string[], error: undefined}),
    subscribe: (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn); },
    async grant(preparation: {id: string; itemId: string}) {
      return {preparationId: preparation.id, itemId: preparation.itemId, url: '/grant/' + preparation.id, token: 't', issuedAt: '', expiresAt: '', replayWindowSeconds: 60, artifact: ARTIFACT};
    },
    publish() { for (const fn of listeners) fn(); },
    dispose() {},
  };
  /** The server finishes preparing the first `count` members. */
  const ready = (count: number) => {
    state.ready = count;
    prepared = Array.from({length: count}, (_, i) => parseDownloadPreparation(preparation(member(i), true)));
    service.publish();
  };
  return {api, service, state, cursors, ready};
}

type OfflineBridge = Pick<typeof OfflineModule, 'offlineDownloads'>;
let previous: OfflineBridge | undefined;
async function bridge(): Promise<OfflineBridge> {
  previous?.offlineDownloads.disconnect();
  previous?.offlineDownloads.dispose();
  device.reset();
  extraStore.clear();
  previous = {offlineDownloads: new OfflineDownloads()};
  return previous;
}
/** `connect` starts a 60-second interval; nothing may be left running when a test ends. */
afterEach(() => {
  previous?.offlineDownloads.disconnect();
  previous?.offlineDownloads.dispose();
  previous = undefined;
});
const wanted = (): string[] => Object.values((JSON.parse(device.state || '{}').wanted ?? {})[OWNER] ?? {}).map(value => (value as {itemId: string}).itemId);
/** Waits until no page has been asked for and nothing has been saved for a few turns. */
async function quiet(cursors: string[]) {
  for (let calm = 0, seen = -1, saved = ''; calm < 5;) {
    await new Promise(done => setTimeout(done, 5));
    if (cursors.length === seen && device.state === saved) calm++; else { calm = 0; seen = cursors.length; saved = device.state; }
  }
}

test('every member of a very large request is enrolled, a bounded batch and a bounded list at a time', async () => {
  const {offlineDownloads} = await bridge();
  const s = containerServer(5_443);
  let saves = 0, largest = 0;
  const firstSaveAfter: number[] = [];
  const write = NativeModules.PorticoDownloads.writeState;
  NativeModules.PorticoDownloads.writeState = async (json: string) => {
    saves++; firstSaveAfter.push(s.cursors.length);
    largest = Math.max(largest, Object.keys(JSON.parse(json).wanted?.[OWNER] ?? {}).length);
    return write(json);
  };
  try {
    offlineDownloads.connect(s.api as any, s.service as unknown as DownloadsService, VIEWER);
    await quiet(s.cursors);
    // The list of members waiting to be collected fills, and the reading waits there.
    assert.equal(wanted().length, 5_000);
    assert.equal(s.cursors.length, 49);
    assert.equal(offlineDownloads.getSnapshot().error, undefined);
    // The first six hundred are prepared and come to the device: the reading goes on to the end.
    s.ready(600);
    await quiet(s.cursors);
    assert.equal(device.started.length, 600);
    const waiting = wanted();
    assert.equal(waiting.length, 4_843);
    assert.ok(waiting.includes(member(5_442)) && waiting.includes(member(5_200)), 'members past the first fifty pages are wanted');
    assert.ok(!waiting.includes(member(0)) && waiting.includes(member(600)));
    // Every member was either collected or is waiting: none was skipped.
    assert.equal(new Set([...device.started.map((d: {itemId: string}) => d.itemId), ...waiting]).size, 5_443);
    // 54 continuation pages after the first hundred, none asked for twice.
    assert.equal(s.cursors.length, 54);
    assert.equal(new Set(s.cursors).size, 54);
    // Bounded: a batch is saved before the next reads on, and the waiting list stays within its
    // limit (a page may carry it at most one page past).
    assert.ok(saves >= 2 && firstSaveAfter[0]! <= 50, `first save after ${firstSaveAfter[0]} pages`);
    assert.ok(largest < 5_100, `the list reached ${largest}`);
    assert.equal(offlineDownloads.getSnapshot().error, undefined);
  } finally { NativeModules.PorticoDownloads.writeState = write; }
});

test('a change in the request\'s counts reads on from where the reading stopped, not from the start', async () => {
  const {offlineDownloads} = await bridge();
  const s = containerServer(1_250);
  offlineDownloads.connect(s.api as any, s.service as unknown as DownloadsService, VIEWER);
  await quiet(s.cursors);
  assert.equal(wanted().length, 1_250);
  const before = s.cursors.length;
  // An unchanged request asks for nothing more.
  s.service.publish();
  await quiet(s.cursors);
  assert.equal(s.cursors.length, before);
  // Members become ready: only the last page is looked at again.
  s.state.ready = 40;
  s.service.publish();
  await quiet(s.cursors);
  assert.deepEqual(s.cursors.slice(before), [member(1_199)]);
  // The server lists more members: they are read from the end of what was read.
  s.state.total = 1_480;
  s.service.publish();
  await quiet(s.cursors);
  assert.equal(new Set([...wanted(), ...device.started.map((value: {itemId: string}) => value.itemId)]).size, 1_480);
  assert.equal(wanted().length, 1_440);
  assert.deepEqual(device.started.map((value: {itemId: string}) => value.itemId).sort(), Array.from({length: 40}, (_, i) => member(i)).sort());
  assert.deepEqual(s.cursors.slice(before + 1), [member(1_199), member(1_299), member(1_399)]);
});

test('a failed page waits for its retry floor unless request counts change sooner', async () => {
  const {offlineDownloads} = await bridge();
  const s = containerServer(900);
  s.state.fail = cursor => cursor === member(499);
  offlineDownloads.connect(s.api as any, s.service as unknown as DownloadsService, VIEWER);
  await quiet(s.cursors);
  assert.equal(wanted().length, 500);
  const before = s.cursors.length;
  s.service.publish();
  await quiet(s.cursors);
  assert.equal(s.cursors.length, before, 'no retry while nothing has changed');
  s.state.fail = undefined;
  s.state.ready = 3;
  s.service.publish();
  await quiet(s.cursors);
  assert.equal(new Set([...wanted(), ...device.started.map((value: {itemId: string}) => value.itemId)]).size, 900);
  assert.equal(wanted().length, 897);
  assert.deepEqual(device.started.map((value: {itemId: string}) => value.itemId), [member(0), member(1), member(2)]);
  assert.equal(s.cursors[before], member(499), 'the reading resumes at the page that failed');
});

test('a server whose cursor never advances does not keep the device reading for ever', async () => {
  const {offlineDownloads} = await bridge();
  const s = containerServer(2_000);
  s.state.stuck = true;
  offlineDownloads.connect(s.api as any, s.service as unknown as DownloadsService, VIEWER);
  await quiet(s.cursors);
  assert.ok(s.cursors.length <= 2, `${s.cursors.length} pages read`);
});

test('a transient continuation failure is retried after its floor even with unchanged counts', async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.parse('2026-10-13T12:00:00Z')});
  const {offlineDownloads} = await bridge();
  const s = containerServer(900);
  s.state.fail = cursor => cursor === member(499);
  offlineDownloads.connect(s.api, s.service as unknown as DownloadsService, VIEWER);
  await quiet(s.cursors);
  assert.equal(wanted().length, 500);
  const before = s.cursors.length;
  s.state.fail = undefined;
  s.service.publish();
  await quiet(s.cursors);
  assert.equal(s.cursors.length, before, 'the retry floor avoids a tight loop');
  t.mock.timers.tick(60000);
  offlineDownloads.wake();
  await quiet(s.cursors);
  assert.equal(wanted().length, 900);
});

test('the backlog limit applies to first pages across many requests', async () => {
  const {offlineDownloads} = await bridge();
  const s = containerServer(100);
  const snapshot = s.service.getSnapshot;
  s.service.getSnapshot = () => ({...snapshot(), requests: Array.from({length: 70}, (_, r) => ({request: {
    ...snapshot().requests[0]!.request, requestId: 'request-' + r,
    items: Array.from({length: 100}, (_, i) => ({itemId: `item-${r}-${i}`, preparation: parseDownloadPreparation(preparation(`item-${r}-${i}`))})),
  }}))});
  offlineDownloads.connect(s.api, s.service as unknown as DownloadsService, VIEWER);
  await quiet(s.cursors);
  assert.equal(wanted().length, 5000);
});

test('another profile holding the same title does not suppress enrollment', async () => {
  const {offlineDownloads} = await bridge();
  device.seed({id: 'other-copy', itemId: member(0), meta: {owner: 'another-profile'}});
  const s = containerServer(1);
  offlineDownloads.connect(s.api, s.service as unknown as DownloadsService, VIEWER);
  await quiet(s.cursors);
  assert.deepEqual(wanted(), [member(0)]);
});

test('continuation state is scoped to the viewer even when request IDs repeat', async () => {
  const {offlineDownloads} = await bridge();
  const first = containerServer(250);
  offlineDownloads.connect(first.api, first.service as unknown as DownloadsService, VIEWER);
  await quiet(first.cursors);
  const next = containerServer(250), other = {...VIEWER, serverId: 'other-server'};
  offlineDownloads.connect(next.api, next.service as unknown as DownloadsService, other);
  await quiet(next.cursors);
  const owner = JSON.stringify([other.authority, other.accountId, other.profileId, other.serverId]);
  assert.equal(Object.keys(JSON.parse(device.state).wanted[owner]).length, 250);
  assert.deepEqual(next.cursors, [member(99), member(199)]);
});
