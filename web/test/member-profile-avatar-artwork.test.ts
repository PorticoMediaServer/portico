import assert from 'node:assert/strict';
import test from 'node:test';
import {authenticatedArtwork, memberProfileAvatarArtwork} from '../src/shell/artwork.ts';

const identity = {serverId: 'server', viewerId: 'owner-profile', authorityKey: 'owner-authority'};
const avatar = {profileId: 'movies', version: 2, updatedAt: '2026-10-28T12:00:00Z', sourceMime: 'image/png', url: '/v1/admin/access/members/person/profiles/movies/avatar?v=2'};

test('avatar admission preserves account, profile, version, rendition and viewer authority in the cache identity', () => {
  const url = avatar.url + '&size=160';
  const selected = memberProfileAvatarArtwork(url, identity)!;
  assert.equal(selected.url, url);
  for (const changed of [url.replace('/person/', '/another/'), url.replace('/movies/', '/another/'), url.replace('v=2', 'v=3'), url.replace('size=160', 'size=64')]) {
    assert.notEqual(memberProfileAvatarArtwork(changed, identity)!.cacheKey, selected.cacheKey);
  }
  for (const changed of [{...identity, authorityKey: 'changed'}, {...identity, viewerId: 'admin'}, {...identity, serverId: 'other'}]) {
    assert.notEqual(memberProfileAvatarArtwork(url, changed)!.cacheKey, selected.cacheKey);
  }
  for (const invalid of ['https://other.invalid' + url, '//other.invalid' + url, url + '\n', url + '#fragment', url + '&token=private', url + '&v=3', url.replace('v=2', 'v=0'),
    url.replace('v=2', 'v=9007199254740992'), url.replace('size=160', 'size=128'), url.replace('/person/', '/../'), url.replace('/movies/', '/%00/'),
    url.replace('/movies/', '/%ZZ/'), url.replace('/movies/', '/mo vies/'), url.replace('/movies/', '/mo\\vies/'), url.replace('/v1/admin/access/members/', '/v1/profiles/')]) {
    assert.equal(memberProfileAvatarArtwork(invalid, identity), undefined, invalid);
  }
});

test('member pictures use the authenticated queue, fixed upload-made sizes and one bearer renewal', async () => {
  const original = globalThis.createImageBitmap;
  globalThis.createImageBitmap = async () => ({width: 160, height: 160, close() {}} as ImageBitmap);
  const requests: {url: string; init?: RequestInit}[] = [];
  let recoveries = 0, current = true;
  const runtime = authenticatedArtwork({identity, currentToken: () => 'old-test-token', isCurrent: () => current,
    auth: {token: async value => value, recover: async () => {recoveries++; return 'renewed-test-token';}},
    api: {baseUrl: 'https://server.invalid', routeFetch: async (url, init) => {
      requests.push({url: String(url), init});
      return requests.length === 1 ? new Response(null, {status: 401}) : new Response(new Uint8Array([1, 2]), {headers: {'Content-Type': 'image/png'}});
    }},
  });
  try {
    const small = runtime.memberProfileAvatar('person', avatar, 44)!;
    const editor = runtime.memberProfileAvatar('person', avatar, 96)!;
    const same = runtime.memberProfileAvatar('person', avatar, 160)!;
    assert.equal(editor.key, same.key);
    assert.notEqual(small.key, editor.key);
    assert.equal(runtime.memberProfileAvatar('another', avatar, 160), undefined);
    assert.equal(runtime.memberProfileAvatar('person', {...avatar, url: '/v1/profiles/movies/avatar?v=2'}, 160), undefined);
    assert.equal(runtime.memberProfileAvatar('person', {...avatar, url: 'https://other.invalid' + avatar.url}, 160), undefined);
    const image = await editor.scheduler.load(editor.key);
    assert.equal(recoveries, 1); assert.equal(image.bytes, 2);
    assert.equal(requests.length, 2);
    assert.ok(requests.every(item => item.url === 'https://server.invalid' + avatar.url + '&size=160'));
    assert.equal(requests[0].init?.credentials, 'omit');
    assert.equal(requests[0].init?.redirect, 'error');
    assert.equal(requests[0].init?.referrerPolicy, 'no-referrer');
    assert.equal((requests[1].init?.headers as Record<string, string>).Authorization, 'Bearer renewed-test-token');
    current = false;
    assert.equal(runtime.memberProfileAvatar('person', avatar, 44), undefined);
    await assert.rejects(small.scheduler.load(small.key));
    assert.equal(requests.length, 2);
  } finally {runtime.dispose(); globalThis.createImageBitmap = original;}
});

test('permission refusal refreshes authority and never exposes public avatar bytes', async () => {
  let checks = 0, requests = 0;
  const runtime = authenticatedArtwork({identity, currentToken: () => 'test-token', isCurrent: () => true,
    auth: {token: async value => value, recover: async () => undefined, permissionDenied: async () => {checks++; return true;}},
    api: {baseUrl: 'https://server.invalid', routeFetch: async () => {requests++; return new Response(null, {status: 403});}},
  });
  try {
    const picture = runtime.memberProfileAvatar('person', avatar, 64)!;
    await assert.rejects(picture.scheduler.load(picture.key));
    assert.equal(checks, 1); assert.equal(requests, 1); assert.equal(picture.scheduler.peek(picture.key), undefined);
  } finally {runtime.dispose();}
});

test('a late successful avatar read after authority retirement cannot populate the queue cache', async () => {
  const original = globalThis.createImageBitmap;
  globalThis.createImageBitmap = async () => ({width: 64, height: 64, close() {}} as ImageBitmap);
  let current = true, finish!: (response: Response) => void;
  const delivery = new Promise<Response>(resolve => {finish = resolve;});
  const runtime = authenticatedArtwork({identity, currentToken: () => 'test-token', isCurrent: () => current,
    auth: {token: async value => value, recover: async () => undefined}, api: {baseUrl: 'https://server.invalid', routeFetch: () => delivery}});
  try {
    const picture = runtime.memberProfileAvatar('person', avatar, 64)!, loading = picture.scheduler.load(picture.key);
    await Promise.resolve(); await Promise.resolve(); current = false;
    finish(new Response(new Uint8Array([1]), {headers: {'Content-Type': 'image/png'}}));
    await assert.rejects(loading); assert.equal(picture.scheduler.peek(picture.key), undefined);
  } finally {runtime.dispose(); globalThis.createImageBitmap = original;}
});
