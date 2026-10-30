import test from 'node:test';import assert from 'node:assert/strict';import {validateRelatedMovies} from '../src/related-movies.ts';import {DetailService} from '../src/detail.ts';
const target={libraryId:'lib',itemId:'source',kind:'movie'};
const row=()=>({id:'genre:tmdb:16',relation:'genre',provider:'tmdb',evidenceId:'16',heading:'More Animation movies',entries:[{id:'related',libraryId:'lib',kind:'movie',title:'Related',available:true,navigation:{view:'item',entityId:'related'}}]});
const related=()=>({version:1,libraryId:'lib',libraryName:'Movies',rows:[row()]});
const detail=(id='source')=>({scope:{serverId:'server',libraryId:'lib',itemId:id,viewerFence:'fence'},revision:{catalog:2,viewer:0},item:{id,libraryId:'lib',kind:'movie',title:id,available:true,duration:10,progressSeconds:0},personal:{revision:0,watchlisted:false,favorite:false,rating:null},metadata:{status:'available',ratings:[],genres:[],credits:[]},actions:[],related:related()});
test('related movie service preserves server labels/order as immutable navigation targets',()=>{const p=validateRelatedMovies(related(),target);assert.equal(p.rows[0].heading,'More Animation movies');assert.ok(Object.isFrozen(p.rows[0].entries));assert.throws(()=>validateRelatedMovies({...related(),libraryId:'denied'},target));const other={...row().entries[0],id:'other',navigation:{view:'item',entityId:'other'}};
 // An entry that is not an available title of this library, the title itself, or one that would play, is left out alone; a row with nothing left is not shown.
 for(const patch of [{id:'source'},{libraryId:'other'},{kind:'song'},{available:false},{navigation:{view:'item',entityId:'other'}},{playback:{itemId:'related'}}]){assert.equal(validateRelatedMovies({...related(),rows:[{...row(),entries:[{...row().entries[0],...patch}]}]},target).rows.length,0);assert.deepEqual(validateRelatedMovies({...related(),rows:[{...row(),entries:[{...row().entries[0],...patch},other,other]}]},target).rows[0].entries.map(e=>e.id),['other']);}
 // The version, the library and the bound of 3 rows stay exact; a row with more than 12 entries is left out alone.
 assert.throws(()=>validateRelatedMovies({...related(),rows:Array(4).fill(row())},target));assert.throws(()=>validateRelatedMovies({...related(),version:2},target));assert.throws(()=>validateRelatedMovies('nope',target));
 const person={...row(),id:'person:tmdb:9',relation:'person',evidenceId:'9'};
 assert.deepEqual(validateRelatedMovies({...related(),future:1,libraryName:undefined,rows:[{...row(),entries:Array(13).fill(row().entries[0])},{...person,heading:undefined,future:true},{...person,id:'mood:tmdb:9',relation:'mood'}]},target),{version:1,libraryId:'lib',libraryName:'',rows:[{id:'person:tmdb:9',relation:'person',provider:'tmdb',evidenceId:'9',heading:'',entries:[row().entries[0]]}]});
 // A repeated row keeps its first appearance; at most two genre rows and one person row are shown.
 const genre=(n:number)=>({...row(),id:`genre:tmdb:${n}`,evidenceId:String(n)});
 assert.deepEqual(validateRelatedMovies({...related(),rows:[genre(1),genre(1),genre(2)]},target).rows.map(r=>r.id),['genre:tmdb:1','genre:tmdb:2']);
 assert.deepEqual(validateRelatedMovies({...related(),rows:[genre(1),genre(2),genre(3)]},target).rows.map(r=>r.id),['genre:tmdb:1','genre:tmdb:2']);});
test('older catalog or new viewer fence clears related targets on refresh; denial cannot retain cards',async()=>{for(const patch of [{revision:{catalog:1,viewer:0}},{scope:{serverId:'server',libraryId:'lib',itemId:'source',viewerFence:'changed'}},null]){let current:any=detail();const api={request:async<T>()=>{if(current===null)throw Object.assign(new Error('Access denied'),{code:'unauthorized'});return current as T}};const s=new DetailService({api,scope:{serverId:'server',viewerId:'viewer'},requestId:async()=>''});await s.select(target);current=patch===null?null:{...detail(),...patch};await s.refresh();assert.equal(s.getSnapshot().data,null);assert.equal(s.getSnapshot().phase,'error');s.dispose();}});
test('late related detail cannot replace another selected movie',async()=>{let resolve!:(v:any)=>void;const old=new Promise<any>(r=>resolve=r);const api={request:async<T>(path:string)=>path.includes('/source/')?old as Promise<T>:detail('new') as T};const s=new DetailService({api,scope:{serverId:'server',viewerId:'viewer'},requestId:async()=>''});const pending=s.select(target);await s.select({libraryId:'lib',itemId:'new'});resolve(detail());await pending;assert.equal(s.getSnapshot().data!.item.id,'new');s.dispose();});

test('related music and book rows preserve real entity navigation with strict family boundaries',()=>{
 for(const [kind,relation,entryKind,view] of [['song','album','album','album'],['song','artist','album','album'],['audiobook_file','book','book','book'],['audiobook_file','author','book','book']]){
  const projection={version:1,libraryId:'lib',libraryName:'Listening',rows:[{id:`${relation}:local:identity`,relation,provider:'local',evidenceId:'identity',heading:'More from this library',entries:[{id:'other',kind:entryKind,libraryId:'lib',title:'Related',available:true,navigation:{view,entityId:'other'}}]}]};
  assert.equal(validateRelatedMovies(projection,{...target,kind}).rows[0].entries[0].kind,entryKind);
  assert.equal(validateRelatedMovies(projection,target).rows.length,0);
  assert.equal(validateRelatedMovies({...projection,rows:[{...projection.rows[0],entries:[{...projection.rows[0].entries[0],libraryId:'private'}]}]},{...target,kind}).rows.length,0);
  assert.equal(validateRelatedMovies({...projection,rows:[{...projection.rows[0],entries:[{...projection.rows[0].entries[0],navigation:{view:'item',entityId:'wrong'}}]}]},{...target,kind}).rows.length,0);
 }
});

test('related discovery rejects playable child cards and accepts whole shows',()=>{
 const projection={version:1,libraryId:'lib',libraryName:'TV',rows:[{id:'show:local:seed',relation:'show',provider:'local',evidenceId:'seed',heading:'More shows',entries:[{id:'other',kind:'show',libraryId:'lib',title:'Related',available:true,navigation:{view:'show',entityId:'other'}}]}]};
 assert.equal(validateRelatedMovies(projection,{...target,kind:'episode'}).rows[0].entries[0].kind,'show');
 for(const [kind,relation,child] of [['episode','show','episode'],['song','album','song'],['audiobook_file','book','audiobook_file']]){const r={...projection.rows[0],id:`${relation}:local:seed`,relation,entries:[{...projection.rows[0].entries[0],kind:child,navigation:{view:'item',entityId:'other'}}]};assert.equal(validateRelatedMovies({...projection,rows:[r]},{...target,kind}).rows.length,0);}
});
