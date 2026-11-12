import {test} from 'node:test';
import assert from 'node:assert/strict';
import {InboxService} from '../src/inbox.ts';
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
