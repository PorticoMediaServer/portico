import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/http.ts';
import {IdentityClient} from '../src/identity-client.ts';
import {presentError} from '../src/presentation/errors.ts';
import {createNativeProfileAvatarUpload,profilePictureSource} from '../src/media-sources.ts';

test('identity avatars read the rebuilt profile list and use its versioned URLs without inventing receipt metadata', async () => {
  const calls: string[] = [], signal = new AbortController().signal;
  const api = new HttpLocalApi('https://server.example', 'fixture-token', async (url, init) => {
    calls.push(String(url)); assert.equal(init?.method, 'GET'); assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer fixture-token');
    return new Response(JSON.stringify({items: [{id: 'main', avatar: '/v1/profiles/main/avatar?v=abc'}, {id: 'kid'}]}));
  });
  const avatars = await new IdentityClient(api).avatars(signal);
  assert.deepEqual(calls, ['https://server.example/v1/profiles']);
  assert.deepEqual(avatars, [{profileId: 'main', url: '/v1/profiles/main/avatar?v=abc'}]);
  assert.deepEqual(profilePictureSource(api, 'fixture-token', avatars[0], 160), {
    uri: 'https://server.example/v1/profiles/main/avatar?v=abc&size=small', headers: {Authorization: 'Bearer fixture-token'},
  });
});

test('the profile reader refuses an avatar URL outside that profile’s authenticated picture route', async () => {
  const api = new HttpLocalApi('https://server.example', 'fixture-token', async () => new Response(JSON.stringify({items: [
    {id: 'main', avatar: 'https://other.example/picture'},
  ]})));
  await assert.rejects(new IdentityClient(api).avatars(), {code: 'invalid_response'});
});


test('profile picture refusals retain the rebuilt server sentence through the real upload and presentation', async () => {
  for (const [status, code, message] of [
    [413, 'body_too_large', 'A profile picture can be at most 4 MB.'],
    [415, 'unsupported_media_type', 'Upload a JPEG, PNG, GIF or WebP picture.'],
    [409, 'managed_by_portico_account', "This profile's picture is managed in the person's Portico Account."],
  ] as const) {
    const api = new HttpLocalApi('https://server.example', 'fixture-token', async (url, init) => {
      assert.equal(String(url), 'https://server.example/v1/profiles/main/avatar'); assert.equal(init?.method, 'PUT');
      return new Response(JSON.stringify({error: {code, message, retry: false, requestId: 'fixture'}}), {status});
    });
    const error = await new IdentityClient(api, createNativeProfileAvatarUpload(api)).uploadAvatar('main', new Uint8Array([1]), 'image/jpeg').catch(error => error);
    assert.equal(error.code, code); assert.equal(error.message, message);
    for (const context of ['profiles', 'server-console'] as const) assert.equal(presentError(error, context).body, message);
  }
  assert.notEqual(presentError(Object.assign(new Error('/private/diagnostic'), {code: 'unexpected', status: 500}), 'profiles').body, '/private/diagnostic');
});
