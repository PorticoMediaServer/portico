import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {DesktopOfflineCoordinator,type DesktopOfflineTransport} from '../src/offline.ts';
import {DesktopDownloadStore,type NativeDownloadsBridge} from '../src/downloads.ts';
import type {DownloadPreparation,DownloadGrant,DownloadBundle} from '../../../packages/client-core/src/downloads.ts';
import type {DownloadsApi} from '../../../packages/client-core/src/downloads-service.ts';

const title={kind:'song',title:'Actual title',artist:'Actual artist',parentId:'album',parentTitle:'Actual album',groupId:'disc',groupTitle:'',groupNumber:1,number:2,year:1977,durationSeconds:181,onServer:true};
const {durationSeconds,...titleFields}=title;
const artifact={kind:'source' as const,ref:'source',sha256:'c'.repeat(64),bytes:600000,estimated:false,container:'flac',contentType:'audio/flac',fileName:'actual.flac'};
const preparation:DownloadPreparation={id:'prep',itemId:'item',libraryId:'library',profileId:'profile',title,quality:'original',origin:'item',batchId:'batch',state:'ready',reason:'',artifact,progress:{bytesDone:artifact.bytes,bytesTotal:artifact.bytes,bytesTotalEstimated:false,percent:100,etaSeconds:null},actions:[],revision:1,createdAt:'2026-10-23T10:00:00Z',updatedAt:'2026-10-23T10:00:00Z',readyAt:'2026-10-23T10:00:00Z',expiresAt:'2026-11-23T10:00:00Z'};
const bundle:DownloadBundle={preparationId:'prep',itemId:'item',quality:'original',qualityLabel:'Original',title,overview:'',media:{container:'flac',videoCodec:'',audioCodec:'flac',width:0,height:0},artwork:[],chapters:[],markers:[],audio:[],subtitles:[]};
const grant:DownloadGrant={preparationId:'prep',itemId:'item',url:'/v1/downloads/artifacts/grant',token:'grant',issuedAt:'2026-10-23T10:00:00Z',expiresAt:'2026-11-23T10:00:00Z',renewAfterSeconds:60,renewed:false,artifact};
const scope={authority:'local' as const,serverId:'server',accountId:'account',profileId:'profile',sessionFamilyId:'family'};
const id='b'.repeat(64),lease='a'.repeat(64);
const claims={kind:'portico.download-receipt',version:'1',receiptId:'receipt',keyId:'key',viewer:{authority:'local',serverId:'server',accountId:'account',profileId:'profile'},itemId:'item',preparationId:'prep',quality:'original',qualityLabel:'Original',artifact:{sha256:artifact.sha256,bytes:artifact.bytes},issuedAt:'2026-10-23T10:00:00Z',expiresAt:'2026-11-23T10:00:00Z'};
const receipt={receiptId:'receipt',keyId:'key',algorithm:'ed25519',payload:Buffer.from(JSON.stringify(claims)).toString('base64url'),signature:Buffer.alloc(64).toString('base64url'),revision:1,claims};

async function harness(published:Partial<Pick<DesktopOfflineTransport,'resolvePublishedPath'|'isPublishedOrigin'>>={}){
  let current=true,cost:'unmetered'|'metered'|'unknown'='unmetered';
  let row={id,revision:0,preparationId:'prep',itemId:'item',quality:'original',sha256:artifact.sha256,bytes:artifact.bytes,bytesDone:0,state:'queued',held:false,addedMs:1,receiptExpiresMs:null,offlineAccess:'needsCheckIn',title:{...titleFields,durationMs:durationSeconds*1000}};
  const costListeners=new Set<()=>void>();
  const operations:Record<string,unknown>[]=[],requests:{path:string;body:unknown}[]=[];
  let afterAppend:(()=>void)|undefined,receiptResult:unknown={results:[{preparationId:'prep',receiptId:'receipt',outcome:'issued',code:'',receipt,titleOnServer:true}]};
  const native:NativeDownloadsBridge={async bind(){return lease},async release(){},async command(_,op){
    operations.push({...op});
    switch(op.action){
      case 'enroll':return {kind:'entry',value:{...row}};
      case 'list':return {kind:'page',value:{items:[{...row}],nextCursor:null}};
      case 'entry':return {kind:'entry',value:{...row}};
      case 'rules':return {kind:'rules',value:{revision:0,rules:{removeWatched:false,nextEpisode:false,unmeteredOnly:true,limitBytes:0}}};
      case 'append':assert.equal(op.offset,row.bytesDone);assert.equal(op.expectedRevision,row.revision);assert.ok((op.bytes as number[]).length<=262144);row={...row,revision:row.revision+1,bytesDone:row.bytesDone+(op.bytes as number[]).length,state:'downloading'};afterAppend?.();return {kind:'entry',value:{...row}};
      case 'seal':assert.equal(row.bytesDone,row.bytes);row={...row,revision:row.revision+1,state:'ready'};return {kind:'entry',value:{...row}};
      case 'hold':if(op.expectedRevision!==row.revision)throw 'downloads_conflict';row={...row,revision:row.revision+1,state:'queued'};return {kind:'entry',value:{...row}};
      case 'serverPresence':row={...row,revision:row.revision+1};return {kind:'entry',value:{...row}};
      default:return {kind:'done'};
    }
  }};
  const store=await DesktopDownloadStore.bind(scope,()=>current,native);
  let apiReply:((path:string,body:unknown)=>unknown)|undefined;
  const api:DownloadsApi={async request<T>(path:string,_method?:string,body?:unknown):Promise<T>{
    requests.push({path,body});
    if(apiReply){const reply=apiReply(path,body);if(reply!==undefined)return reply as T;}
    return (path.endsWith('receipt-keys')?{keys:[{id:'key',algorithm:'ed25519',publicKey:Buffer.alloc(32).toString('base64url'),createdAt:'2026-10-23T10:00:00Z'}]}:receiptResult) as T;
  }};
  let giveGrant=async()=>grant,fetcher:typeof fetch=async()=>new Response(new Uint8Array(artifact.bytes),{headers:{'content-length':String(artifact.bytes)}});
  const coordinator=new DesktopOfflineCoordinator({store,downloads:{grant:()=>giveGrant(),async bundle(){return bundle}},api,resolvePublishedPath:path=>'https://actual.test'+path,serverOrigin:'https://actual.test',...published,fetcher:(...args)=>fetcher(...args),networkCost:()=>cost,subscribeNetworkCost:listener=>{costListeners.add(listener);return()=>{costListeners.delete(listener)}}});
  return {store,coordinator,operations,requests,row:()=>row,setRow:(patch:Partial<typeof row>)=>{row={...row,...patch}},setCost:(value:typeof cost)=>{cost=value;for(const listener of [...costListeners])listener();},retire:()=>{current=false},setGrant:(fn:typeof giveGrant)=>{giveGrant=fn},setFetcher:(fn:typeof fetcher)=>{fetcher=fn},setAfterAppend:(fn:()=>void)=>{afterAppend=fn},setReceipt:(value:unknown)=>{receiptResult=value},setApi:(fn:NonNullable<typeof apiReply>)=>{apiReply=fn},dispose(){coordinator.dispose();store.dispose();}};
}

test('stream transfer uses bounded committed writes, native seal then signed-receipt envelope',async()=>{
  const h=await harness();try{
    await h.coordinator.saveReadyPreparation(preparation,new AbortController().signal);
    assert.equal(h.row().state,'ready');assert.equal(h.row().bytesDone,artifact.bytes);
    const actions=h.operations.map(v=>v.action);assert.ok(actions.indexOf('seal')<actions.indexOf('receipt'));
    const stored=h.operations.find(v=>v.action==='receipt')!;assert.ok(!('claims' in (stored.envelope as object)));
    assert.deepEqual(h.requests.at(-1)?.body,{operationId:(h.requests.at(-1)?.body as {operationId:string}).operationId,preparationIds:['prep']});
  }finally{h.dispose();}
});
test('unknown or metered network leaves persisted work queued and never requests media grant',async()=>{
  for(const cost of ['unknown','metered'] as const){const h=await harness();try{h.setCost(cost);h.setGrant(async()=>{throw new Error('must not request grant')});await h.coordinator.saveReadyPreparation(preparation,new AbortController().signal);assert.equal(h.row().state,'queued');assert.deepEqual(h.operations.map(v=>v.action),['enroll','rules']);}finally{h.dispose();}}
});
test('resume requires exact 206 range and sends If-Range; 200 never appends to a partial',async()=>{
  for(const status of [200,206]){const h=await harness();try{
    h.setRow({bytesDone:2,state:'downloading'});h.setFetcher(async(_,init)=>{
      assert.deepEqual(init?.headers,{Range:'bytes=2-','If-Range':`"sha256-${artifact.sha256}"`});
      assert.equal(init?.redirect,'error');assert.equal(init?.credentials,'omit');
      return new Response(new Uint8Array(artifact.bytes-2),{status,headers:{'content-range':`bytes 2-${artifact.bytes-1}/${artifact.bytes}`}});
    });
    const pending=h.coordinator.saveReadyPreparation(preparation,new AbortController().signal);
    if(status===206)await pending;else{await assert.rejects(pending,/download_verification_failed/);assert.equal(h.operations.some(v=>v.action==='append'),false);}
  }finally{h.dispose();}}
});
test('oversized or truncated bodies cannot seal or receive a receipt',async()=>{
  for(const delta of [-1,1]){const h=await harness();try{h.setFetcher(async()=>new Response(new Uint8Array(artifact.bytes+delta)));await assert.rejects(h.coordinator.saveReadyPreparation(preparation,new AbortController().signal),/download_verification_failed/);assert.equal(h.operations.some(v=>v.action==='seal'||v.action==='receipt'),false);}finally{h.dispose();}}
});
test('scope retirement while grant is in flight never starts a fetch or installs a receipt',async()=>{
  const h=await harness();let complete!:(value:DownloadGrant)=>void;const waiting=new Promise<DownloadGrant>(resolve=>{complete=resolve});h.setGrant(()=>waiting);h.setFetcher(async()=>{assert.fail('retired fetch');});
  try{const pending=h.coordinator.saveReadyPreparation(preparation,new AbortController().signal);await new Promise(resolve=>setImmediate(resolve));h.retire();complete(grant);await assert.rejects(pending,{name:'AbortError'});assert.equal(h.requests.length,0);}finally{h.dispose();}
});
test('abort after an actual committed append never seals and preserves native progress',async()=>{
  const h=await harness(),controller=new AbortController();h.setAfterAppend(()=>controller.abort());
  try{await assert.rejects(h.coordinator.saveReadyPreparation(preparation,controller.signal),{name:'AbortError'});assert.ok(h.row().bytesDone>0);assert.equal(h.operations.some(v=>v.action==='seal'),false);}finally{h.dispose();}
});
test('a changed grant artifact is rejected before any media fetch',async()=>{
  const h=await harness();h.setGrant(async()=>({...grant,artifact:{...artifact,sha256:'d'.repeat(64)}}));h.setFetcher(async()=>{assert.fail('wrong artifact fetch');});
  try{await assert.rejects(h.coordinator.saveReadyPreparation(preparation,new AbortController().signal),/download_verification_failed/);}finally{h.dispose();}
});
test('receipt refusal never fabricates playable success after bytes are sealed',async()=>{
  const h=await harness();h.setReceipt({results:[{preparationId:'prep',outcome:'refused',code:'downloads_not_allowed'}]});
  try{await assert.rejects(h.coordinator.saveReadyPreparation(preparation,new AbortController().signal),/invalid_receipt/);assert.equal(h.row().state,'ready');assert.equal(h.operations.some(v=>v.action==='receipt'),false);}finally{h.dispose();}
});
test('deferred progress acknowledges only a matching exact server echo',async()=>{
  const h=await harness(),observedMs=Date.parse('2026-10-23T10:00:00Z');
  const pending=[{playId:'play',itemId:'item',positionMs:12500,ended:false,observedMs}];
  const bridge:NativeDownloadsBridge={async bind(){return lease},async release(){},async command(_,op){if(op.action==='pendingPlays')return{kind:'pendingPlays',value:pending};h.operations.push({...op});return{kind:'done'}}};
  const store=await DesktopDownloadStore.bind(scope,()=>true,bridge);
  let entries:unknown[]=[{playId:'play',itemId:'item',positionMs:12500,watched:false,observedAt:'2026-10-23T10:00:01Z',outcome:'applied'}];
  const api:DownloadsApi={async request<T>(_path:string,_method?:string,body?:unknown){assert.equal((body as {entries:{positionMs:number}[]}).entries[0].positionMs,12500);return{applied:1,conflicts:0,entries} as T}};
  const coordinator=new DesktopOfflineCoordinator({store,downloads:{async grant(){return grant},async bundle(){return bundle}},api,serverOrigin:'https://actual.test',resolvePublishedPath:p=>'https://actual.test'+p,fetcher:fetch,networkCost:()=> 'unmetered'});
  try{await coordinator.syncProgress(new AbortController().signal);assert.equal(h.operations.length,0);entries=[{...(entries[0] as object),observedAt:'2026-10-23T10:00:00Z'}];await coordinator.syncProgress(new AbortController().signal);assert.deepEqual(h.operations,[{action:'acknowledgePlay',playId:'play',observedMs}]);}finally{coordinator.dispose();store.dispose();h.dispose();}
});
test('real HTTP response stream is cancelled on dispose; no receipt follows the partial body',async()=>{
  const h=await harness();let disconnected!:(value?:unknown)=>void;const closed=new Promise(resolve=>{disconnected=resolve});
  const server=createServer((_,res)=>{res.writeHead(200,{'Content-Length':String(artifact.bytes)});res.write(Buffer.alloc(1024));res.on('close',()=>disconnected());});
  await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));const address=server.address();assert.ok(address&&typeof address==='object');
  h.setFetcher(async(_url,init)=>fetch(`http://127.0.0.1:${address.port}/actual`,init));h.setAfterAppend(()=>h.coordinator.dispose());
  try{await assert.rejects(h.coordinator.saveReadyPreparation(preparation,new AbortController().signal),{name:'AbortError'});await closed;assert.equal(h.operations.some(v=>v.action==='seal'||v.action==='receipt'),false);}finally{server.closeAllConnections();await new Promise<void>(resolve=>server.close(()=>resolve()));h.dispose();}
});

test('same artifact across revision-only READY callbacks shares one genuine transfer',async()=>{
 const h=await harness();let release!:(grant:DownloadGrant)=>void;h.setGrant(()=>new Promise(resolve=>{release=resolve}));
 try{
  const first=h.coordinator.saveReadyPreparation(preparation,new AbortController().signal);
  await new Promise(resolve=>setImmediate(resolve));
  const second=h.coordinator.saveReadyPreparation({...preparation,revision:99},new AbortController().signal);
  release(grant);await Promise.all([first,second]);
  assert.equal(h.operations.filter(v=>v.action==='enroll').length,1);assert.equal(h.operations.filter(v=>v.action==='seal').length,1);
 }finally{h.dispose();}
});

test('queue wake uses persisted complete identity and exact current preparation; held and metered stay untouched',async()=>{
 for(const held of [false,true]){const h=await harness();try{
  h.setRow({held,bytesDone:2,state:held?'paused':'queued'});
  h.setApi(path=>path==='/v1/downloads/preparations/prep'?preparation:undefined);
  h.setFetcher(async(_url,init)=>{assert.deepEqual(init?.headers,{Range:'bytes=2-','If-Range':`"sha256-${artifact.sha256}"`});return new Response(new Uint8Array(artifact.bytes-2),{status:206,headers:{'content-range':`bytes 2-${artifact.bytes-1}/${artifact.bytes}`}});});
  await h.coordinator.resumeQueued(new AbortController().signal);
  assert.equal(h.row().state,held?'paused':'ready');assert.equal(h.requests.some(v=>v.path==='/v1/downloads/preparations/prep'),!held);
 }finally{h.dispose();}}
 const h=await harness();try{h.setCost('metered');await h.coordinator.resumeQueued(new AbortController().signal);assert.equal(h.requests.length,0);}finally{h.dispose();}
});

test('queue wake refuses a different captured profile before grant or enrollment',async()=>{
 const h=await harness();h.setApi(path=>path==='/v1/downloads/preparations/prep'?{...preparation,profileId:'other'}:undefined);
 try{await assert.rejects(h.coordinator.resumeQueued(new AbortController().signal),/download_verification_failed/);assert.equal(h.operations.some(v=>v.action==='enroll'||v.action==='append'),false);}finally{h.dispose();}
});

test('real HTTP transfer aborts on metered observation and acknowledges only durable queued progress',async()=>{
 const h=await harness();let disconnected!:()=>void;const closed=new Promise<void>(resolve=>{disconnected=resolve});
 const server=createServer((_,res)=>{res.writeHead(200,{'Content-Length':String(artifact.bytes)});res.write(Buffer.alloc(1024));res.on('close',disconnected);});
 await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));const address=server.address();assert.ok(address&&typeof address==='object');
 h.setFetcher(async(_url,init)=>fetch(`http://127.0.0.1:${address.port}/actual`,init));h.setAfterAppend(()=>h.setCost('metered'));
 try{await h.coordinator.saveReadyPreparation(preparation,new AbortController().signal);await closed;assert.equal(h.row().state,'queued');assert.ok(h.row().bytesDone>0);assert.equal(h.operations.some(v=>v.action==='seal'||v.action==='receipt'),false);}finally{server.closeAllConnections();await new Promise<void>(resolve=>server.close(()=>resolve()));h.dispose();}
});


test('only the captured published route can admit a different physical origin; replacement never widens egress',async()=>{
 for(const allowed of [true,false]){
  const h=await harness({resolvePublishedPath:path=>'https://proved.test'+path,isPublishedOrigin:origin=>allowed&&origin==='https://proved.test'});let fetched=0;
  h.setFetcher(async input=>{fetched++;assert.equal(String(input),'https://proved.test'+grant.url);return new Response(new Uint8Array(artifact.bytes));});
  try{const pending=h.coordinator.saveReadyPreparation(preparation,new AbortController().signal);if(allowed)await pending;else await assert.rejects(pending,/invalid_command/);assert.equal(fetched,allowed?1:0);}
  finally{h.dispose();}
 }
});

test('published route admission cannot authorize credentials, fragments or a non-HTTP protocol',async()=>{
 for(const address of ['https://user:secret@proved.test/file','https://proved.test/file#hidden','file:///private/file']){
  const h=await harness({resolvePublishedPath:()=>address,isPublishedOrigin:()=>true});let fetched=false;h.setFetcher(async()=>{fetched=true;throw new Error('must not fetch')});
  try{await assert.rejects(h.coordinator.saveReadyPreparation(preparation,new AbortController().signal),/invalid_command/);assert.equal(fetched,false);}
  finally{h.dispose();}
 }
});
