import {newUserMutationKey, isUserMutationKey} from '../user-mutations.ts';
import {sameCastController,sameCastRemote,type CastControllerStamp,type CastRemoteIdentity} from './remote-identity.ts';
import {castBinding,castContext,castScopeEqual,castProofKey,readCastProof,type CastControllerContext,type CastControllerBinding,type CastControllerProof} from './controller-proof.ts';
import {routeBody} from "../../../contracts/src/routes.ts";
import {encodedRoutePath} from "../../../contracts/src/route-paths.ts";
import {routePath} from "../../../contracts/src/route-paths.ts";
/**
 * The Portico Cast sender, one implementation for every platform. The platform finds a Cast
 * device and opens a session (web: the Chrome Cast SDK picker; Apple: `PorticoCast` over a
 * GCKGenericChannel) and hands this controller a `CastTransport` for it. The controller does the
 * rest: pairing through a server-issued code, loading the title with its display metadata,
 * transport commands, track choice, the takeover prompt and being replaced by another sender.
 */
import type {MessageId} from '../../../i18n/src/index.ts';
import {serviceProblem, serviceText} from '../presentation/service-text.ts';
import {CAST_PAIR_TIMEOUT_MS, CAST_PROTOCOL_VERSION, CAST_TAKEOVER_WINDOW_MS, captureCastChoices,castTransferReceipt,castChoiceFields, castMetadataFields, parseCastEvent, type CastQueueSource,type CastTransferReceipt,type CastChoices, type CastCommand, type CastEvent, type CastMetadata, type CastTrack} from './protocol.ts';

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

export type CastApi = {createUserMutationKey?():Promise<string>;request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

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

type CastTransferFlight={id?:string;transport:CastTransport;source:CastQueueSource;itemId:string;position:number;cancellation?:Promise<CastTransferReceipt|undefined>};
type CastStatusRead={transport:CastTransport|undefined;generation:number;controller:CastControllerStamp|undefined;revision:number;event:Extract<CastEvent,{type:'status'}>;remote?:CastRemoteIdentity;failures:number;pending:boolean;timer?:unknown};
export class CastController {
  private transferFlight?:CastTransferFlight;
  private recoveryTransfer?:CastTransferFlight;
  private state: CastSnapshot = idle;
  private listeners = new Set<() => void>();
  private api: CastApi;
  private origin: string;
  private displayName: string;
  private transport?: CastTransport;
  private detach: (() => void)[] = [];
  private startGeneration = 0;
  private requestSequence = 0;
  private loads = new Map<string, {resolve(itemId: string): void; reject(error: Error): void; pending(event: Extract<CastEvent,{type:'load-pending'}>):void}>();
  private waiting?: {resolve(): void; reject(e: Error): void};
  private adopting = false;
  private adoptTimer?: unknown;
  private settleAdoption?: (ok: boolean) => void;
  private takeoverTimer?: unknown;
  private setTimer: (fn: () => void, ms: number) => unknown;
  private clearTimer: (timer: unknown) => void;
  private disposed = false;
  private controllerStorage?:CastControllerOptions['controllerStorage'];
  private readonly proofContext?:CastControllerContext;
  private readonly callerCurrent:()=>boolean;
  private readonly describe?:(itemId:string)=>Promise<string|undefined>;
  private statusRevision=0;
  private statusRead?:CastStatusRead;
  private acceptedController?:CastControllerStamp;
  private renewal?:{attemptId:string;binding:CastControllerBinding;proof:CastControllerProof;transport:CastTransport;generation:number;controller:CastControllerStamp;timer:unknown};
  private expectedPairId?:string;
  private stopping?:{storageKey:string;requestId:string;target:CastRemoteIdentity;transport:CastTransport;generation:number;versionId?:string;timer:unknown};
  private current(){return !this.disposed&&this.callerCurrent();}
  /** External caller/storage ports never get to change the operation being proved. */
  private original(transport=this.transport,generation=this.startGeneration,controller=this.acceptedController){
    const pure=()=>!this.disposed&&this.transport===transport&&this.startGeneration===generation&&this.acceptedController===controller;
    return {pure,current:()=>pure()&&this.current()&&pure()};
  }
  private proof(transport:CastTransport){
    const original=this.original(),context=this.proofContext;
    if(!context||!transport.deviceId||!original.current())return;
    try{
      const raw=this.controllerStorage?.get(castProofKey(context,transport.deviceId));
      if(!original.pure())return;
      return readCastProof(raw,context,transport.deviceId);
    }catch{return;}
  }
  private async validate(binding:CastControllerBinding,attemptId:string,admit?:()=>boolean){
    const original=this.original();
    const current=()=>original.current()&&(!admit||admit())&&original.pure();
    if(!this.proofContext||!current()||!castScopeEqual(binding.scope,this.proofContext)||binding.receiverId!==this.transport?.deviceId)throw new Error('cast_scope_changed');
    // The final external current predicate is followed by the original pure
    // proof BEFORE validation IO; a same-device successor cannot borrow it.
    if(!current())throw new Error('cast_scope_changed');
    const result=await this.api.request<{binding?:unknown;attemptId?:unknown;ticket?:unknown}>(routePath('social_post_v1_cast_controllers_validate'),'POST',{binding,attemptId});
    const accepted=castBinding(result.binding);
    if(!current()||!accepted||JSON.stringify(accepted)!==JSON.stringify(binding)||result.attemptId!==attemptId||typeof result.ticket!=='string'||!/^[A-Za-z0-9_-]{43,256}$/.test(result.ticket))throw new Error('cast_validation_refused');
    return result.ticket;
  }

  constructor(options: CastControllerOptions) {
    if (!/^https:\/\//.test(options.origin)) throw new Error('Cast needs an HTTPS server origin.');
    this.proofContext=options.controllerContext?.origin===new URL(options.origin).origin?castContext(options.controllerContext):undefined;this.callerCurrent=options.isCurrent??(()=>true);this.describe=options.describe;
    this.controllerStorage=options.controllerStorage;
    if(!this.controllerStorage){try{const storage=globalThis.localStorage;if(storage)this.controllerStorage={get:k=>storage.getItem(k)??undefined,set:(k,v)=>{
      storage.setItem(k,v);const keys:string[]=[];for(let n=0;n<storage.length;n++){const key=storage.key(n);if(key?.startsWith('portico.cast.controller:v2:'))keys.push(key);}for(const key of keys.filter(key=>key!==k).slice(0,Math.max(0,keys.length-16)))storage.removeItem(key);
    }};}catch{}}
    this.api = options.api;
    this.origin = new URL(options.origin).origin;
    this.displayName = options.displayName.slice(0, 64);
    this.setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
  }

  getSnapshot = (): CastSnapshot => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };
  private publish(patch: Partial<CastSnapshot>, admit?:()=>boolean) {
    if (!this.current() || (admit && !admit())) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const fn of [...this.listeners]) fn();
  }

  /** The platform's verdict on whether Cast can be offered here. */
  setAvailable(available: boolean) { this.publish({available}); }
  /** The platform's device picker is open ("Choose a device…"). */
  connecting(title?: string) { this.publish({phase: 'connecting', remoteIdentity:undefined,remoteStopped:undefined,controlPending:false,acceptedSource:undefined,title, error: undefined, errorId: undefined, takeover: undefined}); }
  /** The picker closed without a device: not an error. */
  cancelConnecting() { if (this.state.phase === 'connecting') this.publish({phase: 'off'}); }

  /** Pair with the TV on `transport` and start `itemId` there. True once the TV has the title. */
  async start(transport: CastTransport, itemId: string, meta: CastMetadata, startSeconds: number, choices?: CastChoices): Promise<boolean> {
    if(!this.current())return false;
    if(this.recoveryTransfer || this.transferFlight?.id){
      const previous=this.recoveryTransfer ?? this.transferFlight!;
      try{const receipt=await this.cancelTransfer(previous);this.recoveryTransfer=undefined;if(this.transferFlight===previous)this.transferFlight=undefined;
        if(receipt?.state==='committed'&&receipt.itemId===previous.itemId)this.publish({acceptedSource:previous.source});
        this.publish({phase:'off',acceptedSource:undefined,transferPending:false});
      }catch{this.fail('cast.error.unreachable');}
      return false;
    }
    if(this.release(false)!==this.startGeneration)return false;
    const generation = this.startGeneration;
    choices=captureCastChoices(choices);meta=Object.freeze({...meta});
    const flight:CastTransferFlight|undefined=choices?.source?{transport,source:choices.source,itemId,position:Math.max(0,startSeconds)}:undefined;
    this.transferFlight=flight;
    this.transport = transport;
    this.detach.push(transport.onMessage(raw => { const e = parseCastEvent(raw); if (e) this.receive(e); }));
    this.detach.push(transport.onSessionEnd(() => this.sessionEnded()));
    let loading = false;
    this.publish({phase: 'pairing', disclosureRefused:false,remoteIdentity:undefined,remoteStopped:undefined,controlPending:false,reported:undefined,acceptedSource:undefined,transferPending:!!flight,deviceName: transport.deviceName, title: meta.title, itemId: undefined, error: undefined, errorId: undefined, takeover: undefined, audioTracks: [], textTracks: []});
    try {
      const displayName = transport.deviceName.trim().slice(0, 64);
      const bootstrapBody={protocolVersion:CAST_PROTOCOL_VERSION,...(transport.deviceId?{receiverId:transport.deviceId}:{}),...(displayName?{displayName}:{}),...(choices?.source?{source:choices.source}:{})};
      const issued = await this.api.request<{bootstrap?: {id?:unknown;code?: unknown}}>(routePath("social_post_v1_cast_bootstrap"), 'POST', routeBody("social_post_v1_cast_bootstrap", bootstrapBody));
      if(flight){
        const id=issued?.bootstrap?.id;
        if(typeof id!=='string'||!/^[A-Za-z0-9._:-]{1,128}$/.test(id)||!castTransferReceipt(issued,id))throw new CastFailure('cast.error.noCode');
        flight.id=id;choices=Object.freeze({...choices,queue:Object.freeze({...choices!.queue!,transferId:id})});
      }
      if (generation !== this.startGeneration) {if(flight)await this.cancelTransfer(flight);return false;}
      this.expectedPairId=typeof issued?.bootstrap?.id==='string'?issued.bootstrap.id:undefined;
      const code = issued?.bootstrap?.code;
      if (typeof code !== 'string' || !code) throw new CastFailure('cast.error.noCode');
      await this.exchange({type: 'pair', code, origin: this.origin, displayName: this.displayName});
      if (generation !== this.startGeneration) return false;
      loading = true;
      const playingItemId = await this.load({type: 'load', itemId, startSeconds: Math.max(0, startSeconds), ...castMetadataFields(meta), ...castChoiceFields(choices)});
      if (generation !== this.startGeneration) return false;
      if(flight?.id){
        const receipt=await this.transferStatus(flight.id);
        if(generation!==this.startGeneration)return false;
        if(receipt?.state!=='committed'||receipt.itemId!==itemId||playingItemId!==itemId)throw new CastFailure('cast.error.loadFailed');
      }
      if(this.transferFlight===flight)this.transferFlight=undefined;
      this.publish({phase: 'casting', acceptedSource:flight?.source,transferPending:false,itemId: playingItemId, positionSeconds: Math.max(0, startSeconds), paused: false});
      this.send({type:'status'});
      return true;
    } catch (e) {
      if (generation !== this.startGeneration) return false;
      const admitted=this.current();
      if(generation!==this.startGeneration)return false;
      // Retired callers settle locally; no Stop/recovery/SDK IO is admitted.
      if(!admitted){this.detachOriginal(false,false);return false;}
      if(flight?.id){
        // Delivery/ACK loss is uncertainty, not a refusal. Recover the same
        // committed result without stopping it. Only a real receiver failure
        // or the user's Stop takes the cancellation/authorized Stop path.
        if(!(e instanceof CastFailure && e.messageId==='cast.error.loadFailed')){
          try{
            const receipt=await this.transferStatus(flight.id);
            if(generation!==this.startGeneration)return false;
            if(!receipt)throw new CastFailure('cast.error.noAnswer');
            if(receipt.state==='committed'){
              if(receipt.itemId!==itemId)throw new CastFailure('cast.error.loadFailed');
              this.transferFlight=undefined;this.recoveryTransfer=undefined;
              this.publish({phase:'casting',acceptedSource:flight.source,transferPending:false,itemId:receipt.itemId,paused:false});
              this.send({type:'status'});return true;
            }
          }catch{
            if(generation===this.startGeneration){this.transferFlight=undefined;this.recoveryTransfer=flight;if(this.release(false)===this.startGeneration)this.fail('cast.error.unreachable');}
            return false;
          }
        }
        try{
          const receipt=await this.cancelTransfer(flight);
          if(generation===this.startGeneration){
            this.transferFlight=undefined;this.recoveryTransfer=undefined;
            this.publish({transferPending:false,...(receipt?.state==='committed'&&receipt.itemId===itemId?{acceptedSource:flight.source}:{})});
          }
        }catch{
          if(generation===this.startGeneration){this.transferFlight=undefined;this.recoveryTransfer=flight;this.fail('cast.error.unreachable');}
        }
      } else if (loading) this.send({type: 'stop'});
      if(generation!==this.startGeneration)return false;
      if(this.release(false)!==this.startGeneration)return false;
      const id: MessageId = e instanceof CastFailure ? e.messageId : 'cast.error.unreachable';
      this.publish({phase: 'error', takeover: undefined, errorId: id, error: e instanceof CastFailure ? serviceText(id) : serviceProblem(e, 'playback', id)});
      return false;
    }
  }

  /**
   * CAST-05: a session the platform resumed on its own (a page reload while the TV plays) is
   * adopted when the TV answers a status request within `waitMs`: the controller shows it as
   * casting and can control and stop it. Otherwise it is left alone (never ended, never paired
   * again). `describe` names the title from its item id. Resolves true once adopted.
   */
  async adopt(transport: CastTransport, describe = this.describe, waitMs = 5000): Promise<boolean> {
    if (this.transport || !this.current() || this.state.transferPending || this.recoveryTransfer || !describe) return false;
    const proof=this.proof(transport);if(!proof)return false;
    this.transport=transport;this.adopting=true;
    const generation=this.startGeneration,attemptId='adopt-'+(++this.requestSequence)+'-'+Date.now().toString(36);
    const active=()=>this.adopting&&this.transport===transport&&generation===this.startGeneration;
    const current=()=>this.current()&&active();
    return new Promise<boolean>(resolve=>{
      let settled=false;
      const give=(ok:boolean)=>{if(settled)return;settled=true;if(this.settleAdoption===give){const timer=this.adoptTimer;this.adoptTimer=undefined;this.adopting=false;this.settleAdoption=undefined;this.clearTimer(timer);}resolve(ok);};
      const refuse=()=>{if(!active())return;give(false);if(!this.disposed&&this.transport===transport&&generation===this.startGeneration)this.release(false,false);};
      this.settleAdoption=give;
      this.detach.push(transport.onMessage(raw=>{
        const e=parseCastEvent(raw);if(!e)return;
        if(!this.adopting){if(this.current())this.receive(e);return;}
        if(e.type==='status'&&e.attemptId===attemptId&&e.itemId){
          if(!e.controller||e.controller.bindingId!==proof.binding.id||!e.occurrence){refuse();return;}
          const remoteIdentity=Object.freeze({controller:e.controller,occurrence:e.occurrence});
          const revision=++this.statusRevision;
          void describe(e.itemId).then(async title=>{
            if(!current()){refuse();return;}if(revision!==this.statusRevision)return;
            if(!title){refuse();return;}
            try{await this.validate(proof.binding,attemptId);}catch{refuse();return;}
            if(!current()||revision!==this.statusRevision)return;
            this.acceptedController=e.controller;
            this.publish({phase:'casting',disclosureRefused:false,remoteIdentity,remoteStopped:false,controlPending:false,acceptedSource:undefined,deviceName:transport.deviceName,itemId:e.itemId,versionId:remoteIdentity?e.versionId:undefined,title,error:undefined,errorId:undefined,takeover:undefined,
              positionSeconds:e.positionSeconds,durationSeconds:e.durationSeconds,paused:e.paused,audioTracks:e.audioTracks,textTracks:e.textTracks,reported:e.clockKnown?Object.freeze({itemId:e.itemId,...(e.controller&&e.occurrence&&e.versionId?{versionId:e.versionId}:{}),positionSeconds:e.positionSeconds,durationSeconds:e.durationSeconds,paused:e.paused}):undefined});
            give(true);
          },refuse);
        }else if(['pair-required','replaced','busy'].includes(e.type))refuse();
      }));
      this.detach.push(transport.onSessionEnd(()=>{if(this.adopting)refuse();else this.sessionEnded();}));
      this.adoptTimer=this.setTimer(refuse,waitMs);
      void this.validate(proof.binding,attemptId).then(ticket=>{
        if(!current()){refuse();return;}return transport.send({type:'adopt',bindingId:proof.binding.id,attemptId,ticket,controlToken:proof.controlToken});
      }).catch(refuse);
    });
  }

  private exchange(body: CastCommand): Promise<void> {
    const transport=this.transport,generation=this.startGeneration;
    return new Promise<void>((resolve,reject)=>{
      let timer:unknown,settled=false;
      const finish=(error?:Error)=>{
        if(settled)return;
        settled=true;
        if(this.waiting===waiter)this.waiting=undefined;
        const captured=timer;timer=undefined;this.clearTimer(captured);
        if(error)reject(error);else resolve();
      };
      const waiter={resolve:()=>finish(),reject:(error:Error)=>finish(error)};
      if(!transport||this.disposed||generation!==this.startGeneration){finish(new CastFailure('cast.error.noAnswer'));return;}
      this.waiting=waiter;
      const captured=this.setTimer(()=>finish(new CastFailure('cast.error.noAnswer')),CAST_PAIR_TIMEOUT_MS);
      if(settled||this.waiting!==waiter||this.transport!==transport||this.startGeneration!==generation){this.clearTimer(captured);finish(new CastFailure('cast.error.noAnswer'));return;}
      timer=captured;
      const admitted=this.current();
      if(!admitted||settled||this.waiting!==waiter||this.transport!==transport||this.startGeneration!==generation){finish(new CastFailure('cast.error.noAnswer'));return;}
      void transport.send(body).catch(()=>finish(new CastFailure('cast.error.noAnswer')));
    });
  }

  /** Correlate a bounded number of in-flight receiver loads; a transport send is not playback. */
  private transferPath(id:string){return encodedRoutePath('social_get_v1_cast_bootstrap_id',{id:encodeURIComponent(id)});}
  private async transferStatus(id:string):Promise<CastTransferReceipt|undefined>{
    let failure:unknown;
    for(let n=0;n<2;n++)try{return castTransferReceipt(await this.api.request(this.transferPath(id),'GET'),id);}catch(error){failure=error;}
    throw failure;
  }
  private cancelTransfer(flight:CastTransferFlight):Promise<CastTransferReceipt|undefined>{
    if(!flight.id)return Promise.resolve(undefined);
    if(flight.cancellation)return flight.cancellation;
    const id=flight.id;
    flight.cancellation=(async()=>{
      let failure:unknown;
      for(let n=0;n<2;n++)try{
        const receipt=castTransferReceipt(await this.api.request(this.transferPath(id),'DELETE'),id);
        if(!receipt)throw new CastFailure('cast.error.noAnswer');return receipt;
      }catch(error){failure=error;}
      flight.cancellation=undefined;throw failure;
    })();return flight.cancellation;
  }
  /** Replay the same command; resolve uncertain commits with the original authenticated receipt. */
  private load(command: Extract<CastCommand, {type: 'load'}>): Promise<string> {
    if(this.loads.size>=8||!this.transport)return Promise.reject(new CastFailure('cast.error.busy'));
    const requestId='load-'+(++this.requestSequence),transport=this.transport,controller=this.acceptedController,original=this.original();
    const queue=command.queue&&typeof command.queue==='object'&&'queueId' in command.queue?command.queue:undefined,transferId=queue?.transferId;
    return new Promise<string>((resolve,reject)=>{
      const retries:unknown[]=[];
      let timer:unknown,settled=false,startId:string|undefined,sequence=0,deadline=0;
      const pure=()=>!settled&&original.pure()&&this.loads.get(requestId)===flight;
      const current=()=>pure()&&this.current()&&pure();
      const finish=(error?:Error,itemId=command.itemId)=>{
        if(settled)return;
        settled=true;deadline++;
        if(this.loads.get(requestId)===flight)this.loads.delete(requestId);
        const oldTimer=timer;timer=undefined;
        this.clearTimer(oldTimer);for(const retry of retries)this.clearTimer(retry);
        if(error)reject(error);else resolve(itemId);
      };
      const refused=()=>{if(pure())finish(new CastFailure('cast.error.noAnswer'));};
      const recover=async(expected:number)=>{
        if(expected!==deadline)return;
        if(!current()){refused();return;}
        try{
          const receipt=typeof transferId==='string'?await this.transferStatus(transferId):undefined;
          if(expected!==deadline)return;
          if(!current()){refused();return;}
          if(receipt?.state==='committed'&&receipt.itemId===command.itemId){finish(undefined,receipt.itemId);return;}
          finish(new CastFailure('cast.error.noAnswer'));
        }catch{if(expected!==deadline)return;if(current())finish(new CastFailure('cast.error.unreachable'));else refused();}
      };
      const arm=()=>{
        if(!current()){refused();return;}
        const expected=++deadline,old=timer;timer=undefined;this.clearTimer(old);
        if(!current()){refused();return;}
        const next=this.setTimer(()=>{void recover(expected);},150_000);
        if(!pure()){this.clearTimer(next);return;}
        timer=next;
      };
      const flight={resolve:(itemId:string)=>finish(undefined,itemId),reject:(error:Error)=>finish(error),pending:(event:Extract<CastEvent,{type:'load-pending'}>)=>{
        if(!current()||!controller||!sameCastController(controller,event.controller)||event.itemId!==command.itemId||event.sequence<=sequence||(startId!==undefined&&event.startId!==startId))return;
        startId=event.startId;sequence=event.sequence;
        // Only a new, original-correlated validated-202 observation re-arms
        // the finite no-answer deadline. It never completes this LOAD.
        arm();
      }};
      if(!original.pure()){finish(new CastFailure('cast.error.noAnswer'));return;}
      this.loads.set(requestId,flight);arm();
      const send=()=>{if(current())void transport.send({...command,requestId,...(controller?{controller}:{})}).catch(()=>{if(!transferId&&pure())finish(new CastFailure('cast.error.unreachable'));});};
      if(transferId)for(const delay of [10_000,30_000]){
        if(!current())break;
        const retry=this.setTimer(send,delay);
        if(!pure()){this.clearTimer(retry);break;}
        retries.push(retry);
      }
      send();
    });
  }

  private receive(e: CastEvent) {
    const original=this.original();
    if(!original.current())return;
    switch (e.type) {
      case 'renewal-offer': {
        const transport=this.transport, context=this.proofContext, accepted=this.acceptedController;
        const generation=this.startGeneration;
        const proof=transport&&this.proof(transport), next=e.binding;
        if(!original.pure())return;
        if(this.state.phase!=='casting'||!transport||!context||!proof||!accepted||!sameCastController(e.previousController,accepted)||e.controller.bindingId!==accepted.bindingId||e.controller.connectionGeneration!==accepted.connectionGeneration||e.controller.controllerGeneration!==accepted.controllerGeneration+1||this.renewal)return;
        const old=proof.binding;
        if(next.id!==old.id||next.bootstrapId!==old.bootstrapId||next.receiverId!==old.receiverId||next.targetId!==old.targetId||!castScopeEqual(next.scope,old.scope)||next.targetFamilyId===old.targetFamilyId||BigInt(next.targetGeneration)<=BigInt(old.targetGeneration))return;
        const pending={attemptId:e.attemptId,binding:next,proof,transport,generation,controller:e.controller,timer:undefined as unknown};
        const pure=()=>original.pure()&&this.state.phase==='casting'&&this.renewal===pending&&sameCastController(this.acceptedController,accepted);
        const current=()=>pure()&&this.current()&&pure();
        if(!original.pure())return;
        this.renewal=pending;this.statusRevision++;this.publish({controlPending:true},pure);
        if(!current())return;
        const timer=this.setTimer(()=>{if(current())this.redacted(transport,generation,++this.statusRevision);},15000);
        if(!current()){this.clearTimer(timer);return;}
        pending.timer=timer;
        if(!current())return;
        void this.validate(next,e.attemptId,pure).then(ticket=>{
          if(!current())return;
          return transport.send({type:'renewal-ack',bindingId:next.id,attemptId:e.attemptId,ticket,controlToken:proof.controlToken});
        }).catch(()=>{if(current())this.redacted(transport,generation,++this.statusRevision);});
        return;
      }
      case 'renewal-complete': {
        const pending=this.renewal;
        if(!pending||pending.attemptId!==e.attemptId||!sameCastController(e.controller,pending.controller))return;
        const pure=()=>original.pure()&&this.state.phase==='casting'&&this.renewal===pending&&this.transport===pending.transport&&this.startGeneration===pending.generation;
        const current=()=>pure()&&this.current()&&pure();
        if(!current())return;
        const proof:CastControllerProof=Object.freeze({...pending.proof,binding:pending.binding});
        try{this.controllerStorage?.set(castProofKey(proof.context,pending.transport.deviceId!),JSON.stringify(proof));}catch{if(current())this.redacted(pending.transport,pending.generation,++this.statusRevision);return;}
        if(!current())return;
        this.clearTimer(pending.timer);if(!current())return;
        this.renewal=undefined;this.acceptedController=pending.controller;
        const oldRemote=this.state.remoteIdentity;
        const completed=()=>!this.disposed&&this.state.phase==='casting'&&this.transport===pending.transport&&this.startGeneration===pending.generation&&this.acceptedController===pending.controller&&!this.renewal;
        this.publish({controlPending:false,...(oldRemote?{remoteIdentity:Object.freeze({controller:pending.controller,occurrence:oldRemote.occurrence})}:{})},completed);
        if(!completed()||!this.current()||!completed())return;
        void pending.transport.send({type:'status',controller:pending.controller}).catch(()=>{});
        return;
      }
      case 'paired': {
        if(!this.waiting)return;
        const waiting=this.waiting;
        const transport=this.transport,generation=this.startGeneration,context=this.proofContext;
        if(!context){waiting.resolve();return;}
        if(!e.binding||!e.controlToken||!transport?.deviceId||!this.expectedPairId||e.binding.bootstrapId!==this.expectedPairId||!e.controller||e.controller.bindingId!==e.binding.id){waiting.reject(new CastFailure('cast.error.refused'));return;}
        const binding=e.binding,controlToken=e.controlToken;
        void this.validate(binding,'pair-'+(++this.requestSequence)+'-'+Date.now().toString(36)).then(()=>{
          if(!this.current()||generation!==this.startGeneration||this.transport!==transport||this.waiting!==waiting)return;
          this.acceptedController=e.controller;
          const proof:CastControllerProof=Object.freeze({version:2,context,binding,controlToken});
          try{this.controllerStorage?.set(castProofKey(context,transport.deviceId!),JSON.stringify(proof));}catch{}
          waiting.resolve();
        },()=>{if(generation===this.startGeneration)waiting.reject(new CastFailure('cast.error.refused'));});return;
      }
      // The TV is asking the person watching; keep waiting (the pairing timeout allows for it).
      case 'pair-pending': if (this.state.phase === 'pairing') this.publish({phase: 'waiting'}); return;
      case 'pair-failed': this.waiting?.reject(new CastFailure(e.code === 'tv_busy' ? 'cast.error.tvBusy' : 'cast.error.refused')); return;
      case 'busy': this.fail('cast.error.busy'); return;
      case 'pair-required': if(this.release(false,false)!==this.startGeneration)return;this.publish({phase:'off',disclosureRefused:true,remoteIdentity:undefined,remoteStopped:undefined,controlPending:false,itemId:undefined,title:undefined,reported:undefined,audioTracks:[],textTracks:[],positionSeconds:0,durationSeconds:0});return;
      case 'load-pending': this.loads.get(e.requestId)?.pending(e);return;
      case 'loaded': this.loads.get(e.requestId)?.resolve(e.itemId); return;
      case 'load-failed': if (e.requestId) this.loads.get(e.requestId)?.reject(new CastFailure('cast.error.loadFailed')); else this.fail('cast.error.loadFailed'); return;
      case 'takeover-requested': {
        this.clearTimer(this.takeoverTimer);
        if(this.acceptedController&&(!sameCastController(e.controller,this.acceptedController)||!e.requestId))return;
        this.publish({takeover:Object.freeze({requester:e.requester||serviceText('cast.someone'),controller:e.controller,requestId:e.requestId})});
        // The TV treats 30 s without an answer as no; drop the prompt with it.
        this.takeoverTimer = this.setTimer(() => this.publish({takeover: undefined}), CAST_TAKEOVER_WINDOW_MS);
        return;
      }
      case 'replaced':
        this.clearTimer(this.takeoverTimer);
        if(this.release(false)!==this.startGeneration)return;
        this.publish({phase:'replaced',remoteIdentity:undefined,remoteStopped:undefined,controlPending:false,takeover:undefined});
        return;
      case 'stopped': case 'control-failed': {
        const pending=this.stopping;
        if(!pending||e.requestId!==pending.requestId||!sameCastRemote(e.target,pending.target)||this.transport!==pending.transport||this.startGeneration!==pending.generation)return;
        this.clearTimer(pending.timer);this.stopping=undefined;this.statusRevision++;
        if(e.type==='control-failed'){this.redacted(pending.transport,pending.generation,this.statusRevision);return;}
        try {const records=JSON.parse(this.controllerStorage!.get(pending.storageKey)??'[]') as {key:string;state:string}[];const record=records.find(record=>record.key===pending.requestId);if(!record)throw new Error('mutation_store_damaged');record.state='confirmed';this.controllerStorage!.set(pending.storageKey,JSON.stringify(records));} catch {this.redacted(pending.transport,pending.generation,this.statusRevision);return;}
        // Only the correlated actual receiver Stop receipt keeps a stopped view.
        // It grants no media, queue, or local engine occurrence.
        this.publish({phase:'off',remoteStopped:true,controlPending:false,paused:true,remoteIdentity:pending.target,versionId:pending.versionId,audioTracks:[],textTracks:[],
          positionSeconds:e.positionSeconds??this.state.positionSeconds,durationSeconds:e.durationSeconds??this.state.durationSeconds,
          reported:e.positionSeconds!==undefined&&e.durationSeconds!==undefined?Object.freeze({itemId:pending.target.occurrence.itemId,...(pending.versionId?{versionId:pending.versionId}:{}),positionSeconds:e.positionSeconds,durationSeconds:e.durationSeconds,paused:true}):undefined});
        return;
      }
      case 'status': {
        if(this.renewal)return;
        if(this.state.phase!=='casting'&&!this.state.remoteStopped||!this.current())return;
        if(this.acceptedController&&!sameCastController(e.controller,this.acceptedController))return;
        if(this.proofContext&&(!e.controller||e.itemId&&!e.occurrence))return;
        if(this.stopping){
          // Empty status is not a Stop receipt. A different actual occurrence
          // retires that pending action; its late ACK cannot stop the new view.
          if(!e.occurrence||sameCastRemote({controller:e.controller!,occurrence:e.occurrence},this.stopping.target))return;
          this.clearTimer(this.stopping.timer);this.stopping=undefined;
        }
        if(this.state.remoteStopped&&(!e.occurrence||sameCastRemote({controller:e.controller!,occurrence:e.occurrence},this.state.remoteIdentity)))return;
        const remoteIdentity=e.controller&&e.occurrence?Object.freeze({controller:e.controller,occurrence:e.occurrence}):undefined;
        const transport=this.transport,generation=this.startGeneration,revision=++this.statusRevision;
        const publish=(title?:string)=>{if(this.current()&&this.transport===transport&&this.startGeneration===generation&&this.statusRevision===revision&&(this.state.phase==='casting'||this.state.remoteStopped))this.publish({phase:e.itemId?'casting':'off',remoteIdentity,remoteStopped:false,controlPending:false,itemId:e.itemId||undefined,versionId:remoteIdentity?e.versionId:undefined,title,positionSeconds:e.positionSeconds,durationSeconds:e.durationSeconds,paused:e.paused,audioTracks:e.audioTracks,textTracks:e.textTracks,reported:e.itemId&&e.clockKnown?Object.freeze({itemId:e.itemId,...(e.controller&&e.occurrence&&e.versionId?{versionId:e.versionId}:{}),positionSeconds:e.positionSeconds,durationSeconds:e.durationSeconds,paused:e.paused}):undefined});};
        if(!e.itemId){this.clearStatusRead();publish();return;}
        if(this.describe){
          let read=this.statusRead;
          const same=read&&read.transport===transport&&read.generation===generation&&read.controller===this.acceptedController&&read.event.itemId===e.itemId&&(remoteIdentity?sameCastRemote(read.remote,remoteIdentity):!read.remote);
          if(!same){this.clearStatusRead();if(!original.current()||this.statusRevision!==revision)return;read={transport,generation,controller:this.acceptedController,revision,event:e,remote:remoteIdentity,failures:0,pending:false};this.statusRead=read;}
          else {read!.event=e;read!.revision=revision;}
          if(!read!.pending&&read!.timer===undefined)this.describeStatus(read!);
        }
        else publish(this.state.itemId===e.itemId?this.state.title:undefined);
      }
        return;
    }
  }

  private clearStatusRead(){const read=this.statusRead;this.statusRead=undefined;if(read?.timer!==undefined)this.clearTimer(read.timer);}
  private statusReadCurrent(read:CastStatusRead){
    const pure=()=>this.statusRead===read&&this.transport===read.transport&&this.startGeneration===read.generation&&this.acceptedController===read.controller&&this.statusRevision===read.revision&&(this.state.phase==='casting'||!!this.state.remoteStopped);
    return pure()&&this.current()&&pure();
  }
  private describeStatus(read:CastStatusRead){
    if(!this.describe||!this.statusReadCurrent(read))return;
    read.pending=true;
    void Promise.resolve().then(()=>this.statusReadCurrent(read)?this.describe!(read.event.itemId):undefined).then(title=>{
      if(!this.statusReadCurrent(read))return;
      read.pending=false;
      if(!title){this.redacted(read.transport,read.generation,read.revision);return;}
      read.failures=0;
      this.publishStatusRead(read,title);
    },error=>{
      if(!this.statusReadCurrent(read))return;
      read.pending=false;
      const status=(error as {status?:unknown}|null)?.status;
      if(status===401||status===403||status===404||status===410){this.redacted(read.transport,read.generation,read.revision);return;}
      // A failed lookup grants no new disclosure. Only a previously checked
      // occurrence keeps its title and live controls while this read retries.
      if(this.state.itemId===read.event.itemId&&(read.remote?sameCastRemote(this.state.remoteIdentity,read.remote):!this.state.remoteIdentity))this.publishStatusRead(read,this.state.title);
      if(!this.statusReadCurrent(read))return;
      let fired=false;
      const timer=this.setTimer(()=>{fired=true;read.timer=undefined;if(this.statusReadCurrent(read))this.describeStatus(read);},Math.min(30000,1000*2**Math.min(read.failures++,5)));
      if(!fired&&this.statusReadCurrent(read))read.timer=timer;else this.clearTimer(timer);
    });
  }
  private publishStatusRead(read:CastStatusRead,title?:string){
    const e=read.event,revision=read.revision;
    this.publish({phase:'casting',remoteIdentity:read.remote,remoteStopped:false,controlPending:false,itemId:e.itemId,versionId:read.remote?e.versionId:undefined,title,positionSeconds:e.positionSeconds,durationSeconds:e.durationSeconds,paused:e.paused,audioTracks:e.audioTracks,textTracks:e.textTracks,
      reported:e.clockKnown?Object.freeze({itemId:e.itemId,...(e.controller&&e.occurrence&&e.versionId?{versionId:e.versionId}:{}),positionSeconds:e.positionSeconds,durationSeconds:e.durationSeconds,paused:e.paused}):undefined},()=>this.statusReadCurrent(read)&&read.revision===revision);
  }

  private redacted(transport:CastTransport|undefined,generation:number,revision:number){
    if(!this.current()||this.transport!==transport||generation!==this.startGeneration||revision!==this.statusRevision)return;
    if(this.release(false,false)!==this.startGeneration)return;this.publish({phase:'off',disclosureRefused:true,remoteIdentity:undefined,remoteStopped:undefined,controlPending:false,itemId:undefined,title:undefined,reported:undefined,audioTracks:[],textTracks:[],positionSeconds:0,durationSeconds:0});
  }
  private fail(id: MessageId,admit?:()=>boolean) { this.publish({phase: 'error', errorId: id, error: serviceText(id)},admit); }

  private sessionEnded() {
    if(this.release(false,false)!==this.startGeneration)return;
    if (this.state.phase !== 'error' && this.state.phase !== 'replaced') this.publish({phase:'off',remoteIdentity:undefined,remoteStopped:undefined,controlPending:false,itemId:undefined,title:undefined,deviceName:undefined,takeover:undefined});
  }

  /** Detach from the transport; `stopReceiver` closes Portico on the TV. */
  private detachOriginal(stopReceiver: boolean, end: boolean):{released:number;flight?:CastTransferFlight} {
    const released=++this.startGeneration;this.statusRevision++;
    // Detach every old handle before the first injected cleanup port. Cleanup
    // may synchronously start a successor; nothing below selects latest state.
    const renewal=this.renewal,stopping=this.stopping,adoptTimer=this.adoptTimer,settle=this.settleAdoption;
    const flight=this.transferFlight,transport=this.transport,waiting=this.waiting,loads=[...this.loads.values()],detach=this.detach.splice(0);
    const statusRead=this.statusRead;this.statusRead=undefined;
    this.renewal=undefined;this.stopping=undefined;this.acceptedController=undefined;this.expectedPairId=undefined;
    this.adoptTimer=undefined;this.settleAdoption=undefined;this.adopting=false;
    this.state=Object.freeze({...this.state,versionId:undefined,reported:undefined});
    this.transferFlight=undefined;this.transport=undefined;this.waiting=undefined;this.loads.clear();
    if(flight?.id)this.recoveryTransfer=flight;
    if(renewal)this.clearTimer(renewal.timer);
    if(stopping)this.clearTimer(stopping.timer);
    if(statusRead?.timer!==undefined)this.clearTimer(statusRead.timer);
    this.clearTimer(adoptTimer);settle?.(false);
    for(const off of detach)off();
    waiting?.reject(new CastFailure('cast.error.noAnswer'));
    for(const pending of loads)pending.reject(new CastFailure('cast.error.noAnswer'));
    if(this.startGeneration===released&&transport&&end)transport.end(stopReceiver);
    return {released,flight};
  }

  /** Explicitly admitted release may cancel only its captured transfer. */
  private release(stopReceiver:boolean,end=true):number {
    const {released,flight}=this.detachOriginal(stopReceiver,end);
    const original=()=>!this.disposed&&!this.transport&&this.startGeneration===released;
    if(flight)void this.cancelTransfer(flight).then(receipt=>{
      if(!original())return;
      if(receipt?.state==='committed'&&receipt.itemId===flight.itemId)this.publish({acceptedSource:flight.source},original);
      if(!original())return;
      this.recoveryTransfer=undefined;this.publish({phase:'off',acceptedSource:undefined,transferPending:false},original);
    },()=>{if(original()){this.recoveryTransfer=flight;this.fail('cast.error.unreachable',original);}});
    return released;
  }

  private send(message: CastCommand) { if(this.current())void this.transport?.send(message.type==='status'&&this.acceptedController?{...message,controller:this.acceptedController}:message).catch(() => {}); }

  /** The controller's answer to `takeover-requested`. */
  answerTakeover(allow:boolean,expected=this.state.takeover) {
    if(!expected||expected!==this.state.takeover)return;
    this.clearTimer(this.takeoverTimer);
    this.send({type:'takeover',allow,...(expected.controller?{controller:expected.controller}:{}),...(expected.requestId?{requestId:expected.requestId}:{})});
    this.publish({takeover:undefined});
  }
  private controlTarget(expected:CastRemoteIdentity|undefined){
    const target=expected??this.state.remoteIdentity;
    // A paired production sender may have loadACK before the first admitted
    // status. Missing occurrence authority is never a legacy/room downgrade.
    if((this.proofContext||this.acceptedController)&&(!target||!sameCastController(target.controller,this.acceptedController)))return false;
    return this.current()&&this.state.phase==='casting'&&!this.stopping&&!this.renewal&&(!expected||sameCastRemote(expected,this.state.remoteIdentity));
  }
  private targetFields(expected?:CastRemoteIdentity){const target=expected??this.state.remoteIdentity;return target?{target}:{};}
  private sendControl(message:CastCommand,expected?:CastRemoteIdentity){
    const transport=this.transport,generation=this.startGeneration,target=expected??this.state.remoteIdentity;
    if(!transport)return;
    void transport.send(message).catch(()=>{
      if(this.transport===transport&&this.startGeneration===generation&&target&&sameCastRemote(target,this.state.remoteIdentity))this.redacted(transport,generation,this.statusRevision);
    });
  }
  play(expected?:CastRemoteIdentity) { if(!this.controlTarget(expected))return;this.sendControl({type:'play',...this.targetFields(expected)},expected);if(!this.state.remoteIdentity)this.publish({paused:false}); }
  pause(expected?:CastRemoteIdentity) { if(!this.controlTarget(expected))return;this.sendControl({type:'pause',...this.targetFields(expected)},expected);if(!this.state.remoteIdentity)this.publish({paused:true}); }
  toggle(expected?:CastRemoteIdentity) { if(this.state.paused)this.play(expected);else this.pause(expected); }
  seek(positionSeconds:number,expected?:CastRemoteIdentity) { if(!this.controlTarget(expected)||!Number.isFinite(positionSeconds))return;this.sendControl({type:'seek',positionSeconds:Math.max(0,positionSeconds),...this.targetFields(expected)},expected); }
  setAudioTrack(trackId:number,expected?:CastRemoteIdentity) { if(this.controlTarget(expected)&&Number.isSafeInteger(trackId))this.sendControl({type:'audio',trackId,...this.targetFields(expected)},expected); }
  /** `null` turns subtitles off. */
  setSubtitleTrack(trackId:number|null,expected?:CastRemoteIdentity) { if(this.controlTarget(expected))this.sendControl({type:'subtitles',trackIds:trackId===null?[]:[trackId],...this.targetFields(expected)},expected); }
  stop(expected?:CastRemoteIdentity, suppliedMutationKey?:string) {
    if(expected&&!sameCastRemote(expected,this.state.remoteIdentity))return;
    if(this.adopting){this.release(false,false);return;}
    this.clearTimer(this.takeoverTimer);
    const flight=this.transferFlight??this.recoveryTransfer;
    if(flight?.id){
      this.transferFlight=undefined;const generation=++this.startGeneration;
      void this.cancelTransfer(flight).then(receipt=>{
        if(this.disposed||generation!==this.startGeneration||this.transport!==undefined&&this.transport!==flight.transport)return;
        if(receipt?.state==='committed'&&receipt.itemId===flight.itemId)this.publish({acceptedSource:flight.source});
        this.recoveryTransfer=undefined;if(this.release(false)!==this.startGeneration)return;
        this.publish({phase:'off',acceptedSource:undefined,transferPending:false,itemId:undefined,title:undefined,deviceName:undefined,error:undefined,errorId:undefined,takeover:undefined});
      },()=>{if(!this.disposed&&generation===this.startGeneration){this.recoveryTransfer=flight;if(this.release(false)===this.startGeneration)this.fail('cast.error.unreachable');}});
      return;
    }
    const target=expected??this.state.remoteIdentity,transport=this.transport;
    if(target&&transport&&this.controlTarget(target)){
      if (!suppliedMutationKey && this.api.createUserMutationKey) {
        const generation=this.startGeneration;
        void this.api.createUserMutationKey().then(key=>{if(this.current()&&this.startGeneration===generation&&this.transport===transport&&sameCastRemote(target,this.state.remoteIdentity))this.stop(target,key);},()=>this.fail('cast.error.unreachable'));
        return;
      }
      const generation=this.startGeneration;
      const storageKey='portico.cast.stop:v1:'+JSON.stringify([this.proofContext,transport.deviceId,]);
      let requestId: string;
      try {
        if(!this.controllerStorage)throw new Error('mutation_not_queued');
        const raw=this.controllerStorage.get(storageKey), records: {key:string;target:CastRemoteIdentity;state:string}[]=raw?JSON.parse(raw):[];
        if(!Array.isArray(records)||records.length>256||records.some(record=>!isUserMutationKey(record.key)||!record.target?.occurrence||!['pending','confirmed'].includes(record.state)))throw new Error('mutation_store_damaged');
        const occurrence=target.occurrence;
        const prior=records.find(record=>record.target.occurrence.sessionId===occurrence.sessionId&&record.target.occurrence.generation===occurrence.generation&&record.target.occurrence.itemId===occurrence.itemId);
        if(prior?.state==='confirmed'){this.publish({remoteStopped:true,controlPending:false});return;}
        requestId=prior?.key??suppliedMutationKey??newUserMutationKey();
        if(!prior){
          // Receipt UI has already consumed previous completed commands. Pending
          // commands for other occurrences keep their original custody and key.
          const next=records.filter(record=>record.state==='pending');if(next.length>=256)throw new Error('mutation_not_queued');
          next.push({key:requestId,target,state:'pending'});const value=JSON.stringify(next);
          this.controllerStorage.set(storageKey,value);if(this.controllerStorage.get(storageKey)!==value)throw new Error('mutation_not_queued');
        }
      } catch {this.fail('cast.error.unreachable');return;}

      const refuse=()=>{if(this.stopping?.requestId!==requestId)return;this.redacted(transport,generation,this.statusRevision);};
      this.statusRevision++;
      this.stopping={storageKey,requestId,target,transport,generation,versionId:this.state.versionId,timer:this.setTimer(refuse,35_000)};
      this.publish({controlPending:true});
      void transport.send({type:'stop',requestId,target}).catch(refuse);return;
    }
    if(target||this.stopping)return;
    if(this.proofContext||this.acceptedController){
      // No exact occurrence was observed (or it was redacted). Detach this
      // caller only; a bare Stop/SDK end could stop a successor receiver item.
      const generation=this.startGeneration;this.redacted(this.transport,generation,this.statusRevision);return;
    }
    this.send({type: 'stop'});
    if(this.release(true)!==this.startGeneration)return;
    this.publish({phase: 'off', transferPending:false,itemId: undefined, title: undefined, deviceName: undefined, error: undefined, errorId: undefined, takeover: undefined});
  }
  dismiss() { if(this.state.transferPending){this.stop();return;}this.publish({phase:'off',remoteIdentity:undefined,remoteStopped:undefined,controlPending:false,versionId:undefined,reported:undefined,error:undefined,errorId:undefined}); }
  dispose() { const adopting=this.adopting;this.clearTimer(this.takeoverTimer); this.settleAdoption?.(false); this.release(false,!adopting); this.disposed = true; this.listeners.clear(); }
}
