import test from 'node:test';
import assert from 'node:assert/strict';
import {legacyGuideSource,legacyChannelSources,legacyGuideCatalog,legacyChannel,GuideWindowStore,parseGuideDirectory,parseGuideSourceSummaries} from '../src/guide/index.ts';
import type {ChannelApi} from '../src/channel-guide.ts';
const NOW=Date.parse('2026-10-09T12:00:00Z');
const sources=[{id:'a',name:'First source',generation:'g',publishedAt:new Date(NOW).toISOString(),availableStart:'',availableEnd:'',provenance:'live-source',refreshState:'healthy'},{id:'b',name:'Later source',generation:'h',publishedAt:new Date(NOW).toISOString(),availableStart:'',availableEnd:'',provenance:'live-source',refreshState:'healthy'}];
const channel=(n:number)=>({id:'c'+n,sourceId:n<2000?'a':'b',generation:n<2000?'g':'h',provenance:'live-source',name:'Channel '+n,number:String(n),group:'News',programmes:[],tuneAvailable:true,recordAvailable:true,tuneUnavailableReason:'',recordUnavailableReason:'',favorite:n===0,hidden:false,preferenceRevision:0});
function server(){
 const requests:string[]=[];let revision='r1';
 const summaries=()=>({protocolVersion:'1.0',serverId:'s',viewerFence:'f',revision,sources:sources.map((source,index)=>({...source,position:index,recordAvailable:true,guideDays:7,channelCount:index?1:2000,favoriteCount:index?0:1,groups:['News'],groupCounts:[{name:'News',count:index?1:2000}],unavailableReasons:{}}))});
 const api:ChannelApi={async request<T>(path:string):Promise<T>{requests.push(path);const q=new URL(path,'https://s').searchParams;
  if(path.startsWith('/v1/guide/sources?'))return summaries() as T;
  if(path.startsWith('/v1/guide/channels?')){let offset=Number(q.get('offset'));const limit=Number(q.get('limit'));if(q.has('anchorChannelId')){const index=Number(q.get('anchorChannelId')!.slice(1));if(!Number.isInteger(index)||index<0||index>2000)throw Object.assign(new Error('not found'),{status:404});offset=(index+(q.get('direction')==='next'?1:-1)+2001)%2001;}if(q.get('revision')&&q.get('revision')!==revision)throw Object.assign(new Error('changed'),{code:'guide_refresh_required',status:409});const channels=Array.from({length:Math.max(0,Math.min(limit,2001-offset))},(_,i)=>channel(offset+i));return {protocolVersion:'1.0',serverId:'s',directory:{viewerFence:'f',revision,total:2001,offset,limit,channels,sources:sources.filter(s=>channels.some(c=>c.sourceId===s.id))}} as T;}
  const named=q.get('channels')!.split(',');const start=q.get('start')!,end=q.get('end')!;
  const channels=named.map(id=>({...channel(Number(id.slice(1))),programmes:[{id:'p'+id,channelId:id,title:'Programme',start,end,lineage:'provider-id'}]}));
  return {protocolVersion:'1.0',serverId:'s',guide:{state:'ready',viewerFence:'f',start,end,timezone:q.get('timezone'),observedAt:new Date(NOW).toISOString(),nextCursor:'',channels,sources:sources.filter(s=>channels.some(c=>c.sourceId===s.id))}} as T;
 }};
 return {api,requests,summaries,revision:(r:string)=>{revision=r;}};
}
const settle=()=>new Promise(setImmediate);

test('a 2,001-channel lineup loads source rail and visible page in one request each',async()=>{
 const s=server();const rails=await legacyChannelSources(s.api,'s','UTC','Library Channels',NOW);
 assert.deepEqual(rails.map(s=>s.id),['a','b'],'providers after the first page remain visible');assert.equal(s.requests.length,1);
 const source=legacyGuideSource(s.api,'s',{kind:'live-source',sourceId:'',timezone:'UTC',sort:'number'});const signal=new AbortController().signal;
 const page=await source.channels(0,50,signal);assert.equal(page.total,2001);assert.equal(page.items.length,50);assert.equal(s.requests.length,2);assert.equal(legacyChannel(page.items[49])?.id,'c49');
 const programmes=await source.programs(page.items.map(c=>c.id),NOW,NOW+3600000,signal);
 assert.equal(s.requests.length,3,'one named-channel time block, not another directory walk');assert.equal(Object.keys(programmes).length,50);assert.equal(programmes.c49[0].title,'Programme');
 await source.channels(50,50,signal);assert.ok(s.requests.at(-1)?.includes('revision=r1'));
});

test('summary supplies all group chips and favorites without downloading channel rows',async()=>{
 const s=server();const catalog=await legacyGuideCatalog(s.api,'s',{kind:'all',sourceId:'',timezone:'UTC'});
 assert.deepEqual(catalog,{groups:['News'],favorites:true,unavailableReasons:[]});assert.equal(s.requests.length,1);assert.ok(s.requests[0].startsWith('/v1/guide/sources?'));
});

test('directory revision conflicts reset the store and recover the current view',async()=>{
 const s=server();const source=legacyGuideSource(s.api,'s',{kind:'live-source',sourceId:'',timezone:'UTC'});
 await source.channels(0,50,new AbortController().signal);s.revision('r2');
 const store=new GuideWindowStore({source,marginPages:0,marginBlocks:0});store.setViewport({firstRow:50,lastRow:51,start:NOW,end:NOW+3600000});
 for(let n=0;n<12;n++)await settle();
 assert.equal(store.channelAt(50)?.id,'c50');assert.ok(s.requests.some(p=>p.includes('revision=r1')));assert.equal(store.getSnapshot().channelsError,false);store.dispose();
});

test('directory and summary responses fail closed on foreign membership or unexpected programme data',()=>{
 const s=server();const summary=s.summaries();const all=summary.sources.length;summary.sources[0].favoriteCount=3000;
 // A source whose own counts cannot be true is left out alone; the others keep their own counts.
 const kept=parseGuideSourceSummaries(summary,'s').sources;assert.equal(kept.length,all-1);assert.ok(kept.every(source=>source.id!==summary.sources[0].id));
 assert.throws(()=>parseGuideSourceSummaries({...summary,serverId:'other'},'s'));
 const value={protocolVersion:'1.0',serverId:'s',directory:{viewerFence:'f',revision:'r',total:1,offset:0,limit:1,channels:[channel(0)],sources:[sources[0]]}};
 // A channel of a source the answer does not carry, or one carrying programmes in a rows-only answer, is left out alone.
 value.directory.channels[0].sourceId='b';assert.deepEqual(parseGuideDirectory(value,'s',0,1).channels,[]);value.directory.channels[0].sourceId='a';
 (value.directory.channels[0].programmes as unknown[]).push({id:'p'});assert.deepEqual(parseGuideDirectory(value,'s',0,1).channels,[]);
 assert.throws(()=>parseGuideDirectory({...value,serverId:'other'},'s',0,1));
});

test('absent directory or summaries never trigger compatibility enumeration',async()=>{
 for(const status of [404,405]){
  const requests:string[]=[];
  const unavailable=Object.assign(new Error('unavailable'),{status});
  const api:ChannelApi={async request(path){requests.push(path);throw unavailable;}};
  const source=legacyGuideSource(api,'s',{kind:'live-source',sourceId:'',timezone:'UTC'});
  await assert.rejects(source.channels(0,10,new AbortController().signal),e=>e===unavailable);
  await assert.rejects(legacyChannelSources(api,'s','UTC','Library Channels',NOW),e=>e===unavailable);
  await assert.rejects(legacyGuideCatalog(api,'s',{kind:'all',sourceId:'',timezone:'UTC'}),e=>e===unavailable);
  assert.equal(requests.length,3);
  assert.ok(requests.every(p=>p.startsWith('/v1/guide/channels?')||p.startsWith('/v1/guide/sources?')));
 }
});

test('bounded directory reads pass each viewport cancellation to its own transport',async()=>{
 const signals:AbortSignal[]=[];
 const api:ChannelApi={async request<T>(_path:string,_method?:string,_body?:unknown,signal?:AbortSignal):Promise<T>{
  signals.push(signal!);return new Promise<T>((_resolve,reject)=>signal!.addEventListener('abort',()=>reject(new Error('aborted')),{once:true}));
 }};
 const source=legacyGuideSource(api,'s',{kind:'live-source',sourceId:'',timezone:'UTC'}),a=new AbortController(),b=new AbortController();
 const first=source.channels(0,10,a.signal).catch(()=>undefined),second=source.channels(10,10,b.signal).catch(()=>undefined);
 assert.equal(signals.length,2);a.abort();await first;assert.equal(signals[1].aborted,false);b.abort();await second;assert.equal(signals[1].aborted,true);
});

test('transient modern API failure remains a retryable failure instead of starting a full legacy walk',async()=>{
 let requests=0;const busy=Object.assign(new Error('busy'),{status:503,retryable:true,retryAfterSeconds:5});
 const source=legacyGuideSource({request:async()=>{requests++;throw busy;}},'s',{kind:'live-source',sourceId:'',timezone:'UTC'});
 await assert.rejects(source.channels(0,50,new AbortController().signal),e=>e===busy);assert.equal(requests,1);
});


test('surfing a 2,001-channel view requests one neighbor and wraps both ends',async()=>{
 const s=server(),signal=new AbortController().signal;
 const source=legacyGuideSource(s.api,'s',{kind:'all',sourceId:'',timezone:'UTC',sort:'name',group:'News',favorites:true,includeHidden:true});
 const first=(await source.channels(0,1,signal)).items[0];
 const last=await source.neighbor!(first,-1,signal);assert.equal(last?.id,'c2000');
 const wrap=await source.neighbor!(last!,1,signal);assert.equal(wrap?.id,'c0');
 assert.equal(s.requests.length,3,'no full directory enumeration for either neighbor');
 for(const path of s.requests.slice(1)){const q=new URL(path,'https://s').searchParams;assert.equal(q.get('limit'),'1');assert.equal(q.has('offset'),false);assert.equal(q.get('group'),'News');assert.equal(q.get('favorites'),'true');assert.equal(q.get('includeHidden'),'true');assert.equal(q.get('sort'),'name');assert.equal(q.get('anchorProvenance'),'live-source');}
 assert.equal(new URL(s.requests[2],'https://s').searchParams.get('anchorSourceId'),'b');
});

test('neighbor retries one stale revision and unknown anchors never trigger legacy scans',async()=>{
 const s=server(),signal=new AbortController().signal;
 const source=legacyGuideSource(s.api,'s',{kind:'all',sourceId:'',timezone:'UTC'});const first=(await source.channels(0,1,signal)).items[0];
 s.revision('r2');assert.equal((await source.neighbor!(first,1,signal))?.id,'c1');assert.equal(s.requests.length,3);
 assert.ok(s.requests[1].includes('revision=r1'));assert.equal(new URL(s.requests[2],'https://s').searchParams.has('revision'),false);
 const changed={...legacyChannel(first)!,id:'c9000'};
 // A foreign/native handle cannot manufacture an authorized anchor.
 assert.equal(await source.neighbor!({...first,native:changed},1,signal),undefined);assert.equal(s.requests.length,3);
 const value={protocolVersion:'1.0',serverId:'s',directory:{viewerFence:'f',revision:'r',total:1,offset:1,limit:1,channels:[],sources:[]}};
 assert.throws(()=>parseGuideDirectory(value,'s',undefined,1));
});

test('programme airings keep real live flags and episode facts, bound pages and cancel the entire viewer on refusal',async()=>{
 const {GuideFeatureClient,parseGuideAirings}=await import('../src/guide/directory.ts');
 const p={id:'p',channelId:'c',title:'Show',subtitle:'Episode name',start:'2026-10-09T12:00:00Z',end:'2026-10-09T13:00:00Z',lineage:'interval-only',newEvidence:'unknown',flags:{live:false,new:false,premiere:false,repeat:false},categories:['Live'],image:'/v1/items/item/art/poster'};
 const a={channel:{id:'c',sourceId:'s',generation:'g',provenance:'live-source',name:'Channel',number:'1',group:'News',privateURL:'secret'},programme:p};
 const page={groups:[{key:'title:show',title:'Show',airings:[a]}],nextCursor:'next',revision:'r',viewerFence:'f',start:'2026-10-09T00:00:00Z',end:'2026-10-10T00:00:00Z',timezone:'UTC',today:'2026-10-09'};
 const parsed=parseGuideAirings(page);assert.equal(parsed.groups[0].airings[0].programme.flags?.live,false,'a Live category is never a live flag');assert.equal(parsed.groups[0].airings[0].programme.subtitle,'Episode name');assert.ok(!JSON.stringify(parsed).includes('secret'));
 assert.throws(()=>parseGuideAirings({...page,groups:[{...page.groups[0],airings:Array(51).fill(a)}]}));
 const api:ChannelApi={async request(){throw {status:403}}};const client=new GuideFeatureClient(api,'server');await assert.rejects(client.airings({start:page.start,end:page.end,search:'Episode'}));await assert.rejects(client.reminders());
 const foreign=new GuideFeatureClient({async request<T>(){return {protocolVersion:'1.0',serverId:'foreign',airings:page} as T}},'server');await assert.rejects(foreign.airings({start:page.start,end:page.end}));foreign.dispose();
});
