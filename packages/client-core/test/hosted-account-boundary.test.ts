import test from 'node:test';
import assert from 'node:assert/strict';
import {CredentialSessionService, HttpCredentialApi, parseAccountSession, parseDeletionPreview} from '../src/index.ts';
import {parseNativeCredentialRecord,parseBrowserCredentialRecord} from '../src/credential-format.ts';
import {routes} from '../../contracts/src/hosted/routes.ts';
import {schemas} from '../../contracts/src/hosted/schemas.ts';

// Independently authored profile-free Hosted expectations. Server profiles belong only to the
// direct account snapshot; the retired metadata-refresh tests are preserved in the red receipt.
const session={accessToken:'access_A',refreshToken:'refresh_A',familyId:'family_A',expiresAt:'2099-01-01T00:00:00Z',refreshExpiresAt:'2099-01-02T00:00:00Z',account:{id:'account_A',username:'alice',displayName:'Alice'}};
test('Hosted grants and protected records reject a profile field, including an empty list',()=>{
 const record={version:1,revocations:[],active:{session,authority:'refresh_A',context:{}}};
 assert.deepEqual(parseAccountSession(session,'native'),session);
 assert.deepEqual(parseNativeCredentialRecord(record),record);
 const {refreshToken,...browser}=session;
 assert.deepEqual(parseAccountSession(browser,'browser'),browser);
 for(const profiles of [[],[{id:'profile_A',name:'Alice'}]]){
  assert.throws(()=>parseAccountSession({...session,profiles},'native'));
  assert.throws(()=>parseAccountSession({...browser,profiles},'browser'));
  assert.throws(()=>parseNativeCredentialRecord({...record,active:{...record.active,session:{...session,profiles}}}));
  assert.throws(()=>parseBrowserCredentialRecord({...record,active:{...record.active,authority:{familyId:'family_A'},session:{...browser,accessToken:'',profiles}}}));
 }
 assert.equal('refreshProfiles' in CredentialSessionService.prototype,false);
});
test('checked Hosted registry and document schemas expose security and directory work without profile/preference/erasure routes',()=>{
 const ids=new Set<string>(routes.map(row=>row.id));
 for(const id of ['account_preferences_get','account_preferences_set','server_claim_cleanup','server_erasures_list','server_erasure_ack','server_storage_warning'])assert.equal(ids.has(id),false,id);
 assert.equal(ids.has('server_registration_leave'),true);
 assert.deepEqual(Object.keys(schemas.AccountGetResponse.properties),['security']);
 for(const name of ['SessionCreateResponse','SessionRefreshResponse','DeviceAuthorizationTokenResponse','MeGetResponse'] as const)assert.equal(Object.hasOwn(schemas[name].properties,'profiles'),false,name);
 assert.equal(Object.hasOwn(schemas.ServerHeartbeatResponse.properties,'cleanupPending'),false);
});
test('deletion preview concerns server links and refuses the retired server-profile erasure disposition',()=>{
 const preview={deletionToken:'delete_A',expiresAt:'2099-01-01T00:00:00Z',ownedServers:[],affectedServers:[{id:'server_A',name:'Home'}],canDelete:true};
 assert.deepEqual(parseDeletionPreview(preview),preview);
 assert.throws(()=>parseDeletionPreview({...preview,profileCount:0}));
 assert.throws(()=>parseDeletionPreview({...preview,requiredDisposition:'delete_owned_personal_resources'}));
});
test('actual native refresh transport parses current profile-free grant and refuses an old profile-bearing response',async()=>{
 let profiles=false;const bodies:unknown[]=[];
 const api=new HttpCredentialApi('https://accounts.test',async(_url,init)=>{bodies.push(JSON.parse(String(init?.body)));return Response.json(profiles?{...session,profiles:[]}:session);});
 assert.deepEqual(await api.refresh('refresh_A','request_A'),session);
 assert.deepEqual(bodies[0],{refreshToken:'refresh_A',requestId:'request_A'});
 profiles=true;await assert.rejects(api.refresh('refresh_A','request_B'));
});
