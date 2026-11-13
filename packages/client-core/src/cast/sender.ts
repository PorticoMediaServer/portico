import {sameCastController,sameCastRemote,type CastControllerStamp,type CastRemoteIdentity} from './remote-identity.ts';
import {type CastControllerContext} from './controller-proof.ts';
/**
 * The Portico Cast sender, one implementation for every platform. The platform finds a Cast
 * device and opens a session (web: the Chrome Cast SDK picker; Apple: `PorticoCast` over a
 * GCKGenericChannel) and hands this controller a `CastTransport` for it. The controller does the
 * rest: pairing through a server-issued code, loading the title with its display metadata,
 * transport commands, track choice, the takeover prompt and being replaced by another sender.
 */
import type {MessageId} from '../../../i18n/src/index.ts';
import {serviceText} from '../presentation/service-text.ts';
import {CAST_PAIR_TIMEOUT_MS, captureCastChoices,castChoiceFields, castMetadataFields, parseCastEvent, type CastQueueSource,type CastChoices, type CastCommand, type CastEvent, type CastMetadata, type CastTrack} from './protocol.ts';

/** One open session with one receiver, over the `urn:x-cast:tv.getportico.cast` namespace. */
export interface CastTransport {
  /** The device's friendly name ("Living Room TV"). */
  readonly deviceName: string;
  /** The Cast SDK's own id for the device, when the platform gives one: two televisions with one name keep separate pairing proofs. */
  readonly deviceId?: string;
  send(message: CastCommand): Promise<void>;
  /** Receiver messages, as the SDK delivers them (JSON string or decoded object). Returns an unsubscribe. */
  onMessage(listener: (raw: unknown) => void): () => void;
  /** The session ended from the device or the SDK side. Returns an unsubscribe. */
  onSessionEnd(listener: () => void): () => void;
  /** Leave the session; `stopReceiver` also closes Portico on the TV. */
  end(stopReceiver: boolean): void;
}

/** A local command id; the server stop itself is safe to repeat. */
export function castStopKey(now = Date.now()): string {
  const random = new Uint8Array(16);
  globalThis.crypto.getRandomValues(random);
  return 'p251_' + String(now).padStart(13, '0') + '_' + Array.from(random, value => value.toString(16).padStart(2, '0')).join('');
}
export type CastApi = {createCastStopKey?():Promise<string>;request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

export type CastPhase = 'off' | 'connecting' | 'pairing' | 'waiting' | 'casting' | 'error' | 'replaced';

export type CastSnapshot = Readonly<{
  /** The platform can cast from here (set by the platform once its SDK and the server agree). */
  available: boolean;
  /** Internal accepting receipt: never a receiver-supplied source identity. */
  acceptedSource?:CastQueueSource;
  /** Retain the original source until cancellation or commit is authoritative. */
  transferPending?:boolean;
  phase: CastPhase;
  disclosureRefused?:boolean;
  /** View-only facts from the currently validated private receiver channel. */
  remoteIdentity?:CastRemoteIdentity;
  remoteStopped?:boolean;
  controlPending?:boolean;
  /** Published only after current authority and content checks. */
  reported?:Readonly<{itemId:string;versionId?:string;positionSeconds:number;durationSeconds:number;paused:boolean}>;
  deviceName?: string;
  itemId?: string;
  /** Accepted private-status version, bound to remoteIdentity. */
  versionId?: string;
  title?: string;
  positionSeconds: number;
  durationSeconds: number;
  paused: boolean;
  audioTracks: readonly CastTrack[];
  textTracks: readonly CastTrack[];
  /** A catalogue message in the viewer's language, and its ID. */
  error?: string;
  errorId?: MessageId;
  /** Someone else asked the TV to play; this controller answers (30 s without an answer is no). */
  takeover?: Readonly<{requester:string;controller?:CastControllerStamp;requestId?:string}>;
}>;

export type CastControllerOptions = Readonly<{
  api: CastApi;
  controllerContext?:CastControllerContext;
  isCurrent?:()=>boolean;
  describe?:(itemId:string)=>Promise<string|undefined>;
  /** The server origin the TV should redeem its code against. Must be HTTPS: a Cast device loads nothing else. */
  origin: string;
  /** How this sender introduces itself on the TV ("Chrome on Mac", "Sam’s iPhone"). No account details. */
  displayName: string;
  /** Native apps provide installation-owned secure storage; browsers use local storage. */
  controllerStorage?:Readonly<{get(key:string):string|undefined;set(key:string,value:string):void}>;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
}>;

class CastFailure extends Error {
  readonly messageId: MessageId;
  constructor(id: MessageId) { super(id); this.messageId = id; }
}

const idle: CastSnapshot = Object.freeze({available: false, phase: 'off', positionSeconds: 0, durationSeconds: 0, paused: false, audioTracks: Object.freeze([]), textTracks: Object.freeze([])});

/** One controller on one private SDK channel. A fresh one-use code establishes
 * authority on every attachment; server-owned tokens never cross that channel. */
export class CastController {
 private readonly api:CastApi;private readonly origin:string;private readonly displayName:string;
 private readonly callerCurrent:()=>boolean;private readonly describe?:CastControllerOptions['describe'];
 private readonly setTimer:NonNullable<CastControllerOptions['setTimer']>;private readonly clearTimer:NonNullable<CastControllerOptions['clearTimer']>;
 private state:CastSnapshot=idle;private listeners=new Set<()=>void>();private transport?:CastTransport;private detach:(()=>void)[]=[];
 private statusRevision=0;private adoption?:{finish:(ok:boolean)=>void};private generation=0;private disposed=false;private sequence=0;private controller?:CastControllerStamp;
 private waiting?:{resolve:()=>void;reject:(error:Error)=>void;timer:unknown};
 private loads=new Map<string,{resolve:(item:string)=>void;reject:(error:Error)=>void;timer:unknown}>();
 private stopping?:{requestId:string;target:CastRemoteIdentity;timer:unknown};
 constructor(options:CastControllerOptions){this.api=options.api;this.origin=new URL(options.origin).origin;this.displayName=options.displayName.slice(0,64);this.callerCurrent=options.isCurrent??(()=>true);this.describe=options.describe;this.setTimer=options.setTimer??((fn,ms)=>setTimeout(fn,ms));this.clearTimer=options.clearTimer??(timer=>clearTimeout(timer as ReturnType<typeof setTimeout>));}
 private current(){return !this.disposed&&this.callerCurrent();}
 getSnapshot=()=>this.state;
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 private publish(patch:Partial<CastSnapshot>){if(!this.current())return;this.state=Object.freeze({...this.state,...patch});for(const listener of [...this.listeners])listener();}
 setAvailable(available:boolean){this.publish({available});}
 connecting(title?:string){this.publish({phase:'connecting',title,error:undefined,errorId:undefined});}
 cancelConnecting(){if(this.state.phase==='connecting')this.publish({phase:'off'});}
 private fail(id:MessageId){this.publish({phase:'error',errorId:id,error:serviceText(id),controlPending:false});}
 private release(end=false){++this.generation;++this.statusRevision;this.adoption?.finish(false);for(const off of this.detach.splice(0))off();const transport=this.transport;this.transport=undefined;const proof=this.controller;this.controller=undefined;
  if(this.waiting){this.clearTimer(this.waiting.timer);this.waiting.reject(new CastFailure('cast.error.noAnswer'));this.waiting=undefined;}
  for(const [requestId,load] of this.loads){if(proof)void transport?.send({type:'cancel-load',requestId,controller:proof}).catch(()=>{});this.clearTimer(load.timer);load.reject(new CastFailure('cast.error.loadFailed'));}this.loads.clear();
  if(this.stopping){this.clearTimer(this.stopping.timer);this.stopping=undefined;}if(end)transport?.end(false);
 }
 private attach(transport:CastTransport){this.release();this.transport=transport;const generation=this.generation;
  this.detach.push(transport.onMessage(raw=>{if(this.current()&&this.transport===transport&&this.generation===generation){const event=parseCastEvent(raw);if(event)this.receive(event);}}));
  this.detach.push(transport.onSessionEnd(()=>{if(this.transport===transport){this.release();this.publish({phase:'off',remoteIdentity:undefined,reported:undefined,controlPending:false});}}));
  return generation;
 }
 private async pair(transport:CastTransport,generation:number,source?:CastQueueSource){
  const result=await this.api.request<{code?:unknown}>('/v1/cast/codes','POST',source?{sourceSessionId:source.sessionId}:undefined);
  if(!this.current()||this.transport!==transport||this.generation!==generation)throw new CastFailure('cast.error.noAnswer');
  if(typeof result?.code!=='string'||!result.code)throw new CastFailure('cast.error.noCode');
  await new Promise<void>((resolve,reject)=>{const timer=this.setTimer(()=>{if(this.waiting?.timer===timer){this.waiting=undefined;reject(new CastFailure('cast.error.noAnswer'));}},CAST_PAIR_TIMEOUT_MS);this.waiting={resolve,reject,timer};
   void transport.send({type:'pair',code:result.code as string,origin:this.origin,displayName:this.displayName}).catch(error=>{if(this.waiting?.timer===timer){this.clearTimer(timer);this.waiting=undefined;reject(error);}});
  });
 }
 async start(transport:CastTransport,itemId:string,meta:CastMetadata,startSeconds:number,choices?:CastChoices):Promise<boolean>{
  if(!this.current())return false;const generation=this.attach(transport),captured=captureCastChoices(choices);
  this.publish({phase:'pairing',deviceName:transport.deviceName,title:meta.title,remoteIdentity:undefined,reported:undefined,acceptedSource:undefined,transferPending:!!captured?.source,disclosureRefused:false,error:undefined,errorId:undefined});
  try{await this.pair(transport,generation,captured?.source);if(!this.current()||this.generation!==generation)return false;
   const requestId='load-'+(++this.sequence);
   const actual=await new Promise<string>((resolve,reject)=>{const timer=this.setTimer(()=>{this.loads.delete(requestId);reject(new CastFailure('cast.error.loadFailed'));},CAST_PAIR_TIMEOUT_MS);this.loads.set(requestId,{resolve,reject,timer});void transport.send({type:'load',requestId,itemId,startSeconds:Math.max(0,startSeconds),...castMetadataFields(meta),...castChoiceFields(captured),...(this.controller?{controller:this.controller}:{})}).catch(error=>{const held=this.loads.get(requestId);if(held){this.clearTimer(held.timer);this.loads.delete(requestId);reject(error);}});});
   if(!this.current()||this.generation!==generation||actual!==itemId)throw new CastFailure('cast.error.loadFailed');
   this.publish({phase:'casting',itemId:actual,positionSeconds:Math.max(0,startSeconds),paused:false,acceptedSource:captured?.source,transferPending:false});this.send({type:'status'});return true;
  }catch(error){if(this.current()&&this.generation===generation){this.release();this.fail(error instanceof CastFailure?error.messageId:'cast.error.unreachable');this.publish({transferPending:false});}return false;}
 }
 /** The SDK resumed this TV. Prove this caller again with a fresh one-use code,
  * then accept only a title that this caller can still read. */
 async adopt(transport:CastTransport,describe=this.describe,waitMs=5000):Promise<boolean>{
  if(!this.current())return false;const generation=this.attach(transport);this.publish({phase:'pairing',deviceName:transport.deviceName,disclosureRefused:false,remoteIdentity:undefined,reported:undefined});
  try{await this.pair(transport,generation);if(!this.current()||this.generation!==generation)return false;
   const adopted=await new Promise<boolean>(resolve=>{let timer:unknown;const finish=(ok:boolean)=>{off();this.clearTimer(timer);this.adoption=undefined;resolve(ok);};const off=this.subscribe(()=>{if(this.state.reported&&this.generation===generation)finish(true);});this.adoption={finish};timer=this.setTimer(()=>finish(false),waitMs);this.pendingDescribe=describe;this.send({type:'status'});});if(!adopted&&this.generation===generation){this.release();this.publish({phase:'off',disclosureRefused:true,remoteIdentity:undefined,reported:undefined});}return adopted;
  }catch{if(this.current()&&generation===this.generation){this.release();this.publish({phase:'off',disclosureRefused:true,remoteIdentity:undefined});}return false;}
 }
 private pendingDescribe?:CastControllerOptions['describe'];
 private receive(event:CastEvent){
  switch(event.type){
   case 'paired':if(!event.controller){this.waiting?.reject(new CastFailure('cast.error.refused'));return;}this.controller=event.controller;this.clearTimer(this.waiting?.timer);this.waiting?.resolve();this.waiting=undefined;return;
   case 'pair-pending':this.publish({phase:'waiting'});return;
   case 'pair-failed':this.waiting?.reject(new CastFailure(event.code==='tv_busy'?'cast.error.tvBusy':'cast.error.refused'));this.clearTimer(this.waiting?.timer);this.waiting=undefined;return;
   case 'loaded':case 'load-failed':{const held=event.requestId?this.loads.get(event.requestId):undefined;if(!held)return;this.clearTimer(held.timer);this.loads.delete(event.requestId!);if(event.type==='loaded')held.resolve(event.itemId);else held.reject(new CastFailure('cast.error.loadFailed'));return;}
   case 'replaced':this.release();this.publish({phase:'replaced',takeover:undefined,remoteIdentity:undefined,reported:undefined});return;
   case 'takeover-requested':this.publish({takeover:{requester:event.requester,...(event.controller?{controller:event.controller}:{}),...(event.requestId?{requestId:event.requestId}:{})}});return;
   case 'pair-required':case 'busy':this.publish({disclosureRefused:true,remoteIdentity:undefined,reported:undefined});return;
   case 'stopped':if(this.stopping?.requestId===event.requestId&&sameCastRemote(this.stopping.target,event.target)){this.clearTimer(this.stopping.timer);this.stopping=undefined;this.publish({remoteStopped:true,controlPending:false,phase:'off'});}return;
   case 'control-failed':if(this.stopping?.requestId===event.requestId){this.clearTimer(this.stopping.timer);this.stopping=undefined;this.fail('cast.error.unreachable');}return;
   case 'status':{
    if(!this.controller||!sameCastController(this.controller,event.controller))return;
    const transport=this.transport,generation=this.generation,revision=++this.statusRevision;
    if(!event.itemId){this.publish({phase:this.stopping?'casting':'off',remoteStopped:!this.stopping,remoteIdentity:this.stopping?this.state.remoteIdentity:undefined,controlPending:!!this.stopping});return;}
    const remote=event.controller&&event.occurrence?{controller:event.controller,occurrence:event.occurrence}:undefined;
    if(!remote||remote.occurrence.itemId!==event.itemId)return;
    if(event.attemptId&&!event.paused&&event.clockKnown){const load=this.loads.get(event.attemptId);if(load){this.clearTimer(load.timer);this.loads.delete(event.attemptId);load.resolve(event.itemId);}}
    const same=sameCastRemote(remote,this.state.remoteIdentity);
    const publish=(title?:string)=>{
     if(!this.current()||this.transport!==transport||this.generation!==generation||this.statusRevision!==revision)return;
     const reported=event.clockKnown?{itemId:event.itemId,...(event.versionId?{versionId:event.versionId}:{}),positionSeconds:event.positionSeconds,durationSeconds:event.durationSeconds,paused:event.paused}:same?this.state.reported:undefined;
     this.publish({phase:'casting',itemId:event.itemId,versionId:event.versionId,...(title?{title}:{}),...(reported?{positionSeconds:reported.positionSeconds,durationSeconds:reported.durationSeconds,paused:reported.paused}:{positionSeconds:0,durationSeconds:0}),audioTracks:event.audioTracks,textTracks:event.textTracks,remoteIdentity:remote,remoteStopped:false,reported});
    };
    const describe=this.pendingDescribe??this.describe;
    if(describe)void describe(event.itemId).then(title=>{if(title)publish(title);else if(this.generation===generation&&this.statusRevision===revision)this.publish({disclosureRefused:true,remoteIdentity:undefined,reported:undefined});},()=>{if(this.generation===generation&&this.statusRevision===revision)this.publish({disclosureRefused:true,remoteIdentity:undefined,reported:undefined});});else publish();return;
   }
  }
 }
 private send(message:CastCommand){if(this.current())void this.transport?.send(message).catch(()=>this.fail('cast.error.unreachable'));}
 private target(expected?:CastRemoteIdentity){return this.current()&&!!this.state.remoteIdentity&&(!expected||sameCastRemote(expected,this.state.remoteIdentity));}
 answerTakeover(allow:boolean,expected=this.state.takeover){if(expected!==this.state.takeover||!expected)return;this.send({type:'takeover',allow});this.publish({takeover:undefined});}
 play(expected?:CastRemoteIdentity){if(this.target(expected))this.send({type:'play',target:expected??this.state.remoteIdentity});}
 pause(expected?:CastRemoteIdentity){if(this.target(expected))this.send({type:'pause',target:expected??this.state.remoteIdentity});}
 toggle(expected?:CastRemoteIdentity){if(this.state.paused)this.play(expected);else this.pause(expected);}
 seek(positionSeconds:number,expected?:CastRemoteIdentity){if(this.target(expected)&&Number.isFinite(positionSeconds))this.send({type:'seek',positionSeconds:Math.max(0,positionSeconds),target:expected??this.state.remoteIdentity});}
 setAudioTrack(trackId:number,expected?:CastRemoteIdentity){if(this.target(expected)&&Number.isSafeInteger(trackId))this.send({type:'audio',trackId,target:expected??this.state.remoteIdentity});}
 setSubtitleTrack(trackId:number|null,expected?:CastRemoteIdentity){if(this.target(expected))this.send({type:'subtitles',trackIds:trackId===null?[]:[trackId],target:expected??this.state.remoteIdentity});}
 stop(expected?:CastRemoteIdentity,suppliedKey?:string){if(!this.current()||expected&&!sameCastRemote(expected,this.state.remoteIdentity))return;const target=expected??this.state.remoteIdentity;
  if(!target){this.release();this.publish({phase:'off'});return;}
  if(this.stopping)return;const requestId=suppliedKey??castStopKey();const timer=this.setTimer(()=>{if(this.stopping?.requestId===requestId){this.stopping=undefined;this.fail('cast.error.unreachable');}},35000);this.stopping={requestId,target,timer};this.publish({controlPending:true});this.send({type:'stop',requestId,target});
 }
 dismiss(){this.publish({phase:'off',error:undefined,errorId:undefined});}
 dispose(){this.release();this.disposed=true;this.listeners.clear();}
}
