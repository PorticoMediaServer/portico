import {unreadableServerResponse} from '../src/server-messages.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {SubtitleService, validateSubtitlePlan, validateSubtitleCatalog, validateSubtitleDocument, validateSubtitleDelivery, readSubtitleDocument} from '../src/subtitles.ts';
import type {SubtitlePlan,SubtitleResource} from '../src/subtitles.ts';
import type {LibraryContentApi} from '../src/library-content.ts';

const resource:SubtitleResource={id:'sub',sourceId:'source',revision:1,scope:'personal',language:'en',title:'English',format:'vtt',origin:'upload',rights:'Own transcript',offsetUs:'0',canManage:true,enabled:true};
test('original delivery contracts retain grant paths, companion files and skipped cue facts',()=>{
 const base='/v1/media/subtitles/grant/sub/1';
 assert.equal(validateSubtitleDelivery({format:'ass',url:base+'/original.ssa',fontsUrl:base+'/fonts'}).fontsUrl,base+'/fonts');
 assert.equal(validateSubtitleDelivery({format:'vobsub',url:base+'/original.idx',companionUrl:base+'/original.sub'}).companionUrl,base+'/original.sub');
 for(const url of ['https://origin.invalid/sub.ass',base+'/../../private',base+'/original.exe'])assert.throws(()=>validateSubtitleDelivery({format:'ass',url}));
 const envelope={version:1,timeDomain:'source-relative',cues:[{startUs:'0',endUs:'1',text:'{LAUGHS}'}],skippedCues:2};
 const doc=validateSubtitleDocument(envelope);assert.equal(doc.skippedCues,2);assert.equal(doc.cues[0].text,'{LAUGHS}');
 assert.throws(()=>validateSubtitleDocument({...envelope,skippedCues:-1}));
});
const target={itemId:'item',sessionId:'session',generation:1};
function plan(revision=1,selected:SubtitleResource|null=null):SubtitlePlan{return {version:1,sessionId:'session',generation:1,sourceId:'source',revision,catalogRevision:1,renderer:'external_text',mode:selected?'track':'off',offAvailable:true,offsetUs:'0',selected:selected?{...selected,pinned:true}:null,...(selected?{documentUrl:`/v1/media/subtitles/grant/${selected.id}/${selected.revision}`} :{}),resources:[resource],discovered:[]};}
function catalog(){return {version:1,itemId:'item',revision:1,canShare:false,sources:[{id:'source',durationUs:'100000000',inventoryRevision:1,available:true,timingKnown:true}],resources:[resource],discovered:[],provider:{id:'opensubtitles',enabled:false,reason:'provider_not_configured'}};}
function deferred<T>(){let resolve!:(v:T)=>void,reject!:(e:unknown)=>void;const promise=new Promise<T>((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};}
const flush=()=>new Promise<void>(resolve=>setImmediate(resolve));
async function ready(service:SubtitleService){service.start();await flush();assert.equal(service.getSnapshot().loading,false);}
function api(fn:(path:string,method:string,body:unknown,signal?:AbortSignal)=>Promise<unknown>):LibraryContentApi{return {request:<T>(p:string,m='GET',b?:unknown,s?:AbortSignal)=>fn(p,m,b,s) as Promise<T>};}

 test('authoritative track pin and Off plan validate with exact child URL and source binding',()=>{
 const track=validateSubtitlePlan(plan(2,resource));assert.equal(track.selected?.pinned,true);assert(Object.isFrozen(track.resources));
 assert.equal(validateSubtitlePlan(plan()).mode,'off');assert.equal(validateSubtitleCatalog(catalog()).resources[0].id,'sub');
 for(const change of [(p:any)=>p.generation=0,(p:any)=>p.selected.sourceId='other',(p:any)=>p.selected.pinned=false,(p:any)=>p.documentUrl='/v1/media/subtitles/grant/sub/2',(p:any)=>p.documentUrl='https://foreign.invalid/x',(p:any)=>p.offsetUs='-0',(p:any)=>p.offsetUs='600000001',(p:any)=>p.revision=true,(p:any)=>p.resources.push(resource)]){const p=structuredClone(plan(2,resource));change(p);assert.throws(()=>validateSubtitlePlan(p));}
 const off:any=plan();off.documentUrl='/v1/media/subtitles/grant/sub/1';assert.throws(()=>validateSubtitlePlan(off));
 });
 test('cue documents retain decoded literal text, overlaps and exact microsecond integers',()=>{
 const d=validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[{startUs:'1000000',endUs:'3000000',text:'2 < 3 & <b>literal</b>'},{startUs:'2000000',endUs:'2500000',text:'Overlapping\ncaption'}]});assert.equal(d.cues[0].text,'2 < 3 & <b>literal</b>');
 for(const cue of [{startUs:'01',endUs:'2',text:'x'},{startUs:'0',endUs:'0',text:'x'},{startUs:1,endUs:'2',text:'x'},{startUs:'0',endUs:'86400000001',text:'x'},{startUs:'0',endUs:'1',text:'\x00'}])assert.throws(()=>validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[cue]}));
 // Forgiving about cues: out of order is put in order, one bad cue costs only itself, styling that slipped through is removed.
 const mixed=validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[{startUs:'2',endUs:'4',text:'A'},{startUs:'1',endUs:'3',text:'{\\an8}B',top:true,italic:true},{startUs:'x',endUs:'3',text:'dropped'},{startUs:'5',endUs:'6',text:'\x00'},null,{startUs:'7',endUs:'9',text:'x'.repeat(9000)}]});
 assert.deepEqual(mixed.cues.map(c=>c.text.slice(0,4)),['B','A','xxxx']);assert.equal(mixed.cues[0].top,true);assert.equal(mixed.cues[0].italic,true);assert.equal(mixed.cues[1].top,undefined);assert.equal(mixed.cues[2].text.length,8192);
 // Strict about the envelope: a broken response is never shown as an empty track.
 for(const bad of [null,{version:2,timeDomain:'source-relative',cues:[]},{version:1,timeDomain:'wall-clock',cues:[]},{version:1,timeDomain:'source-relative',cues:'none'},{version:1,timeDomain:'source-relative',cues:new Array(20001).fill({startUs:'1',endUs:'2',text:'x'})}])assert.throws(()=>validateSubtitleDocument(bad));
 assert.equal(validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[]}).cues.length,0);
 });
 test('protected document reader validates MIME, declared size, UTF-8, status and cancellation',async()=>{
 const json=JSON.stringify({version:1,timeDomain:'source-relative',cues:[{startUs:'0',endUs:'1000000',text:'Hello'}]});const c=new AbortController();
 assert.equal((await readSubtitleDocument(new Response(json,{headers:{'Content-Type':'application/json'}}),c.signal)).cues.length,1);
 for(const response of [new Response(json),new Response(json,{status:403}),new Response(json,{headers:{'Content-Type':'application/json','Content-Length':'8388609'}}),new Response(new Uint8Array([0xff]),{headers:{'Content-Type':'application/json'}})])await assert.rejects(readSubtitleDocument(response,c.signal));
 c.abort();await assert.rejects(readSubtitleDocument(new Response(json,{headers:{'Content-Type':'application/json'}}),c.signal));
 });
 test('scope retirement rejects late responses even when the transport ignores abort',async()=>{
 const d=deferred<unknown>();const s=new SubtitleService(api(async()=>d.promise),target,()=> 'op');s.start();s.stop();d.resolve(plan());await flush();assert.equal(s.getSnapshot().plan,null);assert.equal(s.getSnapshot().catalog,null);
 });
 test('replacement and deletion do not overwrite the current session pin; revocation clears it',async()=>{
 let current=plan(2,resource),denied=false;const writes:any[]=[];
 const s=new SubtitleService(api(async(p,m,b)=>{if(denied)throw Object.assign(new Error('private transport detail'),{status:403});if(m==='GET')return p.includes('/playback/')?current:catalog();writes.push(b);current={...current,selected:{...resource,pinned:true,retired:true}};return {operationId:(b as any).operationId,resourceId:'sub',revision:2,catalogRevision:2,deleted:m==='DELETE'};}),target,(()=>{let n=0;return()=>`op${++n}`;})());
 try{await ready(s);await s.save({format:'vtt',language:'fr',title:'French',rights:'own',scope:'personal',offsetUs:'0'},resource);await flush();assert.equal(s.getSnapshot().plan?.selected?.revision,1);assert.equal(s.getSnapshot().plan?.selected?.retired,true);await s.remove({...resource,revision:2});await flush();assert.equal(s.getSnapshot().plan?.mode,'track');assert.equal(writes[0].expectedRevision,1);assert.equal(writes[1].expectedRevision,2);denied=true;await s.refresh();assert.equal(s.getSnapshot().catalog,null);assert.equal(s.getSnapshot().plan,null);assert(!JSON.stringify(s.getSnapshot()).includes('private transport'));}finally{s.stop();}
 });
 test('CAS conflict requires review rather than offering an automatic mutation retry',async()=>{
 const s=new SubtitleService(api(async(p,m)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();throw Object.assign(new Error('conflict'),{status:409});}),target,()=> 'op');try{await ready(s);await s.save({format:'vtt',language:'fr',title:'French',rights:'own',scope:'personal',offsetUs:'0'},resource);assert.equal(s.getSnapshot().canRetry,false);assert.match(s.getSnapshot().error!,/Refresh and review/);}finally{s.stop();}
 });

 test('invalid upload requires correction rather than retrying the same bad payload',async()=>{
 const s=new SubtitleService(api(async(p,m)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();throw Object.assign(new Error('unsupported'),{status:422});}),target,()=> 'op');try{await ready(s);await s.save({content:'invalid',format:'srt',language:'en',title:'English',rights:'own',scope:'personal',offsetUs:'0'});assert.equal(s.getSnapshot().canRetry,false);assert.match(s.getSnapshot().error!,/plain SRT/);}finally{s.stop();}
 });

test('media editor reads catalog without a playback session',async()=>{
 const paths:string[]=[];
 const service=new SubtitleService(api(async(p)=>{paths.push(p);return catalog();}),{itemId:'item',sourceId:'source'},()=> 'op');
 try{await ready(service);assert.equal(service.sourceId,'source');assert.equal(service.getSnapshot().plan,null);assert.equal(service.getSnapshot().catalog?.itemId,'item');assert.deepEqual(paths,['/v1/items/item/subtitles']);}finally{service.stop();}
});
test('media editor saves against its selected source and retries ambiguous writes unchanged',async()=>{
 const writes:any[]=[];let failed=false;
 const service=new SubtitleService(api(async(p,m,b)=>{assert(!p.includes('/playback/'));if(m==='GET')return catalog();writes.push(structuredClone(b));if(!failed){failed=true;throw new Error('lost response');}return {operationId:'op',resourceId:'sub',revision:2,catalogRevision:2};}),{itemId:'item',sourceId:'source'},()=> 'op');
 try{await ready(service);await service.save({content:'00:00:00,000 --> 00:00:01,000\nHello',format:'srt',language:'en',title:'English',rights:'own',scope:'personal',offsetUs:'0'});assert.equal(service.getSnapshot().canRetry,true);await service.retry();assert.equal(writes[0].sourceId,'source');assert.deepEqual(writes[0],writes[1]);assert.equal(service.getSnapshot().plan,null);}finally{service.stop();}
});
test('media editor rejects a catalog for a different source and clears data after revocation',async()=>{
 let denied=false;
 const service=new SubtitleService(api(async()=>{if(denied)throw Object.assign(new Error('private detail'),{status:403});return catalog();}),{itemId:'item',sourceId:'source'},()=> 'op');
 const wrong=new SubtitleService(api(async()=>catalog()),{itemId:'item',sourceId:'other'},()=> 'op');
 try{await ready(wrong);assert.equal(wrong.getSnapshot().catalog,null);assert.equal(wrong.getSnapshot().error,unreadableServerResponse);await ready(service);denied=true;await service.refresh();assert.equal(service.getSnapshot().catalog,null);assert.equal(service.getSnapshot().canRetry,false);assert(!JSON.stringify(service.getSnapshot()).includes('private detail'));}finally{service.stop();wrong.stop();}
});

test('the periodic subtitle reload sends its validator and treats 304 as authorised and unchanged',async()=>{
 const {fetchSubtitleDocument}=await import('../src/subtitles.ts');
 const body=JSON.stringify({version:1,timeDomain:'source-relative',cues:[{startUs:'0',endUs:'1000',text:'Hi'}]});
 const seen:RequestInit[]=[];
 const fetcher=(async(_url:string,init:RequestInit)=>{seen.push(init);const match=(init.headers as Record<string,string>|undefined)?.['If-None-Match'];return match==='"res.3"'?new Response(null,{status:304}):new Response(body,{status:200,headers:{'Content-Type':'application/json','ETag':'"res.3"'}});}) as unknown as typeof fetch;
 const first=await fetchSubtitleDocument(fetcher,'https://server.test/doc','',new AbortController().signal);
 assert.equal(first.unchanged,false);assert.equal((first as any).etag,'"res.3"');assert.equal(seen[0].headers,undefined);assert.equal(seen[0].cache,'no-store');
 const second=await fetchSubtitleDocument(fetcher,'https://server.test/doc','"res.3"',new AbortController().signal);
 assert.equal(second.unchanged,true);assert.equal(seen[1].cache,'no-store');
 // A validator that is not one is not sent; a revoked viewer's failure is a failure.
 await fetchSubtitleDocument(fetcher,'https://server.test/doc','not a validator\r\nX: y',new AbortController().signal);assert.equal(seen[2].headers,undefined);
 const revoked=(async()=>new Response('{}',{status:401,headers:{'Content-Type':'application/json'}})) as unknown as typeof fetch;
 await assert.rejects(fetchSubtitleDocument(revoked,'https://server.test/doc','"res.3"',new AbortController().signal));
});

test('CD-21: external_text accepts ass/ssa alongside srt/vtt; bitmap stays refused (the selected track refuses the plan; a listed one is left out)',()=>{
 for(const format of ['ass','ssa'] as const){
  const pinned={...resource,format};
  assert.equal(validateSubtitlePlan(plan(2,{...pinned,pinned:true})).selected?.format,format);
  assert.equal(validateSubtitleCatalog({...catalog(),resources:[pinned]}).resources[0].format,format);
 }
 for(const format of ['pgs','sup','vobsub','idx','dvb'] as const){
  assert.throws(()=>validateSubtitlePlan(plan(2,{...resource,format,pinned:true} as any)));
  assert.deepEqual(validateSubtitleCatalog({...catalog(),resources:[{...resource,format},{...resource,id:'kept'}]}).resources.map(r=>r.id),['kept']);
 }
});

// B-16.10: a track in a format or from an origin newer than this app costs only that track, never the whole list.
test('a subtitle row this app cannot read is left out and the others are still offered',()=>{
 const listed=validateSubtitleCatalog({...catalog(),resources:[{...resource,id:'future',format:'webvtt2'},resource]});
 assert.deepEqual(listed.resources.map(r=>r.id),[resource.id]);
 const planned=validateSubtitlePlan({...plan(2,{...resource,pinned:true}),resources:[{...resource,id:'future',origin:'satellite'},resource]});
 assert.deepEqual(planned.resources.map(r=>r.id),[resource.id]);
 assert.throws(()=>validateSubtitlePlan(plan(2,{...resource,format:'webvtt2',pinned:true} as any)),'the selected track itself stays exact');
});

const upload={content:'00:00:00,000 --> 00:00:01,000\nHello',format:'srt' as const,language:'en',title:'English',rights:'Own transcript',scope:'personal' as const,offsetUs:'0'};
const uploadReceipt={operationId:'op',resourceId:'sub',revision:2,catalogRevision:2};

test('a subtitle save refused by its original host lifetime never reaches transport',async()=>{
 let writes=0;const service=new SubtitleService(api(async(p,m)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();writes++;return uploadReceipt;}),target,()=> 'op');
 try{await ready(service);const abort=new AbortController();abort.abort();
  for(const context of [{signal:abort.signal},{isCurrent:()=>false},{isCurrent:()=>{throw new Error('retired fence');}}])await assert.rejects(service.save(upload,undefined,context),{name:'AbortError'});
  assert.equal(writes,0);assert.equal(service.getSnapshot().busy,false);assert.equal(service.getSnapshot().error,null);assert.equal(service.getSnapshot().canRetry,false);
 }finally{service.stop();}
});

test('queued subtitle upload checks both caller cancellation and currentness before issuing any POST',async()=>{
 for(const mode of ['signal','current'] as const){
  const response=deferred<unknown>(),entered=deferred<void>();let writes=0,current=true;
  const service=new SubtitleService(api(async(p,m)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();writes++;if(writes===1){entered.resolve();return response.promise;}return uploadReceipt;}),target,()=> 'op');
  try{await ready(service);const first=service.save(upload);await entered.promise;const abort=new AbortController();
   const queued=service.save({...upload,language:'fr'},undefined,mode==='signal'?{signal:abort.signal}:{isCurrent:()=>current});
   const canceled=assert.rejects(queued,{name:'AbortError'});abort.abort();current=false;response.resolve(uploadReceipt);await first;await canceled;
   assert.equal(writes,1);assert.equal(service.getSnapshot().error,null);assert.equal(service.getSnapshot().canRetry,false);
   await service.save({...upload,language:'de'},undefined,{isCurrent:()=>true});assert.equal(writes,2,'a canceled upload leaves the serial queue usable');assert.equal(service.getSnapshot().busy,false);
  }finally{service.stop();}
 }
});

test('active subtitle upload forwards caller abort and discards even an abort-ignoring late response',async()=>{
 const response=deferred<unknown>(),entered=deferred<void>();let transportSignal:AbortSignal|undefined;
 const service=new SubtitleService(api(async(p,m,_body,signal)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();transportSignal=signal;entered.resolve();return response.promise;}),target,()=> 'op');
 try{await ready(service);const abort=new AbortController(),save=service.save(upload,undefined,{signal:abort.signal});const canceled=assert.rejects(save,{name:'AbortError'});await entered.promise;
  assert.equal(transportSignal!.aborted,false);abort.abort();assert.equal(transportSignal!.aborted,true);
  response.resolve({not:'a valid receipt'});await canceled;assert.equal(service.getSnapshot().error,null);assert.equal(service.getSnapshot().canRetry,false);assert.equal(service.getSnapshot().busy,false);
 }finally{service.stop();}
});

test('a subtitle upload rejects a retired full-host predicate after HTTP without accepting its receipt',async()=>{
 const response=deferred<unknown>(),entered=deferred<void>();let current=true;
 const service=new SubtitleService(api(async(p,m)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();entered.resolve();return response.promise;}),target,()=> 'op');
 try{await ready(service);const save=service.save(upload,undefined,{isCurrent:()=>current});const canceled=assert.rejects(save,{name:'AbortError'});await entered.promise;
  current=false;response.resolve(uploadReceipt);await canceled;assert.equal(service.getSnapshot().error,null);assert.equal(service.getSnapshot().canRetry,false);
 }finally{service.stop();}
});

test('an ambiguous subtitle write keeps its original retry lifetime and cannot retry after host retirement',async()=>{
 let writes=0,current=true;
 const service=new SubtitleService(api(async(p,m)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();writes++;throw new Error('lost response');}),target,()=> 'op');
 try{await ready(service);await service.save(upload,undefined,{isCurrent:()=>current});assert.equal(service.getSnapshot().canRetry,true);
  current=false;await assert.rejects(service.retry(),{name:'AbortError'});assert.equal(writes,1);assert.equal(service.getSnapshot().canRetry,false);assert.equal(service.getSnapshot().error,null);
 }finally{service.stop();}
});

test('stopping the subtitle service still cancels an active caller-scoped save when transport ignores abort',async()=>{
 const response=deferred<unknown>(),entered=deferred<void>();let transportSignal:AbortSignal|undefined;
 const service=new SubtitleService(api(async(p,m,_body,signal)=>{if(m==='GET')return p.includes('/playback/')?plan():catalog();transportSignal=signal;entered.resolve();return response.promise;}),target,()=> 'op');
 try{await ready(service);const save=service.save(upload,undefined,{isCurrent:()=>true});const canceled=assert.rejects(save,{name:'AbortError'});await entered.promise;
  service.stop();assert.equal(transportSignal!.aborted,true);response.resolve(uploadReceipt);await canceled;assert.equal(service.getSnapshot().catalog,null);assert.equal(service.getSnapshot().plan,null);
 }finally{service.stop();}
});
