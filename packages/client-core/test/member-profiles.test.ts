import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/http.ts';
import {createNativeProfileAvatarUpload} from '../src/media-sources.ts';
import {MemberProfilesClient} from '../src/member-profiles.ts';

test('an owner reads and limits another person\'s profiles with member= on the profile routes', async () => {
  const sent: string[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    sent.push(`${method} ${path}${body ? ' ' + JSON.stringify(body) : ''}`);
    if (path.startsWith('/v1/profiles?')) return {items: [{id: 'prf_main', name: 'Sam', color: 'mint', isPrimary: true, pinRequired: false, accessMarker: 'a'},
      {id: 'prf_kid', name: 'Junior', color: 'coral', isPrimary: false, pinRequired: true, accessMarker: 'b'}]} as T;
    if (path.includes('/restrictions')) return {maxRatingAge: method === 'PUT' ? 0 : null, blockedLabels: [], allLibraries: true, libraries: [], accountAllLibraries: true, accountLibraries: []} as T;
    return undefined as T;
  }};
  const client = new MemberProfilesClient(api, 'acc_sam');
  const [, kid] = await client.list();
  assert.equal(kid!.name, 'Junior');
  const limited = await client.restrict(kid!, {maximumAgeRating: 'G'});
  assert.equal(limited.restrictions.maximumAgeRating, 'G');
  const cleared = await client.resetPIN(kid!);
  assert.equal(cleared.pinRequired, false);
  assert.ok(sent.includes('GET /v1/profiles?member=acc_sam'));
  assert.ok(sent.some(line => line.startsWith('PUT /v1/profiles/prf_kid/restrictions?member=acc_sam {"maxRatingAge":0')));
  assert.ok(sent.includes('DELETE /v1/profiles/prf_kid/pin?member=acc_sam'));
});


test('a manager reads, sets and removes a member picture on the same member-scoped profile route', async () => {
  let picture = '/v1/profiles/prf_main/avatar?v=abc';
  const calls: string[] = [];
  const api = new HttpLocalApi('https://server.example', 'manager-token', async (input, init) => {
    const url = new URL(String(input)), method = init?.method ?? 'GET';
    calls.push(method + ' ' + url.pathname + url.search);
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer manager-token');
    assert.equal(url.searchParams.get('member'), 'acc_sam');
    if (method === 'PUT') {
      assert.equal(url.pathname, '/v1/profiles/prf_main/avatar');
      assert.equal(JSON.parse(String(init?.body)).nativeAvatarBase64, 'AQID');
      picture = '/v1/profiles/prf_main/avatar?v=def';
      return new Response(JSON.stringify({id: 'prf_main', avatar: picture}));
    }
    if (method === 'DELETE') {picture = ''; return new Response(JSON.stringify({id: 'prf_main'}));}
    if (url.pathname.endsWith('/restrictions')) return new Response(JSON.stringify({maxRatingAge: null, blockedLabels: [], allLibraries: true, libraries: [], accountAllLibraries: true, accountLibraries: []}));
    return new Response(JSON.stringify({items: [{id: 'prf_main', name: 'Sam', color: 'mint', isPrimary: true, pinRequired: false, avatar: picture}]}));
  });
  const client = new MemberProfilesClient(api, 'acc_sam', createNativeProfileAvatarUpload(api));
  const [profile] = await client.list();
  assert.equal(profile?.avatar?.url, '/v1/profiles/prf_main/avatar?v=abc&member=acc_sam');
  const saved = await client.uploadPicture(profile!, new Uint8Array([1, 2, 3]), 'image/jpeg');
  assert.equal(saved.avatar?.url, '/v1/profiles/prf_main/avatar?v=def&member=acc_sam');
  const removed = await client.removePicture(saved);
  assert.equal(removed.avatar, undefined);
  assert.deepEqual(calls.slice(-2), ['PUT /v1/profiles/prf_main/avatar?member=acc_sam', 'DELETE /v1/profiles/prf_main/avatar?member=acc_sam']);
});
