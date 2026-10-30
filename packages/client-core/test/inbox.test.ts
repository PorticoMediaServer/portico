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

test('the inbox loads a view, and the badge counts every audience the viewer has',async()=>{
 const s=server(),service=new InboxService(s.api,()=>'op');
 await service.load();await new Promise(r=>setTimeout(r,5));
 const state=service.getSnapshot();
 assert.equal(state.phase,'ready');assert.equal(state.items.length,2);assert.deepEqual(state.audiences,['profile','account-admin']);
 assert.equal(state.unread,5,'profile 2 + account-admin 3');
});

test('a failed refresh keeps what is on screen; only a first load with nothing is an error',async()=>{
 const s=server(),service=new InboxService(s.api,()=>'op');
 s.failWith(new Error('offline'));await service.load();
 assert.equal(service.getSnapshot().phase,'error');
 s.failWith();await service.load();assert.equal(service.getSnapshot().items.length,2);
 s.failWith(new TypeError('Failed to fetch'));await service.load();
 const state=service.getSnapshot();
 assert.equal(state.phase,'ready','the loaded messages stay');assert.equal(state.items.length,2);assert.match(state.error??'',/can’t reach your server/,'an offline refresh says so, from the catalogue');
});

test('marking read is immediate, leaves the unread view, and is put back if the server refuses',async()=>{
 const s=server(),service=new InboxService(s.api,()=>'op-1');await service.load();
 await service.apply('read',['n-1']);
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),['n-2']);
 const sent=s.calls.find(c=>c.path==='/v1/notifications/inbox/actions')!;
 assert.deepEqual(sent.body,{operationId:'op-1',expectedRevision:7,audience:'profile',operations:[{action:'read',ids:['n-1']}]});
 assert.equal(service.getSnapshot().revision,8);
 s.failWith(new Error('The server is busy.'));await service.apply('archive',['n-2']);
 const state=service.getSnapshot();
 assert.deepEqual(state.items.map(n=>n.id),['n-2'],'the row comes back');assert.equal(state.error,'That change couldn’t be saved.','catalogue text, never the server message');
});

test('a change is fenced by the page revision, and an unanswered attempt keeps its key',async()=>{
 const s=server();let issued=0;const service=new InboxService(s.api,()=>'op-'+(++issued));
 await service.load();
 await service.apply('read',['n-1']);
 const sent=s.calls.filter(c=>c.path==='/v1/notifications/inbox/actions');
 assert.deepEqual(sent[0].body,{operationId:'op-1',expectedRevision:7,audience:'profile',
  operations:[{action:'read',ids:['n-1']}]},'the revision the page on screen was read at fences it');
 // The answer to the next change never arrives, so its outcome is unknown.
 s.failWith(new TypeError('Failed to fetch'));
 await service.apply('read',['n-2']);
 s.failWith(undefined);
 // The same change again is that attempt, not a second one: same key, same body.
 await service.apply('read',['n-2']);
 const attempts=s.calls.filter(c=>c.path==='/v1/notifications/inbox/actions');
 assert.equal(attempts[1].body.operationId,'op-2');
 assert.deepEqual(attempts[2].body,attempts[1].body);
 assert.equal(attempts[2].body.expectedRevision,8,'the revision the page was read at after the first change');
 service.dispose();
});

test('a definite refusal finishes the intent, so asking again is a new attempt',async()=>{
 const s=server();let issued=0;const service=new InboxService(s.api,()=>'op-'+(++issued));
 await service.load();
 // A stated refusal means the server did not act: nothing is left to answer for.
 s.failWith(Object.assign(new Error('The notification changed.'),{status:409,code:'revision_mismatch'}));
 await service.apply('read',['n-1']);
 s.failWith(undefined);
 // The refused change retired the page with the read it could not finish; a
 // successful re-read admits the page again before another write.
 await service.load();
 await service.apply('read',['n-1']);
 const attempts=s.calls.filter(c=>c.path==='/v1/notifications/inbox/actions');
 assert.equal(attempts.length,2);
 assert.equal(attempts[0].body.operationId,'op-1');
 assert.equal(attempts[1].body.operationId,'op-2');
 assert.equal(service.getSnapshot().revision,8);
 service.dispose();
});

test('one write owns the page: a repeat or another change sends nothing while it is out',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
 s.setDefer(true);
 const first=service.apply('read',['n-1']);
 const repeat=service.apply('read',['n-1']);
 const other=service.apply('archive',['n-2']);
 assert.equal(s.posts.length,1,'one dispatch, claimed before the rows changed');
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),['n-2'],'only the claimed change is drawn');
 s.posts[0].resolve({data:{revision:8,audience:'profile',applied:1,receipts:[],counts:counts(1)}});
 await Promise.all([first,repeat,other]);
 assert.equal(s.calls.filter(c=>c.method==='POST').length,1);
 assert.equal(service.getSnapshot().revision,8);
 service.dispose();
});

test('an unanswered attempt keeps its intent; a different change is refused until it retries',async()=>{
 const s=flightServer();let issued=0;const service=new InboxService(s.api,()=>'op-'+(++issued));await service.load();
 s.setImmediate(Object.assign(new Error('The server is busy.'),{status:503}));
 await service.apply('read',['n-1']);                       // POST 1: outcome unknown
 assert.equal(s.calls.filter(c=>c.method==='POST').length,1);
 assert.ok(service.getSnapshot().error);
 const rows=service.getSnapshot().items.map(n=>n.id);
 await service.apply('archive',['n-2']);                    // a different action: refused, nothing sent
 assert.equal(s.calls.filter(c=>c.method==='POST').length,1);
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),rows);
 assert.ok(service.getSnapshot().error,'the refusal is shown, not silent');
 // The same change resumes that exact attempt: same body, same operation id.
 await service.apply('read',['n-1']);
 const sent=s.calls.filter(c=>c.method==='POST');
 assert.equal(sent.length,2);
 assert.deepEqual(sent[1].body,sent[0].body);
 assert.equal(sent[0].body.expectedRevision,7,'the revision the page was read at');
 assert.equal(service.getSnapshot().revision,8);
 service.dispose();
});

test('a late answer from an older dispatch settles nothing and cannot clear its successor',async()=>{
 const s=flightServer();let issued=0;const service=new InboxService(s.api,()=>'op-'+(++issued));await service.load();
 s.setDefer(true);
 const older=service.apply('read',['n-1']);
 assert.equal(s.posts.length,1);
 const olderSignal=s.posts[0].signal!;
 // A new page retires the write with its original signal.
 await service.load();
 assert.equal(olderSignal.aborted,true,'load retires the mutation lifetime');
 // A successor settles normally on the new page.
 s.setDefer(false);s.setRevision(10);
 await service.apply('archive',['n-2']);
 assert.equal(service.getSnapshot().revision,11);
 const rows=service.getSnapshot().items.map(n=>n.id);
 // The older answer arrives after the successor: it may not publish or clear it.
 s.posts[0].resolve({data:{revision:8,audience:'profile',applied:1,receipts:[],counts:counts(0)}});
 await older;
 assert.equal(service.getSnapshot().revision,11,'the late answer did not publish');
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),rows);
 assert.equal(service.getSnapshot().error,undefined);
 service.dispose();
});

test('dispose retires the write with its original signal',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
 s.setDefer(true);
 const writing=service.apply('read',['n-1']);
 const signal=s.posts[0].signal!;
 assert.equal(signal.aborted,false);
 service.dispose();
 assert.equal(signal.aborted,true);
 s.posts[0].resolve({data:{revision:8,audience:'profile',applied:1,receipts:[],counts:counts(0)}});
 await writing;
 assert.equal(service.getSnapshot().revision,7,'a retired write publishes nothing');
});

test('a revision conflict re-reads once, keeps its copy, and never resends',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
 s.setRevision(10);s.setItems([notification({id:'n-1',read:true})]);
 s.setImmediate(Object.assign(new Error('The document moved.'),{status:409,code:'revision_mismatch'}));
 await service.apply('read',['n-1']);
 assert.equal(s.calls.filter(c=>c.method==='POST').length,1,'the change is never resent');
 assert.equal(s.calls.filter(c=>c.method==='GET'&&c.path.startsWith('/v1/notifications/inbox?')).length,2,'the load and the one conflict read');
 const error=service.getSnapshot().error;
 assert.ok(error,'the copy stays visible');
 assert.equal(service.getSnapshot().revision,10,'the fresh page is adopted');
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),['n-1']);
 service.dispose();
});

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

test('a same-view load keeps the rows but admits no write until it succeeds',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
 const visible=service.getSnapshot().items.map(n=>n.id);
 s.setHoldRead(true);
 const reload=service.load();
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),visible,'the old rows stay on screen');
 await service.apply('read',['n-1']);
 assert.equal(s.calls.filter(c=>c.method==='POST').length,0,'no write borrows the read that has not landed');
 s.setHoldRead(false);
 s.reads[0].resolve(s.page(9));
 await reload;
 await service.apply('read',['n-1']);
 const sent=s.calls.filter(c=>c.method==='POST');
 assert.equal(sent.length,1);
 assert.equal(sent[0].body.expectedRevision,9,'the revision the page was actually read at');
 service.dispose();
});

test('a listener that reloads or closes on the optimistic rows stops the POST',async()=>{
 for(const retire of ['load','dispose'] as const){
  const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
  let fired=false;
  service.subscribe(()=>{
   const state=service.getSnapshot();
   if(fired||state.items.length!==1||state.error!==undefined)return;
   fired=true;
   if(retire==='load')void service.load();else service.dispose();
  });
  await service.apply('read',['n-1']);
  assert.equal(fired,true,retire);
  assert.equal(s.calls.filter(c=>c.method==='POST').length,0,retire);
  await new Promise(r=>setTimeout(r,0));
 }
});

test('more waits for an outbound write, and a pushed revision keeps its own counts',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
 s.setNextCursor('c2');await service.load();
 s.setDefer(true);
 const writing=service.apply('read',['n-1']);
 await service.more();
 assert.equal(s.calls.filter(c=>c.path.includes('cursor=c2')).length,0,'no appended page during a write');
 s.posts[0].resolve({data:{revision:8,audience:'profile',applied:1,receipts:[],counts:counts(1)}});
 await writing;
 assert.equal(service.getSnapshot().revision,8);
 // A pushed revision replaces the page while the next write is out: its late
 // counts must not downgrade the page that is now on screen.
 s.setDefer(true);
 const later=service.apply('archive',['n-2']);
 service.accept({revision:9,since:8,resync:false,items:[],counts:{unread:5,read:0,archived:0,total:5}});
 assert.equal(service.getSnapshot().revision,9);
 s.posts[1].resolve({data:{revision:8,audience:'profile',applied:1,receipts:[],counts:counts(0)}});
 await later;
 assert.equal(service.getSnapshot().revision,9,'the late counts do not replace the current page');
 assert.equal(service.getSnapshot().counts.unread,5);
 service.dispose();
});

test('a late failure after a pushed page leaves the successor untouched',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
 s.setDefer(true);
 const stale=service.apply('read',['n-1']);
 assert.equal(s.posts.length,1);
 service.accept({revision:9,since:8,resync:false,items:[],counts:{unread:5,read:0,archived:0,total:5}});
 assert.equal(s.posts[0].signal!.aborted,true,'the replaced page retires the stale write');
 // A successor write fails on the new page and leaves its own error and rows.
 s.setImmediate(Object.assign(new Error('The server is busy.'),{status:503}));
 await service.apply('archive',['n-2']);
 const successor=service.getSnapshot();
 assert.ok(successor.error);
 const rows=successor.items.map(n=>n.id);
 // The stale attempt's answer now fails too. It may not touch the successor,
 // not even its error area, and it does not become a reusable intent.
 s.posts[0].reject(new TypeError('Failed to fetch'));
 await stale;
 assert.equal(service.getSnapshot().revision,successor.revision);
 assert.deepEqual(service.getSnapshot().counts,successor.counts);
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),rows);
 assert.equal(service.getSnapshot().error,successor.error,'the successor keeps its own error');
 assert.equal(s.calls.filter(c=>c.method==='POST').length,2,'nothing new is sent');
 assert.equal(service.getSnapshot().revision,9);
 assert.equal(service.getSnapshot().counts.unread,5);
 service.dispose();
});

test('first-page and continuation publishes cannot dispatch a retired read',async()=>{
 for(const boundary of ['first','more'] as const){
  for(const retire of ['load','dispose'] as const){
   const s=flightServer();const service=new InboxService(s.api,()=>'op');
   if(boundary==='more'){s.setNextCursor('c2');await service.load();}
   const before=s.calls.length;let fired=false;let successor:Promise<void>|undefined;
   service.subscribe(()=>{
    if(fired||(boundary==='more'&&!service.getSnapshot().busy))return;
    fired=true;
    if(retire==='dispose')service.dispose();else successor=service.load('profile','all');
   });
   if(boundary==='first')await service.load();else await service.more();
   await successor;
   const reads=s.calls.slice(before).filter(c=>c.path.startsWith('/v1/notifications/inbox?'));
   assert.equal(fired,true);
   assert.equal(reads.length,retire==='load'?1:0,`${boundary}/${retire}`);
   if(retire==='load')assert.match(reads[0].path,/state=all/,'only the successor read dispatches');
   service.dispose();
  }
 }
});

test('a replaced continuation cannot append, publish its error, or clear successor busy',async()=>{
 for(const fail of [false,true]){
  const s=flightServer();const service=new InboxService(s.api,()=>'op');
  s.setNextCursor('c2');await service.load();s.setHoldRead(true);
  const old=service.more();const duplicate=service.more();
  assert.equal(s.reads.length,1,'continuations claim synchronously');
  await service.apply('read',['n-1']);
  assert.equal(s.calls.filter(c=>c.method==='POST').length,0,'no write against a page being appended');
  service.accept({revision:9,since:7,resync:false,items:[],counts:counts(5)});
  assert.equal(s.reads[0].signal!.aborted,true);
  const successor=service.more();assert.equal(s.reads.length,2);
  if(fail)s.reads[0].reject(new Error('old failure'));
  else s.reads[0].resolve(s.page(8,[notification({id:'stale'})]));
  await Promise.all([old,duplicate]);
  assert.equal(service.getSnapshot().revision,9);
  assert.equal(service.getSnapshot().counts.unread,5);
  assert.equal(service.getSnapshot().busy,true,'only the successor may settle its busy state');
  assert.equal(service.getSnapshot().error,undefined);
  assert.ok(!service.getSnapshot().items.some(n=>n.id==='stale'));
  s.setNextCursor('');s.reads[1].resolve(s.page(10,[notification({id:'current'})]));await successor;
  assert.equal(service.getSnapshot().revision,10);assert.equal(service.getSnapshot().busy,false);
  assert.ok(service.getSnapshot().items.some(n=>n.id==='current'));
  service.dispose();
 }
});

test('a conflict copy cannot reacquire or reread a listener replacement',async()=>{
 const s=flightServer();const service=new InboxService(s.api,()=>'op');await service.load();
 let fired=false;let replacement:Promise<void>|undefined;
 service.subscribe(()=>{
  if(fired||!service.getSnapshot().error)return;
  fired=true;replacement=service.load('profile','archived');
 });
 s.setImmediate(Object.assign(new Error('moved'),{status:409,code:'revision_mismatch'}));
 await service.apply('read',['n-1']);await replacement;
 const reads=s.calls.filter(c=>c.path.startsWith('/v1/notifications/inbox?'));
 assert.equal(reads.length,2,'initial page and listener replacement only');
 assert.match(reads[1].path,/state=archived/);
 assert.equal(service.getSnapshot().view,'archived');assert.equal(service.getSnapshot().error,undefined);
 service.dispose();
});

test('an operation-id callback cannot lend a replacement page to the old action',async()=>{
 const s=flightServer();let replace=false,reload:Promise<void>|undefined;
 const service=new InboxService(s.api,()=>{if(replace){replace=false;reload=service.load();}return 'op';});
 await service.load();replace=true;
 await service.apply('read',['n-1']);await reload;
 assert.equal(s.calls.filter(c=>c.method==='POST').length,0);
 assert.equal(service.getSnapshot().items.length,2);
 service.dispose();
});

test('a pushed change merges in; an old revision is ignored; a resync reloads',async()=>{
 const s=server(),service=new InboxService(s.api,()=>'op');await service.load();
 service.accept({revision:6,since:5,resync:false,items:[],counts:counts(9)});
 assert.equal(service.getSnapshot().counts.unread,2,'stale');
 service.accept({revision:9,since:7,resync:false,items:[{...notification({id:'n-3',cursor:'5',revision:9}),createdAt:1,updatedAt:1,expiresAt:99} as any],counts:counts(3)});
 assert.equal(service.getSnapshot().items.length,3);assert.equal(service.getSnapshot().revision,9);
 s.set([notification({id:'n-9'})]);service.accept({revision:10,since:9,resync:true,items:[],counts:counts(1)});
 await new Promise(r=>setTimeout(r,5));assert.deepEqual(service.getSnapshot().items.map(n=>n.id),['n-9']);
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

test('badge refreshes share held requests instead of multiplying profile and owner reads',async()=>{
 let release!:(raw:unknown)=>void;let calls=0;
 const service=new InboxService({request:<T,>(path:string):Promise<T>=>{calls++;return new Promise(r=>{release=r as (raw:unknown)=>void;});}});
 const first=service.refreshUnread(),second=service.refreshUnread(),third=service.refreshUnread();
 assert.equal(first,second);assert.equal(first,third);assert.equal(calls,1);
 release(summary(4));assert.equal(await first,4);assert.equal(await second,4);assert.equal(calls,1);service.dispose();
});

test('a changed audience during a held badge refresh reruns once and publishes profile plus owner, never a partial total',async()=>{
 let release!:(raw:unknown)=>void;let badgeCalls=0;
 const service=new InboxService({request:<T,>(path:string):Promise<T>=>{
  if(path.includes('/inbox?'))return Promise.resolve(inbox([])) as Promise<T>;
  badgeCalls++;if(badgeCalls===1)return new Promise(r=>{release=r as (raw:unknown)=>void;});
  return Promise.resolve(summary(path.includes('account-admin')?3:2)) as Promise<T>;
 }});
 const badges:number[]=[];service.subscribe(()=>badges.push(service.getSnapshot().unread));
 const first=service.refreshUnread();await service.load();
 release(summary(100));assert.equal(await first,5);assert.equal(badgeCalls,3);
 assert.ok(!badges.includes(100));assert.equal(service.getSnapshot().unread,5);service.dispose();
});

test('badge deadlines release a held transport and disposal cancels the next read while retaining the last good badge',async()=>{
 let expire! :()=>void;let heldSignal:AbortSignal|undefined;let calls=0;
 const service=new InboxService({request:<T,>(_path:string,_method?:string,_body?:unknown,signal?:AbortSignal):Promise<T>=>{
  calls++;if(calls===1)return Promise.resolve(summary(7)) as Promise<T>;
  heldSignal=signal;return new Promise(()=>{});
 }},()=> 'op',{unreadTimeoutMs:100,setTimer:fn=>{expire=fn;return 1;},clearTimer:()=>{}});
 assert.equal(await service.refreshUnread(),7);
 const timed=service.refreshUnread();expire();assert.equal(await timed,7);assert.equal(heldSignal?.aborted,true);
 const disposed=service.refreshUnread();service.dispose();assert.equal(await disposed,7);assert.equal(heldSignal?.aborted,true);
 assert.equal(await service.refreshUnread(),7);assert.equal(calls,3);
});

test('an event arriving during a badge refresh causes one follow-up read rather than publishing a stale count',async()=>{
 let release!:(raw:unknown)=>void;let calls=0;
 const service=new InboxService({request:<T,>():Promise<T>=>{
  if(++calls===1)return new Promise(r=>{release=r as (raw:unknown)=>void;});
  return Promise.resolve(summary(9)) as Promise<T>;
 }});
 const pending=service.refreshUnread();
 service.accept({revision:8,since:7,resync:false,items:[],counts:counts(9)});
 service.accept({revision:9,since:8,resync:false,items:[],counts:counts(9)});
 release(summary(1));assert.equal(await pending,9);assert.equal(calls,2);service.dispose();
});

test('a failed obsolete audience refresh still follows up for the newly discovered owner audience',async()=>{
 let reject!:(e:Error)=>void;let calls=0;
 const service=new InboxService({request:<T,>(path:string):Promise<T>=>{
  if(path.includes('/inbox?'))return Promise.resolve(inbox([])) as Promise<T>;
  if(++calls===1)return new Promise((_r,no)=>{reject=no;});
  return Promise.resolve(summary(path.includes('account-admin')?3:2)) as Promise<T>;
 }});
 const pending=service.refreshUnread();await service.load();reject(new Error('offline'));
 assert.equal(await pending,5);assert.equal(calls,3);service.dispose();
});
