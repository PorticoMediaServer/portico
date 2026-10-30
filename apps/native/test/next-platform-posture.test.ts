import test from 'node:test';
import assert from 'node:assert/strict';
import {createPlatformPostureStore} from '../src-next/platform/platformPostureStore';

const report = (revision:number,width=840,height=880) => ({revision,status:'available',window:{width,height},folds:[{orientation:'vertical',state:'half-opened',bounds:{left:415,top:0,right:425,bottom:height},isSeparating:true,occlusion:'full'}]});
const deferred = () => { let resolve!:(value:unknown)=>void; const promise=new Promise<unknown>(done=>{resolve=done;}); return {promise,resolve}; };

test('events win over an older initial read and equal facts retain snapshot identity', async () => {
  const initial=deferred(); let send!:(value:unknown)=>void, removed=0;
  const store=createPlatformPostureStore({getSnapshot:()=>initial.promise,subscribe:listener=>{send=listener;return()=>{removed++;};}});
  let notifications=0; const off=store.subscribe(()=>notifications++);
  assert.equal(store.getSnapshot().status,'pending');
  send(report(3)); const accepted=store.getSnapshot();
  initial.resolve(report(2,100,100)); await initial.promise;
  assert.equal(store.getSnapshot(),accepted);
  send(report(4)); assert.equal(store.getSnapshot(),accepted); assert.equal(notifications,1);
  assert.ok(Object.isFrozen(accepted)); assert.ok(Object.isFrozen(accepted.folds[0].bounds));
  off();off();assert.equal(removed,1);
});

test('last unsubscribe rejects old callbacks and reads across a new observation', async () => {
  const reads=[deferred(),deferred()], callbacks:((value:unknown)=>void)[]=[]; let index=0;
  const store=createPlatformPostureStore({getSnapshot:()=>reads[index++].promise,subscribe:listener=>{callbacks.push(listener);return()=>{};}});
  const listener=()=>{}; const a=store.subscribe(listener), b=store.subscribe(listener);
  assert.equal(callbacks.length,1);a();callbacks[0](report(9));assert.equal(store.getSnapshot().status,'available');b();
  const c=store.subscribe(listener);assert.equal(callbacks.length,2);assert.equal(store.getSnapshot().status,'pending');
  callbacks[0](report(100));reads[0].resolve(report(101));await reads[0].promise;
  assert.equal(store.getSnapshot().status,'pending');callbacks[1](report(1,880,840));assert.equal(store.getSnapshot().window?.width,880);c();
});

test('malformed geometry/status/revisions never replace current facts', () => {
  let send!:(value:unknown)=>void;
  const store=createPlatformPostureStore({getSnapshot:()=>new Promise(()=>{}),subscribe:listener=>{send=listener;return()=>{};}});
  const off=store.subscribe(()=>{});send(report(1));const current=store.getSnapshot();
  for(const bad of [
    {...report(2),revision:NaN},{...report(2),revision:-1},{...report(2),window:null},
    {...report(2),window:{width:0,height:880}},{...report(2),status:'pending'},
    {...report(2),folds:[{...report(2).folds[0],bounds:{left:425,top:0,right:415,bottom:880}}]},
    {...report(2),folds:[{...report(2).folds[0],state:'folded'}]},
  ]) {send(bad);assert.equal(store.getSnapshot(),current);}
  off();
});

test('a missing bridge is unavailable; read failure is honest and an OS event can recover', async () => {
  assert.deepEqual(createPlatformPostureStore(null).getSnapshot(),{status:'unavailable',window:null,folds:[]});
  let send!:(value:unknown)=>void;
  const store=createPlatformPostureStore({getSnapshot:async()=>{throw new Error('native');},subscribe:listener=>{send=listener;return()=>{};}});
  const off=store.subscribe(()=>{});await Promise.resolve();assert.equal(store.getSnapshot().status,'unavailable');
  send(report(1));assert.equal(store.getSnapshot().status,'available');send({revision:2,status:'unavailable',window:null,folds:[]});assert.equal(store.getSnapshot().window,null);off();
});
