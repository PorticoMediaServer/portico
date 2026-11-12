import {test} from 'node:test';
import assert from 'node:assert/strict';
import {InboxService,FeedbackService} from '../src/inbox.ts';
import {setInstallationIdentity} from '../src/installation.ts';

// Instants travel as RFC 3339 text; the parsers hand the app epoch milliseconds.
const at=(ms:number)=>new Date(ms).toISOString();

const notification=(over:Record<string,unknown>={})=>({
 id:'n-1',audience:'profile',severity:'info',source:'downloads',category:'download.finished',
 title:'Download ready',body:'A title finished downloading.',arguments:{},actions:[],
 dedupeKey:'download:dl-1',revision:7,createdAt:at(1),updatedAt:at(1),expiresAt:at(99),
 readAt:null,archivedAt:null,read:false,archived:false,cursor:'3',...over});
const counts=(unread:number)=>({unread,read:0,archived:0,total:unread});
const inbox=(items:unknown[],over:Record<string,unknown>={})=>({scope:{serverId:'s',viewerFence:'f'},data:{
 revision:7,audience:'profile',audiences:['profile','account-admin'],state:'unread',counts:counts(items.length),items,nextCursor:'',observedAt:at(2),retentionDays:180,...over}});
const summary=(unread:number)=>({data:{revision:7,audience:'profile',counts:counts(unread),observedAt:at(2)}});

function server(){
 const calls:{path:string;method:string;body?:any}[]=[];let fail:Error|undefined;let items=[notification(),notification({id:'n-2',cursor:'4'})];
 const api={request:async<T,>(path:string,method='GET',body?:unknown):Promise<T>=>{
  calls.push({path,method,body});if(fail)throw fail;
  if(path.startsWith('/v1/notifications/unread-count'))return summary(path.includes('account-admin')?3:items.length) as T;
  if(path.startsWith('/v1/notifications/inbox/actions'))return {data:{revision:8,audience:'profile',applied:1,receipts:[{action:(body as any).operations[0].action,outcome:'applied'}],counts:counts(1)}} as T;
  if(path.startsWith('/v1/notifications/inbox'))return inbox(items) as T;
  throw new Error('unexpected '+path);
 }};
 return {api,calls,failWith:(e?:Error)=>{fail=e;},set:(next:any[])=>{items=next;}};
}

/** A stub whose POSTs and first-page reads can be held open, failed once, or
 * answered at once, so a dispatch's lifetime and the admitted page are visible. */
function flightServer(){
 const calls:{path:string;method:string;body?:any;signal?:AbortSignal}[]=[];
 const posts:{resolve:(value:any)=>void;reject:(reason:any)=>void;signal?:AbortSignal}[]=[];
 const reads:{resolve:(value:any)=>void;reject:(reason:any)=>void;signal?:AbortSignal}[]=[];
 let immediate:Error|undefined,defer=false,holdRead=false,revision=7,nextCursor='';
 let items:any[]=[notification(),notification({id:'n-2',cursor:'4'})];
 const page=(atRevision=revision,list=items)=>({scope:{serverId:'s',viewerFence:'f'},data:{revision:atRevision,audience:'profile',
  audiences:['profile'],state:'unread',counts:counts(list.length),items:list,nextCursor,observedAt:at(2),retentionDays:180}});
 const api={request:<T,>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>=>{
  calls.push({path,method,body,signal});
  if(path.startsWith('/v1/notifications/unread-count'))return Promise.resolve({data:{revision,audience:'profile',counts:counts(items.length),observedAt:at(2)}} as T);
  if(path.startsWith('/v1/notifications/inbox/actions')){
   if(immediate){const failure=immediate;immediate=undefined;return Promise.reject(failure);}
   if(defer)return new Promise<T>((resolve,reject)=>posts.push({resolve,reject,signal}));
   return Promise.resolve({data:{revision:++revision,audience:'profile',applied:1,receipts:[],counts:counts(0)}} as T);
  }
  if(holdRead)return new Promise<T>((resolve,reject)=>reads.push({resolve,reject,signal}));
  return Promise.resolve(page() as T);
 }};
 return {api,calls,posts,reads,page,setImmediate:(e:Error)=>{immediate=e;},setDefer:(value:boolean)=>{defer=value;},
  setHoldRead:(value:boolean)=>{holdRead=value;},setRevision:(value:number)=>{revision=value;},setItems:(next:any[])=>{items=next;},
  setNextCursor:(value:string)=>{nextCursor=value;}};
}











test('a write needs an admitted page, and a closed service sends nothing',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');
 await service.apply('read',['n-1']);await service.readAll();
 assert.equal(s.calls.length,0,'no page has been read: no request at all');
 // The server's revision 0 means no records and is unfenced, so it cannot carry a write.
 s.setRevision(0);await service.load();
 await service.apply('read',['n-1']);
 assert.equal(s.calls.filter(c=>c.method==='POST').length,0,'revision 0 is not a fence');
 service.dispose();
 const before=s.calls.length;
 await service.apply('read',['n-1']);await service.readAll();await service.more();await service.load();
 assert.equal(s.calls.length,before,'a closed service sends nothing');
});










const capabilities={revision:'e1.0',canSubmit:true,kinds:[{id:'playback',label:'Playback',categories:[
 {id:'buffering',label:'Buffering',description:'It keeps pausing.',wantsPlaybackSession:true,wantsItem:true},
 {id:'other',label:'Something else',description:'Anything.',wantsPlaybackSession:false,wantsItem:false}]}],
 maxMessageLength:2000,minMessageLength:8,diagnosticsSupported:true,diagnosticsOptional:true,duplicateWindowHours:24,retentionDays:180,
 statuses:['open','in-progress','resolved','closed'],diagnosticsDecisions:['attached','unavailable','declined','not-requested'],
 perProfileHourlyLimit:10,reporterName:'Sam',reporterAuthority:'local'};
const report={id:'r-1',cursor:'1',revision:1,status:'open',kind:'playback',category:'buffering',message:'It keeps pausing every minute.',createdAt:at(1),updatedAt:at(1),expiresAt:at(9),
 reporter:{name:'Sam',authority:'local',role:'member',self:true},diagnostics:{decision:'attached',reference:'bundle-1'},duplicates:0,thread:[]};

test('feedback is prefilled from the player only where the category wants it, and a failed send loses nothing',async()=>{
 const bodies:any[]=[];let fail=true;
 const api={request:async<T,>(path:string,_m='GET',body?:unknown):Promise<T>=>{
  if(path==='/v1/feedback/capabilities')return {data:capabilities} as T;
  bodies.push(body);if(fail)throw new TypeError('Failed to fetch');return {data:{report,duplicate:false,created:true}} as T;}};
 let n=0;const service=new FeedbackService(api,()=>'op-'+(++n));await service.open();
 assert.equal(service.getSnapshot().phase,'ready');
 const draft={kind:'playback',category:'buffering',message:'It keeps pausing every minute.',itemId:'item-1',playbackSessionId:'play-1',attachDiagnostics:true};
  assert.equal(service.problem({...draft,message:'short'}),'Describe it in at least 8 characters.');
  assert.equal(await service.submit(draft),false);
 assert.equal(service.getSnapshot().phase,'ready','back to the form, draft intact');assert.match(service.getSnapshot().error??'',/can’t reach your server/);
 fail=false;assert.equal(await service.submit(draft),true);
 assert.equal(service.getSnapshot().phase,'sent');
 assert.equal(bodies[0].operationId,bodies[1].operationId,'a retry is the same report, never a second one');
 assert.equal(bodies[1].playbackSessionId,'play-1');assert.equal(bodies[1].itemId,'item-1');
 await service.open();await service.submit({...draft,category:'other'});
 assert.equal(bodies[2].playbackSessionId,'','a category that does not want the session does not get it');assert.equal(bodies[2].itemId,'');
});

test('SIM-08: a report names the sending app, captured once so a retry is the same request',async()=>{
 const bodies:any[]=[];let fail=true,online=true;
 const api={request:async<T,>(path:string,_m='GET',body?:unknown):Promise<T>=>{
  if(path==='/v1/feedback/capabilities')return {data:capabilities} as T;
  bodies.push(body);if(fail)throw new TypeError('Failed to fetch');return {data:{report,duplicate:false,created:true}} as T;}};
 setInstallationIdentity({installationId:'i'.repeat(32),name:'Kitchen',platform:'android',app:'Portico',appVersion:'1.4.2'});
 try{
  const service=new FeedbackService(api,undefined,()=>online);await service.open();
  const draft={kind:'playback',category:'other',message:'It keeps pausing every minute.',attachDiagnostics:false};
  assert.equal(await service.submit(draft),false);
  online=false;fail=false;assert.equal(await service.submit(draft),true);
  assert.deepEqual(bodies[0].client,{platform:'android',appVersion:'1.4.2',online:true});
  assert.deepEqual(bodies[1],bodies[0],'the retry repeats the captured facts');
  assert.equal(JSON.stringify(bodies[0].client).includes('Kitchen'),false,'no device name leaves');
  const unknown=new FeedbackService(api);await unknown.open();await unknown.submit(draft);
  assert.deepEqual(bodies[2].client,{platform:'android',appVersion:'1.4.2'},'an app that cannot tell sends no online state');
 }finally{setInstallationIdentity(undefined);}
});

test('a profile that may not send feedback is told why',async()=>{
  const api={request:async<T,>():Promise<T>=>({data:{...capabilities,canSubmit:false,submitBlockedReason:'Feedback is turned off for this profile.'}}) as T};
  const service=new FeedbackService(api);await service.open();
  assert.equal(service.getSnapshot().phase,'unavailable');assert.match(service.getSnapshot().error??'',/turned off/);
});

test('CD-46: feedback limits count Unicode code points, trimmed as sent',async()=>{
  const api={request:async<T,>():Promise<T>=>({data:capabilities}) as T};
  const service=new FeedbackService(api);await service.open();
  const draft={kind:'playback',category:'buffering',message:'',itemId:'',playbackSessionId:'',attachDiagnostics:false};
  // 2000 CJK or astral characters are accepted; 2001 are rejected.
  assert.equal(service.problem({...draft,message:'日'.repeat(2000)}),'');
  assert.equal(service.problem({...draft,message:'😀'.repeat(2000)}),'');
  assert.match(service.problem({...draft,message:'日'.repeat(2001)}),/under 2,000/);
  assert.match(service.problem({...draft,message:'😀'.repeat(2001)}),/under 2,000/);
  // 2000 astral characters are 4000 UTF-16 units: length alone must not refuse them.
  assert.equal('😀'.repeat(2000).length,4000);
  // Trimming and the minimum still apply to the text actually sent.
  assert.match(service.problem({...draft,message:'   short '}),/at least 8/);
  assert.equal(service.problem({...draft,message:' 12345678 '}),'');
});







test('a Mark all read held when the service closes is sent nowhere',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');
 await service.load();
 s.setHoldRead(true);
 const switching=service.load('profile','archived');
 await service.readAll();
 service.dispose();
 s.reads.splice(0).forEach(read=>read.resolve(s.page()));
 await switching.catch(()=>{});for(let i=0;i<10;i++)await Promise.resolve();
 assert.equal(s.calls.filter(c=>c.method==='POST').length,0);
});
