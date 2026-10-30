import test from 'node:test';import assert from 'node:assert/strict';
import {SearchService,parseSearchHistory,searchQueryProblem,type SearchGroup} from '../src/search.ts';
const scope={serverId:'server',viewerId:JSON.stringify(['hosted','account','profile'])};
const kinds={movies:'movie',shows:'show',episodes:'episode',artists:'artist',albums:'album',songs:'song',books:'book',collections:'collection',authors:'author',people:'person','live-tv':'channel'} as const;
const all=Object.keys(kinds) as SearchGroup[];
const mediaGroups=all.filter(g=>g!=='people'&&g!=='live-tv');
const capabilities=(unavailable:SearchGroup[]=['live-tv'])=>({groups:all.map(id=>({id,title:id,entityKind:kinds[id],available:!unavailable.includes(id),...(unavailable.includes(id)?{reason:'Not configured on this server.'}:{}),sorts:['relevance','title']})),sorts:['relevance','title','releaseYear','dateAdded'],directions:['asc','desc'],maxLimit:40,groupBudgetMs:600});
function group(id:SearchGroup,ids=['one'],nextCursor=''):any{
 const entityKind=kinds[id];
 const view=['movies','episodes','songs'].includes(id)?'item':entityKind;
 const items=ids.map(entity=>id==='people'||id==='live-tv'
  ?{id:entity,kind:entityKind,title:'Server '+entity,navigation:{view,entityId:entity}}
  :{id:entity,libraryId:'library',kind:entityKind,title:'Server '+entity,navigation:{view,entityId:entity}});
 return {id,title:id,entityKind,status:'success',errorCode:'',items,totalCount:20,hasMore:!!nextCursor,nextCursor};
}
function failed(id:SearchGroup,errorCode='search_group_timeout'):any{return {id,title:id,entityKind:kinds[id],status:'error',errorCode,items:[],totalCount:0,hasMore:false,nextCursor:''};}
function result(path:string,changes:Record<string,unknown>={}):any{
 const url=new URL(path,'https://server.test'),selected=url.searchParams.get('group') as SearchGroup|null;
 const restrict=url.searchParams.get('groups');
 const echoed=selected?[selected]:restrict?restrict.split(','):all;
 return {scope:{serverId:'server',libraryId:'',libraryKind:'mixed',view:'search',entityId:'',viewerFence:'fence'},revision:{catalog:4,viewer:5},heading:{key:'search.title',fallback:'Search'},
  query:{q:url.searchParams.get('q'),sort:url.searchParams.get('sort')??'relevance',direction:url.searchParams.get('direction')??'desc',group:selected??'',groups:echoed,libraryIds:url.searchParams.get('libraryIds')?.split(',')??[],limit:Number(url.searchParams.get('limit')),searchMode:'normalized_prefix',recorded:url.searchParams.get('record')==='1'},
  groups:echoed.map(id=>group(id as SearchGroup)),capabilities:capabilities(),...changes};
}
function service(fn:(path:string,signal?:AbortSignal)=>Promise<unknown>,timeoutMs=1000){return new SearchService({scope,timeoutMs,api:{request:<T>(path:string,_m?:string,_b?:unknown,signal?:AbortSignal)=>fn(path,signal) as Promise<T>}});}
function deferred(){let resolve!:(value:unknown)=>void;const promise=new Promise<unknown>(r=>resolve=r);return {promise,resolve};}

test('pause retains only the arrived page with its actual cursor and aborts a pending continuation',async()=>{
 const held=deferred();const paths:string[]=[];let signal:AbortSignal|undefined;
 const s=service(async(path,provided)=>{paths.push(path);if(paths.length===2){signal=provided;return held.promise;}return result(path,{groups:[group('movies',[paths.length===1?'first':'fresh'],paths.length===1?'next-cursor':'')]});});
 try{
  await s.select('harbor',{group:'movies'});const shown=s.getSnapshot().data;
  const read=s.next('movies');assert.equal(s.getSnapshot().pagination.cursor,'next-cursor');
  s.pause();assert.equal(signal?.aborted,true);assert.equal(s.getSnapshot().phase,'ready');assert.equal(s.getSnapshot().data,shown);
  assert.equal(s.getSnapshot().pagination.cursor,null);assert.equal(s.getSnapshot().pagination.canPrevious,false);
  held.resolve(result(paths[1],{groups:[group('movies',['late'])]}));await read;
  assert.equal(s.getSnapshot().data,shown);await s.next('movies');
  assert.equal(new URL(paths[2],'https://local.invalid').searchParams.get('cursor'),'next-cursor');
  assert.equal(s.getSnapshot().groups[0].items[0].id,'fresh');
 }finally{s.dispose();}
});

test('pause cannot relabel a pending different query with old results',async()=>{
 const held=deferred();let path='';
 const s=service(async p=>{path=p;return new URL(p,'https://local.invalid').searchParams.get('q')==='after'?held.promise:result(p);});
 try{
  await s.select('before',{group:'movies'});const read=s.select('after',{group:'movies'});
  assert.equal(s.getSnapshot().data,null);s.pause();assert.equal(s.getSnapshot().phase,'idle');assert.equal(s.getSnapshot().query,null);
  held.resolve(result(path));await read;assert.equal(s.getSnapshot().data,null);
 }finally{s.dispose();}
});

test('pause refuses a pending changed library or ordering but preserves a recording-only read',async()=>{
 for(const change of [{libraryIds:['other']},{sort:'title' as const},{direction:'asc' as const},{record:true}]){
  const held=deferred();let reads=0,path='';
  const s=service(async p=>{path=p;return ++reads===1?result(p):held.promise;});
  try{
   await s.select('harbor',{group:'movies',libraryIds:['library']});const shown=s.getSnapshot().data;
   const read=s.select('harbor',{group:'movies',libraryIds:['library'],...change});s.pause();
   if('record' in change){assert.equal(s.getSnapshot().data,shown);assert.equal(s.getSnapshot().phase,'ready');}
   else{assert.equal(s.getSnapshot().data,null);assert.equal(s.getSnapshot().phase,'idle');}
   held.resolve(result(path));await read;
   assert.equal(s.getSnapshot().data,'record' in change?shown:null);
  }finally{s.dispose();}
 }
});

test('pause aborts quiet revalidation and refuses its late replacement',async()=>{
 const held=deferred();let reads=0,path='';let signal:AbortSignal|undefined;
 const s=service(async(p,provided)=>{path=p;if(++reads===2){signal=provided;return held.promise;}return result(p);});
 try{
  await s.select('harbor',{group:'movies'});const shown=s.getSnapshot().data,read=s.revalidate();
  s.pause();assert.equal(signal?.aborted,true);held.resolve(result(path,{groups:[group('movies',['late'])]}));await read;
  assert.equal(s.getSnapshot().data,shown);assert.equal(s.getSnapshot().phase,'ready');
  const aborted=new AbortController();aborted.abort();await s.revalidate({signal:aborted.signal});assert.equal(reads,2);
 }finally{s.dispose();}
});

test('a continuation viewer-fence change withdraws the old page before automatic refresh can be paused',async()=>{
 const held=deferred();let reads=0,refreshPath='';
 const refreshing=deferred();
 const s=service(async path=>{
  reads++;
  if(reads===1)return result(path,{groups:[group('movies',['old'],'next-cursor')]});
  if(reads===2){const raw=result(path);return {...raw,scope:{...raw.scope,viewerFence:'different-viewer-fence'}};}
  refreshPath=path;refreshing.resolve(undefined);return held.promise;
 });
 try{
  await s.select('harbor',{group:'movies'});const read=s.next('movies');await refreshing.promise;
  assert.equal(s.getSnapshot().data,null);s.pause();assert.equal(s.getSnapshot().phase,'idle');
  held.resolve(result(refreshPath));await read;assert.equal(s.getSnapshot().data,null);
 }finally{s.dispose();}
});

test('terminal authorization failure cannot be made ready again by pause',async()=>{
 let fail=false;
 const s=service(async path=>{if(fail)throw Object.assign(new Error('denied'),{status:403});return result(path,{groups:[group('movies',['old'],'next-cursor')]});});
 try{
  await s.select('harbor',{group:'movies'});fail=true;await s.next('movies');assert.equal(s.getSnapshot().data,null);
  s.pause();assert.equal(s.getSnapshot().phase,'idle');assert.deepEqual(s.getSnapshot().groups,[]);
 }finally{s.dispose();}
});

test('pause retains a transient read failure rather than presenting its cached page as a successful retry',async()=>{
 let fail=false;
 const s=service(async path=>{if(fail)throw new TypeError('Connection lost');return result(path,{groups:[group('movies',['shown'],'next-cursor')]});});
 try{
  await s.select('harbor',{group:'movies'});const shown=s.getSnapshot().data;
  fail=true;await s.next('movies');const error=s.getSnapshot().error;assert.equal(s.getSnapshot().phase,'error');
  s.pause();assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().error,error);assert.equal(s.getSnapshot().data,shown);
  assert.equal(s.getSnapshot().pagination.cursor,null);
  fail=false;await s.retry();assert.equal(s.getSnapshot().phase,'ready');assert.equal(s.getSnapshot().error,null);
 }finally{s.dispose();}
});

test('disposal erases the actual private Search snapshot and stops subsequent work',async()=>{
 const s=service(async path=>result(path));await s.select('harbor');assert(s.getSnapshot().data);
 s.dispose();assert.equal(s.getSnapshot().data,null);assert.equal(s.getSnapshot().query,null);assert.deepEqual(s.getSnapshot().groups,[]);
 assert.throws(()=>s.pause(),/disposed/);assert.throws(()=>s.select('next'),/disposed/);s.dispose();
});

test('a subscriber pausing the new loading state prevents its HTTP request from starting',async()=>{
 let reads=0;
 const s=service(async path=>{reads++;return result(path);});
 const off=s.subscribe(()=>{if(s.getSnapshot().phase==='loading')s.pause();});
 try{await s.select('harbor',{group:'movies'});assert.equal(reads,0);assert.equal(s.getSnapshot().phase,'idle');}
 finally{off();s.dispose();}
});

test('preserves server group order and publishes per-group capabilities',async()=>{
 const s=service(async path=>result(path,{groups:[group('books'),group('shows'),group('movies')],query:{...result(path).query,groups:['books','shows','movies']}}));
 await s.select('harbor');
 assert.equal(s.getSnapshot().phase,'ready');
 assert.deepEqual(s.getSnapshot().groups.map(g=>g.id),['books','shows','movies']);
 const published=new Map(s.getSnapshot().data!.capabilities.groups.map(g=>[g.id,g.available]));
 assert.equal(published.get('people'),true);
 assert.equal(published.get('live-tv'),false);
 assert.throws(()=>{(s.getSnapshot().groups as any).reverse();});
 s.dispose();
});
test('a failed group is reported beside the groups that succeeded',async()=>{
 const s=service(async path=>{const raw=result(path);raw.groups=raw.groups.map((g:any)=>g.id==='shows'?failed('shows'):g);return raw;});
 await s.select('harbor');
 assert.equal(s.getSnapshot().phase,'ready');
 const shows=s.getSnapshot().groups.find(g=>g.id==='shows')!;
 assert.equal(shows.status,'error');
 assert.equal(shows.errorCode,'search_group_timeout');
 assert.deepEqual(shows.items,[]);
 assert.equal(s.getSnapshot().groups.find(g=>g.id==='movies')!.items.length,1);
 s.dispose();
});
test('a failed group shows only its failure, and what a group does not say reads neutrally',async()=>{
 const read=async(change:(r:any)=>void)=>{const s=service(async path=>{const raw=result(path);change(raw);return raw;});await s.select('harbor');assert.equal(s.getSnapshot().phase,'ready');const data=s.getSnapshot().data!;s.dispose();return data;};
 const movies=async(change:(r:any)=>void)=>(await read(change)).groups.find(g=>g.id==='movies')!;
 // A failed group carries no partial page, whatever arrives with it.
 const partial=await movies(r=>{r.groups[0]={...failed('movies'),items:[{id:'x',libraryId:'l',kind:'movie',title:'X',navigation:{view:'item',entityId:'x'}}],totalCount:1,hasMore:true,nextCursor:'next'};});
 assert.deepEqual([partial.status,partial.errorCode,partial.items.length,partial.totalCount,partial.hasMore,partial.nextCursor],['error','search_group_timeout',0,0,false,'']);
 // A successful group never carries a failure code, and offers more exactly when it has a cursor.
 assert.deepEqual([(await movies(r=>{r.groups[0]={...group('movies'),errorCode:'search_group_timeout'};})).errorCode,(await movies(r=>{r.groups[0]={...group('movies'),hasMore:true};})).hasMore,(await movies(r=>{r.groups[0]={...group('movies',['one'],'next'),hasMore:false};})).hasMore],['',false,true]);
 // A status or failure code this app does not know reads as success, or as unavailable when a failure is named.
 assert.deepEqual([(await movies(r=>{r.groups[0]={...group('movies'),status:'partial'};})).status,(await movies(r=>{r.groups[0]={...failed('movies'),status:'throttled',errorCode:'made_up'};})).errorCode,(await movies(r=>{r.groups[0]={...failed('movies'),errorCode:'made_up'};})).errorCode],['success','search_group_unavailable','search_group_unavailable']);
 // An unavailable group that does not say why is still listed, without a reason.
 const quiet=(await read(r=>{r.capabilities.groups=r.capabilities.groups.map((g:any)=>g.id==='live-tv'?{id:'live-tv',sorts:['relevance','popularity']}:g);})).capabilities.groups.find(g=>g.id==='live-tv')!;
 assert.deepEqual(quiet,{id:'live-tv',title:'',entityKind:'channel',available:false,reason:'',sorts:['relevance']});
});
test('one group or result that cannot be read is left out and the rest of the answer is kept',async()=>{
 const read=async(change:(r:any)=>void,options:any={})=>{const s=service(async path=>{const raw=result(path);change(raw);return raw;});await s.select('harbor',options);assert.equal(s.getSnapshot().phase,'ready');const data=s.getSnapshot().data!;s.dispose();return data;};
 const item=(id:string,patch:any={})=>({id,libraryId:'library',kind:'movie',title:'Server '+id,navigation:{view:'item',entityId:id},...patch});
 // Results: a repeated one, one without a library, one that leads elsewhere, one of another kind, and one that would play though unavailable.
 const data=await read(r=>{r.groups[0].items=[item('one'),item('one'),item('two',{libraryId:undefined}),item('three',{navigation:{view:'item',entityId:'wrong'}}),item('four',{kind:'show'}),item('five',{available:false,playback:{itemId:'five'}}),{id:'broken'},item('six',{year:0,future:true})];r.groups[0].totalCount=1;r.future={a:1};r.query.searchMode='semantic';r.heading=7;});
 assert.deepEqual(data.groups[0].items.map(e=>e.id),['one','six']);
 assert.equal(data.groups[0].totalCount,2,'the count is at least the results kept');
 assert.deepEqual([data.query.searchMode,data.heading,'future' in data],['normalized_prefix',{key:'search',fallback:''},false]);
 // Groups: one this app does not know, a broken one, one whose rows are of another kind, and a repeated one.
 const groups=await read(r=>{r.groups=[{...group('movies'),id:'podcasts',entityKind:'podcast'},'nope',{...group('shows'),entityKind:'movie'},group('books'),{...group('books'),title:'Again'},{...group('albums'),nextCursor:7},{...group('songs'),items:Array.from({length:13},(_,i)=>({id:'s'+i,libraryId:'library',kind:'song',title:'S',navigation:{view:'item',entityId:'s'+i}}))}];});
 assert.deepEqual(groups.groups.map(g=>[g.id,g.title]),[['books','books']]);
 // An order the server chose and this app does not know reads as relevance, best first; unknown capabilities are not offered.
 const order=await read(r=>{r.query.sort='popularity';r.query.direction='random';r.query.groups=[...r.query.groups,'podcasts'];r.capabilities.sorts=['relevance','popularity'];r.capabilities.directions=['desc','random'];r.capabilities.groups.push({id:'podcasts',title:'Podcasts',entityKind:'podcast',available:true,sorts:[]});});
 assert.deepEqual([order.query.sort,order.query.direction,order.query.groups.length,order.capabilities.sorts,order.capabilities.directions,order.capabilities.groups.length],['relevance','desc',all.length,['relevance'],['desc'],all.length]);
 // An answer none of whose groups can be read is refused rather than shown as no results.
 const none=service(async path=>{const raw=result(path);raw.groups=['nope',{id:'podcasts'}];return raw;});await none.select('harbor');assert.equal(none.getSnapshot().phase,'error');assert.equal(none.getSnapshot().error!.code,'invalid_search');none.dispose();
});
test('sort, direction, group restriction and library restriction are server requests',async()=>{
 let path='';const s=service(async p=>{path=p;return result(p);});
 await s.select('harbor',{sort:'releaseYear',direction:'asc',groups:['movies','people'],libraryIds:['a','b']});
 const url=new URL(path,'https://x');
 assert.equal(url.searchParams.get('sort'),'releaseYear');
 assert.equal(url.searchParams.get('direction'),'asc');
 assert.equal(url.searchParams.get('groups'),'movies,people');
 assert.equal(url.searchParams.get('libraryIds'),'a,b');
 assert.deepEqual(s.getSnapshot().groups.map(g=>g.id),['movies','people']);
 assert.throws(()=>s.select('harbor',{sort:'popularity' as any}));
 assert.throws(()=>s.select('harbor',{groups:['movies','nope' as any]}));
 assert.throws(()=>s.select('harbor',{group:'movies',groups:['movies']}));
 s.dispose();
});
test('a server that answers a different sort fails closed; a group that was not asked for is left out',async()=>{
 const wrongSort=service(async p=>{const raw=result(p);raw.query.sort='title';return raw;});
 await wrongSort.select('harbor',{sort:'relevance'});
 assert.equal(wrongSort.getSnapshot().phase,'error');
 wrongSort.dispose();
 const extra=service(async p=>{const raw=result(p);raw.groups.push(group('movies'));return raw;});
 await extra.select('harbor',{groups:['people']});
 assert.deepEqual(extra.getSnapshot().groups.map(g=>g.id),['people']);
 extra.dispose();
 const missing=service(async p=>{const raw=result(p);raw.query.groups=['movies'];return raw;});
 await missing.select('harbor',{groups:['people']});
 assert.equal(missing.getSnapshot().phase,'error');
 missing.dispose();
});
test('people and channel rows carry no library, playback or availability',async()=>{
 const s=service(async p=>result(p,{groups:[group('people')],query:{...result(p).query,groups:['people']}}));
 await s.select('harbor');
 const person=s.getSnapshot().groups[0].items[0] as any;
 assert.equal(person.kind,'person');
 assert.equal(person.navigation.view,'person');
 assert.equal(person.libraryId,undefined);
 s.dispose();
 // Whatever else arrives with a person, the row never carries playback, availability or a library.
 const leaking=service(async p=>{const raw=result(p,{groups:[group('people')],query:{...result(p).query,groups:['people']}});Object.assign(raw.groups[0].items[0],{playback:{itemId:'one'},available:true,libraryId:'library',subtitle:7});return raw;});
 await leaking.select('harbor');
 assert.deepEqual(leaking.getSnapshot().groups[0].items[0],{id:'one',kind:'person',title:'Server one',navigation:{view:'person',entityId:'one'}});
 leaking.dispose();
});
test('Unicode whitespace normalization and punctuation are encoded without local matching',async()=>{let path='';const s=service(async p=>{path=p;return result(p);});await s.select(' \tCaféone & two  ');assert.equal(new URL(path,'https://x').searchParams.get('q'),'Café one & two');assert.equal(s.getSnapshot().query!.q,'Café one & two');assert.throws(()=>s.select('é'.repeat(129)));s.dispose();});
test('CD-30: search validation matches NormalizeSearch before dispatch',async()=>{
 let calls=0;const s=service(async p=>{calls++;return result(p);});
 // Invalid inputs throw as input feedback without dispatching (no connection loss).
 for(const q of ['!!!','one two three four five six seven eight nine','𠮷'.repeat(129),'é'.repeat(129)])assert.throws(()=>s.select(q),/Invalid search query|Search supports/);
 assert.equal(calls,0);
 assert.equal(s.getSnapshot().phase,'idle');
 // Astral counting is by code points, not UTF-16 units: 100 astral letters (200 units) is valid.
 await s.select('𠮷'.repeat(100));
 assert.equal(s.getSnapshot().phase,'ready');
 assert.equal([...s.getSnapshot().query!.q].length,100);
 // Valid non-Latin search dispatches.
 await s.select('日本語');
 assert.equal(s.getSnapshot().phase,'ready');
 assert.equal(s.getSnapshot().query!.q,'日本語');
 // Tokens split on non-letter/non-number: CJK + punctuation still valid.
 await s.select('日本語・テスト');
 assert.equal(s.getSnapshot().phase,'ready');
 s.dispose();
});
test('empty query clears results without requesting recommendations or persisting history',async()=>{let calls=0;const s=service(async p=>{calls++;return result(p);});await s.select('title');await s.select('  ');assert.equal(calls,1);assert.equal(s.getSnapshot().phase,'idle');assert.equal(s.getSnapshot().query,null);assert.deepEqual(s.getSnapshot().groups,[]);s.dispose();});
test('history is recorded only when the caller asks for it',async()=>{
 let path='';const s=service(async p=>{path=p;return result(p);});
 await s.select('harbor');
 assert.equal(new URL(path,'https://x').searchParams.has('record'),false);
 assert.equal(s.getSnapshot().data!.query.recorded,false);
 await s.select('harbor two',{record:true});
 assert.equal(new URL(path,'https://x').searchParams.get('record'),'1');
 assert.equal(s.getSnapshot().data!.query.recorded,true);
 s.dispose();
 // A server that claims it recorded a query the client did not ask to record is rejected.
 const lying=service(async p=>{const raw=result(p);raw.query.recorded=true;return raw;});
 await lying.select('harbor');
 assert.equal(lying.getSnapshot().phase,'error');
 lying.dispose();
 // A server that declines to record (the profile has turned history off since) still answers the search.
 const declined=service(async p=>{const raw=result(p);raw.query.recorded=false;return raw;});
 await declined.select('harbor',{record:true});
 assert.equal(declined.getSnapshot().phase,'ready');assert.equal(declined.getSnapshot().data!.query.recorded,false);
 declined.dispose();
});
test('search history parses as the viewer own bounded list', ()=>{
 const page=parseSearchHistory({serverId:'server',viewerFence:'fence',maxEntries:50,entries:[{q:'harbor',updatedAt:'2026-09-16T00:00:00Z',uses:3}]},scope);
 assert.equal(page.entries[0].q,'harbor');
 assert.throws(()=>parseSearchHistory({serverId:'other',viewerFence:'fence',maxEntries:50,entries:[]},scope));
 assert.throws(()=>parseSearchHistory({serverId:'server',viewerFence:'',maxEntries:50,entries:[]},scope));
 assert.throws(()=>parseSearchHistory({serverId:'server',viewerFence:'fence',maxEntries:1,entries:[{q:'a',updatedAt:'x',uses:1},{q:'b',updatedAt:'y',uses:1}]},scope));
 // An entry that cannot be read is left out alone; a repeated search keeps its first appearance.
 assert.deepEqual(parseSearchHistory({serverId:'server',viewerFence:'fence',maxEntries:50,future:1,entries:[{q:'a',updatedAt:'x',uses:1},{q:'a',updatedAt:'y',uses:1},{q:''},'nope',{q:'b',future:true}]},scope).entries,[{q:'a',updatedAt:'x',uses:1},{q:'b',updatedAt:'',uses:1}]);
});
test('group continuation and Previous preserve exact query and server page; no accumulation',async()=>{const paths:string[]=[];const s=service(async p=>{paths.push(p);const page=new URL(p,'https://x').searchParams.get('cursor');const raw=result(p);raw.groups=raw.groups.map((g:any)=>g.id==='movies'?group('movies',page?['six']:['one'],page?'':'next-page'):g);return raw;});await s.select('title');await s.next('movies');const movies=s.getSnapshot().groups.find(g=>g.id==='movies')!;assert.equal(movies.items[0].id,'six');assert.equal(movies.items.length,1);assert.equal(s.getSnapshot().pagination.canPrevious,true);await s.previous();assert.equal(new URL(paths.at(-1)!,'https://x').searchParams.has('group'),false);assert.equal(s.getSnapshot().groups.find(g=>g.id==='movies')!.items[0].id,'one');s.dispose();});
test('every published group is a server request, preserving typed navigation',async()=>{const s=service(async p=>result(p));for(const selected of all){await s.select('test',{group:selected});assert.equal(s.getSnapshot().phase,'ready');assert.equal(s.getSnapshot().query?.group,selected);}s.dispose();});
test('rapid query change aborts and ignores transport that completes late',async()=>{const old=deferred();let signal:AbortSignal|undefined,oldPath='';const s=service(async(p,a)=>{if(p.includes('q=old')){signal=a;oldPath=p;return old.promise;}return result(p);});const pending=s.select('old');await s.select('new');await pending;assert(signal?.aborted);old.resolve(result(oldPath));await Promise.resolve();assert.equal(s.getSnapshot().data!.query.q,'new');s.dispose();});
test('same in-flight command is singleflight, cancel clears even ignored abort',async()=>{const d=deferred();let calls=0;const s=service(async()=>{calls++;return d.promise;});const a=s.select('test'),b=s.select('test');assert.equal(a,b);assert.equal(calls,1);s.cancel();await a;assert.equal(s.getSnapshot().phase,'idle');assert.equal(s.getSnapshot().query,null);s.dispose();});
test('scope change clears query and pending data before a new principal binds',async()=>{const d=deferred();const s=service(async()=>d.promise);const pending=s.select('private');s.setScope({...scope,viewerId:'different-profile'},{request:async<T>()=>{throw new Error('unused');}});await pending;d.resolve({});await Promise.resolve();assert.equal(s.getSnapshot().scope.viewerId,'different-profile');assert.equal(s.getSnapshot().query,null);assert.deepEqual(s.getSnapshot().groups,[]);s.dispose();});
test('timeout settles transport ignoring abort and explicit retry can recover',async()=>{let calls=0;const s=service(async p=>{calls++;return calls===1?new Promise(()=>{}):result(p);},5);await s.select('test');assert.equal(s.getSnapshot().error?.code,'timeout');await s.retry();assert.equal(s.getSnapshot().phase,'ready');s.dispose();});
test('denied read clears previously visible data and never offers stale results',async()=>{let denied=false;const s=service(async p=>{if(denied)throw Object.assign(new Error('Permission removed'),{code:'forbidden'});return result(p);});await s.select('test');denied=true;await s.refresh();assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().data,null);assert.deepEqual(s.getSnapshot().groups,[]);s.dispose();});
test('a later page read after the library moved is still a page; another viewer\'s answer restarts the search quietly',async()=>{for(const change of ['catalog','viewer','fence']){let second=false,calls=0;const s=service(async p=>{calls++;const raw=result(p);raw.groups=raw.groups.map((g:any)=>g.id==='movies'?group('movies',['one'],p.includes('cursor=')?'':'next'):g);if(second&&p.includes('cursor=')){if(change==='fence')raw.scope.viewerFence='new-fence';else raw.revision[change]++;}return raw;});await s.select('test');second=true;await s.next('movies');assert.equal(s.getSnapshot().phase,'ready');assert.notEqual(s.getSnapshot().data,null);assert.equal(calls,change==='fence'?3:2);assert.equal(s.getSnapshot().pagination.cursor,change==='fence'?null:'next');s.dispose();}});
test('wrong scope, query, revision or limit fail closed',async()=>{const changes=[(r:any)=>r.scope.serverId='wrong',(r:any)=>r.scope.viewerFence='',(r:any)=>r.query.q='other',(r:any)=>r.query.limit=41,(r:any)=>r.query.group='movies',(r:any)=>r.revision.catalog=-1,(r:any)=>r.groups=Array(65).fill(r.groups[0]),(r:any)=>r.capabilities.maxLimit=0];for(const change of changes){const s=service(async p=>{const raw=result(p);change(raw);return raw;});await s.select('test');assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().data,null);s.dispose();}});
test('pagination history is bounded to 64 prior pages and a repeated cursor ends the group',async()=>{const s=service(async p=>{const u=new URL(p,'https://x'),index=Number(u.searchParams.get('cursor')??0);const raw=result(p);raw.groups=raw.groups.map((g:any)=>g.id==='movies'?group('movies',[String(index)],String(index+1)):g);return raw;});await s.select('test',{group:'movies'});for(let i=0;i<70;i++)await s.next('movies');for(let i=0;i<64;i++)await s.previous();assert.equal(s.getSnapshot().pagination.canPrevious,false);assert.equal(s.getSnapshot().groups[0].items[0].id,'6');s.dispose();const repeated=service(async p=>{const raw=result(p);raw.groups=raw.groups.map((g:any)=>g.id==='movies'?group('movies',['one'],'same'):g);return raw;});await repeated.select('test');await repeated.next('movies');assert.equal(repeated.getSnapshot().phase,'ready');assert.equal(repeated.getSnapshot().groups.find(g=>g.id==='movies')!.nextCursor,'');repeated.dispose();});
test('a stale server cursor restarts the search from its first page without a refresh state',async()=>{const paths:string[]=[];const phases:string[]=[];const s=service(async p=>{paths.push(p);if(p.includes('cursor='))throw Object.assign(new Error('Expired continuation'),{code:'stale_continuation',retryable:true});const raw=result(p);raw.groups=raw.groups.map((g:any)=>g.id==='movies'?group('movies',['one'],'next'):g);return raw;});await s.select('test');s.subscribe(()=>phases.push(s.getSnapshot().phase));await s.next('movies');assert.equal(s.getSnapshot().phase,'ready');assert(!phases.includes('refresh-required')&&!phases.includes('error'));assert(!paths.at(-1)!.includes('cursor='));assert.equal(paths.length,3);s.dispose();});
test('same-query transient refresh retains results but a new query never inherits them',async()=>{
 let fail=false;const s=service(async p=>{if(fail)throw new TypeError('Failed to fetch');return result(p);});
 await s.select('harbor');fail=true;const refresh=s.refresh();assert.equal(s.getSnapshot().groups[0].items[0].id,'one');await refresh;
 assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().error?.retryable,true);assert.equal(s.getSnapshot().groups.length,mediaGroups.length+2);
 await s.select('different');assert.equal(s.getSnapshot().groups.length,0);s.dispose();
});

test('searchQueryProblem says why a query would not be sent (CD-30)', () => {
  assert.equal(searchQueryProblem(''), undefined);
  assert.equal(searchQueryProblem('   '), undefined);
  assert.equal(searchQueryProblem('a'), undefined);
  assert.equal(searchQueryProblem('!!!'), 'short');
  assert.equal(searchQueryProblem('a b c'), undefined);
  assert.equal(searchQueryProblem('one two three four five six seven eight nine'), 'words');
  assert.equal(searchQueryProblem('x'.repeat(129)), 'long');
  assert.equal(searchQueryProblem('テスト'), undefined);
  assert.equal(searchQueryProblem('ab'), undefined);
  assert.equal(searchQueryProblem('a lumen'), undefined);
});

test('preserves count accuracy while keeping unknown totals pageable',async()=>{
 const s=service(async path=>result(path,{groups:[{...group('movies',['one'],'next'),totalCount:1,totalCountExact:false},{...group('shows'),totalCountExact:true}],query:{...result(path).query,groups:['movies','shows']}}));
 await s.select('Title 000000');
 assert.equal(s.getSnapshot().phase,'ready');
 const [movies,shows]=s.getSnapshot().groups;
 assert.equal(movies.totalCountExact,false);
 assert.equal(movies.hasMore,true);
 assert.equal(movies.nextCursor,'next');
 assert.equal(shows.totalCountExact,true);
 assert.equal(shows.totalCount,20);
 s.dispose();
});

test('channel anchors preserve the complete actual source mapping without guessing older rows',async()=>{
 const read=async(navigation:Record<string,unknown>)=>{
  const s=service(async path=>{
   const raw=result(path);
   raw.groups=[{...group('live-tv'),items:[{id:'news',kind:'channel',title:'News',navigation}]}];
   return raw;
  });
  await s.select('harbor',{group:'live-tv'});
  assert.equal(s.getSnapshot().phase,'ready');
  const items=s.getSnapshot().groups[0].items;
  s.dispose();return items;
 };
 const base={view:'channel',entityId:'news'};
 const tuple={sourceId:'actual-source',channelId:'news',provenance:'live-source'};
 assert.deepEqual((await read({...base,...tuple}))[0].navigation,{...base,...tuple});
 assert.deepEqual((await read(base))[0].navigation,base);
 for(const invalid of [{sourceId:'actual-source'},{...tuple,sourceId:''},{...tuple,channelId:'other'},{...tuple,provenance:'library-channel'}]){
  assert.deepEqual(await read({...base,...invalid}),[]);
 }
 const entries=await read({...base,...tuple,token:'private',url:'https://private.invalid'});
 assert.deepEqual(entries[0].navigation,{...base,...tuple});
});

test('offered group continuations preserve parent All facts and never record another history intent',async()=>{
 const paths:string[]=[];
 const s=service(async path=>{
  paths.push(path);const raw=result(path),cursor=new URL(path,'https://fixture.invalid').searchParams.get('cursor');
  raw.groups=raw.groups.map((value:any)=>group(value.id,[cursor?'continued':'first'],cursor||value.id==='live-tv'?'':value.id+'-next'));
  return raw;
 });
 let child:SearchService|undefined;
 try{
  assert.equal(s.forkContinuation('movies'),undefined);
  await s.select('harbor',{record:true});const shown=s.getSnapshot();
  assert.equal(s.forkContinuation('live-tv'),undefined);
  child=s.forkContinuation('movies');assert.ok(child);
  await child.next('movies');
  assert.equal(s.getSnapshot(),shown);
  assert.equal(child.getSnapshot().pagination.cursor,'movies-next');
  assert.equal(child.getSnapshot().groups[0].items[0].id,'continued');
  assert.equal(new URL(paths[0],'https://fixture.invalid').searchParams.get('record'),'1');
  assert.equal(new URL(paths[1],'https://fixture.invalid').searchParams.get('record'),null);
  assert.equal(new URL(paths[1],'https://fixture.invalid').searchParams.get('group'),'movies');
 }finally{child?.dispose();s.dispose();}
});

test('continuation children own independent cancellation and keep the original viewer fence',async()=>{
 const pending=new Map<string,{resolve:(value:unknown)=>void;path:string;signal?:AbortSignal}>();
 const s=service(async(path,signal)=>{
  const url=new URL(path,'https://fixture.invalid');
  if(url.searchParams.has('cursor'))return new Promise(resolve=>pending.set(url.searchParams.get('group')!,{resolve,path,signal}));
  const raw=result(path);raw.groups=raw.groups.map((value:any)=>group(value.id,['first'],value.id+'-next'));return raw;
 });
 let movies:SearchService|undefined,shows:SearchService|undefined;
 try{
  await s.select('harbor');const shown=s.getSnapshot();movies=s.forkContinuation('movies');shows=s.forkContinuation('shows');assert.ok(movies);assert.ok(shows);
  const first=movies.next('movies'),second=shows.next('shows');
  movies.dispose();assert.equal(pending.get('movies')?.signal?.aborted,true);assert.equal(pending.get('shows')?.signal?.aborted,false);
  const movie=pending.get('movies')!,show=pending.get('shows')!;
  movie.resolve(result(movie.path,{groups:[group('movies',['late'])]}));
  show.resolve(result(show.path,{groups:[group('shows',['arrived'])]}));
  await Promise.all([first,second]);assert.equal(s.getSnapshot(),shown);assert.equal(shows.getSnapshot().groups[0].items[0].id,'arrived');
 }finally{movies?.dispose();shows?.dispose();s.dispose();}
});

test('continuation forks reject unoffered cursors and revalidate a changed viewer fence',async()=>{
 const paths:string[]=[];
 const s=service(async path=>{
  paths.push(path);const raw=result(path),cursor=new URL(path,'https://fixture.invalid').searchParams.has('cursor');
  raw.groups=raw.groups.map((value:any)=>group(value.id,['one'],value.id==='movies'?'movies-next':''));
  if(cursor)raw.scope.viewerFence='changed-fence';return raw;
 });
 let child:SearchService|undefined;
 try{
  await s.select('harbor');const shown=s.getSnapshot();assert.equal(s.forkContinuation('shows'),undefined);
  child=s.forkContinuation('movies');assert.ok(child);await child.next('movies');
  assert.equal(paths.length,3);assert.equal(new URL(paths[2],'https://fixture.invalid').searchParams.has('cursor'),false);
  assert.equal(s.getSnapshot(),shown);assert.equal(child.getSnapshot().pagination.cursor,null);
 }finally{child?.dispose();s.dispose();}
});
