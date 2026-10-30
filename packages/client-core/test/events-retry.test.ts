import {test} from 'node:test';
import assert from 'node:assert/strict';
import {EventsClient, type RawStreamEvent} from '../src/playback-v1/events.ts';
import type {V1Response} from '../src/playback-v1/http.ts';

const settle=async()=>{for(let i=0;i<12;i++)await Promise.resolve();};
function timers(){
 const pending=new Map<number,{fn:()=>void;ms:number}>();let id=0;
 return {pending,setTimer:(fn:()=>void,ms:number)=>{pending.set(++id,{fn,ms});return id;},clearTimer:(timer:unknown)=>{pending.delete(timer as number);},fire:()=>{const [id,t]=[...pending][0];pending.delete(id);t.fn();}};
}
const held=()=>new Promise<V1Response>(()=>{});

test('event poll honours a 60-second Retry-After minimum and cancelling its sleep stops requests',async()=>{
 const c=timers();let calls=0;
 const events=new EventsClient({http:{send:async()=>{calls++;return {status:429,headers:{'Retry-After':'60'}};}},random:()=>0,setTimer:c.setTimer,clearTimer:c.clearTimer});
 events.start();await settle();
 assert.equal(calls,1);assert.equal([...c.pending.values()][0].ms,60_000);
 events.stop();await settle();assert.equal(c.pending.size,0);assert.equal(calls,1);
});

test('event retries jitter their backoff deterministically without shortening a server minimum',async()=>{
 for(const [random,delay] of [[0,750],[1,1250]]){
  const c=timers();const events=new EventsClient({http:{send:async()=>{throw new Error('offline');}},random:()=>random,setTimer:c.setTimer,clearTimer:c.clearTimer});
  events.start();await settle();assert.equal([...c.pending.values()][0].ms,delay);events.stop();
 }
});

test('quiet clean SSE closures back off before falling back to long poll, retaining the long-poll wait',async()=>{
 const c=timers();let streams=0,polls=0;
 const events=new EventsClient({http:{send:r=>{polls++;assert.match(r.path,/waitMs=25000/);return held();}},openStream:async()=>{streams++;},waitMs:55000,random:()=>0.5,setTimer:c.setTimer,clearTimer:c.clearTimer});
 events.start();await settle();assert.equal(streams,1);assert.equal(polls,0);
 assert.equal([...c.pending.values()][0].ms,1000);
 c.fire();await settle();assert.equal(streams,2);assert.equal([...c.pending.values()][0].ms,2000);
 c.fire();await settle();assert.equal(streams,3);assert.equal(polls,1);assert.equal(events.getStatus().mode,'poll');events.stop();
});

test('SSE greetings, control and invalid frames do not forgive repeated stream failures',async()=>{
 for(const frame of [{id:'hello',type:'stream.opened'},{id:'resync',type:'stream.resync'},null]){
  const c=timers();let streams=0,polls=0;
  const events=new EventsClient({http:{send:()=>{polls++;return held();}},openStream:async(_cursor,event)=>{streams++;event({data:frame?JSON.stringify(frame):'invalid json'});},random:()=>0.5,setTimer:c.setTimer,clearTimer:c.clearTimer});
  try{
   events.start();await settle();assert.equal(streams,1);
   c.fire();await settle();assert.equal(streams,2);
   c.fire();await settle();assert.equal(streams,3);assert.equal(polls,1);assert.equal(events.getStatus().mode,'poll');
  }finally{events.stop();}
 }
});

test('a newly delivered application event forgives preceding short stream failures',async()=>{
 const c=timers();let streams=0,polls=0;
 const events=new EventsClient({http:{send:()=>{polls++;return held();}},openStream:async(_cursor,event)=>{if(++streams===2)event({data:JSON.stringify({id:'one',type:'session.updated'})});},random:()=>0.5,setTimer:c.setTimer,clearTimer:c.clearTimer});
 try{
  events.start();await settle();c.fire();await settle();c.fire();await settle();
  assert.equal(streams,3);assert.equal(polls,0);
  c.fire();await settle();assert.equal(streams,4);assert.equal(polls,1);
 }finally{events.stop();}
});

test('stream errors retain Retry-After even when the next request switches to long poll',async()=>{
 const c=timers();let polls=0;
 const events=new EventsClient({http:{send:()=>{polls++;return held();}},openStream:async()=>{throw {status:503,retryAfterMs:60_000};},streamFailuresBeforePoll:1,random:()=>0.5,setTimer:c.setTimer,clearTimer:c.clearTimer});
 // The server's minute is a floor; each client waits a little longer, at random, so they do not all come back together.
 events.start();await settle();assert.equal(polls,0);assert.equal([...c.pending.values()][0].ms,67_500);
 c.fire();await settle();assert.equal(polls,1);events.stop();
});

test('a late poll response from a stopped run cannot publish events or cursors into a restarted run',async()=>{
 let resolve!:(r:V1Response)=>void;let calls=0;
 const events=new EventsClient({http:{send:()=>++calls===1?new Promise(r=>{resolve=r;}):held()}});
 let delivered=0;events.on('session.updated',()=>delivered++);
 events.start();await settle();events.stop();events.start();await settle();
 resolve({status:200,headers:{},body:{events:[{id:'old',type:'session.updated'}],nextAfter:'old'}});await settle();
 assert.equal(delivered,0);assert.equal(events.getStatus().cursor,undefined);assert.equal(events.getStatus().connected,false);assert.equal(events.getStatus().mode,'poll');events.stop();
});

test('a retired SSE callback and cleanup cannot overwrite the restarted connection status',async()=>{
 const runs:{event:(raw:RawStreamEvent)=>void;resolve:()=>void}[]=[];
 const events=new EventsClient({http:{send:held},openStream:(_cursor,event)=>new Promise(resolve=>{runs.push({event,resolve});})});
 const emit=(run:number,id:string)=>runs[run].event({data:JSON.stringify({id,type:'session.updated'})});
 const seen:string[]=[];events.on('session.updated',e=>seen.push(e.id));
 events.start();events.stop();events.start();emit(1,'new');emit(0,'old');runs[0].resolve();await settle();
 assert.deepEqual(seen,['new']);assert.equal(events.getStatus().cursor,'new');assert.equal(events.getStatus().connected,true);events.stop();runs[1].resolve();
});

test('a zero-delay stream.closed envelope cannot cause an immediate reconnect loop',async()=>{
 const c=timers();let streams=0;
 const events=new EventsClient({http:{send:held},openStream:async(_cursor,event)=>{streams++;event({data:JSON.stringify({id:'close',type:'stream.closed',data:{reconnectAfterMs:0}})});},random:()=>0,setTimer:c.setTimer,clearTimer:c.clearTimer});
 events.start();await settle();assert.equal(streams,1);assert.equal([...c.pending.values()][0].ms,1000);events.stop();
});

test('event retry sleep chunks long Retry-After values without overflowing a timer or retrying early',async()=>{
 const c=timers();let calls=0;
 const events=new EventsClient({http:{send:async()=>{calls++;return {status:429,headers:{'Retry-After':'3000000'}};}},random:()=>0,setTimer:c.setTimer,clearTimer:c.clearTimer});
 events.start();await settle();assert.equal([...c.pending.values()][0].ms,2_147_483_647);
 c.fire();await settle();assert.equal(calls,1);assert.equal([...c.pending.values()][0].ms,3_000_000_000-2_147_483_647);events.stop();await settle();assert.equal(c.pending.size,0);
});

test('a wait the server asked for is spread between clients and never shortened',async()=>{
 for(const [random,delay] of [[0,60_000],[1,75_000]]){
  const c=timers();const events=new EventsClient({http:{send:async()=>({status:429,headers:{'Retry-After':'60'}})},random:()=>random,setTimer:c.setTimer,clearTimer:c.clearTimer});
  events.start();await settle();assert.equal([...c.pending.values()][0].ms,delay);events.stop();
 }
 for(const [random,delay] of [[0,5000],[1,6250]]){
  const c=timers();const events=new EventsClient({http:{send:held},openStream:async(_cursor,event)=>{event({data:JSON.stringify({id:'close',type:'stream.closed',data:{reconnectAfterMs:5000}})});},random:()=>random,setTimer:c.setTimer,clearTimer:c.clearTimer});
  events.start();await settle();assert.equal([...c.pending.values()][0].ms,delay);events.stop();
 }
});

const waitLimit=()=>({status:429,headers:{'Retry-After':'2'},body:{error:{code:'event_wait_limit',message:'This device already has its event waits open.',retryable:true}}});
const page=(more=false)=>({status:200,body:{events:[],nextAfter:'e1.next',more}});

test('a refused parked poll keeps the cursor and reads without waiting, paced, until it may park again',async()=>{
 const c=timers();let clock=0;const paths:string[]=[];
 const replies=[waitLimit(),page(),page()];
 const events=new EventsClient({http:{send:r=>{paths.push(r.path);const next=replies.shift();return next?Promise.resolve(next as V1Response):held();}},cursor:'e1.kept',random:()=>0,now:()=>clock,setTimer:c.setTimer,clearTimer:c.clearTimer});
 try{
  events.start();await settle();
  assert.match(paths[0],/waitMs=25000&after=e1\.kept$/);
  assert.equal([...c.pending.values()][0].ms,2000);assert.equal(events.getStatus().failures,0);
  c.fire();await settle();
  assert.match(paths[1],/waitMs=0&after=e1\.kept$/,'the cursor survives the refusal');
  assert.equal([...c.pending.values()][0].ms,2000,'reads that do not park are paced');
  clock=29_999;c.fire();await settle();
  assert.match(paths[2],/waitMs=0&/);
  clock=30_000;c.fire();await settle();
  assert.match(paths[3],/waitMs=25000&after=e1\.next$/,'after 30 seconds it parks again');
  assert.equal(paths.length,4);assert.equal(events.getStatus().mode,'poll');
 }finally{events.stop();}
});

test('a page that stopped at the server budget is continued a second later, not at once',async()=>{
 const c=timers();let calls=0;
 const events=new EventsClient({http:{send:async()=>{calls++;return (calls===1?page(true):held()) as V1Response;}},random:()=>0,setTimer:c.setTimer,clearTimer:c.clearTimer});
 try{
  events.start();await settle();
  assert.equal(calls,1);assert.equal([...c.pending.values()][0].ms,1000);
  c.fire();await settle();assert.equal(calls,2);
 }finally{events.stop();}
});

test('a generic 429 is still a failure with its own Retry-After, not the parked-poll fallback',async()=>{
 const c=timers();const paths:string[]=[];
 const events=new EventsClient({http:{send:async r=>{paths.push(r.path);return {status:429,headers:{'Retry-After':'60'},body:{error:{code:'rate_limited',message:'',retryable:true}}};}},random:()=>0,setTimer:c.setTimer,clearTimer:c.clearTimer});
 try{
  events.start();await settle();assert.equal([...c.pending.values()][0].ms,60_000);assert.equal(events.getStatus().failures,1);
  c.fire();await settle();assert.match(paths[1],/waitMs=25000/);
 }finally{events.stop();}
});

test('every resync subscriber is told before the cursor moves past the gap, even if one throws',async()=>{
 const events=new EventsClient({http:{send:held},cursor:'e1.before'});
 const seen:string[]=[];
 events.onResync(()=>{seen.push(String(events.getStatus().cursor));throw new Error('broken subscriber');});
 events.onResync(()=>{seen.push(String(events.getStatus().cursor));});
 events.deliver({id:'e1.after',type:'stream.resync'});
 assert.deepEqual(seen,['e1.before','e1.before']);
 assert.equal(events.getStatus().cursor,'e1.after');
});
