import {wireFixture} from './wire-fixture.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {SavedService} from '../src/saved.ts';
const scope={serverId:'server',viewerId:'["local","account","profile"]'};
const tick=()=>new Promise(r=>setTimeout(r,0));
const deferred=()=>{let resolve!:(v:any)=>void;const promise=new Promise<any>(r=>resolve=r);return{promise,resolve};};
const media=(id='movie')=>({id,libraryId:'library',kind:'movie',title:'Movie',available:true,playback:{itemId:id,startSeconds:0}});
const occurrence=(id='entry')=>({id,kind:'playlist_entry',hidden:false,media:media()});
function savedFilters(count=3){return [{id:'filter',labelKey:'saved.filter',options:[{id:'all',label:'All',count},{id:'unwatched',label:'Unwatched',count},{id:'inProgress',label:'In progress',count}]}];}
function projection(view='playlist',revision=0,entries:any[]=[occurrence()],cursor='',category?:string){
 const listView=view==='watchlist'||view==='favorites';
 const resolved=category??(listView||view==='playlist'?'all':'');
 return {scope:{serverId:'server',libraryId:'',libraryKind:'mixed',view,entityId:view==='playlist'?'playlist':'',viewerFence:'fence'},revision:{catalog:revision,viewer:0},...(view==='playlist'?{playlistRevision:revision}:{}),...(view==='playlists'?{actions:['create_playlist']}:{}),heading:{key:'saved.title',fallback:'Saved'},navigation:[{id:'watchlist',labelKey:'saved.watchlist',view:'watchlist'},{id:'favorites',labelKey:'saved.favorites',view:'favorites'},{id:'playlists',labelKey:'saved.playlists',view:'playlists'}],query:{sort:view==='playlist'?'position':'title',direction:'asc',category:resolved,q:'',limit:40,searchMode:'none'},sorts:[],filters:listView?savedFilters(Math.max(3,entries.length)):[],sections:[{id:'entries',type:'list',heading:{key:'saved.entries',fallback:'Entries'},entries,totalCount:Math.max(3,entries.length),nextCursor:cursor,start:0,...(view==='playlist'&&entries.length?{order:{...(cursor?{next:'after-page',last:'playlist-tail'}:{last:entries.at(-1).id})}}:{})}]};
}
const resource=(revision=0,entryCount=1)=>({serverId:'server',viewerFence:'fence',id:'playlist',name:'Playlist',summary:'Summary',revision,role:'owner',entryCount,actions:['rename','summary','delete','share','add','remove','reorder'],limits:{maxEntries:1000,maxShares:100,maxReorderEntries:1000},shares:[]});
const receipt=(body:any,revision=1)=>({serverId:'server',viewerFence:'fence',operationId:body.operationId,playlistId:'playlist',revision,deleted:false,entryId:'new-entry'});
function setup(request:(path:string,method?:string,body?:any,signal?:AbortSignal)=>Promise<any>,timeoutMs=200){let i=0;return new SavedService({scope,api:{request:async (...args:any[])=>wireFixture(await (request as any)(...args),{'duration': 'durationMs', 'progressSeconds': 'progressMs', 'startSeconds': 'startMs', 'positionSeconds': 'positionMs'})},requestId:async()=>`00000000-0000-4000-8000-${String(++i).padStart(12,'0')}`,timeoutMs});}
const isContent=(p:string)=>p.includes('/content');

test('server occurrence order preserves duplicate media and opaque hidden rows',async()=>{
 const entries=[occurrence('one'),{id:'hidden',kind:'playlist_entry',hidden:true},occurrence('two')];
 const s=setup(async p=>isContent(p)?projection('playlist',0,entries):resource(0,3));await s.select({view:'playlist',playlistId:'playlist'});
 assert.equal(s.getSnapshot().phase,'ready');assert.deepEqual(s.getSnapshot().projection!.sections[0].entries,entries);assert(Object.isFrozen(s.getSnapshot().projection!.sections[0].entries));
});
test('Saved retains genuine shared catalogue facts and stored display titles for media and visible occurrences',async()=>{
 const facts={...media('episode'),kind:'episode',title:'The Hunger Games: Special Edition',displayTitle:'  The Hunger Games: Special Edition  ',year:2012,watched:true,new:false,seasonNumber:2,episodeNumber:7,show:{id:'show',name:'Actual show'},artist:{id:'artist',name:'Actual artist'},album:{id:'album',name:'Actual album'},author:'Actual author',libraryId:'actual-library',duration:120,progressSeconds:17};
 for(const view of ['watchlist','playlist']){
  const entries=view==='playlist'?[{id:'occurrence',kind:'playlist_entry',hidden:false,media:facts}]:[facts];
  const s=setup(async p=>isContent(p)?projection(view,0,entries):resource());
  await s.select(view==='playlist'?{view:'playlist',playlistId:'playlist'}:{view:'watchlist'});
  const entry=s.getSnapshot().projection!.sections[0].entries[0];
  assert.deepEqual(view==='playlist'?(entry as {media:unknown}).media:entry,facts);
  s.dispose();
 }
 const hidden=setup(async p=>isContent(p)?projection('playlist',0,[{id:'hidden',kind:'playlist_entry',hidden:true,media:facts}]):resource());
 await hidden.select({view:'playlist',playlistId:'playlist'});
 assert.deepEqual(hidden.getSnapshot().projection!.sections[0].entries,[{id:'hidden',kind:'playlist_entry',hidden:true}]);
 hidden.dispose();
});
test('a hidden occurrence stays opaque whatever else its row carries, and a repeated occurrence ID keeps its first row',async()=>{
 for(const entries of [[{...occurrence(),hidden:true}],[{id:'entry',kind:'playlist_entry',hidden:true,title:'Secret'}]]){const s=setup(async p=>isContent(p)?projection('playlist',0,entries):resource());await s.select({view:'playlist',playlistId:'playlist'});assert.equal(s.getSnapshot().phase,'ready');assert.deepEqual(s.getSnapshot().projection!.sections[0].entries,[{id:'entry',kind:'playlist_entry',hidden:true}]);}
 const s=setup(async p=>isContent(p)?projection('playlist',0,[occurrence('one'),{...occurrence('one'),media:media('other')},occurrence('two')]):resource());await s.select({view:'playlist',playlistId:'playlist'});
 assert.equal(s.getSnapshot().phase,'ready');assert.deepEqual(s.getSnapshot().projection!.sections[0].entries,[occurrence('one'),occurrence('two')]);
});
test('one entry this app cannot read is left out and the rest of the page is kept',async()=>{
 const entries=[media('one'),{id:'pod',libraryId:'library',kind:'podcast',title:'Podcast'},{id:'bad',libraryId:'library',kind:'movie'},'nonsense',{...media('two'),futureField:{deep:[1,2,3]}}];
 const s=setup(async()=>({...projection('watchlist',0,entries),futureField:true}));await s.select({view:'watchlist'});
 assert.equal(s.getSnapshot().phase,'ready');assert.deepEqual(s.getSnapshot().projection!.sections[0].entries,[media('one'),media('two')]);
 assert.equal(s.getSnapshot().projection!.sections[0].totalCount,5);
});
test('an unknown layout reads as a grid, an unreadable destination or play action is left off, and a short count is repaired',async()=>{
 const page=projection('watchlist',0,[{...media('one'),navigation:{view:'hologram',entityId:'one'}},{...media('two'),playback:{startSeconds:-1}},media('three'),media('four')]);
 page.sections[0]={...page.sections[0],type:'mosaic',totalCount:1};page.navigation[1]={id:'future',labelKey:'saved.future',view:'future'} as any;page.sorts=[{id:'title',labelKey:'sort.title',directions:['asc','sideways']},{id:'title',labelKey:'sort.again',directions:['desc']},{labelKey:'sort.none'}] as any;
 const s=setup(async()=>page);await s.select({view:'watchlist'});
 const read=s.getSnapshot().projection!;assert.equal(s.getSnapshot().phase,'ready');
 assert.equal(read.sections[0].type,'grid');assert.equal(read.sections[0].totalCount,4);
 const {playback:_,...plain}=media('two');assert.deepEqual(read.sections[0].entries,[media('one'),plain,media('three'),media('four')]);
 assert.deepEqual(read.navigation.map(n=>n.id),['watchlist','playlists']);assert.deepEqual(read.sorts,[{id:'title',labelKey:'sort.title',directions:['asc']}]);
});
test('a playlist page reads its published filters, and too many still refuse it',async()=>{
 const s=setup(async p=>isContent(p)?{...projection(),filters:savedFilters()}:resource());await s.select({view:'playlist',playlistId:'playlist'});
 assert.equal(s.getSnapshot().phase,'ready');assert.deepEqual(s.getSnapshot().projection!.filters.map(f=>f.id),['filter']);
 const tooMany=setup(async p=>isContent(p)?{...projection(),filters:new Array(5).fill(savedFilters()[0])}:resource());await tooMany.select({view:'playlist',playlistId:'playlist'});assert.equal(tooMany.getSnapshot().error?.code,'invalid_saved');
});
test('sizes, cursors, scope and revisions still refuse the whole page',async()=>{
 const tooMany=projection('watchlist',0,Array.from({length:101},(_,i)=>media('m'+i)));
 const tooManySections=projection('watchlist');tooManySections.sections=Array.from({length:5},(_,i)=>({...tooManySections.sections[0],id:'s'+i}));
 const badCursor=projection('watchlist');badCursor.sections[0]={...badCursor.sections[0],nextCursor:7 as any};
 const otherServer=projection('watchlist');otherServer.scope={...otherServer.scope,serverId:'other'};
 const noFence=projection('watchlist');noFence.scope={...noFence.scope,viewerFence:''};
 const badRevision=projection('watchlist');badRevision.revision={catalog:-1,viewer:0};
 const notAList={...projection('watchlist'),sections:{}};
 for(const page of [tooMany,tooManySections,badCursor,otherServer,noFence,badRevision,notAList]){const s=setup(async()=>page);await s.select({view:'watchlist'});assert.equal(s.getSnapshot().error?.code,'invalid_saved');assert.equal(s.getSnapshot().projection,null);}
});
test('a playlist drops an action it cannot read, lists a share of an unknown role as a viewer, and never guesses its own role or a limit',async()=>{
 const share={authority:'local',accountId:'account2',profileId:'profile2',role:'viewer',displayName:'Second'};
 const s=setup(async p=>isContent(p)?projection():{...resource(),actions:[...resource().actions,'teleport'],shares:[share,{...share,profileId:'profile3',role:'curator'},share],futureField:1});await s.select({view:'playlist',playlistId:'playlist'});
 assert.equal(s.getSnapshot().phase,'ready');assert.deepEqual(s.getSnapshot().playlist!.actions,resource().actions);assert.deepEqual(s.getSnapshot().playlist!.shares,[share,{...share,profileId:'profile3',role:'viewer'}],'someone the playlist is shared with is never hidden');
 for(const patch of [{role:'curator'},{role:'viewer',shares:undefined},{limits:{maxShares:100}},{limits:{maxShares:101,maxReorderEntries:200}},{revision:'1'},{viewerFence:''},{actions:new Array(17).fill('add')}]){const denied=setup(async p=>isContent(p)?projection():{...resource(),...patch});await denied.select({view:'playlist',playlistId:'playlist'});assert.equal(denied.getSnapshot().error?.code,'invalid_saved',JSON.stringify(patch));assert.equal(denied.getSnapshot().playlist,null);}
});
test('global requests consume server order and declared query without local ranking',async()=>{
 let path='';const s=setup(async p=>{path=p;const d=projection('favorites',0,[media('z'),media('a')]);d.query.sort='added';d.query.direction='desc';return d;});await s.select({view:'favorites',sort:'added',direction:'desc'});
 assert.equal(path,'/v1/content?limit=40&view=favorites&sort=added&direction=desc');assert.deepEqual(s.getSnapshot().projection!.sections[0].entries.map(e=>e.id),['z','a']);
});
test('playlist resource/content torn revision never authorizes reorder',async()=>{
 const s=setup(async p=>isContent(p)?projection():resource(1));await s.select({view:'playlist',playlistId:'playlist'});assert.equal(s.getSnapshot().phase,'refresh-required');assert.equal(s.getSnapshot().playlist,null);assert.throws(()=>s.mutate({action:'reorder',entryIds:['entry']}));
});
test('scope changes abort stale reads even when the adapter ignores abort',async()=>{
 const old=deferred();const s=setup(async()=>old.promise);const first=s.select({view:'watchlist'});s.setScope({...scope,viewerId:'other'},{request:async<T>()=>projection('favorites',0,[]) as T});await s.select({view:'favorites'});old.resolve(projection('watchlist',0,[media()]));await first;assert.equal(s.getSnapshot().projection!.scope.view,'favorites');
});
test('a next page read after the list moved is still a page, with no refresh state',async()=>{
 let revision=0;const requests:string[]=[];const s=setup(async p=>{requests.push(p);return projection('watchlist',revision,[media()], p.includes('cursor')?'':'next');});await s.select({view:'watchlist'});revision=1;await s.next('entries');assert.equal(s.getSnapshot().phase,'ready');assert.equal(s.getSnapshot().projection!.revision.catalog,1);assert.equal(s.getSnapshot().pagination.cursor,'next');assert.equal(requests.length,2);
});
test('failed page retry repeats the requested cursor and successful Back restores prior page',async()=>{
 let fail=true;const s=setup(async p=>{if(p.includes('cursor')&&fail){fail=false;throw new Error('network');}return projection('watchlist',0,[media(p.includes('cursor')?'second':'first')],p.includes('cursor')?'':'next');});await s.select({view:'watchlist'});await s.next('entries');assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().projection!.sections[0].entries[0].id,'first');await s.retry();assert.equal(s.getSnapshot().projection!.sections[0].entries[0].id,'second');await s.previous();assert.equal(s.getSnapshot().projection!.sections[0].entries[0].id,'first');
});
test('deliberate duplicate Adds are separate occurrences; serialized CAS uses refreshed revisions',async()=>{
 let revision=0;const writes:any[]=[];const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection('playlist',revision):resource(revision);writes.push(b);return receipt(b,++revision);});await s.select({view:'playlist',playlistId:'playlist'});const a=s.mutate({action:'add',itemId:'movie'});const b=s.mutate({action:'add',itemId:'movie'});await Promise.all([a,b]);await tick();assert.equal(writes.length,2);assert.notEqual(writes[0].operationId,writes[1].operationId);assert.deepEqual(writes.map(x=>x.expectedRevision),[0,1]);
});
test('lost mutation response retries identical operation/body rather than adding twice',async()=>{
 const writes:any[]=[];const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection():resource();writes.push(b);if(writes.length===1)return new Promise(()=>{});return receipt(b);},10);await s.select({view:'playlist',playlistId:'playlist'});await assert.rejects(s.mutate({action:'add',itemId:'movie'}),/timed out/);assert.deepEqual(s.getSnapshot().pending,[]);assert.equal(s.getSnapshot().mutationError!.code,'timeout');await s.retryMutation();await tick();assert.deepEqual(writes[0],writes[1]);assert.equal(s.getSnapshot().lastReceipt!.entryId,'new-entry');
});
test('a playlist conflict preserves the original draft and only a reviewed retry authors a new operation',async()=>{
 let revision=3;const writes:any[]=[];const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection('playlist',revision):resource(revision);writes.push(b);if(writes.length===1){revision=4;throw Object.assign(new Error('Changed'),{code:'revision_mismatch',status:412});}return receipt(b,++revision);});
 await s.select({view:'playlist',playlistId:'playlist'});await assert.rejects(s.mutate({action:'rename',name:'New'}),/Changed/);await tick();
 assert.equal(writes.length,1);assert.equal(writes[0].expectedRevision,3);assert.equal(s.getSnapshot().mutationError?.code,'revision_mismatch');assert.equal(s.getSnapshot().playlist!.revision,4);
 await s.retryMutation();await tick();assert.equal(writes.length,2);assert.notEqual(writes[0].operationId,writes[1].operationId);assert.equal(writes[1].expectedRevision,4);assert.equal(s.getSnapshot().mutationError,null);s.dispose();
});
test('repeated playlist conflicts each retain their own refused revision',async()=>{
 let revision=0;const writes:any[]=[];let losing=true;const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection('playlist',revision):resource(revision);writes.push(b);if(losing){revision++;throw Object.assign(new Error('Changed'),{code:'revision_mismatch',status:412});}return receipt(b,++revision);});await s.select({view:'playlist',playlistId:'playlist'});
 await assert.rejects(s.mutate({action:'rename',name:'New'}),/Changed/);await tick();assert.equal(writes.length,1);assert.equal(s.getSnapshot().playlist!.revision,1);assert.deepEqual(s.getSnapshot().pending,[]);
 losing=false;await s.retryMutation();await tick();assert.notEqual(writes[0].operationId,writes[1].operationId);assert.equal(writes[1].expectedRevision,1);s.dispose();
});
test('editor cannot rename/share/delete and an unwitnessed partial-page reorder is rejected before transport',async()=>{
 let writes=0;const s=setup(async(p,m)=>{if(m!=='GET')writes++;const r=resource(0,3);r.role='editor';r.actions=['add','remove','reorder'];delete (r as any).shares;const page=projection();delete (page.sections[0] as any).order;return isContent(p)?page:r;});await s.select({view:'playlist',playlistId:'playlist'});assert.throws(()=>s.mutate({action:'rename',name:'X'}));assert.throws(()=>s.mutate({action:'delete'}));assert.throws(()=>s.mutate({action:'reorder',entryIds:['entry']}));assert.equal(writes,0);
});
test('deletion clears pending actions and cannot resurrect detail on late read',async()=>{
 let deleted=false;const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection():resource();deleted=true;return {...receipt(b),deleted:true};});await s.select({view:'playlist',playlistId:'playlist'});await s.mutate({action:'delete'});await tick();assert(deleted);assert.equal(s.getSnapshot().playlist,null);assert.equal(s.getSnapshot().projection,null);assert(s.getSnapshot().lastReceipt!.deleted);
});
test('a share candidate this app cannot read is left out; scope, revision and size still refuse the page',async()=>{
 const candidate={id:'candidate',authority:'local',accountId:'account2',profileId:'profile2',displayName:'Second'};
 const page=(patch:any={})=>({serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,candidates:[candidate,{...candidate,id:'robot',authority:'galactic'},{...candidate,displayName:'Again'},{id:'nameless'}],nextCursor:'',futureField:1,...patch});
 const s=setup(async p=>p.includes('share-candidates')?page():isContent(p)?projection():resource());await s.select({view:'playlist',playlistId:'playlist'});await s.loadCandidates();assert.deepEqual(s.getSnapshot().candidates!.candidates,[candidate]);
 for(const patch of [{serverId:'other'},{viewerFence:'other'},{revision:'0'},{nextCursor:7},{candidates:new Array(41).fill(candidate)},{candidates:{}}]){const denied=setup(async p=>p.includes('share-candidates')?page(patch):isContent(p)?projection():resource());await denied.select({view:'playlist',playlistId:'playlist'});await denied.loadCandidates();assert.equal(denied.getSnapshot().candidates,null);assert(denied.getSnapshot().candidatesError,JSON.stringify(patch));}
});
test('candidate page has exact scope, owner rights and bounded entries',async()=>{
 const candidate={id:'candidate',authority:'local',accountId:'account2',profileId:'profile2',displayName:'Profile …profile2'};const s=setup(async p=>p.includes('share-candidates')?{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,candidates:[candidate],nextCursor:''}:isContent(p)?projection():resource());await s.select({view:'playlist',playlistId:'playlist'});await s.loadCandidates();assert.deepEqual(s.getSnapshot().candidates!.candidates,[candidate]);assert.throws(()=>s.mutate({action:'reorder',entryIds:['x','x']}));
});
test('late mutation and queued commands cannot publish across item or profile transition',async()=>{
 const pending=deferred();let body:any;const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection():resource();body=b;return pending.promise;});await s.select({view:'playlist',playlistId:'playlist'});const first=s.mutate({action:'add',itemId:'movie'});await tick();const second=s.mutate({action:'add',itemId:'movie'});s.cancel();await Promise.all([first,second]);pending.resolve(receipt(body));await tick();assert.equal(s.getSnapshot().phase,'idle');assert.equal(s.getSnapshot().lastReceipt,null);
});

test('directory create obeys server capability; receipt points to created resource without invented rows',async()=>{
 let available=false;let wrote=false;const s=setup(async(p,m,b)=>{if(m==='GET'){const d=projection('playlists',0,[]);(d as any).actions=available?['create_playlist']:[];return d;}wrote=true;return receipt(b,0);});await s.select({view:'playlists'});assert.throws(()=>s.mutate({action:'create',name:'New'}));available=true;await s.refresh();await s.mutate({action:'create',name:'New',summary:'A summary'});await tick();assert(wrote);assert.equal(s.getSnapshot().lastReceipt!.playlistId,'playlist');assert.equal(s.getSnapshot().projection!.sections[0].entries.length,0);
});

test('confirmed revision cannot be replaced by an older post-mutation read',async()=>{
 const s=setup(async(p,m,b)=>m==='GET'?(isContent(p)?projection():resource()):receipt(b,2));await s.select({view:'playlist',playlistId:'playlist'});await s.mutate({action:'rename',name:'New'});await tick();assert.equal(s.getSnapshot().phase,'refresh-required');assert.equal(s.getSnapshot().lastReceipt!.revision,2);assert.throws(()=>s.mutate({action:'delete'}));
});
test('Unicode names and multiline summaries survive server resource projection',async()=>{
 const r=resource();r.name='🎵'.repeat(200);r.summary='First paragraph.\n\nSecond paragraph.';const s=setup(async p=>isContent(p)?projection():r);await s.select({view:'playlist',playlistId:'playlist'});assert.equal(s.getSnapshot().playlist!.name,r.name);assert.equal(s.getSnapshot().playlist!.summary,r.summary);assert.throws(()=>s.mutate({action:'rename',summary:'x'.repeat(4001)}));
});

test('windowed order read authorizes the occurrences it carries, without ranking',async()=>{
 const ids=Array.from({length:60},(_,i)=>'entry-'+i);let body:any;const s=setup(async(p,m,b)=>{if(m==='PUT'){body=b;return receipt(b,1);}if(p.endsWith('/order')||p.includes('/order?'))return{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:ids,nextCursor:''};return isContent(p)?projection('playlist',0,ids.slice(0,40).map(occurrence),'next'):resource(0,60);});await s.select({view:'playlist',playlistId:'playlist'});
 assert.throws(()=>s.mutate({action:'reorder',entryIds:ids}),/Load the current/);await s.loadOrder();assert.deepEqual(s.getSnapshot().order!.entryIds,ids);assert.equal(s.getSnapshot().order!.nextCursor,'');assert(Object.isFrozen(s.getSnapshot().order!.entryIds));await s.mutate({action:'reorder',entryIds:[...ids].reverse()});assert.equal(body.expectedRevision,0);assert.deepEqual(body.entryIds,[...ids].reverse());assert(!('afterEntryId' in body));
});
test('windowed reorder moves occurrences after an anchor, or prepends on empty anchor',async()=>{
 const ids=['a','b','c','d'];let body:any,rev=0;const s=setup(async(p,m,b)=>{if(m==='PUT'){body=b;return receipt(b,++rev);}if(p.includes('/order'))return{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:rev,entryIds:ids,nextCursor:''};return isContent(p)?projection('playlist',rev,ids.map(occurrence)):resource(rev,4);});await s.select({view:'playlist',playlistId:'playlist'});await s.loadOrder();
 await s.mutate({action:'reorder',entryIds:['d'],afterEntryId:'b'});assert.deepEqual(body.entryIds,['d']);assert.equal(body.afterEntryId,'b');
 await s.mutate({action:'reorder',entryIds:['d'],afterEntryId:''});assert.equal(body.afterEntryId,'');
 assert.throws(()=>s.mutate({action:'reorder',entryIds:['d'],afterEntryId:'d'}));assert.throws(()=>s.mutate({action:'reorder',entryIds:['d'],afterEntryId:'unseen'}));assert.throws(()=>s.mutate({action:'reorder',entryIds:new Array(201).fill('a')}));
});
test('order windows page and append; a revision move restarts from the first page',async()=>{
 const first=Array.from({length:100},(_,i)=>'entry-'+i),second=['entry-100','entry-101'];let revision=0;const s=setup(async p=>{if(p.includes('/order')){if(revision===1)return{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:1,entryIds:first,nextCursor:''};return p.includes('cursor=')?{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:second,nextCursor:''}:{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:first,nextCursor:'cursor-2'};}return isContent(p)?projection('playlist',0,first.slice(0,40).map(occurrence),'next'):resource(0,102);});await s.select({view:'playlist',playlistId:'playlist'});
 await s.loadOrder();assert.equal(s.getSnapshot().order!.entryIds.length,100);assert.equal(s.getSnapshot().order!.nextCursor,'cursor-2');
 await s.loadMoreOrder();assert.equal(s.getSnapshot().order!.entryIds.length,102);assert.equal(s.getSnapshot().order!.nextCursor,'');
 await s.loadMoreOrder();assert.equal(s.getSnapshot().order!.entryIds.length,102);
 revision=1;await s.loadOrder();assert.equal(s.getSnapshot().order,null);assert(s.getSnapshot().orderError);
});
test('order rejects torn revision, wrong fence, over-page windows and unreadable occurrence IDs',async()=>{
 const full=new Array(101).fill('entry');
 for(const patch of [{revision:1},{viewerFence:'other'},{entryIds:full},{entryIds:['entry','']},{entryIds:['entry',7]},{nextCursor:7}]){const s=setup(async p=>p.includes('/order')?{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:['entry'],nextCursor:'',...patch}:isContent(p)?projection():resource());await s.select({view:'playlist',playlistId:'playlist'});await s.loadOrder();assert.equal(s.getSnapshot().order,null);assert(s.getSnapshot().orderError);assert.equal(s.getSnapshot().orderLoading,false);}
});
test('an order window ignores a field it does not know and keeps the first of a repeated occurrence ID',async()=>{
 const s=setup(async p=>p.includes('/order')?{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:['entry','other','entry'],nextCursor:'',media:{title:'Private'}}:isContent(p)?projection():resource());await s.select({view:'playlist',playlistId:'playlist'});await s.loadOrder();
 assert.equal(s.getSnapshot().orderError,null);assert.deepEqual(s.getSnapshot().order,{serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:['entry','other'],nextCursor:''});
});
test('late order-window response is fenced after scope change and cancellation',async()=>{
 for(const transition of ['scope','cancel']){const pending=deferred();const s=setup(async p=>p.includes('/order')?pending.promise:isContent(p)?projection():resource());await s.select({view:'playlist',playlistId:'playlist'});const read=s.loadOrder();if(transition==='scope')s.setScope({...scope,viewerId:'other'},{request:async<T>()=>projection('watchlist',0,[]) as T});else s.cancel();await read;pending.resolve({serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:['entry'],nextCursor:''});await tick();assert.equal(s.getSnapshot().order,null);assert.equal(s.getSnapshot().orderLoading,false);}
});
test('viewer cannot fetch order windows and unknown occurrence IDs cannot be moved',async()=>{
 let calls=0;const s=setup(async p=>{calls++;const r=resource();r.role='viewer';r.actions=[];delete(r as any).shares;return isContent(p)?projection():r;});await s.select({view:'playlist',playlistId:'playlist'});await assert.rejects(()=>s.loadOrder(),/editable/);assert.equal(calls,2);
 const editable=setup(async p=>isContent(p)?projection():resource());await editable.select({view:'playlist',playlistId:'playlist'});assert.throws(()=>editable.mutate({action:'reorder',entryIds:['unseen']}),/current playlist order/);
});
test('reorder retains selected revision across delayed operation-ID generation',async()=>{
 const requestId=deferred();let revision=0,body:any;const s=new SavedService({scope,requestId:()=>requestId.promise,api:{request:async<T>(p:string,m?:string,b?:unknown)=>{if(m==='PUT'){body=b;return receipt(b,2) as T;}return wireFixture(isContent(p)?projection('playlist',revision):resource(revision),{startSeconds:'startMs'}) as T;}}});await s.select({view:'playlist',playlistId:'playlist'});const mutation=s.mutate({action:'reorder',entryIds:['entry']});revision=1;await s.refresh();requestId.resolve('00000000-0000-4000-8000-000000000099');await mutation;assert.equal(body.expectedRevision,0);
});
test('uncapped playlists read past 1,000 entries through order windows',async()=>{
 const ids=Array.from({length:1050},(_,i)=>'entry-'+i);
 const window=(offset:number)=>({serverId:'server',viewerFence:'fence',playlistId:'playlist',revision:0,entryIds:ids.slice(offset,offset+100),nextCursor:offset+100>=ids.length?'':'c'+(offset+100)});
 const r=resource(0,1050);delete (r.limits as any).maxEntries;r.limits.maxReorderEntries=200;
 const s=setup(async p=>{const at=p.match(/cursor=c(\d+)/);return p.includes('/order')?window(at?Number(at[1]):0):isContent(p)?projection('playlist',0,ids.slice(0,40).map(occurrence),'next'):r;});
 await s.select({view:'playlist',playlistId:'playlist'});await s.loadOrder();
 assert.equal(s.getSnapshot().playlist!.entryCount,1050);assert.equal(s.getSnapshot().playlist!.limits.maxEntries,undefined);
 for(let page=0;page<10;page++)await s.loadMoreOrder();
 assert.equal(s.getSnapshot().order!.entryIds.length,1050);assert.equal(s.getSnapshot().order!.nextCursor,'');
 await s.loadMoreOrder();assert.equal(s.getSnapshot().order!.entryIds.length,1050);
});

test('Saved page2 detail cancellation and scoped restore preserves exact cursor and Back page',async()=>{
 const paths:string[]=[];const s=setup(async p=>{paths.push(p);return projection('watchlist',0,[media(p.includes('cursor=second')?'second':'first')],p.includes('cursor=second')?'':'second');});await s.select({view:'watchlist'});await s.next('entries');const saved=s.getSnapshot();assert.deepEqual(saved.pagination.history,[null]);s.cancel();await s.select(saved.route!,{scope:saved.scope,cursor:saved.pagination.cursor,history:saved.pagination.history});assert(paths.at(-1)!.includes('cursor=second'));assert.equal(s.getSnapshot().projection!.sections[0].entries[0].id,'second');assert.deepEqual(s.getSnapshot().pagination.history,[null]);await s.previous();assert.equal(s.getSnapshot().projection!.sections[0].entries[0].id,'first');
});
test('an expired restored continuation reads the first page instead, with no refresh state',async()=>{
 const paths:string[]=[];const phases:string[]=[];const s=setup(async p=>{paths.push(p);if(p.includes('cursor='))throw Object.assign(new Error('Expired'),{code:'invalid_cursor'});return projection('favorites',0,[media()]);});s.subscribe(()=>phases.push(s.getSnapshot().phase));await s.select({view:'favorites'},{scope,cursor:'expired',history:[null]});assert.equal(s.getSnapshot().phase,'ready');assert(!phases.includes('refresh-required')&&!phases.includes('error'));assert(!paths.at(-1)!.includes('cursor='));assert.equal(paths.length,2);assert.deepEqual(s.getSnapshot().pagination.history,[]);
});
test('restore bounds and foreign viewer/server scope reject before transport',async()=>{
 let calls=0;const s=setup(async()=>{calls++;return projection('watchlist',0,[]);});for(const restore of [{cursor:'x'},{scope:{...scope,viewerId:'other'},cursor:'x'},{scope:{...scope,serverId:'other'},cursor:'x'},{scope,cursor:'x',history:Array(65).fill(null)},{scope,cursor:'x'.repeat(4097)},{scope,cursor:null,history:[null]}])assert.throws(()=>s.select({view:'watchlist'},restore));assert.equal(calls,0);s.setScope({...scope,viewerId:'new'},{request:async<T>()=>projection('watchlist',0,[]) as T});assert.throws(()=>s.select({view:'watchlist'},{scope,cursor:'old'}));
});
test('ignored-abort restored page cannot publish after a newer Saved selection',async()=>{
 const pending=deferred();const s=setup(async p=>p.includes('cursor=old')?pending.promise:projection('favorites',0,[media('new')]));const old=s.select({view:'watchlist'},{scope,cursor:'old',history:[null]});await s.select({view:'favorites'});pending.resolve(projection('watchlist',0,[media('old')]));await old;assert.equal(s.getSnapshot().projection!.scope.view,'favorites');assert.equal(s.getSnapshot().projection!.sections[0].entries[0].id,'new');assert.deepEqual(s.getSnapshot().pagination.history,[]);
});

test('playlist selection resumes an ambiguous addition without duplicating confirmed entries',async()=>{
 const {appendPlaylistSelection}=await import('../src/playlist-selection.ts');
 let revision=0,completed=0,lost=true;const stored=new Map<string,any>(),items:string[]=[],writes:any[]=[];
 const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection('playlist',revision,items.map((_,i)=>occurrence('e'+i))):resource(revision,items.length);
 writes.push(b);if(stored.has(b.operationId))return stored.get(b.operationId);
 items.push(b.itemId);const result=receipt(b,++revision);stored.set(b.operationId,result);
 if(b.itemId==='second'&&lost){lost=false;throw new TypeError('Connection lost');}return result;});
 await s.select({view:'playlist',playlistId:'playlist'});
 await assert.rejects(appendPlaylistSelection(s,['first','second','third'],completed,n=>completed=n));
 assert.equal(completed,1);assert.deepEqual(items,['first','second']);
 await appendPlaylistSelection(s,['first','second','third'],completed,n=>completed=n);
 assert.equal(completed,3);assert.deepEqual(items,['first','second','third']);assert.equal(writes[1].operationId,writes[2].operationId);
 s.dispose();
});
test('playlist selection stops at a changed principal or route',async()=>{
 const {appendPlaylistSelection}=await import('../src/playlist-selection.ts');
 const response=deferred();let writes=0,body:any;const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection():resource();writes++;body=b;return response.promise;});
 await s.select({view:'playlist',playlistId:'playlist'});let progress=0;
 const run=appendPlaylistSelection(s,['one','two'],0,n=>progress=n);await tick();s.cancel();response.resolve(receipt(body));await run;assert.equal(progress,0);assert.equal(writes,1);s.dispose();
});


import {readPlaylistSelection} from '../src/playlist-selection.ts';
test('Play all reads every page, preserves repeated media occurrences and skips inaccessible entries',async()=>{
 const paths:string[]=[];const s=setup(async p=>{paths.push(p);return isContent(p)?projection('playlist',0,p.includes('cursor')?[occurrence('third')]:[occurrence('first'),{id:'hidden',kind:'playlist_entry',hidden:true}],p.includes('cursor')?'':'next'):resource(0,3);});
 const result=await readPlaylistSelection(s,'playlist');assert.equal(result.unavailable,1);assert.deepEqual(result.entries.map(e=>e.itemId),['movie','movie']);assert.deepEqual(result.entries.map(e=>e.sourceContext?.entryId),['first','third']);assert(paths.some(p=>p.includes('cursor=next')));s.dispose();
});
test('Play all refuses changed revisions, incomplete counts and repeated pages',async()=>{
 for(const failure of ['revision','count','repeat']){let page=0;const s=setup(async p=>{if(isContent(p)){page=p.includes('cursor')?1:0;return projection('playlist',failure==='revision'?page:0,[occurrence(page&&failure!=='repeat'?'second':'first')],!page&&failure!=='count'?'next':'');}return resource(failure==='revision'?page:0,2);});await assert.rejects(readPlaylistSelection(s,'playlist'));s.dispose();}
});
test('Play all never returns a cancelled or oversized selection',async()=>{
 const abort=new AbortController();abort.abort();const s=setup(async p=>isContent(p)?projection():resource());await assert.rejects(readPlaylistSelection(s,'playlist',abort.signal),/cancelled/);s.dispose();
 const big=setup(async p=>isContent(p)?projection():resource(0,1001));await assert.rejects(readPlaylistSelection(big,'playlist'),{code:'invalid_saved'});big.dispose();
});

test('CD-17: selecting, paging and reloading each filter produces the intended URL/query',async()=>{
 for(const filter of ['all','unwatched','inProgress'] as const){
  const paths:string[]=[];
  const s=setup(async p=>{paths.push(p);const url=new URL(p,'https://x');const cursor=url.searchParams.get('cursor');return projection('watchlist',0,[media(cursor?`second-${filter}`:`first-${filter}`)],cursor?'':`next-${filter}`,filter);});
  await s.select({view:'watchlist',filter});
  assert.equal(s.getSnapshot().phase,'ready');
  const first=new URL(paths[0]!,'https://x');
  assert.equal(first.searchParams.get('view'),'watchlist');
  const sent=first.searchParams.get('filter')??'all';
  assert.equal(sent,filter);
  assert.equal(s.getSnapshot().projection!.query.category,filter);
  assert.equal(s.getSnapshot().projection!.filters[0].options.length,3);
  await s.next('entries');
  assert(paths.at(-1)!.includes(`cursor=next-${filter}`));
  const paged=new URL(paths.at(-1)!,'https://x');
  assert.equal(paged.searchParams.get('filter')??'all',filter);
  assert.equal(s.getSnapshot().projection!.query.category,filter);
  await s.refresh();
  assert(!paths.at(-1)!.includes('cursor='));
  assert.equal(s.getSnapshot().projection!.query.category,filter);
  s.dispose();
 }
 // Late responses cannot overwrite another filter: selecting unwatched then all keeps all.
 {
  const pending=new Map<string,()=>void>();
  const s=setup(async p=>{
   const url=new URL(p,'https://x');const f=url.searchParams.get('filter')??'all';
   if(f==='unwatched')await new Promise<void>(r=>pending.set('unwatched',()=>r()));
   return projection('watchlist',0,[media(f)],'',f as any);
  });
  const first=s.select({view:'watchlist',filter:'unwatched'});
  await tick();
  await s.select({view:'watchlist',filter:'all'});
  assert.equal(s.getSnapshot().projection!.query.category,'all');
  pending.get('unwatched')!();await first;
  assert.equal(s.getSnapshot().projection!.query.category,'all');
  s.dispose();
 }
 // A filter another view does not offer is rejected before transport; the list of playlists has its own groups.
 {
  const s=setup(async()=>projection('playlists',0,[]));
  assert.throws(()=>s.select({view:'playlists',filter:'unwatched' as any}));
  assert.throws(()=>s.select({view:'collections',filter:'all' as any}));
  assert.doesNotThrow(()=>void s.select({view:'playlists',filter:'mine'}).catch(()=>{}));
  assert.throws(()=>s.select({view:'watchlist',filter:'everything' as any}));
  s.dispose();
 }
 // A mismatched echo fails closed.
 {
  const s=setup(async()=>projection('watchlist',0,[media()],'','unwatched'));
  await s.select({view:'watchlist',filter:'all'});
  assert.equal(s.getSnapshot().phase,'error');
  s.dispose();
 }
});

test('CD-17 response preservation: server filter echo and counts survive decoding',async()=>{
 const s=setup(async()=>projection('favorites',0,[media()],'' ,'unwatched'));
 await s.select({view:'favorites',filter:'unwatched'});
 assert.equal(s.getSnapshot().phase,'ready');
 assert.equal(s.getSnapshot().projection!.query.category,'unwatched');
 assert.equal(s.getSnapshot().projection!.filters[0].id,'filter');
 assert.deepEqual(s.getSnapshot().projection!.filters[0].options.map(o=>o.id),['all','unwatched','inProgress']);
 s.dispose();
});

test('CD-23: watchlist/history entries accept extra, author, book_series and disc',async()=>{
 const kinds: Record<string,string> = {
  extra:'item', author:'author', book_series:'book_series', disc:'disc',
 };
 for(const [kind,view] of Object.entries(kinds)){
  const item={id:`id-${kind}`,libraryId:'library',kind,title:`Title ${kind}`,navigation:{view,entityId:`id-${kind}`}};
  const s=setup(async()=>projection('watchlist',0,[item],'' ,'all'));
  await s.select({view:'watchlist'});
  assert.equal(s.getSnapshot().phase,'ready');
  assert.equal((s.getSnapshot().projection!.sections[0].entries[0] as any).kind,kind);
  s.dispose();
 }
 // An entry of a kind this app does not know is left out; it is never shown as something else.
 {
  const s=setup(async()=>projection('watchlist',0,[{id:'x',libraryId:'library',kind:'podcast',title:'X'}],'' ,'all'));
  await s.select({view:'watchlist'});
  assert.equal(s.getSnapshot().phase,'ready');
  assert.deepEqual(s.getSnapshot().projection!.sections[0].entries,[]);
  s.dispose();
 }
});
test('a removal answers its undo token, and Undo sends it back to put that occurrence back',async()=>{
 const writes:any[]=[];let revision=0;
 const s=setup(async(p,m,b)=>{if(m==='GET')return isContent(p)?projection('playlist',revision,[occurrence('one'),occurrence('two')]):resource(revision,2);writes.push({p,m,b});revision++;return m==='DELETE'?{...receipt(b,revision),undoToken:'undo-1',undoUntil:'2026-10-15T12:00:30.000Z'}:receipt(b,revision);});
 await s.select({view:'playlist',playlistId:'playlist'});
 await s.mutate({action:'remove',entryId:'two'});
 assert.equal(s.getSnapshot().lastReceipt?.undoToken,'undo-1');assert.equal(s.getSnapshot().lastReceipt?.undoUntil,'2026-10-15T12:00:30.000Z');
 await s.mutate({action:'restore',undoToken:'undo-1'});
 assert.equal(writes.length,2);assert.equal(writes[1].m,'POST');assert.match(writes[1].p,/\/v1\/playlists\/playlist\/entries$/);assert.equal(writes[1].b.undoToken,'undo-1');assert.equal(writes[1].b.itemId,undefined);
 assert.throws(()=>s.mutate({action:'restore',undoToken:''} as any));
});
test('a playlist paged past its first page is read again through every page on screen, as one first page',async()=>{
 let ids=Array.from({length:90},(_,i)=>'e'+i);const reads:string[]=[];
 const s=setup(async p=>{
  if(!isContent(p))return resource(0,ids.length);
  const at=Number(new URL('http://x'+p).searchParams.get('cursor')??0);reads.push(String(at));
  return projection('playlist',0,ids.slice(at,at+40).map(occurrence),at+40<ids.length?String(at+40):'');
 });
 await s.select({view:'playlist',playlistId:'playlist'});await s.next('entries');await s.next('entries');
 assert.equal(s.getSnapshot().pagination.cursor,'80');reads.length=0;
 ids=ids.filter(id=>id!=='e50');await s.revalidate();
 assert.deepEqual(reads,['0','40','80']);assert.equal(s.getSnapshot().pagination.cursor,null);assert.deepEqual(s.getSnapshot().pagination.history,[]);
 const shown=s.getSnapshot().projection!.sections[0].entries.map(e=>e.id);assert.equal(shown.length,89);assert(!shown.includes('e50'));
 assert.deepEqual(s.getSnapshot().pagination.next,[]);
 // Shown as one long first page now: the next change reads it all again too, never only its first 40.
 reads.length=0;ids=['n',...ids];await s.revalidate();assert.deepEqual(reads,['0','40','80']);assert.equal(s.getSnapshot().projection!.sections[0].entries.length,90);assert.equal(s.getSnapshot().phase,'ready');
});

test('Saved summary, shape paths and removed-title reads use typed routes and validate authority', async()=>{
 const {savedListPath,fetchSavedSummary,fetchRemovedTitle,dismissRemovedTitle}=await import('../src/saved.ts');
 assert.equal(savedListPath('watchlist',{shape:'squares',limit:37}),'/v1/content?view=watchlist&limit=37&shape=squares');
 assert.throws(()=>savedListPath('watchlist',{shape:'mixed' as never}));
 const calls:string[]=[];
 const response={serverId:'s',viewerFence:'f',revision:{catalog:3,viewer:2},myList:{posters:1203,squares:40},favorites:{posters:3,squares:1},playlists:2,collections:1,savedViews:4};
 const api={async request<T>(path:string,method?:string):Promise<T>{calls.push((method??'GET')+' '+path);return (path.endsWith('/summary')?response:method==='DELETE'?{removed:true}:{id:'gone',title:'Gone',kind:'movie',removedAt:'2026-10-22T00:00:00Z',canRemoveFromContinue:true}) as T;}};
 assert.equal((await fetchSavedSummary(api,{serverId:'s',viewerId:'p'})).myList.posters,1203);
 await assert.rejects(fetchSavedSummary(api,{serverId:'other',viewerId:'p'}));
 assert.equal((await fetchRemovedTitle(api,'gone')).title,'Gone');
 await dismissRemovedTitle(api,'gone');
 assert.deepEqual(calls,['GET /v1/me/saved/summary','GET /v1/me/saved/summary','GET /v1/me/removed-titles/gone','DELETE /v1/me/removed-titles/gone']);
});

test('a Reading Saved row with AV playback or duration is refused by the canonical reader',async()=>{
 for(const extra of [{playback:{itemId:'book'}},{durationMs:1000}]){
  const book={id:'book',libraryId:'library',kind:'ebook',title:'Book',available:true,author:'Author',pageCount:320,posterUrl:'/art/cover',navigation:{view:'item',entityId:'book'},...extra};
  const s=setup(async()=>projection('watchlist',0,[book]));await s.select({view:'watchlist'});
  assert.equal(s.getSnapshot().phase,'ready');assert.deepEqual(s.getSnapshot().projection!.sections[0].entries,[]);
  s.dispose();
 }
});
test('Reading Saved facts survive without AV progress or playback',async()=>{
 const book={id:'book',libraryId:'library',kind:'comic',title:'Book',available:true,author:'Author',series:[{name:'Series',position:'1'}],pageCount:120,posterUrl:'/art/cover',progressSeconds:.25,navigation:{view:'item',entityId:'book'}};
 const s=setup(async()=>projection('watchlist',0,[book]));await s.select({view:'watchlist'});
 const entry=s.getSnapshot().projection!.sections[0].entries[0] as any;
 assert.equal(entry.kind,'comic');assert.equal(entry.author,'Author');assert.equal(entry.pageCount,120);assert.deepEqual(entry.series,[{name:'Series',position:'1'}]);
 assert.equal(entry.posterUrl,'/art/cover');assert.equal(entry.navigation.view,'item');assert.equal('progressSeconds' in entry,false);assert.equal('playback' in entry,false);
 s.dispose();
});

test('reversed quiet Saved replies keep the later accepted answer',async()=>{
 const reads:ReturnType<typeof deferred>[]=[];let initial=true;
 const s=setup(async p=>{if(isContent(p)){if(initial){initial=false;return projection('watchlist',0,[media('a')]);}const read=deferred();reads.push(read);return read.promise;}throw new Error(p);});
 await s.select({view:'watchlist'});
 const older=s.revalidate(),newer=s.revalidate();
 reads[1]!.resolve(projection('watchlist',3,[media('new')]));
 await newer;
 reads[0]!.resolve(projection('watchlist',1,[media('old')]));
 await older;
 assert.equal(s.getSnapshot().projection!.sections[0].entries[0]!.id,'new','a delayed older answer cannot replace the later one');
 s.dispose();
});

test('a delayed failure from a superseded quiet Saved read leaves the successor shown',async()=>{
 const reads:ReturnType<typeof deferred>[]=[];let initial=true;
 const s=setup(async p=>{if(isContent(p)){if(initial){initial=false;return projection('watchlist',0,[media('a')]);}const read=deferred();reads.push(read);return read.promise;}throw new Error(p);});
 await s.select({view:'watchlist'});
 const older=s.revalidate(),newer=s.revalidate();
 reads[1]!.resolve(projection('watchlist',2,[media('new')]));
 await newer;
 reads[0]!.resolve(Promise.reject(Object.assign(new Error('forbidden'),{status:403,code:'forbidden'})));
 await older;
 assert.equal(s.getSnapshot().phase,'ready');
 assert.equal(s.getSnapshot().projection!.sections[0].entries[0]!.id,'new');
 assert.equal(s.getSnapshot().error,null);
 s.dispose();
});

test('a settle already pending cannot let its older Saved answer publish after a successor',async()=>{
 const reads:ReturnType<typeof deferred>[]=[];let initial=true,release!:()=>void;
 const s=setup(async p=>{if(isContent(p)){if(initial){initial=false;return projection('watchlist',0,[media('a')]);}const read=deferred();reads.push(read);return read.promise;}throw new Error(p);});
 await s.select({view:'watchlist'});
 const older=s.revalidate({settle:()=>new Promise<void>(resolve=>{release=resolve;})});
 reads[0]!.resolve(projection('watchlist',1,[media('mid')]));
 await tick();
 const newer=s.revalidate();
 reads[1]!.resolve(projection('watchlist',4,[media('new')]));
 await newer;
 release();await older;
 assert.equal(s.getSnapshot().projection!.sections[0].entries[0]!.id,'new','the post-settle check rejects the older commit');
 s.dispose();
});

test('an explicit refresh invalidates an older quiet Saved read',async()=>{
 const reads:ReturnType<typeof deferred>[]=[];let initial=true;
 const s=setup(async p=>{if(isContent(p)){if(initial){initial=false;return projection('watchlist',0,[media('a')]);}const read=deferred();reads.push(read);return read.promise;}throw new Error(p);});
 await s.select({view:'watchlist'});
 const quiet=s.revalidate();
 const refreshed=s.refresh();
 reads[1]!.resolve(projection('watchlist',5,[media('loaded')]));
 await refreshed;
 reads[0]!.resolve(projection('watchlist',1,[media('stale')]));
 await quiet;
 assert.equal(s.getSnapshot().projection!.sections[0].entries[0]!.id,'loaded');
 s.dispose();
});

test('a retired post-mutation deep failure does not trigger recovery; a current one still does',async()=>{
 const first=Array.from({length:41},(_,i)=>occurrence('e'+i));
 async function run(superseded:boolean,code='stale_continuation',post=false){
  let mode='first',deepReads:ReturnType<typeof deferred>[]=[],refreshes=0;
  const s=setup(async (p:string,method?:string,body?:any)=>{
   if(method==='PATCH'&&p.includes('/v1/playlists/playlist'))return receipt({operationId:body.operationId},2);
   if(p.includes('/content')){
    const cursor=new URL(p,'https://x').searchParams.get('cursor');
    if(mode==='first')return cursor?projection('playlist',0,[occurrence('last')],''):projection('playlist',0,first,'more');
    if(mode==='quiet'){ if(!cursor)return projection('playlist',2,first,'more'); const read=deferred();deepReads.push(read);return read.promise; }
    if(mode==='recover'||mode==='newer'){refreshes++;}
    return projection('playlist',5,[occurrence('newer')],'');
   }
   return resource(mode==='newer'||mode==='recover'?5:mode==='quiet'?2:0,41);
  });
  await s.select({view:'playlist',playlistId:'playlist'});
  await s.next('entries');
  assert.equal(s.getSnapshot().projection!.sections[0].entries.length,1);
  mode='quiet';
  const mutation=s.mutate({action:'rename',name:'Renamed'});
  for(let i=0;i<30&&!deepReads.length;i++)await tick();
  if(superseded){ mode='newer'; await s.refresh(); }
  assert.equal(deepReads.length,1,'the post-mutation deep read is in flight');
  refreshes=0;
  if(post){ mode='newer'; deepReads[0]!.resolve(Promise.reject(Object.assign(new Error(code),{code,retryable:true}))); queueMicrotask(()=>{void s.refresh();}); }
  else { if(!superseded) mode='recover'; deepReads[0]!.resolve(Promise.reject(Object.assign(new Error(code),{code,retryable:true}))); }
  await mutation.catch(()=>{});
  for(let i=0;i<20&&s.getSnapshot().projection!.sections[0].entries[0]?.id!=='newer';i++)await tick();
  const requestsAfter=refreshes;
  if(post){
   assert.equal(requestsAfter,1,'only the successor refresh runs after the retired failure');
   assert.equal(s.getSnapshot().projection!.sections[0].entries[0]!.id,'newer');
  }else if(superseded){
   assert.equal(requestsAfter,0,'a retired deep failure adds no recovery read');
   assert.equal(s.getSnapshot().projection!.sections[0].entries[0]!.id,'newer');
  }else{
   assert.ok(requestsAfter>=1,'a current deep failure still refreshes');
   assert.equal(s.getSnapshot().projection!.sections[0].entries[0]!.id,'newer');
  }
  s.dispose();
 }
 await run(true);
 await run(false);
 await run(true,'timeout');
 await run(false,'timeout');
 await run(true,'stale_continuation',true);
 await run(true,'timeout',true);
});

for (const retired of [false, true]) test(`actual private quiet deadline ${retired ? 'stays neutral after retirement' : 'recovers while still owned'}`, async t => {
 t.mock.timers.enable({apis: ['setTimeout']});
 const flush = async () => {for (let i = 0; i < 100; i++) await Promise.resolve();};
 const first = Array.from({length: 41}, (_, i) => occurrence(`deadline-${i}`));
 let mode = 'initial', tailSignal: AbortSignal | undefined, recoveryReads = 0;
 const service = setup(async (path, method, body, signal) => {
  if (method === 'PATCH') return receipt(body, 2);
  if (path.includes('/content')) {
   const cursor = new URL(path, 'https://x').searchParams.get('cursor');
   if (mode === 'initial') return cursor ? projection('playlist', 0, [occurrence('last')]) : projection('playlist', 0, first, 'more');
   if (mode === 'quiet') {
    if (!cursor) return projection('playlist', 2, first, 'more');
    tailSignal = signal; return new Promise(() => {});
   }
   recoveryReads++; return projection('playlist', 5, [occurrence('newer')]);
  }
  return resource(mode === 'initial' ? 0 : mode === 'quiet' ? 2 : 5, 42);
 }, 50);
 try {
  await service.select({view: 'playlist', playlistId: 'playlist'}); await service.next('entries');
  mode = 'quiet'; const mutation = service.mutate({action: 'rename', name: 'Renamed'});
  await flush(); assert(tailSignal); assert.equal(tailSignal.aborted, false);
  if (retired) {mode = 'successor'; await service.refresh(); recoveryReads = 0;}
  mode = 'recovery'; t.mock.timers.tick(50); await flush(); await mutation;
  assert.equal(tailSignal.aborted, true, 'the real deadline aborts its own HTTP controller');
  assert.equal(recoveryReads, retired ? 0 : 1, 'only a still-owned timeout admits a recovery head');
  assert.equal(service.getSnapshot().projection!.sections[0].entries[0]!.id, 'newer');
 } finally {service.dispose(); t.mock.timers.reset();}
});

test('a successor installed after real quietThrough returns but before pump resumes owns the only recovery head', async () => {
 const first = Array.from({length: 41}, (_, i) => occurrence(`returned-${i}`));
 let mode = 'initial', recoveryReads = 0, successor: Promise<void> | undefined, crossed = false;
 const service = setup(async (path, method, body) => {
  if (method === 'PATCH') return receipt(body, 2);
  if (path.includes('/content')) {
   const cursor = new URL(path, 'https://x').searchParams.get('cursor');
   if (mode === 'initial') return cursor ? projection('playlist', 0, [occurrence('last')]) : projection('playlist', 0, first, 'more');
   if (mode === 'quiet') {
    if (!cursor) return projection('playlist', 2, first, 'more');
    throw Object.assign(new Error('original deep failure'), {code: 'stale_continuation', retryable: true});
   }
   recoveryReads++; return projection('playlist', 5, [occurrence('newer')]);
  }
  return resource(mode === 'initial' ? 0 : mode === 'quiet' ? 2 : 5, 42);
 });
 try {
  await service.select({view: 'playlist', playlistId: 'playlist'}); await service.next('entries');
  // Delegate to the real private operation, changing only the controlled
  // scheduling seam after it returns. No outcome or ownership is fabricated.
  const quiet = Reflect.get(service, 'quietThrough') as () => Promise<{recovered: boolean; current: () => boolean}>;
  assert.equal(typeof quiet, 'function');
  Reflect.set(service, 'quietThrough', async () => {
   const outcome = await quiet.call(service); assert.equal(outcome.recovered, false); assert.equal(outcome.current(), true);
   crossed = true; mode = 'successor'; successor = service.refresh(); assert.equal(outcome.current(), false);
   return outcome;
  });
  mode = 'quiet'; await service.mutate({action: 'rename', name: 'Renamed'}); await successor;
  assert.equal(crossed, true); assert.equal(recoveryReads, 1, 'the resumed pump cannot add a fallback over the accepted successor');
  assert.equal(service.getSnapshot().projection!.sections[0].entries[0]!.id, 'newer');
 } finally {service.dispose();}
});

for (const deep of [false, true]) for (const successorKind of ['select', 'refresh', 'quiet'] as const)
test(`receipt subscriber ${successorKind} owns its ${deep ? 'deep' : 'first-page'} successor without old mutation recovery`, async () => {
 const flush = async () => {for (let i = 0; i < 100; i++) await Promise.resolve();};
 const first = Array.from({length: deep ? 41 : 1}, (_, i) => occurrence(`receipt-${i}`)), pending = deferred();
 let mode = 'initial', installed = false, settled = false, successor: Promise<void> | undefined;
 const successorReads: Array<{path: string; signal?: AbortSignal}> = [];
 const service = setup(async (path, method, body, signal) => {
  if (method === 'PATCH') return receipt(body, 2);
  if (path.includes('/content')) {
   if (mode === 'successor') {successorReads.push({path, signal}); return pending.promise;}
   const cursor = new URL(path, 'https://x').searchParams.get('cursor');
   return cursor ? projection('playlist', 0, [occurrence('last')]) : projection('playlist', 0, first, deep ? 'more' : '');
  }
  return resource(mode === 'initial' ? 0 : 2, deep ? 42 : 1);
 });
 let off = () => {};
 try {
  await service.select({view: 'playlist', playlistId: 'playlist'}); if (deep) await service.next('entries');
  off = service.subscribe(() => {
   if (installed || !service.getSnapshot().lastReceipt) return;
   installed = true; mode = 'successor';
   successor = successorKind === 'select' ? service.select({view: 'favorites'}) : successorKind === 'refresh' ? service.refresh() : service.revalidate();
  });
  const mutation = service.mutate({action: 'rename', name: 'Committed'}).then(() => {settled = true;});
  await flush(); assert.equal(installed, true); assert.equal(settled, true, 'the committed original settles while its successor HTTP request is still pending');
  assert.equal(successorReads.length, 1, 'only the subscriber-owned head dispatches'); assert(successorReads[0].signal); assert.equal(successorReads[0].signal.aborted, false);
  const entries = successorKind === 'select' ? [media('newer')] : Array.from({length: deep && successorKind === 'quiet' ? 42 : 1}, (_, i) => occurrence(i ? `newer-${i}` : 'newer'));
  pending.resolve(projection(successorKind === 'select' ? 'favorites' : 'playlist', 2, entries));
  await Promise.all([mutation, successor]); await flush();
  assert.equal(successorReads.length, 1); assert.equal(successorReads[0].signal.aborted, false);
  assert.equal(service.getSnapshot().phase, 'ready'); assert.equal(service.getSnapshot().route?.view, successorKind === 'select' ? 'favorites' : 'playlist');
  assert.equal(service.getSnapshot().projection!.sections[0].entries[0]!.id, 'newer');
 } finally {off(); service.dispose();}
});

for (const successorKind of ['select', 'refresh', 'none'] as const)
test(`committed DELETE read-abort cleanup preserves ${successorKind === 'none' ? 'ordinary deletion' : `its ${successorKind} successor`}`, async () => {
 const flush = async () => {for (let i = 0; i < 100; i++) await Promise.resolve();};
 const pending = deferred(); let initialSignal: AbortSignal | undefined, successor: Promise<void> | undefined;
 let mode = 'initial', settled = false, aborts = 0, deletes = 0;
 const heads: Array<{path: string; signal?: AbortSignal}> = [];
 const service = setup(async (path, method, body, signal) => {
  if (method === 'DELETE') {
   deletes++; assert.equal(body.expectedRevision, 0);
   return {serverId: 'server', viewerFence: 'fence', operationId: body.operationId, playlistId: 'playlist', revision: 1, deleted: true};
  }
  if (path.includes('/content')) {
   if (mode === 'initial') {initialSignal = signal; return projection();}
   heads.push({path, signal}); return pending.promise;
  }
  return resource(mode === 'initial' ? 0 : 1);
 });
 try {
  await service.select({view: 'playlist', playlistId: 'playlist'}); assert(initialSignal); assert.equal(initialSignal.aborted, false);
  initialSignal.addEventListener('abort', () => {
   aborts++; if (successorKind === 'none') return;
   mode = 'successor'; successor = successorKind === 'select' ? service.select({view: 'favorites'}) : service.refresh();
  }, {once: true});
  const mutation = service.mutate({action: 'delete'}).then(() => {settled = true;});
  await flush(); assert.equal(deletes, 1); assert.equal(aborts, 1); assert.equal(settled, true, 'the committed command settles without waiting for a replacement read');
  if (successorKind === 'none') {
   await mutation; assert.equal(heads.length, 0); assert.equal(service.getSnapshot().phase, 'ready');
   assert.equal(service.getSnapshot().projection, null); assert.equal(service.getSnapshot().playlist, null);
  } else {
   assert.equal(heads.length, 1); assert(heads[0].signal); assert.equal(heads[0].signal.aborted, false);
   assert.equal(service.getSnapshot().phase, 'loading', 'old deletion cannot publish ready/null over a held successor');
   pending.resolve(projection(successorKind === 'select' ? 'favorites' : 'playlist', 1, successorKind === 'select' ? [media('newer')] : [occurrence('newer')]));
   await Promise.all([mutation, successor]); await flush();
   assert.equal(heads.length, 1); assert.equal(heads[0].signal.aborted, false); assert.equal(service.getSnapshot().phase, 'ready');
   assert.equal(service.getSnapshot().route?.view, successorKind === 'select' ? 'favorites' : 'playlist');
   assert.equal(service.getSnapshot().projection!.sections[0].entries[0]!.id, 'newer');
  }
 } finally {service.dispose();}
});

test('a page of the playlist’s own order witnesses moves across its edges; another order moves nothing',async()=>{
 const bodies:any[]=[];
 const witnessed=async()=>{const s=setup(async(p,m,b)=>{if(m!=='GET'){bodies.push(b);return receipt(b);}if(!isContent(p))return resource(0,90);const page=projection('playlist',0,[occurrence('a'),occurrence('b')],'more');(page.sections[0] as any).start=40;(page.sections[0] as any).order={previous:'before-page',next:'after-page',last:'playlist-tail'};return page;});await s.select({view:'playlist',playlistId:'playlist'});return s;};
 const s=await witnessed();
 assert.equal(s.getSnapshot().projection!.sections[0].start,40);
 assert.throws(()=>s.mutate({action:'reorder',entryIds:['a'],afterEntryId:'unseen'}));
 await s.mutate({action:'reorder',entryIds:['a'],afterEntryId:'playlist-tail'});
 await (await witnessed()).mutate({action:'reorder',entryIds:['a','before-page']});
 assert.deepEqual(bodies.map(b=>[b.entryIds,b.afterEntryId,b.expectedRevision]),[[['a'],'playlist-tail',0],[['a','before-page'],undefined,0]]);
 const sorted=setup(async p=>{if(!isContent(p))return resource(0,90);const page=projection('playlist',0,[occurrence('a')]);page.query.sort='title';page.query.direction='desc';delete (page.sections[0] as any).order;return page;});
 await sorted.select({view:'playlist',playlistId:'playlist',sort:'title',direction:'desc'});
 assert.equal(sorted.getSnapshot().phase,'ready');
 assert.throws(()=>sorted.mutate({action:'reorder',entryIds:['a'],afterEntryId:''}),/own order/);
 assert.throws(()=>s.select({view:'playlist',playlistId:'playlist',sort:'position',direction:'desc'}));
});
