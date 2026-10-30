import test from 'node:test';
import assert from 'node:assert/strict';
import {peopleStreamLimits} from '../src/presentation/settings-bindings.ts';
import {parseMemberLimits,type AccessMember} from '../src/server-administration.ts';
import {SettingsPeopleClient} from '../src/settings-people.ts';

const limits=parseMemberLimits({accessTokens:false,maxProfiles:3,allowRemote:false,conversion:'audio',maxStreamsMode:'inherit',maxStreams:0,remoteBitrateKbps:1280,schedule:{timezone:'America/Halifax',windows:[{days:[1,2],startMinute:60,endMinute:120}]},channelPolicy:{mode:'allow',channels:['real-channel']},tagPolicy:{deniedLabels:['real-label']}});
const person:AccessMember={accountId:'real-person',username:'realperson',profileId:'main-profile',role:'member',disabled:false,revision:8,allowedLibraries:['real-library'],limitsRevision:12,limits,serverMaxStreams:2,effectiveMaxStreams:2};

test('inherit and explicit unlimited retain different choices even with identical zero/count facts',()=>{
 assert.equal(peopleStreamLimits.read(person),'default');
 const explicit={...person,limits:{...limits,maxStreamsMode:'unlimited' as const}};
 assert.equal(peopleStreamLimits.read(explicit),'unlimited');
 assert.equal(peopleStreamLimits.read({...person,limits:{...limits,maxStreamsMode:'limit',maxStreams:6}}),'6');
 for(const invalid of [{maxStreamsMode:'inherit',maxStreams:1},{maxStreamsMode:'unlimited',maxStreams:2},{maxStreamsMode:'limit',maxStreams:0},{maxStreamsMode:'unknown',maxStreams:0}]) assert.throws(()=>peopleStreamLimits.read({limits:{...limits,...invalid}} as any));
});

test('member patches preserve every unrelated permission and use genuine mode/count pairs',()=>{
 for(const [choice,mode,count] of [['default','inherit',0],['unlimited','unlimited',0],['6','limit',6]] as const){
  const changed=peopleStreamLimits.patch(person,choice);
  assert.deepEqual(changed,{...limits,maxStreamsMode:mode,maxStreams:count});
  assert.equal(changed.schedule,limits.schedule);
 }
 assert.equal(person.limits.maxStreamsMode,'inherit');
 for(const invalid of [0,2,'0','02','2.0','1001','keep',null,undefined]) assert.throws(()=>peopleStreamLimits.patch(person,invalid));
});

test('bulk Keep removes only the stream edit while explicit inherit and unlimited remain distinct',()=>{
 const other={allowRemote:false,accessTokens:false};
 assert.equal(peopleStreamLimits.readBulk(undefined),'keep');
 for(const choice of ['default','unlimited','2']) {
  const changed=peopleStreamLimits.patchBulk(other,choice);
  assert.equal(peopleStreamLimits.readBulk(changed),choice);
  assert.deepEqual(peopleStreamLimits.patchBulk(changed,'keep'),other);
 }
 assert.throws(()=>peopleStreamLimits.readBulk({maxStreams:2}));
});

test('stream changes use the existing member CAS transport and retain current effective facts from the response',async()=>{
 let sent:any;
 const client=new SettingsPeopleClient({request:async(path,method,body)=>{
  assert.equal(path,'/v1/admin/access/members/real-person/limits');assert.equal(method,'PUT');sent=body;
  return {accountId:person.accountId,revision:13,limits:(body as any).limits,serverMaxStreams:2,effectiveMaxStreams:2} as any;
 }},()=>true);
 try {
  const saved=await client.limits(person,peopleStreamLimits.patch(person,'unlimited'));
  assert.equal(sent.expectedRevision,12);assert.ok(sent.operationId);
  assert.equal(saved.limits.maxStreamsMode,'unlimited');assert.equal(saved.limits.maxStreams,0);
  assert.equal(saved.effectiveMaxStreams,2);assert.equal(saved.serverMaxStreams,2);
  assert.deepEqual(sent.limits.tagPolicy,limits.tagPolicy);
 } finally {client.dispose();}
});
