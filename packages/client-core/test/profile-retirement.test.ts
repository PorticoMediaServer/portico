import test from 'node:test';
import assert from 'node:assert/strict';
import {ManagedProfilesClient,parseProfileRetirement} from '../src/profile-management.ts';

const lifetime='0123456789abcdef0123456789abcdef';
const otherLifetime='fedcba9876543210fedcba9876543210';
const preview={profileId:'child',revision:7,lifetime,primary:false,name:'Child',playlists:2};
const retirement=(state:string,lifetimeValue=lifetime,profileId='child')=>({profileId,lifetime:lifetimeValue,state});
const profile=(overrides:Record<string,unknown>={})=>({id:'main',name:'Main',primary:true,position:0,art:'blue',revision:2,pinRevision:1,pinRequired:false,...overrides});
const scope={authority:'local' as const,accountId:'account',serverId:'server'};
type Call={path:string;method?:string;body?:unknown;signal?:AbortSignal};
/** A scripted transport: each request takes the next scripted answer, and the test sees every call. */
function scripted(answers:Array<unknown|Error>,calls:Call[]){return {request:async<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{
 calls.push({path,method,body,signal});
 const next=answers.shift();
 if(next===undefined)throw new Error('unexpected request '+path);
 if(next instanceof Error)throw next;
 return structuredClone(next) as T;
}};}
const deletionPath='/v1/direct/profiles/child';
const retirementPath='/v1/direct/profiles/child/retirement?lifetime='+lifetime;

test('a retirement names the exact profile and 32-hex lifetime it was previewed for',()=>{
 assert.deepEqual({...parseProfileRetirement(retirement('removed'),{profileId:'child',lifetime})},{profileId:'child',lifetime,state:'removed'});
 for(const patch of [{profileId:'other'},{lifetime:otherLifetime},{lifetime:lifetime.toUpperCase()},{lifetime:lifetime.slice(0,31)},{lifetime:lifetime+'0'},{lifetime:7},{state:'done'},{state:'REMOVED'},{state:''},{state:undefined},{profileId:''}]){
  assert.throws(()=>parseProfileRetirement({...retirement('removing'),...patch},{profileId:'child',lifetime}),undefined,JSON.stringify(patch));
 }
 for(const raw of [null,undefined,'removed',[],{}])assert.throws(()=>parseProfileRetirement(raw,{profileId:'child',lifetime}));
 assert.ok(Object.isFrozen(parseProfileRetirement(retirement('removed'),{profileId:'child',lifetime})));
});

test('a deletion preview without the server lifetime is unreadable and never offered',async()=>{
 const calls:Call[]=[];
 const {lifetime:_omitted,...withoutLifetime}=preview;
 for(const answer of [{...withoutLifetime,primary:false},{...withoutLifetime,lifetime:otherLifetime.toUpperCase(),primary:false},{profileId:'other',revision:7,lifetime,primary:false},{profileId:'child',revision:0,lifetime,primary:false},{profileId:'child',revision:7,lifetime,primary:true}]){
  await assert.rejects(new ManagedProfilesClient(scripted([answer],calls),scope).previewDelete(profile({id:'child',primary:false}) as never));
 }
 const client=new ManagedProfilesClient(scripted([{...preview,primary:false}],calls),scope);
 const checked=await client.previewDelete(profile({id:'child',primary:false}) as never);
 assert.equal(checked.lifetime,lifetime);assert.ok(Object.isFrozen(checked));
 assert.equal(calls.at(-1)?.path,deletionPath+'/deletion-preview');
 await assert.rejects(client.previewDelete(profile({id:'main'}) as never));
});

test('an accepted deletion replays one immutable body and lifetime and waits for the server',async()=>{
 const calls:Call[]=[];
 const answers=[retirement('removing'),retirement('removing'),retirement('removed')];
 const statuses:string[]=[];
 const client=new ManagedProfilesClient(scripted(answers,calls),scope);
 const checked=await client.delete(preview,{intervalMs:1,maxWaitMs:100,onStatus:value=>statuses.push(value.state)});
 assert.equal(checked.state,'removed');
 assert.deepEqual(statuses,['removing','removing','removed']);
 assert.deepEqual(calls.map(call=>[call.path,call.method]),[[deletionPath,'DELETE'],[retirementPath,'GET'],[retirementPath,'GET']]);
 assert.deepEqual(calls[0]!.body,{expectedRevision:7,lifetime,deleteSavedResources:true});
 assert.ok(Object.isFrozen(calls[0]!.body),'the accepted body is frozen so a replay cannot differ');
 assert.throws(()=>{(calls[0]!.body as Record<string,unknown>).lifetime=otherLifetime;},TypeError);
 assert.equal(calls[1]!.body,undefined);
});

test('a profile already removed is reported once and never polled',async()=>{
 const calls:Call[]=[];const statuses:string[]=[];
 const client=new ManagedProfilesClient(scripted([retirement('removed')],calls),scope);
 assert.equal((await client.delete(preview,{intervalMs:1,maxWaitMs:100,onStatus:value=>statuses.push(value.state)})).state,'removed');
 assert.deepEqual(statuses,['removed']);assert.equal(calls.length,1);
});

test('a blocked retirement is not success, is reported, and does not spin',async()=>{
 const calls:Call[]=[];const statuses:string[]=[];
 const client=new ManagedProfilesClient(scripted([retirement('blocked')],calls),scope);
 await assert.rejects(client.delete(preview,{intervalMs:1,maxWaitMs:100,onStatus:value=>statuses.push(value.state)}),{code:'retirement_blocked'});
 assert.deepEqual(statuses,['blocked']);assert.equal(calls.length,1);
 const pending:Call[]=[];
 const blockedLater=new ManagedProfilesClient(scripted([retirement('removing'),retirement('blocked')],pending),scope);
 await assert.rejects(blockedLater.delete(preview,{intervalMs:1,maxWaitMs:100}),{code:'retirement_blocked'});
 assert.equal(pending.length,2);
});

test('a lifetime that does not match the preview is never read as removed',async()=>{
 for(const answer of [retirement('removed',otherLifetime),retirement('removed',lifetime,'other'),retirement('done')]){
  const calls:Call[]=[];const statuses:string[]=[];
  const client=new ManagedProfilesClient(scripted([retirement('removing'),answer],calls),scope);
  await assert.rejects(client.delete(preview,{intervalMs:1,maxWaitMs:100,onStatus:value=>statuses.push(value.state)}),{code:'invalid_response'});
  assert.deepEqual(statuses,['removing'],'no unverified state reaches the dialog');
 }
});

test('a lost status read or a 404 is never taken as removed',async()=>{
 for(const answer of [Object.assign(new Error('offline'),{name:'TypeError'}),Object.assign(new Error('gone'),{status:404})]){
  const calls:Call[]=[];
  const client=new ManagedProfilesClient(scripted([retirement('removing'),answer],calls),scope);
  await assert.rejects(client.delete(preview,{intervalMs:1,maxWaitMs:100}));
  assert.equal(calls.length,2);
 }
});

test('an aborted wait stops its one timer, asks no further question and reports nothing late',async()=>{
 const calls:Call[]=[];const statuses:string[]=[];const controller=new AbortController();
 const client=new ManagedProfilesClient(scripted([retirement('removing')],calls),scope);
 const task=client.delete(preview,{signal:controller.signal,intervalMs:20,onStatus:value=>statuses.push(value.state)});
 setTimeout(()=>controller.abort(),5);
 await assert.rejects(task,{name:'AbortError'});
 assert.equal(calls.length,1,'the poll after the aborted wait never leaves');
 assert.deepEqual(statuses,['removing']);
});

test('an aborted status read keeps its answer out of the dialog',async()=>{
 const calls:Call[]=[];const statuses:string[]=[];const controller=new AbortController();
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
 const api={request:async<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{
  calls.push({path,method,body,signal});
  if(method==='DELETE')return retirement('removing') as T;
  await held;return retirement('removed') as T;
 }};
 const task=new ManagedProfilesClient(api,scope).delete(preview,{signal:controller.signal,intervalMs:1,maxWaitMs:200,onStatus:value=>statuses.push(value.state)});
 await new Promise(resolve=>setTimeout(resolve,5));
 controller.abort();release();
 await assert.rejects(task,{name:'AbortError'});
 assert.deepEqual(statuses,['removing']);
});

test('without a test bound the wait keeps checking until the purge finishes',async()=>{
 const calls:Call[]=[];const statuses:string[]=[];
 const answers=[retirement('removing'),...Array.from({length:5},()=>retirement('removing')),retirement('removed')];
 const client=new ManagedProfilesClient(scripted(answers,calls),scope);
 assert.equal((await client.delete(preview,{intervalMs:1,onStatus:value=>statuses.push(value.state)})).state,'removed');
 assert.equal(calls.length,7);assert.deepEqual(statuses,[...Array(6).fill('removing'),'removed']);
});

test('a lost delete response is retried by the caller with the very same body and lifetime',async()=>{
 const calls:Call[]=[];
 const client=new ManagedProfilesClient(scripted([Object.assign(new Error('offline'),{name:'TypeError'}),retirement('removed')],calls),scope);
 await assert.rejects(client.delete(preview,{intervalMs:1,maxWaitMs:50}));
 assert.equal((await client.delete(preview,{intervalMs:1,maxWaitMs:50})).state,'removed');
 const deletes=calls.filter(call=>call.method==='DELETE');
 assert.equal(deletes.length,2);
 assert.deepEqual(deletes[0]!.body,deletes[1]!.body);
 assert.deepEqual(deletes[1]!.body,{expectedRevision:7,lifetime,deleteSavedResources:true});
});

test('a delete answer that arrives after the abort is never published as progress',async()=>{
 const calls:Call[]=[];const statuses:string[]=[];const controller=new AbortController();
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
 const api={request:async<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{
  calls.push({path,method,body,signal});
  await held;return retirement('removing') as T;
 }};
 const task=new ManagedProfilesClient(api,scope).delete(preview,{signal:controller.signal,intervalMs:1,onStatus:value=>statuses.push(value.state)});
 await new Promise(resolve=>setTimeout(resolve,5));
 controller.abort();release();
 await assert.rejects(task,{name:'AbortError'});
 assert.deepEqual(statuses,[]);assert.equal(calls.length,1);
});

test('a retirement that is still running is waited for, not abandoned, until the viewer retires',async()=>{
 const calls:Call[]=[];
 const controller=new AbortController();
 const client=new ManagedProfilesClient({request:async<T>(path:string,method?:string)=>{calls.push({path,method});return retirement('removing') as T;}},scope);
 const task=client.delete(preview,{signal:controller.signal,intervalMs:1});
 setTimeout(()=>controller.abort(),20);
 await assert.rejects(task,{name:'AbortError'});
 assert.ok(calls.length>10,'still checking after any fixed short wait would have passed');
});

test('a bounded wait gives up in writing instead of polling forever',async()=>{
 const calls:Call[]=[];
 const client=new ManagedProfilesClient({request:async<T>(path:string,method?:string)=>{calls.push({path,method});return retirement('removing') as T;}},scope);
 await assert.rejects(client.delete(preview,{intervalMs:10,maxWaitMs:50}),{code:'retirement_pending'});
 assert.equal(calls.length,1+5,'five bounded waits, one request each');
});

test('a checked Retry-After is honoured once instead of spinning on an error',async()=>{
 const calls:Call[]=[];
 const busy=Object.assign(new Error('busy'),{retryAfterSeconds:1});
 const client=new ManagedProfilesClient(scripted([retirement('removing'),busy,retirement('removed')],calls),scope);
 assert.equal((await client.delete(preview,{intervalMs:1,maxWaitMs:5000})).state,'removed');
 assert.deepEqual(calls.map(call=>call.method),['DELETE','GET','GET']);
});
