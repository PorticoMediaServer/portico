import test from 'node:test';
import assert from 'node:assert/strict';
import {validateRepair} from '../src/metadata-repair.ts';
const scope={serverId:'server',viewerId:'owner'},target={kind:'item' as const,id:'movie',libraryId:'library'};
function response(){return {serverId:'server',viewerFence:'a'.repeat(64),target:{kind:'item',id:'movie'},libraryId:'library',revision:'b'.repeat(64),
 schema:[{field:'title',label:'Title',group:'general',type:'text',maxLength:300,bulk:false},{field:'contentRating',label:'Content rating',group:'general',type:'text',maxLength:300,bulk:true},{field:'tags',label:'Tags',group:'general',type:'list',maxLength:128,max:64,bulk:true}],
 artworkRoles:['poster','backdrop'],
 snapshot:{fields:{title:{value:'Before',automaticValue:'Before',source:'automatic',locked:false},contentRating:{value:'PG',automaticValue:'R',source:'manual',locked:true},tags:{value:'["Noir"]',automaticValue:'',source:'manual',locked:true,values:['Noir']}},identity:{provider:'tmdb',id:'12',revision:1,status:'matched',locked:false},relationshipLocks:{},relationships:[],artwork:[]},candidates:[],history:[],artwork:{candidates:[],jobs:[]},cascades:[]};}
test('repair projections require exact target, authority fence and canonical roles',()=>{
 const raw=response();assert.equal(validateRepair(raw,scope,target).revision,raw.revision);
 for(const patch of [{serverId:'other'},{libraryId:'other'},{viewerFence:'invalid'},{target:{kind:'album',id:'movie'}}])assert.throws(()=>validateRepair({...raw,...patch},scope,target));
 const art={role:'still',subject:'',candidateId:'c'.repeat(64),digest:'d'.repeat(64),locked:true,revision:1,attribution:'Local artwork',url:'/v1/metadata/item/movie/art/still?v='+ 'd'.repeat(64)};
 assert.equal(validateRepair({...raw,snapshot:{...raw.snapshot,artwork:[art]}},scope,target).snapshot.artwork.length,1);
 assert.deepEqual(validateRepair({...raw,snapshot:{...raw.snapshot,artwork:[{...art,role:'clearart'}]}},scope,target).snapshot.artwork,[]);
 assert.deepEqual(validateRepair({...raw,snapshot:{...raw.snapshot,artwork:[{...art,url:'https://provider.invalid/private'}]}},scope,target).snapshot.artwork,[]);
});
test('repair validates relationship provenance, empty locked classes and bounded history',()=>{
 const raw=response();const relationship={kind:'credit',recordId:'provider-credit',provider:'tmdb',targetKind:'person',targetId:'100',label:'Performer',role:'Character',department:'Acting',source:'tmdb',ordinal:0,locked:true};
 const data=validateRepair({...raw,snapshot:{...raw.snapshot,relationshipLocks:{credit:true},relationships:[relationship]}},scope,target);assert.equal(data.snapshot.relationships[0].recordId,'provider-credit');assert.equal(data.snapshot.relationshipLocks.credit,true);
 assert.throws(()=>validateRepair({...raw,history:Array.from({length:31},(_,i)=>({id:i+1,trigger:'edit',observedAt:'2026-09-06T00:00:00Z'}))},scope,target));
});
test('repair projection strips opaque actor and acquisition fields',()=>{
 const raw=response();const data=validateRepair({...raw,actor:'PRIVATE',snapshot:{...raw.snapshot,identity:{...raw.snapshot.identity,lockedByUserId:'PRIVATE'}},artwork:{candidates:[{id:'c'.repeat(64),role:'poster',subject:'',provider:'tmdb',imageId:'x',locale:'en',rank:1,attribution:'TMDB',observedAt:'2026-09-06T00:00:00Z',current:true,origin:'PRIVATE'}],jobs:[]}},scope,target);assert.doesNotMatch(JSON.stringify(data),/PRIVATE|lockedByUserId|origin/);
});
test('screen candidates retain real provider ordering choices without acquisition data',()=>{
 const raw=response();const candidate={id:'c'.repeat(64),provider:'tvdb',title:'Series',subtitle:'show · 42',observedAt:'2026-09-07',orders:[{id:'official',name:'Aired order',private:'PRIVATE'},{id:'dvd',name:'DVD order'}],inputDigest:'PRIVATE'};
 const data=validateRepair({...raw,snapshot:{...raw.snapshot,identity:{...raw.snapshot.identity,engine:'screen',type:'show'}},candidates:[candidate]},scope,target);
 assert.equal(data.snapshot.identity?.engine,'screen');assert.deepEqual(data.candidates[0].orders,[{id:'official',name:'Aired order'},{id:'dvd',name:'DVD order'}]);assert.doesNotMatch(JSON.stringify(data),/PRIVATE/);
 assert.deepEqual(validateRepair({...raw,candidates:[{...candidate,orders:[{id:'x'.repeat(161),name:'Order'}]}]},scope,target).candidates,[]);
});
test('cascade admission, terminal and skipped counts remain separately projected',()=>{
 const raw=response();const c={id:'operation',intent:'refresh_unlocked',status:'queued',processed:8,failed:1,completed:3,skipped:2,pending:2};
 assert.deepEqual(validateRepair({...raw,cascades:[c]},scope,target).cascades,[c]);
 assert.deepEqual(validateRepair({...raw,cascades:[{...c,completed:-1}]},scope,target).cascades,[]);
});
test('the registry drives the editable fields, and an unlisted field is refused',()=>{
 const raw=response();const data=validateRepair(raw,scope,target);
 assert.equal(data.schema.length,3);assert.equal(data.schema[1].bulk,true);assert.deepEqual([...data.artworkRoles],['poster','backdrop']);
 assert.deepEqual([...data.snapshot.fields.tags.values!],['Noir']);
 assert.equal(data.snapshot.fields.contentRating.source,'manual');
 // A value with no registry entry, and a registry entry with no value, are both broken projections.
 assert.throws(()=>validateRepair({...raw,snapshot:{...raw.snapshot,fields:{...raw.snapshot.fields,mystery:{value:'x',automaticValue:'',source:'manual',locked:false}}}},scope,target));
 assert.throws(()=>validateRepair({...raw,schema:[...raw.schema,{field:'studio',label:'Studio',group:'general',type:'text',bulk:true}]},scope,target));
 assert.deepEqual(validateRepair({...raw,artworkRoles:['clearart']},scope,target).artworkRoles,[]);
 assert.throws(()=>validateRepair({...raw,schema:[{field:'title',label:'Title',group:'nowhere',type:'text',bulk:false}],snapshot:{...raw.snapshot,fields:{title:raw.snapshot.fields.title}}},scope,target));
 // A parsed list only belongs to a list field.
 assert.throws(()=>validateRepair({...raw,snapshot:{...raw.snapshot,fields:{...raw.snapshot.fields,contentRating:{...raw.snapshot.fields.contentRating,values:['PG']}}}},scope,target));
});
test('identity candidates publish a year and only a proxied preview path',()=>{
 const raw=response();
 const candidate={id:'c'.repeat(64),provider:'tmdb',title:'Movie',subtitle:'1999',year:1999,observedAt:'2026-09-06T00:00:00Z',previewUrl:'/v1/metadata/item/movie/art/poster?v='+'d'.repeat(64)+'&candidate='+'c'.repeat(64)};
 const data=validateRepair({...raw,candidates:[candidate]},scope,target);
 assert.equal(data.candidates[0].year,1999);assert.ok(data.candidates[0].previewUrl!.startsWith('/v1/metadata/item/movie/art/'));
 assert.deepEqual(validateRepair({...raw,candidates:[{...candidate,previewUrl:'https://image.tmdb.org/t/p/w500/x.jpg'}]},scope,target).candidates,[]);
 assert.deepEqual(validateRepair({...raw,candidates:[{...candidate,year:99999}]},scope,target).candidates,[]);
});

test('CD-47: role 500, code-point list tags, 64 order choices and 2048 order names',()=>{
 const raw=response();
 // 301–500 role accepted, 501 refused.
 const rel={kind:'credit',targetKind:'person',targetId:'100',label:'Performer',role:'r'.repeat(500),department:'Acting',source:'tmdb',ordinal:0,locked:true};
 assert.equal(validateRepair({...raw,snapshot:{...raw.snapshot,relationships:[rel]}},scope,target).snapshot.relationships.length,1);
 assert.deepEqual(validateRepair({...raw,snapshot:{...raw.snapshot,relationships:[{...rel,role:'r'.repeat(501)}]}},scope,target).snapshot.relationships,[]);
 // 128 astral-character tag accepted, 129 refused; control still rejected.
 const tags={value:'',automaticValue:'',source:'manual',locked:true,values:['𠮷'.repeat(128)]};
 assert.deepEqual(validateRepair({...raw,snapshot:{...raw.snapshot,fields:{...raw.snapshot.fields,tags}}},scope,target).snapshot.fields.tags.values,['𠮷'.repeat(128)]);
 assert.throws(()=>validateRepair({...raw,snapshot:{...raw.snapshot,fields:{...raw.snapshot.fields,tags:{...tags,values:['𠮷'.repeat(129)]}}}},scope,target));
 assert.throws(()=>validateRepair({...raw,snapshot:{...raw.snapshot,fields:{...raw.snapshot.fields,tags:{...tags,values:['bad\x07']}}}},scope,target));
 // 33–64 order choices accepted, 65 refused; 513–2048 order names accepted, 2049 refused.
 const orders=(n:number,nameLen:number)=>Array.from({length:n},(_,i)=>({id:`order${i}`,name:'n'.repeat(nameLen)}));
 const cand=(orders:any)=>({id:'c'.repeat(64),provider:'tvdb',title:'Series',subtitle:'show',observedAt:'2026-09-07',orders});
 assert.equal(validateRepair({...raw,candidates:[cand(orders(64,2048))]},scope,target).candidates[0].orders!.length,64);
 assert.equal(validateRepair({...raw,candidates:[cand(orders(33,513))]},scope,target).candidates[0].orders!.length,33);
 assert.deepEqual(validateRepair({...raw,candidates:[cand(orders(65,10))]},scope,target).candidates,[]);
 assert.deepEqual(validateRepair({...raw,candidates:[cand(orders(1,2049))]},scope,target).candidates,[]);
});

test('Part 2.1: artwork candidate votes decode when present and stay absent when null',()=>{
 const raw=response();
 const base={id:'c'.repeat(64),role:'poster',subject:'',provider:'tmdb',imageId:'x',locale:'en',rank:7.5,attribution:'TMDB',observedAt:'2026-09-06T00:00:00Z',current:true};
 assert.equal(validateRepair({...raw,artwork:{candidates:[{...base,votes:37}],jobs:[]}},scope,target).artwork.candidates[0].votes,37);
 assert.equal(validateRepair({...raw,artwork:{candidates:[{...base,votes:null}],jobs:[]}},scope,target).artwork.candidates[0].votes,null);
 assert.equal('votes' in validateRepair({...raw,artwork:{candidates:[base],jobs:[]}},scope,target).artwork.candidates[0],false);
 assert.deepEqual(validateRepair({...raw,artwork:{candidates:[{...base,votes:-1}],jobs:[]}},scope,target).artwork.candidates,[]);
});

test('candidate command keys and typed provider identities remain distinct', () => {
 const candidate={id:'d'.repeat(64),identityId:'12',identityType:'movie',provider:'tmdb',title:'Movie',subtitle:'',observedAt:''};
 const raw={...response(),candidates:[candidate]};
 const parsed=validateRepair(raw,scope,target);
 assert.equal(parsed.candidates[0]?.id,candidate.id);
 assert.equal(parsed.candidates[0]?.identityId,'12');
 assert.equal(parsed.candidates[0]?.identityType,'movie');
 const malformed=validateRepair({...raw,candidates:[{...candidate,identityType:['movie']}]},scope,target);
 assert.equal(malformed.candidates.length,0);
});

// OW-3/OW-4: the service publishes a code and status, never an English sentence, and a field the server refused is a
// definite answer that keeps the editor's values (it used to read as an uncertain write, then as "Something went wrong").
