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

test('the viewer\'s inboxes share one read of a view, and a view opened again shows at once with no request',async()=>{
 const s=server(),{queries}=cache(s.api);
 const shell=new InboxService(s.api,()=>'op',{queries});
 await shell.load('profile','unread');await tick();
 assert.equal(inboxReads(s.reads),1);
 assert.equal(unreadReads(s.reads),1);
 assert.ok(queries.client.getQueryData(queryKeys.notificationsInbox('profile','unread')));

 // The bell opens its own view of the same inbox: drawn at once, nothing read.
 const bell=new InboxService(s.api,()=>'op',{queries});
 const phases:string[]=[];bell.subscribe(()=>phases.push(bell.getSnapshot().phase));
 void bell.load('profile','unread');
 assert.equal(bell.getSnapshot().phase,'ready','shown in the same turn');
 assert.deepEqual(bell.getSnapshot().items.map(n=>n.id),['n-1','n-2']);
 assert.ok(!phases.includes('loading'),'no loading state for a view the cache holds');
 await tick();
 assert.equal(inboxReads(s.reads),1,'no second inbox read');
 assert.equal(unreadReads(s.reads),1,'nor a second count');
 assert.equal(bell.getSnapshot().unread,2);

 // Another view is its own document.
 await bell.load('profile','archived');await tick();
 assert.equal(inboxReads(s.reads),2);
 shell.dispose();bell.dispose();
});

test('a notification hint makes the cached views owed: the next opening reads again, and a hint on an open view reads the server',async()=>{
 const s=server(),{hub,queries}=cache(s.api);
 const first=new InboxService(s.api,()=>'op',{queries});
 await first.load('profile','unread');await tick();
 s.set({revision:9,items:[notice('n-3'),notice('n-1'),notice('n-2')]});
 hub.accept(changed(9) as never);
 // The open view follows the hint (the screen calls refresh), reading past the cache.
 first.refresh('9');await tick();
 assert.equal(inboxReads(s.reads),2);
 assert.deepEqual(first.getSnapshot().items.map(n=>n.id),['n-3','n-1','n-2']);
 assert.equal(first.getSnapshot().unread,3,'its count is read again with it');
 // A view opened after the hint gets that answer, with nothing more read.
 const later=new InboxService(s.api,()=>'op',{queries});
 void later.load('profile','unread');await tick();
 assert.equal(later.getSnapshot().revision,9);
 assert.equal(inboxReads(s.reads),2);
 // A view the hint moved that nobody had open since is read when next opened.
 s.set({revision:10});
 hub.accept(changed(10) as never);
 const again=new InboxService(s.api,()=>'op',{queries});
 await again.load('profile','unread');await tick();
 assert.equal(inboxReads(s.reads),3);
 assert.equal(again.getSnapshot().revision,10);
 first.dispose();later.dispose();again.dispose();
});

test('a change made on this device reads every cached notification document again when next asked for',async()=>{
 const s=server(),{queries}=cache(s.api);
 const page=new InboxService(s.api,()=>'op',{queries});
 await page.load('profile','unread');await tick();
 const readsBefore=inboxReads(s.reads);
 s.set({items:[{...notice('n-1'),read:true,readAt:at(3)},notice('n-2')]});
 await page.apply('read',['n-1']);await tick();
 assert.equal(s.posts.length,1);
 assert.equal(page.getSnapshot().unread,1,'the badge follows the change');
 // The bell opened after the change never shows the page cached before it.
 const bell=new InboxService(s.api,()=>'op',{queries});
 await bell.load('profile','unread');await tick();
 assert.equal(inboxReads(s.reads),readsBefore+1,'read again, not taken from the cache');
 assert.equal(bell.getSnapshot().revision,8);
 page.dispose();bell.dispose();
});

test('a count asked for fresh is read from the server even while a cached round is out; one asked for from the cache is not',async()=>{
 const s=server(),{queries}=cache(s.api);
 const service=new InboxService(s.api,()=>'op',{queries});
 await service.load('profile','unread');await tick();
 const before=unreadReads(s.reads);
 await service.refreshUnread(false);
 assert.equal(unreadReads(s.reads),before,'the cached count answers');
 await service.refreshUnread();
 assert.equal(unreadReads(s.reads),before+1,'a fresh count reads the server');
 const cached=service.refreshUnread(false),fresh=service.refreshUnread();
 await cached;await fresh;await tick();
 assert.equal(unreadReads(s.reads),before+2,'the fresh ask is not answered by the cached round');
 service.dispose();
});

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

test('the screen that started a shared read closes, and the other inbox waiting on it still gets the view',async()=>{
 const s=server();let release!:()=>void;const open=new Promise<void>(r=>{release=r;});
 const held={request:async<T,>(path:string,method?:string):Promise<T>=>{if(path.startsWith('/v1/notifications/inbox'))await open;return s.api.request<T>(path,method);}};
 const {queries}=cache(held);
 const screenA=screenApi(held),screenB=screenApi(held);
 const first=new InboxService(screenA.api,()=>'op',{queries}),second=new InboxService(screenB.api,()=>'op',{queries});
 const a=first.load('profile','unread');await tick();
 const b=second.load('profile','unread');await tick();
 first.dispose();screenA.close();
 release();await Promise.allSettled([a,b]);await tick();
 assert.equal(second.getSnapshot().phase,'ready','the second inbox is answered');
 assert.deepEqual(second.getSnapshot().items.map(n=>n.id),['n-1','n-2']);
 assert.equal(inboxReads(s.reads),1,'one read, shared');
 second.dispose();
});

test('a cached notification answer without a well-formed envelope, or for another audience, is refused and kept nowhere',async()=>{
 const doc=(audience='profile')=>({revision:1,audience,audiences:['profile'],state:'unread',counts:counts(0),items:[],nextCursor:'',observedAt:at(2),retentionDays:180});
 for(const answer of [
  {data:doc()},
  {scope:{serverId:'server'},data:doc()},
  {scope:{serverId:'server',viewerFence:'not-a-fence'},data:doc()},
  {scope:{viewerFence:'a'.repeat(64)},data:doc()},
  {scope,data:doc('account-admin')},
 ]){
  let retired=0;
  const api={request:async<T,>(path:string):Promise<T>=>(path.startsWith('/v1/notifications/unread-count')?{scope,data:{revision:1,audience:'profile',counts:counts(0),observedAt:at(2)}}:answer) as T};
  const {queries}=cache(api);
  const inbox=new InboxService(api,()=>'op',{queries,onScopeChanged:()=>{retired++;}});
  await inbox.load('profile','unread');await tick();
  assert.equal(inbox.getSnapshot().phase,'error',JSON.stringify(answer.scope));
  assert.equal(queries.client.getQueryData(queryKeys.notificationsInbox('profile','unread')),undefined,'nothing kept');
  assert.equal(retired,0,'an unreadable answer retires nothing');
  inbox.dispose();
 }
});

test('another server\'s answer to a shared notification read retires every screen waiting on it, and is kept nowhere',async()=>{
 const other={scope:{serverId:'elsewhere',viewerFence:'a'.repeat(64)},data:{revision:1,audience:'profile',audiences:['profile'],state:'unread',counts:counts(0),items:[],nextCursor:'',observedAt:at(2),retentionDays:180}};
 let release!:()=>void;const open=new Promise<void>(r=>{release=r;});
 const api={request:async<T,>(path:string):Promise<T>=>{if(path.startsWith('/v1/notifications/inbox'))await open;return (path.startsWith('/v1/notifications/unread-count')?{...other,data:{revision:1,audience:'profile',counts:counts(0),observedAt:at(2)}}:other) as T;}};
 const {queries}=cache(api);
 const retired:string[]=[];
 const first=new InboxService(api,()=>'op',{queries,onScopeChanged:()=>{retired.push('first');}}),second=new InboxService(api,()=>'op',{queries,onScopeChanged:()=>{retired.push('second');}});
 const a=first.load('profile','unread'),b=second.load('profile','unread');await tick();
 release();await Promise.allSettled([a,b]);await tick();
 assert.ok(retired.includes('first')&&retired.includes('second'),'both screens retire: '+retired.join(','));
 assert.equal(queries.client.getQueryData(queryKeys.notificationsInbox('profile','unread')),undefined);
 assert.equal(first.getSnapshot().items.length,0);assert.equal(second.getSnapshot().items.length,0);
 first.dispose();second.dispose();
});
