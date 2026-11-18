import test from 'node:test';
import assert from 'node:assert/strict';

test('a setup result is read by its variant and an unknown variant needs a newer app',async()=>{
 const {parseSetupSession}=await import('../src/setup-onboarding.ts');
 const {NeedsNewerAppError}=await import('../src/needs-newer-app.ts');
 assert.throws(()=>parseSetupSession({serverId:'srv_1'},'srv_1'),(e:any)=>!(e instanceof NeedsNewerAppError));
 assert.throws(()=>parseSetupSession({variant:'review',serverId:'srv_1'},'srv_1'),(e:any)=>e instanceof NeedsNewerAppError&&e.tag==='review'&&e.code==='needs_newer_app');
});

test('account setup pending response cannot be parsed as a local owner session',async()=>{
 const {parseSetupSession}=await import('../src/setup-onboarding.ts');
 const pending={variant:'claim-required',serverId:'srv_1',requestId:'33333333-3333-4333-8333-333333333333',authMode:'hosted',claimRequired:true,revision:'1'};
 const {variant:_v,...body}=pending;
 assert.deepEqual(parseSetupSession(pending,'srv_1'),body);
 for(const bad of [{...pending,serverId:'other'},{...pending,revision:'0'},{...pending,accessToken:''},{...pending,viewer:{role:'owner'}}])assert.throws(()=>parseSetupSession(bad,'srv_1'));
});
