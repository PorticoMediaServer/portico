import {UserMutationError, parseUserMutationReceipt, type UserMutationTerminalProof, type UserMutationScope} from '../../../../../packages/client-core/src/user-mutations';
import {nativeListeningStatus} from './listening-status';
import type {RouteSnapshot} from '../../../../../packages/client-core/src/route-connection';
import {listeningDeadline,type NativeListeningV1Context,type NativeListeningV1Control,type NativeListeningV1Event,type NativeQueueAdvance,type ListeningAction,type NativeListeningCommandTarget,type ListeningDeadlines,type NativeListeningPolicy} from '../../../../../packages/client-core/src/playback/native-listening.ts';
import {parseQueue,parseSession,parseSessionEnd} from '../../../../../packages/client-core/src/playback-v1/types.ts';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {routeOrigin} from '../../../../../packages/client-core/src/route-identity';
/** Whose listening this is: the server and viewer (the native owner scopes its Keychain records by
 * `principalKey`). */
export type ListeningPrincipal = Readonly<{origin:string;serverId:string;authority:'local'|'hosted';accountId:string;profileId:string}>;
const wireID=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
function listeningPrincipal(value:ListeningPrincipal):ListeningPrincipal{
 const url=new URL(value.origin);
 if(url.username||url.password||url.search||url.hash||url.pathname!=='/'||!['http:','https:'].includes(url.protocol)||!wireID(value.serverId)||!wireID(value.accountId)||!wireID(value.profileId)||!['local','hosted'].includes(value.authority))throw new Error('This device’s listening owner could not be verified.');
 routeOrigin(value.origin);
 return Object.freeze({origin:url.origin,serverId:value.serverId,authority:value.authority,accountId:value.accountId,profileId:value.profileId});
}
const principalKey=(p:ListeningPrincipal)=>JSON.stringify([p.origin,p.serverId,p.authority,p.accountId,p.profileId]);
export interface NativeListeningBridge {
 listeningRoute?(owner:string,route:{ready:boolean;origin:string|null;revision:number}):Promise<unknown>;
 listeningSnapshot(owner:string):Promise<unknown>;
 acknowledgeListening(owner:string,sequence:number):Promise<unknown>;
 listeningAction(owner:string,action:string,value:number|null):Promise<unknown>;
 listeningActionFor?(owner:string,expected:NativeListeningCommandTarget,action:string,value:number|null):Promise<unknown>;
 listeningDeadlines(owner:string,deadlines:ListeningDeadlines):Promise<unknown>;
 listeningCredentials(owner:string,token:string):Promise<unknown>;
 listeningPolicy?(owner:string,policy:NativeListeningPolicy):Promise<unknown>;
 retireListening(owner:string,stop:boolean):Promise<unknown>;
 /** Playback v1 (plan §9.3 C3). */
 adoptListeningSession?(context:Record<string,unknown>):Promise<unknown>;
 listeningAdvance?(owner:string,reason:string,entryId:string|null):Promise<unknown>;
 listeningSessionUpdated?(owner:string,sessionId:string):Promise<unknown>;
}
// Keep the bridge capability bounds aligned with the Apple and Android producers.
const nativeListeningRates: readonly number[] = [.5,.75,1,1.25,1.5,1.75,2,2.5,3];
/** The checks every native listening snapshot passes. */
function checkNativeStatus(raw:any,owner:string){
 if(!raw||raw.owner!==owner||!Number.isSafeInteger(raw.sequence)||raw.sequence<1||!['playing','paused'].includes(raw.intent)||!Number.isFinite(raw.positionSeconds)||raw.positionSeconds<0||!Number.isFinite(raw.rate)||raw.rate<=0||!Number.isSafeInteger(raw.commandRevision)||raw.commandRevision<0||typeof raw.route!=='string'||raw.route.length>4096||![raw.observed,raw.ready,raw.seeking,raw.ended,raw.terminal].every(v=>typeof v==='boolean')||raw.error!==null&&typeof raw.error!=='string'||!listeningDeadline(raw.deadlines?.sleepAtMs)||!listeningDeadline(raw.deadlines?.passoutAtMs))throw new Error('Invalid native listening snapshot.');
 for(const key of ['rendering','eqApplied','levelingApplied','timerStopped','rateWaiting'] as const)if(raw[key]!==undefined&&typeof raw[key]!=='boolean')throw new Error('Invalid native render status.');
 if(raw.levelingGainDb!==undefined&&(typeof raw.levelingGainDb!=='number'||!Number.isFinite(raw.levelingGainDb)||raw.levelingGainDb<-60||raw.levelingGainDb>30))throw new Error('Invalid native applied gain.');
 if(raw.effectiveRate!==undefined&&(!Number.isFinite(raw.effectiveRate)||raw.effectiveRate<0||raw.effectiveRate>3))throw new Error('Invalid native effective rate.');
 if(raw.supportedRates!==undefined&&(!Array.isArray(raw.supportedRates)||raw.supportedRates.length>nativeListeningRates.length||raw.supportedRates.some((rate:unknown)=>typeof rate!=='number'||!nativeListeningRates.includes(rate))))throw new Error('Invalid native supported rates.');
 if(raw.rateError!==undefined&&(typeof raw.rateError!=='string'||raw.rateError.length>4096))throw new Error('Invalid native rate error.');
 for(const key of ['expiredTimerRevision','policyRevision'] as const)if(raw[key]!==undefined&&(!Number.isSafeInteger(raw[key])||raw[key]<(key==='expiredTimerRevision'?-1:0)))throw new Error('Invalid native policy revision.');
}
/**
 * A v1 native snapshot (plan §9.3 C3): the status every owner reports, plus the v1 session the
 * owner reports for now. A different session than JS knows must come with a `transition` from the
 * session JS knows (or from the root of transitions JS has not acknowledged yet).
 */
export function parseNativeListeningV1Event(raw:any,owner:string,sessionId:string,ancestor?:string):NativeListeningV1Event {
 checkNativeStatus(raw,owner);
 const session=parseSession(raw.session);
 if(session.kind!=='audio')throw new Error('Not a native audio session.');
 let transition:NativeListeningV1Event['transition'];
 if(raw.transition!==undefined&&raw.transition!==null){
  const previous=raw.transition.previousSessionId;
  if(typeof previous!=='string'||previous!==sessionId&&previous!==ancestor&&session.id!==sessionId)throw new Error('Native queue handover did not match.');
  transition=Object.freeze({previousSessionId:previous,queue:raw.transition.queue?parseQueue(raw.transition.queue):null});
 }
 if(session.id!==sessionId&&!transition)throw new Error('Native listening session changed without a queue move.');
 const queue=raw.queue===null||raw.queue===undefined?null:(typeof raw.queue?.id==='string'&&typeof raw.queue?.revision==='string'&&raw.queue.revision.length<=256?Object.freeze({id:raw.queue.id,revision:raw.queue.revision}):undefined);
 if(queue===undefined)throw new Error('Invalid native queue.');
 if(!Number.isSafeInteger(raw.lastSeq)||raw.lastSeq<0||!Number.isFinite(raw.durationSeconds)||raw.durationSeconds<0)throw new Error('Invalid native timeline.');
 if(raw.notice!==undefined&&(typeof raw.notice!=='string'||raw.notice.length>4096))throw new Error('Invalid native notice.');
 const b=raw.binding;
 if(b!==null&&(!b||!wireID(b.lease)||!Number.isSafeInteger(b.intentId)||b.intentId<1||b.sessionId!==session.id||b.generation!==session.presentation.generation||b.itemId!==session.itemId))throw new Error('Native engine binding changed.');
 const end=raw.terminal?parseSessionEnd(raw.end):undefined;
 return Object.freeze({...nativeListeningStatus(raw, serviceText),session,queue,transition,end,lastSeq:raw.lastSeq,durationSeconds:raw.durationSeconds});
}

/** Capture the caller's policy before a bridge can retain it; no later form/source mutation
 * may change the timer occurrence or ordered book boundary being acknowledged. */
function capturedPolicy(value:NativeListeningPolicy):NativeListeningPolicy {
 const t=value.timer;
 return Object.freeze({...value,effects:Object.freeze({...value.effects}),preferences:value.preferences?Object.freeze({...value.preferences}):null,
  ...(value.audiobook?{audiobook:Object.freeze({...value.audiobook})}:{}),
  timer:t?Object.freeze({...t,...(t.occurrence?{occurrence:Object.freeze({...t.occurrence,queue:t.occurrence.queue?Object.freeze({...t.occurrence.queue}):null})}:{}),
   ...(t.bookBoundary?{bookBoundary:Object.freeze({...t.bookBoundary,revision:Object.freeze({...t.bookBoundary.revision}),items:Object.freeze([...t.bookBoundary.items])})}:{})}):null});
}

/** The v1 native owner's JS side: one handover per session, then intents only. Snapshots are
 * acknowledged, which lets the owner forget a transition JS has seen. */
export class NativeListeningV1Adapter implements NativeListeningV1Control {
 private bridge:NativeListeningBridge;private principal:ListeningPrincipal;private bearer:()=>string;private id:()=>string;private subscribe:(sink:(event:unknown)=>void)=>()=>void;
 private route?:{getSnapshot:()=>RouteSnapshot;subscribe:(listener:()=>void)=>()=>void};
 private originalMutationScope?:UserMutationScope;
 private mutationClock?:()=>Promise<number>;
 private mutationScope?:()=>UserMutationScope|undefined;
 private epoch=0;private policyAttempt=0;private policy?:NativeListeningPolicy;
 private current?:{owner:string;sessionId:string;sink:(event:NativeListeningV1Event)=>void;sequence:number;last?:NativeListeningV1Event;ancestor?:string;unsubscribe:()=>void};
 constructor(options:{mutationClock?:()=>Promise<number>;mutationScope?:()=>UserMutationScope|undefined;route?:{getSnapshot:()=>RouteSnapshot;subscribe:(listener:()=>void)=>()=>void};bridge:NativeListeningBridge;principal:ListeningPrincipal;bearer:()=>string;id:()=>string;subscribe:(sink:(event:unknown)=>void)=>()=>void}){const scope=options.mutationScope?.();this.originalMutationScope=scope?Object.freeze({...scope}):undefined;this.mutationClock=options.mutationClock;this.mutationScope=options.mutationScope;this.route=options.route;this.bridge=options.bridge;this.principal=listeningPrincipal(options.principal);this.bearer=options.bearer;this.id=options.id;this.subscribe=options.subscribe;}
 async adopt(context:NativeListeningV1Context,sink:(event:NativeListeningV1Event)=>void){
  if(!this.bridge.adoptListeningSession)throw new Error('This app version can’t hand audio to the system player.');
  const playbackRun=context.playbackRun,epoch=++this.epoch,previous=this.current;this.current=undefined;
  if(previous){previous.unsubscribe();await this.bridge.retireListening(previous.owner,false);}
  if(this.epoch!==epoch)return;
  const active:NonNullable<NativeListeningV1Adapter['current']>={owner:this.id(),sessionId:context.session.id,sink,sequence:0,unsubscribe:()=>{}};this.current=active;
  active.unsubscribe=this.subscribe(raw=>{if(this.current!==active||(raw as any)?.owner!==active.owner)return;try{this.accept(active,raw);}catch(error){this.fail(active,error);}});
  try{
   const offset=this.mutationClock?await this.mutationClock():0;
   if(!Number.isSafeInteger(offset))throw new Error('mutation_clock');
   if(this.current!==active)return;
   const scope=this.mutationScope?.();
   if(this.mutationScope&&(!scope||!this.originalMutationScope||(['serverId','origin','authority','accountId','profileId','familyId','deviceId'] as const).some(key=>scope[key]!==this.originalMutationScope![key])))throw new UserMutationError('mutation_scope_changed');
   const capturedScope=scope?Object.freeze({...scope}):undefined;
   const route=this.route?.getSnapshot();
   const raw=await this.bridge.adoptListeningSession({owner:active.owner,origin:this.principal.origin,transport:route?{ready:route.phase==='ready',origin:route.origin,revision:route.revision}:undefined,principalKey:principalKey(this.principal),
    mutationClockOffsetMs:offset,mutationScope:capturedScope,session:context.session,...(playbackRun===undefined?{}:{playbackRun}),queueId:context.queueId,queueRevision:context.queueRevision,durationSeconds:context.durationSeconds,lastSeq:context.lastSeq,...(context.observerEpoch?{observerEpoch:context.observerEpoch}:{}),leaseMs:context.leaseMs,transitions:context.transitions,intent:context.intent,policy:this.policy??null,accessToken:this.bearer()});
   if(this.current!==active)return;
   this.accept(active,raw);
   if(this.route){const update=()=>{if(this.current!==active)return;const r=this.route!.getSnapshot();if(!this.bridge.listeningRoute){this.fail(active,new Error('Native route handoff is unavailable.'));return;}void this.bridge.listeningRoute(active.owner,{ready:r.phase==='ready',origin:r.origin,revision:r.revision}).catch(error=>this.fail(active,error));};const off=this.route.subscribe(update),offEvent=active.unsubscribe;active.unsubscribe=()=>{off();offEvent();};update();}
   await this.bridge.acknowledgeListening(active.owner,active.sequence);
  }catch(error){if(this.current===active)await this.detach(false);throw error;}
 }
 private accept(active:NonNullable<NativeListeningV1Adapter['current']>,raw:unknown){
  if(this.current!==active)return;
  const candidate=raw as any;
  if(candidate?.owner===active.owner&&Number.isSafeInteger(candidate.sequence)&&candidate.sequence<=active.sequence)return;
  const value=parseNativeListeningV1Event(raw,active.owner,active.sessionId,active.ancestor);
  if(value.transition)active.ancestor=value.transition.previousSessionId;
  active.sequence=value.sequence;active.sessionId=value.session.id;active.last=value;active.sink(value);
  void this.bridge.acknowledgeListening(active.owner,active.sequence).catch(()=>{});
 }
 private fail(active:NonNullable<NativeListeningV1Adapter['current']>,_error:unknown){
  if(this.current!==active)return;
  if(active.last)active.sink({...active.last,sequence:active.sequence+1,terminal:true,intent:'paused',error:serviceText('player.nativeHandoverFailed')});
  void this.detach(true);
 }
 async refresh(){const active=this.current;if(!active)return;await this.bridge.listeningCredentials(active.owner,this.bearer());if(this.current!==active)return;const raw=await this.bridge.listeningSnapshot(active.owner);if(this.current!==active)return;this.accept(active,raw);await this.bridge.acknowledgeListening(active.owner,active.sequence);}
 private commandTarget(event:NativeListeningV1Event|undefined):NativeListeningCommandTarget|undefined{
  if(!event?.binding||event.terminal)return;
  return Object.freeze({binding:Object.freeze({...event.binding}),commandRevision:event.commandRevision,
   queue:event.session.queue?Object.freeze({queueId:event.session.queue.queueId,entryId:event.session.queue.entryId}):null});
 }
 private matchesCommand(active:NonNullable<NativeListeningV1Adapter['current']>,expected:NativeListeningCommandTarget){
  const actual=this.commandTarget(active.last);
  return this.current===active&&!!actual&&actual.commandRevision===expected.commandRevision
   &&(['lease','intentId','sessionId','generation','itemId'] as const).every(key=>actual.binding[key]===expected.binding[key])
   &&actual.queue?.queueId===expected.queue?.queueId&&actual.queue?.entryId===expected.queue?.entryId;
 }
 async action(action:ListeningAction,value?:number,expected?:NativeListeningCommandTarget){
  const active=this.current,original=expected?Object.freeze({...expected,binding:Object.freeze({...expected.binding}),queue:expected.queue?Object.freeze({...expected.queue}):null}):this.commandTarget(active?.last);
  if(!active||!original||!this.matchesCommand(active,original)||!this.bridge.listeningActionFor)throw new Error('This listening output is no longer available.');
  // Pause stays local safety first. Other commands keep the original occurrence through refresh;
  // the accepting native queue repeats the same fence, including changes after this check.
  if(action!=='pause')await this.refresh();
  if(!this.matchesCommand(active,original))throw new Error('This listening output is no longer available.');
  await this.bridge.listeningActionFor(active.owner,original,action,value??null);
  if(action==='pause'&&this.current===active)await this.refresh();
 }
 async advance(reason:NativeQueueAdvance,entryId?:string){
  const active=this.current;if(!active)return;
  if(!this.bridge.listeningAdvance)throw new Error('This app version can’t move the queue from the system player.');
  await this.refresh();if(this.current===active)await this.bridge.listeningAdvance(active.owner,reason,entryId??null);
 }
 async deadlines(value:ListeningDeadlines){const active=this.current;if(!active)return;await this.refresh();if(this.current===active)await this.bridge.listeningDeadlines(active.owner,value);}
 async configure(policy:NativeListeningPolicy):Promise<boolean>{
  const active=this.current,epoch=this.epoch,attempt=++this.policyAttempt;
  const original=capturedPolicy(policy);
  // Getters can synchronously replace the owner or submit a newer policy while cloning.
  if(this.current!==active||this.epoch!==epoch||this.policyAttempt!==attempt)return false;
  this.policy=original;if(!active||!this.bridge.listeningPolicy)return false;
  const accepted=await this.bridge.listeningPolicy(active.owner,original);
  // An accepted policy may itself pause/advance playback. Keep its owner, not the live header;
  // replacement ownership or a newer policy must never acknowledge this older command.
  return accepted===true&&this.current===active&&this.epoch===epoch&&this.policyAttempt===attempt&&this.policy===original;
 }
 async detach(stop:boolean){
  ++this.epoch;const active=this.current;this.current=undefined;if(!active)return;active.unsubscribe();
  const result=await this.bridge.retireListening(active.owner,stop);
  if(stop&&result&&typeof result==='object'&&'state' in result){
   const status=result as {state:string;key?:string;code?:string;receipt?:unknown;terminalProof?:UserMutationTerminalProof;draft?:unknown};
   if(status.state!=='confirmed')throw Object.assign(new UserMutationError(status.code|| (status.state==='pending'?'mutation_pending':'mutation_needs_review'),status.key,status.receipt&&status.key?parseUserMutationReceipt(status.receipt,{key:status.key,operation:'stop_playback_session'}):undefined,undefined,status.terminalProof??undefined),{draft:status.draft});
  }
 }
 async sessionUpdated(sessionId:string){const active=this.current;if(!active||active.sessionId!==sessionId||!this.bridge.listeningSessionUpdated)return;await this.bridge.listeningSessionUpdated(active.owner,sessionId);}
}
