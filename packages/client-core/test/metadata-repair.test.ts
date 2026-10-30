import test from 'node:test';
import assert from 'node:assert/strict';
import {MetadataRepairService,validateRepair} from '../src/metadata-repair.ts';
const scope={serverId:'server',viewerId:'owner'},target={kind:'item' as const,id:'movie',libraryId:'library'};
function response(){return {serverId:'server',viewerFence:'a'.repeat(64),target:{kind:'item',id:'movie'},libraryId:'library',revision:'b'.repeat(64),
 schema:[{field:'title',label:'Title',group:'general',type:'text',maxLength:300,bulk:false},{field:'contentRating',label:'Content rating',group:'general',type:'text',maxLength:300,bulk:true},{field:'tags',label:'Tags',group:'general',type:'list',maxLength:128,max:64,bulk:true}],
 artworkRoles:['poster','backdrop'],
 snapshot:{fields:{title:{value:'Before',automaticValue:'Before',source:'automatic',locked:false},contentRating:{value:'PG',automaticValue:'R',source:'manual',locked:true},tags:{value:'["Noir"]',automaticValue:'',source:'manual',locked:true,values:['Noir']}},identity:{provider:'tmdb',id:'12',revision:1,status:'matched',locked:false},relationshipLocks:{},relationships:[],artwork:[]},candidates:[],history:[],artwork:{candidates:[],jobs:[]},cascades:[]};}
function deferred(){let resolve!:(v:any)=>void;return {promise:new Promise<any>(r=>resolve=r),resolve};}
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
test('owner commands use the last read revision and are never guessed from drafts',async()=>{
 const calls:any[]=[];let raw=response();const service=new MetadataRepairService({scope,api:{async request(path,method,body){calls.push({path,method,body});if(method==='POST')raw={...raw,revision:'c'.repeat(64)};return raw as any;}}});
 await service.load(target);assert.equal(await service.command({action:'edit',fields:{title:{value:'Owner'}}}),true);
 assert.deepEqual(calls[1].body,{action:'edit',fields:{title:{value:'Owner'}},expectedRevision:'b'.repeat(64)});assert.equal(service.getSnapshot().data!.revision,'c'.repeat(64));service.dispose();
});
test('lost write stays uncertain, cannot replay, and reconciles only by a fresh read',async()=>{
 const pending=deferred();let writes=0;let raw=response();const service=new MetadataRepairService({scope,timeoutMs:5,api:{request(_path,method){if(method==='POST'){writes++;return pending.promise;}return Promise.resolve(raw) as any;}}});
 await service.load(target);assert.equal(await service.command({action:'select_artwork',candidateId:'c'.repeat(64),confirm:true}),false);assert.equal(service.getSnapshot().phase,'uncertain');await assert.rejects(()=>service.command({action:'repair_assets'}));raw={...raw,revision:'d'.repeat(64)};pending.resolve(raw);await Promise.resolve();assert.equal(service.getSnapshot().phase,'uncertain');await service.load(target);assert.equal(service.getSnapshot().data!.revision,'d'.repeat(64));assert.equal(writes,1);service.dispose();
});
test('conflicts retain review values while denial clears them and hides server error text',async()=>{
 let error:any={status:409,message:'PRIVATE'};const service=new MetadataRepairService({scope,api:{async request(_p,method){if(method==='POST')throw error;return response() as any;}}});await service.load(target);await service.command({action:'lock_all',confirm:true});assert.equal(service.getSnapshot().phase,'conflict');assert.ok(service.getSnapshot().data);await service.load(target);error={status:403,message:'PRIVATE'};await service.command({action:'lock_all',confirm:true});assert.equal(service.getSnapshot().phase,'denied');assert.equal(service.getSnapshot().data,null);assert.doesNotMatch(JSON.stringify(service.getSnapshot()),/PRIVATE/);service.dispose();
});
test('cancel/dispose fence ignoring-abort reads and late completions',async()=>{
 for(const dispose of [false,true]){const pending=deferred();const service=new MetadataRepairService({scope,api:{request:()=>pending.promise}});const read=service.load(target);if(dispose)service.dispose();else service.cancel();await read;pending.resolve(response());await Promise.resolve();assert.equal(service.getSnapshot().data,null);}
});
test('changed owner fence cannot reuse an old repair view',async()=>{
 let raw=response();const service=new MetadataRepairService({scope,api:{async request(){return raw as any;}}});await service.load(target);raw={...raw,viewerFence:'f'.repeat(64)};await service.load(target);assert.equal(service.getSnapshot().phase,'denied');assert.equal(service.getSnapshot().data,null);service.dispose();
});
test('merge preview is non-destructive, revision-bound and scoped to the requested target',async()=>{
 const calls:any[]=[];const service=new MetadataRepairService({scope,api:{async request(path,method,body){calls.push({path,method,body});if(path.includes('/preview'))return {revision:'b'.repeat(64),descendants:600,lockedFields:1,selectedImages:1,message:'Retained',merge:{target:{kind:'item',id:'other'},allowed:false,blockers:['Unregistered references'],references:{'progress.item_id':3}}} as any;return response() as any;}}});await service.load(target);await service.preview('other');assert.equal(service.getSnapshot().preview!.descendants,600);assert.equal(service.getSnapshot().preview!.merge!.allowed,false);assert.equal(calls.filter(c=>c.method==='POST').length,0);service.dispose();
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
test('effect cleanup can cancel and reload the same service without stale publication',async()=>{
 const first=deferred();let calls=0;const service=new MetadataRepairService({scope,api:{request(){calls++;return calls===1?first.promise:Promise.resolve(response()) as any;}}});
 const loading=service.load(target);service.cancel();await loading;await service.load(target);
 first.resolve({...response(),revision:'c'.repeat(64)});await Promise.resolve();
 assert.equal(service.getSnapshot().phase,'ready');assert.equal(service.getSnapshot().data!.revision,'b'.repeat(64));service.dispose();
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
