import test from 'node:test';import assert from 'node:assert/strict';
import {SearchService,type SearchGroup} from '../src/search.ts';
import {SEARCH_CHIPS,searchChipOffered} from '../src/presentation/search-model.ts';
const scope={serverId:'server',viewerId:JSON.stringify(['hosted','account','profile'])};
/** The rebuilt server: it searches no Live TV yet, so a Live TV group never comes back. */
const kinds={movies:'movie',shows:'show',episodes:'episode',artists:'artist',albums:'album',songs:'track',books:'audiobook',reading:'book',people:'person'} as const;
function result(_path:string):any{
 return {groups:(Object.entries(kinds) as [SearchGroup,string][]).map(([id,kind])=>id==='people'?{kind,total:1,people:[{id:'one',name:'Server one',images:{}}]}:{kind,total:1,items:[{id:'one',kind,title:'Server one',libraryId:'library'}]})};
}
function service(){return new SearchService({scope,timeoutMs:1000,api:{request:<T>(path:string)=>Promise.resolve(result(path)) as Promise<T>}});}

test('a requested group the server does not search is absent, not a refused answer',async()=>{
 const s=service();
 try{
  await s.select('harbor',{groups:['movies','shows','episodes','people','artists','albums','songs','books','live-tv']});
  const snapshot=s.getSnapshot();
  assert.equal(snapshot.phase,'ready','answer refused: '+JSON.stringify(snapshot.error));
  assert.deepEqual(snapshot.groups.map(value=>value.id),['movies','shows','episodes','artists','albums','songs','books','people']);
  assert.equal(snapshot.groups.find(value=>value.id==='movies')?.totalCount,1);
  assert.equal(snapshot.data?.capabilities.groups.some(value=>value.id==='live-tv'),false);
 }finally{s.dispose();}
});
test('every chip the server can answer is offered, the same while one type is open; Live TV has none',async()=>{
 const s=service();
 try{
  const chips=()=>SEARCH_CHIPS.filter(chip=>searchChipOffered(chip.id,s.getSnapshot().data!.capabilities)).map(chip=>chip.id);
  await s.select('harbor');const offered=chips();
  assert.equal(offered.includes('live-tv' as never),false);assert.ok(offered.includes('reading'));assert.ok(offered.includes('movies'));
  await s.select('harbor',{group:'shows'});assert.deepEqual(chips(),offered);
 }finally{s.dispose();}
});
test('before any answer every chip but the optional Reading is drawn; an unavailable group never is',()=>{
 assert.deepEqual(SEARCH_CHIPS.filter(chip=>searchChipOffered(chip.id,undefined)).map(chip=>chip.id),SEARCH_CHIPS.map(chip=>chip.id).filter(id=>id!=='reading'));
 const capabilities={groups:[{id:'movies',available:true},{id:'live-tv',available:false},{id:'reading',available:true}],sorts:[],directions:[],maxLimit:40,groupBudgetMs:600} as any;
 assert.deepEqual(SEARCH_CHIPS.filter(chip=>searchChipOffered(chip.id,capabilities)).map(chip=>chip.id),['all','movies','reading']);
});
