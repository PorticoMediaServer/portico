import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DesktopDownloadStore,type NativeDownloadsBridge} from '../src/downloads.ts';
import {createDesktopSavedDownloadsPort,type DesktopSavedPlayback} from '../src/saved-downloads.ts';

const scope={authority:'local' as const,serverId:'server',accountId:'account',profileId:'profile',sessionFamilyId:'family'};
const id='b'.repeat(64),groupRevision='d'.repeat(64);
const row={id,revision:4,preparationId:'prep',itemId:'item',quality:'original',sha256:'c'.repeat(64),bytes:8,bytesDone:2,
 state:'ready',held:false,addedMs:1,receiptExpiresMs:1234,offlineAccess:'valid',
 title:{kind:'song',title:'Stored song',artist:'Track artist',parentId:'album',parentTitle:'Stored album',groupId:'disc',groupTitle:'',groupNumber:1,number:2,year:1977,durationMs:181000,onServer:false}};
const group={kind:'songs',parentId:'album',groupId:'disc',count:9,bytes:72,bytesDone:64,revision:groupRevision,state:'ready',held:false,offlineAccess:'valid'};
const flat={removeWatched:false,nextEpisode:false,unmeteredOnly:true,limitBytes:0};
async function harness(playback:DesktopSavedPlayback={async play(){}}){
 let current=true,revision=7,values=flat,failRead=false;
 let custom:NativeDownloadsBridge['command']|undefined;
 const calls:Readonly<Record<string,unknown>>[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>current,{async bind(){return 'a'.repeat(64)},async release(){},async command(lease,operation){
  calls.push(operation);if(custom)return custom(lease,operation);
  switch(operation.action){
   case 'list':return {kind:'page',value:{items:[row],nextCursor:null}};
   case 'groups':return {kind:'groups',value:[group]};
   case 'capacity':return {kind:'capacity',value:{totalBytes:1000,freeBytes:700,usedBytes:300,otherDownloadsBytes:50}};
   case 'rules':if(failRead)throw '/private/unavailable';return {kind:'rules',value:{revision,rules:values}};
   case 'setRules':if(operation.expectedRevision!==revision)throw 'downloads_conflict';values=operation.value as typeof flat;return {kind:'rules',value:{revision:++revision,rules:values}};
   default:throw 'invalid_command';
  }
 }});
 const lifetime=new AbortController(),folders:string[]=[];let wakes=0;
 const port=createDesktopSavedDownloadsPort({store,signal:lifetime.signal,playback,wake(){++wakes},folder:{async read(){return{displayPath:'/chosen/Portico'}},async choose(){folders.push('choose')},async open(){folders.push('open')}}});
 return {port,store,calls,folders,get wakes(){return wakes},retire(){current=false},failRead(){failRead=true},command(value:typeof custom){custom=value},close(){port.dispose();store.dispose();}};
}
test('Saved projects genuine native totals, title and bytes; no first-page aggregate or private artifact fields',async()=>{
 const h=await harness(),signal=new AbortController().signal;
 try{
  await h.port.refresh(signal);const snapshot=h.port.getSnapshot();
  assert.equal(snapshot.phase,'ready');assert.equal(snapshot.items.length,1);assert.equal(snapshot.groups?.[0].count,9);
  assert.equal(snapshot.groups?.[0].artist,undefined);assert.equal(snapshot.items[0].percent,25);assert.equal(snapshot.items[0].etaSeconds,undefined);
  assert.equal(snapshot.items[0].title.durationSeconds,181);assert.equal(snapshot.items[0].onServer,false);
  assert.equal('sha256' in snapshot.items[0],false);assert.equal('receiptExpiresMs' in snapshot.items[0],false);
  assert.equal(snapshot.rules?.revision,7);assert.equal(snapshot.storage?.usedBytes,300);assert.equal(snapshot.folder?.displayPath,'/chosen/Portico');
  assert.equal(h.port.playGroup,undefined);assert.ok(Object.isFrozen(snapshot.items));
  await h.port.openFolder(signal);assert.deepEqual(h.folders,['open']);
 }finally{h.close()}
});
test('Saved CAS returns the committed native revision even when the following read fails; conflicts cannot change it',async()=>{
 const h=await harness(),signal=new AbortController().signal;
 try{
  await h.port.refresh(signal);h.failRead();
  assert.deepEqual(await h.port.setRules(7,{...flat,limitBytes:123},signal),{revision:8});
  assert.equal(h.port.getSnapshot().rules?.revision,8);assert.equal(h.port.getSnapshot().rules?.value.limitBytes,123);assert.equal(h.wakes,1);
  await assert.rejects(h.port.setRules(7,flat,signal),/^Error: conflict$/);assert.equal(h.port.getSnapshot().rules?.revision,8);
 }finally{h.close()}
});
test('retiring the captured native scope discards late reads and synchronously erases the Saved snapshot',async()=>{
 const h=await harness();let finish!:(value:unknown)=>void,notices=0;
 h.port.subscribe(()=>++notices);
 h.command(async(_,operation)=>operation.action==='list'?new Promise(resolve=>{finish=resolve}):operation.action==='groups'?{kind:'groups',value:[]}:
  operation.action==='capacity'?{kind:'capacity',value:{totalBytes:1,freeBytes:1,usedBytes:0,otherDownloadsBytes:0}}:{kind:'rules',value:{revision:7,rules:flat}});
 try{
  const reading=h.port.refresh(new AbortController().signal);h.retire();finish({kind:'page',value:{items:[row],nextCursor:null}});
  await assert.rejects(reading,{name:'AbortError'});assert.equal(h.port.getSnapshot().available,false);assert.equal(h.port.getSnapshot().items.length,0);assert.equal(notices,0);
 }finally{h.close()}
});
test('native queue callbacks receive only exact native IDs/revisions and group Play remains independently optional',async()=>{
 const selections:unknown[]=[];const h=await harness({async play(selection){selections.push(selection)},async playGroup(selection){selections.push(selection)}}),signal=new AbortController().signal;
 try{
  await h.port.refresh(signal);await h.port.play(id,signal);
  await h.port.playGroup!({kind:'songs',parentId:'album',groupId:'disc'},groupRevision,signal);
  assert.deepEqual(selections,[{kind:'entry',entryId:id,expectedRevision:4},{kind:'group',selector:{kind:'songs',parentId:'album',groupId:'disc'},expectedRevision:groupRevision}]);
  await assert.rejects(h.port.playGroup!({kind:'songs',parentId:'other',groupId:'disc'},groupRevision,signal),/^Error: play$/);assert.equal(selections.length,2);
 }finally{h.close()}
});
test('unreported volume capacity stays absent without erasing genuine local files and rules',async()=>{
 const h=await harness();
 h.command(async(_,operation)=>{
  if(operation.action==='capacity')throw 'unsupported';
  if(operation.action==='list')return{kind:'page',value:{items:[row],nextCursor:null}};
  if(operation.action==='groups')return{kind:'groups',value:[group]};
  return{kind:'rules',value:{revision:7,rules:flat}};
 });
 try{await h.port.refresh(new AbortController().signal);const snapshot=h.port.getSnapshot();assert.equal(snapshot.phase,'ready');assert.equal(snapshot.items.length,1);assert.equal(snapshot.rules?.revision,7);assert.equal(snapshot.storage,undefined);}finally{h.close()}
});
