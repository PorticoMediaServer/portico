import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDesktopDownloadsHost} from '../src/downloads-host.ts';
import {DesktopNetworkMonitor} from '../src/network.ts';
import type {NativeDownloadsBridge} from '../src/downloads.ts';
import type {SavedDownloadsBinderInput} from '../../../web/src/screens/saved/native-binding.ts';
import type {WebLocalQueueController} from '../../../web/src/platform/playback/types.ts';
const scope={authority:'local' as const,serverId:'server',accountId:'account',profileId:'profile',sessionFamilyId:'family'},lease='a'.repeat(64),entryId='b'.repeat(64),preparationId='c'.repeat(64),groupRevision='d'.repeat(64);
const title={kind:'song',title:'Native title',artist:'Artist',parentId:'album',parentTitle:'Album',groupId:'disc',groupTitle:'',groupNumber:1,number:1,year:2026,durationMs:2000,onServer:false};
const first={id:entryId,revision:4,itemId:'first',title,durationMs:2000,localSourceId:'e'.repeat(64)};
const second={...first,id:'f'.repeat(64),itemId:'second',localSourceId:'1'.repeat(64),title:{...title,number:2}};
async function setup(){
 const operations:Readonly<Record<string,unknown>>[]=[],binds:unknown[]=[],releases:string[]=[],errors:Error[]=[];
 const row={...first,preparationId:'server-prep',quality:'original',sha256:'2'.repeat(64),bytes:8,bytesDone:8,state:'ready',held:false,addedMs:1,receiptExpiresMs:null,offlineAccess:'valid'};
 const bridge:NativeDownloadsBridge={async bind(value){binds.push(value);return lease},async release(value){releases.push(value)},async command(_,op){
  operations.push(op);switch(op.action){
   case 'rules':return{kind:'rules',value:{revision:0,rules:{removeWatched:false,nextEpisode:false,unmeteredOnly:true,limitBytes:0}}};
   case 'list':return{kind:'page',value:{items:[row],nextCursor:null}};
   case 'groups':return{kind:'groups',value:[{kind:'songs',parentId:'album',groupId:'disc',revision:groupRevision,count:2,bytes:16,bytesDone:16,state:'ready',held:false,offlineAccess:'valid',parentTitle:'Album'}]};
   case 'capacity':return{kind:'capacity',value:{totalBytes:100,freeBytes:84,usedBytes:16,otherDownloadsBytes:0}};
   case 'prepareLocalQueue':return{kind:'localQueue',value:{preparationId,scope,selectionRevision:String((op.selection as {expectedRevision:unknown}).expectedRevision),items:(op.selection as {kind:string}).kind==='group'?[second,first]:[first]}};
   default:return{kind:'done'};
  }
 },folder:{async location(){return{displayPath:'/owned/Portico'}},async begin(){return'3'.repeat(64)},async choose(){},async open(){},async cancel(){}}};
 const host=createDesktopDownloadsHost({bridge,reportFailure:error=>errors.push(error),network:()=>new DesktopNetworkMonitor({async read(){return'unknown'}},()=>0,{set(){return 0},clear(){}})});
 const signal=new AbortController();let current=true;
 const input={runtime:{scope,downloads:{}},api:{baseUrl:'https://server.test',getRouteConnection(){return undefined},mediaUrl(path:string){return new URL(path,'https://server.test').href},routeFetch(){throw new Error('No transfer is admitted on an unknown route')}},isCurrent:()=>current,signal:signal.signal} as unknown as SavedDownloadsBinderInput;
 return{host,input,signal,operations,binds,releases,errors,retire(){current=false;signal.abort()}};
}
test('Saved waits for a genuine same-five-field mounted native runtime; abort retires the waiter without binding files',async()=>{
 const h=await setup();let ready=false;
 const controller:WebLocalQueueController={canPlayLocalQueue:()=>true,async playLocalQueue(){assert.fail('foreign runtime')},subscribe(){return()=>{}}};
 const detach=h.host.attachLocalQueue({...scope,sessionFamilyId:'other'},controller);
 try{
  const pending=h.host.bindSaved(h.input).then(value=>{ready=true;return value});await new Promise(resolve=>setImmediate(resolve));
  assert.equal(ready,false);assert.deepEqual(h.binds,[]);h.retire();await assert.rejects(pending,{name:'AbortError'});assert.deepEqual(h.binds,[]);
 }finally{detach();await h.host.dispose()}
});
test('Saved native group Play hands every ordered opaque member to the actual attached controller and preserves complete scope',async()=>{
 const h=await setup(),events=new Set<()=>void>();let mounted=false,offCalls=0;
 const played:string[][]=[];
 const controller:WebLocalQueueController={canPlayLocalQueue:()=>mounted,subscribe(listener){events.add(listener);return()=>{offCalls++;events.delete(listener)}},async playLocalQueue(resource,context){
  assert.equal(context.isCurrent(),true);assert.deepEqual(resource.preparation.scope,scope);
  played.push(resource.preparation.items.map(item=>item.itemId));assert.doesNotMatch(JSON.stringify(resource.preparation),/https?:|lease|path|url/);
  await resource.release();
 }};
 const detach=h.host.attachLocalQueue(scope,controller);
 try{
  const pending=h.host.bindSaved(h.input);await new Promise(resolve=>setImmediate(resolve));assert.deepEqual(h.binds,[]);
  mounted=true;for(const event of events)event();const binding=await pending;
  assert.deepEqual(h.binds,[scope]);await binding.localDownloads.refresh(new AbortController().signal);
  await binding.localDownloads.playGroup!({kind:'songs',parentId:'album',groupId:'disc'},groupRevision,new AbortController().signal);
  assert.deepEqual(played,[['second','first']]);
  assert.deepEqual(h.operations.filter(op=>op.action==='prepareLocalQueue'),[{action:'prepareLocalQueue',selection:{kind:'group',selector:{kind:'songs',parentId:'album',groupId:'disc'},expectedRevision:groupRevision}}]);
  binding.dispose();binding.dispose();await new Promise(resolve=>setImmediate(resolve));assert.deepEqual(h.releases,[lease]);assert.equal(binding.localDownloads.getSnapshot().available,false);
 }finally{detach();await h.host.dispose();assert.equal(offCalls,1)}
});
test('stale runtime detach cannot remove its replacement or gain authority over the captured Saved binding',async()=>{
 const h=await setup();let firstOff=0,secondOff=0;
 const old=h.host.attachLocalQueue(scope,{canPlayLocalQueue:()=>true,async playLocalQueue(){assert.fail('retired runtime')},subscribe(){return()=>{firstOff++}}});
 let calls=0;
 const next=h.host.attachLocalQueue(scope,{canPlayLocalQueue:()=>true,async playLocalQueue(resource){calls++;await resource.release()},subscribe(){return()=>{secondOff++}}});old();
 try{
  const binding=await h.host.bindSaved(h.input);await binding.localDownloads.refresh(new AbortController().signal);
  await binding.localDownloads.play(entryId,new AbortController().signal);assert.equal(calls,1);assert.equal(firstOff,1);
  h.retire();assert.equal(binding.localDownloads.getSnapshot().available,false);
 }finally{next();await h.host.dispose();assert.equal(secondOff,1)}
});
