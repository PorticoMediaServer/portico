import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService} from '../src/downloads-service.ts';

const sha='a'.repeat(64);
const usageDoc={profileId:'profile',profileBytes:0,profileCount:0,serverBytes:0,serverCount:0,distinctArtifactBytes:0,maxPreparedBytes:0,remainingBytes:null,retentionDays:7,profiles:[]};
const preparation=(over:Record<string,unknown>={}):any=>({
 id:'prep',itemId:'item',libraryId:'lib',profileId:'profile',quality:'1080p-low',origin:'item',title:{kind:'movie',title:'Film',onServer:true},batchId:'batch',state:'ready',reason:'',
 artifact:{kind:'prepared',ref:'version',sha256:sha,bytes:4096,estimated:false,container:'mp4',contentType:'video/mp4',fileName:'prep.mp4'},
 progress:{bytesDone:4096,bytesTotal:4096,bytesTotalEstimated:false,percent:100,etaMs:null},
 actions:['remove'],revision:3,createdAt:'2026-09-16T10:00:00Z',updatedAt:'2026-09-16T10:05:00Z',readyAt:'2026-09-16T10:05:00Z',expiresAt:'2026-10-16T10:05:00Z',...over});
const running=()=>preparation({id:'busy',state:'running',artifact:{kind:'',ref:'',sha256:'',bytes:0,estimated:true,container:'',contentType:'',fileName:''},progress:{bytesDone:100,bytesTotal:1000,bytesTotalEstimated:true,percent:10,etaMs:60000},actions:['pause','cancel'],readyAt:'',expiresAt:''});
const grantDoc={preparationId:'prep',itemId:'item',url:'/v1/downloads/artifacts/tok-1',token:'tok-1',issuedAt:'2026-09-17T00:00:00Z',expiresAt:'2026-09-17T00:10:00Z',renewAfterMs:600000,renewed:false,artifact:{kind:'prepared',ref:'version',sha256:sha,bytes:4096,estimated:false,container:'mp4',contentType:'video/mp4',fileName:'prep.mp4'}};

function server(items:any[]){
 const calls:{path:string;method:string;body?:any}[]=[];let fail:any;
 return{calls,failWith:(e:any)=>{fail=e;},set:(next:any[])=>{items=next;},api:{request:async<T,>(path:string,method='GET',body?:any):Promise<T>=>{
  calls.push({path,method,body});if(fail)throw fail;
  if(path.startsWith('/v1/downloads/preparations?')){
   const query=new URL(path,'https://server.test').searchParams;
   const state=query.get('state');
   const limit=Number(query.get('limit')??'100');
   const cursor=Number(query.get('cursor')??'0');
   const ids=query.get('ids')?.split(',');
   const pool=ids?items.filter(p=>ids.includes(p.id)):state?items.filter(p=>p.state===state):items;
   const slice=pool.slice(cursor,cursor+limit);
   return {items:slice,nextCursor:cursor+limit<pool.length?String(cursor+limit):''} as T;
  }
  if(path==='/v1/downloads/usage')return usageDoc as T;
  if(/^\/v1\/downloads\/preparations\/[^/]+\/grant$/.test(path))return grantDoc as T;
  const single=path.match(/^\/v1\/downloads\/preparations\/([^/]+)$/);
  if(single&&method==='GET'){const row=items.find(p=>p.id===single[1]);if(!row)throw Object.assign(new Error('Not found'),{status:404});return row as T;}
  if(path.endsWith('/actions')){const id=path.split('/')[4];const row=items.find(p=>p.id===id);const next={...row,state:body.action==='pause'?'paused':row.state,actions:['resume','cancel'],revision:row.revision+1};items=items.map(p=>p.id===id?next:p);return next as T;}
  throw new Error('unexpected '+path);}}};
}

test('a failed refresh keeps the list; a server without downloads is a fact, not an error',async()=>{
 const s=server([preparation()]),service=new DownloadsService(s.api,()=>'op',10);
 try{
  await service.refresh();assert.equal(service.getSnapshot().items.length,1);
  s.failWith(new TypeError('Failed to fetch'));await service.refresh();
  assert.equal(service.getSnapshot().phase,'ready');assert.equal(service.getSnapshot().items.length,1);assert.ok(service.getSnapshot().error,'a catalogue message');assert.ok(service.getSnapshot().failure,'the error itself is kept for presentError');
 }finally{service.dispose();}
 const none=server([]);none.failWith(Object.assign(new Error('nope'),{status:404}));
 const off=new DownloadsService(none.api,()=>'op',10);
 try{
  await off.refresh();assert.equal(off.getSnapshot().unavailable,true);assert.equal(off.getSnapshot().error,undefined);
 }finally{off.dispose();}
});

test('it polls only while something is still being prepared, and stops when told',async()=>{
 const s=server([running()]),service=new DownloadsService(s.api,()=>'op',15);
 try{
  service.start();await new Promise(r=>setTimeout(r,60));
  const polls=s.calls.filter(c=>c.path.startsWith('/v1/downloads/preparations?')).length;assert.ok(polls>=2,'kept polling while running: '+polls);
  s.set([preparation()]);await new Promise(r=>setTimeout(r,60));
  const settled=s.calls.filter(c=>c.path.startsWith('/v1/downloads/preparations?')).length;
  await new Promise(r=>setTimeout(r,60));
  assert.equal(s.calls.filter(c=>c.path.startsWith('/v1/downloads/preparations?')).length,settled,'no polling once everything is ready');
 }finally{service.dispose();}
});

test('an action carries the revision it was made against',async()=>{
 const s=server([running()]),service=new DownloadsService(s.api,()=>'op-7',10000);
 try{
  await service.refresh();await service.act(service.getSnapshot().items[0],'pause');
  const sent=s.calls.find(c=>c.path.endsWith('/actions'))!;
  assert.deepEqual(sent.body,{operationId:'op-7',action:'pause',expectedRevision:3});
  assert.equal(service.getSnapshot().items[0].state,'paused');
  await new Promise(r=>setTimeout(r,5));assert.equal(service.getSnapshot().items[0].state,'paused','the follow-up read agrees');
 }finally{service.dispose();}
});

test('a conflict re-reads the row and re-sends the action once when it still applies',async()=>{
 const calls:{path:string;method:string;body?:any}[]=[];let attempts=0;
 const stale=preparation({actions:['pause','cancel'],revision:3});
 const current=preparation({actions:['pause','cancel'],revision:5});
 const api={request:async<T,>(path:string,method='GET',body?:any):Promise<T>=>{
  calls.push({path,method,body});
  if(path.startsWith('/v1/downloads/preparations?'))return {items:[attempts?current:stale],nextCursor:''} as T;
  if(path==='/v1/downloads/usage')return usageDoc as T;
  if(path.endsWith('/actions')){attempts++;if(attempts===1)throw Object.assign(new Error('Someone else changed this download.'),{status:409});return preparation({actions:['cancel'],revision:6,state:'paused'}) as T;}
  if(/^\/v1\/downloads\/preparations\/prep$/.test(path))return current as T;
  throw new Error('unexpected '+method+' '+path);}};
 const service=new DownloadsService(api,()=>'op-7',10000);
 try{
  await service.refresh();
  await service.act(service.getSnapshot().items[0],'pause');
  const sends=calls.filter(c=>c.path.endsWith('/actions'));
  assert.equal(sends.length,2,'the action was re-sent once after the conflict');
  assert.deepEqual(sends[0].body,{operationId:'op-7',action:'pause',expectedRevision:3});
  assert.deepEqual(sends[1].body,{operationId:'op-7',action:'pause',expectedRevision:5},'the re-send fences on the revision it just read');
  await new Promise(r=>setTimeout(r,10));
  assert.equal(service.getSnapshot().error,undefined,'a conflict that resolved is not an error to show');
 }finally{service.dispose();}
});

test('a conflict for an action that no longer applies shows the current row without an error',async()=>{
 const calls:{path:string;method:string;body?:any}[]=[];let conflicted=false;
 const stale=preparation({actions:['pause','cancel'],revision:3});
 const current=preparation({actions:['remove'],revision:4});
 const api={request:async<T,>(path:string,method='GET',body?:any):Promise<T>=>{
  calls.push({path,method,body});
  if(path.startsWith('/v1/downloads/preparations?'))return {items:[conflicted?current:stale],nextCursor:''} as T;
  if(path==='/v1/downloads/usage')return usageDoc as T;
  if(path.endsWith('/actions'))throw Object.assign(new Error('Someone else changed this download.'),{status:409});
  if(/^\/v1\/downloads\/preparations\/prep$/.test(path)){conflicted=true;return current as T;}
  throw new Error('unexpected '+method+' '+path);}};
 const service=new DownloadsService(api,()=>'op-7',10000);
 try{
  await service.refresh();
  await service.act(service.getSnapshot().items[0],'pause');
  assert.equal(calls.filter(c=>c.path.endsWith('/actions')).length,1,'an action that no longer applies is not re-sent');
  await new Promise(r=>setTimeout(r,10));
  assert.equal(service.getSnapshot().error,undefined);
  assert.equal(service.getSnapshot().items[0].revision,4,'the row is shown as it is now');
 }finally{service.dispose();}
});

test('grant(preparation, token) renews the same address',async()=>{
 const s=server([preparation()]),service=new DownloadsService(s.api,()=>'op-g',10000);
 try{
  const fresh=await service.grant(preparation());
  assert.equal(fresh.token,'tok-1');
  assert.equal(fresh.renewAfterSeconds,600);
  const renewed=await service.grant(preparation(),'tok');
  assert.equal(renewed.url,'/v1/downloads/artifacts/tok-1');
  const grants=s.calls.filter(c=>c.path.endsWith('/grant'));
  assert.deepEqual(grants[0].body,{operationId:'op-g'},'a first grant sends no renewal');
  assert.deepEqual(grants[1].body,{operationId:'op-g',renew:'tok'});
 }finally{service.dispose();}
});

test('followDevice finds the requests this device started',async()=>{
 const summary={requestId:'req-1',state:'complete',target:{kind:'season',id:'s-1'},title:'Season 1',quality:'original',createdAt:'2026-09-16T10:00:00Z'};
 const view={requestId:'req-1',total:2,totalKnown:true,state:'complete',ready:2,preparing:0,queued:0,failed:0,paused:0,items:[],nextCursor:''};
 const calls:{path:string;method:string;body?:any}[]=[];
 const api={request:async<T,>(path:string,method='GET',body?:any):Promise<T>=>{
  calls.push({path,method,body});
  if(path==='/v1/downloads/requests?'+new URLSearchParams({deviceId:'dev-1',limit:'50'}))return {requests:[summary]} as T;
  if(path.startsWith('/v1/downloads/requests/req-1'))return view as T;
  if(path.startsWith('/v1/downloads/preparations?'))return {items:[],nextCursor:''} as T;
  if(path==='/v1/downloads/usage')return usageDoc as T;
  throw new Error('unexpected '+method+' '+path);}};
 const service=new DownloadsService(api,()=>'op',10000);
 try{
  service.followDevice('dev-1');
  service.start();
  await new Promise(r=>setTimeout(r,40));
  assert.ok(calls.some(c=>c.path==='/v1/downloads/requests?deviceId=dev-1&limit=50'),'the device asks for its own requests');
  const tracked=service.getSnapshot().requests.find(r=>r.request.requestId==='req-1');
  assert.ok(tracked,'the listed request is tracked');
  assert.equal(tracked!.title,'Season 1');
  assert.equal(tracked!.request.state,'complete');
  assert.equal(tracked!.request.ready,2);
 }finally{service.dispose();}
});

test('PERF-S12: with 1,100 preparations a poll reads only the active pages (<= 2 requests)',async()=>{
 const settled=(i:number)=>preparation({id:'prep-'+i,itemId:'item-'+i,state:'ready',revision:3});
 const runningOne=running();runningOne.id='prep-0';runningOne.itemId='item-0';
 const s=server([runningOne,...Array.from({length:1099},(_,i)=>settled(i+1))]);
 const service=new DownloadsService(s.api,()=>'op',20);
 try{
  service.start();
  await new Promise(r=>setTimeout(r,10));
  // The list itself is paged: the first page only, never 10x100 up front.
  assert.equal(service.getSnapshot().items.length,100);
  assert.ok(service.getSnapshot().nextCursor,'more pages remain');
  await new Promise(r=>setTimeout(r,100));
  const pages=()=>s.calls.filter(c=>c.path.startsWith('/v1/downloads/preparations?'));
  const polls=pages().filter(c=>c.path.includes('state='));
  // Every poll is exactly the two active-state pages; the only unfiltered read is the first page.
  assert.ok(polls.length>=4,'polled while active: '+polls.length);
  assert.ok(polls.length%2===0,'polls come in queued+running pairs: '+polls.length);
  assert.ok(polls.every(c=>c.path==='/v1/downloads/preparations?limit=100&state=queued'||c.path==='/v1/downloads/preparations?limit=100&state=running'));
  assert.equal(pages().filter(c=>!c.path.includes('state=')&&!c.path.includes('ids=')).length,1,'one unfiltered first page, never a full re-read');
  // Finish it server-side: one single read settles it, then polling stops (<= 2 per poll held).
  s.set([settled(0),...Array.from({length:1099},(_,i)=>settled(i+1))]);
  await new Promise(r=>setTimeout(r,80));
  assert.equal(service.getSnapshot().items.find(p=>p.id==='prep-0')?.state,'ready');
  const settledCount=pages().length;
  await new Promise(r=>setTimeout(r,60));
  assert.equal(pages().length,settledCount,'polling stopped once nothing was active');
 }finally{service.dispose();}
});

test('PERF-S12: every preparation is reachable by paging, and a finished one settles without a reload',async()=>{
 const item=(i:number,state:string)=>preparation({id:'prep-'+i,itemId:'item-'+i,state,revision:3,artifact:state==='ready'?preparation().artifact:running().artifact,progress:state==='ready'?preparation().progress:running().progress,actions:state==='ready'?['remove']:['pause','cancel'],readyAt:state==='ready'?'2026-09-16T10:05:00Z':'',expiresAt:state==='ready'?'2026-10-16T10:05:00Z':''});
 const rows=[item(0,'running'),...Array.from({length:1099},(_,i)=>item(i+1,'ready'))];
 const s=server(rows);
 const service=new DownloadsService(s.api,()=>'op',15);
 try{
  await service.refresh();
  for(let page=0;page<10;page++)await service.loadMore();
  assert.equal(service.getSnapshot().items.length,1100,'all 1,100 reachable by paging');
  assert.equal(service.getSnapshot().nextCursor,'','no silent truncation left');
  assert.equal(new Set(service.getSnapshot().items.map(p=>p.id)).size,1100,'no duplicates across pages');
  // Follow the list while one preparation is still running, then finish it server-side: the poll
  // re-reads it by id and stops, with no full reload.
  service.start();
  await new Promise(r=>setTimeout(r,40));
  s.set([item(0,'ready'),...rows.slice(1)]);
  await new Promise(r=>setTimeout(r,80));
  assert.equal(service.getSnapshot().items.find(p=>p.id==='prep-0')?.state,'ready','the finished preparation settled via the poll');
  const pages=s.calls.filter(c=>c.path.startsWith('/v1/downloads/preparations?'));
  assert.ok(pages.length>0);
  const unfiltered=pages.filter(c=>!c.path.includes('state=')&&!c.path.includes('ids='));
  assert.equal(unfiltered.length,12,'only the refresh/loadMore reads are unfiltered pages (1 + 10 + start refresh); every poll reads active states');
  const singles=s.calls.filter(c=>c.path.includes('ids='));
  assert.ok(singles.length>=1&&singles.length<=3,'the transition took only single reads: '+singles.length);
  const settled=pages.length;
  await new Promise(r=>setTimeout(r,60));
  assert.equal(s.calls.filter(c=>c.path.startsWith('/v1/downloads/preparations?')).length,settled,'polling stopped once nothing was active');
 }finally{service.dispose();}
});

test('a whole-container request is tracked from capture to completion',async()=>{
 const view=(over:Record<string,unknown>={})=>({requestId:'req-1',total:0,totalKnown:false,state:'capturing',ready:0,preparing:0,queued:0,failed:0,paused:0,items:[],nextCursor:'',...over});
 let reads=0;
 const api={request:async<T,>(path:string,method='GET',body?:any):Promise<T>=>{
  if(path==='/v1/downloads/requests'&&method==='POST'){
   assert.deepEqual(body,{operationId:'op-1',target:{kind:'season',id:'s-1'},deviceId:'dev-1',quality:'original',policy:{episodes:'next',keepNext:3}});
   return view() as T;
  }
  if(path.startsWith('/v1/downloads/requests/req-1')){
   reads++;
   return (reads<2?view():view({state:'complete',total:2,totalKnown:true,ready:2})) as T;
  }
  if(path.startsWith('/v1/downloads/preparations?'))return {items:[],nextCursor:''} as T;
  if(path==='/v1/downloads/usage')return usageDoc as T;
  throw new Error('unexpected '+method+' '+path);}};
 const service=new DownloadsService(api,()=>'op-1',15);
 try{
  const created=await service.requestContainer({target:{kind:'season',id:'s-1',title:'Season 1'},deviceId:'dev-1',quality:'original',policy:{episodes:'next',keepNext:3}});
  assert.equal(created.requestId,'req-1');
  assert.equal(service.getSnapshot().requests.length,1);
  assert.equal(service.getSnapshot().requests[0].title,'Season 1');
  assert.equal(service.getSnapshot().requests[0].request.totalKnown,false);
  await new Promise(r=>setTimeout(r,80));
  assert.equal(service.getSnapshot().requests[0].request.state,'complete');
  assert.equal(service.getSnapshot().requests[0].request.ready,2);
  await assert.rejects(service.requestContainer({target:{kind:'episode' as any,id:'e'},deviceId:'dev-1',quality:'original',policy:{episodes:'all'}}));
 }finally{service.dispose();}
});

test('an ordinary list refresh does not stop following a whole-container request',async()=>{
 const view=(over:Record<string,unknown>={})=>({requestId:'req-1',total:0,totalKnown:false,state:'capturing',ready:0,preparing:0,queued:0,failed:0,paused:0,items:[],nextCursor:'',...over});
 let reads=0;
 const api={request:async<T,>(path:string,method='GET'):Promise<T>=>{
  if(path==='/v1/downloads/requests'&&method==='POST')return view() as T;
  if(path.startsWith('/v1/downloads/requests/req-1')){reads++;return (reads<4?view({state:'preparing',total:2,totalKnown:true}):view({state:'complete',total:2,totalKnown:true,ready:2})) as T;}
  if(path.startsWith('/v1/downloads/preparations?'))return {items:[],nextCursor:''} as T;
  if(path==='/v1/downloads/usage')return usageDoc as T;
  throw new Error('unexpected '+method+' '+path);}};
 const service=new DownloadsService(api,()=>'op-1',15);
 try{
  await service.requestContainer({target:{kind:'season',id:'s-1',title:'Season 1'},deviceId:'dev-1',quality:'original',policy:{episodes:'all'}});
  // Opening the Downloads screen refreshes the list (a new generation).
  await service.refresh();
  await service.refresh();
  await new Promise(r=>setTimeout(r,150));
  assert.equal(service.getSnapshot().requests[0].request.state,'complete','the request kept being followed to completion');
 }finally{service.dispose();}
});

test('a list from a newer server shows every download this app can read, and acts on them exactly',async()=>{
 const s=server([preparation({id:'kept',shelf:'new',quality:'4320p',origin:'schedule',actions:['remove','archive']}),preparation({id:'newer',state:'verifying'}),preparation({id:'unhashed',artifact:{kind:'prepared',ref:'version',sha256:'',bytes:4096,estimated:false,container:'mp4',contentType:'video/mp4',fileName:'prep.mp4'}}),running()]),service=new DownloadsService(s.api,()=>'op-9',10000);
 await service.refresh();
 const state=service.getSnapshot();
 assert.equal(state.phase,'ready');assert.equal(state.error,undefined);
 assert.deepEqual(state.items.map(p=>p.id),['kept','busy']);
 assert.deepEqual([...state.items[0].actions],['remove']);
 await service.act(state.items[1],'pause');
 assert.deepEqual(s.calls.find(c=>c.path.endsWith('/actions'))!.body,{operationId:'op-9',action:'pause',expectedRevision:3});
 service.dispose();
});

test('large queued lists use one bounded sparse page and rotate older claims',async()=>{
 const rows=Array.from({length:350},(_,i)=>({...running(),id:'queued-'+i,itemId:'item-'+i,state:'queued'}));
 const s=server(rows),service=new DownloadsService(s.api,()=>'op',10000);
 try{
  await service.refresh();await service.loadMore();await service.loadMore();await service.loadMore();
  assert.equal(service.getSnapshot().items.length,350);
  let before=s.calls.length;
  await (service as any).poll();
  const first=s.calls.slice(before);
  assert.equal(first.length,3,'two active pages and one sparse page');
  assert.ok(first.every(c=>c.path.includes('?')),'no individual requests');
  const firstIds=new URL(first.find(c=>c.path.includes('ids='))!.path,'https://server.test').searchParams.get('ids')!.split(',');
  assert.equal(firstIds.length,200);
  const changed=rows[340];
  s.set(rows.map(p=>p.id===changed.id?preparation({id:p.id,itemId:p.itemId}):p));
  before=s.calls.length;
  await (service as any).poll();
  const second=s.calls.slice(before);
  assert.equal(second.length,3);
  const secondIds=new URL(second.find(c=>c.path.includes('ids='))!.path,'https://server.test').searchParams.get('ids')!.split(',');
  assert.ok(secondIds.some(id=>!firstIds.includes(id)),'older claims are not starved');
  assert.equal(service.getSnapshot().items.find(p=>p.id===changed.id)?.state,'ready');
  assert.equal(service.getSnapshot().items.length,350,'paging content is preserved');
 }finally{service.dispose();}
});

test('a quiet read brings in what changed elsewhere and keeps the rows that did not',async()=>{
 const first=preparation({id:'one'}),second=preparation({id:'two',itemId:'item2'});
 const s=server([first,second]),service=new DownloadsService(s.api,()=>'op',10);
 try{
  await service.refresh();
  const before=service.getSnapshot().items;
  // Nothing moved: the list is the same list (no re-render), and nothing is said.
  await service.revalidate();
  assert.equal(service.getSnapshot().items,before);
  // Asked for on another device, and one let go: both show without the page being reloaded.
  s.set([preparation({id:'new',itemId:'item3'}),first]);
  await service.revalidate();
  const after=service.getSnapshot().items;
  assert.deepEqual(after.map(p=>p.id),['new','one']);
  assert.equal(after[1],before[0],'an unchanged row is the same row');
  assert.equal(service.getSnapshot().error,undefined);
 }finally{service.dispose();}
});

test('a quiet read that fails leaves the list as it is and says nothing',async()=>{
 const s=server([preparation()]),service=new DownloadsService(s.api,()=>'op',10);
 try{
  await service.refresh();
  const before=service.getSnapshot();
  s.failWith(new TypeError('Failed to fetch'));
  await assert.rejects(service.revalidate());
  assert.equal(service.getSnapshot().items,before.items);
  assert.equal(service.getSnapshot().error,undefined,'no error reaches the page');
  assert.equal(service.getSnapshot().phase,'ready');
 }finally{service.dispose();}
});

test('a quiet read never runs before the first load or on a server without downloads',async()=>{
 const s=server([preparation()]),service=new DownloadsService(s.api,()=>'op',10);
 try{await service.revalidate();assert.equal(s.calls.length,0);}finally{service.dispose();}
});


test('container options keep server aggregate sizes and reject a different target',async()=>{
 const calls: unknown[]=[];let wrong=false;
 const service=new DownloadsService({async request<T>(path:string,method='GET',body?:unknown):Promise<T>{calls.push({path,method,body});return {target:{kind:'season',id:wrong?'other':'season'},total:43,options:[{quality:'original',label:'Original',sameAsOriginal:false,estimatedBytes:43000,estimated:false,available:true,unavailableCount:0,unknownSizeCount:0}]} as T;}});
 try {
  const result=await service.containerOptions({kind:'season',id:'season'},'device',{episodes:'all'});
  assert.equal(result.total,43);assert.equal(result.options[0]?.estimatedBytes,43000);
  assert.deepEqual(calls,[{path:'/v1/downloads/options',method:'POST',body:{target:{kind:'season',id:'season'},deviceId:'device',policy:{episodes:'all'}}}]);
  wrong=true;await assert.rejects(service.containerOptions({kind:'season',id:'season'},'device',{episodes:'all'}));
 } finally {service.dispose();}
});

test('quality defaults r25: item omission sends Original and never rewrites an explicit request',async()=>{
 const s=server([]),sent:any[]=[];
 const api={request:async<T>(path:string,method='GET',body?:any):Promise<T>=>{
  if(path==='/v1/downloads/preparations'&&method==='POST'){sent.push(body);return {batchId:'b',accepted:1,items:[preparation({quality:body.quality})],rejected:[],duplicate:false} as T;}
  return s.api.request<T>(path,method,body);
 }};
 const service=new DownloadsService(api,()=>'op');
 try {
  await service.request({mediaId:'item'});
  assert.equal(sent.at(-1).quality,'original');
  for(const quality of ['original','automatic','720p-high']){await service.request({mediaId:'item'},quality);assert.equal(sent.at(-1).quality,quality);}
  const n=sent.length;
  for(const quality of ['',null,'auto','1080p','bogus'])await assert.rejects(service.request({mediaId:'item'},quality as any));
  assert.equal(sent.length,n);
 }finally{service.dispose();}
});

test('a generic empty-list 503 or timeout is a retryable failure, never feature absence',async()=>{
 for(const error of [Object.assign(new Error('busy'),{status:503,code:'server_busy'}),Object.assign(new Error('timeout'),{status:504,code:'request_timeout'})]){
  const s=server([]),service=new DownloadsService(s.api,()=>'op',10);
  try{
   s.failWith(error);await service.refresh();
   const state=service.getSnapshot();
   assert.equal(state.unavailable,false,'a transient failure is not downloads-off');
   assert.equal(state.phase,'error');
   assert.ok(state.error);assert.strictEqual(state.failure,error);
   s.failWith(undefined);await service.refresh();
   assert.equal(service.getSnapshot().phase,'ready');
   assert.equal(service.getSnapshot().unavailable,false);
  }finally{service.dispose();}
 }
});

test('a nonempty list survives a generic 503 and keeps its Retry',async()=>{
 const s=server([preparation()]),service=new DownloadsService(s.api,()=>'op',10);
 try{
  await service.refresh();
  s.failWith(Object.assign(new Error('busy'),{status:503,code:'server_busy'}));await service.refresh();
  assert.equal(service.getSnapshot().items.length,1);
  assert.equal(service.getSnapshot().phase,'ready');
  assert.equal(service.getSnapshot().unavailable,false);
  assert.ok(service.getSnapshot().error);
 }finally{service.dispose();}
});

test('the canonical downloads_unavailable code and unsupported 404 still mark feature absence',async()=>{
 for(const error of [Object.assign(new Error('off'),{status:503,code:'downloads_unavailable'}),Object.assign(new Error('missing'),{status:404})]){
  const s=server([]),service=new DownloadsService(s.api,()=>'op',10);
  try{
   s.failWith(error);await service.refresh();
   assert.equal(service.getSnapshot().unavailable,true);
   assert.equal(service.getSnapshot().error,undefined);
  }finally{service.dispose();}
 }
});

test('a started service retries an empty generic 503 through its bounded schedule',async()=>{
 const s=server([]),service=new DownloadsService(s.api,()=>'op',10);
 try{
  s.failWith(Object.assign(new Error('busy'),{status:503,code:'server_busy'}));
  service.start();
  await new Promise(r=>setTimeout(r,80));
  s.failWith(undefined);s.set([preparation()]);
  await new Promise(r=>setTimeout(r,120));
  assert.equal(service.getSnapshot().phase,'ready');
  assert.equal(service.getSnapshot().items.length,1,'the scheduled retry reached the recovered page');
 }finally{service.dispose();}
});

test('discovery follows every page of a device\'s requests, within its bound',async()=>{
 const summary=(id:string)=>({requestId:id,state:'preparing',target:{kind:'season',id:'s-'+id},title:id,quality:'original',createdAt:'2026-09-16T10:00:00Z'});
 const view=(id:string)=>({requestId:id,total:1,totalKnown:true,state:'preparing',ready:0,preparing:1,queued:0,failed:0,paused:0,items:[],nextCursor:''});
 const lists:string[]=[];
 const api={request:async<T,>(path:string):Promise<T>=>{
  if(path.startsWith('/v1/downloads/requests?')){
   lists.push(path);const cursor=new URL('http://x'+path).searchParams.get('cursor');
   return (cursor===null?{requests:[summary('a')],nextCursor:'2.a'}:cursor==='2.a'?{requests:[summary('b')],nextCursor:'1.b'}:{requests:[summary('c')]}) as T;
  }
  if(path.startsWith('/v1/downloads/requests/'))return view(path.split('?')[0].split('/').at(-1)!) as T;
  throw new Error('unexpected '+path);}};
 const service=new DownloadsService(api,()=>'op',10000);
 try{
  service.followDevice('dev-1');
  await service.refreshRequests();
  assert.equal(lists.length,3);
  assert.deepEqual(service.getSnapshot().requests.map(r=>r.request.requestId).sort(),['a','b','c']);
 }finally{service.dispose();}
});
