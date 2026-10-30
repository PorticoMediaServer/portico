import test from 'node:test';
import assert from 'node:assert/strict';
import {DVRClient,DVRModel,parseDVRPage,parseRecording,validRecordingOptions,validStoragePolicy,defaultRecordingOptions,dvrMessage,countUnwatched,seriesRuleConfig,type DVRCommandLifetime,type RecordingOptions} from '../src/dvr.ts';
import type {ChannelApi} from '../src/channel-guide.ts';
const hex=(n=64,c='a')=>c.repeat(n);
const time='2026-09-06T12:00:00Z',end='2026-09-06T13:00:00Z';
/** Padding on the wire is milliseconds; defaultRecordingOptions is the client's own (seconds) form. */
const wireOptions={beforeMs:0,afterMs:0,priority:0,retentionDays:0,episodeLimit:0};
const recording=()=>({id:hex(),revision:1,occurrence:{sourceId:hex(48),channelId:hex(64,'b'),generation:hex(48,'c'),programmeId:hex(64,'d')},programme:{id:hex(64,'d'),channelId:hex(64,'b'),title:'Owned programme',start:time,end,lineage:'provider-id',seriesId:'series',episodeId:'episode',newEvidence:'unknown'},options:{...wireOptions},start:time,end,state:'scheduled',reason:'',keep:false,itemId:'',libraryId:'',coverageStart:'',coverageEnd:'',bytes:0,ruleId:'',conflicts:[]});
const page=()=>({recordings:[recording()],rules:[],nextCursor:'',revision:1,captureAvailable:true,captureUnavailableReason:'',deletionAvailable:true,usage:{bytes:0,pendingDeleteBytes:0,recordings:1}});
const envelope=(result:unknown)=>({protocolVersion:'1.0',serverId:'server',result});
const nonce=async()=>hex(48,'e');
test('DVR channel marks retain only actual canonical text and seed',()=>{
 const raw={...recording(),channel:{id:hex(64,'b'),name:'Channel One',number:'1',mark:{text:'C1',seed:`${hex(48)}:${hex(64,'b')}`,privateURL:'https://private.invalid'}}};
 const parsed=parseRecording(raw);
 assert.deepEqual(parsed.channel?.mark,{text:'C1',seed:raw.channel.mark.seed});
 assert.ok(!JSON.stringify(parsed).includes('private'));
 const absent={...raw,channel:{id:raw.channel.id,name:raw.channel.name,number:raw.channel.number}};
 assert.equal('mark' in parseRecording(absent).channel!,false,'unknown stays absent without a seed fallback');
 for(const mark of [null,{text:'C1'},{text:'',seed:raw.channel.mark.seed},{text:'C1',seed:'other:channel'}]){
  assert.throws(()=>parseRecording({...raw,channel:{...raw.channel,mark}}));
 }
});
test('DVR public DTOs project known fields and reject foreign identity or invalid intervals',()=>{
 const raw=recording();Object.assign(raw,{path:'/private/secret',providerURL:'secret'});Object.assign(raw.options,{secret:'secret'});Object.assign(raw.programme,{secret:'secret'});
 assert.ok(!JSON.stringify(parseRecording(raw)).includes('secret'));
 raw.occurrence.programmeId=hex(64,'f');assert.throws(()=>parseRecording(raw));
 raw.occurrence.programmeId=raw.programme.id;raw.end=raw.start;assert.throws(()=>parseRecording(raw));
 raw.end=end;raw.programme.start='2026-02-30T12:00:00Z';assert.throws(()=>parseRecording(raw));
 // On a page, a repeated recording keeps its first row, and one that cannot be read or is in a state this app does not know is left out alone.
 const p:any=page();const first=p.recordings[0];p.recordings.push(first,{...first,id:hex(64,'9'),state:'archived-to-tape'},'nope');p.future=true;delete p.usage;p.captureAvailable='yes';
 const parsed=parseDVRPage(p);assert.deepEqual(parsed.recordings.map(r=>r.id),[first.id]);
 assert.equal(parsed.captureAvailable,false,'recording is offered only when the server says so outright');assert.deepEqual(parsed.usage,{bytes:0,pendingDeleteBytes:0,recordings:0});
 const over:any=page();over.recordings=Array(51).fill(first);assert.throws(()=>parseDVRPage(over));
 const noRevision:any=page();delete noRevision.revision;assert.throws(()=>parseDVRPage(noRevision));
});
test('DVR projections preserve observed incomplete coverage without inventing play authority',()=>{
 const r=recording();r.state='incomplete-playable';r.itemId=hex(64,'1');r.libraryId=hex(64,'2');r.coverageStart=time;r.coverageEnd='2026-09-06T12:30:00Z';r.bytes=188*100;
 const got=parseRecording(r);assert.equal(got.state,'incomplete-playable');assert.equal(got.coverageEnd,r.coverageEnd);assert.equal(got.libraryId,r.libraryId);assert.ok(!('streamURL' in got));
 r.libraryId='';assert.throws(()=>parseRecording(r));
});
test('DVR mutation retries reuse operation id and expected revision, including unknown outcomes',async()=>{
 let calls=0,ids=0;const sent:Array<{path:string;method:string;body:any}>=[];
 const api:ChannelApi={request:async<T>(path:string,method?:string,body?:unknown)=>{sent.push({path,method:method??'GET',body});if(++calls===1)throw new Error('unknown outcome');return envelope(recording()) as T;}};
 const client=new DVRClient(api,'server',async()=>{ids++;return nonce()});const r=parseRecording(recording());
 await assert.rejects(client.update(r,r.options));await client.update(r,r.options);
 assert.equal(ids,1);assert.deepEqual(sent[0],sent[1]);assert.equal(sent[0].body.expectedRevision,1);
 await client.keep(r);assert.equal(ids,2);assert.equal(sent[2].body.keep,true);client.dispose();
});
test('DVR stale revision is not automatically rebased or replayed with a fresh receipt',async()=>{
 const sent:any[]=[];const api:ChannelApi={request:async<T>(_p:string,_m?:string,b?:unknown)=>{sent.push(b);throw {status:409}}};
 const client=new DVRClient(api,'server',nonce);const r=parseRecording(recording());await assert.rejects(client.cancel(r));
 assert.equal(sent.length,1);assert.equal(sent[0].expectedRevision,r.revision);assert.match(dvrMessage({status:409}),/Refresh/);client.dispose();
});
test('DVR checks selected server, capacity shape, options bounds and exact rule nonce length',async()=>{
 let response:unknown={...envelope(page()),serverId:'foreign'};
 const api:ChannelApi={request:async<T>()=>response as T};const client=new DVRClient(api,'server',nonce);
 await assert.rejects(client.list('upcoming'));response=envelope({recording:recording(),capacity:{known:true,effective:0},overlaps:[],nextOverlap:''});await assert.rejects(client.detail(hex()));
 assert.equal((await client.newRuleID()).length,64);assert.equal(validRecordingOptions({...defaultRecordingOptions,beforeSeconds:21601}),false);
 assert.equal(validRecordingOptions({...defaultRecordingOptions,priority:-1000}),true);assert.equal(validStoragePolicy({revision:1,retentionDays:0,episodeLimit:0,floorBytes:Number.MAX_SAFE_INTEGER+1,capBytes:0}),false);client.dispose();
});
test('DVR late route responses and disposed viewers cannot revive private state',async()=>{
 const pending:Array<(v:unknown)=>void>=[];const api:ChannelApi={request:<T>()=>new Promise<T>(resolve=>pending.push(v=>resolve(v as T)))};
 const model=new DVRModel(new DVRClient(api,'server',nonce));const first=model.load({view:'upcoming'}),second=model.load({view:'history'});
 pending[1](envelope({...page(),revision:2}));await second;pending[0](envelope(page()));await first;assert.equal(model.getSnapshot().page?.revision,2);assert.equal(model.getSnapshot().query.view,'history');
 const third=model.load({view:'recorded'});model.dispose();pending[2](envelope(page()));await third;assert.equal(model.getSnapshot().page,null);assert.equal(model.getSnapshot().detail,null);
});
test('DVR temporary errors retain known same-route state, permission changes clear it',async()=>{
 let error:unknown;const api:ChannelApi={request:async<T>()=>{if(error)throw error;return envelope(page()) as T;}};
 const model=new DVRModel(new DVRClient(api,'server',nonce));await model.load();error=new Error('/private/path/provider-password');await model.load();assert.equal(model.getSnapshot().page?.recordings.length,1);assert.ok(!model.getSnapshot().error.includes('private'));
 error={status:403};await model.load();assert.equal(model.getSnapshot().page,null);model.dispose();
});
test('DVR deadline settles an adapter which ignores AbortSignal',{timeout:1000},async()=>{
 const api:ChannelApi={request:<T>()=>new Promise<T>(()=>{})};const client=new DVRClient(api,'server',nonce,5);await assert.rejects(client.list('upcoming'),/timed out/);client.dispose();
});
test('series rule DTOs do not retain injected provider fields',()=>{
 const p:any=page();p.rules=[{id:hex(),revision:1,reconcileState:'ready',diagnostic:'',updatedAt:time,secret:'secret',config:{name:'Rule',sourceId:hex(48),seriesId:'series',enabled:true,episodes:'new',allowedChannels:[],blockedChannels:[],keywords:[],blockedKeywords:[],options:{...wireOptions,secret:'secret'},secret:'secret'}}];
 assert.ok(!JSON.stringify(parseDVRPage(p)).includes('secret'));assert.equal(parseDVRPage(p).rules[0].config.episodes,'new');
});

test('Part 2.2: channel and watched parse behind presence; unwatched counts only false',()=>{
 const withBoth={...recording(),channel:{id:hex(),name:'Channel One',number:'1.1'},watched:false};
 const parsed=parseRecording(withBoth);
 assert.equal(parsed.channel!.name,'Channel One');
 assert.equal(parsed.watched,false);
 assert.equal(parseRecording({...recording(),watched:null}).watched,null);
 assert.equal(parseRecording({...recording(),watched:true}).watched,true);
 // Absent on older servers stays absent.
 assert.equal('channel' in parseRecording(recording()),false);
 assert.equal('watched' in parseRecording(recording()),false);
 // Empty channel name (gone from guide and source) decodes.
 assert.equal(parseRecording({...recording(),channel:{id:hex(),name:'',number:''}}).channel!.name,'');
 assert.throws(()=>parseRecording({...recording(),channel:{id:'bad',name:'X',number:'1'}}));
 assert.throws(()=>parseRecording({...recording(),watched:'yes' as any}));
 // Null counts as neither watched nor unwatched.
 assert.equal(countUnwatched([{watched:false},{watched:false},{watched:true},{watched:null},{}]),2);
 assert.equal(countUnwatched([]),0);
});

test('all DVR read projections drop unknown nested fields and validate overlap intervals',async()=>{
 let response:unknown;const api:ChannelApi={request:async<T>()=>envelope(response) as T};
 const client=new DVRClient(api,'server',nonce),r=parseRecording(recording());
 const cap={known:true,effective:2,planningEstimate:2,mode:'configured',secret:'secret'};
 const overlap={id:hex(64,'f'),title:'Other recording',start:time,end,priority:0,state:'scheduled',secret:'secret'};
 response={recording:recording(),capacity:cap,overlaps:[overlap],nextOverlap:'',secret:'secret'};
 assert.ok(!JSON.stringify(await client.detail(r.id)).includes('secret'));
 response={recording:recording(),capacity:cap,overlaps:[{...overlap,end:time}],nextOverlap:''};await assert.rejects(client.detail(r.id));
 const cfg={name:'Rule',sourceId:hex(48),seriesId:'series',enabled:true,episodes:'all' as const,allowedChannels:[],blockedChannels:[],keywords:[],blockedKeywords:[],options:defaultRecordingOptions};
 response={matches:1,unknownNewEvidence:1,sample:[recording().programme],generation:hex(48),capacity:cap,secret:'secret'};
 assert.ok(!JSON.stringify(await client.preview(cfg)).includes('secret'));
 response={channels:[{id:hex(),name:'Channel',number:'1',secret:'secret'}],generation:hex(48),nextCursor:'',secret:'secret'};
 assert.ok(!JSON.stringify(await client.channels(hex(48))).includes('secret'));
 const policy={revision:1,retentionDays:0,episodeLimit:0,floorBytes:1,capBytes:0,secret:'secret'};
 response={policy,measurement:{freeBytes:1,usedBytes:0,reservedBytes:0,writeHealthy:true,measuredAt:time,secret:'secret'},pendingDeleteBytes:0,forecastBytes:0,forecastHours:24,forecastDescription:'Estimate',captureAvailable:true,warning:'',secret:'secret'};
 assert.ok(!JSON.stringify(await client.storage()).includes('secret'));
 response=policy;assert.ok(!JSON.stringify(await client.saveStorage(policy)).includes('secret'));
 response={recordingId:r.id,revision:1,bytes:0,keep:false,activeReaders:false,result:'Delete?',secret:'secret'};
 assert.ok(!JSON.stringify(await client.deletePreview(r)).includes('secret'));client.dispose();
});

test('overlap denial clears private details and invalidates other responses in this viewer',async()=>{
 const pending:Array<{resolve:(v:unknown)=>void;reject:(e:unknown)=>void}>=[];
 let delayed=false;
 const detail={recording:recording(),capacity:{known:true,effective:1},overlaps:[],nextOverlap:hex(64,'f')};
 const api:ChannelApi={request:<T>()=>delayed?new Promise<T>((resolve,reject)=>pending.push({resolve:v=>resolve(v as T),reject})):Promise.resolve(envelope(detail) as T)};
 const model=new DVRModel(new DVRClient(api,'server',nonce));await model.load({view:'upcoming',recordingId:hex()});delayed=true;
 const more=model.moreOverlaps(),refresh=model.load();pending[0].reject({status:403});await more;
 assert.equal(model.getSnapshot().detail,null);assert.equal(model.getSnapshot().accessDenied,true);
 pending[1].resolve(envelope(detail));await refresh;assert.equal(model.getSnapshot().detail,null);model.dispose();
});

test('editor requests using the shared client invalidate model presentation on denial',async()=>{
 let denied=false;const api:ChannelApi={request:async<T>()=>{if(denied)throw {status:401};return envelope(page()) as T}};
 const model=new DVRModel(new DVRClient(api,'server',nonce));await model.load();denied=true;
 await assert.rejects(model.client.deletePreview(parseRecording(recording())));
 assert.equal(model.getSnapshot().page,null);assert.equal(model.getSnapshot().accessDenied,true);
 denied=false;await model.load();assert.equal(model.getSnapshot().accessDenied,false);assert.equal(model.getSnapshot().page?.recordings.length,1);model.dispose();
});

test('duplicate overlap pagination responses cannot append the same rows twice',async()=>{
 let wait=false;const pending:Array<(v:unknown)=>void>=[];
 const detail={recording:recording(),capacity:{known:true,effective:1},overlaps:[],nextOverlap:hex(64,'f')};
 const api:ChannelApi={request:<T>()=>wait?new Promise<T>(resolve=>pending.push(v=>resolve(v as T))):Promise.resolve(envelope(detail) as T)};
 const model=new DVRModel(new DVRClient(api,'server',nonce));await model.load({view:'upcoming',recordingId:hex()});wait=true;
 const first=model.moreOverlaps(),second=model.moreOverlaps();
 const next=envelope({...detail,nextOverlap:'',overlaps:[{id:hex(64,'e'),title:'Other',start:time,end,priority:0,state:'scheduled'}]});
 pending[1](next);await second;pending[0](next);await first;
 assert.equal(model.getSnapshot().detail?.overlaps.length,1);model.dispose();
});

test('effect replay reactivates the memoized model without reviving old requests or cleanup',async()=>{
 const pending:Array<(v:unknown)=>void>=[];
 const api:ChannelApi={request:<T>()=>new Promise<T>(resolve=>pending.push(v=>resolve(v as T)))};
 const model=new DVRModel(new DVRClient(api,'server',nonce));
 const old=model.activate(),first=model.load();model.deactivate(old);
 assert.equal(model.getSnapshot().page,null);
 const current=model.activate(),second=model.load({view:'recorded'});
 pending[1](envelope({...page(),revision:2}));await second;
 pending[0](envelope(page()));await first;model.deactivate(old);
 assert.equal(model.getSnapshot().page?.revision,2);assert.equal(model.getSnapshot().query.view,'recorded');
 model.deactivate(current);assert.equal(model.getSnapshot().page,null);
});


test('an old action cannot clear a newer activation mutation fence', async()=>{
 const api={request:async()=>({protocolVersion:'1.0',serverId:'server',result:page()})} as ChannelApi;
 const model=new DVRModel(new DVRClient(api,'server',nonce));
 const oldLease=model.activate();
 let finishOld!:()=>void,finishNew!:()=>void;
 const oldAction=model.act(()=>new Promise<void>(resolve=>{finishOld=resolve}));
 model.deactivate(oldLease);model.activate();
 const newAction=model.act(()=>new Promise<void>(resolve=>{finishNew=resolve}));
 finishOld();assert.equal(await oldAction,false);
 assert.equal(model.getSnapshot().mutating,true);
 let ran=false;assert.equal(await model.act(async()=>{ran=true}),false);assert.equal(ran,false);
 finishNew();assert.equal(await newAction,true);assert.equal(model.getSnapshot().mutating,false);
 model.dispose();
});


test('late nonce failure from disposed client lifetime cannot delete a new retry receipt',async()=>{
 let rejectOld!:(e:unknown)=>void,ids=0;
 const api={request:async()=>{throw new Error('unknown transport outcome')}} as ChannelApi;
 const client=new DVRClient(api,'server',()=>++ids===1?new Promise<string>((_,reject)=>{rejectOld=reject}):nonce());
 const r=parseRecording(recording());
 const old=assert.rejects(client.keep(r));client.activate();
 await assert.rejects(client.keep(r));
 rejectOld(new Error('old nonce failed'));await old;
 await assert.rejects(client.keep(r));assert.equal(ids,2);
 client.dispose();
});

test('FEAT-02: rule-editor state maps to RuleConfig (new/all, keep, padding, channel scope)',()=>{
 const draft=()=>({channel:{sourceId:hex(48),id:hex(64,'b'),generation:hex(48,'c')},programme:{id:hex(64,'d'),title:'Show',seriesId:'series'},series:false} as any);
 const {config,anchor}=seriesRuleConfig(draft(),{episodes:'new',keep:5,beforeSeconds:60,afterSeconds:300,anyChannel:false});
 assert.equal(config.episodes,'new');
 assert.equal(config.options.episodeLimit,5);
 assert.equal(config.options.beforeSeconds,60);
 assert.equal(config.options.afterSeconds,300);
 assert.deepEqual(config.allowedChannels,[hex(64,'b')]);
 assert.deepEqual(anchor,{sourceId:hex(48),channelId:hex(64,'b'),generation:hex(48,'c'),programmeId:hex(64,'d')});
 const any=seriesRuleConfig(draft(),{episodes:'all',keep:0,beforeSeconds:0,afterSeconds:0,anyChannel:true});
 assert.equal(any.config.episodes,'all');
 assert.deepEqual(any.config.allowedChannels,[]);
 assert.ok(validRecordingOptions(any.config.options));
 // Out-of-range editor state never reaches the server.
 assert.throws(()=>seriesRuleConfig(draft(),{episodes:'some' as any,keep:0,beforeSeconds:0,afterSeconds:0,anyChannel:true}));
 assert.throws(()=>seriesRuleConfig(draft(),{episodes:'new',keep:-1,beforeSeconds:0,afterSeconds:0,anyChannel:true}));
 assert.throws(()=>seriesRuleConfig(draft(),{episodes:'new',keep:0,beforeSeconds:21601,afterSeconds:0,anyChannel:true}));
 const bad=()=>({channel:{sourceId:'bad',id:hex(64,'b'),generation:hex(48,'c')},programme:{id:hex(64,'d'),title:'Show',seriesId:'series'},series:false} as any);
 assert.throws(()=>seriesRuleConfig(bad(),{episodes:'new',keep:0,beforeSeconds:0,afterSeconds:0,anyChannel:true}));
});

test('FEAT-08: a recording carries the server\'s seriesId and seriesTitle; an unreadable one is left out, never fatal',()=>{
 const named=parseRecording({...recording(),seriesId:'show-1',seriesTitle:'The Show'});
 assert.equal(named.seriesId,'show-1');assert.equal(named.seriesTitle,'The Show');
 const odd=parseRecording({...recording(),seriesId:42,seriesTitle:''});
 assert.equal(odd.seriesId,undefined);assert.equal(odd.seriesTitle,undefined);
});

test('recordings retain programme facts, image, position and duration without copying private fields',()=>{
 const raw:any=recording();Object.assign(raw.programme,{subtitle:'Episode name',episode:{season:2,number:5},categories:['News'],rating:{system:'VCHIP',value:'TV-G'},year:2026,starRating:'4/5',flags:{live:false,new:true,premiere:true,repeat:false},image:'/v1/dvr/recordings/'+raw.id+'/image',providerURL:'private'});raw.positionMs=15000;raw.durationMs=3600000;
 raw.programme.seriesId='s'.repeat(512);raw.programme.episodeId='e'.repeat(512);
 const r=parseRecording(raw);assert.equal(r.programme.seriesId,raw.programme.seriesId);assert.equal(r.programme.episodeId,raw.programme.episodeId);assert.equal(r.programme.subtitle,'Episode name');assert.deepEqual(r.programme.episode,{season:2,number:5});assert.equal(r.programme.flags?.live,false);assert.equal(r.programme.flags?.premiere,true);assert.equal(r.programme.image,raw.programme.image);assert.equal(r.positionMs,15000);assert.equal(r.durationMs,3600000);assert.ok(!JSON.stringify(r).includes('private'));
 raw.durationMs=-1;assert.throws(()=>parseRecording(raw));
});

test('provider-id-free rule drafts explicitly match exact title on the selected channel',async()=>{
 const {draftRule}=await import('../src/dvr.ts');const {programme:program}=recording();program.seriesId='';const d:any={programme:program,channel:{id:program.channelId,sourceId:hex(48,'b'),generation:hex(48,'d')},series:true};
 const c=draftRule(d);assert.equal(c.matchKind,'title');assert.equal(c.title,program.title);assert.equal(c.channelId,program.channelId);assert.equal(c.seriesId,'');
});

function deferred<T>(){let resolve!:(value:T)=>void,reject!:(error:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};}
const dvrTurn=()=>new Promise<void>(resolve=>setImmediate(resolve));
const updated=(options:RecordingOptions={...defaultRecordingOptions},revision=2)=>({...recording(),revision,options:{beforeMs:options.beforeSeconds*1000,afterMs:options.afterSeconds*1000,priority:options.priority,retentionDays:options.retentionDays,episodeLimit:options.episodeLimit}});
function updateHarness(timeoutMs=18000){
 const allocation=deferred<string>();let ids=0,current=true;
 const calls:{path:string;method?:string;body:unknown;signal:AbortSignal;response:ReturnType<typeof deferred<unknown>>}[]=[];
 const api:ChannelApi={request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{assert.ok(signal);const response=deferred<unknown>();calls.push({path,method,body,signal,response});return response.promise as Promise<T>;}};
 const client=new DVRClient(api,'server',()=>{ids++;return allocation.promise;},timeoutMs),controller=new AbortController();
 const lifetime:DVRCommandLifetime={signal:controller.signal,isCurrent:()=>current};
 return {client,controller,lifetime,calls,allocation,ids:()=>ids,retire:()=>{current=false;}};
}

test('scoped update freezes original id, revision, options and receipt before delayed allocation',async t=>{
 const h=updateHarness();t.after(()=>h.client.dispose());const r=parseRecording(recording()),options={...r.options,beforeSeconds:60,afterSeconds:120,priority:7};
 const original={...options},work=h.client.update(r,options,h.lifetime);
 r.id=hex(64,'f');r.revision=90;options.beforeSeconds=300;options.priority=-7;
 h.allocation.resolve(hex(48,'e'));await dvrTurn();assert.equal(h.calls.length,1);
 assert.equal(h.calls[0]!.path,`/v1/dvr/recordings/${hex()}`);assert.equal(h.calls[0]!.method,'PATCH');
 assert.deepEqual(h.calls[0]!.body,{expectedRevision:1,options:{beforeMs:60000,afterMs:120000,priority:7,retentionDays:0,episodeLimit:0},requestId:hex(48,'e')});
 assert.ok(Object.isFrozen(h.calls[0]!.body));assert.ok(Object.isFrozen((h.calls[0]!.body as {options:unknown}).options));
 h.calls[0]!.response.resolve(envelope(updated(original)));const result=await work;
 assert.equal(result.id,hex());assert.equal(result.revision,2);assert.deepEqual(result.options,original);assert.equal(h.ids(),1);
});

test('scoped update refuses preaborted, false, nontrue and throwing predicates before allocation or PATCH',async t=>{
 const h=updateHarness();t.after(()=>h.client.dispose());const r=parseRecording(recording()),dead=new AbortController();dead.abort();let checked=0;
 for(const lifetime of [
  {signal:dead.signal,isCurrent:()=>{checked++;return true;}},
  {signal:new AbortController().signal,isCurrent:()=>false},
  {signal:new AbortController().signal,isCurrent:(()=>1) as unknown as ()=>boolean},
  {signal:new AbortController().signal,isCurrent:()=>{throw new Error('retired owner');}},
 ])await assert.rejects(h.client.update(r,r.options,lifetime),{name:'AbortError'});
 assert.equal(checked,0);assert.equal(h.ids(),0);assert.equal(h.calls.length,0);
});

for(const retirement of ['abort','currentness','dispose','activate'] as const)test(`delayed request ID after original ${retirement} produces zero PATCH`,async t=>{
 const h=updateHarness();t.after(()=>h.client.dispose());const r=parseRecording(recording());const work=h.client.update(r,r.options,h.lifetime),refused=assert.rejects(work,{name:'AbortError'});
 assert.equal(h.ids(),1);assert.equal(h.calls.length,0);
 if(retirement==='abort')h.controller.abort();else if(retirement==='currentness')h.retire();else if(retirement==='dispose')h.client.dispose();else h.client.activate();
 h.allocation.resolve(hex(48,'e'));await refused;assert.equal(h.calls.length,0);
});

test('same-key sibling keeps the original pending receipt when only its predecessor waiter retires',async t=>{
 const h=updateHarness();t.after(()=>h.client.dispose());const r=parseRecording(recording()),other=new AbortController();
 const first=h.client.update(r,r.options,h.lifetime),refused=assert.rejects(first,{name:'AbortError'});
 const second=h.client.update(r,r.options,{signal:other.signal,isCurrent:()=>true});assert.equal(h.ids(),1);
 h.controller.abort();h.allocation.resolve(hex(48,'e'));await refused;await dvrTurn();
 assert.equal(h.calls.length,1);assert.equal(h.calls[0]!.signal.aborted,false);assert.equal((h.calls[0]!.body as {requestId:string}).requestId,hex(48,'e'));
 h.calls[0]!.response.resolve(envelope(updated()));await second;
 const retry=h.client.update(r,r.options,{signal:other.signal,isCurrent:()=>true});await dvrTurn();assert.equal(h.ids(),1);assert.deepEqual(h.calls[1]!.body,h.calls[0]!.body);
 h.calls[1]!.response.resolve(envelope(updated()));await retry;
});

test('ID-provider activation reentry cannot overwrite or clear the successor receipt',async t=>{
 const oldID=deferred<string>(),newID=deferred<string>(),r=parseRecording(recording());let ids=0,successor:Promise<unknown>|undefined,client!:DVRClient;
 const sent:unknown[]=[];const api:ChannelApi={request:async<T>(_path:string,_method?:string,body?:unknown)=>{sent.push(body);return envelope(updated()) as T;}};
 const other={signal:new AbortController().signal,isCurrent:()=>true};
 client=new DVRClient(api,'server',()=>{if(++ids===1){client.activate();successor=client.update(r,r.options,other);return oldID.promise;}return newID.promise;});t.after(()=>client.dispose());
 await assert.rejects(client.update(r,r.options,{signal:new AbortController().signal,isCurrent:()=>true}),{name:'AbortError'});
 assert.ok(successor);assert.equal(ids,2);assert.equal(sent.length,0);
 newID.resolve(hex(48,'f'));await successor;oldID.reject(new Error('late old ID'));await dvrTurn();
 await client.update(r,r.options,other);assert.equal(ids,2);assert.equal(sent.length,2);assert.deepEqual(sent[0],sent[1]);assert.equal((sent[0] as {requestId:string}).requestId,hex(48,'f'));
});

for(const action of ['abort','dispose','activate'] as const)test(`TRUE final pre-PATCH predicate ${action} cannot dispatch or affect a subsequent opening`,async t=>{
 const controller=new AbortController();let atDispatch=false,acted=false,calls=0,client!:DVRClient;
 const api:ChannelApi={get request(){atDispatch=true;return async<T>()=>{calls++;return envelope(updated()) as T;};}};
 client=new DVRClient(api,'server',nonce);t.after(()=>client.dispose());const r=parseRecording(recording());
 const lifetime={signal:controller.signal,isCurrent:()=>{if(atDispatch&&!acted){acted=true;if(action==='abort')controller.abort();else if(action==='dispose')client.dispose();else client.activate();}return true;}};
 await assert.rejects(client.update(r,r.options,lifetime),{name:'AbortError'});assert.equal(acted,true);assert.equal(calls,0);
 if(action==='dispose')client.activate();
 await client.update(r,r.options,{signal:new AbortController().signal,isCurrent:()=>true});assert.equal(calls,1);
});

test('TRUE predicate retirement immediately after ID allocation is rechecked before any PATCH',async t=>{
 const controller=new AbortController();let allocated=false,calls=0;
 const api:ChannelApi={request:async<T>()=>{calls++;return envelope(updated()) as T;}};
 const client=new DVRClient(api,'server',()=>{allocated=true;return nonce();});t.after(()=>client.dispose());const r=parseRecording(recording());
 await assert.rejects(client.update(r,r.options,{signal:controller.signal,isCurrent:()=>{if(allocated)controller.abort();return true;}}),{name:'AbortError'});assert.equal(calls,0);
});

test('caller retirement before a late 403 cannot notify access change or abort a sibling transport',async t=>{
 const h=updateHarness();t.after(()=>h.client.dispose());const r=parseRecording(recording());let accessChanges=0;h.client.onAccessChanged(()=>{accessChanges++;});
 const first=h.client.update(r,r.options,h.lifetime),refused=assert.rejects(first,{name:'AbortError'});h.allocation.resolve(hex(48,'e'));await dvrTurn();
 const other=new AbortController(),second=h.client.update(r,r.options,{signal:other.signal,isCurrent:()=>true});await dvrTurn();assert.equal(h.calls.length,2);
 h.controller.abort();h.calls[0]!.response.reject({status:403});await refused;
 assert.equal(accessChanges,0);assert.equal(h.calls[1]!.signal.aborted,false);h.calls[1]!.response.resolve(envelope(updated()));await second;
});

for(const stage of ['response','parse','return'] as const)test(`post-${stage} TRUE predicate retirement cannot return an original update as accepted`,async t=>{
 const h=updateHarness();t.after(()=>h.client.dispose());const r=parseRecording(recording());let armed=false,seen=0;
 const lifetime={signal:h.controller.signal,isCurrent:()=>{if(armed&&++seen===(stage==='response'?1:stage==='parse'?5:6))h.controller.abort();return true;}};
 const work=h.client.update(r,r.options,lifetime),refused=assert.rejects(work,{name:'AbortError'});h.allocation.resolve(hex(48,'e'));await dvrTurn();armed=true;
 h.calls[0]!.response.resolve(envelope(updated()));await refused;assert.equal(h.calls.length,1);assert.equal(h.controller.signal.aborted,true);
});

test('scoped update retains the canonical finite timeout and exact receipt for an explicit retry',async t=>{
 t.mock.timers.enable({apis:['setTimeout']});const h=updateHarness(40);t.after(()=>h.client.dispose());const r=parseRecording(recording());
 const work=h.client.update(r,r.options,h.lifetime),timedOut=assert.rejects(work,/timed out/);h.allocation.resolve(hex(48,'e'));await dvrTurn();
 t.mock.timers.tick(40);await timedOut;assert.equal(h.calls[0]!.signal.aborted,true);assert.equal(h.calls.length,1);
 const retry=h.client.update(r,r.options,{signal:new AbortController().signal,isCurrent:()=>true});await dvrTurn();
 assert.equal(h.ids(),1);assert.deepEqual(h.calls[1]!.body,h.calls[0]!.body);h.calls[1]!.response.resolve(envelope(updated()));await retry;
});

test('a scoped update rejects a foreign or latest receipt while legacy omitted arguments keep their old return semantics',async t=>{
 let raw=recording();const api:ChannelApi={request:async<T>()=>envelope(raw) as T},client=new DVRClient(api,'server',nonce);t.after(()=>client.dispose());const r=parseRecording(recording());
 assert.equal((await client.update(r,r.options)).revision,1,'legacy one-argument lifetime omission retains its original result parsing');
 for(const value of [{...updated(),id:hex(64,'f')},{...updated(),revision:3},{...updated(),options:{...wireOptions,beforeMs:60000}}]){
  raw=value;await assert.rejects(client.update(r,r.options,{signal:new AbortController().signal,isCurrent:()=>true}));
 }
});
