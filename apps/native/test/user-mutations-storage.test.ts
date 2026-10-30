import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeUserMutationStore,type NativeUserMutationBridge} from '../src-next/platform/user-mutations.ts';
import type {UserMutationState,UserMutationEntry} from '../../../packages/client-core/src/user-mutations.ts';
const scope={serverId:'server',origin:'http://192.0.2.1:3240',authority:'local',accountId:'account',profileId:'profile',familyId:'family'};
const key=(n:number)=>`p251_1793200000000_${n.toString(16).padStart(32,'0')}`;
const entry=(n:number):UserMutationEntry=>({key:key(n),operation:'stop_playback_session',method:'DELETE',path:'/v1/playback/sessions/original',target:'stop:original:7',intentId:'stop:original:7',fingerprint:'original',scope,body:{generation:7,positionMs:1234},headers:{'Idempotency-Key':key(n)},state:'pending',attempts:0,dueMs:1793200000000});
const state=(revision:number,entries:UserMutationEntry[]=[]):UserMutationState=>({version:1,revision,entries});
test('native adapter sends one complete CAS to platform owner and exposes false',async()=>{
 const calls:{operation:string;payload:unknown}[]=[];
 const a=new NativeUserMutationStore({userMutationTransaction:async(operation,payload)=>{calls.push({operation,payload:JSON.parse(payload)});return operation==='read'?JSON.stringify(state(7)):false;}});
 assert.deepEqual(await a.read(),state(7));assert.equal(await a.compareAndSwap(7,state(8,[entry(1)])),false);
 assert.deepEqual(calls,[{operation:'read',payload:{}},{operation:'cas',payload:{expectedRevision:7,next:state(8,[entry(1)])}}]);
});
test('invalid revisions, secrets, count and byte limits fail before native commit',async()=>{
 let commits=0;const a=new NativeUserMutationStore({userMutationTransaction:async()=>{commits++;return true;}});
 await assert.rejects(a.compareAndSwap(3,state(5)));
 await assert.rejects(a.compareAndSwap(0,state(1,[{...entry(100),headers:{Authorization:'Bearer protected'}}])));
 await assert.rejects(a.compareAndSwap(0,state(1,Array.from({length:257},(_,i)=>entry(i+1000)))));
 await assert.rejects(a.compareAndSwap(0,state(1,Array.from({length:140},(_,i)=>({...entry(i+2000),body:{draft:'x'.repeat(64000)}})))));
 assert.equal(commits,0);
});
test('unavailable bridge fails explicitly without JS read/write fallback',async()=>{
 await assert.rejects(new NativeUserMutationStore({} as NativeUserMutationBridge).read(),{code:'mutation_store_unavailable'});
});
