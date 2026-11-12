import test from 'node:test';
import assert from 'node:assert/strict';
import {keepDownloadsInStep, readDownloadedItem, reportDownloadPlay, type DownloadHoldings, type OfflinePermission} from '../src/device-downloads.ts';
import {isOutboxRoute, outboxBody} from '../src/outbox-routes.ts';

type Sent = {path: string; method: string; body: unknown};
const server = (answer: (sent: Sent) => unknown) => {
  const sent: Sent[] = [];
  return {sent, api: {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    const one = {path, method, body};
    sent.push(one);
    return answer(one) as T;
  }}};
};

test('at a contact the device says what it holds, removes what the server names, fetches what it offers, and renews everything', async () => {
  const {sent, api} = server(() => ({playableUntil: '2026-12-11T06:00:00Z', playableForMs: 2_592_000_000,
    remove: [{downloadId: 'dl_1', itemId: 'itm_kept', reason: 'no_access'}], fetch: [{downloadId: 'dl_2', itemId: 'itm_new'}]}));
  const removed: string[] = [], fetched: string[] = [];
  let renewed: OfflinePermission | undefined;
  const holdings: DownloadHoldings = {
    held: async () => [{downloadId: 'dl_1', itemId: 'itm_kept'}, {downloadId: 'dl_1', itemId: 'itm_other'}],
    remove: async title => { removed.push(title.itemId + ':' + title.reason); },
    fetch: async title => { if (title.itemId === 'itm_new') fetched.push(title.itemId); },
    renew: async permission => { renewed = permission; },
  };
  const done = await keepDownloadsInStep(api, holdings, () => 1_000);
  assert.deepEqual(sent, [{path: '/v1/downloads/sync', method: 'POST',
    body: {holds: [{downloadId: 'dl_1', itemId: 'itm_kept'}, {downloadId: 'dl_1', itemId: 'itm_other'}]}}]);
  assert.deepEqual(removed, ['itm_kept:no_access']);
  assert.deepEqual(fetched, ['itm_new']);
  assert.deepEqual(renewed, {receivedAt: 1_000, playableForMs: 2_592_000_000, playableUntil: '2026-12-11T06:00:00Z'});
  assert.deepEqual(done.failed, []);

  // A removal that fails (a file the system would not delete) does not hold back the rest; it is tried at the next contact.
  holdings.remove = async () => { throw new Error('busy'); };
  fetched.length = 0;
  const again = await keepDownloadsInStep(api, holdings, () => 2_000);
  assert.equal(again.failed.length, 1);
  assert.deepEqual(fetched, ['itm_new']);
  assert.equal(renewed?.receivedAt, 2_000);
});

test('a title of a download is read by both ids', async () => {
  const {sent, api} = server(() => ({files: []}));
  await readDownloadedItem(api, 'dl_1', 'itm_a/b');
  assert.equal(sent[0]!.path, '/v1/downloads/dl_1/items/itm_a%2Fb');
});

test('a play is sent through the state batch, and a batch kept out of reach says how long ago each edit and play was made', async () => {
  const {sent, api} = server(() => ({items: [], plays: []}));
  await reportDownloadPlay(api, {itemId: 'itm_film', playId: 'play-1', positionMs: 5_400_000, playedMs: 5_000_000});
  assert.deepEqual(sent[0], {path: '/v1/items/state:batch', method: 'POST',
    body: {items: [], plays: [{itemId: 'itm_film', playId: 'play-1', positionMs: 5_400_000, playedMs: 5_000_000, madeAgoMs: 0}]}});
  // The outbox keeps it, and asking for or removing a download, while the server is out of reach.
  assert.ok(isOutboxRoute('/v1/items/state:batch', 'POST'));
  assert.ok(isOutboxRoute('/v1/downloads', 'POST'));
  assert.ok(isOutboxRoute('/v1/downloads/dl_1', 'DELETE'));
  assert.ok(!isOutboxRoute('/v1/downloads/dl_1', 'PATCH'));

  const kept = {path: '/v1/items/state:batch', method: 'POST', createdAt: 1_000,
    body: {items: [{itemId: 'itm_show', watched: true, madeAgoMs: 0}], plays: [{itemId: 'itm_film', playId: 'play-1', madeAgoMs: 30_000}]}};
  assert.deepEqual(outboxBody(kept, 61_000), {items: [{itemId: 'itm_show', watched: true, madeAgoMs: 60_000}],
    plays: [{itemId: 'itm_film', playId: 'play-1', madeAgoMs: 90_000}]});
  // A clock that went backwards never makes an edit or a play younger than it was.
  assert.deepEqual(outboxBody(kept, 500), kept.body);
});

test('a downloaded SubRip sidecar becomes a WebVTT document without changing cue ids or text', async () => {
  const {downloadedSubtitleText} = await import('../src/device-downloads.ts');
  const vtt = downloadedSubtitleText('srt', '1\n00:00:01,000 --> 00:00:03,000\nHello\n');
  assert.equal(vtt, 'WEBVTT\n\n1\n00:00:01.000 --> 00:00:03.000\nHello\n');
  assert.equal(downloadedSubtitleText('srt', vtt), vtt);
});
