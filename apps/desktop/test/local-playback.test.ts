import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DesktopDownloadStore,type NativeDownloadsBridge} from '../src/downloads.ts';
import {DesktopLocalPlaybackBroker} from '../src/local-playback.ts';
import type {LocalPlayerSource} from '../../../packages/client-core/src/playback-v1/local-port.ts';

const scope={authority:'local' as const,serverId:'server',accountId:'account',profileId:'profile',sessionFamilyId:'family'},lease='a'.repeat(64),entryId='b'.repeat(64),localSourceId='c'.repeat(64),preparationId='d'.repeat(64);
const member={id:entryId,revision:4,itemId:'item',localSourceId,durationMs:2000,title:{kind:'song',title:'Actual title',artist:'',parentId:'album',parentTitle:'Album',groupId:'disc',groupTitle:'',groupNumber:1,number:1,year:2026,durationMs:2000,onServer:true}};
const source:LocalPlayerSource={kind:'local',scope,preparationId,localSourceId,entryId,itemId:'item',generation:1,startPositionMs:0,autoplay:true};
async function setup(load:NonNullable<NativeDownloadsBridge['loadLocal']>){
 const retired:number[]=[],released:string[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>true,{async bind(){return lease},async release(){},async command(_,operation){if(operation.action==='prepareLocalQueue')return {kind:'localQueue',value:{preparationId,scope,selectionRevision:'4',items:[member]}};released.push(String(operation.preparationId));return {kind:'done'};},loadLocal:load,async retireLocal(_,generation){retired.push(generation)}});
 const prepared=await store.prepareLocalQueue({kind:'entry',entryId,expectedRevision:4},new AbortController().signal);
 const broker=new DesktopLocalPlaybackBroker();broker.retain(store,prepared);return {store,prepared,broker,retired,released};
}
const receipt=(generation:number)=>({kind:'local',scope,preparationId,localSourceId,generation,sessionId:`local:${entryId}:1`,playId:'e'.repeat(64),entryId,itemId:'item',artifact:{sha256:'f'.repeat(64),bytes:8}});
test('only the exact native preparation can map an acknowledged private load to public generation',async()=>{
 const h=await setup(async(_,request)=>receipt(request.generation));
 try{
  const ack=await h.broker.load(source,101,{signal:new AbortController().signal,isCurrent:()=>true});
  assert.equal(ack.generation,1);assert.equal(ack.playId,'e'.repeat(64));assert.equal(ack.artifact.bytes,8);assert.doesNotMatch(JSON.stringify(ack),/lease|path|url/);
  await assert.rejects(h.broker.load({...source,scope:{...scope,sessionFamilyId:'other'}},102,{signal:new AbortController().signal,isCurrent:()=>true}),/downloads_conflict/);
  await h.broker.release({...h.prepared});assert.deepEqual(h.released,[]);
  await h.broker.release(h.prepared);assert.deepEqual(h.retired,[101]);assert.deepEqual(h.released,[preparationId]);
 }finally{await h.broker.dispose();h.store.dispose();}
});
test('viewer/operation retirement after native commit retires that exact load before publishing any public receipt',async()=>{
 let complete!:(value:unknown)=>void,current=true;
 const h=await setup(()=>new Promise(resolve=>{complete=resolve}));
 try{
  const pending=h.broker.load(source,107,{signal:new AbortController().signal,isCurrent:()=>current});
  current=false;complete(receipt(107));await assert.rejects(pending,{name:'AbortError'});assert.deepEqual(h.retired,[107]);
 }finally{await h.broker.dispose();h.store.dispose();}
});

test('release retains failed load custody, still releases preparation, and retries without duplicate preparation release',async()=>{
 let fail=true;const retired:unknown[]=[],released:string[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>true,{async bind(){return lease},async release(){},
  async command(_,operation){if(operation.action==='prepareLocalQueue')return {kind:'localQueue',value:{preparationId,scope,selectionRevision:'4',items:[member]}};released.push(String(operation.preparationId));return{kind:'done'}},
  async loadLocal(_,request){return receipt(request.generation)},async retireLocal(value,generation){retired.push({value,generation});if(fail)throw new Error('failed')}});
 const broker=new DesktopLocalPlaybackBroker(),prepared=await store.prepareLocalQueue({kind:'entry',entryId,expectedRevision:4},new AbortController().signal);broker.retain(store,prepared);
 try{
  await broker.load(source,101,{signal:new AbortController().signal,isCurrent:()=>true});
  await assert.rejects(broker.release(prepared),/downloads_unavailable/);assert.deepEqual(released,[preparationId]);
  await assert.rejects(broker.load(source,102,{signal:new AbortController().signal,isCurrent:()=>true}),/downloads_conflict/);
  fail=false;await broker.dispose();await broker.dispose();
  assert.deepEqual(released,[preparationId]);assert.deepEqual(retired,[{value:lease,generation:101},{value:lease,generation:101}]);
 }finally{fail=false;await broker.dispose();store.dispose()}
});

test('prepared resource preserves native order and transfers lifetime beyond the initiating action',async()=>{
 const h=await setup(async(_,request)=>receipt(request.generation));await h.broker.release(h.prepared);h.released.length=0;
 try{
  const abort=new AbortController(),resource=await h.broker.prepare(h.store,{kind:'entry',entryId,expectedRevision:4},{signal:abort.signal,isCurrent:()=>true});
  assert.equal(Object.isFrozen(resource),true);assert.equal(resource.preparation.items[0].localSourceId,localSourceId);abort.abort();assert.deepEqual(h.released,[]);
  await h.broker.load(source,101,{signal:new AbortController().signal,isCurrent:()=>true});
  await Promise.all([resource.release(),resource.release()]);await resource.release();
  assert.deepEqual(h.released,[preparationId]);assert.deepEqual(h.retired,[101]);
 }finally{await h.broker.dispose();h.store.dispose()}
});

test('late preparation fence failure keeps cleanup retryable until native acknowledges release',async()=>{
 let current=true,fail=true,releases=0;
 const store=await DesktopDownloadStore.bind(scope,()=>true,{async bind(){return lease},async release(){},async command(_,operation){
  if(operation.action==='prepareLocalQueue'){current=false;return{kind:'localQueue',value:{preparationId,scope,selectionRevision:'4',items:[member]}};}
  releases++;if(fail)throw new Error('failed');return{kind:'done'};
 }}),broker=new DesktopLocalPlaybackBroker();
 try{
  await assert.rejects(broker.prepare(store,{kind:'entry',entryId,expectedRevision:4},{signal:new AbortController().signal,isCurrent:()=>current}),{name:'AbortError'});
  assert.equal(releases,1);fail=false;await broker.dispose();assert.equal(releases,2);
 }finally{fail=false;await broker.dispose();store.dispose()}
});

test('runtime release after captured store disposal recognizes the actual native preparation ACK',async()=>{
 const h=await setup(async(_,request)=>receipt(request.generation));
 try{
  await h.broker.load(source,101,{signal:new AbortController().signal,isCurrent:()=>true});
  h.store.dispose();await new Promise(resolve=>setImmediate(resolve));
  await h.broker.release(h.prepared);assert.deepEqual(h.released,[preparationId]);assert.deepEqual(h.retired,[101]);
 }finally{await h.broker.dispose();h.store.dispose()}
});
