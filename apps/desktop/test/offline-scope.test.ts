import {test} from 'node:test';
import assert from 'node:assert/strict';
import {offlineDownloadScope} from '../src/offline-scope.ts';
const now=Date.parse('2026-10-29T00:00:00Z');
const session=(authority:string,horizon='2026-10-30T00:00:00Z')=>({sessionFamilyId:'family',expiresAt:horizon,authorizationHorizon:horizon,
  viewer:{serverId:'server',authority,accountId:'account',profileId:'profile',role:'member'}});
test('offline scope is the persisted session authority, and nothing else',()=>{
 for(const authority of ['local','hosted'] as const)assert.deepEqual(offlineDownloadScope(session(authority),now),{serverId:'server',authority,accountId:'account',profileId:'profile',sessionFamilyId:'family'});
 for(const authority of ['','anonymous','Local'])assert.equal(offlineDownloadScope(session(authority),now),undefined);
 assert.equal(offlineDownloadScope(session('local','2026-10-28T00:00:00Z'),now),undefined);
 assert.equal(offlineDownloadScope(null,now),undefined);
});
