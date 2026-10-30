import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLocalSession} from '../src/local-session.ts';
import {parseNativeCredentialRecord,parseBrowserCredentialRecord,parseNativeCredentialJSON} from '../src/credential-format.ts';
const local=()=>({accessToken:'test-access',expiresAt:'2099-01-01T00:00:00.123456789Z',sessionFamilyId:'family_A',tokenGeneration:'9223372036854775807',authorizationHorizon:'2099-01-02T00:00:00Z',viewer:{accountId:'a',profileId:'p',serverId:'s',authority:'local',role:'owner'}});
const hosted=()=>({accessToken:'test-access',refreshToken:'test-refresh',familyId:'family_A',expiresAt:'2000-01-01T00:00:00Z',refreshExpiresAt:'2099-01-02T00:00:00Z',account:{id:'a',username:'user',displayName:'User'}});
const record=()=>({version:1,active:{session:hosted(),authority:'test-refresh',context:{profileId:'p'},pendingRequestId:'913e39bf-dada-4ccc-8b29-c4f87706b61a'},revocations:['older-current-refresh']});
test('current local envelope preserves all family metadata and captured viewer binding',()=>{
 const input=local(),parsed=parseLocalSession(input,{authority:'local',serverId:'s'});assert.deepEqual(parsed,input);assert.ok(Object.isFrozen(parsed));assert.ok(Object.isFrozen(parsed.viewer));
 assert.throws(()=>parseLocalSession(input,{serverId:'other'}));
});
test('old local shapes and noncanonical generations are refused without synthesis',()=>{
 for(const field of ['sessionFamilyId','tokenGeneration','authorizationHorizon']){const value:any=local();delete value[field];assert.throws(()=>parseLocalSession(value));}
 for(const tokenGeneration of ['0','01','+1','1.0','9223372036854775808',1])assert.throws(()=>parseLocalSession({...local(),tokenGeneration}));
 assert.throws(()=>parseLocalSession({...local(),obsolete:true}));
});
test('invalid calendar, expired access and sub-millisecond horizon overflow are refused',()=>{
 for(const expiresAt of ['2099-02-30T00:00:00Z','2000-01-01T00:00:00Z','2099-01-01'])assert.throws(()=>parseLocalSession({...local(),expiresAt}));
 assert.throws(()=>parseLocalSession({...local(),authorizationHorizon:'2099-01-01T00:00:00.123456788Z'}));
 assert.equal(parseLocalSession({...local(),authorizationHorizon:'2099-01-01T00:00:00.123456789Z'}).tokenGeneration,local().tokenGeneration);
});
test('native current record retains expired access plus exact pending renewal authority',()=>{
 const input=record();assert.deepEqual(parseNativeCredentialRecord(input),input);
 assert.throws(()=>parseNativeCredentialRecord({...input,version:0}));
 assert.throws(()=>parseNativeCredentialRecord({...input,active:{...input.active,authority:'wrong-refresh'}}));
 for(const field of ['familyId','refreshExpiresAt','refreshToken']){const input:any=record();delete input.active.session[field];assert.throws(()=>parseNativeCredentialRecord(input));}
});
test('browser current storage strips ephemeral access and refuses persisted credentials',()=>{
 const session:any=hosted();delete session.refreshToken;
 const input={version:1,active:{session,authority:{familyId:'family_A'},context:{}},revocations:[{familyId:'previous-family'}]};
 const saved=parseBrowserCredentialRecord(input,false);assert.equal(saved.active!.session.accessToken,'');assert.equal('refreshToken' in saved.active!.session,false);
 assert.deepEqual(parseBrowserCredentialRecord(saved),saved);assert.throws(()=>parseBrowserCredentialRecord(input));
 assert.throws(()=>parseBrowserCredentialRecord({...input,active:{...input.active,session:hosted()}},false));
 assert.throws(()=>parseBrowserCredentialRecord({...saved,active:{...saved.active,authority:{familyId:'other'}}}));
});
test('current credential records refuse missing schema fields and unsafe server context',()=>{
 for(const parse of [parseNativeCredentialRecord,parseBrowserCredentialRecord,parseNativeCredentialJSON]){assert.throws(()=>parse({version:1}));assert.throws(()=>parse({version:1,revocations:[],oldSessions:[]}));}
 const input=record();assert.throws(()=>parseNativeCredentialRecord({...input,active:{...input.active,context:{server:{id:'s',name:'Server',baseUrl:'http://remote.example',publicKey:'key',policyRevision:1}}}}));
 assert.deepEqual(parseNativeCredentialRecord({version:1,revocations:[]}),{version:1,revocations:[]});
});

test('native bridge accepts only null as missing and rejects empty or malformed stored text',()=>{
 assert.equal(parseNativeCredentialJSON(null),undefined);
 for(const value of ['',undefined,0,'{}','{broken'])assert.throws(()=>parseNativeCredentialJSON(value));
 assert.deepEqual(parseNativeCredentialJSON(JSON.stringify(record())),record());
});
