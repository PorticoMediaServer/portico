import test from 'node:test';
import assert from 'node:assert/strict';
import {device} from './react-native-stub.mjs';
import {OfflineDownloads, offlineOwnerKey} from '../platform/offline-downloads';

const viewer = {authority: 'local', serverId: 'srv_1', accountId: 'acc_1', profileId: 'pro_1'};
const day = 86_400_000;
const film = {id: 'dl_1', deviceId: 'dev_1', target: {kind: 'movie', id: 'itm_film', title: 'Film'}, quality: 'original', selection: 'all', paused: false,
  counts: {titles: 1, ready: 1, waiting: 0, failed: 0}, createdAt: '2026-11-11T10:00:00Z'};
type Sent = {path: string; method: string; body: unknown};

/** The new server as the device meets it: a sync answer chosen by the test, and the routes a fetch reads. */
function server(sync: () => unknown) {
  const sent: Sent[] = [];
  let reachable = true;
  const api = {baseUrl: 'https://server.test', mediaUrl: (path: string) => 'https://server.test' + path,
    request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
      if (!reachable) throw new TypeError('offline');
      sent.push({path, method, body});
      if (path === '/v1/downloads/sync') return sync() as T;
      if (path === '/v1/downloads') return {items: [film]} as T;
      if (path === '/v1/downloads/dl_1') return (method === 'DELETE' ? undefined : film) as T;
      if (path === '/v1/downloads/dl_1/items/itm_film') return {item: {id: 'itm_film', kind: 'movie', title: 'Film', year: 2020, addedAt: '', images: {poster: '/v1/images/p'}, state: {}, libraryId: 'lib_1'},
        versionId: 'ver_1', quality: 'original', files: [{url: '/v1/media/g1/file/1', sizeBytes: 4096, container: 'mkv'}], audio: [],
        subtitles: [{id: 'sub_1', format: 'srt', source: 'sidecar', language: 'en', forced: false, sdh: false, default: true, url: '/v1/media/g1/subtitles/sub_1'}], chapters: [], markers: []} as T;
      if (path === '/v1/items/state:batch') return {items: [], plays: []} as T;
      throw new Error('unexpected ' + method + ' ' + path);
    }};
  return {sent, api, offline: () => { reachable = false; }, online: () => { reachable = true; }};
}
const settle = async () => { for (let n = 0; n < 20; n++) await new Promise(done => setImmediate(done)); };
const ready = (store: OfflineDownloads) => { const entry = device.entries.get('dl-dl_1-itm_film'); store['accept']({...entry, state: 'ready', bytesDone: entry.bytes}); };

test('at a contact the device fetches what the server offers, with its poster and subtitles, and plays it for the 30 days the server gave, not longer even with the clock turned back', async t => {
  device.reset();
  const now = Date.now(), until = new Date(now + 30 * day).toISOString();
  const {api} = server(() => ({playableUntil: until, playableForMs: 30 * day, remove: [], fetch: [{downloadId: 'dl_1', itemId: 'itm_film'}]}));
  const store = new OfflineDownloads();
  t.after(() => store.dispose());
  store.connect(api, viewer);
  await settle();
  assert.deepEqual(device.started.map(one => [one.id, one.url, one.bytes]), [['dl-dl_1-itm_film', 'https://server.test/v1/media/g1/file/1', 4096]]);
  assert.equal(device.metaOf('dl-dl_1-itm_film').owner, offlineOwnerKey(viewer));
  assert.equal(device.extras.get('dl-dl_1-itm_film/poster'), 'from https://server.test/v1/images/p');
  assert.equal(device.extras.get('dl-dl_1-itm_film/sub-sub_1-0.srt'), 'from https://server.test/v1/media/g1/subtitles/sub_1');

  ready(store);
  const entry = store.getSnapshot().entries[0]!;
  assert.equal(store.playabilityReason(entry), null);
  const realNow = Date.now;
  try {
    Date.now = () => now + 31 * day;
    assert.equal(store.playabilityReason(entry), 'needs_check_in');
    Date.now = () => now;
    assert.equal(store.playabilityReason(entry), 'needs_check_in', 'turning the clock back gives no more time');
  } finally { Date.now = realNow; }
});

test('the server takes away what a profile may no longer see, and removing a download of one title here removes it on the server, at the next contact when out of reach', async t => {
  device.reset();
  let answer: unknown = {playableUntil: new Date(Date.now() + 30 * day).toISOString(), playableForMs: 30 * day, remove: [], fetch: [{downloadId: 'dl_1', itemId: 'itm_film'}]};
  const {sent, api, offline, online} = server(() => answer);
  const store = new OfflineDownloads();
  t.after(() => store.dispose());
  store.connect(api, viewer);
  await settle();
  answer = {...answer as object, fetch: [], remove: [{downloadId: 'dl_1', itemId: 'itm_film', reason: 'no_access'}]};
  store.wake();
  await settle();
  assert.deepEqual(device.removed, ['dl-dl_1-itm_film']);

  // Fetched again, then removed by the person while the server is out of reach.
  answer = {...answer as object, remove: [], fetch: [{downloadId: 'dl_1', itemId: 'itm_film'}]};
  store.wake();
  await settle();
  offline();
  await store.remove(store.getSnapshot().entries[0]!);
  assert.equal(sent.some(one => one.method === 'DELETE'), false);
  online();
  answer = {...answer as object, fetch: []};
  store.wake();
  await settle();
  assert.deepEqual(sent.filter(one => one.method === 'DELETE').map(one => one.path), ['/v1/downloads/dl_1']);
  const deleteAt = sent.findIndex(one => one.method === 'DELETE'), syncAt = sent.findLastIndex(one => one.path === '/v1/downloads/sync');
  assert.ok(deleteAt < syncAt, 'the removal reaches the server before the device says what it holds');
});

test('a play made offline is sent through the state batch at the next contact, with how long ago it was made, and sent once', async t => {
  device.reset();
  const {sent, api, offline, online} = server(() => ({playableUntil: new Date(Date.now() + 30 * day).toISOString(), playableForMs: 30 * day, remove: [], fetch: [{downloadId: 'dl_1', itemId: 'itm_film'}]}));
  const store = new OfflineDownloads();
  t.after(() => store.dispose());
  store.connect(api, viewer);
  await settle();
  ready(store);
  const entry = store.getSnapshot().entries[0]!;
  offline();
  const media = await store.localMedia(entry);
  store.recordProgress({entryId: entry.id, itemId: entry.itemId, playId: media.playId, positionSeconds: 600, durationSeconds: 5400, state: 'paused', ended: false});
  await settle();
  assert.equal(store.getSnapshot().pendingProgress, 1);
  online();
  store.wake();
  await settle();
  const batches = sent.filter(one => one.path === '/v1/items/state:batch');
  assert.equal(batches.length, 1);
  const play = (batches[0]!.body as {plays: {itemId: string; playId: string; positionMs: number; playedMs: number; madeAgoMs: number}[]}).plays[0]!;
  assert.deepEqual([play.itemId, play.playId, play.positionMs, play.playedMs], ['itm_film', media.playId, 600_000, 600_000]);
  assert.ok(play.madeAgoMs >= 0);
  assert.equal(store.getSnapshot().pendingProgress, 0);
  store.wake();
  await settle();
  assert.equal(sent.filter(one => one.path === '/v1/items/state:batch').length, 1);
});

test('removing one episode of a season tells the server to drop that title, and a server without that route leaves it to this device', async t => {
  device.reset();
  const season = {...film, target: {kind: 'season', id: 'itm_s1', title: 'Season 1'}, counts: {titles: 2, ready: 2, waiting: 0, failed: 0}};
  for (const missing of [false, true]) {
    const sent: Sent[] = [];
    const api = {baseUrl: 'https://server.test', mediaUrl: (path: string) => 'https://server.test' + path,
      request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
        sent.push({path, method, body});
        if (path === '/v1/downloads/sync') return {playableUntil: new Date(Date.now() + 30 * day).toISOString(), playableForMs: 30 * day, remove: [],
          fetch: sent.filter(one => one.path === '/v1/downloads/sync').length === 1 ? [{downloadId: 'dl_1', itemId: 'itm_film'}] : []} as T;
        if (path === '/v1/downloads') return {items: [season]} as T;
        if (path === '/v1/downloads/dl_1') return season as T;
        if (path === '/v1/downloads/dl_1/items/itm_film' && method === 'DELETE') {
          if (missing) throw Object.assign(new Error('method not allowed'), {status: 405});
          return undefined as T;
        }
        return (await server(() => undefined).api.request<T>(path, method, body));
      }};
    const store = new OfflineDownloads();
    t.after(() => store.dispose());
    store.connect(api, viewer);
    await settle();
    await store.remove(store.getSnapshot().entries[0]!);
    assert.deepEqual(sent.filter(one => one.method === 'DELETE').map(one => one.path), ['/v1/downloads/dl_1/items/itm_film']);
    store.wake();
    await settle();
    assert.equal(sent.filter(one => one.method === 'DELETE').length, 1, 'nothing is kept to say again: the server has it, or has no such route');
    store.dispose();
    device.reset();
  }
});

test('another viewer\'s downloads on this device are never listed or played, and are removed only as the confirm drew them', async t => {
  device.reset();
  device.seed({id: 'dl-other', itemId: 'itm_x', bytes: 500, meta: {owner: offlineOwnerKey({...viewer, profileId: 'pro_2'}), downloadId: 'dl_x'}});
  const store = new OfflineDownloads();
  t.after(() => store.dispose());
  store.adopt(viewer);
  await store.open();
  const shown = store.getSnapshot();
  assert.deepEqual(shown.entries, []);
  assert.equal(shown.otherBytes, 500);
  await assert.rejects(store.removeOthersConfirmed({revision: shown.othersRevision! + 1, bytes: 500}), {code: 'conflict'});
  await store.removeOthersConfirmed({revision: shown.othersRevision!, bytes: 500});
  assert.deepEqual(device.removed, ['dl-other']);
});
