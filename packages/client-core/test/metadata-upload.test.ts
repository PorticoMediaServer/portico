import assert from 'node:assert/strict';
import test from 'node:test';
import {HttpLocalApi} from '../src/index.ts';

// A picture goes to the rebuilt server as its own bytes, typed as the image, under the session that started it.
test('artwork upload sends the image to the rebuilt route and refuses a stale session response', async () => {
  let received: RequestInit | undefined, url = '';
  let finish!: (response: Response) => void;
  const api = new HttpLocalApi('https://server.example', 'first', async (to, init) => {url = String(to); received = init; return new Promise(resolve => {finish = resolve;});});
  const body = new Blob(['image'], {type: 'image/png'});
  const pending = api.requestMetadataUpload('/v1/admin/items/itm_1/artwork?role=poster', body);
  api.setAccessToken('second');
  finish(new Response('{}', {status: 201, headers: {'Content-Type': 'application/json'}}));
  await assert.rejects(pending, /old session/);
  const headers = new Headers(received?.headers);
  assert.equal(url, 'https://server.example/v1/admin/items/itm_1/artwork?role=poster');
  assert.equal(received?.body, body);
  assert.equal(headers.get('Authorization'), 'Bearer first');
  assert.equal(headers.get('Content-Type'), 'image/png');
  assert.equal(received?.redirect, 'error');
  assert.equal(received?.credentials, 'omit');
  await assert.rejects(api.requestMetadataUpload('/v1/metadata/item/movie/art/poster/upload', body), /Invalid metadata upload path/);
});
