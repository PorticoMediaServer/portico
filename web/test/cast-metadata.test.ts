import assert from 'node:assert/strict';
import test from 'node:test';
import {setImmediate as settle} from 'node:timers/promises';
import type {ItemResponse} from '../../packages/client-core/src/media-item.ts';
import type {CastApi} from '../../packages/client-core/src/cast/index.ts';
import {readWatchCastMetadata} from '../src/screens/watch/cast-metadata.ts';

// Authored UNRUN vectors for the production display reader. No Cast SDK,
// receiver, browser session or media engine is created.
const song: ItemResponse = {
  id: 'song-2', kind: 'song', title: 'Second song', libraryId: 'music', missing: false, available: true,
  durationMs: 180000, progressMs: 0, addedAt: null,
  song: {albumId: 'album-2', albumTitle: 'Second album', artist: 'Artist', albumArtist: 'Artist',
    providerMatchStatus: 'matched', providerReleaseStatus: 'matched'},
};
const album: ItemResponse = {...song, id: 'album-2', kind: 'album', title: 'Second album',
  posterUrl: '/v1/items/album-2/art/poster', song: undefined};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {resolve = done;});
  return {promise, resolve};
}
function fixture(primary: ItemResponse = song, related: () => Promise<ItemResponse> = async () => album) {
  const calls: string[] = [];let live = true;
  const assertCurrent = () => {if (!live) throw new DOMException('Original Cast display retired', 'AbortError');};
  const api: CastApi = {request: async<T>(path: string): Promise<T> => {
    calls.push(path);
    return (calls.length === 1 ? primary : await related()) as T;
  }};
  return {api, calls, assertCurrent, retire: () => {live = false;}};
}

test('the current song uses exactly one admitted album read while retaining its own identity', async () => {
  const f = fixture();const result = await readWatchCastMetadata(f.api, song.id, f.assertCurrent, 'cover');
  assert.equal(result.item, song);assert.equal(result.metadata.title, song.title);assert.equal(result.metadata.kind, 'song');
  assert.equal(result.metadata.artworkPath, album.posterUrl);
  assert.deepEqual(f.calls, ['/v1/items/song-2', '/v1/items/album-2']);
});

test('Root50 identity validation does not wait for or request optional cover enrichment', async () => {
  const held = deferred<ItemResponse>(), f = fixture(song, () => held.promise);
  const result = await readWatchCastMetadata(f.api, song.id, f.assertCurrent, 'identity');
  assert.equal(result.metadata.title, song.title);assert.equal(result.metadata.artworkPath, undefined);
  assert.deepEqual(f.calls, ['/v1/items/song-2']);
});

test('own projected artwork needs no extra request', async () => {
  for (const field of ['posterUrl', 'backdropUrl'] as const) {
    const primary = {...song, [field]: '/v1/items/song-2/art/own'}, f = fixture(primary);
    const result = await readWatchCastMetadata(f.api, song.id, f.assertCurrent, 'cover');
    assert.equal(result.metadata.artworkPath, primary[field]);assert.equal(f.calls.length, 1);
  }
});

test('an absent, refused, missing, mismatched or unsafe related cover does not replace the admitted song', async () => {
  const cases: (() => Promise<ItemResponse>)[] = [
    async () => ({...album, id: 'another-album'}), async () => ({...album, kind: 'book'}),
    async () => ({...album, missing: true}), async () => ({...album, posterUrl: '//outside.invalid/cover'}),
    async () => ({...album, posterUrl: '/cover\n'}),
    async () => {throw Object.assign(new Error('refused'), {status: 403});},
  ];
  for (const related of cases) {
    const f = fixture(song, related), result = await readWatchCastMetadata(f.api, song.id, f.assertCurrent, 'cover');
    assert.equal(result.metadata.title, song.title);assert.equal(result.metadata.artworkPath, undefined);
    assert.equal(f.calls.length, 2);
  }
});

test('a wrong primary identity never authorizes a related read', async () => {
  const f = fixture({...song, id: 'other-song'});
  await assert.rejects(readWatchCastMetadata(f.api, song.id, f.assertCurrent, 'cover'), /cast_item_mismatch/);
  assert.equal(f.calls.length, 1);
});

test('original retirement before dispatch makes zero requests', async () => {
  const f = fixture();f.retire();
  await assert.rejects(readWatchCastMetadata(f.api, song.id, f.assertCurrent, 'cover'), {name: 'AbortError'});
  assert.equal(f.calls.length, 0);
});

test('a cover response after original scope retirement cannot publish or enter the caller cache', async () => {
  const held = deferred<ItemResponse>(), f = fixture(song, () => held.promise);
  const published: unknown[] = [];
  const read = readWatchCastMetadata(f.api, song.id, f.assertCurrent, 'cover').then(value => {published.push(value);});
  await settle();assert.equal(f.calls.length, 2);f.retire();held.resolve(album);
  await assert.rejects(read, {name: 'AbortError'});assert.deepEqual(published, []);assert.equal(f.calls.length, 2);
});
