import test from 'node:test';
import assert from 'node:assert/strict';
import {getEventListeners} from 'node:events';
import {ArtworkScheduler} from '../src/artwork/scheduler.ts';

const listeners=(signal:AbortSignal)=>getEventListeners(signal,'abort').length;
const tick=()=>new Promise<void>(resolve=>setImmediate(resolve));

test('a reused caller signal retains no settled artwork callbacks',async()=>{
 const caller=new AbortController(),scheduler=new ArtworkScheduler<string>({load:async key=>key});
 try{
  for(let n=0;n<32;n++){
   assert.equal(await scheduler.load(String(n),{signal:caller.signal}),String(n));
   assert.equal(listeners(caller.signal),0);
  }
 }finally{scheduler.dispose();}
});

test('terminal artwork failure removes caller interest while retry keeps it',async()=>{
 const caller=new AbortController(),timers=new Map<object,{fn:()=>void;ms:number}>();
 let attempts=0;
 const failure=new Error('final');
 const scheduler=new ArtworkScheduler<string>({
  load:async()=>{attempts++;throw failure;},backoffMs:[7],classify:()=>({retryable:true}),
  setTimer:(fn,ms)=>{const token={};timers.set(token,{fn,ms});return token;},clearTimer:token=>{timers.delete(token as object);},
 });
 try{
  const waiting=scheduler.load('poster',{signal:caller.signal}),rejected=assert.rejects(waiting,error=>error===failure);
  await tick();assert.equal(attempts,1);assert.equal(listeners(caller.signal),1);
  const retry=[...timers].find(([,timer])=>timer.ms===7);assert.ok(retry);timers.delete(retry[0]);retry[1].fn();
  await rejected;assert.equal(attempts,2);assert.equal(listeners(caller.signal),0);
 }finally{scheduler.dispose();}
});

test('caller abort and scheduler disposal remove every affected waiter listener',async()=>{
 const caller=new AbortController(),other=new AbortController();
 const scheduler=new ArtworkScheduler<string>({load:()=>new Promise(()=>{}),setTimer:()=>({}),clearTimer:()=>{}});
 const first=scheduler.load('poster',{signal:caller.signal}),second=scheduler.load('poster',{signal:other.signal});
 const aborted=assert.rejects(first,{name:'AbortError'}),disposed=assert.rejects(second,{name:'AbortError'});
 assert.equal(listeners(caller.signal),1);assert.equal(listeners(other.signal),1);
 caller.abort();await aborted;assert.equal(listeners(caller.signal),0);assert.equal(listeners(other.signal),1);
 scheduler.dispose();await disposed;assert.equal(listeners(other.signal),0);
});
