import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {wireDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
/** One listening policy for a proved account/profile/server/device lane.
 * Foreground engines enforce it here; the native owner enforces the same policy
 * when JavaScript is suspended. The queue alone selects accepted occurrences. */
import type {SavedScope} from './saved.ts';
import type {PlaybackService} from './index.ts';
import type {ListeningTimerOccurrence,ListeningBookBoundary} from './playback/native-listening.ts';
import type {QueueScope,QueueItemInput} from './queues.ts';
import {parseQueueItem} from './queues.ts';
/** The viewer facts a read-only catalogue read shares with a queue scope. Controller
 * identity belongs to queue commands and is never invented for a read. */
export type ListeningViewerScope = Readonly<Pick<QueueScope, 'serverId' | 'authority' | 'accountId' | 'profileId'>>;
import {PlayerChaptersService} from './player-chapters.ts';
import {defaultAudiobookPreferences,type AudiobookPreferences} from './audiobook-preferences.ts';
import {requireSessionChangeCurrent,sessionChangeCurrent,type SessionChangeContext} from './playback-v1/change-context.ts';

async function listeningRead<T>(signals:readonly (AbortSignal|undefined)[],read:(signal:AbortSignal)=>Promise<T>):Promise<T>{
 const controller=new AbortController(),abort=()=>controller.abort();
 for(const signal of signals){if(signal?.aborted)abort();else signal?.addEventListener('abort',abort,{once:true});}
 try{return await read(controller.signal);}finally{for(const signal of signals)signal?.removeEventListener('abort',abort);}
}

export const listeningRates=[.5,.75,1,1.25,1.5,1.75,2] as const;
export const audiobookRates=[...listeningRates,2.5,3] as const;
export const passoutChoices=[0,30,60,90,120] as const;
export type ListeningPreferences=Readonly<{revision:number;bookRate:number;passoutMinutes:number}>;
export type ListeningTarget=Readonly<{libraryId:string;kind:'library'|'song'|'artist'|'album'|'disc'|'book';id:string}>;
export type ListeningJourney=Readonly<{target:ListeningTarget;actions:readonly ('play'|'shuffle'|'mix'|'enqueue')[];resume?:Readonly<{itemId:string;positionSeconds:number}>;resumeUnavailable:boolean}>;
export type ListeningItem=Readonly<{itemId:string;libraryId:string;kind:string;bookId?:string;lastBookItemId?:string;bookEndAvailable?:boolean;albumId?:string;artistId?:string}>;
export type ListeningRequest=(path:string,body?:unknown,method?:'GET'|'POST'|'PUT',signal?:AbortSignal)=>Promise<unknown>;
export type SleepChoice='off'|'chapter'|'item'|'book'|15|30|45|60|90;
export type ListeningTimer=Readonly<{choice:SleepChoice;label:string;deadline?:number;wallDeadline?:number;libraryId?:string;endSeconds?:number;itemId?:string;bookId?:string;sessionId?:string;generation?:number;occurrence?:ListeningTimerOccurrence;bookBoundary?:ListeningBookBoundary}>;
export type ListeningSnapshot=Readonly<{eqApplied:boolean;levelingApplied:boolean;rewind?:Readonly<{seconds:number;fromSeconds:number;toSeconds:number;messageKey:'listening.rewound'}>; phase:'loading'|'ready'|'saving'|'error'|'disposed';preferences:ListeningPreferences|null;context:ListeningItem|null;rate:number;timer:ListeningTimer|null;timerLoading:boolean;stopped:boolean;message:string|null;error:string|null}>;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
const finite=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v)&&v>=0;
const counter=(v:unknown):v is number=>Number.isSafeInteger(v)&&Number(v)>=0;
function bad():never{throw new Error(unreadableServerResponse);}
export function parseListeningPreferences(v:unknown):ListeningPreferences {
 if(!object(v)||Object.keys(v).some(key=>!['revision','bookRate','passoutMinutes'].includes(key))||!counter(v.revision)||!audiobookRates.includes(v.bookRate as never)||!passoutChoices.includes(v.passoutMinutes as never))bad();
 return Object.freeze({revision:v.revision,bookRate:v.bookRate as number,passoutMinutes:v.passoutMinutes as number});
}
export function listeningData(raw:unknown,scope:ListeningViewerScope):unknown {
 if(!object(raw)||!object(raw.scope)||(['serverId','authority','accountId','profileId'] as const).some(k=>(raw.scope as Record<string,unknown>)[k]!==scope[k]))bad();
 return raw.data;
}
/** What can be played from a page. Its target is exact; a way of playing this app does not know is not offered, and the ones it knows still are. */
export function parseListeningJourney(v:unknown,libraryId:string):ListeningJourney {
 if(!object(v)||!object(v.target)||v.target.libraryId!==libraryId||!['library','song','artist','album','disc','book'].includes(String(v.target.kind))||typeof v.target.id!=='string'||!(/^[A-Za-z0-9_-]{1,128}(?::disc:\d{1,4})?$/.test(v.target.id))||!Array.isArray(v.actions)||v.actions.length>16||typeof v.resumeUnavailable!=='boolean')bad();
 let resume:ListeningJourney['resume'];if(v.resume!==undefined){const r=wireDurations(v.resume,{positionSeconds:'positionMs'});if(!object(r)||!id(r.itemId)||!finite(r.positionSeconds))bad();resume=Object.freeze({itemId:r.itemId,positionSeconds:r.positionSeconds});}
 return Object.freeze({target:Object.freeze({libraryId,kind:v.target.kind as ListeningTarget['kind'],id:v.target.id}),actions:Object.freeze([...new Set(v.actions.filter(a=>['play','shuffle','mix','enqueue'].includes(String(a))))]) as ListeningJourney['actions'],...(resume?{resume}:{}),resumeUnavailable:v.resumeUnavailable});
}
function parseItem(v:unknown,itemId:string):ListeningItem {
 if(!object(v)||v.itemId!==itemId||!id(v.libraryId)||typeof v.kind!=='string')bad();
 if(v.bookEndAvailable!==undefined&&typeof v.bookEndAvailable!=='boolean')bad();
 for(const k of ['bookId','lastBookItemId','albumId','artistId'])if(v[k]!==undefined&&!id(v[k]))bad();
 return Object.freeze({itemId,libraryId:v.libraryId,kind:v.kind,...(typeof v.bookEndAvailable==='boolean'?{bookEndAvailable:v.bookEndAvailable}:{}),...(v.bookId?{bookId:v.bookId as string}:{}),...(v.lastBookItemId?{lastBookItemId:v.lastBookItemId as string}:{}),...(v.albumId?{albumId:v.albumId as string}:{}),...(v.artistId?{artistId:v.artistId as string}:{})});
}
/** Expands the server's complete selection through signed, revision-bound pages.
 * Refuses an over-capacity selection rather than silently using its first page. */
export async function loadListeningSelection(request:ListeningRequest,scope:QueueScope,target:ListeningTarget,options:{mix?:boolean;resume?:boolean;capacity?:number;startItem?:string;signal?:AbortSignal}={}) {
 const entries:QueueItemInput[]=[],seen=new Set<string>(),cursors=new Set<string>();let cursor='',seed='',total=-1,unavailable=0,startSeconds:number|undefined;
 const capacity=Math.max(0,Math.min(1000,options.capacity??1000));
 do {
  if(options.signal?.aborted)throw new Error('Listening action cancelled.');
  const q=new URLSearchParams({kind:target.kind,entityId:target.id,mode:options.mix?'mix':'ordered',resume:String(options.resume??false),limit:'100'});if(options.startItem)q.set('startItem',options.startItem);if(cursor)q.set('cursor',cursor);if(seed)q.set('seed',seed);
  const d=listeningData(await request(encodedRoutePath("get_libraries_id_listening_selection", {"id": encodeURIComponent(target.libraryId)}) + "?" + (q),undefined,'GET',options.signal),scope);
  if(!object(d)||!object(d.target)||d.target.libraryId!==target.libraryId||d.target.kind!==target.kind||d.target.id!==target.id||!counter(d.totalCount)||!counter(d.unavailableCount)||!Array.isArray(d.entries)||d.entries.length>100||typeof d.nextCursor!=='string'||d.nextCursor.length>4096||typeof d.seed!=='string'||d.seed.length>128)bad();
  if(total!==-1&&(total!==d.totalCount||seed!==d.seed||unavailable!==d.unavailableCount))bad();
  total=d.totalCount;seed=d.seed;unavailable=d.unavailableCount;
  if(total>capacity)throw new Error(`This selection has ${total.toLocaleString()} available items; this device queue has room for ${capacity.toLocaleString()}. Use the page actions or remove queue entries first (1,000 maximum).`);
  if(d.resume!==undefined){d.resume=wireDurations(d.resume,{positionSeconds:'positionMs'});if(!object(d.resume)||!id(d.resume.itemId)||!finite(d.resume.positionSeconds))bad();if(!entries.length)startSeconds=d.resume.positionSeconds;}
  for(const raw of d.entries){const row=parseQueueItem(raw);if(seen.has(row.itemId))bad();seen.add(row.itemId);entries.push(row);}
  cursor=d.nextCursor;if(cursor){if(!d.entries.length||cursors.has(cursor))bad();cursors.add(cursor);}
 }while(cursor);
 if(entries.length!==total)bad();
 if(!entries.length)throw new Error('No available listening items were found. Check the library source or choose another item.');
 return Object.freeze({entries:Object.freeze(entries),seed,unavailableCount:unavailable,startSeconds});
}

export class ListeningService {
 private state:ListeningSnapshot=Object.freeze({eqApplied:false,levelingApplied:false,phase:'loading',preferences:null,context:null,rate:1,timer:null,timerLoading:false,stopped:false,message:null,error:null});
 private listeners=new Set<()=>void>();private disposed=false;private connected=false;private abort=new AbortController();private unsubscribe?:()=>void;private interval?:ReturnType<typeof setInterval>;
 private contextGeneration=0;private timerGeneration=0;private prefGeneration=0;private itemId='';private lastRateKey='';private lastActivity:number;private chapters?:PlayerChaptersService;private sessionKey='';
 private readonly audiobook:()=>AudiobookPreferences;
 private pausedBook?:{session:string;generation:number;item:string;intentId:number;run:number;seekRevision:number;at:number};
 /** A speed chosen in the player: for this playback only (`playbackRun`), never saved. */
 private heldRate?:{run:number;kind:string;rate:number};
 private pendingTimer?:Readonly<{revision:number;timer:ListeningTimer}>;
 private automaticFlights=new Set<object>();
 private acceptedBookNext?:Readonly<{revision:number;from:ListeningTimerOccurrence;to:ListeningTimerOccurrence}>;
 private queueOccurrence?:(sessionId:string,generation:number)=>ListeningTimerOccurrence['queue'];
 private activityRevision=0;private activityAtMs=Date.now();
 private readonly scope:QueueScope;private readonly request:ListeningRequest;private readonly playback:PlaybackService;private readonly now:()=>number;
 /** PERF-24: whether an item can be a listening item (a song or audiobook file), when the caller
  * knows; video never asks for listening details. */
 private listens?:(itemId:string,sessionId:string)=>boolean;
 constructor(scope:QueueScope,request:ListeningRequest,playback:PlaybackService,now:()=>number=()=>performance.now(),listens?:(itemId:string,sessionId:string)=>boolean,audiobook:()=>AudiobookPreferences=()=>defaultAudiobookPreferences,queueOccurrence?:(sessionId:string,generation:number)=>ListeningTimerOccurrence['queue']){this.scope=scope;this.request=request;this.playback=playback;this.now=now;this.listens=listens;this.audiobook=audiobook;this.queueOccurrence=queueOccurrence;this.lastActivity=this.now();}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(patch:Partial<ListeningSnapshot>,sync=true){if(this.disposed)return;this.state=Object.freeze({...this.state,...patch});if(sync)this.syncNativePolicy();for(const fn of this.listeners)fn();}
 private syncNativePolicy(){
  const source=this.playback.getSnapshot(),serverSource=!!source.session&&source.session.mode!=='local'&&!source.channel;
  const timer=serverSource?(this.pendingTimer?.timer??this.state.timer):null;
  return this.playback.setListeningPolicy?.({preferences:serverSource&&this.state.preferences?{bookRate:this.rateFor('audiobook_file'),passoutMinutes:this.state.preferences.passoutMinutes}:null,audiobook:this.audiobook(),
   activityRevision:this.activityRevision,activityAtMs:this.activityAtMs,timerRevision:this.timerGeneration,
   timer:timer?{choice:timer.choice,label:timer.label,...(timer.wallDeadline===undefined?{}:{wallDeadline:timer.wallDeadline}),...(timer.endSeconds===undefined?{}:{endSeconds:timer.endSeconds}),...(timer.itemId?{itemId:timer.itemId}:{}),...(timer.bookId?{bookId:timer.bookId}:{}),...(timer.sessionId?{sessionId:timer.sessionId,generation:timer.generation}:{}),...(timer.libraryId?{libraryId:timer.libraryId}:{}),...(timer.occurrence?{occurrence:timer.occurrence}:{}),...(timer.bookBoundary?{bookBoundary:timer.bookBoundary}:{})}:null});
 }
 refreshAudiobookPreferences(){if(!this.disposed)this.syncNativePolicy();}
 retryContext(){if(this.disposed)return;this.itemId='';this.publish({error:null});this.observe();}
 connect(){if(this.connected||this.disposed)return;this.connected=true;this.unsubscribe=this.playback.subscribe(()=>this.observe());this.interval=setInterval(()=>this.tick(),1000);void this.refreshPreferences();this.observe();}
 async refreshPreferences(){const g=++this.prefGeneration;this.publish({phase:'loading',error:null});try{const p=parseListeningPreferences(listeningData(await this.request(routePath("get_listening_preferences"),undefined,'GET',this.abort.signal),this.scope));if(this.disposed||g!==this.prefGeneration)return;this.publish({phase:'ready',preferences:p,error:null});this.applyRate();}catch(e){if(!this.disposed&&g===this.prefGeneration)this.publish({phase:'error',error:'Listening preferences could not be loaded. Retry before changing them.'});}}
 async savePreferences(patch:Partial<Omit<ListeningPreferences,'revision'>>){const old=this.state.preferences;if(this.disposed||!old||this.state.phase!=='ready')throw new Error('Load listening preferences before saving.');const next=parseListeningPreferences({...old,...patch});const g=++this.prefGeneration;this.publish({phase:'saving',error:null});
  try{const p=parseListeningPreferences(listeningData(await this.request(routePath("put_listening_preferences"),next,'PUT',this.abort.signal),this.scope));if(this.disposed||g!==this.prefGeneration)return;this.interact();this.publish({phase:'ready',preferences:p,error:null});this.applyRate();}
  catch(e){if(this.disposed||g!==this.prefGeneration)return;const conflict=object(e)&&e.code==='listening_preferences_conflict';this.publish({phase:'error',error:conflict?'These preferences changed on another device. Reload them before saving again.':'The preference save could not be confirmed. Reload to recover its actual result.'});throw new Error(this.state.error!);}
 }
 /** The speed for this playback, chosen in the player. Settings keep the default a new playback
  * starts at; this goes on through the queue and is forgotten when the viewer starts something new. */
 async setRate(rate:number){const context=this.state.context,p=this.state.preferences;if(!context||!['song','audiobook_file'].includes(context.kind))throw new Error('Choose a music or audiobook item first.');
  if(!p)throw new Error('Load listening preferences before changing the speed.');
  if(context.kind==='song'){if(rate!==1)throw new Error('Music plays at recorded speed.');}
  else parseListeningPreferences({...p,bookRate:rate});
  this.heldRate={run:this.run(),kind:context.kind,rate};this.interact();this.applyRate();}
 private run(){return this.playback.getSnapshot().playbackRun??0;}
 private rateFor(kind:string){if(kind!=='audiobook_file')return 1;const p=this.state.preferences,held=this.heldRate;if(held&&held.kind===kind&&held.run===this.run())return held.rate;return p?.bookRate??1;}
 interact(){if(this.disposed)return;this.lastActivity=this.now();this.activityAtMs=Date.now();this.activityRevision++;if(this.state.stopped)this.publish({stopped:false,message:null});else this.syncNativePolicy();}
 private applyRate(){const p=this.state.preferences,c=this.state.context,s=this.playback.getSnapshot();
  if(!s.session||s.session.mode==='local'||s.channel){const reset=this.lastRateKey!==''&&this.state.rate!==1;this.lastRateKey='';if(s.session?.mode==='local'||s.channel)this.heldRate=undefined;if(this.state.rate!==1)this.publish({rate:1});if(reset&&!this.playback.isNativeListening?.())this.playback.setPlaybackRate(1);return;}
  if(!p||!c||!['song','audiobook_file'].includes(c.kind)){this.lastRateKey='';if(this.state.rate!==1)this.publish({rate:1});return;}const rate=this.rateFor(c.kind);const key=`${s.session?.id??''}:${s.session?.generation??0}:${rate}`;
  if(this.state.rate!==rate)this.publish({rate});
  // A subscriber can replace playback synchronously while the rate publishes.
  const current=this.playback.getSnapshot();
  if(this.state.context!==c||current.session?.mode==='local'||current.channel||current.itemId!==s.itemId||current.session?.id!==s.session?.id||current.session?.generation!==s.session?.generation||current.playbackRun!==s.playbackRun)return;
  if(key!==this.lastRateKey){this.lastRateKey=key;if(s.session&&!s.channel&&!this.playback.isNativeListening?.())this.playback.setPlaybackRate(rate);}
 }
 private occurrence():ListeningTimerOccurrence|undefined{
  const p=this.playback.getSnapshot();if(!p.session||!p.itemId||p.channel||p.session.mode==='local')return;
  return Object.freeze({sessionId:p.session.id,generation:p.session.generation,itemId:p.itemId,playbackRun:p.playbackRun,
   queue:this.queueOccurrence?.(p.session.id,p.session.generation)??((this.pendingTimer?.timer??this.state.timer)?.occurrence?.sessionId===p.session.id&&(this.pendingTimer?.timer??this.state.timer)?.occurrence?.generation===p.session.generation?(this.pendingTimer?.timer??this.state.timer)!.occurrence!.queue:null),...(p.nativeListening?{owner:p.nativeListening.owner}:{})});
 }
 private sameOccurrence(a:ListeningTimerOccurrence|undefined,b:ListeningTimerOccurrence|undefined){return !!a&&!!b&&a.sessionId===b.sessionId&&a.generation===b.generation&&a.itemId===b.itemId&&a.playbackRun===b.playbackRun&&a.queue?.queueId===b.queue?.queueId&&a.queue?.entryId===b.queue?.entryId&&a.owner===b.owner;}
 /** A real source-transition request is in flight. A new end boundary cannot undo a write that may land. */
 beginAutomaticTransition(){const token={};this.automaticFlights.add(token);return()=>this.automaticFlights.delete(token);}
 /** Called only with the actual accepted session of a still-owned completion/commit operation. */
 acceptAutomaticTransition(next:Readonly<{id:string;itemId?:string;presentation:Readonly<{generation:number}>;queue?:Readonly<{queueId:string;entryId:string}>}>){
  const t=this.state.timer,from=this.occurrence();
  if(t?.choice!=='book'||!this.sameOccurrence(t.occurrence,from)||!from?.queue||!next.queue||next.queue.queueId!==from.queue.queueId||next.queue.entryId===from.queue.entryId)return;
  const parts=t.bookBoundary?.items,index=parts?.indexOf(from.itemId)??-1;
  if(!parts||index<0||parts[index+1]!==next.itemId)return;
  this.acceptedBookNext=Object.freeze({revision:this.timerGeneration,from,to:Object.freeze({...from,sessionId:next.id,generation:next.presentation.generation,itemId:next.itemId!,queue:Object.freeze({...next.queue})})});
 }
 /** Deliberate queue navigation retires an end boundary even if it starts the same item again. */
 cancelBoundaryTimer(){if((this.pendingTimer?.timer??this.state.timer)?.occurrence)this.clearTimer();}
 private observe(){if(this.disposed)return;const p=this.playback.getSnapshot();let item=!p.session||p.channel||p.session.mode==='local'?'':p.itemId??'';
  const eqApplied=p.audioEffects.eqApplied,levelingApplied=p.audioEffects.levelingApplied;
  if(this.state.eqApplied!==eqApplied||this.state.levelingApplied!==levelingApplied)this.publish({eqApplied,levelingApplied});
  // The item ID can precede a local session; never request source policy until its session exists.
  const sessionKey=`${p.session?.id??''}:${p.session?.generation??0}`;
  if(item&&this.state.context?.itemId===item&&this.state.context.kind==='audiobook_file'&&p.phase==='ready'&&p.session&&!this.playback.isNativeListening?.()){
   if(p.intent==='paused'&&this.pausedBook?.session!==p.session.id)this.pausedBook={session:p.session.id,generation:p.session.generation,item,intentId:p.intentId,run:p.playbackRun,seekRevision:p.seekRevision,at:this.now()};
   else if(p.intent==='playing'&&this.pausedBook){const paused=this.pausedBook;this.pausedBook=undefined;if(paused.session===p.session.id&&paused.generation===p.session.generation&&paused.item===item&&paused.intentId===p.intentId&&paused.run===p.playbackRun&&paused.seekRevision===p.seekRevision){
    const rewind=audiobookResumeRewind(this.now()-paused.at,this.audiobook().resumeRewindMs);
    if(!this.disposed&&this.playback.getSnapshot()===p&&rewind>0&&!p.pendingSeek&&!p.failedSeek){const toSeconds=Math.max(0,p.positionSeconds-rewind);if(toSeconds<p.positionSeconds){this.playback.seek(toSeconds);this.publish({rewind:Object.freeze({seconds:p.positionSeconds-toSeconds,fromSeconds:p.positionSeconds,toSeconds,messageKey:'listening.rewound'})});}}
   }}
  }else this.pausedBook=undefined;
  if(this.sessionKey!==sessionKey)this.sessionKey=sessionKey;
  const boundary=this.pendingTimer?.timer??this.state.timer,actual=this.occurrence();
  if(boundary?.occurrence&&!this.sameOccurrence(boundary.occurrence,actual)){
   const accepted=this.acceptedBookNext;
   // The native owner alone carries its book chain while JS sleeps. A new run/owner never inherits it.
   const nativeBook=boundary.choice==='book'&&!!actual?.owner&&actual.owner===boundary.occurrence.owner&&actual.playbackRun===boundary.occurrence.playbackRun;
   if(boundary.choice==='book'&&accepted?.revision===this.timerGeneration&&this.sameOccurrence(accepted.from,boundary.occurrence)&&this.sameOccurrence(accepted.to,actual)){
    this.acceptedBookNext=undefined;this.publish({timer:Object.freeze({...boundary,occurrence:actual})});
   }else if(!nativeBook)this.clearTimer();
  }
  if(item!==this.itemId){const old=this.state.context;this.itemId=item;const generation=++this.contextGeneration;this.publish({context:null,rewind:undefined});
   if(!item){this.clearTimer();this.publish({stopped:false,message:null});}
   else if(this.listens&&!this.listens(item,p.session?.id??'')){this.clearTimer();}
   else void this.request(encodedRoutePath("get_items_id_listening", {"id": encodeURIComponent(item)}),undefined,'GET',this.abort.signal).then(raw=>{if(this.disposed||generation!==this.contextGeneration)return;const context=parseItem(listeningData(raw,this.scope),item);const t=this.state.timer;
    if(!['song','audiobook_file'].includes(context.kind)||t?.choice==='book'&&t.bookId!==context.bookId||t?.choice==='item'&&t.itemId!==item||old&&old.libraryId!==context.libraryId)this.clearTimer();
    this.publish({context});this.applyRate();this.tick();
   }).catch(()=>{if(!this.disposed&&generation===this.contextGeneration)this.publish({error:'Listening details could not be loaded. Retry listening details; book and chapter timers were not inferred.'});});
  }
  this.applyRate();this.tick();
 }
 clearTimer(message:string|null=null){++this.timerGeneration;this.pendingTimer=undefined;this.acceptedBookNext=undefined;this.chapters?.dispose();this.chapters=undefined;this.publish({timer:null,timerLoading:false,message});}
 private async installTimer(timer:ListeningTimer,context?:SessionChangeContext){
  requireSessionChangeCurrent(context);
  if(timer.occurrence&&this.automaticFlights.size)throw new Error('Playback is already moving to another item. Choose the timer again.');
  const revision=++this.timerGeneration;this.pendingTimer=Object.freeze({revision,timer});
  // A closing opening can retire only this still-pending install. It cannot
  // clear a newer timer, nor an already accepted timer after this call returns.
  const retire=()=>{if(!this.disposed&&revision===this.timerGeneration&&this.pendingTimer?.revision===revision)this.clearTimer();};
  context?.signal?.addEventListener('abort',retire,{once:true});
  try{
   // Notify first, then recheck the opening before dispatch: a synchronous
   // listener may close the sheet or install a newer timer from that update.
   requireSessionChangeCurrent(context);this.publish({timer:null,timerLoading:true},false);
   requireSessionChangeCurrent(context);if(this.disposed||revision!==this.timerGeneration)return;
   const accepted=await this.syncNativePolicy();
   requireSessionChangeCurrent(context);
   if(this.disposed||revision!==this.timerGeneration)return;
   this.pendingTimer=undefined;
   const native=this.playback.getSnapshot().nativeListening;
   if(accepted!==false&&native?.expiredTimerRevision===revision){this.publish({timer:null,timerLoading:false,stopped:native.timerStopped===true});return;}
   if(accepted===false||timer.occurrence&&!this.sameOccurrence(timer.occurrence,this.occurrence())){
    this.clearTimer();throw new Error('The playback source changed before the timer was accepted.');
   }
   this.publish({timer,timerLoading:false});
  }catch(error){retire();throw error;}finally{context?.signal?.removeEventListener('abort',retire);}
 }
 async setTimer(choice:SleepChoice,expected?:ListeningTimerOccurrence,context?:SessionChangeContext){
  const captured=expected??this.occurrence(),original=captured?Object.freeze({...captured,queue:captured.queue?Object.freeze({...captured.queue}):null}):undefined;
  context=context?Object.freeze({signal:context.signal,isCurrent:context.isCurrent}):undefined;
  const check=()=>{requireSessionChangeCurrent(context);if(this.disposed||original&&!this.sameOccurrence(original,this.occurrence()))throw Object.assign(new Error('Listening timer source retired'),{name:'AbortError'});};
  check();this.interact();check();this.clearTimer();check();if(choice==='off')return;
  const p=this.playback.getSnapshot(),c=this.state.context;if(!c||!p.session||!original||p.channel||!['song','audiobook_file'].includes(c.kind))throw new Error('Start a music or audiobook item before setting a timer.');
  if(typeof choice==='number'){if(![15,30,45,60,90].includes(choice))throw new Error('Invalid timer choice.');check();await this.installTimer(Object.freeze({choice,label:`${choice} minutes`,deadline:this.now()+choice*60000,wallDeadline:Date.now()+choice*60000,libraryId:c.libraryId}),context);return;}
  if(choice==='item'){check();await this.installTimer(Object.freeze({choice,label:'End of this item',itemId:c.itemId,libraryId:c.libraryId,occurrence:original}),context);return;}
  if(choice==='book'){
   if(!c.bookId||!c.lastBookItemId||c.bookEndAvailable!==true)throw new Error('The final book part is unavailable. Choose a minute, item or verified chapter timer instead.');
   const bookId=c.bookId;
   const occurrence=original,revision=this.timerGeneration;
   if(!occurrence?.queue)throw new Error('Start the ordered book queue before choosing its end.');
   const items:string[]=[],seen=new Set<string>(),cursors=new Set<string>();let cursor='',timeline:BookTimeline|undefined;
   this.publish({timerLoading:true});
   try{do{
    check();const page=await listeningRead([this.abort.signal,context?.signal],signal=>readBookTimeline(this.request,this.scope,bookId,{cursor,limit:100,signal}));check();
    if(this.disposed||revision!==this.timerGeneration)return;
    if(!this.sameOccurrence(occurrence,this.occurrence())){this.clearTimer();throw new Error('The playback source changed. Choose the timer again.');}
    if(page.libraryId!==c.libraryId||page.totalCount>1000||timeline&&(page.revision.catalog!==timeline.revision.catalog||page.revision.viewer!==timeline.revision.viewer||page.totalCount!==timeline.totalCount))throw new Error('Book order changed. Choose the timer again.');
    timeline=page;
    for(const part of page.parts){if(seen.has(part.itemId))throw new Error('Book order changed. Choose the timer again.');seen.add(part.itemId);items.push(part.itemId);if(items.length>page.totalCount)throw new Error('Book order changed. Choose the timer again.');}
    cursor=page.nextCursor;if(cursor){if(!page.parts.length||cursors.has(cursor))throw new Error('Book order changed. Choose the timer again.');cursors.add(cursor);}
   }while(cursor);
   if(!timeline||items.length!==timeline.totalCount||!items.includes(c.itemId)||items.at(-1)!==c.lastBookItemId)throw new Error('The final book part is unavailable.');
   check();await this.installTimer(Object.freeze({choice,label:'End of this book',bookId:c.bookId,itemId:c.lastBookItemId,libraryId:c.libraryId,occurrence,bookBoundary:Object.freeze({revision:timeline.revision,items:Object.freeze(items)})}),context);return;
   }catch(error){if(!this.disposed&&revision===this.timerGeneration)this.clearTimer();throw error;}
  }
  if(choice!=='chapter')throw new Error('Invalid timer choice.');
  const generation=this.timerGeneration,position=p.positionSeconds,sessionId=p.session.id,sessionGeneration=p.session.generation;
  const chapters=this.chapters=new PlayerChaptersService({scope:{serverId:this.scope.serverId,viewerId:JSON.stringify([this.scope.authority,this.scope.accountId,this.scope.profileId])},api:{request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{check();return listeningRead([this.abort.signal,context?.signal,signal],joined=>this.request(path,body,method as 'GET',joined)) as Promise<T>;}}});
  this.publish({timerLoading:true});
  try{check();await chapters.select({libraryId:c.libraryId,itemId:c.itemId,sessionId,sessionGeneration});for(let page=0;page<41;page++){
    check();if(this.disposed||generation!==this.timerGeneration)return;const state=chapters.getSnapshot(),data=state.data;if(state.phase!=='ready'||!data||data.status!=='available')throw new Error(state.error?.message??'Verified chapter timing is unavailable for this source.');
    const chapter=data.chapters.find(ch=>position>=ch.startSeconds&&position<ch.endSeconds);
    if(chapter){const latest=this.playback.getSnapshot();if(latest.session?.id!==sessionId||latest.session.generation!==sessionGeneration||latest.pendingSeek||latest.failedSeek||latest.positionSeconds<chapter.startSeconds||latest.positionSeconds>=chapter.endSeconds)throw new Error('Playback moved to another chapter. Choose the timer again.');check();await this.installTimer(Object.freeze({choice,label:`End of chapter: ${chapter.title}`,itemId:c.itemId,sessionId,generation:sessionGeneration,endSeconds:chapter.endSeconds,libraryId:c.libraryId,occurrence:original}),context);return;}
    if(!data.nextCursor)break;await chapters.next();
   }throw new Error('No verified chapter contains the current position.');
  }catch(e){if(!sessionChangeCurrent(context)){if(!this.disposed&&generation===this.timerGeneration)this.clearTimer();throw new DOMException('Playback change retired','AbortError');}if(!this.disposed&&generation===this.timerGeneration){this.publish({timerLoading:false,error:e instanceof Error?e.message:'Chapter timer unavailable.'});throw e;}}
  finally{chapters.dispose();if(this.chapters===chapters)this.chapters=undefined;}
 }
 /** Called before natural advancement, and on foreground ticks/observations. */
 tick(){if(this.disposed)return;const p=this.playback.getSnapshot(),c=this.state.context;
  if(!p.session||p.session.mode==='local'||p.channel)return;
  if(p.nativeListening){
   const native=p.nativeListening;
   if(native.timerStopped&&!this.state.stopped){this.publish({stopped:true,message:'The listening timer paused playback.'});}
   if(!native.timerStopped&&this.state.stopped)this.publish({stopped:false,message:null});
   if(this.state.timer&&native.expiredTimerRevision===this.timerGeneration){this.publish({timer:null,timerLoading:false});}
   return;
  }
  if(this.state.stopped)return;if(!c||p.channel||!p.session||!['song','audiobook_file'].includes(c.kind))return;
  const t=this.state.timer;let reason:string|null=null;
  if(t?.deadline!==undefined&&(this.now()>=t.deadline||t.wallDeadline!==undefined&&Date.now()>=t.wallDeadline))reason='Sleep timer ended.';
  if(t?.choice==='chapter'&&t.sessionId===p.session.id&&t.generation===p.session.generation&&!p.pendingSeek&&p.positionSeconds>=t.endSeconds!)reason='The selected chapter ended.';
  if(t&&(t.choice==='item'||t.choice==='book')&&t.itemId===c.itemId&&p.phase==='ended')reason=t.choice==='book'?'The book ended.':'The selected item ended.';
  const minutes=this.state.preferences?.passoutMinutes??0;if(minutes&&p.intent==='playing'&&this.now()-this.lastActivity>=minutes*60000)reason='Playback paused after the selected period without interaction.';
  if(reason){this.clearTimer();this.publish({stopped:true,message:reason});this.playback.pause();}
 }
 /** Prebuffering may span minute timers, but never accept an edge beyond a
  * selected item/chapter/book end or a deadline that precedes that edge. */
 mayTransition(remainingSeconds:number){
  if(!this.mayAdvance())return false;
  const t=this.pendingTimer?.timer??this.state.timer,p=this.playback.getSnapshot();
  if(!p.session||p.session.mode==='local'||p.channel)return Number.isFinite(remainingSeconds)&&remainingSeconds>=0;
  if(t?.choice==='chapter'||t&&(t.choice==='item'||t.choice==='book')&&t.itemId===p.itemId)return false;
  if(!Number.isFinite(remainingSeconds)||remainingSeconds<0)return false;
  if(t?.deadline!==undefined&&this.now()+remainingSeconds*1000>=t.deadline||t?.wallDeadline!==undefined&&Date.now()+remainingSeconds*1000>=t.wallDeadline)return false;
  const minutes=this.state.preferences?.passoutMinutes??0;
  return !minutes||this.now()+remainingSeconds*1000<this.lastActivity+minutes*60000;
 }
 /** Listening queues continue unless stopped by a timer or awaiting their item's context.
  * Loading preferences does not decide whether an album or audiobook part continues. */
 mayAdvance(){this.tick();return !this.pendingTimer?.timer.occurrence&&!this.disposed&&!this.state.stopped&&(!this.itemId||!!this.state.context);}
 dispose(){if(this.disposed)return;this.disposed=true;this.pendingTimer=undefined;this.acceptedBookNext=undefined;this.automaticFlights.clear();this.abort.abort();this.unsubscribe?.();clearInterval(this.interval);this.chapters?.dispose();this.state=Object.freeze({...this.state,phase:'disposed',eqApplied:false,levelingApplied:false,preferences:null,context:null,timer:null,stopped:false});this.listeners.clear();}
}

export async function readListeningItem(request:ListeningRequest,scope:QueueScope,itemId:string,signal?:AbortSignal){return parseItem(listeningData(await request(encodedRoutePath("get_items_id_listening", {"id": encodeURIComponent(itemId)}),undefined,'GET',signal),scope),itemId);}
export function listeningItemInput(itemId:string,target?:ListeningTarget):QueueItemInput {
 const source=target&&['artist','album','book'].includes(target.kind)?{kind:target.kind as 'artist'|'album'|'book',id:target.id}:{kind:'item' as const,id:itemId};
 return parseQueueItem({itemId,editionId:null,partId:null,sourceContext:{...source,revision:null,entryId:null}});
}

/** Read-only entity navigation must remain available before a playback controller connects. */
export async function readListeningContext(request:ListeningRequest,scope:SavedScope,itemId:string,signal?:AbortSignal):Promise<ListeningItem>{
 const raw=await request(encodedRoutePath("get_items_id_listening", {"id": encodeURIComponent(itemId)}),undefined,'GET',signal);
 if(!object(raw)||!object(raw.scope)||raw.scope.serverId!==scope.serverId||JSON.stringify([raw.scope.authority,raw.scope.accountId,raw.scope.profileId])!==scope.viewerId)bad();
 return parseItem(raw.data,itemId);
}

export function audiobookResumeRewind(pauseMs:number,rewindMs=0):number{if(![0,5000,15000,30000].includes(rewindMs))throw new Error('invalid_audiobook_preference');return pauseMs>=300000?rewindMs/1000:0;}

/** Nested book times stay explicit integer milliseconds. They never pass through
 * the top-level legacy display-duration conversion. */
export type BookProgress=Readonly<{bookId:string;itemId:string;partPositionMs:number;positionMs?:number;durationMs?:number;remainingMs?:number;chapter?:Readonly<{index:number;title:string;startMs:number;endMs:number}>}>;
export function parseBookProgress(v:unknown):BookProgress{
 if(!object(v)||!id(v.bookId)||!id(v.itemId)||!counter(v.partPositionMs))bad();
 const out:{-readonly[K in keyof BookProgress]:BookProgress[K]}={bookId:v.bookId,itemId:v.itemId,partPositionMs:v.partPositionMs};
 for(const k of ['positionMs','durationMs','remainingMs'] as const){if(v[k]!==undefined){if(!counter(v[k]))bad();out[k]=v[k];}}
 if(out.remainingMs!==undefined&&(out.durationMs===undefined||out.positionMs===undefined||out.remainingMs!==Math.max(0,out.durationMs-out.positionMs)))bad();
 if(out.durationMs!==undefined&&out.positionMs!==undefined&&out.positionMs>out.durationMs)bad();
 if(v.chapter!==undefined){const c=v.chapter;if(!object(c)||!counter(c.index)||typeof c.title!=='string'||c.title.length>2048||/[\x00-\x1f\x7f]/.test(c.title)||!counter(c.startMs)||!counter(c.endMs)||c.endMs<=c.startMs||out.partPositionMs<c.startMs||out.partPositionMs>=c.endMs)bad();out.chapter=Object.freeze({index:c.index,title:c.title,startMs:c.startMs,endMs:c.endMs});}
 return Object.freeze(out);
}
export type BookTimelinePart=Readonly<{itemId:string;title:string;discNumber?:number;partNumber?:number;offsetMs?:number;durationMs?:number}>;
export type BookTimeline=Readonly<{bookId:string;libraryId:string;revision:Readonly<{catalog:number;viewer:number}>;parts:readonly BookTimelinePart[];totalCount:number;nextCursor:string;durationMs?:number;progress?:BookProgress;seek?:Readonly<{itemId:string;startMs:number}>}>;
export function parseBookTimeline(v:unknown,bookId:string):BookTimeline{
 if(!object(v)||v.bookId!==bookId||!id(v.libraryId)||!object(v.revision)||!counter(v.revision.catalog)||!counter(v.revision.viewer)||!counter(v.totalCount)||!Array.isArray(v.parts)||v.parts.length>100||v.parts.length>v.totalCount||typeof v.nextCursor!=='string'||v.nextCursor.length>4096)bad();
 const ids=new Set<string>();let previousEnd:number|undefined;
 const parts=v.parts.map(raw=>{if(!object(raw)||!id(raw.itemId)||ids.has(raw.itemId)||typeof raw.title!=='string'||raw.title.length>2048||/[\x00-\x1f\x7f]/.test(raw.title))bad();ids.add(raw.itemId);const p:{-readonly[K in keyof BookTimelinePart]:BookTimelinePart[K]}={itemId:raw.itemId,title:raw.title};for(const k of ['discNumber','partNumber','offsetMs','durationMs'] as const){if(raw[k]!==undefined){if(!counter(raw[k]))bad();p[k]=raw[k];}}if(previousEnd!==undefined&&p.offsetMs!==undefined&&p.offsetMs<previousEnd)bad();previousEnd=p.offsetMs!==undefined&&p.durationMs!==undefined?p.offsetMs+p.durationMs:undefined;return Object.freeze(p);});
 const out:{-readonly[K in keyof BookTimeline]:BookTimeline[K]}={bookId,libraryId:v.libraryId,revision:Object.freeze({catalog:v.revision.catalog,viewer:v.revision.viewer}),parts:Object.freeze(parts),totalCount:v.totalCount,nextCursor:v.nextCursor};
 if(v.durationMs!==undefined){if(!counter(v.durationMs))bad();out.durationMs=v.durationMs;}
 if(v.progress!==undefined){out.progress=parseBookProgress(v.progress);if(out.progress.bookId!==bookId)bad();}
 if(v.seek!==undefined){if(!object(v.seek)||!id(v.seek.itemId)||!counter(v.seek.startMs))bad();out.seek=Object.freeze({itemId:v.seek.itemId,startMs:v.seek.startMs});}
 return Object.freeze(out);
}
export async function readBookTimeline(request:ListeningRequest,scope:QueueScope,bookId:string,options:{cursor?:string;limit?:number;positionMs?:number;signal?:AbortSignal}={}):Promise<BookTimeline>{
 const q=new URLSearchParams();if(options.cursor)q.set('cursor',options.cursor);if(options.limit!==undefined)q.set('limit',String(options.limit));if(options.positionMs!==undefined){if(!counter(options.positionMs))bad();q.set('positionMs',String(options.positionMs));}
 const raw=await request(encodedRoutePath('get_books_id_timeline',{id:encodeURIComponent(bookId)})+(q.size?'?'+q:''),undefined,'GET',options.signal);
 return parseBookTimeline(listeningData(raw,scope),bookId);
}

/** Screen choices follow the media boundary; a file part is never labelled as a book. */
export function listeningSleepChoices(kind:'song'|'audiobook_file'):readonly SleepChoice[]{return Object.freeze(kind==='audiobook_file'?['off',15,30,45,60,90,'chapter','book']:['off',15,30,45,60,90,'item']);}
