import assert from 'node:assert/strict';
import test from 'node:test';
import {fetchSongLyrics, pasteSongLyrics, readSongLyrics} from '../src/metadata-lyrics.ts';

// A song's Lyrics tab reads, pastes and fetches on the rebuilt server (area 4 rule 64), never the old lyrics routes.
test('song lyrics are read, pasted and fetched through the rebuilt routes', async () => {
  const calls: unknown[][] = [];
  const page = {items: [{id: 'lyr0', origin: 'pasted', format: 'lrc', text: '[00:01.00] La', lines: [], offsetMs: 0}]};
  const api = {async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {calls.push([method, path, body]); return (path.endsWith('/fetch') ? {found: false} : page) as T;}};
  assert.equal((await readSongLyrics(api, 'itm_9'))[0].origin, 'pasted');
  await pasteSongLyrics(api, 'itm_9', '[00:01.00] La', ' en ');
  await pasteSongLyrics(api, 'itm_9', 'La');
  assert.equal(await fetchSongLyrics(api, 'itm_9'), false);
  assert.deepEqual(calls, [['GET', '/v1/items/itm_9/lyrics', undefined], ['PUT', '/v1/admin/items/itm_9/lyrics', {text: '[00:01.00] La', language: 'en'}],
    ['PUT', '/v1/admin/items/itm_9/lyrics', {text: 'La'}], ['POST', '/v1/admin/items/itm_9/lyrics/fetch', undefined]]);
});
