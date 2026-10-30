import {wireFixture} from './wire-fixture.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {PersonalSavedService,makeSavedDefinition,savedTitlePrefix,savedCategoryId} from '../src/personal-saved.ts';
const scope={serverId:'server',viewerId:'["local","account","profile"]'};
const media=(id='item')=>({id,libraryId:'library',kind:'movie',title:'Movie',available:true,playback:{itemId:id,startSeconds:0}});
const resource=(patch:any={})=>({serverId:'server',viewerFence:'fence',id:'collection',kind:'collection',name:'Collection',summary:'',visibility:'private',revision:1,role:'owner',entryCount:0,actions:['pin','update','delete','entries','share'],status:'ready',invalidComponents:[],pinned:false,pinRevision:0,shares:[],...patch});
const directory=(resources:any[]=[])=>({serverId:'server',viewerFence:'fence',revision:1,resources,nextCursor:''});
// A collection page echoes its query, offers its orders and filters, and counts what matches.
const pageFields=(total:number)=>({query:{sort:'position',direction:'asc',category:'all',q:'',limit:40,searchMode:'none'},sorts:[{id:'position',labelKey:'sort.position',directions:['asc']}],filters:[{id:'filter',labelKey:'saved.filter',options:[{id:'all',label:'All',count:total}]}],totalCount:total,start:0});
const content=(r=resource(),entries:any[]=[])=>({resource:r,entries,nextCursor:'',...pageFields(entries.length)});
const deferred=()=>{let resolve!:(v:any)=>void;const promise=new Promise<any>(r=>resolve=r);return{promise,resolve};};
const tick=()=>new Promise(r=>setTimeout(r,0));
function setup(request:(path:string,method?:string,body?:any,signal?:AbortSignal)=>Promise<any>){let n=0;return new PersonalSavedService({request:async (...args:any[])=>wireFixture(await (request as any)(...args),{'duration': 'durationMs', 'progressSeconds': 'progressMs', 'startSeconds': 'startMs', 'positionSeconds': 'positionMs'})},scope,async()=>`00000000-0000-4000-8000-${String(++n).padStart(12,'0')}`);}

test('collection directory and opaque membership use exact server IDs, roles and page positions',async()=>{
 const paths:string[]=[];const s=setup(async p=>{paths.push(p);return p.includes('/content')?{...content(resource(),[{id:'member',hidden:false,media:media()},{id:'hidden',hidden:true}]),nextCursor:p.includes('cursor')?'':'next'}:directory([resource()]);});
 await s.select({view:'collections'});assert.equal(s.getSnapshot().resources[0].id,'collection');
 await s.select({view:'resource',resourceId:'collection'});assert.deepEqual(s.getSnapshot().entries.map(e=>e.id),['member','hidden']);
 await s.next();assert.equal(s.getSnapshot().cursor,'next');assert.deepEqual(s.getSnapshot().history,[null]);await s.previous();assert.equal(s.getSnapshot().cursor,null);assert(paths.some(p=>p.includes('cursor=next')));assert(Object.isFrozen(s.getSnapshot().entries));s.dispose();
});
test('collection network retry preserves exact operation and original CAS revision',async()=>{
 const writes:any[]=[];let revision=1;const s=setup(async(p,m,b)=>{if(m==='GET')return content(resource({revision}));writes.push(b);if(writes.length===1)throw new Error('lost response');revision=2;return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'collection',revision,deleted:false}};});
 await s.select({view:'resource',resourceId:'collection'});assert.equal(await s.mutate({action:'entries',addItemIds:['item']}),false);assert(s.getSnapshot().retryPending);assert(await s.retry());assert.deepEqual(writes[0],writes[1]);assert.equal(writes[0].expectedRevision,1);assert.equal(s.getSnapshot().resource?.revision,2);s.dispose();
});
test('CAS conflict refresh requires an explicit rebase with a different operation ID',async()=>{
 const writes:any[]=[];let revision=1;const s=setup(async(p,m,b)=>{if(m==='GET')return content(resource({revision}));writes.push(b);if(writes.length===1){revision=5;throw Object.assign(new Error('Changed'),{code:'revision_mismatch',status:412});}revision=6;return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'collection',revision,deleted:false}};});
 await s.select({view:'resource',resourceId:'collection'});await s.mutate({action:'update',name:'Changed'});assert(s.getSnapshot().mutationError?.conflict);assert.equal(writes.length,1);assert.equal(s.getSnapshot().resource?.revision,5);assert(await s.rebase());assert.notEqual(writes[0].operationId,writes[1].operationId);assert.equal(writes[1].expectedRevision,5);s.dispose();
});
test('current deletion status on replay does not resurrect a previously created collection',async()=>{
 const s=setup(async(p,m,b)=>m==='GET'?directory():{serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'deleted-collection',revision:1,deleted:false},current:{resourceId:'deleted-collection',revision:2,deleted:true}});
 await s.select({view:'collections'});assert(await s.mutate({action:'create',kind:'collection',name:'Collection'}));assert.deepEqual(s.getSnapshot().result,{resourceId:'deleted-collection',deleted:true});s.dispose();
});
test('viewer permissions and opaque member validation fail closed',async()=>{
 let writes=0;const s=setup(async(p,m)=>{if(m!=='GET')writes++;return content(resource({role:'viewer',actions:['pin']}));});await s.select({view:'resource',resourceId:'collection'});await assert.rejects(()=>s.mutate({action:'entries',addItemIds:['item']}),/not currently/);assert.equal(writes,0);s.dispose();
 // A hidden member stays opaque whatever else its row carries, and a repeated member keeps its first row.
 for(const entries of [[{id:'hidden',hidden:true,media:media()}],[{id:'hidden',hidden:true},{id:'hidden',hidden:true}]]){const opaque=setup(async()=>content(resource(),entries));await opaque.select({view:'resource',resourceId:'collection'});assert.equal(opaque.getSnapshot().error,null);assert.deepEqual(opaque.getSnapshot().entries,[{id:'hidden',hidden:true}]);opaque.dispose();}
});
test('one member, history row or resource this app cannot read is left out and the rest is kept',async()=>{
 const members=setup(async()=>({...content(resource(),[{id:'one',hidden:false,media:media('one')},{id:'broken',hidden:false,media:{id:'x'}},{hidden:false},'nonsense',{id:'two',hidden:false,media:media('two'),futureField:[1]}]),futureField:true}));
 await members.select({view:'resource',resourceId:'collection'});assert.equal(members.getSnapshot().error,null);assert.deepEqual(members.getSnapshot().entries.map(e=>e.id),['one','two']);members.dispose();
 const row=(id:string,patch:any={})=>({id,media:media(id),updatedAt:'2026-09-06T12:00:00Z',positionSeconds:30,completed:false,...patch});
 const history=setup(async()=>({serverId:'server',viewerFence:'fence',revision:4,entries:[row('one'),row('bad',{positionSeconds:-1}),row('one',{completed:true}),row('two')],nextCursor:''}));
 await history.select({view:'history'});assert.equal(history.getSnapshot().error,null);assert.deepEqual(history.getSnapshot().entries.map(e=>[e.id,e.completed]),[['one',false],['two',false]]);assert.equal(history.getSnapshot().revision,4);history.dispose();
 const listed=setup(async()=>directory([resource(),resource({id:'smart',kind:'smart-shelf'}),resource({id:'review',status:'archived'}),resource({id:'other',actions:['pin','teleport'],shares:[{authority:'local',accountId:'a',profileId:'p',role:'viewer',displayName:'Second'},{authority:'local',accountId:'a',profileId:'q',role:'curator',displayName:'Third'}]}),resource()]));
 await listed.select({view:'collections'});assert.equal(listed.getSnapshot().error,null);assert.deepEqual(listed.getSnapshot().resources.map(r=>r.id),['collection','other']);
 assert.deepEqual(listed.getSnapshot().resources[1].actions,['pin']);assert.deepEqual(listed.getSnapshot().resources[1].shares.map(s=>[s.profileId,s.role]),[['p','viewer'],['q','viewer']]);listed.dispose();
});
test('sizes, scope, fences, revisions, roles and cursors still refuse the whole document',async()=>{
 for(const page of [directory(Array.from({length:101},(_,i)=>resource({id:'c'+i}))),directory([resource(),resource({id:'two',viewerFence:'other'})]),directory([resource({serverId:'other'})]),{...directory(),serverId:'other'},{...directory(),revision:-1},{...directory(),nextCursor:7},{...directory(),resources:{}}]){
  const s=setup(async()=>page);await s.select({view:'collections'});assert.equal(s.getSnapshot().error?.code,'invalid_saved');assert.equal(s.getSnapshot().resources.length,0);s.dispose();
 }
 for(const patch of [{role:'curator'},{role:'viewer'},{role:'editor',actions:['pin','delete']},{revision:'1'},{pinRevision:-1},{visibility:'everyone'},{viewerFence:''},{id:'another'},{actions:new Array(9).fill('pin')}]){
  const s=setup(async()=>content(resource(patch)));await s.select({view:'resource',resourceId:'collection'});assert.equal(s.getSnapshot().error?.code,'invalid_saved',JSON.stringify(patch));assert.equal(s.getSnapshot().resource,null);s.dispose();
 }
});
test('late reads and writes cannot publish into another resource or disposed scope',async()=>{
 const late=deferred();let body:any;const s=setup(async(p,m,b)=>{if(m==='GET')return p.includes('/content')?content():directory();body=b;return late.promise;});await s.select({view:'resource',resourceId:'collection'});const change=s.mutate({action:'delete'});await tick();await s.select({view:'views'});await change;late.resolve({serverId:'server',viewerFence:'fence',receipt:{operationId:body.operationId,resourceId:'collection',revision:2,deleted:true}});await tick();assert.equal(s.getSnapshot().route?.view,'views');assert.equal(s.getSnapshot().result,null);s.dispose();
 const read=deferred();const x=setup(async()=>read.promise);const work=x.select({view:'collections'});x.dispose();await work;read.resolve(directory([resource()]));await tick();assert.equal(x.getSnapshot().resources.length,0);
});
test('history-only clear and reset-viewing-activity remain distinct idempotent commands',async()=>{
 const bodies:any[]=[];let revision=0;const s=setup(async(p,m,b)=>{if(m==='GET')return {serverId:'server',viewerFence:'fence',revision,entries:[{id:'occurrence',media:media(),updatedAt:'2026-09-06T12:00:00Z',positionSeconds:30,completed:false}],nextCursor:''};bodies.push(b);return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,revision:++revision,action:b.action}};});await s.select({view:'history'});await s.mutate({action:'clear-history'});await s.mutate({action:'reset-viewing-activity'});assert.deepEqual(bodies.map(b=>b.action),['clear-history','reset-viewing-activity']);assert.deepEqual(bodies.map(b=>b.expectedRevision),[0,1]);assert.notEqual(bodies[0].operationId,bodies[1].operationId);s.dispose();
});
test('saved query needs-review does not execute or normalize an unsupported definition',async()=>{
 const s=setup(async()=>content(resource({id:'query',kind:'view',actions:['pin','update','delete'],status:'needs-review',invalidComponents:['pivot'],definition:{pivot:'obsolete'}})));await s.select({view:'resource',resourceId:'query'});assert.equal(s.getSnapshot().resource?.status,'needs-review');assert.equal(s.getSnapshot().browse,null);assert.equal(s.getSnapshot().resource?.definition,undefined);assert.equal(s.getSnapshot().error,null);s.dispose();
 assert.throws(()=>makeSavedDefinition('','prefix'));assert.equal(savedTitlePrefix(makeSavedDefinition('library','Prefix')),'Prefix');assert.equal(savedCategoryId(makeSavedDefinition('library','','grid','title','asc','decade:1990')),'decade:1990');
});
test('pin uses its separate revision and remains available to a resource viewer',async()=>{
 let body:any;const s=setup(async(p,m,b)=>{if(m==='GET')return content(resource({role:'viewer',actions:['pin'],pinRevision:3}));assert.equal(p,'/v1/saved-pins/collection/collection');body=b;return {serverId:'server',viewerFence:'fence',operationId:b.operationId,resourceId:'collection',revision:1,pinRevision:4,pinned:true,deleted:false};});await s.select({view:'resource',resourceId:'collection'});assert(await s.mutate({action:'pin',pinned:true}));assert.equal(body.expectedRevision,3);assert.equal(body.pinned,true);s.dispose();
});
test('scope navigation cancels stalled operation-ID acquisition without issuing a mutation',async()=>{
 const id=deferred();let writes=0;const s=new PersonalSavedService({request:async<T>(_p,m)=>{if(m!=='GET')writes++;return directory() as T;}},scope,()=>id.promise);await s.select({view:'collections'});const pending=s.mutate({action:'create',kind:'collection',name:'Name'});await s.select({view:'views'});assert.equal(await pending,false);id.resolve('00000000-0000-4000-8000-000000000001');await tick();assert.equal(writes,0);assert.equal(s.getSnapshot().pending,false);s.dispose();
});

test('collection receipts preserve added, unchanged and refused item outcomes for UI confirmation',async()=>{
 for(const entries of [
  {added:['item'],removed:[],unchanged:[],failed:[]},
  {added:[],removed:[],unchanged:['item'],failed:[]},
  {added:[],removed:[],unchanged:[],failed:[{itemId:'item',code:'item_unavailable'}]},
 ]){
  const s=setup(async(_p,m,b)=>m==='GET'?content():{serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'collection',revision:2,deleted:false,entries}});
  await s.select({view:'resource',resourceId:'collection'});assert.equal(await s.mutate({action:'entries',addItemIds:['item']}),entries.failed.length===0);
  assert.equal(s.getSnapshot().retryPending,entries.failed.length>0);assert.deepEqual(s.getSnapshot().result?.entries,entries);assert.ok(Object.isFrozen(s.getSnapshot().result?.entries));s.dispose();
 }
});
test('malformed collection outcome cannot become a successful receipt',async()=>{
 const s=setup(async(_p,m,b)=>m==='GET'?content():{serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'collection',revision:2,deleted:false,entries:{added:['item'],removed:[],unchanged:[],failed:[{itemId:'item',code:'denied'}]}}});
 await s.select({view:'resource',resourceId:'collection'});assert.equal(await s.mutate({action:'entries',addItemIds:['item']}),false);assert.equal(s.getSnapshot().result,null);s.dispose();
});
test('a collection removal carries its undo token, and Undo sends the token alone',async()=>{
 const writes:any[]=[];let revision=1;const s=setup(async(_p,m,b)=>{if(m==='GET')return content(resource({revision}),[{id:'member',hidden:false,media:media()}]);writes.push(b);revision++;return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'collection',revision,deleted:false,entries:b.undoToken?{added:['member'],removed:[],unchanged:[],failed:[]}:{added:[],removed:['member'],unchanged:[],failed:[]},...(b.undoToken?{}:{undoToken:'undo-1',undoUntil:'2026-10-15T12:00:30.000Z'})}};});
 await s.select({view:'resource',resourceId:'collection'});
 assert.equal(await s.mutate({action:'entries',removeEntryIds:['member']}),true);
 assert.deepEqual(s.getSnapshot().result?.undo,{token:'undo-1',until:'2026-10-15T12:00:30.000Z'});
 assert.equal(await s.mutate({action:'entries',undoToken:'undo-1'}),true);
 assert.equal(writes[1].undoToken,'undo-1');assert.equal(writes[1].addItemIds,undefined);assert.equal(writes[1].removeEntryIds,undefined);assert.equal(s.getSnapshot().result?.undo,undefined);
 await assert.rejects(()=>s.mutate({action:'entries',undoToken:'undo-1',removeEntryIds:['member']}),/Invalid/);s.dispose();
});
test('a several-entry reorder refused because the collection changed surfaces the conflict instead of reapplying an old order',async()=>{
 const writes:any[]=[];let revision=3;const s=setup(async(p,m,b)=>{if(m==='GET')return content(resource({revision,actions:['pin','update','delete','entries','reorder','share']}),[{id:'e1',hidden:false,media:media('a')},{id:'e2',hidden:false,media:media('b')}]);writes.push(b);revision=4;throw Object.assign(new Error('Changed'),{code:'revision_mismatch'});});
 await s.select({view:'resource',resourceId:'collection'});
 assert.equal(await s.mutate({action:'reorder',entryIds:['e2','e1'],afterEntryId:''}),false);
 assert.equal(writes.length,1,'no automatic second write');
 assert.equal(s.getSnapshot().mutationError?.conflict,true);assert.equal(s.getSnapshot().revision,4,'the newer collection is shown');
 s.dispose();
});
test('a collection move preserves its refused revision until the viewer explicitly reviews and rebases',async()=>{
 const writes:any[]=[];const paths:string[]=[];let revision=3;const s=setup(async(p,m,b)=>{if(m==='GET')return content(resource({revision,actions:['pin','update','delete','entries','reorder','share']}),[{id:'e1',hidden:false,media:media('a')},{id:'e2',hidden:false,media:media('b')}]);paths.push(p);writes.push(b);if(writes.length===1){revision=4;throw Object.assign(new Error('Changed'),{code:'revision_mismatch'});}revision=5;return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'collection',revision,deleted:false}};});
 await s.select({view:'resource',resourceId:'collection'});
 assert.equal(await s.mutate({action:'reorder',entryIds:['e2'],afterEntryId:''}),false);
 assert.equal(writes.length,1,'a conflict never authors another operation automatically');
 assert.equal(writes[0].expectedRevision,3);
 assert.equal(s.getSnapshot().mutationError?.code,'revision_mismatch');
 assert.equal(await s.rebase(),true);
 assert.deepEqual(paths,['/v1/saved-resources/collection/order','/v1/saved-resources/collection/order']);
 assert.deepEqual([writes[0].expectedRevision,writes[1].expectedRevision],[3,4]);
 assert.deepEqual(writes[1].entryIds,['e2']);assert.equal(writes[1].afterEntryId,'');assert.notEqual(writes[0].operationId,writes[1].operationId);
 assert.equal(s.getSnapshot().mutationError,null);
 await assert.rejects(()=>s.mutate({action:'reorder',entryIds:['e1','e1']}),/Invalid collection order/);
 s.dispose();
 // An editor may move entries; a viewer is not offered it.
 const viewer=setup(async()=>content(resource({role:'viewer',actions:['pin']})));await viewer.select({view:'resource',resourceId:'collection'});
 await assert.rejects(()=>viewer.mutate({action:'reorder',entryIds:['e1']}),/not currently/);viewer.dispose();
 const editor=setup(async()=>content(resource({role:'editor',actions:['pin','entries','reorder']})));await editor.select({view:'resource',resourceId:'collection'});
 assert.deepEqual(editor.getSnapshot().resource?.actions,['pin','entries','reorder']);editor.dispose();
});
test('a collection paged past its first page is read again through every page on screen, and a move keeps the viewer where they are',async()=>{
 // 100 members, 40 a page: the cursor names where the page starts.
 let order=Array.from({length:100},(_,i)=>'m'+i);let revision=1;const reads:string[]=[];
 const s=setup(async(p,m,b)=>{
  if(m==='GET'){const at=Number(new URL('http://x'+p).searchParams.get('cursor')??0);reads.push(String(at));const ids=order.slice(at,at+40);return {resource:resource({revision,actions:['pin','update','delete','entries','reorder','share']}),entries:ids.map(id=>({id,hidden:false,media:media(id)})),nextCursor:at+40<order.length?String(at+40):'',...pageFields(order.length),start:at};}
  const from=order.indexOf(b.entryIds[0]);order.splice(from,1);order.splice(b.afterEntryId?order.indexOf(b.afterEntryId)+1:0,0,b.entryIds[0]);revision++;
  return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'collection',revision,deleted:false}};
 });
 await s.select({view:'resource',resourceId:'collection'});await s.next();await s.next();
 assert.equal(s.getSnapshot().cursor,'80');reads.length=0;
 // A change elsewhere: the first page is read again, through the page in view, as one first page.
 order=['new',...order];await s.revalidate();
 assert.deepEqual(reads,['0','40','80']);assert.equal(s.getSnapshot().cursor,null);assert.deepEqual(s.getSnapshot().history,[]);
 assert.equal(s.getSnapshot().entries.length,101);assert.equal(s.getSnapshot().entries[0].id,'new');assert.equal(s.getSnapshot().nextCursor,'');
 // A move deep in the list: no loading state, every entry still on screen, in the server's new order.
 reads.length=0;const loading:boolean[]=[];const off=s.subscribe(()=>loading.push(s.getSnapshot().loading));
 assert(await s.mutate({action:'reorder',entryIds:['m90'],afterEntryId:''}));off();
 assert(!loading.includes(true));assert.equal(s.getSnapshot().entries.length,101);assert.equal(s.getSnapshot().entries[0].id,'m90');assert.equal(s.getSnapshot().resource?.revision,2);
 s.dispose();
});

test('cancelling a deep Saved quiet reread aborts its pending page and never reads the next one',async()=>{
 for(const retire of ['caller','dispose'] as const){
  const late=deferred();let quiet=false,reads=0,settles=0,signal:AbortSignal|undefined;
  const page=(at:number)=>({...content(resource(),Array.from({length:40},(_,i)=>({id:'member-'+(at+i),hidden:true}))),nextCursor:String(at+40)});
  const s=setup(async(path,_method,_body,current)=>{
   const at=Number(new URL(path,'https://server.test').searchParams.get('cursor')??0);
   if(quiet){reads++;signal=current;return late.promise;}
   return page(at);
  });
  await s.select({view:'resource',resourceId:'collection'});await s.next();await s.next();
  const shown=s.getSnapshot(),controller=new AbortController();quiet=true;
  const refresh=s.revalidate({signal:controller.signal,settle:async()=>{settles++;}});
  if(retire==='caller')controller.abort();else s.dispose();
  await refresh;
  assert.equal(signal?.aborted,true);
  assert.equal(reads,1);
  assert.equal(settles,0);
  assert.equal(s.getSnapshot(),shown);
  late.resolve(page(0));await tick();
  assert.equal(reads,1,'an ignored abort by transport cannot resume page traversal');
  s.dispose();
 }
});

test('an already-cancelled Saved quiet read dispatches no request',async()=>{
 let reads=0;
 const s=setup(async()=>{reads++;return directory([resource()]);});
 await s.select({view:'collections'});
 const controller=new AbortController();controller.abort();
 await s.revalidate({signal:controller.signal});
 assert.equal(reads,1);s.dispose();
});

test('capturing and reopening a saved view preserves relative dates verbatim', async () => {
 const definition={libraryId:'library',pivot:'movies',query:{field:'dateAdded',operator:'within-last-days',value:30},sort:[{field:'added',direction:'desc'}],presentation:'grid'} as const;
 let stored:any;const writes:any[]=[];
 const s=setup(async(p,m,b)=>{
  if(m!=='GET'){writes.push(b);stored=structuredClone(b.definition);return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'recent',revision:1,deleted:false}};}
  const r=resource({id:'recent',kind:'view',name:'Recent',definition:stored,actions:['pin','update','delete']});
  return p.includes('/content')?{resource:r,browse:{pivot:'movies',applied:{query:stored.query,sort:stored.sort},entries:[],pageInfo:{start:0,total:0,totalIsLowerBound:false,revision:'browse_relative',hasMore:false,nextCursor:''},positionIndex:[]},nextCursor:''}:directory(stored?[r]:[]);
 });
 await s.select({view:'views'});
 assert.equal(await s.mutate({action:'create',kind:'view',name:'Recent',definition}),true);
 assert.deepEqual(writes[0].definition,definition);
 await s.select({view:'resource',resourceId:'recent'});
 assert.equal(s.getSnapshot().error,null);
 assert.deepEqual(s.getSnapshot().resource?.definition,definition);
 assert.deepEqual(s.getSnapshot().browse?.applied.query,definition.query);
 s.dispose();
});

test('a saved view keeps the relative date operator verbatim through capture and reopen',async()=>{
 const definition={libraryId:'library',pivot:'movies',query:{all:[{field:'dateAdded',operator:'within-last-days',value:30}]},sort:[{field:'added',direction:'desc'}],presentation:'grid'};
 let written:any;
 const s=setup(async(p,m,b)=>{if(m==='GET')return p.includes('/content')?content(resource({id:'query',kind:'view',definition,actions:['pin','update','delete']})):directory([resource({id:'query',kind:'view',definition})]);written=b;return {serverId:'server',viewerFence:'fence',receipt:{operationId:b.operationId,resourceId:'query',revision:2,deleted:false}};});
 await s.select({view:'views'});
 assert.deepEqual(s.getSnapshot().resources[0]?.definition,definition);
 await s.mutate({action:'create',kind:'view',name:'New albums',definition:definition as never});
 assert.deepEqual(written.definition,definition);
 s.dispose();
});
