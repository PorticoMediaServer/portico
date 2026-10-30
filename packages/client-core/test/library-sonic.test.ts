import test from 'node:test';
import assert from 'node:assert/strict';
import {librarySonicAvailable,readLibrarySonic,retryLibrarySonic,saveLibrarySonic} from '../src/server-admin/library-sonic.ts';
import type {LibraryContentApi} from '../src/library-content.ts';

const status=(changes:Record<string,unknown>={})=>({libraryId:'library/a',available:true,enabled:false,defaultEnabled:true,revision:3,policyAllows:false,state:'disabled',done:2,total:4,policyRevision:5,observedAtMs:1000,...changes});
function api(handler:(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>unknown):LibraryContentApi{
 return {async request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>{return await handler(path,method,body,signal) as T;}};
}
test('Sonic readiness remains independent of enabled, policy, model and counts',async()=>{
 for(const available of [true,false]){
  const doc=await readLibrarySonic(api(()=>status({available})), 'library/a');
  assert.equal(librarySonicAvailable(doc),available);
  assert.equal(doc.enabled,false);
  assert.equal(doc.policyAllows,false);
  assert.equal(Object.isFrozen(doc),true);
 }
});
test('Sonic parser refuses missing or malformed readiness, wrong library and unknown states',async()=>{
 const missing=status();delete (missing as Partial<typeof missing>).available;
 for(const value of [missing,status({available:null}),status({available:'ready'}),status({available:1}),status({libraryId:'other'}),status({state:'ready'}),status({revision:0}),status({done:-1})]){
  await assert.rejects(readLibrarySonic(api(()=>value),'library/a'));
 }
});
test('Sonic factory default is a required producer fact independent of the stored choice',async()=>{
 const missing=status();delete (missing as Partial<typeof missing>).defaultEnabled;
 for(const value of [missing,status({defaultEnabled:null}),status({defaultEnabled:'true'}),status({defaultEnabled:1})])await assert.rejects(readLibrarySonic(api(()=>value),'library/a'));
 const doc=await readLibrarySonic(api(()=>status()),'library/a');
 assert.equal(doc.enabled,false);assert.equal(doc.defaultEnabled,true);assert.equal(doc.revision,3);
});
test('Sonic read, CAS save and retry use only the generated library routes and exact bodies',async()=>{
 const calls:unknown[][]=[];
 const transport=api((path,method,body,signal)=>{
  calls.push([path,method,body,signal]);
  return method==='PUT'?status({enabled:true,revision:4}):status();
 });
 const controller=new AbortController();
 const doc=await readLibrarySonic(transport,'library/a',controller.signal);
 const saved=await saveLibrarySonic(transport,doc,true,controller.signal);
 assert.equal(saved.revision,4);
 await retryLibrarySonic(transport,doc.libraryId,controller.signal);
 assert.deepEqual(calls.map(([path,method,body])=>[path,method,body]),[
  ['/v1/admin/libraries/library%2Fa/sonic-analysis','GET',undefined],
  ['/v1/admin/libraries/library%2Fa/sonic-analysis','PUT',{enabled:true,expectedRevision:3}],
  ['/v1/admin/libraries/library%2Fa/sonic-analysis/retry','POST',{}],
 ]);
 assert.ok(calls.every(call=>call[3]===controller.signal));
});
test('Sonic writes do not replay conflicts or accept stale acknowledgements',async()=>{
 const doc=await readLibrarySonic(api(()=>status()),'library/a');
 for(const response of [status({enabled:true}),status({enabled:false,revision:4})])await assert.rejects(saveLibrarySonic(api(()=>response),doc,true));
 let calls=0;
 const conflict=Object.assign(new Error('conflict'),{code:'administration_conflict'});
 await assert.rejects(saveLibrarySonic(api(()=>{calls++;throw conflict;}),doc,true),e=>e===conflict);
 assert.equal(calls,1);
});
test('Sonic refuses cancellation before dispatch and after a late response',async()=>{
 const controller=new AbortController();controller.abort();
 let calls=0;
 await assert.rejects(readLibrarySonic(api(()=>{calls++;return status();}),'library/a',controller.signal));
 assert.equal(calls,0);
 const late=new AbortController();
 await assert.rejects(readLibrarySonic(api(()=>{late.abort();return status();}),'library/a',late.signal));
});
