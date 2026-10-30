import test from 'node:test';
import assert from 'node:assert/strict';
import {IndexedDBUserMutationStore} from '../src/platform/user-mutations.ts';
import {emptyUserMutationState,UserMutationCoordinator} from '../../packages/client-core/src/user-mutations.ts';

/** A queued transactional IDB fixture: writes publish only at complete and an
 * abort restores the preceding record. Two independent adapters share it. */
function idbFixture(){
 const databases=new Map<string,any>();const modes:string[]=[];let deny=false,quota=false;
 const factory={open(name:string){const request:any={};queueMicrotask(()=>{
  if(deny){request.error=new Error('denied');request.onerror?.();return;}
  let database=databases.get(name),created=false;
  if(!database){created=true;const data=new Map<string,unknown>();let tail=Promise.resolve();
   database={createObjectStore(){},close(){},transaction(_name:string,mode:string,options?:{durability?:string}){
    modes.push(mode);const tx:any={durability:options?.durability??'default'},jobs:Array<()=>void>=[];let aborted=false,values:Map<string,unknown>;
    tx.abort=()=>{aborted=true;};
    tx.objectStore=()=>({get(key:string){const r:any={};jobs.push(()=>{r.result=structuredClone(values.get(key));r.onsuccess?.();});return r;},put(value:unknown,key:string){const captured=structuredClone(value);jobs.push(()=>{if(quota){aborted=true;tx.error=new DOMException('quota','QuotaExceededError');}else values.set(key,captured);});return{};}});
    tail=tail.then(async()=>{values=new Map([...data].map(([k,v])=>[k,structuredClone(v)]));await Promise.resolve();while(jobs.length&&!aborted){jobs.shift()!();await Promise.resolve();}if(aborted)tx.onabort?.();else{if(mode==='readwrite'){data.clear();for(const[k,v]of values)data.set(k,v);}tx.oncomplete?.();}});return tx;
   }};databases.set(name,database);
  }
  request.result=database;if(created)request.onupgradeneeded?.();request.onsuccess?.();
 });return request;}};
 return {factory:factory as unknown as IDBFactory,modes,deny(){deny=true;},quota(value=true){quota=value;}};
}

test('HTTP-LAN store compares and replaces atomically across independent tabs without crypto or Web Locks',async()=>{
 const fixture=idbFixture(),one=new IndexedDBUserMutationStore(fixture.factory),two=new IndexedDBUserMutationStore(fixture.factory);
 const crypto=Object.getOwnPropertyDescriptor(globalThis,'crypto'),navigator=Object.getOwnPropertyDescriptor(globalThis,'navigator');
 Object.defineProperty(globalThis,'crypto',{configurable:true,get(){throw new Error('crypto unavailable on this fixture');}});
 Object.defineProperty(globalThis,'navigator',{configurable:true,get(){throw new Error('Web Locks unavailable on HTTP');}});
 try{
  assert.deepEqual(await one.read(),emptyUserMutationState());
  const next={...emptyUserMutationState(),revision:1};
  const results=await Promise.all([one.compareAndSwap(0,next),two.compareAndSwap(0,next)]);
  assert.equal(results.filter(Boolean).length,1);assert.equal((await two.read()).revision,1);
  assert.equal(await one.compareAndSwap(0,next),false);assert.deepEqual(fixture.modes,['readonly','readwrite','readwrite','readonly','readwrite']);
 }finally{if(crypto)Object.defineProperty(globalThis,'crypto',crypto);else delete (globalThis as any).crypto;if(navigator)Object.defineProperty(globalThis,'navigator',navigator);else delete (globalThis as any).navigator;await one.close();await two.close();}
});
test('denied IndexedDB produces an explicit storage failure without pretending a draft is queued',async()=>{
 const fixture=idbFixture();fixture.deny();const store=new IndexedDBUserMutationStore(fixture.factory);
 await assert.rejects(store.read(),(e:any)=>e.code==='mutation_store_unavailable');
 await assert.rejects(store.compareAndSwap(0,{...emptyUserMutationState(),revision:1}),(e:any)=>e.code==='mutation_store_unavailable');assert.equal(fixture.modes.length,0);
});

test('a quota abort rolls back the aggregate and prevents transport dispatch',async()=>{
 const fixture=idbFixture();fixture.quota();const store=new IndexedDBUserMutationStore(fixture.factory);let sends=0;
 const coordinator=new UserMutationCoordinator({store,ownerId:'test',scope:()=>({serverId:'s',origin:'http://localhost:8096',authority:'local',accountId:'a',profileId:'p',familyId:'f'}),transport:{send:async()=>{sends++;throw new Error('no send');},lookup:async()=>{throw new Error('no lookup');},recover:async()=>{throw new Error('no recover');}}});
 try{
  await assert.rejects(coordinator.run({operation:'stop_playback_session',method:'POST',path:'/v1/playback/sessions/original/stop',target:'original:1',intentId:'stop:original:1',body:{expectedGeneration:1}}),(e:any)=>e.code==='mutation_store_unavailable');
  assert.equal(sends,0);assert.deepEqual(await store.read(),emptyUserMutationState());
  fixture.quota(false);assert.equal(await store.compareAndSwap(0,{...emptyUserMutationState(),revision:1}),true);assert.equal((await store.read()).revision,1);
 }finally{coordinator.dispose();await store.close();}
});

test('CAS captures its replacement before awaiting IndexedDB and survives reopen',async()=>{
 const fixture=idbFixture(),one=new IndexedDBUserMutationStore(fixture.factory);const next={...emptyUserMutationState(),revision:1};
 const writing=one.compareAndSwap(0,next);next.revision=99;assert.equal(await writing,true);await one.close();
 const two=new IndexedDBUserMutationStore(fixture.factory);assert.equal((await two.read()).revision,1);await two.close();
});
