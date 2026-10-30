import test from 'node:test';
import assert from 'node:assert/strict';
import type {HttpLocalApi} from '../src/index.ts';
import {startAppSetup} from '../src/setup-onboarding.ts';
import {safeApprovalUrl} from '../src/claim-web.ts';

function api(answer: unknown, serverId = 'srv_1') {
  const calls: {path: string; method: string}[] = [];
  const fake = {
    getRouteConnection: () => ({pin: {serverId, publicKey: 'pk', fingerprint: 'fp'}}),
    request: async (path: string, method: string) => { calls.push({path, method}); return answer; },
  } as unknown as HttpLocalApi;
  return {fake, calls};
}

test('first run from an app asks the app route for the setup secret of the pinned server', async () => {
  const {fake, calls} = api({serverId: 'srv_1', setupToken: 't'.repeat(43)});
  assert.deepEqual(await startAppSetup(fake), {serverId: 'srv_1', setupToken: 't'.repeat(43)});
  assert.deepEqual(calls, [{path: '/v1/setup/app', method: 'POST'}]);
});

test('a setup result is read by its variant and an unknown variant needs a newer app',async()=>{
 const {parseSetupSession}=await import('../src/setup-onboarding.ts');
 const {NeedsNewerAppError}=await import('../src/needs-newer-app.ts');
 assert.throws(()=>parseSetupSession({serverId:'srv_1'},'srv_1'),(e:any)=>!(e instanceof NeedsNewerAppError));
 assert.throws(()=>parseSetupSession({variant:'review',serverId:'srv_1'},'srv_1'),(e:any)=>e instanceof NeedsNewerAppError&&e.tag==='review'&&e.code==='needs_newer_app');
});

test('a setup answer for another server, or a malformed one, is refused', async () => {
  await assert.rejects(startAppSetup(api({serverId: 'srv_2', setupToken: 'x'}).fake));
  await assert.rejects(startAppSetup(api({serverId: 'srv_1'}).fake));
  await assert.rejects(startAppSetup(api(null).fake));
});

test('the claim approval page opened from an app is only ever the Portico Account site', () => {
  const hosted = 'https://web.getportico.tv';
  assert.equal(safeApprovalUrl(`${hosted}/claim#code=ABC&server=Den`, hosted), `${hosted}/claim#code=ABC&server=Den`);
  assert.equal(safeApprovalUrl('https://evil.example/claim#code=ABC', hosted), undefined);
  assert.equal(safeApprovalUrl(`${hosted}/other#code=ABC`, hosted), undefined);
});

test('same-tab claim consumer refuses unsafe origins, ports, lookalikes, userinfo and duplicate codes',()=>{
 const hosted='https://web.getportico.tv';
 for(const target of [
  'https://web.getportico.tv.evil.example/claim#code=secret',
  'https://web.getportico.tv:444/claim#code=secret',
  'https://user:password@web.getportico.tv/claim#code=secret',
  'http://web.getportico.tv/claim#code=secret',
  'javascript:alert(1)',
  'https://evil.example/claim#code=secret',
  `${hosted}/claim?next=https://evil.example#code=secret`,
  `${hosted}/claim#code=one&code=two`,
 ])assert.equal(safeApprovalUrl(target,hosted),undefined);
});

test('account setup pending response cannot be parsed as a local owner session',async()=>{
 const {parseSetupSession}=await import('../src/setup-onboarding.ts');
 const pending={variant:'claim-required',serverId:'srv_1',requestId:'33333333-3333-4333-8333-333333333333',authMode:'hosted',claimRequired:true,revision:'1'};
 const {variant:_v,...body}=pending;
 assert.deepEqual(parseSetupSession(pending,'srv_1'),body);
 for(const bad of [{...pending,serverId:'other'},{...pending,revision:'0'},{...pending,accessToken:''},{...pending,viewer:{role:'owner'}}])assert.throws(()=>parseSetupSession(bad,'srv_1'));
});
