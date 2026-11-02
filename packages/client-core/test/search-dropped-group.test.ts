import test from 'node:test';import assert from 'node:assert/strict';
import {SearchService,type SearchGroup} from '../src/search.ts';
import {SEARCH_CHIPS,searchChipOffered} from '../src/presentation/search-model.ts';
const scope={serverId:'server',viewerId:JSON.stringify(['hosted','account','profile'])};
const kinds={movies:'movie',shows:'show',episodes:'episode',artists:'artist',albums:'album',songs:'song',books:'book',collections:'collection',authors:'author',people:'person','live-tv':'channel'} as const;
const all=Object.keys(kinds) as SearchGroup[];
function group(id:SearchGroup):any{
 const entityKind=kinds[id],view=['movies','episodes','songs'].includes(id)?'item':entityKind;
 const items=id==='people'||id==='live-tv'
  ?[{id:'one',kind:entityKind,title:'Server one',navigation:{view,entityId:'one'}}]
  :[{id:'one',libraryId:'library',kind:entityKind,title:'Server one',navigation:{view,entityId:'one'}}];
 return {id,title:id,entityKind,status:'success',errorCode:'',items,totalCount:1,hasMore:false,nextCursor:''};
}
/** A server that treats `groups` as a filter over what this viewer may search.
 *  A profile without Live TV is answered with the remaining groups, exactly as
 *  catalog.Search drops live-tv (`searchSelection` + the !r.LiveTV filter). */
function result(path:string):any{
 const url=new URL(path,'https://server.test'),restrict=url.searchParams.get('groups');
 const echoed=restrict?restrict.split(',').filter(id=>id!=='live-tv') as SearchGroup[]:all.filter(id=>id!=='live-tv');
 const capabilities={groups:all.map(id=>({id,title:id,entityKind:kinds[id],available:id!=='live-tv',...(id==='live-tv'?{reason:'Live TV is not available on this profile.'}:{}),sorts:['relevance']})),sorts:['relevance'],directions:['desc'],maxLimit:40,groupBudgetMs:600};
 return {scope:{serverId:'server',libraryId:'',libraryKind:'mixed',view:'search',entityId:'',viewerFence:'fence'},revision:{catalog:4,viewer:5},heading:{key:'search.title',fallback:'Search'},
  query:{q:url.searchParams.get('q'),sort:'relevance',direction:'desc',group:'',groups:echoed,libraryIds:[],limit:Number(url.searchParams.get('limit')),searchMode:'normalized_prefix',recorded:false},
  groups:echoed.map(group),capabilities};
}
function service(){return new SearchService({scope,timeoutMs:1000,api:{request:<T>(path:string)=>Promise.resolve(result(path)) as Promise<T>}});}

test('a requested group the viewer may not search is absent, not a refused answer',async()=>{
 const s=service();
 const groups=(['movies','shows','episodes','people','artists','albums','songs','books','live-tv'] as SearchGroup[]);
 try{
  await s.select('harbor',{groups});
  const snapshot=s.getSnapshot();
  assert.equal(snapshot.phase,'ready','answer refused: '+JSON.stringify(snapshot.error));
  assert.equal(snapshot.data?.query.groups.includes('live-tv'),false);
  assert.equal(snapshot.groups.some(value=>value.id==='live-tv'),false);
  assert.equal(snapshot.groups.find(value=>value.id==='movies')?.totalCount,1);
  assert.equal(snapshot.data?.capabilities.groups.find(value=>value.id==='live-tv')?.available,false);
 }finally{s.dispose();}
});

/** The real handler for a profile without Live TV: no `live-tv` id in the echo, the results or the
 *  capabilities at all (catalog.searchCapabilities skips the group for that viewer). */
function deniedService(){
 return new SearchService({scope,timeoutMs:1000,api:{request:<T>(path:string)=>{
  const answer=result(path);
  answer.capabilities.groups=answer.capabilities.groups.filter((value:any)=>value.id!=='live-tv');
  return Promise.resolve(answer) as Promise<T>;
 }}});
}
test('the real answer for a profile without Live TV is read, and only Live TV has no chip',async()=>{
 const s=deniedService();
 try{
  await s.select('harbor',{groups:['movies','shows','episodes','people','artists','albums','songs','books','live-tv']});
  const snapshot=s.getSnapshot();
  assert.equal(snapshot.phase,'ready','answer refused: '+JSON.stringify(snapshot.error));
  assert.deepEqual(snapshot.groups.map(value=>value.id),['movies','shows','episodes','people','artists','albums','songs','books']);
  const capabilities=snapshot.data!.capabilities;
  assert.deepEqual(SEARCH_CHIPS.filter(chip=>searchChipOffered(chip.id,capabilities)).map(chip=>chip.id),['all','movies','shows','episodes','people','artists','albums','songs','books']);
 }finally{s.dispose();}
});
test('before any answer every chip but the optional Reading is drawn; an unavailable group never is',()=>{
 assert.deepEqual(SEARCH_CHIPS.filter(chip=>searchChipOffered(chip.id,undefined)).map(chip=>chip.id),SEARCH_CHIPS.map(chip=>chip.id).filter(id=>id!=='reading'));
 const capabilities={groups:[{id:'movies',available:true},{id:'live-tv',available:false},{id:'reading',available:true}],sorts:[],directions:[],maxLimit:40,groupBudgetMs:600} as any;
 assert.deepEqual(SEARCH_CHIPS.filter(chip=>searchChipOffered(chip.id,capabilities)).map(chip=>chip.id),['all','movies','reading']);
});
test('a widened echo is still refused: a list never answers a group it did not ask for, a single group only itself',async()=>{
 const widened=new SearchService({scope,timeoutMs:1000,api:{request:<T>(path:string)=>{
  const answer=result(path.replace('groups=movies','groups=movies%2Cshows'));
  return Promise.resolve(answer) as Promise<T>;
 }}});
 try{
  await widened.select('harbor',{groups:['movies']});
  assert.equal(widened.getSnapshot().phase,'error');
  assert.equal(widened.getSnapshot().error?.code,'invalid_search');
 }finally{widened.dispose();}
 const empty=new SearchService({scope,timeoutMs:1000,api:{request:<T>(path:string)=>{
  const answer=result(path);answer.query.group='movies';answer.query.groups=[];answer.groups=[];
  return Promise.resolve(answer) as Promise<T>;
 }}});
 try{
  await empty.select('harbor',{group:'movies'});
  assert.equal(empty.getSnapshot().phase,'error');
 }finally{empty.dispose();}
});
