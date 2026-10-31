import test from 'node:test';
import assert from 'node:assert/strict';
import {ManagedProfilesClient,parseManagedProfiles,parseDirectSnapshot,selectDirectProfile} from '../src/profile-management.ts';
import {resetSkippedParts,skippedParts} from '../src/tolerant.ts';
const p=(i=0)=>({id:'profile-'+i,name:'Profile '+i,primary:i===0,position:i,art:'blue',revision:1,pinRevision:1,pinRequired:false});
const scope={authority:'local' as const,accountId:'account',profileId:'profile',serverId:'server',installationId:'installation'};
const apiFor=(response:unknown,calls:Array<{path:string;method?:string;body?:unknown}>)=>({request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return response as T;}});
test('profile DTOs require one primary and at most eight unique active profiles',()=>{
 assert.equal(parseManagedProfiles(Array.from({length:8},(_,i)=>p(i))).length,8);
 for(const rows of [[],Array.from({length:9},(_,i)=>p(i)),[p(),p()],[{...p(),primary:false}],[{...p(),pinRevision:0}],[{...p(),art:7}],[{...p(),art:''}],[{...p(),art:'x'.repeat(65)}],[p(),{...p(1),pinRequired:'no'}],[p(),{...p(1),allowedLibraries:[7]}],[p(),{...p(1),revision:0}],[p(),null]])assert.throws(()=>parseManagedProfiles(rows));
 const sorted=parseManagedProfiles([p(2),p(),p(1)]);assert.equal(sorted[0].id,'profile-0');assert.ok(Object.isFrozen(sorted));
});
test('Direct account DTO does not derive administration from a selected child profile',()=>{
 const raw={authority:'local',serverId:'server',account:{id:'account',username:'member',primaryProfileId:'profile-0',role:'member',revision:1,disabled:false,allowedLibraries:[]},profiles:[p(),p(1)],canManage:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false}};
 assert.equal(parseDirectSnapshot(raw,{accountId:'account',serverId:'server'}).canManage,false);
 assert.throws(()=>parseDirectSnapshot(raw,{accountId:'another'}));assert.throws(()=>parseDirectSnapshot(raw,{authority:'hosted'}));
 assert.throws(()=>parseDirectSnapshot({...raw,account:{...raw.account,primaryProfileId:'profile-1'}}));
});
test('profile selection sends remember and reads the remembered expiry; the snapshot says whether remembering is offered',async()=>{
 const expiresAt=new Date(Date.now()+30*86400000).toISOString().replace(/\.\d+Z$/,'Z');
 const session={accessToken:'access',expiresAt:new Date(Date.now()+600000).toISOString().replace(/\.\d+Z$/,'Z'),sessionFamilyId:'family',tokenGeneration:'1',authorizationHorizon:new Date(Date.now()+86400000).toISOString().replace(/\.\d+Z$/,'Z'),viewer:{accountId:'account',profileId:'profile',serverId:'server',authority:'local',role:'member'}};
 const calls:any[]=[];
 const chosen=await selectDirectProfile(apiFor({session,remembered:{expiresAt}},calls),{authority:'local',accountId:'account',serverId:'server'},'profile',{pin:'1234',installationId:'installation',remember:true});
 assert.deepEqual(calls[0].body,{pin:'1234',installationId:'installation',remember:true});
 assert.equal(chosen.remembered?.expiresAt,expiresAt);
 const plain=await selectDirectProfile(apiFor({session},[]),{authority:'local',accountId:'account',serverId:'server'},'profile',{pin:'1234'});
 assert.equal(plain.remembered,undefined);
 const raw={authority:'local',serverId:'server',account:{id:'account',username:'owner',primaryProfileId:'profile-0',role:'owner',revision:1,disabled:false,allowedLibraries:[]},profiles:[p(),p(1)],canManage:true,capabilities:{manageAccount:true,managePeople:true,manageServer:true,manageSignIn:true}};
 assert.equal(parseDirectSnapshot({...raw,rememberProfiles:true},{accountId:'account',serverId:'server'}).rememberProfiles,true);
 assert.equal(parseDirectSnapshot(raw,{accountId:'account',serverId:'server'}).rememberProfiles,false);
 assert.equal(parseDirectSnapshot({...raw,rememberProfiles:true,managePinRequired:true},{accountId:'account',serverId:'server'}).managePinRequired,true);
});
test('profile edits carry optimistic revision and refuse malformed PINs before sending',async()=>{
 const calls:any[]=[];const c=new ManagedProfilesClient(apiFor({},calls),scope);
 await c.edit(p(),{pin:'0123',policy:{serverId:'server',allowedLibraries:null}});
 assert.deepEqual(calls[0].body,{pin:'0123',policy:{allowedLibraries:null},expectedRevision:1});
 for(const pin of ['123','12345','12a4',' 123'])await assert.rejects(c.edit(p(),{pin}));
 await assert.rejects(c.edit(p(),{policy:{serverId:'another',allowedLibraries:[]}}));assert.equal(calls.length,1);
 await assert.rejects(c.previewDelete(p()));assert.equal(calls.length,1);
});
test('a picture colour this app does not know reads as blue and the profile is otherwise unchanged',()=>{
 resetSkippedParts();
 // The colour is one of this app's own names or blue: a value from the server is never passed on to be drawn.
 const [first,second]=parseManagedProfiles([{...p(),art:'remote-url',pinRequired:true,allowedLibraries:['films'],futureField:{nested:true}},{...p(1),art:'coral'}]);
 assert.deepEqual(first,{id:'profile-0',name:'Profile 0',primary:true,position:0,art:'blue',revision:1,pinRevision:1,pinRequired:true,allowedLibraries:['films']});
 assert.equal(second.art,'coral');
 assert.deepEqual(skippedParts(),[{document:'profiles',part:'profile.art',count:1}]);
});
test('a profile list read for display leaves out one unreadable profile and never shows it with a restriction missing',()=>{
 const listed=(profiles:unknown)=>parseManagedProfiles(profiles,true);
 resetSkippedParts();
 const kept=listed([p(),{...p(1),pinRequired:'no'},{...p(2),allowedLibraries:[7]},{...p(3),allowedLibraries:'all'},{...p(4),pinRevision:0},{...p(5),name:''},null,{...p(7),pinRequired:true,allowedLibraries:[]}]);
 assert.deepEqual(kept.map(v=>v.id),['profile-0','profile-7']);assert.equal(kept[1].pinRequired,true);assert.deepEqual(kept[1].allowedLibraries,[]);
 assert.deepEqual(skippedParts(),[{document:'profiles',part:'rows',count:6}]);
 // The primary profile, the size of the list and a repeated id are not negotiable.
 assert.throws(()=>listed([{...p(),pinRequired:'no'},p(1)]));
 assert.throws(()=>listed([p(),{...p(1),primary:true}]));
 assert.throws(()=>listed([p(),p(1),{...p(1),pinRequired:true}]));
 assert.throws(()=>listed(Array.from({length:9},(_,i)=>p(i))));
 assert.throws(()=>listed([]));
 assert.throws(()=>listed({}));
});
test('sign-in and the account snapshot still refuse a profile list with an unreadable profile in it',async()=>{
 const account={id:'account',username:'member',primaryProfileId:'profile-0',role:'member',revision:1,disabled:false,allowedLibraries:[]};
 const raw={authority:'local',serverId:'server',account,profiles:[p(),{...p(1),pinRequired:'no'}],canManage:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false}};
 // Left out, the second profile would make this look like an account with one unprotected profile.
 assert.throws(()=>parseDirectSnapshot(raw));assert.throws(()=>parseDirectSnapshot(raw,{accountId:'account',serverId:'server'}));
 const local=new ManagedProfilesClient(apiFor(raw,[]),scope);
 assert.deepEqual((await local.list()).map(v=>v.id),['profile-0']);
 // The account's own role, restrictions and identity are exact in every reading.
 for(const patch of [{role:'superuser'},{allowedLibraries:[7]},{allowedLibraries:undefined},{disabled:'no'},{revision:0},{id:''}]){
  const changed={...raw,profiles:[p()],account:{...account,...patch}};
  assert.throws(()=>parseDirectSnapshot(changed));await assert.rejects(new ManagedProfilesClient(apiFor(changed,[]),scope).list());
 }
 await assert.rejects(new ManagedProfilesClient(apiFor({...raw,profiles:[p()],serverId:'another'},[]),scope).list());
});


test('Forgot PIN uses and retires a temporary account sign-in without adopting a viewer',async()=>{
 const {resetProfilePINWithFreshSignIn}=await import('../src/profile-management.ts');
 const calls:any[]=[];const fresh:any={kind:'signed-in',snapshot:{account:{id:'account'},serverId:'server',profiles:[{id:'profile'}]},session:{accessToken:'fresh-only'}};
 const viewer={accessToken:'installed-viewer'};
 await resetProfilePINWithFreshSignIn(async()=>fresh,session=>{assert.equal(session.accessToken,'fresh-only');return {request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return {} as T;}};},scope,'profile','4821');
 assert.deepEqual(calls,[{path:'/v1/direct/profiles/profile/pin-reset',method:'POST',body:{pin:'4821'}},{path:'/v1/sessions/current',method:'DELETE',body:undefined}]);
 assert.equal(viewer.accessToken,'installed-viewer');
 calls.length=0;
 await assert.rejects(resetProfilePINWithFreshSignIn(async()=>({...fresh,snapshot:{...fresh.snapshot,account:{id:'wrong'}}}),()=>apiFor({},calls),scope,'profile',''));
 assert.equal(calls.length,1);assert.equal(calls[0].path,'/v1/sessions/current');
 await assert.rejects(resetProfilePINWithFreshSignIn(async()=>({kind:'challenge',token:'challenge',expiresAt:'2026-11-01T00:00:00Z'}),()=>{throw Error('must not create a transport');},scope,'profile','4821'));
});

test('Highest rating options are only actual contentRatings and never a country or kids preset',async()=>{
 const {highestRatingChoices}=await import('../src/profile-limits.ts');
 assert.deepEqual(highestRatingChoices(['PG','Custom rating','PG','']),['PG','Custom rating']);
 assert.deepEqual(highestRatingChoices([]),[]);
});
