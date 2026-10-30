import test,{type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {ListeningService,loadListeningSelection,listeningItemInput,parseListeningJourney,parseListeningPreferences,readBookTimeline,readListeningItem,readListeningContext,type ListeningRequest} from '../src/listening.ts';
import type {PlaybackService} from '../src/index.ts';
import {defaultAudiobookPreferences,type AudiobookPreferences} from '../src/audiobook-preferences.ts';
import {defaultAudioEffects,type AudioEffectsSnapshot} from '../src/audio-effects.ts';
const scope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'};
const prefs={revision:0,bookRate:1.5,passoutMinutes:0};
const wrapped=(data:unknown,other={})=>({scope:{...scope,...other},data});
const settle=()=>new Promise<void>(resolve=>setImmediate(resolve));
const target={libraryId:'library',kind:'album' as const,id:'album'};
class Engine {
 state:any={itemId:'song',intent:'playing',phase:'ready',positionSeconds:5,session:{id:'session',generation:1},intentId:1,playbackRun:1,audioEffects:{settings:defaultAudioEffects,rendering:false,rate:1,eqApplied:false,levelingApplied:false} satisfies AudioEffectsSnapshot};
 policies:any[]=[];native=false;
 /** The native owner's answer to one policy. A test can hold it open or refuse it. */
 answer:(policy:any)=>boolean|Promise<boolean>=()=>true;
 setListeningPolicy=(policy:any)=>{this.policies.push(policy);return Promise.resolve(this.answer(policy));};
 isNativeListening=()=>this.native;
 listeners=new Set<()=>void>();rates:number[]=[];pauses=0;
 subscribe=(f:()=>void)=>{this.listeners.add(f);return()=>{this.listeners.delete(f);};};
 getSnapshot=()=>this.state;
 setPlaybackRate=(rate:number)=>{this.rates.push(rate);};
 pause=()=>{this.pauses++;this.update({intent:'paused'});};
 update(patch:unknown){this.state={...this.state,...patch as any};for(const f of this.listeners)f();}
}
/** The served book timeline object the client parses: ordered parts, one page here. */
function bookTimeline(cursor:string,over:{bookId?:string;libraryId?:string;parts?:readonly string[];totalCount?:number;revision?:{catalog:number;viewer:number};nextCursor?:string}={}){
 const ids=over.parts??['song','last'],parts=ids.map((itemId,index)=>({itemId,title:`Part ${index+1}`,offsetMs:index*60000,durationMs:60000}));
 return {bookId:over.bookId??'book',libraryId:over.libraryId??'library',revision:over.revision??{catalog:1,viewer:1},parts,totalCount:over.totalCount??ids.length,nextCursor:over.nextCursor??''};
}
/** An audiobook context whose final part is the timeline's own last part. */
const audiobookContext=(id:string)=>({itemId:id,libraryId:'library',kind:'audiobook_file',bookId:'book',lastBookItemId:'last',bookEndAvailable:true});
function chapterData(){return {scope:{serverId:'server',libraryId:'library',itemId:'song',sessionId:'session',generation:1,sourceId:'source',viewerFence:'fence'},revision:'revision',status:'available',reason:null,durationMs:60000,chapters:[{id:'source:1',index:1,title:'One',startMs:0,endMs:30000,action:{id:'seek',positionMs:0}},{id:'source:2',index:2,title:'Two',startMs:30000,endMs:60000,action:{id:'seek',positionMs:30000}}],totalCount:2,nextCursor:''};}
function setup(t:TestContext,options:{preferences?:Partial<typeof prefs>;context?:(id:string)=>unknown;request?:ListeningRequest;audiobook?:()=>AudiobookPreferences;queue?:(sessionId:string,generation:number)=>{queueId:string;entryId:string}|null;timeline?:(cursor:string)=>unknown}={}) {
 const engine=new Engine();let now=0,p={...prefs,...options.preferences};const writes:unknown[]=[];
 const request:ListeningRequest=options.request??(async(path,body,method)=>{
  if(path==='/v1/listening/preferences'){if(method==='PUT'){writes.push(body);p={...body as typeof p,revision:p.revision+1};}return wrapped(p);}
  if(path.includes('/timeline')){const cursor=new URL(path,'https://server.test').searchParams.get('cursor')??'';return wrapped(options.timeline?.(cursor)??bookTimeline(cursor));}
  if(path.includes('/chapters'))return chapterData();
  const id=path.split('/')[3];return wrapped(options.context?.(id)??{itemId:id,libraryId:'library',kind:'song',albumId:'album',artistId:'artist'});
 });
 const queue=options.queue??(()=>({queueId:'queue',entryId:'entry-1'}));
 const service=new ListeningService(scope,request,engine as unknown as PlaybackService,()=>now,undefined,options.audiobook,queue);
 t.after(()=>service.dispose());
 service.connect();return {service,engine,writes,setNow:(v:number)=>{now=v;}};
}
test('preferences strictly validate rates, revision and bounded inactivity choices; scope is never caller-switchable',async()=>{
 assert.deepEqual(parseListeningPreferences(prefs),prefs);
 for(const patch of [{revision:-1},{bookRate:NaN},{musicRate:1},{musicRate:'go'},{autoplayNext:true},{passoutMinutes:1}])assert.throws(()=>parseListeningPreferences({...prefs,...patch}));
 assert.throws(()=>parseListeningJourney({target:{...target,libraryId:'other'},actions:['play'],resumeUnavailable:false},'library'));
 await assert.rejects(readListeningItem(async()=>wrapped({itemId:'song',libraryId:'library',kind:'song'},{profileId:'other'}),scope,'song'));
 await assert.rejects(readListeningItem(async()=>wrapped({itemId:'song',libraryId:'library',kind:'song',bookId:''}),scope,'song'));
});
test('songs stay at recorded speed; audiobook session speed survives parts without changing defaults',async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:id==='song'||id==='song2'?'song':'audiobook_file'})});await settle();
 f.engine.update({playbackRun:1});
 await assert.rejects(f.service.setRate(1.25));assert.equal(f.engine.rates.at(-1),1);assert.equal(f.writes.length,0,'songs have no speed setting');
 assert.deepEqual(f.service.getSnapshot().preferences,prefs);
 assert.deepEqual(f.engine.policies.at(-1).preferences,{bookRate:1.5,passoutMinutes:0});
 f.engine.update({itemId:'part',session:{id:'book-session',generation:1}});await settle();assert.equal(f.engine.rates.at(-1),1.5,'another kind starts at its own default');
 f.engine.update({session:{id:'book-session',generation:2}});assert.equal(f.engine.rates.at(-1),1.5);
 await f.service.setRate(2);assert.equal(f.engine.rates.at(-1),2);
 f.engine.update({itemId:'part2',session:{id:'book-session-2',generation:1}});await settle();assert.equal(f.engine.rates.at(-1),2,'the next part of the same playback keeps the chosen speed');
 assert.equal(f.engine.policies.at(-1).preferences.bookRate,2,'a native owner is told the speed in force');
 f.engine.update({playbackRun:2,itemId:'part3',session:{id:'book-session-3',generation:1}});await settle();assert.equal(f.engine.rates.at(-1),1.5,'a new playback starts at the Settings speed');
 await assert.rejects(f.service.setRate(9),'an impossible speed is refused');
 assert.equal(f.writes.length,0);
});
test('minute timer pauses the real foreground player once; manual interaction resets stop latch',async t=>{
 const f=setup(t);await settle();await f.service.setTimer(15);
 f.setNow(899999);f.service.tick();assert.equal(f.engine.pauses,0);
 f.setNow(900000);f.service.tick();f.service.tick();assert.equal(f.engine.pauses,1);assert.equal(f.service.mayAdvance(),false);assert.equal(f.service.getSnapshot().timer,null);
 f.service.interact();assert.equal(f.service.mayAdvance(),true);
});
test('inactivity preference is enforced and deliberate foreground interactions reset its deadline',async t=>{
 const f=setup(t,{preferences:{passoutMinutes:30}});await settle();
 f.setNow(29*60000);f.service.interact();f.setNow(58*60000);f.service.tick();assert.equal(f.engine.pauses,0);
 f.setNow(59*60000);f.service.tick();assert.equal(f.engine.pauses,1);assert.match(f.service.getSnapshot().message!,/without interaction/);
});
test('album continuation has no listening preference switch',async t=>{
 const f=setup(t);await settle();assert.equal(f.service.mayAdvance(),true);
 await f.service.savePreferences({bookRate:2});assert.equal(f.service.mayAdvance(),true);
 assert.deepEqual(f.writes,[{revision:0,bookRate:2,passoutMinutes:0}]);
});
test('item timer ends only on that item\'s actual ended observation',async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:'audiobook_file',bookId:'book',lastBookItemId:'last',bookEndAvailable:true})});await settle();
 await f.service.setTimer('item');f.engine.update({phase:'ended'});assert.equal(f.engine.pauses,1);
 f.service.interact();f.engine.update({phase:'ready',intent:'playing'});await f.service.setTimer('item');f.engine.update({positionSeconds:10000});assert.equal(f.engine.pauses,1);
 f.engine.update({phase:'ended'});assert.equal(f.engine.pauses,2);
});
test('an unavailable final part never invents an end-of-book timer',async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:'audiobook_file',bookId:'book',lastBookItemId:'missing',bookEndAvailable:false})});await settle();
 await assert.rejects(f.service.setTimer('book'),/final book part is unavailable/);assert.equal(f.service.getSnapshot().timer,null);await f.service.setTimer(30);assert.equal(f.service.getSnapshot().timer?.choice,30);
});
test('chapter timer uses source/session timing and does not stop on unsettled seeks',async t=>{
 const f=setup(t);await settle();await f.service.setTimer('chapter');assert.equal(f.service.getSnapshot().timer?.endSeconds,30);
 f.engine.update({positionSeconds:35,pendingSeek:{revision:1}});assert.equal(f.engine.pauses,0);
 f.engine.update({pendingSeek:undefined});assert.equal(f.engine.pauses,1);assert.equal(f.service.mayAdvance(),false);
});
test('chapter timers clear on source generation change, while minute timers clear across named libraries',async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:id==='song'?'library':'other',kind:'song'})});await settle();
 await f.service.setTimer('chapter');f.engine.update({session:{id:'session',generation:2}});assert.equal(f.service.getSnapshot().timer,null);
 await f.service.setTimer(30);f.engine.update({itemId:'other-song'});await settle();assert.equal(f.service.getSnapshot().timer,null);assert.equal(f.engine.pauses,0);
});
test('late old-profile reads and disposed clocks cannot publish or pause',async t=>{
 let resolve!:(v:unknown)=>void;const f=setup(t,{request:()=>new Promise(r=>{resolve=r;})});f.service.dispose();resolve(wrapped(prefs));await settle();f.setNow(99*60000);f.service.tick();
 assert.equal(f.service.getSnapshot().phase,'disposed');assert.equal(f.service.getSnapshot().context,null);assert.equal(f.engine.pauses,0);
});
test('save conflict retains actual preference and demands explicit reload; context retry is real',async t=>{
 let failedContext=true;const f=setup(t,{request:async(path,_body,method)=>{if(path==='/v1/listening/preferences'){if(method==='PUT')throw Object.assign(new Error('conflict'),{code:'listening_preferences_conflict'});return wrapped(prefs);}if(failedContext)throw new Error('offline');return wrapped({itemId:'song',libraryId:'library',kind:'song'});}});await settle();
 assert.equal(f.service.getSnapshot().context,null);failedContext=false;f.service.retryContext();await settle();assert.equal(f.service.getSnapshot().context?.itemId,'song');
 await assert.rejects(f.service.savePreferences({bookRate:2}),/changed on another device/);assert.deepEqual(f.service.getSnapshot().preferences,prefs);assert.equal(f.service.getSnapshot().phase,'error');await f.service.refreshPreferences();assert.equal(f.service.getSnapshot().phase,'ready');
});
test('full listening action reads every signed page, preserving server order and mix seed',async()=>{
 const items=Array.from({length:205},(_,i)=>listeningItemInput(`song-${i}`,target));let calls=0;
 const result=await loadListeningSelection(async path=>{const q=new URL(path,'https://server.test').searchParams;const at=Number(q.get('cursor')??0);calls++;if(at)assert.equal(q.get('seed'),'server-seed');return wrapped({target,entries:items.slice(at,at+100),totalCount:205,unavailableCount:3,nextCursor:at+100<205?String(at+100):'',seed:'server-seed'});},scope,target,{mix:true});
 assert.equal(calls,3);assert.deepEqual(result.entries,items);assert.equal(result.unavailableCount,3);assert.equal(result.seed,'server-seed');
});
test('selection refuses partial lists, repeated cursors, scope changes and queue overflow without a mutation',async()=>{
 const data={target,entries:[listeningItemInput('song',target)],totalCount:2,unavailableCount:0,nextCursor:'cursor',seed:''};
 await assert.rejects(loadListeningSelection(async()=>wrapped(data),scope,target,{capacity:1}),/room for 1/);
 await assert.rejects(loadListeningSelection(async()=>wrapped(data),scope,target));
 await assert.rejects(loadListeningSelection(async()=>wrapped({...data,nextCursor:''}),scope,target));
 await assert.rejects(loadListeningSelection(async()=>wrapped({...data,totalCount:1,nextCursor:''},{accountId:'other'}),scope,target));
 const abort=new AbortController();abort.abort();let calls=0;await assert.rejects(loadListeningSelection(async()=>{calls++;return wrapped(data);},scope,target,{signal:abort.signal}));assert.equal(calls,0);
});
test('book resume and explicit chapter selection preserve server part order and absolute source start',async()=>{
 const book={libraryId:'library',kind:'book' as const,id:'book'};let path='';const data={target:book,entries:[listeningItemInput('part-2',book),listeningItemInput('part-3',book)],totalCount:2,unavailableCount:0,nextCursor:'',seed:'',resume:{itemId:'part-2',positionMs:123500}};
 const r=await loadListeningSelection(async p=>{path=p;return wrapped(data);},scope,book,{resume:true});assert.match(path,/resume=true/);assert.equal(r.startSeconds,123.5);assert.equal(r.entries[0].itemId,'part-2');
 await loadListeningSelection(async p=>{path=p;return wrapped({...data,resume:undefined});},scope,book,{startItem:'part-2'});assert.match(path,/startItem=part-2/);
});

test('native policy has stable timer/activity revisions; observations cannot extend deadlines',async t=>{
 const f=setup(t);await settle();
 await f.service.setTimer(15);const armed=f.engine.policies.at(-1);assert.ok(armed.timer.wallDeadline>Date.now());
 f.engine.update({positionSeconds:10});f.service.tick();
 const observed=f.engine.policies.at(-1);assert.equal(observed.timerRevision,armed.timerRevision);assert.equal(observed.timer.wallDeadline,armed.timer.wallDeadline);assert.equal(observed.activityRevision,armed.activityRevision);
 f.service.interact();const interacted=f.engine.policies.at(-1);assert.ok(interacted.activityRevision>armed.activityRevision);assert.equal(interacted.timer.wallDeadline,armed.timer.wallDeadline);
 await f.service.setTimer('off');assert.ok(f.engine.policies.at(-1).timerRevision>armed.timerRevision);assert.equal(f.engine.policies.at(-1).timer,null);
});
test('native owns expiry and rate application; stale timer receipts cannot clear a replacement',async t=>{
 const f=setup(t);await settle();f.engine.native=true;
 f.engine.update({nativeListening:{timerStopped:false,expiredTimerRevision:-1}});
 await f.service.setTimer(15);const old=f.engine.policies.at(-1).timerRevision;
 await f.service.setTimer(30);const current=f.engine.policies.at(-1).timerRevision;
 const rates=f.engine.rates.length;await f.service.setRate(1);assert.equal(f.engine.rates.length,rates,'no duplicate JS rate writer for a native occurrence');
 f.setNow(9999999);f.service.tick();assert.equal(f.engine.pauses,0,'JS must not run the native deadline engine');
 f.engine.update({nativeListening:{timerStopped:true,expiredTimerRevision:old}});assert.equal(f.service.getSnapshot().timer?.choice,30);
 f.engine.update({nativeListening:{timerStopped:true,expiredTimerRevision:current}});assert.equal(f.service.getSnapshot().timer,null);assert.equal(f.service.mayAdvance(),false);assert.equal(f.engine.pauses,0);
 f.engine.update({nativeListening:{timerStopped:false,expiredTimerRevision:current}});assert.equal(f.service.getSnapshot().stopped,false);
});
test('transition admission respects final-item, verified chapter, minute and inactivity edges',async t=>{
 const f=setup(t,{preferences:{passoutMinutes:30}});await settle();
 assert.equal(f.service.mayTransition(10),true);assert.equal(f.service.mayTransition(NaN),false);
 await f.service.setTimer('item');assert.equal(f.service.mayTransition(1),false);
 await f.service.setTimer('chapter');assert.equal(f.service.mayTransition(1),false);
 await f.service.setTimer(15);f.setNow(899000);assert.equal(f.service.mayTransition(2),false);assert.equal(f.service.mayTransition(.1),true);
 await f.service.setTimer('off');f.service.interact();f.setNow(899000+1800000-1000);assert.equal(f.service.mayTransition(2),false);
});
test('end-of-book policy permits intermediate parts but prevents a prebuffered final-part escape',async t=>{
 const f=setup(t,{context:audiobookContext,queue:sessionId=>sessionId==='session'?{queueId:'queue',entryId:'entry-1'}:{queueId:'queue',entryId:'entry-2'}});await settle();await f.service.setTimer('book');assert.equal(f.service.mayTransition(2),true);
 f.service.acceptAutomaticTransition({id:'last-session',itemId:'last',presentation:{generation:1},queue:{queueId:'queue',entryId:'entry-2'}});
 f.engine.update({itemId:'last',session:{id:'last-session',generation:1}});await settle();
 assert.equal(f.service.getSnapshot().timer?.choice,'book');
 assert.equal(f.service.mayTransition(2),false);
});

test('browsing listening entities needs no queue and rejects cross-viewer or cross-item responses',async()=>{
 const bound={serverId:scope.serverId,viewerId:JSON.stringify([scope.authority,scope.accountId,scope.profileId])};
 const data={itemId:'song',libraryId:'library',kind:'song',albumId:'album',artistId:'artist'};
 assert.deepEqual(await readListeningContext(async()=>wrapped(data),bound,'song'),data);
 for(const change of [{serverId:'other'},{accountId:'other'},{profileId:'other'},{authority:'hosted'}])await assert.rejects(readListeningContext(async()=>wrapped(data,change),bound,'song'));
 await assert.rejects(readListeningContext(async()=>wrapped({...data,itemId:'other'}),bound,'song'));
});

test('PERF-24: a video session never asks for listening details; a listening one does, once its session is known',async t=>{
 const engine=new Engine();const paths:string[]=[];
 const request:ListeningRequest=async(path)=>{paths.push(path);if(path==='/v1/listening/preferences')return wrapped(prefs);const id=path.split('/')[3];return wrapped({itemId:id,libraryId:'library',kind:'song',albumId:'album',artistId:'artist'});};
 engine.state={...engine.state,itemId:'movie',session:{id:'s_movie',generation:1}};
 const service=new ListeningService(scope,request,engine as unknown as PlaybackService,()=>0,(itemId,sessionId)=>!(itemId==='movie'&&sessionId==='s_movie'));
 t.after(()=>service.dispose());
 service.connect();await settle();
 assert.equal(paths.filter(p=>p.includes('/items/')).length,0,'no /listening for the movie');
 engine.update({itemId:'song',session:undefined});await settle();
 assert.equal(paths.filter(p=>p.includes('/items/')).length,0,'waits for the song’s session');
 engine.update({session:{id:'s_song',generation:1}});await settle();
 assert.deepEqual(paths.filter(p=>p.includes('/items/')),['/v1/items/song/listening']);
 service.dispose();
});

// P1 (web, 24 Sep): an album stopped after track 1 when the listening preferences hadn't been
// read (or couldn't be). Preference loading must not gate queue continuation.
test('a song advances before the listening preferences are known', async t => {
 const f=setup(t,{request:async(path)=>{if(path==='/v1/listening/preferences')throw Object.assign(new Error('unavailable'),{status:503});const id=path.split('/')[3];return wrapped({itemId:id,libraryId:'library',kind:'song',albumId:'album',artistId:'artist'});}});
 await settle();await settle();
 assert.equal(f.service.getSnapshot().preferences,null);
 assert.equal(f.service.mayAdvance(),true);
});

test('a long audiobook pause rewinds once, while short pauses and music do not',async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:'audiobook_file',bookId:'book'}),audiobook:()=>({...defaultAudiobookPreferences,resumeRewindMs:15000})});const seeks:number[]=[];(f.engine as any).seek=(position:number)=>seeks.push(position);await settle();
 f.engine.update({positionSeconds:300,intent:'paused'});f.setNow(300001);f.engine.update({intent:'playing'});assert.deepEqual(seeks,[285]);assert.deepEqual(f.service.getSnapshot().rewind,{seconds:15,fromSeconds:300,toSeconds:285,messageKey:'listening.rewound'});f.engine.update({positionSeconds:286});assert.deepEqual(seeks,[285]);
 f.engine.update({intent:'paused'});f.setNow(300100);f.engine.update({intent:'playing'});assert.deepEqual(seeks,[285]);
});

test('same-item local replacement retires server book context, rate, timers and inactivity policy',async t=>{
 const paths:string[]=[];
 const f=setup(t,{preferences:{passoutMinutes:30},request:async path=>{paths.push(path);if(path==='/v1/listening/preferences')return wrapped({...prefs,passoutMinutes:30});return wrapped({itemId:'song',libraryId:'library',kind:'audiobook_file',bookId:'book'});}});
 const seeks:number[]=[];(f.engine as any).seek=(position:number)=>seeks.push(position);
 await settle();await f.service.setRate(2);await f.service.setTimer(15);
 assert.equal(f.engine.rates.at(-1),2);
 f.engine.update({intent:'paused'});f.setNow(2_000_000);
 f.engine.update({session:{id:'local:source',generation:1,mode:'local'},intent:'playing',nativeListening:{timerStopped:true,expiredTimerRevision:999}});
 assert.equal(f.service.getSnapshot().context,null);
 assert.equal(f.service.getSnapshot().timer,null);
 assert.equal(f.service.getSnapshot().rate,1);
 assert.equal(f.service.getSnapshot().rewind,undefined);
 assert.equal(f.engine.rates.at(-1),1);
 assert.deepEqual(seeks,[]);
 assert.equal(f.service.mayAdvance(),true);
 assert.equal(f.service.mayTransition(60),true);
 assert.equal(f.service.mayTransition(NaN),false);
 f.service.tick();assert.equal(f.engine.pauses,0);
 assert.deepEqual(f.engine.policies.at(-1).preferences,null);
 assert.deepEqual(f.engine.policies.at(-1).timer,null);
 f.engine.update({itemId:'file:private-local-item'});await settle();
 assert.deepEqual(paths.filter(path=>path.includes('/items/')),['/v1/items/song/listening']);
 f.engine.update({session:{id:'server-return',generation:1,mode:'direct'},itemId:'song',nativeListening:undefined});await settle();
 assert.equal(f.service.getSnapshot().context?.kind,'audiobook_file');
 assert.equal(f.service.getSnapshot().rate,1.5,'old per-playback held rate was not retained');
 assert.equal(paths.filter(path=>path.includes('/items/')).length,2);
});

test('late server context and chapter responses cannot rebind a local source',async t=>{
 let context!:(value:unknown)=>void;
 const paths:string[]=[];
 const f=setup(t,{request:async path=>{paths.push(path);if(path==='/v1/listening/preferences')return wrapped(prefs);return new Promise(resolve=>{context=resolve;});}});
 await settle();f.engine.update({session:{id:'local:source',generation:1,mode:'local'}});
 context(wrapped({itemId:'song',libraryId:'library',kind:'audiobook_file',bookId:'book'}));await settle();
 assert.equal(f.service.getSnapshot().context,null);assert.equal(f.service.getSnapshot().rate,1);
 assert.deepEqual(paths.filter(path=>path.includes('/items/')),['/v1/items/song/listening']);
 const chapterPending:{resolve?:(value:unknown)=>void}={};
 const g=setup(t,{request:async path=>{if(path==='/v1/listening/preferences')return wrapped(prefs);if(path.includes('/chapters'))return new Promise(resolve=>{chapterPending.resolve=resolve;});return wrapped({itemId:'song',libraryId:'library',kind:'audiobook_file',bookId:'book'});}});
 await settle();const setting=g.service.setTimer('chapter');await settle();
 assert.equal(g.service.getSnapshot().timerLoading,true);
 g.engine.native=true;g.engine.update({session:{id:'local:source',generation:1,mode:'local'}});
 chapterPending.resolve?.(chapterData());await setting;
 assert.equal(g.service.getSnapshot().context,null);assert.equal(g.service.getSnapshot().timer,null);assert.equal(g.service.getSnapshot().timerLoading,false);
 assert.equal(g.engine.policies.at(-1).preferences,null);assert.equal(g.engine.policies.at(-1).timer,null);
});

test('a synchronous local replacement during rate publication cannot apply the retired book rate',async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:'audiobook_file',bookId:'book'})});
 let replaced=false;
 f.service.subscribe(()=>{if(!replaced&&f.service.getSnapshot().rate===1.5){replaced=true;f.engine.update({session:{id:'local:source',generation:1,mode:'local'}});}});
 await settle();
 assert.equal(replaced,true);assert.equal(f.service.getSnapshot().context,null);assert.equal(f.service.getSnapshot().rate,1);
 assert.equal(f.engine.rates.includes(1.5),false);
});

test('a listening journey keeps millisecond resume and drops only unknown actions',()=>{
 const result=parseListeningJourney({target:{libraryId:'library',kind:'book',id:'book'},actions:['play','future','play'],resume:{itemId:'part',positionMs:123500},resumeUnavailable:false},'library');
 assert.deepEqual(result.actions,['play']);
 assert.deepEqual(result.resume,{itemId:'part',positionSeconds:123.5});
});

test('listening exposes actual engine processing facts and clears them when the engine clears them',async t=>{
 const f=setup(t);await settle();
 assert.equal(f.service.getSnapshot().eqApplied,false);assert.equal(f.service.getSnapshot().levelingApplied,false);
 f.engine.update({audioEffects:{...f.engine.state.audioEffects,eqApplied:true,levelingApplied:true}});
 assert.equal(f.service.getSnapshot().eqApplied,true);assert.equal(f.service.getSnapshot().levelingApplied,true);
 f.engine.update({phase:'ended',audioEffects:{...f.engine.state.audioEffects,eqApplied:false,levelingApplied:false}});
 assert.equal(f.service.getSnapshot().eqApplied,false);assert.equal(f.service.getSnapshot().levelingApplied,false);
 f.service.dispose();assert.equal(f.engine.listeners.size,0);
});

for(const elapsed of [300001,3600001]) test(`default zero never rewinds after ${elapsed}ms`,async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:'audiobook_file'})});const seeks:number[]=[];(f.engine as any).seek=(v:number)=>seeks.push(v);await settle();
 f.engine.update({positionSeconds:100,intent:'paused'});f.setNow(elapsed);f.engine.update({intent:'playing'});
 assert.deepEqual(seeks,[]);assert.equal(f.service.getSnapshot().rewind,undefined);
});
test('chosen long-pause amount clamps once and is not doubled for the native owner',async t=>{
 let amount=5000;const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:'audiobook_file'}),audiobook:()=>({...defaultAudiobookPreferences,resumeRewindMs:amount})});const seeks:number[]=[];(f.engine as any).seek=(v:number)=>seeks.push(v);await settle();
 f.engine.update({positionSeconds:2,intent:'paused'});f.setNow(3600001);f.engine.update({intent:'playing'});assert.deepEqual(seeks,[0]);assert.equal(f.service.getSnapshot().rewind?.seconds,2);
 f.engine.update({positionSeconds:1});assert.deepEqual(seeks,[0]);
 f.engine.native=true;f.engine.update({positionSeconds:50,intent:'paused'});amount=30000;f.service.refreshAudiobookPreferences();f.setNow(7200002);f.engine.update({intent:'playing'});assert.deepEqual(seeks,[0],'native owner alone applies its own long-pause policy');
 assert.equal(f.engine.policies.at(-1).audiobook.resumeRewindMs,30000);
});
for(const change of [{seekRevision:1},{pendingSeek:{revision:1,positionSeconds:30}},{failedSeek:{revision:1,positionSeconds:30}},{session:{id:'replacement',generation:2}},{playbackRun:2}]) test(`long-pause rewind yields to changed intent ${JSON.stringify(change)}`,async t=>{
 const f=setup(t,{context:id=>({itemId:id,libraryId:'library',kind:'audiobook_file'}),audiobook:()=>({...defaultAudiobookPreferences,resumeRewindMs:15000})});const seeks:number[]=[];(f.engine as any).seek=(v:number)=>seeks.push(v);await settle();
 f.engine.update({positionSeconds:50,intent:'paused'});f.setNow(3600001);f.engine.update({...change,intent:'playing'});assert.deepEqual(seeks,[]);
});

// S-GAP-91056AB regressions. A timer follows the accepted occurrence it was armed for, never a
// later start of the same catalogue item, and a book boundary only crosses an actually accepted,
// ordered next part. These are authored against the in-flight listening source: item and chapter
// timers capture the original session/generation/item/run/queue (+ native owner), the book timer
// reads the real scoped timeline, and a pending or refused native policy never shows an armed timer.
test('an end timer follows its exact occurrence; a new session or queue entry of the same item retires it',async t=>{
 let entry='entry-1';
 const f=setup(t,{queue:()=>({queueId:'queue',entryId:entry})});await settle();
 await f.service.setTimer('item');
 assert.deepEqual(f.engine.policies.at(-1).timer.occurrence,{sessionId:'session',generation:1,itemId:'song',playbackRun:1,queue:{queueId:'queue',entryId:'entry-1'}});
 f.engine.update({session:{id:'same-item-session',generation:1}});
 assert.equal(f.service.getSnapshot().timer,null,'another session of the same item is not this occurrence');
 await f.service.setTimer('item');
 entry='entry-2';
 f.engine.update({positionSeconds:6});
 assert.equal(f.service.getSnapshot().timer,null,'another queue entry of the same item is not this occurrence');
});
test('an item timer records the native owner it was armed under',async t=>{
 const f=setup(t);await settle();f.engine.native=true;f.engine.update({nativeListening:{owner:'owner-a'}});
 await f.service.setTimer('item');
 assert.equal(f.engine.policies.at(-1).timer.occurrence.owner,'owner-a');
 assert.equal(f.engine.policies.at(-1).timer.occurrence.sessionId,'session');
});
test('deliberate navigation retires an end boundary and the same item never inherits it',async t=>{
 let entry='entry-1';
 const f=setup(t,{queue:()=>({queueId:'queue',entryId:entry})});await settle();
 await f.service.setTimer('item');
 f.service.cancelBoundaryTimer();
 assert.equal(f.service.getSnapshot().timer,null);
 entry='entry-2';
 f.engine.update({session:{id:'same-item-again',generation:1}});await settle();
 assert.equal(f.service.getSnapshot().timer,null,'a later start of the same item does not inherit a retired boundary');
 assert.equal(f.service.mayAdvance(),true);
});
test('only an actually accepted ordered book part carries the boundary',async t=>{
 let entry='entry-1';
 const f=setup(t,{context:audiobookContext,queue:()=>({queueId:'queue',entryId:entry}),timeline:cursor=>cursor==='p2'?bookTimeline(cursor,{parts:['last'],totalCount:3}):bookTimeline(cursor,{parts:['song','mid'],totalCount:3,nextCursor:'p2'})});await settle();
 await f.service.setTimer('book');
 assert.deepEqual(f.service.getSnapshot().timer?.bookBoundary?.items,['song','mid','last']);
 assert.deepEqual(f.engine.policies.at(-1).timer.bookBoundary,{revision:{catalog:1,viewer:1},items:['song','mid','last']});
 // The source callback derives the queue from the immutable CURRENT session, so the original
 // entry must still be current when the acceptance arrives; the new one follows the handoff.
 f.service.acceptAutomaticTransition({id:'mid-session',itemId:'mid',presentation:{generation:2},queue:{queueId:'queue',entryId:'entry-2'}});
 entry='entry-2';
 f.engine.update({itemId:'mid',session:{id:'mid-session',generation:2}});await settle();
 assert.equal(f.service.getSnapshot().timer?.choice,'book');
 assert.deepEqual(f.service.getSnapshot().timer?.occurrence,{sessionId:'mid-session',generation:2,itemId:'mid',playbackRun:1,queue:{queueId:'queue',entryId:'entry-2'}});
 assert.equal(f.service.mayTransition(2),true,'an intermediate accepted part is not the book end');
});
test('an unaccepted next book part clears the boundary',async t=>{
 let entry='entry-1';
 const f=setup(t,{context:audiobookContext,queue:()=>({queueId:'queue',entryId:entry})});await settle();
 await f.service.setTimer('book');
 entry='entry-2';f.engine.update({itemId:'last',session:{id:'next-session',generation:1}});
 assert.equal(f.service.getSnapshot().timer,null,'a part that moved without an accepted transition is not proved');
});
test('a transition from another queue never carries the boundary',async t=>{
 let entry='entry-1';
 const f=setup(t,{context:audiobookContext,queue:()=>({queueId:'queue',entryId:entry})});await settle();
 await f.service.setTimer('book');
 f.service.acceptAutomaticTransition({id:'next-session',itemId:'last',presentation:{generation:1},queue:{queueId:'other-queue',entryId:'entry-2'}});
 entry='entry-2';f.engine.update({itemId:'last',session:{id:'next-session',generation:1}});
 assert.equal(f.service.getSnapshot().timer,null,'a transition from another queue is not this book');
});
test('a misordered part, a new playback run or another native owner clears the boundary',async t=>{
 let entry='entry-1';
 const g=setup(t,{context:audiobookContext,queue:()=>({queueId:'queue',entryId:entry})});await settle();
 await g.service.setTimer('book');
 g.service.acceptAutomaticTransition({id:'next-session',itemId:'mid',presentation:{generation:1},queue:{queueId:'queue',entryId:'entry-2'}});
 entry='entry-2';
 g.engine.update({itemId:'mid',session:{id:'next-session',generation:1}});
 assert.equal(g.service.getSnapshot().timer,null,'a part that is not the immediate next one is refused');
 const r=setup(t,{context:audiobookContext});await settle();
 await r.service.setTimer('book');
 r.engine.update({playbackRun:2});
 assert.equal(r.service.getSnapshot().timer,null,'a new playback run never inherits the boundary');
 const f=setup(t,{context:audiobookContext});await settle();
 f.engine.update({nativeListening:{owner:'owner-a'}});
 await f.service.setTimer('book');
 f.engine.update({nativeListening:{owner:'owner-b'}});
 assert.equal(f.service.getSnapshot().timer,null,'another native owner never inherits the boundary');
});
test('the book timeline is read from its real scoped path with the server order',async()=>{
 const paths:string[]=[];
 const timeline=await readBookTimeline(async path=>{paths.push(path);return wrapped(bookTimeline('',{parts:['part-a','part-b'],totalCount:2}));},scope,'book',{limit:100});
 assert.deepEqual(timeline.parts.map(part=>part.itemId),['part-a','part-b']);
 assert.match(paths[0],/^\/v1\/books\/book\/timeline\?/);
 assert.match(paths[0],/limit=100/);
});
test('a book timeline that changes, repeats, belongs elsewhere or lacks the final part is refused',async t=>{
 const changed=setup(t,{context:audiobookContext,timeline:cursor=>cursor==='p2'?bookTimeline(cursor,{parts:['last'],totalCount:3,revision:{catalog:2,viewer:1}}):bookTimeline(cursor,{parts:['song','mid'],totalCount:3,nextCursor:'p2'})});await settle();
 await assert.rejects(changed.service.setTimer('book'),/Book order changed/);assert.equal(changed.service.getSnapshot().timer,null);
 const repeated=setup(t,{context:audiobookContext,timeline:cursor=>cursor==='p2'?bookTimeline(cursor,{parts:['mid'],totalCount:3}):bookTimeline(cursor,{parts:['song','mid'],totalCount:3,nextCursor:'p2'})});await settle();
 await assert.rejects(repeated.service.setTimer('book'),/Book order changed/);
 const elsewhere=setup(t,{context:audiobookContext,timeline:cursor=>bookTimeline(cursor,{libraryId:'other'})});await settle();
 await assert.rejects(elsewhere.service.setTimer('book'),/Book order changed/);
 const huge=setup(t,{context:audiobookContext,timeline:cursor=>bookTimeline(cursor,{totalCount:1001})});await settle();
 await assert.rejects(huge.service.setTimer('book'),/Book order changed/);
 const missing=setup(t,{context:audiobookContext,timeline:cursor=>bookTimeline(cursor,{parts:['song','mid'],totalCount:2})});await settle();
 await assert.rejects(missing.service.setTimer('book'),/final book part is unavailable/);assert.equal(missing.service.getSnapshot().timer,null);
 await missing.service.setTimer(30);assert.equal(missing.service.getSnapshot().timer?.choice,30);
});
test('a pending native acknowledgement shows no armed timer and blocks continuation',async t=>{
 const f=setup(t);await settle();
 let release!:(v:boolean)=>void;f.engine.answer=()=>new Promise<boolean>(resolve=>{release=resolve;});
 const setting=f.service.setTimer('item');
 assert.equal(f.service.getSnapshot().timer,null);assert.equal(f.service.getSnapshot().timerLoading,true);
 assert.equal(f.service.mayAdvance(),false);
 assert.equal(f.engine.policies.at(-1).timer.choice,'item','the pending policy carries the boundary the native owner must accept');
 release(true);await setting;
 assert.equal(f.service.getSnapshot().timer?.choice,'item');assert.equal(f.service.getSnapshot().timerLoading,false);
});
test('a refused native acknowledgement rejects the timer and leaves none armed',async t=>{
 const f=setup(t);await settle();f.engine.answer=()=>false;
 await assert.rejects(f.service.setTimer('item'),/source changed/);
 assert.equal(f.service.getSnapshot().timer,null);assert.equal(f.service.getSnapshot().timerLoading,false);
 assert.equal(f.service.mayAdvance(),true);
});
test('a stale acknowledgement cannot replace a newer timer',async t=>{
 const f=setup(t);await settle();
 const answers:((v:boolean)=>void)[]=[];f.engine.answer=()=>new Promise<boolean>(resolve=>{answers.push(resolve);});
 const first=f.service.setTimer('item');
 await settle();
 assert.equal(f.service.getSnapshot().timer,null,'the older install is still waiting for its answer');
 const second=f.service.setTimer(15);
 await settle();
 for(const resolve of answers)resolve(true);
 await first;await second;await settle();
 assert.equal(f.service.getSnapshot().timer?.choice,15,'the older acknowledgement cannot replace the newer timer');
 assert.equal(f.service.getSnapshot().timerLoading,false);
});
test('an in-flight automatic transition refuses an end boundary until it retires',async t=>{
 const f=setup(t);await settle();
 const release=f.service.beginAutomaticTransition();
 await assert.rejects(f.service.setTimer('item'),/already moving/);
 await f.service.setTimer(15);assert.equal(f.service.getSnapshot().timer?.choice,15,'a minute deadline is not an end boundary');
 release();
 await f.service.setTimer('item');assert.equal(f.service.getSnapshot().timer?.choice,'item');
});
test('a chapter timer keeps its exact occurrence and stops only at the verified chapter edge',async t=>{
 const f=setup(t);await settle();await f.service.setTimer('chapter');
 assert.deepEqual(f.service.getSnapshot().timer?.occurrence,{sessionId:'session',generation:1,itemId:'song',playbackRun:1,queue:{queueId:'queue',entryId:'entry-1'}});
 f.engine.update({positionSeconds:10,pendingSeek:{revision:1}});assert.equal(f.engine.pauses,0,'an unsettled seek is not the chapter edge');
 f.engine.update({positionSeconds:45});assert.equal(f.engine.pauses,0,'even past the edge an unsettled seek does not stop playback');
 f.engine.update({positionSeconds:29,pendingSeek:undefined});assert.equal(f.engine.pauses,0);
 f.engine.update({positionSeconds:30,pendingSeek:undefined});assert.equal(f.engine.pauses,1);
});
test('an acceptance after the engine already moved cannot resurrect a cleared boundary',async t=>{
 const f=setup(t,{context:audiobookContext,queue:()=>({queueId:'queue',entryId:'entry-2'})});await settle();
 await f.service.setTimer('book');
 f.engine.update({itemId:'last',session:{id:'next-session',generation:1}});
 assert.equal(f.service.getSnapshot().timer,null);
 f.service.acceptAutomaticTransition({id:'next-session',itemId:'last',presentation:{generation:1},queue:{queueId:'queue',entryId:'entry-2'}});
 assert.equal(f.service.getSnapshot().timer,null,'a late acceptance cannot revive the retired boundary');
});
test('a part whose own listening context belongs to another book never carries the boundary',async t=>{
 let entry='entry-1';
 const context=(id:string)=>id==='last'?{...audiobookContext(id),bookId:'other-book'}:audiobookContext(id);
 const f=setup(t,{context,queue:()=>({queueId:'queue',entryId:entry})});await settle();
 await f.service.setTimer('book');
 f.service.acceptAutomaticTransition({id:'next-session',itemId:'last',presentation:{generation:1},queue:{queueId:'queue',entryId:'entry-2'}});
 entry='entry-2';
 f.engine.update({itemId:'last',session:{id:'next-session',generation:1}});await settle();
 assert.equal(f.service.getSnapshot().timer,null,'the next part reported another book');
});
test('a queue occurrence that moved before acceptance cannot carry or revive the boundary',async t=>{
 let entry='entry-1';
 const f=setup(t,{context:audiobookContext,queue:()=>({queueId:'queue',entryId:entry})});await settle();
 await f.service.setTimer('book');
 // The engine's own current occurrence is no longer the one the timer captured, so the real
 // sameOccurrence guard refuses the acceptance instead of carrying it.
 entry='entry-2';
 f.service.acceptAutomaticTransition({id:'next-session',itemId:'last',presentation:{generation:1},queue:{queueId:'queue',entryId:'entry-2'}});
 f.engine.update({itemId:'last',session:{id:'next-session',generation:1}});
 assert.equal(f.service.getSnapshot().timer,null,'a moved original occurrence is not an accepted handoff');
});
// Merged-source edges: the native owner can expire a boundary while the install is waiting, and a
// caller's original occurrence must never be replaced by a source that changes during setup.
test('a native expiry reported during the install leaves no armed timer and reports the stop',async t=>{
 const f=setup(t);await settle();f.engine.native=true;
 // The owner's own answer carries the expiry for this exact revision; no listener round trip needed.
 f.engine.answer=policy=>{f.engine.state={...f.engine.state,nativeListening:{timerStopped:true,expiredTimerRevision:policy.timerRevision}};return true;};
 await f.service.setTimer('item');
 const snapshot=f.service.getSnapshot();
 assert.equal(snapshot.timer,null,'the owner expiry means this timer is not armed');
 assert.equal(snapshot.timerLoading,false);
 assert.equal(snapshot.stopped,true,'the stop the owner reported is the state the screen sees');
});
test('a stale explicit occurrence refuses before any timer mutation',async t=>{
 const f=setup(t);await settle();
 const before=f.service.getSnapshot(),policies=f.engine.policies.length;
 const stale={sessionId:'older-session',generation:1,itemId:'song',playbackRun:1,queue:{queueId:'queue',entryId:'entry-1'}};
 await assert.rejects(f.service.setTimer('item',stale),(error:any)=>error?.name==='AbortError'&&/source retired/.test(error.message));
 assert.equal(f.service.getSnapshot(),before,'the refusal published no snapshot change');
 assert.equal(f.engine.policies.length,policies,'the refusal sent no policy to the native owner');
 assert.equal(f.service.getSnapshot().timer,null);
 assert.equal(f.service.getSnapshot().timerLoading,false);
});
test('a source replaced by a synchronous listener during setup is never the timer target',async t=>{
 const f=setup(t);await settle();
 let replaced=false;
 f.service.subscribe(()=>{if(!replaced){replaced=true;f.engine.update({session:{id:'replacement-session',generation:1}});}});
 await assert.rejects(f.service.setTimer('item'),(error:any)=>error?.name==='AbortError'&&/source retired/.test(error.message));
 assert.equal(replaced,true);assert.equal(f.service.getSnapshot().timer,null);
 assert.deepEqual(f.service.getSnapshot().timer,null,'the replacement occurrence was not pinned');
 const g=setup(t);await settle();
 await g.service.setTimer(15);g.setNow(900000);g.service.tick();
 assert.equal(g.service.getSnapshot().stopped,true);
 let swapped=false;
 g.service.subscribe(()=>{if(!swapped){swapped=true;g.engine.update({session:{id:'swap',generation:1}});}});
 await assert.rejects(g.service.setTimer('item'),(error:any)=>error?.name==='AbortError');
 assert.equal(swapped,true);assert.equal(g.service.getSnapshot().timer,null);
});

// Opening-lifetime regressions: a listening timer belongs to the caller's original opening. A
// retired opening mutates nothing and dispatches no armed policy, a retirement during a book or
// chapter read aborts that exact request, and a close only ever clears its own still-pending
// install — never a newer one, and never one whose acknowledgement was already accepted.
const deferred=<T>()=>{let resolve!:(value:T)=>void;const promise=new Promise<T>(r=>{resolve=r;});return {promise,resolve};};
const abortedError=()=>Object.assign(new Error('aborted'),{name:'AbortError'});

test('an already-retired opening refuses a timer before any mutation',async t=>{
 const f=setup(t);await settle();
 const retired=new AbortController();retired.abort();
 for(const context of [{signal:retired.signal},{isCurrent:()=>false}]){
  const before=f.service.getSnapshot(),policies=f.engine.policies.length;
  await assert.rejects(f.service.setTimer('item',undefined,context),(error:any)=>error?.name==='AbortError');
  assert.equal(f.service.getSnapshot(),before,'the refusal published no snapshot change');
  assert.equal(f.engine.policies.length,policies,'the refusal dispatched no policy');
  assert.equal(f.service.getSnapshot().timer,null);
 }
});

test('a retired opening during the book read dispatches no armed timer',async t=>{
 let heldSignal:AbortSignal|undefined;
 const f=setup(t,{context:audiobookContext,request:async(path,_body,_method,signal)=>{
  if(path==='/v1/listening/preferences')return wrapped(prefs);
  if(path.includes('/timeline')){heldSignal=signal;return new Promise((_resolve,reject)=>{signal?.addEventListener('abort',()=>reject(abortedError()),{once:true});});}
  return wrapped(audiobookContext(path.split('/')[3]));
 }});
 await settle();
 const controller=new AbortController(),engine=f.engine.state;
 const setting=f.service.setTimer('book',undefined,{signal:controller.signal});
 await settle();
 assert.equal(heldSignal?.aborted,false,'the read starts under the original opening');
 const armedFrom=f.engine.policies.length;
 controller.abort();
 await assert.rejects(setting,(error:any)=>error?.name==='AbortError');
 assert.equal(heldSignal?.aborted,true,'the received request signal aborts with the opening');
 assert.equal(f.engine.state,engine,'no output was pinned to a later occurrence');
 assert.equal(f.service.getSnapshot().timer,null);
 assert.equal(f.service.getSnapshot().timerLoading,false);
 assert.equal(f.engine.policies.slice(armedFrom).some(policy=>policy.timer),false,'no armed timer policy was dispatched');
});

test('an opening that is no longer current during the book read dispatches no armed timer',async t=>{
 let releaseTimeline!:(value:unknown)=>void;
 const f=setup(t,{context:audiobookContext,request:async(path)=>{
  if(path==='/v1/listening/preferences')return wrapped(prefs);
  if(path.includes('/timeline'))return new Promise(resolve=>{releaseTimeline=resolve;});
  return wrapped(audiobookContext(path.split('/')[3]));
 }});
 await settle();
 let current=true;const engine=f.engine.state;
 const setting=f.service.setTimer('book',undefined,{isCurrent:()=>current});
 await settle();
 const armedFrom=f.engine.policies.length;
 current=false;
 releaseTimeline(wrapped(bookTimeline('')));
 await assert.rejects(setting,(error:any)=>error?.name==='AbortError');
 assert.equal(f.engine.state,engine,'no output was pinned to a later occurrence');
 assert.equal(f.service.getSnapshot().timer,null);
 assert.equal(f.service.getSnapshot().timerLoading,false);
 assert.equal(f.engine.policies.slice(armedFrom).some(policy=>policy.timer),false,'no armed timer policy was dispatched');
});

test('a retired opening during the chapter read dispatches no armed timer',async t=>{
 let heldSignal:AbortSignal|undefined;
 const f=setup(t,{request:async(path,_body,_method,signal)=>{
  if(path==='/v1/listening/preferences')return wrapped(prefs);
  if(path.includes('/chapters')){heldSignal=signal;return new Promise((_resolve,reject)=>{signal?.addEventListener('abort',()=>reject(abortedError()),{once:true});});}
  return wrapped({itemId:path.split('/')[3],libraryId:'library',kind:'song',albumId:'album',artistId:'artist'});
 }});
 await settle();
 const controller=new AbortController(),engine=f.engine.state;
 const setting=f.service.setTimer('chapter',undefined,{signal:controller.signal});
 await settle();
 assert.equal(heldSignal?.aborted,false,'the chapter read starts under the original opening');
 const armedFrom=f.engine.policies.length;
 controller.abort();
 await assert.rejects(setting,(error:any)=>error?.name==='AbortError');
 assert.equal(heldSignal?.aborted,true,'the received chapter request signal aborts with the opening');
 assert.equal(f.engine.state,engine,'no output was pinned to a later occurrence');
 assert.equal(f.service.getSnapshot().timer,null);
 assert.equal(f.engine.policies.slice(armedFrom).some(policy=>policy.timer),false,'no armed timer policy was dispatched');
});

test('an opening that closes during the acknowledgement clears only its pending install',async t=>{
 const f=setup(t);await settle();
 const held=deferred<boolean>();let itemPolicies=0;
 f.engine.answer=policy=>policy.timer?.choice==='item'&&itemPolicies++<1?held.promise:true;
 const controller=new AbortController();
 const setting=f.service.setTimer('item',undefined,{signal:controller.signal});
 await settle();
 assert.equal(itemPolicies,1,'the install is waiting for its single explicit acknowledgement');
 const armedFrom=f.engine.policies.length;
 controller.abort();
 assert.equal(f.service.getSnapshot().timer,null);
 assert.equal(f.service.getSnapshot().timerLoading,false);
 held.resolve(true);
 await assert.rejects(setting,(error:any)=>error?.name==='AbortError');
 assert.equal(f.service.getSnapshot().timer,null,'a late acknowledgement armed the retired install');
 assert.equal(f.engine.policies.slice(armedFrom).some(policy=>policy.timer),false,'no armed timer policy was dispatched after retirement');
});

test('a newer timer is untouched when an older opening closes and acknowledges late',async t=>{
 const f=setup(t);await settle();
 const held=deferred<boolean>();let itemPolicies=0;
 f.engine.answer=policy=>policy.timer?.choice==='item'&&itemPolicies++<1?held.promise:true;
 const controller=new AbortController();
 const older=f.service.setTimer('item',undefined,{signal:controller.signal});
 await settle();
 await f.service.setTimer(15);
 assert.equal(f.service.getSnapshot().timer?.choice,15);
 const policies=f.engine.policies.length;
 controller.abort();
 held.resolve(true);
 await assert.rejects(older,(error:any)=>error?.name==='AbortError');
 assert.equal(f.service.getSnapshot().timer?.choice,15,'the older opening cleared the newer timer');
 assert.equal(f.engine.policies.length,policies,'the retired older install dispatched another policy');
});

test('closing the opening after the timer was accepted does not clear it',async t=>{
 const f=setup(t);await settle();
 const controller=new AbortController();
 await f.service.setTimer('item',undefined,{signal:controller.signal});
 const accepted=f.service.getSnapshot().timer;
 assert.equal(accepted?.choice,'item');
 const policies=f.engine.policies.length;
 controller.abort();
 await settle();
 assert.equal(f.service.getSnapshot().timer,accepted,'an accepted timer was cleared by a later opening close');
 assert.equal(f.engine.policies.length,policies,'closing an accepted opening dispatched a policy');
});
test('a synchronous close from the timerLoading update dispatches no armed timer',async t=>{
 const f=setup(t);await settle();
 const controller=new AbortController();let closed=false;
 f.service.subscribe(()=>{if(!closed&&f.service.getSnapshot().timerLoading){closed=true;controller.abort();}});
 const policies=f.engine.policies.length;
 await assert.rejects(f.service.setTimer('item',undefined,{signal:controller.signal}),(error:any)=>error?.name==='AbortError');
 assert.equal(closed,true,'the timerLoading update reached the listener before any dispatch');
 assert.equal(f.service.getSnapshot().timer,null);
 assert.equal(f.service.getSnapshot().timerLoading,false);
 assert.equal(f.engine.policies.slice(policies).some(policy=>policy.timer),false,'no armed timer policy was dispatched');
});
