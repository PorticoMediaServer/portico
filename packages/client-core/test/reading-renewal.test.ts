import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession} from '../src/http.ts';
const viewer = {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p', role: 'member'};
const session = (accessToken: string): LocalSession => ({accessToken, viewer, sessionFamilyId: 'fam', tokenGeneration: '1', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-04-01T00:00:00Z'});
test('a reader stays open across same-family renewal and uses the renewed token', async () => {
 const tokens: string[] = [];
 const api = new HttpLocalApi('https://fixture.invalid', 'old', async (_input, init) => {tokens.push(new Headers(init?.headers).get('Authorization')!);return Response.json({ok: true});});
 api.setOutboxSession(session('old'));
 const reader = api.reading({viewer, isCurrent: () => true});
 api.setOutboxSession(session('new'));
 assert.equal(reader.signal.aborted, false);
 await reader.response('/v1/items/itm_one/book', {}, undefined, r => r.json());
 assert.deepEqual(tokens, ['Bearer new']);
 api.setOutboxSession({...session('other'), viewer: {...viewer, profileId: 'other'}});
 assert.equal(reader.signal.aborted, true);
});
test('reader 401 follows session recovery and a different family retires the reader', async () => {
 const tokens: string[] = [];
 const api = new HttpLocalApi('https://fixture.invalid', 'old', async (_input, init) => {const token = new Headers(init?.headers).get('Authorization')!;tokens.push(token);return token === 'Bearer old' ? Response.json({}, {status: 401}) : Response.json({ok: true});});
 api.setOutboxSession(session('old'));
 api.setAuthRecovery({token: async token => token, recover: async () => {api.setOutboxSession(session('new'));return 'new';}});
 const reader = api.reading({viewer, isCurrent: () => true});
 assert.deepEqual(await reader.response('/v1/items/itm_one/book', {}, undefined, r => r.json()), {ok: true});
 assert.deepEqual(tokens, ['Bearer old', 'Bearer new']);assert.equal(reader.signal.aborted, false);
 api.setOutboxSession({...session('third'), sessionFamilyId: 'different'});assert.equal(reader.signal.aborted, true);
});
