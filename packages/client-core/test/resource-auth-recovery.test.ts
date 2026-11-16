import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession} from '../src/http.ts';
const viewer = {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p', role: 'owner'};
const session = (accessToken: string): LocalSession => ({accessToken, viewer, sessionFamilyId: 'family', tokenGeneration: '1', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-04-01T00:00:00Z'});
for (const resource of ['lyrics', 'artwork'] as const) {
 const request = (api: HttpLocalApi) => resource === 'lyrics' ? api.requestLyrics('/v1/items/itm_song/lyrics') : api.requestMetadataUpload('/v1/admin/items/itm_film/artwork?role=poster', new Blob(['picture'], {type: 'image/jpeg'}));
 test(`${resource}: a protected 401 renews the same viewer before one retry`, async () => {
  const tokens: string[] = [];
  const api = new HttpLocalApi('https://fixture.invalid', 'old', async (_url, init) => {
   const token = new Headers(init?.headers).get('Authorization')!;tokens.push(token);
   assert.equal(init?.redirect, 'error');assert.equal(init?.credentials, 'omit');
   if (resource === 'artwork') assert.equal(await (init!.body as Blob).text(), 'picture');
   return Response.json(token === 'Bearer old' ? {error: {code: 'unauthorized'}} : {ok: true}, {status: token === 'Bearer old' ? 401 : 200});
  });
  api.setOutboxSession(session('old'));
  api.setAuthRecovery({token: async token => token, recover: async () => {api.setOutboxSession(session('new'));return 'new';}});
  assert.deepEqual(await request(api), {ok: true});assert.deepEqual(tokens, ['Bearer old', 'Bearer new']);
 });
 test(`${resource}: refused renewal ends the sign-in; recovery cannot write as another profile`, async () => {
  for (const replace of [false, true]) {
   let ended = false, calls = 0;
   const api = new HttpLocalApi('https://fixture.invalid', 'old', async () => {calls++;return Response.json({error: {code: 'unauthorized'}}, {status: 401});});
   api.setOutboxSession(session('old'));
   api.setAuthRecovery({token: async token => token, recover: async () => {ended = true;api.setOutboxSession(replace ? {...session('other'), viewer: {...viewer, profileId: 'other'}} : undefined);return replace ? 'other' : undefined;}});
   await assert.rejects(request(api));assert.equal(ended, true);assert.equal(calls, 1);
  }
 });
}
