import test from 'node:test';
import assert from 'node:assert/strict';
import {ChannelGuideService,parseChannelGuide,topChannelGroups,stripDays,dayPrimeTime,type ChannelApi,type GuideRoute} from '../src/channel-guide.ts';
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
test('old request cannot overwrite newer guide intent; dispose fences outstanding response',async()=>{const pending:Array<(v:unknown)=>void>=[];const api:ChannelApi={request:<T>()=>new Promise<T>(resolve=>pending.push(v=>resolve(v as T)))};const service=new ChannelGuideService(api,'server');const old=service.load(route);const changed={...route,search:'new'};const latest=service.load(changed);pending[1](fixture());await latest;assert.equal(service.getSnapshot().route?.search,'new');pending[0](fixture());await old;assert.equal(service.getSnapshot().route?.search,'new');const last=service.load(route);service.dispose();pending[2](fixture());await last;assert.equal(service.getSnapshot().route?.search,'');assert.equal(service.getSnapshot().guide,null);});
test('temporary refresh failure retains guide; authorization failure removes it',async()=>{let failure:unknown;const api:ChannelApi={request:async<T>()=>{if(failure)throw failure;return fixture() as T}};const service=new ChannelGuideService(api,'server');await service.load(route);failure=new Error('raw private diagnostics');await service.load(route,'refresh');assert.equal(service.getSnapshot().guide?.channels.length,1);assert.ok(!service.getSnapshot().error?.includes('private'));failure={status:403};await service.load(route,'refresh');assert.equal(service.getSnapshot().guide,null);service.dispose();});
test('M25-2: a single 429 retries automatically with Retry-After, then succeeds', async () => {
  let calls = 0;
  const api: ChannelApi = {request: async <T>() => { calls++; if (calls === 1) throw {status: 429, retryAfterSeconds: 0}; return fixture() as T; }};
  const service = new ChannelGuideService(api, 'server');
  await service.load(route);
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.equal(calls, 2, 'one automatic retry, no manual Try again');
  service.dispose();
});
test('M25-2: an HTTP-date Retry-After is honoured, persistent 429s surface after a few attempts', async () => {
  const future = new Date(Date.now() + 100).toUTCString();
  let calls = 0;
  const dated: ChannelApi = {request: async <T>() => { calls++; if (calls === 1) throw {status: 429, retryAfter: future}; return fixture() as T; }};
  const datedService = new ChannelGuideService(dated, 'server');
  await datedService.load(route);
  assert.equal(datedService.getSnapshot().phase, 'ready');
  datedService.dispose();
  let failed = 0;
  const always: ChannelApi = {request: async <T>(): Promise<T> => { failed++; throw {status: 429, retryAfterSeconds: 0}; }};
  const failing = new ChannelGuideService(always, 'server');
  await failing.load(route);
  assert.equal(failing.getSnapshot().phase, 'error');
  assert.equal(failed, 3, 'bounded: at most a few attempts');
  failing.dispose();
});
test('deadline ends non-settling adapter and access recovery establishes new fence',{timeout:1000},async()=>{const stuck:ChannelApi={request:<T>()=>new Promise<T>(()=>{})};const service=new ChannelGuideService(stuck,'server',5);await service.load(route);assert.equal(service.getSnapshot().phase,'error');service.dispose();let next=fixture();const api:ChannelApi={request:async<T>()=>next as T};const recovered=new ChannelGuideService(api,'server');await recovered.load(route);next=fixture();next.guide.viewerFence='new-fence';await recovered.load(route,'refresh');assert.equal(recovered.getSnapshot().guide,null);recovered.resetAccess();await recovered.load(route);assert.equal(recovered.getSnapshot().guide?.viewerFence,'new-fence');recovered.dispose();});

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
test('favorite mutation binds exact profile-scoped revision and verifies server response',async()=>{
 const {saveChannelPreference}=await import('../src/channel-guide.ts');const channel=parseChannelGuide(fixture(),'server',route).channels[0];
 let sent:unknown;let path='';let response:unknown={protocolVersion:'1.0',serverId:'server',revision:1};
 const api:ChannelApi={request:async<T>(p,_method,body)=>{path=p;sent=body;return response as T}};
 assert.equal(await saveChannelPreference(api,'server',channel,'same-operation',{favorite:true,hidden:false}),1);
 assert.equal(path,'/v1/channels/preferences');assert.deepEqual(sent,{requestId:'same-operation',sourceId:'source',channelId:'channel',expectedRevision:0,favorite:true,hidden:false});
 response={protocolVersion:'1.0',serverId:'other-server',revision:1};await assert.rejects(saveChannelPreference(api,'server',channel,'same-operation',{favorite:true,hidden:false}));
 response={protocolVersion:'1.0',serverId:'server',revision:0};await assert.rejects(saveChannelPreference(api,'server',channel,'same-operation',{favorite:true,hidden:false}));
});
test('operation identifiers require two valid secure UUID results',async()=>{
 const {channelOperationId}=await import('../src/channel-guide.ts');let calls=0;
 const id=await channelOperationId(async()=>{calls++;return '12345678-1234-4123-8123-123456789abc'});
 assert.equal(calls,2);assert.match(id,/^[0-9a-f]{48}$/);
 await assert.rejects(channelOperationId(async()=> 'untrusted-random-value'));
});

test('FEAT-04: group chips rank the top groups by count (ties keep server order)',()=>{
 const channels=[{group:'News'},{group:'Sports'},{group:'News'},{group:'Movies'},{group:'Sports'},{group:'Sports'},{group:'Kids'},{group:''},{group:'Movies'}];
 assert.deepEqual(topChannelGroups(channels),['Sports','News','Movies','Kids']);
 assert.deepEqual(topChannelGroups(channels,2),['Sports','News']);
 assert.deepEqual(topChannelGroups([],5),[]);
 assert.deepEqual(topChannelGroups([{group:''}]),[]);
});

test('FEAT-04: day strip lists Today plus the next days across a DST change (America/New_York)',()=>{
 const zone='America/New_York';
 // A week crossing the fall-back (2026-11-01, a 25-hour day): 7 consecutive local midnights.
 const fall=stripDays(Date.parse('2026-10-30T15:00:00Z'),zone);
 assert.equal(fall.length,7);
 const fmt=new Intl.DateTimeFormat('en-US',{timeZone:zone,year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hourCycle:'h23'});
 const at=(ms:number)=>fmt.format(new Date(ms));
 assert.equal(at(fall[0]),'10/30/2026, 00:00');
 assert.equal(at(fall[2]),'11/01/2026, 00:00');
 assert.equal(at(fall[3]),'11/02/2026, 00:00');
 assert.equal(fall[3]-fall[2],25*3600_000);
 // A week crossing the spring-forward (2026-03-08, a 23-hour day).
 const spring=stripDays(Date.parse('2026-03-07T15:00:00Z'),zone);
 assert.equal(spring.length,7);
 assert.equal(at(spring[1]),'03/08/2026, 00:00');
 assert.equal(at(spring[2]),'03/09/2026, 00:00');
 assert.equal(spring[2]-spring[1],23*3600_000);
 // Prime time is the local 18:00 hour on ordinary and DST days alike.
 assert.equal(at(dayPrimeTime(fall[2],zone)),'11/01/2026, 18:00');
 assert.equal(at(dayPrimeTime(spring[1],zone)),'03/08/2026, 18:00');
 assert.equal(at(dayPrimeTime(fall[0],zone)),'10/30/2026, 18:00');
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
