import test from 'node:test';
import assert from 'node:assert/strict';
import {parseSelectedLocalSession,type SelectionContext} from '../src/session-selection.ts';

const context:SelectionContext={accountId:'account',sessionId:'central-family',serverId:'server',profileId:'profile'};
const legacy=()=>({accessToken:'synthetic-local-token',expiresAt:'2099-01-01T00:00:00Z',viewer:{authority:'hosted',accountId:'account',profileId:'profile',serverId:'server',role:'member'}});
const complete=()=>({...legacy(),sessionFamilyId:'local-family',tokenGeneration:'1',authorizationHorizon:'2099-01-01T01:00:00Z'});
const invalid=(value:unknown)=>assert.throws(()=>parseSelectedLocalSession(value,context),{code:'invalid_selection'});

test('missing family metadata and unknown fields fail closed in current local sessions',()=>{
 invalid(legacy());invalid({...complete(),unknown:'not-retained'});
});
test('complete local family metadata is preserved exactly without float generation conversion',()=>{
 for(const generation of ['1','9007199254740993','9223372036854775807']){
  const input={...complete(),tokenGeneration:generation};const value=parseSelectedLocalSession(input,context);
  assert.equal(value.sessionFamilyId,'local-family');assert.equal(value.tokenGeneration,generation);assert.equal(value.authorizationHorizon,input.authorizationHorizon);
  assert.equal(Object.hasOwn(value,'unknown'),false);assert.notEqual(value.viewer,input.viewer);
 }
});
test('partial optional family groups and explicitly undefined or null members fail closed',()=>{
 const keys=['sessionFamilyId','tokenGeneration','authorizationHorizon'] as const;
 for(let mask=1;mask<7;mask++){
  const input:Record<string,unknown>={...legacy()};const family=complete();keys.forEach((key,index)=>{if(mask&(1<<index))input[key]=family[key];});invalid(input);
 }
 for(const key of keys){invalid({...complete(),[key]:undefined});invalid({...complete(),[key]:null});}
});
test('family identity and canonical signed-int64 decimal generation bounds are enforced',()=>{
 for(const family of ['', 'x'.repeat(129),'family/path','family name','family\n','é'])invalid({...complete(),sessionFamilyId:family});
 for(const generation of [1,0,-1,'','0','-1','01','+1','1.0','1e3',' 1','1 ','9223372036854775808','10000000000000000000'])invalid({...complete(),tokenGeneration:generation});
 assert.equal(parseSelectedLocalSession({...complete(),sessionFamilyId:'x'.repeat(128)},context).sessionFamilyId?.length,128);
});
test('family UTC timestamps reject invalid calendar dates and enforce exact horizon including fractions',()=>{
 for(const horizon of ['invalid','2099-02-30T00:00:00Z','2099-13-01T00:00:00Z','2099-01-01T24:00:00Z','2099-01-01T00:00:60Z','2099-01-01','2099-01-01T01:00:00+01:00','2099-01-01T01:00:00.1234567890Z'])invalid({...complete(),authorizationHorizon:horizon});
 invalid({...complete(),expiresAt:'2099-01-01T01:00:01Z'});
 invalid({...complete(),expiresAt:'2099-02-30T00:00:00Z',authorizationHorizon:'2099-03-02T00:00:00Z'});
 invalid({...complete(),expiresAt:'2099-01-01T00:00:00.000000002Z',authorizationHorizon:'2099-01-01T00:00:00.000000001Z'});
 const boundary={...complete(),expiresAt:'2099-01-01T00:00:00.123456789Z',authorizationHorizon:'2099-01-01T00:00:00.123456789+00:00'};
 assert.equal(parseSelectedLocalSession(boundary,context).authorizationHorizon,boundary.authorizationHorizon);
 assert.equal(parseSelectedLocalSession({...complete(),authorizationHorizon:'2099-01-01t01:00:00z'},context).authorizationHorizon,'2099-01-01t01:00:00z');
});
test('family metadata never bypasses selected viewer scope or expiry validation',()=>{
 for(const patch of [{accountId:'other'},{profileId:'other'},{serverId:'other'},{authority:'local'}])assert.throws(()=>parseSelectedLocalSession({...complete(),viewer:{...complete().viewer,...patch}},context),{code:'session_identity_mismatch'});
 invalid({...complete(),expiresAt:'2000-01-01T00:00:00Z'});
 invalid({...complete(),accessToken:''});
});
