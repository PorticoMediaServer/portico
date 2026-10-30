import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DesktopDownloadStore,downloadFailure,parseLocalDownload,type DownloadScope,type NativeDownloadsBridge} from '../src/downloads.ts';
const scope:DownloadScope={authority:'local',serverId:'server',accountId:'account',profileId:'profile',sessionFamilyId:'family'};
const lease='a'.repeat(64),rowId='b'.repeat(64);
const row={id:rowId,preparationId:'prep',itemId:'item',quality:'original',sha256:'c'.repeat(64),bytes:8,bytesDone:2,state:'downloading',held:false,revision:4,addedMs:1,receiptExpiresMs:null,offlineAccess:'needsCheckIn',title:{kind:'song',title:'Actual stored title',artist:'Actual artist',parentId:'album',parentTitle:'Actual album',groupId:'disc',groupTitle:'',groupNumber:1,number:2,year:1977,durationMs:181000,onServer:false}};
function bridge(command:NativeDownloadsBridge['command']):NativeDownloadsBridge{return{async bind(){return lease},async release(){},command};}
test('native records preserve canonical title units without exposing raw fields',()=>{
 const parsed=parseLocalDownload({...row,path:'/private/never',token:'never'});
 assert.equal(parsed.title.durationSeconds,181);assert.equal(parsed.title.onServer,false);
 assert.ok(Object.isFrozen(parsed));assert.ok(!('path' in parsed));assert.ok(!('token' in parsed));
 assert.throws(()=>parseLocalDownload({...row,bytesDone:9}));
 assert.equal(parsed.offlineAccess,'needsCheckIn');
 assert.throws(()=>parseLocalDownload({...row,offlineAccess:undefined}));
 assert.throws(()=>parseLocalDownload({...row,offlineAccess:'ready'}));
});
test('a late native acknowledgement is discarded after captured viewer retirement',async()=>{
 let current=true,finish!:(value:unknown)=>void;const releases:string[]=[];
 const native=bridge(()=>new Promise(resolve=>{finish=resolve}));
 const store=await DesktopDownloadStore.bind(scope,()=>current,{...native,async release(value){releases.push(value)}});
 const request=store.entry(rowId);current=false;finish({kind:'entry',value:row});
 await assert.rejects(request,{name:'AbortError'});store.dispose();await Promise.resolve();assert.deepEqual(releases,[lease]);
});
test('cancelled bind retires the actual native lease and never returns a usable adapter',async()=>{
 const controller=new AbortController();let complete!:(value:string)=>void;const releases:string[]=[];
 const request=DesktopDownloadStore.bind(scope,()=>true,{...bridge(async()=>null),bind:()=>new Promise(resolve=>{complete=resolve}),async release(value){releases.push(value)}},controller.signal);
 controller.abort();complete(lease);await assert.rejects(request,{name:'AbortError'});assert.deepEqual(releases,[lease]);
});
test('bounded writes carry committed revision and offset; oversized payload never invokes native',async()=>{
 const operations:unknown[]=[];const store=await DesktopDownloadStore.bind(scope,()=>true,bridge(async(_,op)=>{operations.push(op);return{kind:'entry',value:{...row,revision:5,bytesDone:4}}}));
 const saved=await store.append(parseLocalDownload(row),new Uint8Array([3,4]));
 assert.equal(saved.revision,5);assert.deepEqual(operations,[{action:'append',id:rowId,expectedRevision:4,offset:2,bytes:[3,4]}]);
 await assert.rejects(store.append(saved,new Uint8Array(262145)),/invalid_command/);assert.equal(operations.length,1);store.dispose();
});
test('artwork is a native byte Blob or null; raw errors never reach UI',async()=>{
 const store=await DesktopDownloadStore.bind(scope,()=>true,bridge(async()=>({kind:'artwork',value:{mime:'image/png',bytes:[137,80,78,71]}})));
 const blob=await store.getArtwork(rowId);assert.equal(blob?.type,'image/png');assert.deepEqual([...new Uint8Array(await blob!.arrayBuffer())],[137,80,78,71]);store.dispose();
 assert.equal(downloadFailure(new Error('/private/path?secret=hidden')).message,'downloads_unavailable');assert.equal(downloadFailure('downloads_conflict').message,'downloads_conflict');
});
test('native folder read exposes only its deliberate display path; cancellation before begin ACK never opens a dialog',async()=>{
 let finish!:(value:unknown)=>void;const cancelled:string[]=[],effects:string[]=[];
 const folder:NonNullable<NativeDownloadsBridge['folder']>={async location(){return{displayPath:'/owned/Portico',private:'not projected'}},begin:()=>new Promise(resolve=>{finish=resolve}),async choose(token){effects.push(token)},async open(token){effects.push(token)},async cancel(token){cancelled.push(token)}};
 const store=await DesktopDownloadStore.bind(scope,()=>true,{...bridge(async()=>null),folder});
 try{
  assert.deepEqual(await store.folderLocation(new AbortController().signal),{displayPath:'/owned/Portico'});
  const controller=new AbortController(),task=store.chooseFolder(controller.signal);controller.abort();finish('f'.repeat(64));
  await assert.rejects(task,{name:'AbortError'});assert.deepEqual(effects,[]);assert.deepEqual(cancelled,['f'.repeat(64)]);
 }finally{store.dispose()}
});
test('disposing a folder operation retires its exact native token and never publishes a late completion',async()=>{
 let finish!:()=>void;const cancelled:string[]=[];let notices=0;
 const folder:NonNullable<NativeDownloadsBridge['folder']>={async location(){return{displayPath:'/owned'}},async begin(){return'f'.repeat(64)},choose:()=>new Promise(resolve=>{finish=resolve}),async open(){},async cancel(token){cancelled.push(token)}};
 const store=await DesktopDownloadStore.bind(scope,()=>true,{...bridge(async()=>null),folder});store.subscribe(()=>++notices);
 const task=store.chooseFolder(new AbortController().signal);await new Promise(resolve=>setImmediate(resolve));store.dispose();finish();
 await assert.rejects(task,{name:'AbortError'});assert.equal(notices,0);assert.ok(cancelled.every(token=>token==='f'.repeat(64)));assert.ok(cancelled.length>0);
});

test('MAIN group facts are complete native aggregates without first-row metadata or guessed access',async()=>{
 const revision='d'.repeat(64);let actual:Record<string,unknown>={kind:'songs',parentId:'album',groupId:'disc',count:4,bytes:32,bytesDone:16,revision,offlineAccess:'needsCheckIn'};
 const calls:Record<string,unknown>[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>true,bridge(async(_,op)=>{calls.push({...op});return op.action==='groups'?{kind:'groups',value:[actual]}:{kind:'done'}}));
 try{
  let groups=await store.groups();assert.equal(groups[0].count,4);assert.equal(groups[0].offlineAccess,'needsCheckIn');assert.equal('artist' in groups[0],false);assert.equal('parentTitle' in groups[0],false);
  actual={...actual,parentTitle:'Uniform native album',artist:'Uniform native artist',held:false,state:'ready',offlineAccess:'valid'};
  groups=await store.groups();assert.equal(groups[0].artist,'Uniform native artist');assert.equal(groups[0].parentTitle,'Uniform native album');
  await store.actionGroup({kind:'songs',parentId:'album',groupId:'disc'},revision,'pause');
  assert.deepEqual(calls.at(-1),{action:'groupAction',selector:{kind:'songs',parentId:'album',groupId:'disc'},expectedRevision:revision,operation:'pause'});
  actual={...actual,bytesDone:33};await assert.rejects(store.groups(),/downloads_unavailable/);
 }finally{store.dispose();}
});

test('other-profile removal sends exact native confirmation and rejects every different captured scope',async()=>{
 const token='e'.repeat(64),calls:Record<string,unknown>[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>true,bridge(async(_,op)=>{calls.push({...op});return op.action==='previewOtherProfiles'?{kind:'otherProfiles',value:{snapshotToken:token,bytes:777}}:{kind:'done'}}));
 try{
  const confirmation=await store.previewOtherProfiles();assert.deepEqual(confirmation,{scope,snapshotToken:token,bytes:777});
  for(const key of ['authority','serverId','accountId','profileId','sessionFamilyId'] as const){
   const other={...confirmation,scope:{...scope,[key]:key==='authority'?'hosted':'other'}};
   await assert.rejects(store.removeOtherProfiles(other),/downloads_conflict/);
  }
  assert.equal(calls.length,1);await store.removeOtherProfiles(confirmation);
  assert.deepEqual(calls.at(-1),{action:'removeOtherProfiles',snapshotToken:token});
 }finally{store.dispose();}
});

const prepared=()=>({preparationId:'f'.repeat(64),scope,selectionRevision:'d'.repeat(64),items:[
 {id:'1'.repeat(64),revision:2,itemId:'second',localSourceId:'2'.repeat(64),durationMs:181000,title:row.title},
 {id:'3'.repeat(64),revision:4,itemId:'first',localSourceId:'4'.repeat(64),durationMs:181000,title:{...row.title,number:3}},
]});
const selection={kind:'group' as const,selector:{kind:'songs' as const,parentId:'album',groupId:'disc'},expectedRevision:'d'.repeat(64)};

test('complete native queue preparation preserves native order and releases only its opaque preparation',async()=>{
 const calls:Record<string,unknown>[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>true,bridge(async(_,operation)=>{
  calls.push({...operation});return operation.action==='prepareLocalQueue'?{kind:'localQueue',value:{...prepared(),path:'/private/not-public'}}:{kind:'done'};
 }));
 try{
  const queue=await store.prepareLocalQueue(selection,new AbortController().signal);
  assert.deepEqual(queue.items.map(value=>value.itemId),['second','first']);assert.equal(queue.items[0].title.durationSeconds,181);
  assert.equal(Object.isFrozen(queue.items),true);assert.doesNotMatch(JSON.stringify(queue),/private|path|lease/);
  await store.releaseLocalQueue(queue);
  assert.deepEqual(calls.at(-1),{action:'releaseLocalQueue',preparationId:'f'.repeat(64)});
  await assert.rejects(store.releaseLocalQueue({...queue}),/downloads_conflict/);
 }finally{store.dispose();}
});

test('late native preparation after cancellation and disposal retires its real opaque result',async()=>{
 let finish!:(value:unknown)=>void;const calls:Record<string,unknown>[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>true,bridge(async(_,operation)=>{
  calls.push({...operation});return operation.action==='prepareLocalQueue'?new Promise(resolve=>{finish=resolve}):{kind:'done'};
 }));
 const abort=new AbortController(),pending=store.prepareLocalQueue(selection,abort.signal);
 abort.abort();store.dispose();finish({kind:'localQueue',value:prepared()});
 await assert.rejects(pending,{name:'AbortError'});
 assert.deepEqual(calls.at(-1),{action:'releaseLocalQueue',preparationId:'f'.repeat(64)});
});

test('a wrong family, selection revision or duplicate native queue handle is rejected and retired',async()=>{
 for(const bad of [
  {...prepared(),scope:{...scope,sessionFamilyId:'other'}},
  {...prepared(),selectionRevision:'a'.repeat(64)},
  {...prepared(),items:[prepared().items[0],{...prepared().items[1],localSourceId:prepared().items[0].localSourceId}]},
 ]){
  const calls:Record<string,unknown>[]=[];
  const store=await DesktopDownloadStore.bind(scope,()=>true,bridge(async(_,operation)=>{calls.push({...operation});return operation.action==='prepareLocalQueue'?{kind:'localQueue',value:bad}:{kind:'done'};}));
  try{await assert.rejects(store.prepareLocalQueue(selection,new AbortController().signal),/downloads_unavailable/);assert.equal(calls.at(-1)?.action,'releaseLocalQueue');}
  finally{store.dispose();}
 }
});

const localReceipt=(generation=9)=>({kind:'local',scope,preparationId:prepared().preparationId,localSourceId:prepared().items[0].localSourceId,generation,
 sessionId:`local:${prepared().items[0].id}:1`,playId:'7'.repeat(64),entryId:prepared().items[0].id,itemId:prepared().items[0].itemId,artifact:{sha256:'8'.repeat(64),bytes:5}});
test('local load returns only native acknowledged identity/artifact and never synthesizes a URL or journal play',async()=>{
 const requests:unknown[]=[],retired:unknown[]=[];
 const native:NativeDownloadsBridge={...bridge(async()=>({kind:'localQueue',value:prepared()})),async loadLocal(lease,source){requests.push({lease,source});return {...localReceipt(source.generation),path:'/private/not-public',token:'hidden'}},async retireLocal(lease,generation){retired.push({lease,generation})}};
 const store=await DesktopDownloadStore.bind(scope,()=>true,native),signal=new AbortController().signal;
 try{
  const queue=await store.prepareLocalQueue(selection,signal),ack=await store.loadLocal(queue,queue.items[0].localSourceId,9,0,true,signal);
  assert.deepEqual(ack,localReceipt());assert.doesNotMatch(JSON.stringify(ack),/path|private|token|url|lease/);
  assert.deepEqual(requests,[{lease,source:{preparationId:queue.preparationId,localSourceId:queue.items[0].localSourceId,generation:9,startPositionMs:0,autoplay:true}}]);
  assert.ok(Object.isFrozen(ack.artifact));assert.equal(retired.length,0);
  await store.retireLocal(9);assert.deepEqual(retired,[{lease,generation:9}]);
 }finally{store.dispose();}
});
test('late local ACK after cancellation/disposal retires exactly its captured native generation and original lease',async()=>{
 let complete!:(value:unknown)=>void;const retired:unknown[]=[];
 const native:NativeDownloadsBridge={...bridge(async(_,op)=>op.action==='prepareLocalQueue'?{kind:'localQueue',value:prepared()}:{kind:'done'}),loadLocal:()=>new Promise(resolve=>{complete=resolve}),async retireLocal(lease,generation){retired.push({lease,generation})}};
 const store=await DesktopDownloadStore.bind(scope,()=>true,native),abort=new AbortController(),queue=await store.prepareLocalQueue(selection,abort.signal);
 const pending=store.loadLocal(queue,queue.items[0].localSourceId,9,0,true,abort.signal);
 abort.abort();store.dispose();complete(localReceipt());
 await assert.rejects(pending,{name:'AbortError'});assert.deepEqual(retired,[{lease,generation:9}]);
});
test('mismatched native local receipt cannot be adopted, and the potentially committed load is retired',async()=>{
 for(const bad of [
  {...localReceipt(),scope:{...scope,sessionFamilyId:'other'}},
  {...localReceipt(),generation:8},
  {...localReceipt(),preparationId:'a'.repeat(64)},
  {...localReceipt(),localSourceId:'b'.repeat(64)},
  {...localReceipt(),sessionId:'remote-session'},
  {...localReceipt(),artifact:{sha256:'8'.repeat(64),bytes:0}},
 ]){
  const retired:number[]=[];
  const store=await DesktopDownloadStore.bind(scope,()=>true,{...bridge(async()=>({kind:'localQueue',value:prepared()})),async loadLocal(){return bad},async retireLocal(_,generation){retired.push(generation)}});
  try{const signal=new AbortController().signal,queue=await store.prepareLocalQueue(selection,signal);await assert.rejects(store.loadLocal(queue,queue.items[0].localSourceId,9,0,true,signal),/downloads_unavailable/);assert.deepEqual(retired,[9]);}
  finally{store.dispose();}
 }
});

test('failed native retirement retries only its original lease after disposal; unknown generations do nothing',async()=>{
 const calls:unknown[]=[],releases:string[]=[];let fail=true;
 const native:NativeDownloadsBridge={...bridge(async(_,op)=>op.action==='prepareLocalQueue'?{kind:'localQueue',value:prepared()}:{kind:'done'}),
  async release(value){releases.push(value)},async loadLocal(_,request){return localReceipt(request.generation)},
  async retireLocal(value,generation){calls.push({lease:value,generation});if(fail)throw new Error('/private/never surface');}};
 const store=await DesktopDownloadStore.bind(scope,()=>true,native),signal=new AbortController().signal;
 const queue=await store.prepareLocalQueue(selection,signal);await store.loadLocal(queue,queue.items[0].localSourceId,9,0,true,signal);
 await store.retireLocal(10);assert.deepEqual(calls,[]);
 await assert.rejects(store.retireLocal(9),/^Error: downloads_unavailable$/);
 store.dispose();await new Promise(resolve=>setImmediate(resolve));
 assert.deepEqual(releases,[lease]);fail=false;await store.retireLocal(9);await store.retireLocal(9);
 assert.equal(calls.length,3);assert.ok(calls.every(call=>JSON.stringify(call)===JSON.stringify({lease,generation:9})));
});

test('concurrent retirement shares the genuine native acknowledgement and blocks generation reuse until ACK',async()=>{
 let finish!:()=>void,calls=0;
 const store=await DesktopDownloadStore.bind(scope,()=>true,{...bridge(async(_,op)=>op.action==='prepareLocalQueue'?{kind:'localQueue',value:prepared()}:{kind:'done'}),
  async loadLocal(_,request){return localReceipt(request.generation)},retireLocal:()=>{calls++;return new Promise(resolve=>{finish=resolve})}});
 try{
  const signal=new AbortController().signal,queue=await store.prepareLocalQueue(selection,signal);
  await store.loadLocal(queue,queue.items[0].localSourceId,9,0,true,signal);
  const first=store.retireLocal(9),second=store.retireLocal(9);
  await assert.rejects(store.loadLocal(queue,queue.items[0].localSourceId,9,0,true,signal),/downloads_conflict/);
  assert.equal(calls,1);finish();await Promise.all([first,second]);
 }finally{store.dispose()}
});

test('retirement cannot acknowledge cleanup before an outstanding native load commits',async()=>{
 let finish!:(value:unknown)=>void;const retired:number[]=[];
 const store=await DesktopDownloadStore.bind(scope,()=>true,{...bridge(async(_,op)=>op.action==='prepareLocalQueue'?{kind:'localQueue',value:prepared()}:{kind:'done'}),
  loadLocal:()=>new Promise(resolve=>{finish=resolve}),async retireLocal(_,generation){retired.push(generation)}});
 try{
  const abort=new AbortController(),queue=await store.prepareLocalQueue(selection,abort.signal);
  const load=store.loadLocal(queue,queue.items[0].localSourceId,9,0,true,abort.signal);
  abort.abort();const retirement=store.retireLocal(9);await Promise.resolve();assert.deepEqual(retired,[]);
  finish(localReceipt());await assert.rejects(load,{name:'AbortError'});await retirement;assert.deepEqual(retired,[9]);
 }finally{store.dispose()}
});
