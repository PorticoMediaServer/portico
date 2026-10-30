import test from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService} from '../src/downloads-service.ts';

const request=(id:string)=>({requestId:id,total:1,totalKnown:true,state:'preparing',ready:0,preparing:1,queued:0,failed:0,paused:0,items:[],nextCursor:''});
const preparation=(id:string)=>({
 id,itemId:'item-'+id,libraryId:'library',profileId:'profile',quality:'original',origin:'item',
 title:{kind:'movie',title:'Film',onServer:true},batchId:'batch',state:'ready',reason:'',
 artifact:{kind:'prepared',ref:'version',sha256:'a'.repeat(64),bytes:1,estimated:false,container:'mp4',contentType:'video/mp4',fileName:'film.mp4'},
 progress:{bytesDone:1,bytesTotal:1,bytesTotalEstimated:false,percent:100,etaMs:null},
 actions:['remove'],revision:1,createdAt:'2026-09-16T10:00:00Z',updatedAt:'2026-09-16T10:05:00Z',readyAt:'2026-09-16T10:05:00Z',expiresAt:'2026-10-16T10:05:00Z',
});

test('starting before the device is supplied still discovers that device through the shared refresh',async()=>{
 const listed:string[]=[];
 const service=new DownloadsService({async request<T>(path:string):Promise<T>{
  if(path.startsWith('/v1/downloads/requests?')){
   listed.push(new URL(path,'https://server.test').searchParams.get('deviceId')!);
   return {requests:[{requestId:'restored',state:'complete',target:{kind:'season',id:'season'},title:'Season',quality:'original',createdAt:'2026-09-16T10:00:00Z'}]} as T;
  }
  if(path.startsWith('/v1/downloads/requests/restored'))return {...request('restored'),state:'complete',preparing:0,ready:1} as T;
  if(path.startsWith('/v1/downloads/preparations?'))return {items:[],nextCursor:''} as T;
  if(path==='/v1/downloads/usage')return {profileId:'profile',profileBytes:0,profileCount:0,serverBytes:0,serverCount:0,distinctArtifactBytes:0,maxPreparedBytes:0,remainingBytes:null,retentionDays:7,profiles:[]} as T;
  throw new Error('Unexpected request '+path);
 }});
 try{
  service.start();service.followDevice('device');
  await service.refreshRequests();
  assert.deepEqual(listed,['device']);
  assert.equal(service.getSnapshot().requests[0]?.request.requestId,'restored');
  assert.equal(service.getSnapshot().requests[0]?.request.ready,1);
 }finally{service.dispose();}
});

test('container recovery fairly polls more than twenty followed requests with bounded concurrency',async()=>{
 let created=0,active=0,peak=0;
 const reads:string[]=[],members:string[]=[];
 const service=new DownloadsService({async request<T>(path:string,method='GET'):Promise<T>{
  if(method==='POST')return request('request-'+created++) as T;
  const id=path.split('?')[0].split('/').at(-1)!;
  // A poll reads the counts alone; the member page only when a request's progress moved.
  if(path.includes('limit=0'))reads.push(id);else members.push(id);
  active++;peak=Math.max(peak,active);
  await new Promise<void>(resolve=>setImmediate(resolve));
  active--;return request(id) as T;
 }},()=>'operation',10000);
 try{
  for(let i=0;i<25;i++)await service.requestContainer({target:{kind:'season',id:'season-'+i},deviceId:'device',policy:{episodes:'all'}});
  await Promise.all([service.refreshRequests(),service.refreshRequests()]);
  assert.equal(reads.length,20,'concurrent refresh callers share one bounded turn');
  assert.equal(peak,2,'only two request reads run concurrently');
  await service.refreshRequests();
  assert.equal(reads.length,40);
  assert.equal(new Set(reads).size,25,'requests beyond the first twenty get a turn');
  assert.deepEqual(reads.slice(20,25),Array.from({length:5},(_,i)=>'request-'+(20+i)));
  assert.deepEqual([...members].sort(),[...new Set(reads)].sort(),'each member page is read once while nothing moves');
 }finally{service.dispose();}
});

test('disposing the shared request poller aborts in-flight reads and starts no queued work',async()=>{
 let created=0,reads=0,aborts=0;
 const service=new DownloadsService({async request<T>(_path:string,method='GET',_body?:unknown,signal?:AbortSignal):Promise<T>{
  if(method==='POST')return request('request-'+created++) as T;
  reads++;
  return await new Promise<T>((_resolve,reject)=>signal!.addEventListener('abort',()=>{aborts++;reject(Object.assign(new Error('aborted'),{name:'AbortError'}));},{once:true}));
 }},()=>'operation',10000);
 for(let i=0;i<5;i++)await service.requestContainer({target:{kind:'season',id:'season-'+i},deviceId:'device',policy:{episodes:'all'}});
 const poll=service.refreshRequests();
 const dispatched=reads;
 service.dispose();
 await poll;
 assert.equal(dispatched,2);
 assert.equal(aborts,2);
 assert.equal(reads,2);
});

test('exact preparation recovery reaches wanted items beyond the first page without changing it',async()=>{
 const calls:string[]=[];
 const service=new DownloadsService({async request<T>(path:string):Promise<T>{
  calls.push(path);
  const ids=new URL(path,'https://server.test').searchParams.get('ids')!.split(',');
  return {items:ids.filter(id=>id!=='removed').map(preparation),nextCursor:''} as T;
 }});
 try{
  const before=service.getSnapshot();
  const result=await service.lookupPreparations(['older-201','older-350','older-201','removed']);
  assert.deepEqual(result.map(p=>p.id),['older-201','older-350']);
  assert.equal(service.getSnapshot(),before,'recovery does not grow or replace the screen list');
  assert.equal(calls.length,1);
  const query=new URL(calls[0],'https://server.test').searchParams;
  assert.equal(query.get('limit'),'200');
  assert.equal(query.get('ids'),'older-201,older-350,removed');
  assert.deepEqual(await service.lookupPreparations([]),[]);
  await assert.rejects(service.lookupPreparations(Array.from({length:201},(_,i)=>'id-'+i)));
  await assert.rejects(service.lookupPreparations(['bad,id']));
  assert.equal(calls.length,1,'invalid or empty batches never dispatch');
 }finally{service.dispose();}
});

test('sparse recovery refuses unrelated preparations and cannot outlive its caller or service',async()=>{
 const service=new DownloadsService({async request<T>():Promise<T>{return {items:[preparation('other')],nextCursor:''} as T;}});
 await assert.rejects(service.lookupPreparations(['wanted']));
 const caller=new AbortController();caller.abort();
 await assert.rejects(service.lookupPreparations(['wanted'],caller.signal),{name:'AbortError'});
 service.dispose();
 await assert.rejects(service.lookupPreparations(['wanted']),{name:'AbortError'});

 const pending=new DownloadsService({async request<T>(_path:string,_method?:string,_body?:unknown,signal?:AbortSignal):Promise<T>{
  return await new Promise<T>((_resolve,reject)=>signal!.addEventListener('abort',()=>reject(Object.assign(new Error('aborted'),{name:'AbortError'})),{once:true}));
 }});
 const lookup=pending.lookupPreparations(['wanted']);
 pending.dispose();
 await assert.rejects(lookup,{name:'AbortError'});
});

test('a failed request discovery keeps the list error clear and is read again later',async()=>{
 let lists=0;
 const service=new DownloadsService({async request<T>(path:string):Promise<T>{
  if(path.startsWith('/v1/downloads/requests?')){
   if(lists++===0)throw Object.assign(new Error('busy'),{status:503});
   return {requests:[{requestId:'found',state:'complete',target:{kind:'season',id:'season'},title:'Season',quality:'original',createdAt:'2026-09-16T10:00:00Z'}]} as T;
  }
  if(path.startsWith('/v1/downloads/requests/found'))return {...request('found'),state:'complete',preparing:0,ready:1} as T;
  if(path.startsWith('/v1/downloads/preparations?'))return {items:[],nextCursor:''} as T;
  if(path==='/v1/downloads/usage')return {profileId:'profile',profileBytes:0,profileCount:0,serverBytes:0,serverCount:0,distinctArtifactBytes:0,maxPreparedBytes:0,remainingBytes:null,retentionDays:7,profiles:[]} as T;
  throw new Error('Unexpected request '+path);
 }},()=>'operation',5);
 try{
  service.followDevice('device');service.start();
  await service.refreshRequests();
  assert.equal(lists,1);
  assert.equal(service.getSnapshot().failure,undefined,'discovery is not the list failing');
  for(let i=0;i<50&&!service.getSnapshot().requests.length;i++)await new Promise(resolve=>setTimeout(resolve,5));
  assert.equal(lists,2,'the failed discovery is retried once by itself');
  assert.equal(service.getSnapshot().requests[0]?.request.requestId,'found');
 }finally{service.dispose();}
});
