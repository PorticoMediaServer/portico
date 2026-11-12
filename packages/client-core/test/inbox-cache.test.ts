import {test} from 'node:test';
import assert from 'node:assert/strict';
import {InboxService} from '../src/inbox.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../src/query/viewer-queries.ts';
import {queryKeys} from '../src/query/keys.ts';

// Client data step 6: notifications are read through the viewer's cache. Every inbox of the viewer
// (the shell's badge, the bell, the page, the admin audience) shares one read of each document; a
// view opened again shows at once with no request; a hint or a change made here reads it again.
const at=(ms:number)=>new Date(ms).toISOString();
const notice=(id:string,audience='profile')=>({
 id,audience,severity:'info',source:'downloads',category:'download.finished',
 title:'Download ready',body:'A title finished downloading.',arguments:{},actions:[],
 dedupeKey:'download:'+id,revision:7,createdAt:at(1),updatedAt:at(1),expiresAt:at(99),
 readAt:null,archivedAt:null,read:false,archived:false,cursor:id});
const counts=(unread:number)=>({unread,read:0,archived:0,total:unread});
/** The server's envelope around every notification document. */
const scope={serverId:'server',viewerFence:'a'.repeat(64)};

function server(){
 const reads:string[]=[],posts:string[]=[];
 let revision=7,items:any[]=[notice('n-1'),notice('n-2')];
 const api={request:<T,>(path:string,method='GET'):Promise<T>=>{
  if(method==='POST'){posts.push(path);return Promise.resolve({scope,data:{revision:++revision,audience:'profile',applied:1,receipts:[],counts:counts(items.length)}} as T);}
  reads.push(path);
  if(path.startsWith('/v1/notifications/unread-count'))return Promise.resolve({scope,data:{revision,audience:'profile',counts:counts(items.filter(n=>!n.read).length),observedAt:at(2)}} as T);
  const view=new URLSearchParams(path.slice(path.indexOf('?')+1)).get('state');
  return Promise.resolve({scope,data:{revision,audience:'profile',audiences:['profile'],state:view,counts:counts(items.length),items:view==='archived'?[]:items,nextCursor:'',observedAt:at(2),retentionDays:180}} as T);
 }};
 return {api,reads,posts,set:(next:{revision?:number;items?:any[]})=>{revision=next.revision??revision;items=next.items??items;}};
}
const tick=()=>new Promise(resolve=>setTimeout(resolve,5));
const inboxReads=(reads:string[])=>reads.filter(p=>p.startsWith('/v1/notifications/inbox')).length;
const unreadReads=(reads:string[])=>reads.filter(p=>p.startsWith('/v1/notifications/unread-count')).length;
const made:ViewerQueries[]=[];
test.afterEach(()=>{for(const q of made.splice(0))q.dispose();});
function cache(api:ReturnType<typeof server>['api']){
 const hub=new FreshnessHub({settleMs:0});
 const queries=createViewerQueries({scope:{serverId:'server',viewerId:'viewer'},hub,api,gcTimeMs:Infinity});
 made.push(queries);
 return {hub,queries};
}
const changed=(revision:number)=>({id:'e'+revision,type:'notification.changed',revision:String(revision)});





test('privacy: an inbox of an ended viewer\'s cache shows nothing and reads nothing more',async()=>{
 const s=server(),{queries}=cache(s.api);
 const service=new InboxService(s.api,()=>'op',{queries});
 queries.dispose();
 await service.load('profile','unread');await tick();
 assert.equal(service.getSnapshot().items.length,0);
 assert.notEqual(service.getSnapshot().phase,'ready');
 assert.equal(inboxReads(s.reads),0);
 service.dispose();
});

/** A screen's own requester: everything it sends ends when the screen closes. */
function screenApi(base:ReturnType<typeof server>['api']){
 const life=new AbortController();
 const api={request:<T,>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>=>new Promise<T>((resolve,reject)=>{
  const stop=()=>reject(Object.assign(new Error('closed'),{name:'AbortError'}));
  for(const s of [life.signal,signal])if(s){if(s.aborted)stop();else s.addEventListener('abort',stop,{once:true});}
  (base.request as any)(path,method,body,signal).then(resolve,reject);
 })};
 return {api,close:()=>life.abort()};
}



