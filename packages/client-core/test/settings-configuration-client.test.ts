import test from 'node:test';
import assert from 'node:assert/strict';
import {ConsoleClient,type ConsoleAPI} from '../src/console.ts';

const document={revision:4,registryRevision:'settings-r40.1',path:'/state/portico.toml',fields:[{id:'streaming.musicAwayQuality',key:'streaming.music_away_quality',current:'320',effective:'96',default:'original',source:'file',configOnly:false,applicationMode:'next-operation'}]};
const fence='a'.repeat(64);
const scope={serverId:'actual-server',viewerId:'actual-viewer'};

test('configuration loader reads genuine producer values with the existing selected-session transport',async()=>{
 let sentSignal:AbortSignal|undefined;
 const api:ConsoleAPI={request:async(path,method,body,signal)=>{
  assert.equal(path,'/v1/admin/settings/configuration');assert.equal(method,'GET');assert.equal(body,undefined);sentSignal=signal;
  return {scope:{serverId:scope.serverId,viewerFence:fence},data:document} as any;
 }};
 const client=new ConsoleClient(api,scope);
 try {
  const doc=await client.configuration();assert.equal(doc.fields[0].current,'320');assert.equal(doc.fields[0].effective,'96');assert.equal(doc.fields[0].default,'original');assert.equal(doc.fields[0].source,'file');
  assert.ok(sentSignal instanceof AbortSignal);assert.ok(Object.isFrozen(doc.fields));
 }finally{client.dispose();}
});

test('configuration cannot publish a foreign viewer or late result and never guesses missing facts',async()=>{
 let foreign=false;
 const client=new ConsoleClient({request:async()=>({scope:{serverId:scope.serverId,viewerFence:foreign?'b'.repeat(64):fence},data:document}) as any},scope);
 try{await client.configuration();client.setDraft('private','kept only in this scope');foreign=true;await assert.rejects(client.configuration(),/selected viewer changed/);assert.equal(client.hasDrafts(),false);}finally{client.dispose();}
 let resolve!:(value:unknown)=>void;
 const late=new ConsoleClient({request:async()=>new Promise(r=>resolve=r) as any},scope);
 const pending=late.configuration();late.dispose();resolve({scope:{serverId:scope.serverId,viewerFence:fence},data:document});await assert.rejects(pending,/ended/);
 const malformed=new ConsoleClient({request:async()=>({scope:{serverId:scope.serverId,viewerFence:fence},data:{...document,fields:[{...document.fields[0],current:undefined}]}}) as any},scope);
 try{await assert.rejects(malformed.configuration());}finally{malformed.dispose();}
});
