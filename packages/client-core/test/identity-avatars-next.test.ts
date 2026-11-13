import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/http.ts';
import {IdentityClient} from '../src/identity-client.ts';
import {profilePictureSource} from '../src/media-sources.ts';

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
