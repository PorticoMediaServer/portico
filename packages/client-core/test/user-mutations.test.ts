import test from 'node:test';
import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import {UserMutationCoordinator,UserMutationError,emptyUserMutationState,newUserMutationKey,userMutationRetryAfterMs,type DurableUserMutationStore,type UserMutationState,type UserMutationScope,type UserMutationEntry,type UserMutationResponse} from '../src/user-mutations.ts';
const copy=<T>(v:T):T=>JSON.parse(JSON.stringify(v));
class Store implements DurableUserMutationStore{
 state=emptyUserMutationState();writes=0;fail=false;
 async read(){return copy(this.state);}
 async compareAndSwap(revision:number,next:UserMutationState){if(this.fail)throw new Error('disk full');if(this.state.revision!==revision)return false;this.state=copy(next);this.writes++;return true;}
}
class Timers{
 now=1_800_000_000_000;next=0;jobs=new Map<number,{at:number;call:()=>void}>();
 setTimeout=(call:()=>void,delay=0)=>{const id=++this.next;this.jobs.set(id,{at:this.now+delay,call});return id as unknown as ReturnType<typeof globalThis.setTimeout>;};
 clearTimeout=(id:ReturnType<typeof globalThis.setTimeout>)=>{this.jobs.delete(id as unknown as number);};
 async tick(ms=0){this.now+=ms;for(let round=0;round<30;round++){for(const[id,job]of [...this.jobs])if(job.at<=this.now){this.jobs.delete(id);job.call();}await setImmediate();}}
}
const scope:UserMutationScope={serverId:'server',origin:'http://100.120.50.24:8096',authority:'local',accountId:'account',profileId:'profile',familyId:'family',deviceId:'device'};
const input={operation:'patch_preferences',method:'PATCH',path:'/v1/preferences',target:'preferences',intentId:'one',body:{idempotencyKey:'original-id',expectedRevision:1,values:{'reading.theme':'sepia'}}};
const receipt=(entry:UserMutationEntry,status='applied')=>({status:200,headers:{},body:{key:entry.key,operation:entry.operation,status,targetId:'preferences',revision:2,committedMs:1_800_000_000_000,expiresMs:1_800_000_000_000+30*86400_000}});
const ok:UserMutationResponse={status:200,headers:{},body:{current:true}};
function coordinator(store:Store,timers:Timers,transport:ConstructorParameters<typeof UserMutationCoordinator>[0]['transport'],getScope=()=>scope){return new UserMutationCoordinator({store,transport,scope:getScope,now:()=>timers.now,random:()=>0,ownerId:'test-owner',timers:timers as unknown as Pick<typeof globalThis,'setTimeout'|'clearTimeout'>});}

test('persists immutable request before first send and persists tiny receipt before resolving',async()=>{
 const store=new Store(),timers=new Timers();let sent=0;
 const core=coordinator(store,timers,{send:async entry=>{sent++;assert.equal(store.state.entries[0].key,entry.key);assert.equal((entry.body as any).expectedRevision,1);assert.equal((entry.body as any).idempotencyKey,entry.key);assert.ok(entry.key.startsWith('p251_'));return ok;},lookup:async entry=>receipt(entry),recover:async()=>ok});
 const result=core.run(input);await timers.tick();assert.deepEqual(await result,ok);assert.equal(sent,1);assert.equal(store.state.entries[0].state,'confirmed');assert.equal(store.state.entries[0].receipt?.revision,2);assert.equal(store.state.entries[0].body,undefined);assert.equal(store.state.entries[0].fingerprint,'');assert.equal(JSON.stringify(store.state).includes('sepia'),false);assert.equal('current'in(store.state.entries[0].receipt??{}),false);core.dispose();
});
test('lost commit response reconciles same key and applies once after coordinator restart',async()=>{
 const store=new Store(),timers=new Timers();let applied:UserMutationEntry|undefined,sends=0;
 const transport={send:async(entry:UserMutationEntry)=>{sends++;applied=copy(entry);throw new Error('lost response');},lookup:async(entry:UserMutationEntry)=>applied?receipt(entry):{status:404,headers:{},body:{error:{code:'receipt_unknown'}}},recover:async()=>ok};
 const first=coordinator(store,timers,transport);const abort=new AbortController();const pending=first.run(input,abort.signal);const cancelled=assert.rejects(pending,{name:'AbortError'});await timers.tick();abort.abort();await cancelled;first.dispose();assert.equal(store.state.entries[0].state,'pending');const key=store.state.entries[0].key;
 const second=coordinator(store,timers,transport);await second.start();await timers.tick(2000);assert.equal(sends,1);assert.equal(store.state.entries[0].key,key);assert.equal(store.state.entries[0].state,'confirmed');second.dispose();
});
test('caller cancellation keeps accepted intent, and unavailable storage sends nothing',async()=>{
 const store=new Store(),timers=new Timers();let sends=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return ok;},lookup:async e=>receipt(e),recover:async()=>ok});
 store.fail=true;await assert.rejects(core.run(input));assert.equal(sends,0);assert.equal(store.state.entries.length,0);store.fail=false;
 const abort=new AbortController(),promise=core.run(input,abort.signal);const cancelled=assert.rejects(promise,{name:'AbortError'});await setImmediate();abort.abort();await cancelled;assert.equal(store.state.entries.length,1);await timers.tick();assert.equal(sends,1);assert.equal(store.state.entries[0].state,'confirmed');core.dispose();
});
test('Retry-After floor above 30 seconds and HTTP dates are not shortened',async()=>{
 const store=new Store(),timers=new Timers();let sends=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return {status:503,headers:{'Retry-After':'120'},body:{error:{code:'writer_capacity'}}};},lookup:async()=>({status:404,headers:{},body:{error:{code:'receipt_unknown'}}}),recover:async()=>ok});
 const abort=new AbortController(),promise=core.run(input,abort.signal),cancelled=assert.rejects(promise,{name:'AbortError'});await timers.tick();assert.equal(sends,1);assert.ok(store.state.entries[0].dueMs>=timers.now+120000);await timers.tick(119999);assert.equal(sends,1);abort.abort();await cancelled;core.dispose();
 assert.equal(userMutationRetryAfterMs(new Date(1_800_000_000_000+300000).toUTCString(),1_800_000_000_000),300000);
});
test('a scope change quarantines pending work and never sends into the next family',async()=>{
 const store=new Store(),timers=new Timers();let current=scope,sends=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return ok;},lookup:async e=>receipt(e),recover:async()=>ok},()=>current);
 const promise=core.run(input),refused=assert.rejects(promise,(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_scope_changed');await setImmediate();current={...scope,familyId:'new-family'};await timers.tick();await refused;assert.equal(sends,0);assert.equal(store.state.entries[0].state,'quarantined');assert.ok(store.state.entries[0].body);core.dispose();
});
test('parallel owners CAS one key and never overlap same-target commands',async()=>{
 const store=new Store(),timers=new Timers();let sends=0,release!:()=>void;
 const barrier=new Promise<void>(resolve=>{release=resolve;});
 const transport={send:async()=>{sends++;await barrier;return ok;},lookup:async(e:UserMutationEntry)=>receipt(e),recover:async()=>ok};
 const one=coordinator(store,timers,transport),two=new UserMutationCoordinator({store,scope:()=>scope,transport,ownerId:'other-owner',now:()=>timers.now,timers:timers as any});
 const p1=one.run(input),p2=two.run(input);await timers.tick();assert.equal(sends,1);assert.equal(store.state.entries.length,1);release();await timers.tick();await p1;await timers.tick(30001);await p2;assert.equal(sends,1);one.dispose();two.dispose();
});
test('conflict retains original draft/revision and secret settings are explicitly not queued',async()=>{
 const store=new Store(),timers=new Timers();const core=coordinator(store,timers,{send:async()=>({status:409,headers:{},body:{error:{code:'revision_mismatch'}}}),lookup:async e=>receipt(e),recover:async()=>ok});
 const promise=core.run(input),refused=assert.rejects(promise,(e:unknown)=>e instanceof UserMutationError&&e.code==='revision_mismatch');await timers.tick();await refused;assert.equal(store.state.entries[0].state,'review');assert.equal((store.state.entries[0].body as any).expectedRevision,1);
 await assert.rejects(core.run({...input,intentId:'secret',body:{password:'do-not-store'}}),(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_not_queued');assert.equal(store.state.entries.length,1);core.dispose();
});
test('quota failure never evicts unresolved work and never sends the new draft',async()=>{
 const store=new Store(),timers=new Timers();store.state.entries=Array.from({length:256},(_,i)=>({key:'p251_1800000000000_'+i.toString(16).padStart(32,'0'),operation:'patch_preferences',method:'PATCH',path:'/v1/preferences',target:'preferences',intentId:'old-'+i,fingerprint:'{}',scope,state:'review',attempts:1,dueMs:timers.now,headers:{}}));let sends=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return ok;},lookup:async e=>receipt(e),recover:async()=>ok});await assert.rejects(core.run(input),(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_not_queued');assert.equal(store.state.entries.length,256);assert.equal(sends,0);core.dispose();
});
test('key creation needs getRandomValues only, no subtle or locks',()=>{
 const key=newUserMutationKey(1_800_000_000_000,out=>{out.fill(7);return out;});assert.match(key,/^p251_1800000000000_07070707070707070707070707070707$/);
});

test('duplicates reuse custody without changing original body, and changed drafts conflict',async()=>{
 const store=new Store(),timers=new Timers();let release!:()=>void,sends=0;
 const gate=new Promise<void>(r=>{release=r;});
 const core=coordinator(store,timers,{send:async()=>{sends++;await gate;return ok;},lookup:async e=>receipt(e),recover:async()=>ok});
 const original=copy(input),first=core.run(original);original.body.expectedRevision=999;
 const duplicate=core.run(input);await timers.tick();const persisted=copy(store.state.entries[0]);
 await assert.rejects(core.run({...input,body:{...input.body,expectedRevision:2}}),(e:unknown)=>e instanceof UserMutationError&&e.code==='idempotency_conflict');
 assert.deepEqual(store.state.entries[0].body,persisted.body);assert.equal((persisted.body as any).expectedRevision,1);assert.equal(sends,1);
 release();await timers.tick();await Promise.all([first,duplicate]);assert.equal(sends,1);core.dispose();
});

test('applied lookup reads current typed state without storing or inventing a historical response',async()=>{
 const store=new Store(),timers=new Timers();let sends=0,reads=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;throw new Error('commit response lost');},lookup:async e=>({...receipt(e),body:{...receipt(e).body,accidentalFullResponse:{secret:'must not persist'}}}),recover:async(e,r)=>{reads++;assert.equal(r.revision,2);assert.equal(store.state.entries[0].state,'confirmed');assert.equal('accidentalFullResponse' in r,false);return {status:200,headers:{},body:{revision:7,items:['current']}};}});
 const result=core.run(input);await timers.tick();await timers.tick(2000);
 assert.deepEqual((await result).body,{revision:7,items:['current']});assert.equal(sends,1);assert.equal(reads,1);
 const stored=JSON.stringify(store.state);assert.equal(stored.includes('must not persist'),false);assert.equal(stored.includes('items'),false);core.dispose();
});

test('two tabs share the two-send ceiling and serialize target work',async()=>{
 const store=new Store(),timers=new Timers(),releases=new Map<string,()=>void>();let active=0,peak=0;const sent:string[]=[];
 const transport={send:async(e:UserMutationEntry)=>{active++;peak=Math.max(peak,active);sent.push(e.intentId);await new Promise<void>(r=>releases.set(e.intentId,r));active--;return ok;},lookup:async(e:UserMutationEntry)=>receipt(e),recover:async()=>ok};
 const one=coordinator(store,timers,transport),two=new UserMutationCoordinator({store,scope:()=>scope,transport,ownerId:'tab-two',now:()=>timers.now,timers:timers as any});
 const requests=[one.run({...input,intentId:'A'}),two.run({...input,intentId:'A2'}),two.run({...input,intentId:'B',target:'other'}),one.run({...input,intentId:'C',target:'third'})];
 await timers.tick();assert.deepEqual(new Set(sent),new Set(['A','B']));assert.equal(peak,2);
 releases.get('B')!();await timers.tick();assert.ok(sent.includes('C'));assert.equal(sent.includes('A2'),false);
 releases.get('A')!();await timers.tick();assert.ok(sent.includes('A2'));assert.equal(peak,2);
 releases.get('A2')!();releases.get('C')!();await timers.tick();await timers.tick(1000);await Promise.all(requests);one.dispose();two.dispose();
});

test('a live owner renews its lease; after a crashed owner the next tab looks up the same key',async()=>{
 const store=new Store(),timers=new Timers();let finish!:()=>void,sends=0,lookups=0;
 const gate=new Promise<void>(r=>{finish=r;});
 const firstTransport={send:async()=>{sends++;await gate;return ok;},lookup:async(e:UserMutationEntry)=>receipt(e),recover:async()=>ok};
 const one=coordinator(store,timers,firstTransport);const pending=one.run(input),abandoned=assert.rejects(pending);await timers.tick();
 const key=store.state.entries[0].key,initialLease=store.state.entries[0].leaseUntilMs!;
 await timers.tick(10000);assert.ok(store.state.entries[0].leaseUntilMs!>initialLease);
 one.dispose();await abandoned; // Deliberately stalled transport ignores cancellation: uncertain lease race.
 const two=new UserMutationCoordinator({store,scope:()=>scope,ownerId:'successor-tab',now:()=>timers.now,timers:timers as any,transport:{send:async()=>{throw new Error('lookup must recover');},lookup:async(e)=>{lookups++;assert.equal(e.key,key);return receipt(e);},recover:async()=>ok}});
 await two.start();await timers.tick(29999);assert.equal(lookups,0);await timers.tick(1);assert.equal(lookups,1);assert.equal(store.state.entries[0].key,key);assert.equal(store.state.entries[0].state,'confirmed');
 finish();await timers.tick();assert.equal(store.state.entries[0].state,'confirmed');assert.equal(sends,1);two.dispose();
});

test('foreground retry exhaustion retains pending work for successful background reconciliation',async()=>{
 const store=new Store(),timers=new Timers();let sends=0,committed=false;
 const core=coordinator(store,timers,{send:async()=>{sends++;throw new Error('offline');},lookup:async e=>committed?receipt(e):{status:404,headers:{},body:{error:{code:'receipt_unknown'}}},recover:async()=>ok});
 const promise=core.run(input),pending=assert.rejects(promise,(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_pending');await timers.tick();
 for(let i=0;i<4;i++)await timers.tick(40000);await pending;assert.equal(sends,5);assert.equal(store.state.entries[0].state,'pending');const key=store.state.entries[0].key;
 committed=true;await timers.tick(40000);assert.equal(store.state.entries[0].state,'confirmed');assert.equal(store.state.entries[0].key,key);assert.equal(sends,5);core.dispose();
});

test('HTTP date Retry-After remains a dispatch floor across cancellable scheduler chunks',async()=>{
 const store=new Store(),timers=new Timers();let sends=0,lookups=0;const until=timers.now+180000;
 const core=coordinator(store,timers,{send:async()=>{sends++;return {status:503,headers:{'Retry-After':new Date(until).toUTCString()},body:{error:{code:'writer_capacity'}}};},lookup:async e=>{lookups++;return receipt(e);},recover:async()=>ok});
 const promise=core.run(input);await timers.tick();assert.ok(store.state.entries[0].dueMs>=until);
 await timers.tick(60000);await timers.tick(60000);await timers.tick(59999);assert.equal(sends,1);assert.equal(lookups,0);assert.equal(timers.jobs.size,1);
 await timers.tick(2000);await promise;assert.equal(sends,1);assert.equal(lookups,1);core.dispose();assert.equal(timers.jobs.size,0);
});

test('partial and expired results retain drafts; terminal current state stays separate',async()=>{
 for(const status of ['partial','receipt_expired']){
  const store=new Store(),timers=new Timers();const current={status:200,headers:{},body:{revision:9,current:['newer']}};
  const core=coordinator(store,timers,{send:async()=>status==='partial'?ok:{status:410,headers:{},body:{error:{code:status}}},lookup:async e=>receipt(e,'partial'),recover:async()=>current,current:async()=>current});
  const promise=core.run(input),review=assert.rejects(promise,(e:unknown)=>e instanceof UserMutationError&&e.currentState===current);await timers.tick();await review;
  assert.equal(store.state.entries[0].state,'review');assert.equal((store.state.entries[0].body as any).expectedRevision,1);assert.equal(JSON.stringify(store.state).includes('newer'),false);
  const before=copy(store.state.entries[0]);await assert.rejects(core.run(input));assert.deepEqual(store.state.entries[0],before);core.dispose();
 }
});

test('confirmation storage failure never resolves or drops the durable request',async()=>{
 const store=new Store(),timers=new Timers();let sent=0;
 const core=coordinator(store,timers,{send:async()=>{sent++;return ok;},lookup:async e=>{store.fail=true;return receipt(e);},recover:async()=>ok});
 const promise=core.run(input),refused=assert.rejects(promise,(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_store_unavailable');await timers.tick();await refused;
 assert.equal(sent,1);assert.equal(store.state.entries[0].state,'pending');assert.ok(store.state.entries[0].body);assert.equal(store.state.entries[0].receipt,undefined);core.dispose();
});

test('completed receipts are not silently evicted at capacity',async()=>{
 const store=new Store(),timers=new Timers();
 store.state.entries=Array.from({length:256},(_,i)=>{const e:UserMutationEntry={key:'p251_1800000000000_'+i.toString(16).padStart(32,'0'),operation:input.operation,method:input.method,path:input.path,target:input.target,intentId:'old-'+i,fingerprint:'',scope,state:'confirmed',attempts:1,dueMs:timers.now,headers:{}};e.receipt=receipt(e).body as any;return e;});
 const core=coordinator(store,timers,{send:async()=>{throw new Error('no dispatch');},lookup:async e=>receipt(e),recover:async()=>ok});
 await assert.rejects(core.run(input),(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_not_queued');assert.equal(store.state.entries.length,256);
 await core.acknowledge(store.state.entries[0].key);assert.equal(store.state.entries.length,255);core.dispose();
});

test('getRandomValues works when subtle and Web Locks are unavailable',()=>{
 const crypto=Object.getOwnPropertyDescriptor(globalThis,'crypto'),navigator=Object.getOwnPropertyDescriptor(globalThis,'navigator');let randomCalls=0;
 Object.defineProperty(globalThis,'crypto',{configurable:true,value:{getRandomValues(out:Uint8Array){randomCalls++;out.fill(5);return out;}}});
 Object.defineProperty(globalThis,'navigator',{configurable:true,get(){throw new Error('no Web Locks');}});
 try{assert.match(newUserMutationKey(1800000000000),/^p251_1800000000000_05050505050505050505050505050505$/);assert.equal(randomCalls,1);}finally{if(crypto)Object.defineProperty(globalThis,'crypto',crypto);else delete (globalThis as any).crypto;if(navigator)Object.defineProperty(globalThis,'navigator',navigator);else delete (globalThis as any).navigator;}
});

test('aggregate byte quota counts quarantines and rejects the new request without eviction or send',async()=>{
 const store=new Store(),timers=new Timers();store.state.entries=Array.from({length:139},(_,i)=>({key:'p251_1800000000000_'+i.toString(16).padStart(32,'0'),operation:input.operation,method:input.method,path:input.path,target:input.target,intentId:'old-'+i,fingerprint:'x'.repeat(59900),scope,state:'quarantined',attempts:1,dueMs:timers.now,headers:{}}));
 assert.ok(new TextEncoder().encode(JSON.stringify(store.state)).length<8*1024*1024);let sends=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return ok;},lookup:async e=>receipt(e),recover:async()=>ok});
 await assert.rejects(core.run({...input,body:{draft:'x'.repeat(30000)}}),(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_not_queued');assert.equal(sends,0);assert.equal(store.state.entries.length,139);core.dispose();
});

test('64KiB requests and secret credentials are refused before storage or dispatch',async()=>{
 const store=new Store(),timers=new Timers();let sends=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return ok;},lookup:async e=>receipt(e),recover:async()=>ok});
 for(const bad of [{...input,body:{draft:'x'.repeat(65536)}},{...input,headers:{Authorization:'Bearer test'}},{...input,body:{access_token:'do-not-store'}},{...input,body:{endpoint:'https://user:password@example.invalid'}}])await assert.rejects(core.run(bad),(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_not_queued');
 assert.equal(store.state.entries.length,0);assert.equal(sends,0);core.dispose();
});

test('a scope switch while send is in flight never performs lookup with the next family',async()=>{
 const store=new Store(),timers=new Timers();let current=scope,release!:()=>void,lookups=0;const gate=new Promise<void>(r=>{release=r;});
 const core=coordinator(store,timers,{send:async()=>{await gate;return ok;},lookup:async e=>{lookups++;return receipt(e);},recover:async()=>ok},()=>current);
 const pending=core.run(input),quarantined=assert.rejects(pending,(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_scope_changed');await timers.tick();current={...scope,familyId:'next'};release();await timers.tick();await quarantined;
 assert.equal(lookups,0);assert.equal(store.state.entries[0].state,'quarantined');current=scope;await core.reconcile();await timers.tick(60000);assert.equal(lookups,0);assert.equal(store.state.entries[0].state,'quarantined');core.dispose();
});

test('explicit reviewed release requires current scope and typed terminal proof',async()=>{
 const store=new Store(),timers=new Timers();let current=scope;
 const core=coordinator(store,timers,{send:async()=>({status:409,headers:{},body:{error:{code:'revision_mismatch'}}}),lookup:async e=>receipt(e),recover:async()=>ok},()=>current);
 const promise=core.run(input),review=assert.rejects(promise);await timers.tick();await review;const key=store.state.entries[0].key;
 assert.deepEqual(store.state.entries[0].terminalProof,{kind:'refused',httpStatus:409,code:'revision_mismatch'});
 current={...scope,familyId:'other'};await assert.rejects(core.dismissReviewed(key));assert.equal(store.state.entries.length,1);
 current=scope;await core.dismissReviewed(key);assert.equal(store.state.entries.length,0);core.dispose();
 for(const state of ['review','pending','quarantined'] as const){
  const entry:UserMutationEntry={key:'p251_1800000000000_00000000000000000000000000000001',operation:input.operation,method:input.method,path:input.path,target:input.target,intentId:'original',fingerprint:'{}',scope,state,attempts:1,dueMs:timers.now,headers:{},errorCode:'receipt_expired'};
  store.state.entries=[entry];await assert.rejects(core.dismissReviewed(entry.key));assert.equal(store.state.entries.length,1);
 }
});

test('confirmed fingerprint is erased and a consumed identity can only read current state',async()=>{
 const store=new Store(),timers=new Timers();let sends=0,reads=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return ok;},lookup:async e=>receipt(e),recover:async()=>{reads++;return ok;}});
 const first=core.run(input);await timers.tick();await first;assert.equal(store.state.entries[0].fingerprint,'');assert.equal(store.state.entries[0].body,undefined);
 await core.run({...input,body:{...input.body,values:{'reading.theme':'dark'}}});assert.equal(sends,1);assert.equal(reads,1);assert.equal(store.state.entries[0].fingerprint,'');core.dispose();
});

test('immediate HTTP refusals expose original response transiently, separately from current readback',async()=>{
 for(const status of [400,404,409,410,412,422]){
  const store=new Store(),timers=new Timers();const wire={status,headers:{'Retry-After':'120','X-Test':'original'},body:{error:{code:'wire_'+status},current:{revision:8,marker:'transient-wire-only'}}};
  const current={status:200,headers:{},body:{revision:9,marker:'transient-read-only'}};
  const core=coordinator(store,timers,{send:async()=>wire,lookup:async e=>receipt(e),recover:async()=>ok,current:async()=>current});
  const pending=core.run(input),refused=assert.rejects(pending,(error:unknown)=>{assert.ok(error instanceof UserMutationError);assert.equal(error.code,'wire_'+status);assert.equal(error.response,wire);assert.equal(error.currentState,current);assert.equal(error.terminalProof?.httpStatus,[400,409,412,422].includes(status)?status:undefined);return true;});
  await timers.tick();await refused;assert.equal(JSON.stringify(store.state).includes('transient-'),false);assert.equal('response' in store.state.entries[0],false);assert.equal('currentState' in store.state.entries[0],false);
  if(status===412){await core.dismissReviewed(store.state.entries[0].key);assert.equal(store.state.entries.length,0);}core.dispose();
 }
});

test('metadata camel and dotted credential names never enter durable custody',async()=>{
 const store=new Store(),timers=new Timers();let sends=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return ok;},lookup:async e=>receipt(e),recover:async()=>ok});
 for(const field of ['TMDBReadToken','TVDBProjectKey','openSubtitlesApiKey','openSubtitlesPassword','acoustidKey','metadata.TMDBReadToken','metadata.tvdb.projectKey','metadata.openSubtitles.api_key','metadata.acoustid.key']){
  await assert.rejects(core.run({...input,body:{settings:{[field]:'private-value'}}}),(e:unknown)=>e instanceof UserMutationError&&e.code==='mutation_not_queued');
 }
 assert.equal(store.state.entries.length,0);assert.equal(store.writes,0);assert.equal(sends,0);core.dispose();
});

test('Stop handover uses first frozen evidence only for the identical occurrence',async()=>{
 const store=new Store(),timers=new Timers();let sends=0,release!:()=>void;const gate=new Promise<void>(r=>{release=r;});
 const core=coordinator(store,timers,{send:async entry=>{sends++;assert.deepEqual(entry.body,{expectedGeneration:3,positionMs:100});await gate;return ok;},lookup:async e=>receipt(e),recover:async()=>ok});
 const stop={operation:'stop_playback_session',method:'POST',path:'/v1/playback/sessions/original/stop',target:'original:3',intentId:'stop:original:3',body:{expectedGeneration:3,positionMs:100}};
 const first=core.run(stop),handover=core.run({...stop,body:{expectedGeneration:3,positionMs:200}});await timers.tick();
 assert.equal(store.state.entries.length,1);assert.deepEqual(store.state.entries[0].body,stop.body);
 for(const changed of [{...stop,target:'original:4',body:{expectedGeneration:4,positionMs:200}},{...stop,body:{expectedGeneration:4,positionMs:200}},{...stop,path:'/v1/playback/sessions/successor/stop',target:'successor:3'},{...stop,operation:'other_command',body:{expectedGeneration:3,positionMs:200}}])await assert.rejects(core.run(changed),(e:unknown)=>e instanceof UserMutationError&&e.code==='idempotency_conflict');
 release();await timers.tick();await Promise.all([first,handover]);assert.equal(sends,1);core.dispose();
});

test('507 never becomes terminal proof or releases uncertain custody',async()=>{
 const store=new Store(),timers=new Timers();const core=coordinator(store,timers,{send:async()=>({status:507,headers:{},body:{error:{code:'unknown_storage'}}}),lookup:async()=>({status:404,headers:{},body:{error:{code:'receipt_unknown'}}}),recover:async()=>ok});
 const abort=new AbortController(),pending=core.run(input,abort.signal),cancelled=assert.rejects(pending,{name:'AbortError'});await timers.tick();abort.abort();await cancelled;
 assert.equal(store.state.entries[0].state,'pending');assert.equal(store.state.entries[0].terminalProof,undefined);await assert.rejects(core.dismissReviewed(store.state.entries[0].key));core.dispose();
});

test('one explicit native Stop consumer waits for foreground receipt consumption and then releases on the existing pump',async()=>{
 const store=new Store(),timers=new Timers();let consumed:Promise<boolean>|undefined,requested=false;
 const core=coordinator(store,timers,{send:async()=>ok,lookup:async entry=>receipt(entry),recover:async()=>ok});
 const off=core.subscribe(()=>{const entry=core.snapshot().find(entry=>entry.state==='confirmed');if(entry&&!requested){requested=true;consumed=core.consumeConfirmedStop(entry.key);}});
 const pending=core.run({operation:'stop_playback_session',method:'DELETE',path:'/v1/playback/sessions/s',target:'stop:s:1',intentId:'stop:s:1',body:{generation:1,positionMs:20}});
 await timers.tick();await pending;assert.equal(await consumed,false,'foreground waiters prevented early deletion');
 await timers.tick(1000);assert.equal(store.state.entries.length,0,'the original consumer request drains after the waiter without another API call');off();core.dispose();
});

function held<T=void>(){let resolve!:(value:T)=>void;const promise=new Promise<T>(done=>{resolve=done;});return {promise,resolve};}
for(const boundary of ['read','CAS'])test('disposed pump cannot dispatch or quarantine after held '+boundary+' during same-family rebuild',async()=>{
 const store=new Store(),timers=new Timers(),gate=held(),entered=held();let captured=false,live:UserMutationScope|undefined=scope,sends=0,applied=false;
 const read=store.read.bind(store),cas=store.compareAndSwap.bind(store);
 if(boundary==='read')store.read=async()=>{const state=await read();if(!captured&&state.entries[0]?.state==='pending'){captured=true;entered.resolve();await gate.promise;}return state;};
 else store.compareAndSwap=async(revision,next)=>{const accepted=await cas(revision,next);if(!captured&&accepted&&next.entries[0]?.attempts===1){captured=true;entered.resolve();await gate.promise;}return accepted;};
 const transport={send:async()=>{sends++;applied=true;return ok;},lookup:async(e:UserMutationEntry)=>applied?receipt(e):{status:404,headers:{},body:{error:{code:'receipt_unknown'}}},recover:async()=>ok};
 const old=coordinator(store,timers,transport,()=>live as UserMutationScope),pending=old.run(input);const retired=assert.rejects(pending,UserMutationError);
 await timers.tick();await entered.promise;const original=copy(store.state.entries[0]);old.dispose();live=undefined;gate.resolve();await retired;await timers.tick();
 assert.equal(sends,0);assert.equal(store.state.entries[0].state,'pending');assert.equal(store.state.entries[0].key,original.key);assert.deepEqual(store.state.entries[0].body,original.body);
 const fresh=coordinator(store,timers,transport);try{await fresh.start();await timers.tick(31000);assert.equal(sends,1);assert.equal(store.state.entries[0].state,'confirmed');assert.equal(store.state.entries[0].key,original.key);}finally{fresh.dispose();old.dispose();gate.resolve();}
});
test('native Stop consumption waits for both confirmed foreground recoveries',async()=>{
 const store=new Store(),timers=new Timers(),gates=[held<UserMutationResponse>(),held<UserMutationResponse>()];let recoveries=0;
 const core=coordinator(store,timers,{send:async()=>ok,lookup:async e=>receipt(e),recover:async()=>gates[recoveries++].promise});
 const stop={...input,operation:'stop_playback_session',method:'DELETE',path:'/v1/playback/sessions/original',target:'stop:original:1',intentId:'stop:original:1',body:{generation:1,positionMs:42000}};
 try{
  const initial=core.run(stop);await timers.tick();await initial;const key=store.state.entries[0].key;
  store.state.entries[0].nativeStopOwner='vega';store.state.entries[0].nativeStopConsumed=true;
  const first=core.run(stop),second=core.run(stop);for(let n=0;n<20&&recoveries<2;n++)await setImmediate();assert.equal(recoveries,2);
  assert.equal(await core.consumeConfirmedStop(key),false);assert.equal(store.state.entries.length,1);
  gates[0].resolve(ok);await first;await timers.tick();assert.equal(store.state.entries.length,1,'one foreground reader still owns the result');
  gates[1].resolve(ok);await second;await timers.tick();assert.equal(store.state.entries.length,0);
 }finally{gates.forEach(gate=>gate.resolve(ok));core.dispose();}
});
for(const status of ['applied','partial','receipt_unknown','receipt_expired'])test('targeted review Check again looks up only and retains unresolved '+status,async()=>{
 const store=new Store(),timers=new Timers();let sends=0,lookups=0;
 const core=coordinator(store,timers,{send:async()=>{sends++;return {status:400,headers:{},body:{error:{code:'needs_review'}}};},lookup:async e=>{lookups++;return status==='receipt_unknown'?{status:404,headers:{},body:{error:{code:status}}}:status==='receipt_expired'?{status:410,headers:{},body:{error:{code:status}}}:receipt(e,status);},recover:async()=>ok});
 try{
  const refused=assert.rejects(core.run(input),UserMutationError);await timers.tick();await refused;const entry=copy(store.state.entries[0]);let notifications=0;core.subscribe(()=>{notifications++;});
  await core.reconcile(entry.key);assert.equal(sends,1);assert.equal(lookups,1);assert.equal(store.state.entries[0].key,entry.key);
  if(status==='applied'){assert.equal(store.state.entries[0].state,'confirmed');assert.equal(store.state.entries[0].body,undefined);assert.equal(store.state.entries[0].fingerprint,'');assert.ok(notifications>0);}
  else if(status==='partial'){assert.equal(store.state.entries[0].state,'review');assert.deepEqual(store.state.entries[0].body,entry.body);assert.equal(store.state.entries[0].receipt?.status,'partial');}
  else assert.deepEqual(store.state.entries[0],entry);
 }finally{core.dispose();}
});
for(const race of ['entry','scope'])test('targeted receipt lookup cannot overwrite a changed '+race+' witness',async()=>{
 const store=new Store(),timers=new Timers(),gate=held<UserMutationResponse>();let entry!:UserMutationEntry,live=scope;
 const core=coordinator(store,timers,{send:async()=>({status:400,headers:{},body:{error:{code:'review'}}}),lookup:async e=>{entry=copy(e);return gate.promise;},recover:async()=>ok},()=>live);
 try{
  const refused=assert.rejects(core.run(input));await timers.tick();await refused;const key=store.state.entries[0].key;
  const check=core.reconcile(key);if(race==='scope')check.catch(()=>{});for(let n=0;n<20&&!entry;n++)await setImmediate();assert.ok(entry);
  if(race==='scope')live={...scope,familyId:'successor'};else store.state.entries[0].errorCode='review_changed';
  gate.resolve(receipt(entry));if(race==='scope')await assert.rejects(check,UserMutationError);else await check;
  assert.equal(store.state.entries[0].state,'review');assert.ok(store.state.entries[0].body);assert.equal(store.state.entries[0].receipt,undefined);
 }finally{gate.resolve(ok);core.dispose();}
});
