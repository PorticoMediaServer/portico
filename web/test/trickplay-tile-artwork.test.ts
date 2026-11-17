import assert from 'node:assert/strict';
import test from 'node:test';
import {parseScrubPreviews, trickplayFrameAt} from '@core/trickplay.ts';
import {authenticatedArtwork, trickplayTileArtwork} from '../src/shell/artwork.ts';

const identity = {serverId: 'server', viewerId: 'viewer', authorityKey: 'authority'};
const sheet = (n: number) => '/v1/items/item-one/scrub-previews?version=med_1&sheet=' + n;
const set = parseScrubPreviews({version: 'med_1', available: true, durationMs: 1_500_000, intervalMs: 10_000, frameWidth: 320, frameHeight: 180,
  columns: 10, frameCount: 150, sheets: [sheet(0), sheet(1)]}, 'item-one', 'med_1')!;

test('only an item\'s own preview sheet path is admitted, unscaled, and its cache key names set, revision and viewer', () => {
  const url = sheet(1), selected = trickplayTileArtwork(url, identity, 'med_1')!;
  assert.equal(selected.url, url);
  assert.notEqual(trickplayTileArtwork(url, identity, 'med_2')!.cacheKey, selected.cacheKey);
  assert.notEqual(trickplayTileArtwork(url, {...identity, viewerId: 'other'}, 'med_1')!.cacheKey, selected.cacheKey);
  for (const invalid of [url + '&w=256', url + '#x', 'https://other.invalid' + url, sheet(1).replace('sheet=1', 'sheet=01'), sheet(1).replace('sheet=1', 'sheet=-1'), sheet(100000),
    url.replace('item-one', '..'), url.replace('item-one', '%ZZ'), url.replace('/scrub-previews', '/chapters'), url + '\n']) {
    assert.equal(trickplayTileArtwork(invalid, identity, 'med_1'), undefined, invalid);
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
    const frame = trickplayFrameAt(set, 1045)!;
    assert.equal(frame.tileIndex, 1);
    const source = runtime.trickplayTile(set, frame.tileIndex)!;
    assert.equal(runtime.trickplayTile(set, 1)!.key, source.key);
    assert.notEqual(runtime.trickplayTile(set, 0)!.key, source.key);
    assert.equal(runtime.trickplayTile(set, 2), undefined);
    const image = await source.scheduler.load(source.key);
    assert.deepEqual([image.width, image.height], [640, 360]);
    assert.deepEqual(requests.map(request => request.url), ['https://server.invalid' + sheet(1)]);
    assert.equal((requests[0]!.init?.headers as Record<string, string>).Authorization, 'Bearer test-token');
    assert.equal(requests[0]!.init?.credentials, 'omit');
    current = false;
    assert.equal(runtime.trickplayTile(set, 0), undefined);
  } finally {runtime.dispose(); globalThis.createImageBitmap = original;}
});
