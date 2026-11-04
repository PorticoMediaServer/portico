import assert from 'node:assert/strict';
import test from 'node:test';
import {parseTrickplay, trickplayFrameAt} from '@core/trickplay.ts';
import {authenticatedArtwork, trickplayTileArtwork} from '../src/shell/artwork.ts';

const identity = {serverId: 'server', viewerId: 'viewer', authorityKey: 'authority'};
const setId = 'a'.repeat(64), revision = 'b'.repeat(64), target = {libraryId: 'movies', itemId: 'item-one'};
const tiles = '/v1/items/item-one/trickplay/' + setId + '/tiles';
const set = parseTrickplay({scope: {serverId: 'server', libraryId: 'movies', itemId: 'item-one', viewerFence: 'fence', sessionId: '', sessionGeneration: 0}, sets: [{
  id: setId, sourceId: 'source', sourceRevision: 'r1', revision, width: 640, height: 360, columns: 2, rows: 2, tileWidth: 320, tileHeight: 180,
  intervalMs: 10000, durationMs: 80000, sourceOffsetMs: 0, frameCount: 8, tileCount: 2, stale: false,
  tilesUrl: tiles, thumbnailsUrl: '/v1/items/item-one/trickplay/' + setId + '/thumbnails.vtt'}]}, 'server', target).sets[0]!;

test('only an item\'s own preview sheet path is admitted, unscaled, and its cache key names set, revision and viewer', () => {
  const url = tiles + '/1.jpg', selected = trickplayTileArtwork(url, identity, revision)!;
  assert.equal(selected.url, url);
  assert.notEqual(trickplayTileArtwork(url, identity, 'c'.repeat(64))!.cacheKey, selected.cacheKey);
  assert.notEqual(trickplayTileArtwork(url, {...identity, viewerId: 'other'}, revision)!.cacheKey, selected.cacheKey);
  for (const invalid of [url + '?w=256', url + '#x', 'https://other.invalid' + url, tiles + '/01.jpg', tiles + '/-1.jpg', tiles + '/1.png', tiles + '/100000.jpg',
    url.replace(setId, 'A'.repeat(64)), url.replace(setId, 'a'.repeat(63)), url.replace('item-one', '..'), url.replace('item-one', '%ZZ'), url.replace('/trickplay/', '/chapters/'), url + '\n']) {
    assert.equal(trickplayTileArtwork(invalid, identity, revision), undefined, invalid);
  }
});

test('a scrub frame\'s sheet loads through the authenticated queue at its own path, with no size query', async () => {
  const original = globalThis.createImageBitmap;
  globalThis.createImageBitmap = async () => ({width: 640, height: 360, close() {}} as ImageBitmap);
  const requests: {url: string; init?: RequestInit}[] = [];
  let current = true;
  const runtime = authenticatedArtwork({identity, currentToken: () => 'test-token', isCurrent: () => current,
    auth: {token: async value => value, recover: async () => undefined},
    api: {baseUrl: 'https://server.invalid', routeFetch: async (url, init) => {requests.push({url: String(url), init}); return new Response(new Uint8Array([1, 2, 3]), {headers: {'Content-Type': 'image/jpeg'}});}},
  });
  try {
    const frame = trickplayFrameAt(set, 45)!;
    assert.equal(frame.tileIndex, 1);
    const source = runtime.trickplayTile(set, frame.tileIndex)!;
    assert.equal(runtime.trickplayTile(set, 1)!.key, source.key);
    assert.notEqual(runtime.trickplayTile(set, 0)!.key, source.key);
    assert.equal(runtime.trickplayTile(set, 2), undefined);
    const image = await source.scheduler.load(source.key);
    assert.deepEqual([image.width, image.height], [640, 360]);
    assert.deepEqual(requests.map(request => request.url), ['https://server.invalid' + tiles + '/1.jpg']);
    assert.equal((requests[0]!.init?.headers as Record<string, string>).Authorization, 'Bearer test-token');
    assert.equal(requests[0]!.init?.credentials, 'omit');
    current = false;
    assert.equal(runtime.trickplayTile(set, 0), undefined);
  } finally {runtime.dispose(); globalThis.createImageBitmap = original;}
});
