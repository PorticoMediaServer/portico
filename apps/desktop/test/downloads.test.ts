import assert from 'node:assert/strict';
import test from 'node:test';
import {desktopDownloads, downloadArtwork} from '../src/downloads.ts';

const item = {downloadId: 'dl_1', itemId: 'itm_1', versionId: 'ver_1', quality: 'original',
  item: {id: 'itm_1', kind: 'movie', title: 'A film', images: {poster: '/v1/images/p', backdrop: '/v1/images/b'}, state: {watched: false, positionMs: 600_000.4, playCount: 0, favorite: false, inList: false, notInterested: false}, durationMs: 60_000, versions: [{id: 'ver_1', durationMs: 60_000, parts: [{durationMs: 60_000}]}]},
  files: [{url: '/v1/media/g/file/1', sizeBytes: 1000, container: 'mkv'}],
  subtitles: [{id: 's0', source: 'embedded', format: 'subrip', language: 'en', forced: false, sdh: false, default: false, deliveries: []},
    {id: 'f1', source: 'sidecar', format: 'srt', language: 'fr', forced: false, sdh: false, default: false, deliveries: [], url: '/v1/media/g/subtitles/f1'}], chapters: [], markers: [], audio: []};

/** The server (sync, the item's files, received, remove) and the app's store, both recorded. */
function fakes() {
  const server: string[] = [], app: [string, Record<string, unknown> | undefined][] = [];
  let entries: Record<string, unknown>[] = [];
  let plays = [{itemId: 'itm_1', playId: 'play_1', positionMs: 30_000, playedMs: 31_000, madeAtMs: Date.now() - 5000}];
  const api = {
    mediaUrl: (path: string) => 'http://server.test' + path,
    async request(path: string, method = 'GET', body?: unknown) {
      server.push(`${method} ${path}`);
      if (path === '/v1/items/state:batch') server.push(JSON.stringify(body));
      if (path === '/v1/downloads/sync') return {remove: [], fetch: [{downloadId: 'dl_1', itemId: 'itm_1'}], playableForMs: 30 * 86_400_000, playableUntil: '2099-01-01T00:00:00Z'};
      if (path === '/v1/downloads/dl_1/items/itm_1') return item;
      return undefined;
    },
  };
  const invoke = async <T,>(command: string, args?: Record<string, unknown>) => {
    app.push([command, args]);
    if (command === 'downloads_list') return {entries, playableUntilMs: Date.now() + 1000} as T;
    if (command === 'downloads_plays') return plays as T;
    if (command === 'downloads_sent') plays = plays.filter(p => !(args as {playIds: string[]}).playIds.includes(p.playId));
    if (command === 'downloads_fetch') entries = [{serverId: 'srv_1', profileId: 'prf_1', downloadId: 'dl_1', itemId: 'itm_1', quality: 'original',
      title: {kind: 'movie', title: 'A film', onServer: true}, files: [{name: '1.mkv', bytes: 1000, done: 1000}], state: 'ready', received: false, addedMs: 1}];
    if (command === 'downloads_change' && (args as {action: string}).action === 'remove') entries = [];
    if (command === 'downloads_artwork') return new Uint8Array([137, 80, 78, 71]).buffer as T;
    if (command === 'downloads_storage') return {totalBytes: 1000_000, freeBytes: 400_000, usedBytes: 3000, otherDownloadsBytes: 2000} as T;
    if (command === 'downloads_others') return {snapshotToken: 'tok_1', bytes: 2000} as T;
    return undefined as T;
  };
  return {server, app, api, invoke};
}
const scope = {authority: 'local' as const, serverId: 'srv_1', accountId: 'acc_1', profileId: 'prf_1', sessionFamilyId: 'fam_1'};
const settle = () => new Promise(resolve => setTimeout(resolve, 20));

test('what the server says to fetch is fetched into the app, and the server is told once it is all here', async () => {
  const f = fakes(), life = new AbortController();
  const binding = await desktopDownloads(f.invoke)({runtime: {scope} as never, api: f.api as never, isCurrent: () => true, signal: life.signal});
  await settle();
  const fetch = f.app.find(([command]) => command === 'downloads_fetch')![1] as {ask: {files: {url: string; bytes: number}[]; positionMs: number; artwork?: string}};
  assert.deepEqual((fetch.ask as unknown as {subtitles: unknown}).subtitles, [{url: 'http://server.test/v1/media/g/subtitles/f1', format: 'srt', language: 'fr'}],
    'the subtitle beside the file comes along; the one inside it is in the file already');
  assert.equal(fetch.ask.artwork, 'http://server.test/v1/images/p?w=480&h=480', "a film's poster, at the server's small size");
  assert.deepEqual(fetch.ask.files, [{url: 'http://server.test/v1/media/g/file/1', bytes: 1000, container: 'mkv'}], 'the store gets the whole address, never a path');
  assert.equal(fetch.ask.positionMs, 600_000, 'Play starts where the person was when it was downloaded');
  assert.ok(f.server.includes('POST /v1/downloads/dl_1/items/itm_1/received'));
  assert.ok(f.app.some(([command, args]) => command === 'downloads_change' && (args as {action: string}).action === 'received'));
  const batch = JSON.parse(f.server[f.server.indexOf('POST /v1/items/state:batch') + 1]!) as {plays: {playId: string; positionMs: number; madeAgoMs: number}[]};
  assert.equal(batch.plays[0]!.playId, 'play_1', 'a play made with or without the server is sent at the next sync');
  assert.ok(batch.plays[0]!.madeAgoMs >= 5000);
  assert.ok(f.app.some(([command, args]) => command === 'downloads_sent' && (args as {playIds: string[]}).playIds[0] === 'play_1'));
  assert.ok(f.app.some(([command, args]) => command === 'downloads_permission' && (args as {untilMs: number}).untilMs > Date.now() + 29 * 86_400_000));
  const row = binding.localDownloads.getSnapshot().items[0]!;
  assert.equal(row.state, 'ready'); assert.equal(row.offlineAccess, 'valid'); assert.equal(row.percent, 100);
  assert.equal((await binding.localDownloads.getArtwork(row.id, life.signal))?.size, 4, "the kept picture's bytes");
  assert.equal(binding.localDownloads.getSnapshot().storage?.otherDownloadsBytes, 2000, "other profiles' space is shown");
  const fetchAsk = f.app.find(([command]) => command === 'downloads_fetch')![1] as {ask: {accountId: string}};
  assert.equal(fetchAsk.ask.accountId, 'acc_1', 'each download records its account, so only its other profiles count');
  const others = await binding.localDownloads.previewOtherProfiles(life.signal);
  assert.deepEqual([others.snapshotToken, others.bytes, others.scope], ['tok_1', 2000, scope]);
  await binding.localDownloads.removeOtherProfiles(others, life.signal);
  assert.deepEqual(f.app.find(([command]) => command === 'downloads_remove_others')![1], {serverId: 'srv_1', accountId: 'acc_1', profileId: 'prf_1', snapshotToken: 'tok_1'}, 'only what was shown');
  await binding.localDownloads.action(row.id, 0, 'remove', life.signal);
  assert.ok(f.server.includes('DELETE /v1/downloads/dl_1/items/itm_1'), 'taken out on the server too, so its sync does not bring it back');
  assert.equal(binding.localDownloads.getSnapshot().items.length, 0);
  binding.dispose(); life.abort();
});

test("each tile's picture fits its shape, and a title with none has none", () => {
  const images = {poster: '/p', backdrop: '/b', thumb: '/t', cover: '/c'};
  const title = (kind: string) => ({kind, title: 'x', onServer: true}) as never;
  assert.equal(downloadArtwork(title('episode'), images), '/t?w=480&h=480');
  assert.equal(downloadArtwork(title('episode'), {backdrop: '/b'}), '/b?w=480&h=480');
  assert.equal(downloadArtwork(title('album'), images), '/c?w=480&h=480');
  assert.equal(downloadArtwork(title('movie'), images), '/p?w=480&h=480');
  assert.equal(downloadArtwork(title('movie'), {}), undefined);
});
