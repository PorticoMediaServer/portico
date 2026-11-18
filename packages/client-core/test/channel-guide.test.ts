import test from 'node:test';
import assert from 'node:assert/strict';
import {parseChannelGuide,dayPrimeTime,type ChannelApi,type GuideRoute} from '../src/channel-guide.ts';
const route:GuideRoute={kind:'live-source',start:'2026-09-05T12:00:00Z',end:'2026-09-05T15:00:00Z',timezone:'UTC',search:'',sourceId:''};
const fixture=()=>({protocolVersion:'1.0',serverId:'server',guide:{state:'ready',viewerFence:'viewer',start:route.start,end:route.end,timezone:route.timezone,nextCursor:'',observedAt:'2026-09-05T12:00:00Z',sources:[{id:'source',name:'Live source',generation:'generation',publishedAt:'2026-09-05T11:00:00Z',refreshState:'manual',availableStart:'2026-09-05T12:00:00Z',availableEnd:'2026-09-05T13:00:00Z',provenance:'live-source'}],channels:[{id:'channel',sourceId:'source',provenance:'live-source',name:'Channel one',number:'1',group:'Local',generation:'generation',tuneAvailable:false,recordAvailable:false,tuneUnavailableReason:'delivery-unavailable',recordUnavailableReason:'recording-unavailable',favorite:false,hidden:false,preferenceRevision:0,programmes:[{id:'occurrence',channelId:'channel',title:'Programme',start:'2026-09-05T12:00:00Z',end:'2026-09-05T13:00:00Z',lineage:'provider-id'}]}]}});
test('guide preserves genuine full-source marks and never derives missing or malformed marks',()=>{
 const value=fixture();
 Object.assign(value.guide.channels[0],{mark:{text:'N24',seed:'source:channel',privateFact:'discard'}});
 assert.deepEqual(parseChannelGuide(value,'server',route).channels[0].mark,{text:'N24',seed:'source:channel'});
 for(const mark of [undefined,null,{},'N24',{text:'',seed:'source:channel'},{text:'N24',seed:''},{text:'N24\nprivate',seed:'source:channel'},{text:'N24',seed:'x'.repeat(514)}]){
  Object.assign(value.guide.channels[0],{mark});
  const channel=parseChannelGuide(value,'server',route).channels[0];
  assert.equal(channel.name,'Channel one');
  assert.equal(channel.mark,undefined);
 }
});
test('guide validates source/generation/interval and strips unknown fields',()=>{const value=fixture();Object.assign(value.guide,{providerURL:'secret'});const guide=parseChannelGuide(value,'server',route);assert.equal(guide.channels[0].programmes[0].title,'Programme');assert.ok(!JSON.stringify(guide).includes('secret'));assert.throws(()=>parseChannelGuide(value,'other-server',route));
 // A channel from another generation of its source is left out alone; so is a programme that is not a real interval.
 value.guide.channels[0].generation='old';assert.deepEqual(parseChannelGuide(value,'server',route).channels,[]);value.guide.channels[0].generation='generation';value.guide.channels[0].programmes[0].end=route.start;assert.deepEqual(parseChannelGuide(value,'server',route).channels[0].programmes,[]);});
test('guide programme facts: full programme parses, minimal programme has no facts, malformed facts are dropped',()=>{
 const full=fixture();
 Object.assign(full.guide,{days:8});
 Object.assign(full.guide.channels[0].programmes[0],{subtitle:'The Arrival',episode:{season:2,number:5,display:'S2E5'},categories:['Drama','Mystery'],rating:{system:'VCHIP',value:'TV-14'},year:2019,starRating:'7/10',flags:{live:false,new:true,premiere:true,repeat:false},image:'/v1/items/item123/art/poster'});
 const guide=parseChannelGuide(full,'server',route);
 const p=guide.channels[0].programmes[0];
 assert.equal(p.subtitle,'The Arrival');
 assert.deepEqual(p.episode,{season:2,number:5,display:'S2E5'});
 assert.deepEqual(p.categories,['Drama','Mystery']);
 assert.deepEqual(p.rating,{system:'VCHIP',value:'TV-14'});
 assert.equal(p.year,2019);
 assert.equal(p.starRating,'7/10');
 assert.deepEqual(p.flags,{live:false,new:true,premiere:true,repeat:false});
 assert.equal(p.image,'/v1/items/item123/art/poster');
 assert.equal(guide.days,8);
 const minimal=fixture();
 const bare=parseChannelGuide(minimal,'server',route);
 const q=bare.channels[0].programmes[0];
 assert.equal(q.subtitle,undefined);
 assert.equal(q.episode,undefined);
 assert.equal(q.categories,undefined);
 assert.equal(q.rating,undefined);
 assert.equal(q.year,undefined);
 assert.equal(q.starRating,undefined);
 assert.equal(q.flags,undefined);
 assert.equal(q.image,undefined);
 assert.equal(bare.days,undefined);
 const bad=fixture();
 Object.assign(bad.guide,{days:99});
 Object.assign(bad.guide.channels[0].programmes[0],{subtitle:42,episode:{season:0,number:'x'},categories:'not-an-array',rating:{value:42},year:'n/a',starRating:7,flags:'yes',image:'https://provider.invalid/icon.png'});
 const dropped=parseChannelGuide(bad,'server',route);
 const r=dropped.channels[0].programmes[0];
 assert.equal(r.subtitle,undefined);
 assert.equal(r.episode,undefined);
 assert.deepEqual(r.categories,[]);
 assert.equal(r.rating,undefined);
 assert.equal(r.year,undefined);
 assert.equal(r.starRating,undefined);
 assert.equal(r.flags,undefined);
 assert.equal(r.image,undefined);
 assert.equal(dropped.days,undefined);
 assert.ok(!JSON.stringify(dropped).includes('provider.invalid'));
});
test('Library Channel millisecond intervals decode without advertising DVR',()=>{
 const v=fixture();const libraryRoute:GuideRoute={...route,kind:'library-channel'};
 v.guide.sources[0].provenance='library-channel';v.guide.channels[0].provenance='library-channel';
 v.guide.channels[0].recordUnavailableReason='not-recordable';
 v.guide.channels[0].programmes[0].lineage='generated';
 v.guide.channels[0].programmes[0].start='2026-09-05T12:00:00.125Z';
 v.guide.channels[0].programmes[0].end='2026-09-05T13:00:00.875Z';
 const got=parseChannelGuide(v,'server',libraryRoute);assert.equal(got.channels[0].programmes[0].start,'2026-09-05T12:00:00.125Z');
 assert.equal(got.channels[0].recordAvailable,false);
 // A library channel is never recordable, whatever the document says.
 v.guide.channels[0].recordAvailable=true;v.guide.channels[0].recordUnavailableReason='';
 const again=parseChannelGuide(v,'server',libraryRoute).channels[0];assert.equal(again.recordAvailable,false);assert.equal(again.recordUnavailableReason,'delivery-unavailable');
});
test('a channel is available only when the server says so outright and gives no reason against',()=>{
 for(const override of [{tuneAvailable:'true'},{tuneAvailable:true},{recordAvailable:true},{tuneUnavailableReason:'unknown-policy'},{recordUnavailableReason:''},{recordUnavailableReason:'a-reason-from-a-newer-server'}]){
  const v=fixture();Object.assign(v.guide.channels[0],override);const channel=parseChannelGuide(v,'server',route).channels[0];
  assert.equal(channel.tuneAvailable,false,JSON.stringify(override));assert.equal(channel.recordAvailable,false,JSON.stringify(override));
  assert.notEqual(channel.tuneUnavailableReason,'');assert.notEqual(channel.recordUnavailableReason,'');
 }
 const unknown=fixture();unknown.guide.channels[0].tuneUnavailableReason='unknown-policy';assert.equal(parseChannelGuide(unknown,'server',route).channels[0].tuneUnavailableReason,'delivery-unavailable','a reason this app does not know reads as plainly unavailable');
 const v=fixture();v.guide.channels[0].tuneAvailable=true;v.guide.channels[0].tuneUnavailableReason='';
 assert.equal(parseChannelGuide(v,'server',route).channels[0].tuneAvailable,true);
});
test('invalid calendar dates, protocol versions and foreign source membership fail closed',()=>{
 const v=fixture();v.protocolVersion='2.0';assert.throws(()=>parseChannelGuide(v,'server',route));
 // A channel of a source the answer does not carry is left out; a programme on a day that does not exist is left out.
 v.protocolVersion='1.0';v.guide.channels[0].sourceId='not-returned';assert.deepEqual(parseChannelGuide(v,'server',route).channels,[]);
 v.guide.channels[0].sourceId='source';v.guide.channels[0].programmes[0].start='2026-02-30T12:00:00Z';assert.deepEqual(parseChannelGuide(v,'server',route).channels[0].programmes,[]);
});
test('one channel, programme or source that cannot be read is left out and the rest of the guide is kept',()=>{
 const v:any=fixture();const good=v.guide.channels[0];
 v.guide.sources.push({id:'broken'});
 v.guide.channels=[{...good,id:'second',name:'Channel two',programmes:[{...good.programmes[0],id:'p2',channelId:'second',lineage:'guessed',newEvidence:'maybe'},{id:'nope'},{...good.programmes[0],id:'p3',channelId:'second'}]},'nope',{...good,id:'third',sourceId:'broken'},good];
 v.guide.state='partly-cloudy';v.guide.future=true;
 const guide=parseChannelGuide(v,'server',route);
 assert.deepEqual(guide.channels.map(c=>c.id),['second','channel']);
 assert.deepEqual(guide.channels[0].programmes.map(p=>[p.id,p.lineage,p.newEvidence]),[['p2','interval-only','unknown']],'an overlapping repeat of the slot is left out');
 assert.equal(guide.sources.length,1);assert.equal(guide.state,'ready');
 const over:any=fixture();over.guide.channels=Array(51).fill(over.guide.channels[0]);assert.throws(()=>parseChannelGuide(over,'server',route),'the bound on channels is exact');
});
test('a favourite is the profile\'s channel marks on the rebuilt server',async()=>{
 const {saveChannelPreference}=await import('../src/channel-guide.ts');const channel=parseChannelGuide(fixture(),'server',route).channels[0];
 const sent:unknown[]=[];
 const api:ChannelApi={request:async<T>(p:string,method?:string,body?:unknown)=>{sent.push([method,p,body]);return {favorite:true,hidden:false} as T}};
 await saveChannelPreference(api,channel,{favorite:true,hidden:false});
 assert.deepEqual(sent,[['PUT','/v1/live/channels/channel/state',{favorite:true,hidden:false}]]);
});

test('dayPrimeTime lands at 18:00 local on ordinary days and across DST changes',()=>{
 const zone='America/New_York';
 assert.equal(new Date(dayPrimeTime(Date.parse('2026-10-30T04:00:00Z'),zone)).toISOString(),'2026-10-30T22:00:00.000Z');
 assert.equal(new Date(dayPrimeTime(Date.parse('2026-11-01T04:00:00Z'),zone)).toISOString(),'2026-11-01T23:00:00.000Z');
 assert.equal(new Date(dayPrimeTime(Date.parse('2026-03-08T05:00:00Z'),zone)).toISOString(),'2026-03-08T22:00:00.000Z');
});

test('the bounded named-channel guide accepts fifty rows and rejects a larger response',()=>{
 const value=fixture();const channel=value.guide.channels[0];
 value.guide.channels=Array.from({length:50},(_,i)=>({...channel,id:'channel'+i,programmes:[]}));
 assert.equal(parseChannelGuide(value,'server',route).channels.length,50);
 value.guide.channels.push({...channel,id:'overflow',programmes:[]});
 assert.throws(()=>parseChannelGuide(value,'server',route));
});


test('guide accepts programme-bound images and refuses unscoped or external image URLs',()=>{
 const safe='/v1/guide/programme-images/'+'ab'.repeat(24)+'/'+'cd'.repeat(32);
 for(const image of [safe,'/v1/guide/images/'+'ab'.repeat(32),'https://provider.invalid/icon.png',safe+'?token=secret',safe.replace('programme-images/','programme-images/../')]){
  const value=fixture();Object.assign(value.guide.channels[0].programmes[0],{image});
  const parsed=parseChannelGuide(value,'server',route).channels[0].programmes[0];
  assert.equal(parsed.image,image===safe?safe:undefined);
 }
});
