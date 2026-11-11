import test from 'node:test';import assert from 'node:assert/strict';
import {SearchService,parseSearchHistory,searchQueryProblem,type SearchGroup} from '../src/search.ts';
const scope={serverId:'server',viewerId:JSON.stringify(['hosted','account','profile'])};
/** The rebuilt server's kind for each group the app shows, and the server's answer for a search. */
const kinds={movies:'movie',shows:'show',episodes:'episode',artists:'artist',albums:'album',songs:'track',books:'audiobook',reading:'book',people:'person'} as const;
const all=Object.keys(kinds) as SearchGroup[];
const mediaGroups=all.filter(g=>g!=='people');
const groupOf=(kind:string)=>all.find(g=>kinds[g as keyof typeof kinds]===kind) as SearchGroup|undefined;
function group(id:SearchGroup,ids=['one'],nextCursor='',total:number|null=20):any{
 const kind=kinds[id as keyof typeof kinds];
 return {kind,...(total===null?{}:{total}),...(id==='people'?{people:ids.map(entity=>({id:entity,name:'Server '+entity,images:{}}))}:{items:ids.map(entity=>({id:entity,kind,title:'Server '+entity,libraryId:'library'}))}),...(nextCursor?{nextCursor}:{})};
}
function result(path:string,changes:Record<string,unknown>={}):any{
 const url=new URL(path,'https://server.test'),selected=groupOf(url.searchParams.get('kind')??'');
 return {groups:(selected?[selected]:all).map(id=>group(id)),...changes};
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

test('a cursor refused after the viewer fence moved withdraws the old page before automatic refresh can be paused',async()=>{
 const held=deferred();let reads=0,refreshPath='';
 const refreshing=deferred();
 const s=service(async path=>{
  reads++;
  if(reads===1)return result(path,{groups:[group('movies',['old'],'next-cursor')]});
  // The viewer's fence moved: the server refuses the cursor read under the old one.
  if(reads===2)throw Object.assign(new Error('Invalid cursor'),{code:'invalid_cursor',status:400});
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

test('preserves server group order and publishes what each answered group can be sorted by',async()=>{
 const s=service(async path=>result(path,{groups:[group('books'),group('shows'),group('people'),group('movies')]}));
 await s.select('harbor');
 assert.equal(s.getSnapshot().phase,'ready');
 assert.deepEqual(s.getSnapshot().groups.map(g=>g.id),['books','shows','people','movies']);
 const published=new Map(s.getSnapshot().data!.capabilities.groups.map(g=>[g.id,g.sorts]));
 assert.deepEqual(published.get('people'),['relevance','title']);
 assert.deepEqual(published.get('movies'),['relevance','title','releaseYear','dateAdded']);
 assert.equal(published.has('live-tv'),false,'the rebuilt server searches no Live TV yet');
 assert.throws(()=>{(s.getSnapshot().groups as any).reverse();});
 s.dispose();
});


test('a result or a group this app cannot show is left out and the rest of the answer is kept',async()=>{
 const s=service(async path=>result(path,{groups:[{...group('movies',['one','two']),items:[{id:'one',kind:'movie',title:'Server one',libraryId:'library'},{id:'odd',kind:'hologram',title:'?'},{id:'',kind:'movie',title:'no id'},{id:'two',kind:'movie',title:'Server two',libraryId:'library'}]},{kind:'channel',total:3,items:[]},group('shows')]}));
 await s.select('harbor');
 assert.equal(s.getSnapshot().phase,'ready');
 assert.deepEqual(s.getSnapshot().groups.map(g=>g.id),['movies','shows'],'a kind this app does not show is left out');
 assert.deepEqual(s.getSnapshot().groups[0].items.map(e=>e.id),['one','two']);
 s.dispose();
});

test('sort and direction are server requests; a group restriction keeps only those groups',async()=>{
 let path='';const s=service(async p=>{path=p;return result(p);});
 await s.select('harbor',{group:'movies',sort:'releaseYear',direction:'asc'});
 let url=new URL(path,'https://x');
 assert.equal(url.searchParams.get('kind'),'movie');assert.equal(url.searchParams.get('sort'),'year');assert.equal(url.searchParams.get('order'),'asc');
 await s.select('harbor',{groups:['movies','people']});
 url=new URL(path,'https://x');assert.equal(url.searchParams.has('kind'),false);assert.equal(url.searchParams.get('limit'),'12');
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
test('people rows carry no library, playback or availability, and a book opens its page rather than playing',async()=>{
 const s=service(async p=>result(p,{groups:[{kind:'person',total:1,people:[{id:'one',name:'Server one',images:{thumb:'/art/one.jpg'},biography:'long',titleCount:4}]},group('reading')]}));
 await s.select('harbor');
 assert.deepEqual(s.getSnapshot().groups[0].items[0],{id:'one',kind:'person',title:'Server one',posterUrl:'/art/one.jpg',navigation:{view:'person',entityId:'one'}});
 const book=s.getSnapshot().groups[1].items[0] as any;
 assert.equal(book.kind,'ebook');assert.equal(book.playback,undefined);
 s.dispose();
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
 assert.equal(new URL(path,'https://x').searchParams.get('record'),'true');
 assert.equal(s.getSnapshot().data!.query.recorded,true);
 s.dispose();
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
test('group continuation and Previous preserve exact query and server page; no accumulation',async()=>{const paths:string[]=[];const s=service(async p=>{paths.push(p);const page=new URL(p,'https://x').searchParams.get('cursor');const raw=result(p);raw.groups=raw.groups.map((g:any)=>groupOf(g.kind)==='movies'?group('movies',page?['six']:['one'],page?'':'next-page'):g);return raw;});await s.select('title');await s.next('movies');const movies=s.getSnapshot().groups.find(g=>g.id==='movies')!;assert.equal(movies.items[0].id,'six');assert.equal(movies.items.length,1);assert.equal(s.getSnapshot().pagination.canPrevious,true);await s.previous();assert.equal(new URL(paths.at(-1)!,'https://x').searchParams.has('kind'),false);assert.equal(s.getSnapshot().groups.find(g=>g.id==='movies')!.items[0].id,'one');s.dispose();});
test('every published group is a server request, preserving typed navigation',async()=>{const s=service(async p=>result(p));for(const selected of all){await s.select('test',{group:selected});assert.equal(s.getSnapshot().phase,'ready');assert.equal(s.getSnapshot().query?.group,selected);}s.dispose();});
test('rapid query change aborts and ignores transport that completes late',async()=>{const old=deferred();let signal:AbortSignal|undefined,oldPath='';const s=service(async(p,a)=>{if(p.includes('q=old')){signal=a;oldPath=p;return old.promise;}return result(p);});const pending=s.select('old');await s.select('new');await pending;assert(signal?.aborted);old.resolve(result(oldPath));await Promise.resolve();assert.equal(s.getSnapshot().data!.query.q,'new');s.dispose();});
test('same in-flight command is singleflight, cancel clears even ignored abort',async()=>{const d=deferred();let calls=0;const s=service(async()=>{calls++;return d.promise;});const a=s.select('test'),b=s.select('test');assert.equal(a,b);assert.equal(calls,1);s.cancel();await a;assert.equal(s.getSnapshot().phase,'idle');assert.equal(s.getSnapshot().query,null);s.dispose();});
test('scope change clears query and pending data before a new principal binds',async()=>{const d=deferred();const s=service(async()=>d.promise);const pending=s.select('private');s.setScope({...scope,viewerId:'different-profile'},{request:async<T>()=>{throw new Error('unused');}});await pending;d.resolve({});await Promise.resolve();assert.equal(s.getSnapshot().scope.viewerId,'different-profile');assert.equal(s.getSnapshot().query,null);assert.deepEqual(s.getSnapshot().groups,[]);s.dispose();});
test('timeout settles transport ignoring abort and explicit retry can recover',async()=>{let calls=0;const s=service(async p=>{calls++;return calls===1?new Promise(()=>{}):result(p);},5);await s.select('test');assert.equal(s.getSnapshot().error?.code,'timeout');await s.retry();assert.equal(s.getSnapshot().phase,'ready');s.dispose();});
test('denied read clears previously visible data and never offers stale results',async()=>{let denied=false;const s=service(async p=>{if(denied)throw Object.assign(new Error('Permission removed'),{code:'forbidden'});return result(p);});await s.select('test');denied=true;await s.refresh();assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().data,null);assert.deepEqual(s.getSnapshot().groups,[]);s.dispose();});
test('a cursor the server refuses restarts the search quietly from its first page',async()=>{let second=false,calls=0;const s=service(async p=>{calls++;const raw=result(p);raw.groups=raw.groups.map((g:any)=>groupOf(g.kind)==='movies'?group('movies',['one'],p.includes('cursor=')?'':'next'):g);if(second&&p.includes('cursor='))throw Object.assign(new Error('Invalid cursor'),{code:'invalid_cursor',status:400});return raw;});await s.select('test');second=true;await s.next('movies');assert.equal(s.getSnapshot().phase,'ready');assert.notEqual(s.getSnapshot().data,null);assert.equal(calls,3);assert.equal(s.getSnapshot().pagination.cursor,null);s.dispose();});
test('wrong scope, query, revision or limit fail closed',async()=>{const changes=[(r:any)=>r.scope.serverId='wrong',(r:any)=>r.scope.viewerFence='',(r:any)=>r.query.q='other',(r:any)=>r.query.limit=41,(r:any)=>r.query.group='movies',(r:any)=>r.revision.catalog=-1,(r:any)=>r.groups=Array(65).fill(r.groups[0]),(r:any)=>r.capabilities.maxLimit=0];for(const change of changes){const s=service(async p=>{const raw=result(p);change(raw);return raw;});await s.select('test');assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().data,null);s.dispose();}});
test('pagination history is bounded to 64 prior pages and a repeated cursor ends the group',async()=>{const s=service(async p=>{const u=new URL(p,'https://x'),index=Number(u.searchParams.get('cursor')??0);const raw=result(p);raw.groups=raw.groups.map((g:any)=>groupOf(g.kind)==='movies'?group('movies',[String(index)],String(index+1)):g);return raw;});await s.select('test',{group:'movies'});for(let i=0;i<70;i++)await s.next('movies');for(let i=0;i<64;i++)await s.previous();assert.equal(s.getSnapshot().pagination.canPrevious,false);assert.equal(s.getSnapshot().groups[0].items[0].id,'6');s.dispose();const repeated=service(async p=>{const raw=result(p);raw.groups=raw.groups.map((g:any)=>groupOf(g.kind)==='movies'?group('movies',['one'],'same'):g);return raw;});await repeated.select('test');await repeated.next('movies');assert.equal(repeated.getSnapshot().phase,'ready');assert.equal(repeated.getSnapshot().groups.find(g=>g.id==='movies')!.nextCursor,'');repeated.dispose();});
test('a stale server cursor restarts the search from its first page without a refresh state',async()=>{const paths:string[]=[];const phases:string[]=[];const s=service(async p=>{paths.push(p);if(p.includes('cursor='))throw Object.assign(new Error('Expired continuation'),{code:'stale_continuation',retryable:true});const raw=result(p);raw.groups=raw.groups.map((g:any)=>groupOf(g.kind)==='movies'?group('movies',['one'],'next'):g);return raw;});await s.select('test');s.subscribe(()=>phases.push(s.getSnapshot().phase));await s.next('movies');assert.equal(s.getSnapshot().phase,'ready');assert(!phases.includes('refresh-required')&&!phases.includes('error'));assert(!paths.at(-1)!.includes('cursor='));assert.equal(paths.length,3);s.dispose();});
test('same-query transient refresh retains results but a new query never inherits them',async()=>{
 let fail=false;const s=service(async p=>{if(fail)throw new TypeError('Failed to fetch');return result(p);});
 await s.select('harbor');fail=true;const refresh=s.refresh();assert.equal(s.getSnapshot().groups[0].items[0].id,'one');await refresh;
 assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().error?.retryable,true);assert.equal(s.getSnapshot().groups.length,all.length);
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
 const s=service(async path=>result(path,{groups:[{...group('movies',['one'],'next',2000),totalIsMinimum:true},group('shows')]}));
 await s.select('Title 000000');
 assert.equal(s.getSnapshot().phase,'ready');
 const [movies,shows]=s.getSnapshot().groups;
 assert.equal(movies.totalCountExact,false);assert.equal(movies.totalCount,2000);
 assert.equal(movies.hasMore,true);
 assert.equal(movies.nextCursor,'next');
 assert.equal(shows.totalCountExact,true);
 assert.equal(shows.totalCount,20);
 s.dispose();
});

test('a later page keeps the first page\'s real count, which the server sends only once',async()=>{
 const s=service(async path=>{const cursor=new URL(path,'https://x').searchParams.get('cursor');return result(path,{groups:[group('movies',[cursor?'two':'one'],cursor?'':'next',cursor?null:37)]});});
 await s.select('harbor',{group:'movies'});await s.next('movies');
 const movies=s.getSnapshot().groups[0];assert.equal(movies.items[0].id,'two');assert.equal(movies.totalCount,37);assert.equal(movies.totalCountExact,true);
 s.dispose();
});

test('offered group continuations preserve parent All facts and never record another history intent',async()=>{
 const paths:string[]=[];
 const s=service(async path=>{
  paths.push(path);const raw=result(path),cursor=new URL(path,'https://fixture.invalid').searchParams.get('cursor');
  raw.groups=raw.groups.map((value:any)=>group(groupOf(value.kind)!,[cursor?'continued':'first'],cursor||false?'':groupOf(value.kind)+'-next'));
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
  assert.equal(new URL(paths[0],'https://fixture.invalid').searchParams.get('record'),'true');
  assert.equal(new URL(paths[1],'https://fixture.invalid').searchParams.get('record'),null);
  assert.equal(new URL(paths[1],'https://fixture.invalid').searchParams.get('kind'),'movie');
 }finally{child?.dispose();s.dispose();}
});

test('continuation children own independent cancellation and keep the original viewer fence',async()=>{
 const pending=new Map<string,{resolve:(value:unknown)=>void;path:string;signal?:AbortSignal}>();
 const s=service(async(path,signal)=>{
  const url=new URL(path,'https://fixture.invalid');
  if(url.searchParams.has('cursor'))return new Promise(resolve=>pending.set(groupOf(url.searchParams.get('kind')!)!,{resolve,path,signal}));
  const raw=result(path);raw.groups=raw.groups.map((value:any)=>group(groupOf(value.kind)!,['first'],groupOf(value.kind)+'-next'));return raw;
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

test('continuation forks reject unoffered cursors and restart when the server refuses a cursor from before the fence moved',async()=>{
 const paths:string[]=[];
 const s=service(async path=>{
  paths.push(path);const raw=result(path),cursor=new URL(path,'https://fixture.invalid').searchParams.has('cursor');
  raw.groups=raw.groups.map((value:any)=>group(groupOf(value.kind)!,['one'],groupOf(value.kind)==='movies'?'movies-next':''));
  if(cursor)throw Object.assign(new Error('Invalid cursor'),{code:'invalid_cursor',status:400});return raw;
 });
 let child:SearchService|undefined;
 try{
  await s.select('harbor');const shown=s.getSnapshot();assert.equal(s.forkContinuation('shows'),undefined);
  child=s.forkContinuation('movies');assert.ok(child);await child.next('movies');
  assert.equal(paths.length,3);assert.equal(new URL(paths[2],'https://fixture.invalid').searchParams.has('cursor'),false);
  assert.equal(s.getSnapshot(),shown);assert.equal(child.getSnapshot().pagination.cursor,null);
 }finally{child?.dispose();s.dispose();}
});

// Step 7, lead's ruling (#674): a continuation fork is one row of the All answer. A cursor the server refuses
// (invalid_cursor: the viewer's access was decided again) reads the row's first page once, in place of the row; a
// continuation that answers clears that guard, so a later refusal reads it again; a refusal straight after a re-read ends.
// A moved revision (stale_continuation) ends the row's paging with no re-read. The main search keeps restarting (#624).
function scripted(script:string[]){
 const paths:string[]=[];
 const s=service(async path=>{
  paths.push(path);const url=new URL(path,'https://fixture.invalid'),cursor=url.searchParams.get('cursor');
  const raw=result(path);
  if(!cursor){raw.groups=raw.groups.map((value:any)=>group(groupOf(value.kind)!,[`${groupOf(value.kind)}-first-${paths.length}`],groupOf(value.kind)==='movies'?'c1':''));return raw;}
  const step=script.shift();
  if(step==='answer'){raw.groups=raw.groups.map((value:any)=>group(groupOf(value.kind)!,[`${groupOf(value.kind)}-after-${cursor}`],groupOf(value.kind)==='movies'?cursor+'+':''));return raw;}
  throw Object.assign(new Error('refused'),{code:step,status:step==='stale_continuation'?409:400});
 });
 return {s,paths,first:()=>paths.filter(path=>!new URL(path,'https://fixture.invalid').searchParams.has('cursor')).length};
}
test('step 7: a continuation fork reads its first page again once per access change, and a refusal straight after that ends',async()=>{
 const {s,first}=scripted(['invalid_cursor','answer','invalid_cursor','invalid_cursor']);
 let child:SearchService|undefined;
 try{
  await s.select('harbor');const shown=s.getSnapshot();child=s.forkContinuation('movies');assert.ok(child);
  // first access change: the row's first page is read again, group-only, and is ready in place of the row
  await child.next('movies');assert.equal(first(),2);assert.equal(child.getSnapshot().phase,'ready');assert.equal(child.getSnapshot().pagination.cursor,null);
  assert.equal(child.getSnapshot().data?.query.group,'movies');assert.equal(s.getSnapshot(),shown);
  // a continuation answers, then a second access change reads the first page again too
  await child.next('movies');assert.equal(child.getSnapshot().pagination.cursor,'c1');assert.equal(child.getSnapshot().groups[0].items[0].id,'movies-after-c1');
  await child.next('movies');assert.equal(first(),3);assert.equal(child.getSnapshot().phase,'ready');
  // a refusal straight after that re-read ends the row's paging with the refusal
  await child.next('movies');assert.equal(first(),3);
  assert.equal(child.getSnapshot().phase,'refresh-required');assert.equal(child.getSnapshot().error?.code,'invalid_cursor');
 }finally{child?.dispose();s.dispose();}
});
test('step 7: a continuation fork whose revision moved (stale_continuation) ends its paging with no re-read; the main search still restarts',async()=>{
 const fork=scripted(['stale_continuation']);
 let child:SearchService|undefined;
 try{
  await fork.s.select('harbor');child=fork.s.forkContinuation('movies');assert.ok(child);
  await child.next('movies');assert.equal(fork.first(),1);
  assert.equal(child.getSnapshot().phase,'refresh-required');assert.equal(child.getSnapshot().error?.code,'stale_continuation');
 }finally{child?.dispose();fork.s.dispose();}
 const main=scripted(['stale_continuation']);
 try{
  await main.s.select('harbor',{group:'movies'});await main.s.next('movies');
  assert.equal(main.first(),2);assert.equal(main.s.getSnapshot().phase,'ready');
 }finally{main.s.dispose();}
});
