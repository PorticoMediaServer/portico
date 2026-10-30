import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, HttpLocalApi, retryAfterSecondsFrom} from '../src/index.ts';
import {separateLibraryFolder, type TitleIdentityApi} from '../src/title-identity.ts';

const file={objectId:'object/1',revision:'original'};
const flush=async()=>{for(let i=0;i<40;i++)await Promise.resolve();};
const pending=(hint?:string)=>new ApiError(409,'request_in_progress','pending',true,retryAfterSecondsFrom(hint??null));
const busy=(hint?:string)=>new ApiError(503,'writer_capacity','busy',true,retryAfterSecondsFrom(hint??null));
const apiOf=(call:(...args:Parameters<TitleIdentityApi['request']>)=>Promise<unknown>):TitleIdentityApi=>({request:call as TitleIdentityApi['request']});

test('pending retries capture one immutable request and honour parsed Retry-After floors',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:1000});
 const calls:unknown[][]=[],times:number[]=[];
 const controller=new AbortController(),input={...file};
 const api=apiOf(async(...args)=>{calls.push(args);times.push(Date.now());if(calls.length<3)throw pending(calls.length===1?'7':new Date(Date.now()+11_000).toUTCString());return {itemId:'album'};});
 const result=separateLibraryFolder(api,'lib/1',input,controller.signal);
 await flush();input.objectId='changed';input.revision='changed';
 t.mock.timers.tick(6999);await flush();assert.equal(calls.length,1);
 t.mock.timers.tick(1);await flush();assert.equal(calls.length,2);
 t.mock.timers.tick(10999);await flush();assert.equal(calls.length,2);
 t.mock.timers.tick(1);await flush();assert.equal(await result,'album');
 assert.deepEqual(times,[1000,8000,19000]);
 for(const call of calls){assert.equal(call[0],'/v1/admin/libraries/lib%2F1/inventory/object%2F1/separate-folder');assert.equal(call[1],'POST');assert.equal(call[2],calls[0]![2]);assert.deepEqual(call[2],{revision:'original'});assert.ok(Object.isFrozen(call[2]));assert.equal(call[3],controller.signal);}
});

test('real HTTP transport exposes request_in_progress and Retry-After to the retry loop',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:1000});let calls=0;
 const api=new HttpLocalApi('https://folder.example','',async()=>{calls++;return calls===1?new Response(JSON.stringify({error:{code:'request_in_progress',retry:'same_request'}}),{status:409,headers:{'Retry-After':'9'}}):new Response(JSON.stringify({itemId:'real-id'}));});
 const result=separateLibraryFolder(api,'lib',file);await flush();
 assert.equal(calls,1);t.mock.timers.tick(8999);await flush();assert.equal(calls,1);
 t.mock.timers.tick(1);await flush();assert.equal(await result,'real-id');assert.equal(calls,2);
});

test('abort before start and during wait stops all requests and removes wait timer/listener',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:0});
 let calls=0;const api=apiOf(async()=>{calls++;throw pending();});
 const before=new AbortController();before.abort();await assert.rejects(separateLibraryFolder(api,'lib',file,before.signal),{name:'AbortError'});assert.equal(calls,0);
 const controller=new AbortController();let listeners=0;
 const add=controller.signal.addEventListener.bind(controller.signal),remove=controller.signal.removeEventListener.bind(controller.signal);
 t.mock.method(controller.signal,'addEventListener',(...args:Parameters<AbortSignal['addEventListener']>)=>{listeners++;return add(...args);});
 t.mock.method(controller.signal,'removeEventListener',(...args:Parameters<AbortSignal['removeEventListener']>)=>{listeners--;return remove(...args);});
 const result=separateLibraryFolder(api,'lib',file,controller.signal);const rejected=assert.rejects(result,{name:'AbortError'});await flush();assert.equal(listeners,1);controller.abort();await rejected;await flush();assert.equal(listeners,0);
 t.mock.timers.tick(120_000);await flush();assert.equal(calls,1);
});

test('in-flight cancellation rejects promptly and cannot publish a late successful id',async()=>{
 let finish!:(value:unknown)=>void;let seen:AbortSignal|undefined;
 const api=apiOf(async(_p,_m,_b,signal)=>{seen=signal;return new Promise(resolve=>{finish=resolve;});});
 const controller=new AbortController();const result=separateLibraryFolder(api,'lib',file,controller.signal);
 const rejected=assert.rejects(result,{name:'AbortError'});await flush();assert.equal(seen,controller.signal);controller.abort();await rejected;finish({itemId:'late'});await flush();
});

test('only pending 409 and writer-capacity 503 retry; other errors stop once',async()=>{
 for(const error of [new ApiError(409,'revision_mismatch','stale'),new ApiError(409,'conflict','conflict'),new ApiError(403,'forbidden','no'),new ApiError(401,'unauthorized','no'),new ApiError(503,'request_in_progress','fault'),new ApiError(503,'server_busy','busy'),new ApiError(409,'writer_capacity','conflict'),new Error('network')]){
  let calls=0;await assert.rejects(separateLibraryFolder(apiOf(async()=>{calls++;throw error;}),'lib',file),e=>e===error);assert.equal(calls,1);
 }
 for(const value of [undefined,{}, {itemId:''},{itemId:7}]){let calls=0;await assert.rejects(separateLibraryFolder(apiOf(async()=>{calls++;return value;}),'lib',file),/Unreadable/);assert.equal(calls,1);}
});

test('finite attempts, elapsed time, and oversized server floors preserve the last pending error',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:0});
 for(const hint of [undefined,'5','121','120','9'.repeat(400)]){
  let calls=0;const error=pending(hint);const start=Date.now();
  const result=separateLibraryFolder(apiOf(async()=>{calls++;throw error;}),'lib',file);const rejected=assert.rejects(result,e=>e===error);
  await flush();
  const step=hint===undefined?2000:5000;
  if(hint===undefined||hint==='5')for(let i=0;i<32;i++){t.mock.timers.tick(step);await flush();}
  await rejected;assert.equal(calls,hint===undefined?32:hint==='5'?24:1);
  if(hint!=='5'&&hint!==undefined)assert.equal(Date.now(),start,'oversized floor must not schedule an early retry');
 }
});

test('invalid identity is refused before requesting',async()=>{
 let calls=0;const api=apiOf(async()=>{calls++;return {itemId:'x'};});
 for(const [library,record] of [['',file],['lib',{objectId:'',revision:'r'}],['lib',{objectId:'o',revision:''}]] as const)await assert.rejects(separateLibraryFolder(api,library,record),/Unreadable/);
 assert.equal(calls,0);
});

test('time spent in flight counts against the retry budget and keeps the original error',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:0});
 let rejectRequest!:(error:unknown)=>void,calls=0;
 const error=pending('2');
 const result=separateLibraryFolder(apiOf(async()=>{calls++;return new Promise((_resolve,reject)=>{rejectRequest=reject;});}),'lib',file);
 const rejected=assert.rejects(result,e=>e===error);await flush();t.mock.timers.tick(119_000);rejectRequest(error);await rejected;assert.equal(calls,1);
});

test('an overdue wait cannot start another request after the time budget',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:0});let calls=0;const error=pending();
 const result=separateLibraryFolder(apiOf(async()=>{calls++;throw error;}),'lib',file);
 const rejected=assert.rejects(result,e=>e===error);await flush();t.mock.timers.tick(120_000);await rejected;assert.equal(calls,1);
});


test('real transport retries writer capacity then pending with one immutable request',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:1000});
 const calls:{url:string;body:unknown;time:number}[]=[];
 const api=new HttpLocalApi('https://folder.example','',async(url,init)=>{
  calls.push({url:String(url),body:init?.body,time:Date.now()});
  if(calls.length===1)return new Response(JSON.stringify({error:{code:'writer_capacity',retry:'same_request'}}),{status:503,headers:{'Retry-After':'7'}});
  if(calls.length===2)return new Response(JSON.stringify({error:{code:'request_in_progress',retry:'same_request'}}),{status:409,headers:{'Retry-After':'3'}});
  return new Response(JSON.stringify({itemId:'same-album'}));
 });
 const input={...file},result=separateLibraryFolder(api,'lib',input);
 await flush();input.objectId='changed';input.revision='changed';
 t.mock.timers.tick(6999);await flush();assert.equal(calls.length,1);
 t.mock.timers.tick(1);await flush();assert.equal(calls.length,2);
 t.mock.timers.tick(2999);await flush();assert.equal(calls.length,2);
 t.mock.timers.tick(1);await flush();assert.equal(await result,'same-album');
 assert.deepEqual(calls.map(c=>c.time),[1000,8000,11000]);
 assert(calls.every(c=>c.url===calls[0]!.url&&c.body===calls[0]!.body));
 assert.deepEqual(JSON.parse(String(calls[0]!.body)),{revision:'original'});
});

test('writer-capacity waits abort without another request',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:0});
 const controller=new AbortController();let calls=0;
 const result=separateLibraryFolder(apiOf(async()=>{calls++;throw busy('7');}),'lib',file,controller.signal);
 const rejected=assert.rejects(result,{name:'AbortError'});await flush();controller.abort();await rejected;
 t.mock.timers.tick(120_000);await flush();assert.equal(calls,1);
});

test('writer capacity shares the existing attempt, elapsed and server-floor limits',async t=>{
 t.mock.timers.enable({apis:['setTimeout','Date'],now:0});
 for(const hint of [undefined,'5','121']){
  let calls=0;const error=busy(hint);
  const result=separateLibraryFolder(apiOf(async()=>{calls++;throw error;}),'lib',file);
  const rejected=assert.rejects(result,e=>e===error);await flush();
  if(hint!=='121')for(let i=0;i<32;i++){t.mock.timers.tick(hint==='5'?5000:2000);await flush();}
  await rejected;assert.equal(calls,hint===undefined?32:hint==='5'?24:1);
 }
});
