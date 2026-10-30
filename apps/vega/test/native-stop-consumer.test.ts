import test from 'node:test';
import assert from 'node:assert/strict';
import {UserMutationCoordinator,type UserMutationEntry,type UserMutationState,type UserMutationTransport,userMutationFingerprint} from '../../../packages/client-core/src/user-mutations.ts';
import {NativeStopResultConsumer} from '../../native/src-next/platform/native-stop-consumer.ts';
import {userMutationTransaction} from '../src/bridge/user-mutations.ts';
const scope={serverId:'server',origin:'http://192.0.2.1',authority:'local',accountId:'account',profileId:'profile',familyId:'family'};
const key=(n:number)=>`p251_1793200000000_${n.toString(16).padStart(32,'0')}`;
const confirmed=(n:number):UserMutationEntry=>({key:key(n),operation:'stop_playback_session',method:'DELETE',path:`/v1/playback/sessions/session-${n}`,target:`stop:session-${n}:1`,intentId:`stop:session-${n}:1`,fingerprint:'',scope,headers:{'Idempotency-Key':key(n)},state:'confirmed',receipt:{key:key(n),operation:'stop_playback_session',status:'applied',committedMs:1793200000000,expiresMs:1795792000000},nativeStopOwner:`native-${n}`,attempts:1,dueMs:1793200000000});
const store={read:async():Promise<UserMutationState>=>JSON.parse(await userMutationTransaction('read','{}') as string),compareAndSwap:async(expectedRevision:number,next:UserMutationState)=>await userMutationTransaction('cas',JSON.stringify({expectedRevision,next}))===true};
async function change(work:(state:UserMutationState)=>void){for(let i=0;i<32;i++){const state=await store.read(),revision=state.revision;work(state);state.revision++;if(await store.compareAndSwap(revision,state))return;}throw new Error('fixture store busy');}
const unused=async()=>{throw new Error('No unreceipted mutation may be sent by this consumer test.');};
const transport:UserMutationTransport={send:unused,lookup:unused,recover:async()=>({status:204,headers:{}})};
function consumer(coordinator:UserMutationCoordinator,errors:unknown[],locallyRetired:Set<string>,calls:string[]){return new NativeStopResultConsumer({coordinator,scope:()=>scope,report:error=>errors.push(error),consumeResult:key=>coordinator.consumeConfirmedStop(key),consumeLocal:async key=>{
 calls.push(key);if(!locallyRetired.has(key))return false;
 await change(state=>{const entry=state.entries.find(entry=>entry.key===key);if(entry)entry.nativeStopConsumed=true;});return true;
}});}
test('300 known locally consumed native Stops release capacity; unknown/review/generic entries remain',async()=>{
 const unknown={...confirmed(9000),state:'pending' as const,fingerprint:'original',body:{generation:1,positionMs:2},dueMs:Number.MAX_SAFE_INTEGER,nativeStopConsumed:true};delete unknown.receipt;
 const review={...confirmed(9001),state:'review' as const,fingerprint:'original',body:{generation:1,positionMs:2},receipt:{...confirmed(9001).receipt!,status:'partial' as const}};
 const generic={...confirmed(9002),operation:'patch_preferences',receipt:{...confirmed(9002).receipt!,operation:'patch_preferences'},nativeStopOwner:undefined};
 await change(state=>{state.entries.push(unknown,review,generic);});
 const coordinator=new UserMutationCoordinator({store,scope:()=>scope,transport,ownerId:'native-consumer-test'}),errors:unknown[]=[],calls:string[]=[],retired=new Set<string>();await coordinator.start();
 let current=consumer(coordinator,errors,retired,calls);
 try{
  for(let n=1;n<=300;n++){
   retired.add(key(n));await change(state=>{state.entries.push(confirmed(n));});
   if(n===150){current.dispose();current=consumer(coordinator,errors,retired,calls);} // JS consumer restart/handover.
   await coordinator.reconcile();current.refresh();await current.settled();
   assert.equal((await store.read()).entries.some(entry=>entry.key===key(n)),false);
  }
  assert.deepEqual((await store.read()).entries.map(e=>e.key).sort(),[unknown.key,review.key,generic.key].sort());
  assert.equal(calls.length,300);assert.deepEqual(errors,[]);
 }finally{current.dispose();coordinator.dispose();}
});
test('native receipt consumer waits for a shared JS waiter recovery before removing its record',async()=>{
 const seed=confirmed(9900),body={generation:1,positionMs:4000};const input={operation:seed.operation,method:seed.method,path:seed.path,target:seed.target,intentId:seed.intentId,key:seed.key,body};
 const pending:UserMutationEntry={...seed,state:'pending',body,fingerprint:userMutationFingerprint(input),leaseOwner:'native-owner',leaseUntilMs:Date.now()+60000};delete pending.receipt;
 await change(state=>{state.entries.push(pending);});
 let release!:(response:{status:number;headers:Record<string,string>})=>void;let recovering=false;
 const held=new Promise<{status:number;headers:Record<string,string>}>(resolve=>{release=resolve;});
 const coordinator=new UserMutationCoordinator({store,scope:()=>scope,ownerId:'native-waiter-test',transport:{...transport,recover:async()=>{recovering=true;return held;}}});await coordinator.start();
 const waiting=coordinator.run(input); // Join the native owner's original pending Stop.
 // Let admission finish before the native owner publishes its committed receipt.
 for(let n=0;n<20;n++)await Promise.resolve();
 await change(state=>{const entry=state.entries.find(e=>e.key===seed.key)!;Object.assign(entry,seed);delete entry.body;delete entry.leaseOwner;delete entry.leaseUntilMs;});
 const errors:unknown[]=[],calls:string[]=[],current=consumer(coordinator,errors,new Set([seed.key]),calls);
 try{
  await coordinator.reconcile();current.refresh();
  for(let n=0;n<80;n++)await Promise.resolve();
  assert.equal(recovering,true);assert.ok((await store.read()).entries.some(entry=>entry.key===seed.key));
  release({status:204,headers:{}});assert.equal((await waiting).status,204);await current.settled();await coordinator.reconcile();
  assert.equal((await store.read()).entries.some(entry=>entry.key===seed.key),false);assert.deepEqual(errors,[]);
 }finally{release({status:204,headers:{}});current.dispose();coordinator.dispose();}
});
