import test from 'node:test';
import assert from 'node:assert/strict';
import {authenticatedArtwork, artworkFailure, foregroundArtwork} from '../src/shell/artwork.ts';

const identity = {serverId: 'server', viewerId: 'account-profile', authorityKey: 'current-authority'};
const url = '/v1/images/' + 'a'.repeat(64);
test('foreground sizes the rebuilt image and refuses retired or foreign addresses', () => {
  const source = foregroundArtwork(url, 620, 16 / 9, identity);
  assert.equal(source?.url, url + '?w=640&h=360');
  assert.equal(foregroundArtwork('/assets/reference.jpg', 620, 1, identity), undefined);
  assert.equal(foregroundArtwork('/v1/items/item/art/backdrop?v=' + 'a'.repeat(64), 620, 1, identity), undefined);
  assert.equal(foregroundArtwork('https://other.invalid' + url, 620, 1, identity), undefined);
});
test('actual Retry-After classification preserves pending-generation retries without retrying permission failures', () => {
  assert.deepEqual(artworkFailure({status: 404, retryAfterSeconds: 5}), {retryable: true, retryAfterSeconds: 5});
  assert.equal(artworkFailure({status: 404}).retryable, false);
  assert.equal(artworkFailure({status: 403}).retryable, false);
  assert.equal(artworkFailure({status: 429, retryAfterSeconds: 30}).retryable, true);
  assert.equal(artworkFailure({name: 'AbortError'}).retryable, false);
});
test('authenticated bytes renew once on 401 and late authority cannot publish a cached image', async () => {
  const original = globalThis.createImageBitmap;
  globalThis.createImageBitmap = async () => ({width: 64, height: 36, close() {}} as ImageBitmap);
  let calls = 0, recoveries = 0, current = true;
  const requested: {url: string; init: RequestInit}[] = [];
  const runtime = authenticatedArtwork({identity, currentToken: () => 'old-private-token', isCurrent: () => current,
    auth: {token: async value => value, recover: async () => { recoveries++; return 'new-private-token'; }},
    api: {baseUrl: 'https://server.invalid', routeFetch: async (input, init) => {
      requested.push({url: String(input), init: init!}); calls++;
      return calls === 1 ? new Response(null, {status: 401}) : new Response(new Uint8Array([1, 2, 3]), {headers: {'Content-Type': 'image/png'}});
    }},
  });
  try {
    const source = runtime.ambient(url)!;
    const image = await source.scheduler.load(source.key);
    assert.equal(recoveries, 1);
    assert.equal(requested.length, 2);
    assert.equal(requested[0].init.credentials, 'omit');
    assert.equal(requested[0].init.redirect, 'error');
    assert.equal((requested[1].init.headers as Record<string, string>).Authorization, 'Bearer new-private-token');
    assert.ok(requested.every(request => request.url.includes('size=ambient-v1') && !request.url.includes('token')));
    assert.equal(image.bytes, 3);
    assert.equal(image.width, 64);
    current = false;
    const changed = runtime.foreground(url, 400, 16 / 9)!;
    await assert.rejects(changed.scheduler.load(changed.key));
    assert.equal(requested.length, 2);
  } finally { runtime.dispose(); globalThis.createImageBitmap = original; }
});
test('ambient accepts only the real bounded decoded representation', async () => {
  const original = globalThis.createImageBitmap;
  globalThis.createImageBitmap = async () => ({width: 1280, height: 720, close() {}} as ImageBitmap);
  const runtime = authenticatedArtwork({identity, currentToken: () => 'private-token', isCurrent: () => true,
    auth: {token: async value => value, recover: async () => undefined},
    api: {baseUrl: 'https://server.invalid', routeFetch: async () => new Response(new Uint8Array([1]), {headers: {'Content-Type': 'image/png'}})},
  });
  try { const source = runtime.ambient(url)!; await assert.rejects(source.scheduler.load(source.key)); assert.equal(source.scheduler.peek(source.key), undefined); }
  finally { runtime.dispose(); globalThis.createImageBitmap = original; }
});

test('a successful image response arriving after authority retirement never reaches the shared cache', async () => {
  let respond!: (response: Response) => void, current = true;
  const response = new Promise<Response>(resolve => { respond = resolve; });
  const original = globalThis.createImageBitmap;
  globalThis.createImageBitmap = async () => ({width: 64, height: 36, close() {}} as ImageBitmap);
  const runtime = authenticatedArtwork({identity, currentToken: () => 'test-token', isCurrent: () => current,
    auth: {token: async value => value, recover: async () => undefined}, api: {baseUrl: 'https://server.invalid', routeFetch: () => response}});
  try {
    const source = runtime.ambient(url)!, loading = source.scheduler.load(source.key);
    await Promise.resolve(); await Promise.resolve(); current = false;
    respond(new Response(new Uint8Array([1]), {headers: {'Content-Type': 'image/png'}}));
    await assert.rejects(loading); assert.equal(source.scheduler.peek(source.key), undefined);
  } finally { runtime.dispose(); globalThis.createImageBitmap = original; }
});
test('artwork permission refusal asks the genuine auth owner to refresh permission facts', async () => {
  let permissionChecks = 0;
  const runtime = authenticatedArtwork({identity, currentToken: () => 'test-token', isCurrent: () => true,
    auth: {token: async value => value, recover: async () => undefined, permissionDenied: async () => { permissionChecks++; return true; }},
    api: {baseUrl: 'https://server.invalid', routeFetch: async () => new Response(null, {status: 403})}});
  try { const source = runtime.ambient(url)!; await assert.rejects(source.scheduler.load(source.key)); assert.equal(permissionChecks, 1); }
  finally { runtime.dispose(); }
});
