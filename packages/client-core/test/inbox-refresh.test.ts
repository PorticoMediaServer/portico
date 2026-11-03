import {test} from 'node:test';
import assert from 'node:assert/strict';
import {InboxService} from '../src/inbox.ts';

// OW-5: an open Notifications view follows `notification.changed` through InboxService.refresh.
const at=(ms:number)=>new Date(ms).toISOString();
const notice=(id:string,cursor:string)=>({
 id,audience:'profile',severity:'info',source:'downloads',category:'download.finished',
 title:'Download ready',body:'A title finished downloading.',arguments:{},actions:[],
 dedupeKey:'download:'+id,revision:7,createdAt:at(1),updatedAt:at(1),expiresAt:at(99),
 readAt:null,archivedAt:null,read:false,archived:false,cursor});
const counts=(unread:number)=>({unread,read:0,archived:0,total:unread});
type Held={resolve:(value:any)=>void;reject:(reason:any)=>void;signal?:AbortSignal};

function stub(){
 const reads:string[]=[],posts:Held[]=[],heldReads:Held[]=[];
 let revision=7,items:any[]=[notice('n-1','3'),notice('n-2','4')],nextCursor='',holdPosts=false,holdReads=false;
 const page=()=>({data:{revision,audience:'profile',audiences:['profile'],state:'unread',counts:counts(items.length),items,nextCursor,observedAt:at(2),retentionDays:180}});
 const api={request:<T,>(path:string,_method='GET',_body?:unknown,signal?:AbortSignal):Promise<T>=>{
  if(path.startsWith('/v1/notifications/unread-count'))return Promise.resolve({data:{revision,audience:'profile',counts:counts(items.length),observedAt:at(2)}} as T);
  if(path.startsWith('/v1/notifications/inbox/actions')){
   if(holdPosts)return new Promise<T>((resolve,reject)=>posts.push({resolve,reject,signal}));
   return Promise.resolve({data:{revision:++revision,audience:'profile',applied:1,receipts:[],counts:counts(items.length)}} as T);
  }
  reads.push(path);
  if(holdReads)return new Promise<T>((resolve,reject)=>heldReads.push({resolve,reject,signal}));
  return Promise.resolve(page() as T);
 }};
 return {api,reads,posts,heldReads,page,
  set:(next:{revision?:number;items?:any[];nextCursor?:string;holdPosts?:boolean;holdReads?:boolean})=>{
   revision=next.revision??revision;items=next.items??items;nextCursor=next.nextCursor??nextCursor;
   holdPosts=next.holdPosts??holdPosts;holdReads=next.holdReads??holdReads;
  },
  revision:()=>revision};
}
const tick=()=>new Promise(resolve=>setTimeout(resolve,5));

test('a change hint rereads the open view and shows a new notice, keeping the rows on screen meanwhile',async()=>{
 const s=stub(),service=new InboxService(s.api,()=>'op');
 await service.load('profile','unread');await tick();
 s.reads.length=0;
 s.set({revision:9,items:[notice('n-3','5'),notice('n-1','3'),notice('n-2','4')],holdReads:true});
 const phases:string[]=[];service.subscribe(()=>phases.push(service.getSnapshot().phase));
 service.refresh('9');
 assert.equal(s.reads.length,1);
 assert.match(s.reads[0],/audience=profile&state=unread&limit=50$/);
 assert.equal(service.getSnapshot().items.length,2,'the page on screen stays until the new one arrives');
 s.heldReads[0].resolve(s.page());await tick();
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),['n-3','n-1','n-2']);
 assert.equal(service.getSnapshot().revision,9);
 assert.ok(!phases.includes('loading'),'a quiet reread never publishes loading over shown rows');
});

test('a hint for a revision the view already holds reads nothing, and an idle view is not loaded by one',async()=>{
 const s=stub(),idle=new InboxService(s.api,()=>'op');
 idle.refresh('12');idle.refresh();
 assert.equal(s.reads.length,0,'a view nobody opened is not read by a hint');
 const service=new InboxService(s.api,()=>'op');
 await service.load();await tick();
 s.reads.length=0;
 service.refresh('7');service.refresh('6');
 assert.equal(s.reads.length,0);
});

test('a hint during a write waits for it instead of cancelling it, and the write\'s own echo reads nothing',async()=>{
 const s=stub(),service=new InboxService(s.api,()=>'op');
 await service.load();await tick();
 s.reads.length=0;s.set({holdPosts:true});
 const archived=service.apply('archive',['n-1']);
 await tick();
 assert.equal(s.posts.length,1);
 // The stream can announce the write before its own answer arrives.
 service.refresh('8');
 assert.equal(s.reads.length,0,'nothing is read while the write is out');
 assert.equal(s.posts[0].signal?.aborted,false,'the hint does not cancel the write');
 s.posts[0].resolve({data:{revision:8,audience:'profile',applied:1,receipts:[],counts:counts(1)}});
 await archived;await tick();
 assert.equal(s.reads.length,0,'revision 8 is the write itself');
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),['n-2']);
});

test('a hint newer than a write rereads once after the write settles',async()=>{
 const s=stub(),service=new InboxService(s.api,()=>'op');
 await service.load();await tick();
 s.reads.length=0;s.set({holdPosts:true});
 const archived=service.apply('archive',['n-1']);
 await tick();
 service.refresh('9');service.refresh('10');
 s.set({revision:10,items:[notice('n-4','6'),notice('n-2','4')]});
 s.posts[0].resolve({data:{revision:8,audience:'profile',applied:1,receipts:[],counts:counts(1)}});
 await archived;await tick();
 assert.equal(s.reads.length,1,'two hints, one reread');
 assert.deepEqual(service.getSnapshot().items.map(n=>n.id),['n-4','n-2']);
});

test('a hint during a first-page read is reread once that read lands, unless the read already holds it',async()=>{
 const s=stub(),service=new InboxService(s.api,()=>'op');
 await service.load();await tick();
 s.reads.length=0;s.set({holdReads:true});
 const loading=service.load('profile','all');
 service.refresh('11');
 assert.equal(s.reads.length,1,'the hint does not cancel the read that is out');
 s.set({revision:9,holdReads:false});
 s.heldReads[0].resolve(s.page());await loading;await tick();
 assert.equal(s.reads.length,2,'the read was older than the hint');
 assert.match(s.reads[1],/state=all/);
 s.reads.length=0;s.set({holdReads:true});
 const again=service.load('profile','all');
 service.refresh('12');
 s.set({revision:12,holdReads:false});
 s.heldReads.at(-1)!.resolve(s.page());await again;await tick();
 assert.equal(s.reads.length,1,'the read already carried revision 12');
});

test('a view that paged on rereads the rows it shows, up to the server\'s page limit',async()=>{
 const s=stub(),run=(prefix:string,length:number,from:number)=>Array.from({length},(_,i)=>notice(prefix+i,String(from+i)));
 s.set({items:run('a-',50,0),nextCursor:'50'});
 const service=new InboxService(s.api,()=>'op');
 await service.load();await tick();
 s.set({items:run('b-',30,50),nextCursor:'80'});
 await service.more();await tick();
 assert.equal(service.getSnapshot().items.length,80);
 s.reads.length=0;
 service.refresh('9');await tick();
 assert.match(s.reads[0],/limit=80$/);
 // The stub answers every read with the same rows; page on past one server page.
 s.set({items:run('c-',50,0),nextCursor:'50'});
 await service.load();await tick();
 s.set({items:run('d-',50,50),nextCursor:'100'});await service.more();await tick();
 s.set({items:run('e-',30,100),nextCursor:''});await service.more();await tick();
 assert.equal(service.getSnapshot().items.length,130);
 s.reads.length=0;s.set({revision:10});
 service.refresh('10');await tick();
 assert.match(s.reads[0],/limit=100$/,'never more than one server page');
});
