import test from 'node:test';
import assert from 'node:assert/strict';
import {CapabilityPublisher} from '../src/playback-v1/options.ts';
import {androidDeclaredProfile,withViewerSettings,type CapabilityDocument} from '../src/client-profile.ts';
import type {V1Response} from '../src/playback-v1/http.ts';

const a=androidDeclaredProfile({television:true});
const b=withViewerSettings(a,{overrides:{forceStereo:true}});
const c=withViewerSettings(a,{overrides:{disableHdr:true}});
const ok:V1Response={status:204,headers:{}};
const settle=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};
function server(){
 const requests:{document:CapabilityDocument;resolve:(r:V1Response)=>void;reject:(e:unknown)=>void}[]=[];
 const publisher=new CapabilityPublisher({send:r=>new Promise((resolve,reject)=>{requests.push({document:r.body as CapabilityDocument,resolve,reject});})});
 return {publisher,requests};
}

test('FMT-02: capability acceptance, current state, invalid input and publication failure are distinct',async()=>{
 let fail=false,calls=0;
 const publisher=new CapabilityPublisher({send:async()=>{calls++;return fail?{status:503,headers:{}}:ok;}});
 assert.equal(await publisher.publish(a),'accepted');assert.equal(await publisher.publish(a),'current');assert.equal(calls,1);
 fail=true;assert.equal(await publisher.publish(b),'failed');assert.equal(await publisher.publish({...a,version:1} as never),'failed');assert.equal(calls,2);
 fail=false;assert.equal(await publisher.publish(b),'accepted');assert.equal(calls,3);
});

test('FMT-17: concurrent capability changes publish only the latest queued document',async()=>{
 const {publisher,requests}=server();
 const first=publisher.publish(a),superseded=publisher.publish(b),latest=publisher.publish(c);
 assert.equal(requests.length,1);assert.equal(await superseded,'failed');requests[0].resolve(ok);await settle();assert.equal(await first,'failed','an accepted but superseded document cannot authorize a start');
 assert.equal(requests.length,2);assert.deepEqual(requests[1].document,c);requests[1].resolve(ok);assert.equal(await latest,'accepted');assert.equal(await publisher.publish(c),'current');
});

test('FMT-17: A-B-A while A is in flight cancels B, retaining the latest A',async()=>{
 const {publisher,requests}=server();
 const first=publisher.publish(a),middle=publisher.publish(b),latest=publisher.publish(a);
 assert.equal(await middle,'failed');requests[0].resolve(ok);
 assert.equal(await first,'accepted');assert.equal(await latest,'accepted');assert.equal(requests.length,1);assert.equal(await publisher.publish(a),'current');
});

test('FMT-17: returning to an already accepted A while B runs republishes A after B',async()=>{
 const {publisher,requests}=server();
 const initial=publisher.publish(a);requests[0].resolve(ok);await initial;
 const middle=publisher.publish(b),latest=publisher.publish(a);requests[1].resolve(ok);assert.equal(await middle,'failed');await settle();
 assert.equal(requests.length,3);assert.deepEqual(requests[2].document,a);requests[2].resolve(ok);assert.equal(await latest,'accepted');
});

test('FMT-02: a lost PUT response invalidates the old accepted-document cache',async()=>{
 let stored:unknown,lose=false,calls=0;
 const publisher=new CapabilityPublisher({send:async r=>{calls++;stored=r.body;if(lose)throw new Error('lost response');return ok;}});
 await publisher.publish(a);lose=true;assert.equal(await publisher.publish(b),'failed');assert.deepEqual(stored,b);
 lose=false;assert.equal(await publisher.publish(a),'accepted');assert.equal(calls,3);assert.deepEqual(stored,a);
});

test('FMT-17: queued bodies are captured and aborted queued publications are not sent',async()=>{
 const {publisher,requests}=server();
 const first=publisher.publish(a),mutable=structuredClone(b);
 const latest=publisher.publish(mutable);Object.assign(mutable,{version:1});requests[0].resolve(ok);await first;await settle();
 assert.deepEqual(requests[1].document,b);requests[1].resolve(ok);await latest;
 const active=publisher.publish(c),abort=new AbortController(),cancelled=publisher.publish(a,abort.signal);abort.abort();requests[2].resolve(ok);await active;
 assert.equal(await cancelled,'failed');assert.equal(requests.length,3);
});

test('FMT-17: reset prevents an old in-flight acknowledgement from making a new scope current',async()=>{
 const {publisher,requests}=server();
 const old=publisher.publish(a);publisher.reset();const current=publisher.publish(a);requests[0].resolve(ok);assert.equal(await old,'failed');await settle();
 assert.equal(requests.length,2);requests[1].resolve(ok);assert.equal(await current,'accepted');assert.equal(await publisher.publish(a),'current');
});

test('FMT-17: a new live caller replaces an aborted same-document publication',async()=>{
 for(const queued of [false,true]){
  const {publisher,requests}=server(),retired=new AbortController();
  const first=publisher.publish(a,queued?undefined:retired.signal);
  const pending=queued?publisher.publish(b,retired.signal):first;
  retired.abort();const latest=publisher.publish(queued?b:a);
  requests[0].resolve(ok);await settle();assert.equal(requests.length,2);
  requests[1].resolve(ok);assert.equal(await pending,'failed');assert.equal(await latest,'accepted');await first;
 }
});

test('FMT-17: an older same-document lifetime cannot cancel a newer caller that already joined',async()=>{
 const {publisher,requests}=server(),older=new AbortController(),newer=new AbortController();
 const first=publisher.publish(a,older.signal),latest=publisher.publish(a,newer.signal);older.abort();
 requests[0].resolve(ok);await settle();assert.equal(requests.length,2);assert.equal(await first,'failed');
 requests[1].resolve(ok);assert.equal(await latest,'accepted');
});

test('FMT-17: different live lifetimes reuse an acknowledged identical document without another PUT',async()=>{
 const {publisher,requests}=server(),older=new AbortController(),newer=new AbortController();
 const first=publisher.publish(a,older.signal),latest=publisher.publish(a,newer.signal);requests[0].resolve(ok);
 assert.equal(await first,'failed');assert.equal(await latest,'current');assert.equal(requests.length,1);
});
