import test from 'node:test';import assert from 'node:assert/strict';
import {parsePersonPage,parsePeopleDirectory,personPath,peopleSearchPath,personPortraitPath,readPerson} from '../src/people.ts';
import {personalBatchBody,parsePersonalBatchReceipt,parseEntryOutcomes,MAX_PERSONAL_BATCH} from '../src/personal-saved.ts';
import {savedListPath,personalHistoryPath} from '../src/saved.ts';
const scope={serverId:'server',viewerId:JSON.stringify(['local','account','profile'])};
const media=(id:string)=>({id,libraryId:'library',kind:'movie',title:'Film '+id,navigation:{view:'item',entityId:id}});
const person=(patch:any={})=>({id:'person-1',name:'Ada Lovelace',sortName:'ada lovelace',biography:'',birthDate:'',deathDate:'',portraitUrl:'/v1/people/person-1/portrait?size=thumbnail&v=abc',roles:['Acting'],knownFor:[media('a')],providerIds:{tmdb:'42'},revision:1,...patch});
const page=(patch:any={})=>({serverId:'server',viewerFence:'fence',revision:{catalog:1,viewer:2},person:person(),credits:[{media:media('a'),role:'Herself',character:'Herself',department:'Acting',creditKind:'cast'}],pageInfo:{total:1,nextCursor:''},...patch});

test('a person page binds to the requested server, person and role filter',()=>{
 const parsed=parsePersonPage(page(),scope,{limit:40,personId:'person-1'});
 assert.equal(parsed.person.name,'Ada Lovelace');
 assert.equal(parsed.credits[0].creditKind,'cast');
 assert.equal(parsed.person.providerIds.tmdb,'42');
 assert.throws(()=>{(parsed.credits as any).push({});});
 // A page for another server or another person is refused; a credit of a role the
 // client did not ask for is left out rather than rendered under the wrong heading.
 assert.throws(()=>parsePersonPage(page({serverId:'other'}),scope,{limit:40,personId:'person-1'}));
 assert.throws(()=>parsePersonPage(page(),scope,{limit:40,personId:'person-2'}));
 assert.throws(()=>parsePersonPage(page({viewerFence:''}),scope,{limit:40,personId:'person-1'}));
 assert.throws(()=>parsePersonPage(page({revision:{catalog:1}}),scope,{limit:40,personId:'person-1'}));
 assert.throws(()=>parsePersonPage(page({person:person({revision:-1})}),scope,{limit:40,personId:'person-1'}));
 assert.equal(parsePersonPage(page(),scope,{limit:40,personId:'person-1',role:'crew'}).credits.length,0);
});
test('one credit that cannot be read is left out, and a person reads neutrally where only drawn',()=>{
 const credit=(id:string,patch:any={})=>({media:media(id),role:'r',character:'c',department:'Acting',creditKind:'cast',...patch});
 const parsed=parsePersonPage(page({future:1,person:{id:'person-1',name:'Ada Lovelace',revision:3,roles:['Acting',7,'Acting'],knownFor:[media('a'),media('a'),{id:'broken'}],providerIds:{tmdb:'42',bad:7},future:true},credits:[credit('a'),credit('b',{creditKind:'guest'}),{media:{id:'broken'},creditKind:'cast'},credit('c',{role:undefined,character:undefined,department:undefined,future:1}),'nope'],pageInfo:{total:1,nextCursor:''}}),scope,{limit:40,personId:'person-1'});
 assert.deepEqual(parsed.credits.map(c=>[c.media.id,c.role,c.character,c.department]),[['a','r','c','Acting'],['c','','','']]);
 assert.equal(parsed.pageInfo.total,2,'the total is at least the credits kept');
 assert.deepEqual(parsed.person,{id:'person-1',name:'Ada Lovelace',sortName:'',biography:'',birthDate:'',deathDate:'',portraitUrl:'',roles:['Acting'],knownFor:[media('a')],providerIds:{tmdb:'42'},revision:3});
});
test('known-for and credit pages stay bounded and consistent with pageInfo',()=>{
 assert.throws(()=>parsePersonPage(page({person:person({knownFor:Array.from({length:9},(_,i)=>media('k'+i))})}),scope,{limit:40,personId:'person-1'}));
 assert.throws(()=>parsePersonPage(page({credits:Array.from({length:41},(_,i)=>({media:media('c'+i),role:'r',character:'c',department:'Acting',creditKind:'cast'}))}),scope,{limit:40,personId:'person-1'}));
 assert.throws(()=>parsePersonPage(page({pageInfo:{total:1,nextCursor:7}}),scope,{limit:40,personId:'person-1'}));
 assert.equal(parsePersonPage(page({pageInfo:{total:0,nextCursor:''}}),scope,{limit:40,personId:'person-1'}).pageInfo.total,1);
 // A continuation on a page the server did not fill would skip rows.
 assert.throws(()=>parsePersonPage(page({pageInfo:{total:9,nextCursor:'next'}}),scope,{limit:40,personId:'person-1'}));
 const full=parsePersonPage(page({credits:[{media:media('a'),role:'r',character:'r',department:'Acting',creditKind:'cast'}],pageInfo:{total:9,nextCursor:'next'}}),scope,{limit:1,personId:'person-1'});
 assert.equal(full.pageInfo.nextCursor,'next');
});
test('people directory keeps the people it can read and refuses foreign servers',()=>{
 const directory=parsePeopleDirectory({serverId:'server',viewerFence:'fence',revision:{catalog:1,viewer:0},q:'ada',people:[{id:'p1',name:'Ada',sortName:'ada',roles:[],creditCount:2}]},scope,25);
 assert.equal(directory.people[0].portraitUrl,'');
 assert.deepEqual(parsePeopleDirectory({serverId:'server',viewerFence:'fence',revision:{catalog:1,viewer:0},future:1,people:[{id:'p1',name:'A',sortName:'a',roles:[],creditCount:0},{id:'p1',name:'B',sortName:'b',roles:[],creditCount:0},{id:'p2'},'nope',{id:'p3',name:'C',future:true}]},scope,25),{serverId:'server',viewerFence:'fence',revision:{catalog:1,viewer:0},q:'',people:[{id:'p1',name:'A',sortName:'a',portraitUrl:'',roles:[],creditCount:0},{id:'p3',name:'C',sortName:'',portraitUrl:'',roles:[],creditCount:0}]});
 assert.throws(()=>parsePeopleDirectory({serverId:'server',viewerFence:'fence',revision:{catalog:1,viewer:0},q:'ada',people:Array.from({length:26},(_,i)=>({id:'p'+i,name:'A'}))},scope,25));
 assert.throws(()=>parsePeopleDirectory({serverId:'other',viewerFence:'fence',revision:{catalog:1,viewer:0},q:'ada',people:[]},scope,25));
});
test('person paths are server addresses the client only assembles',()=>{
 assert.equal(personPath('p 1',{role:'cast',limit:10}),'/v1/people/p%201?limit=10&role=cast');
 assert.equal(personPortraitPath('p1'),'/v1/people/p1/portrait?size=thumbnail');
 assert.match(peopleSearchPath('  Ada   Lovelace '),/^\/v1\/people\?q=Ada\+Lovelace&limit=25$/);
 assert.throws(()=>personPath('p1',{limit:101}));
 assert.throws(()=>personPath('p1',{role:'director' as any}));
 assert.throws(()=>peopleSearchPath('   '));
});
test('readPerson requests exactly what it validates',async()=>{
 let requested='';
 const parsed=await readPerson({request:async<T>(path:string)=>{requested=path;return page() as T;}},scope,'person-1',{limit:40});
 assert.equal(requested,'/v1/people/person-1?limit=40');
 assert.equal(parsed.credits.length,1);
});
test('a bulk personal intent is one bounded request answered row by row',()=>{
 const body=personalBatchBody('op-1',[{itemId:'a',watchlisted:true},{itemId:'b',watched:true,favorite:true}]);
 assert.deepEqual(body.items.map(i=>i.itemId),['a','b']);
 assert.throws(()=>personalBatchBody('op 1',[{itemId:'a',watched:true}]));
 assert.throws(()=>personalBatchBody('op-1',[{itemId:'a',watched:true},{itemId:'a',watched:false}]));
 assert.throws(()=>personalBatchBody('op-1',[{itemId:'a'}]));
 assert.throws(()=>personalBatchBody('op-1',Array.from({length:MAX_PERSONAL_BATCH+1},(_,i)=>({itemId:'i'+i,watched:true}))));
 const receipt=parsePersonalBatchReceipt({serverId:'server',viewerFence:'fence',operationId:'op-1',updated:1,failed:1,results:[{itemId:'a',ok:true,personal:{revision:1}},{itemId:'b',ok:false,code:'personal_state_conflict',message:'stale'}]},'server',['a','b']);
 assert.equal(receipt.updated,1);
 assert.equal(receipt.results[1].code,'personal_state_conflict');
 // A reply that answers a different set, or miscounts its own outcomes, is refused.
 assert.throws(()=>parsePersonalBatchReceipt({serverId:'server',viewerFence:'fence',operationId:'op-1',updated:2,failed:0,results:[{itemId:'a',ok:true,personal:null},{itemId:'b',ok:false,code:'x'}]},'server',['a','b']));
 assert.throws(()=>parsePersonalBatchReceipt({serverId:'server',viewerFence:'fence',operationId:'op-1',updated:1,failed:0,results:[{itemId:'a',ok:true,personal:null}]},'server',['a','b']));
});
test('membership outcomes account for every item exactly once',()=>{
 const outcomes=parseEntryOutcomes({added:['a'],removed:['b'],unchanged:['c'],failed:[{itemId:'d',code:'not_found'}]});
 assert.deepEqual(outcomes.added,['a']);
 assert.equal(outcomes.failed[0].code,'not_found');
 assert.throws(()=>parseEntryOutcomes({added:['a'],removed:['a'],unchanged:[],failed:[]}));
 assert.throws(()=>parseEntryOutcomes({added:['a'],removed:[],unchanged:[],failed:[{itemId:'d'}]}));
});
test('saved list and history addresses carry only published filters and periods',()=>{
 assert.equal(savedListPath('watchlist',{filter:'inProgress',sort:'progress',direction:'desc'}),'/v1/content?view=watchlist&limit=40&filter=inProgress&sort=progress&direction=desc');
 assert.equal(personalHistoryPath({period:'7d',libraryId:'lib'}),'/v1/personal-history?limit=40&period=7d&libraryId=lib');
 assert.throws(()=>savedListPath('favorites',{filter:'everything' as any}));
 assert.throws(()=>savedListPath('favorites',{sort:'rank' as any}));
 assert.throws(()=>personalHistoryPath({period:'forever' as any}));
 assert.throws(()=>personalHistoryPath({limit:101}));
});
