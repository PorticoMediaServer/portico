import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {serviceProblem,serviceText} from './presentation/service-text.ts';
import {SOCIAL_PROTOCOL,parseHandoff,parseReceiver,parseReceiverGrant,parseReceiverList,type Handoff,type Receiver,type ReceiverGrant} from './social-playback.ts';
import type {EventsClient} from './playback-v1/events.ts';
import {SessionsClient,type SessionChange} from './playback-v1/sessions.ts';
import {PlaybackApiError,type V1Request,type V1Response} from './playback-v1/http.ts';
import type {Session} from './playback-v1/types.ts';

/** Moving playback to a Portico television, and being that television.
 *
 * A handoff never guesses: the phone proposes, the television proves it is actually playing,
 * and only then does the phone commit and its own player retire. Anything short of that
 * (declined, timed out, television unreachable) leaves the phone playing exactly where it
 * was. `HandoffController` is the phone's half and `ReceiverHost` the television's. */

export type RemoteApi=Readonly<{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>;requestRaw?(path:string,method:string,options?:Readonly<{headers?:Readonly<Record<string,string>>;body?:unknown;signal?:AbortSignal}>):Promise<V1Response>}>;
const status=(e:unknown)=>(e as {status?:number}|null)?.status??0;
const wait=(ms:number,signal:AbortSignal)=>new Promise<void>(resolve=>{if(signal.aborted){resolve();return;}const t=setTimeout(done,ms);function done(){clearTimeout(t);signal.removeEventListener('abort',done);resolve();}signal.addEventListener('abort',done,{once:true});});
const us=(seconds:number)=>String(Math.max(0,Math.round(seconds*1e6)));

export type HandoffPhase='idle'|'asking'|'preparing'|'starting'|'done'|'failed';
/** `handoffId` is the committed handoff the controls follow (from `send` or `reconnect`): what a later launch reconnects to. */
export type HandoffSnapshot=Readonly<{receivers:readonly Receiver[];directoryPhase:'idle'|'loading'|'ready'|'error';directoryError?:string;phase:HandoffPhase;target?:Receiver;message?:string;handoffId?:string;receiverPlaybackId?:string;remoteSession?:Session;controlling?:boolean}>;
export type HandoffSource=Readonly<{playbackId:string;positionSeconds:number}>;

export class HandoffController{
 private sessions?:Pick<SessionsClient,'get'|'patch'|'stop'>;private controlWork:Promise<boolean>=Promise.resolve(true);
 private api:RemoteApi;private device:{deviceId:string;displayName:string};private requestId:()=>string;private pollMs:number;
 private state:HandoffSnapshot=Object.freeze<HandoffSnapshot>({receivers:[],directoryPhase:'idle',phase:'idle'});
 private listeners=new Set<()=>void>();private run?:AbortController;
 constructor(options:{api:RemoteApi;device:{deviceId:string;displayName:string};requestId:()=>string;pollMs?:number;sessions?:Pick<SessionsClient,'get'|'patch'|'stop'>}){this.api=options.api;this.device=options.device;this.requestId=options.requestId;this.pollMs=options.pollMs??1000;this.sessions=options.sessions??(options.api.requestRaw?new SessionsClient({send:(r:V1Request)=>options.api.requestRaw!(r.path,r.method,{headers:r.headers,body:r.body,signal:r.signal})}):undefined);}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(patch:Partial<HandoffSnapshot>){this.state=Object.freeze({...this.state,...patch});this.listeners.forEach(f=>f());}
 dispose(){this.run?.abort();this.listeners.clear();}

 /** Televisions and Cast devices this viewer may send to. A failed refresh keeps the list. */
 async refresh():Promise<void>{
  if(this.state.directoryPhase==='idle')this.publish({directoryPhase:'loading'});
  try{this.publish({receivers:parseReceiverList(await this.api.request<RouteResponse<"social_get_v1_receivers">>(routePath("social_get_v1_receivers"))).filter(r=>r.state==='active'),directoryPhase:'ready',directoryError:undefined});}
  catch(e){this.publish({directoryPhase:this.state.receivers.length?'ready':'error',directoryError:status(e)===404||status(e)===503?'':serviceProblem(e,'devices','playOn.devicesUnavailable','load')});}
 }
 cancel(){this.run?.abort();this.run=undefined;if(this.state.phase!=='done')this.publish({phase:'idle',target:undefined,message:undefined});}

 /** Resolves true once the television has taken over and this device's player should stop. */
 async send(target:Receiver,source:HandoffSource):Promise<boolean>{
  this.run?.abort();const run=this.run=new AbortController(),signal=run.signal;
  const fail=(message:string)=>{if(!signal.aborted)this.publish({phase:'failed',message});return false;};
  let handoff:Handoff|undefined;const device={device:target.displayName};
  try{
   this.publish({phase:'asking',target,message:undefined,handoffId:undefined,receiverPlaybackId:undefined,remoteSession:undefined});
   let grant=parseReceiverGrant(await this.api.request<RouteResponse<"social_post_v1_receivers_id_grants">>(encodedRoutePath("social_post_v1_receivers_id_grants", {"id": encodeURIComponent(target.id)}),'POST',routeBody("social_post_v1_receivers_id_grants", {protocolVersion:SOCIAL_PROTOCOL,controllerDisplayName:this.device.displayName.slice(0,64)}),signal));
   // A television that has not met this device asks its own viewer first.
   for(let waited=0;grant.state==='pending'&&waited<60000&&!signal.aborted;waited+=this.pollMs){
    this.publish({message:serviceText('playOn.approveOn',device)});
    await wait(this.pollMs,signal);
    const list=await this.api.request<{grants?:unknown[]}>(encodedRoutePath("social_get_v1_receivers_id_grants", {"id": encodeURIComponent(target.id)}),'GET',undefined,signal);
    const mine=(list.grants??[]).find(g=>(g as {id?:string}|null)?.id===grant.id);
    if(mine)grant=parseReceiverGrant({protocolVersion:SOCIAL_PROTOCOL,serverTime:'',grant:mine});
   }
   if(signal.aborted)return false;
   if(grant.state==='pending')return fail(serviceText('playOn.didNotAnswer',device));
   if(grant.state!=='accepted')return fail(serviceText(grant.state==='declined'?'playOn.declined':'playOn.notAccepting',device));
   this.publish({phase:'preparing',message:undefined});
   handoff=parseHandoff(await this.api.request<RouteResponse<"social_post_v1_handoffs">>(routePath("social_post_v1_handoffs"),'POST',routeBody("social_post_v1_handoffs", {protocolVersion:SOCIAL_PROTOCOL,requestId:this.requestId(),receiverId:target.id,grantId:grant.id,sourcePlaybackId:source.playbackId,startPositionUs:us(source.positionSeconds)}),signal));
   this.publish({phase:'starting'});
   // The television proves it is playing; until it does, this device stays the player.
   while(handoff.state==='prepared'&&handoff.outcome==='waiting'&&!signal.aborted){
    await wait(this.pollMs,signal);if(signal.aborted)break;
    handoff=parseHandoff(await this.api.request<RouteResponse<"social_get_v1_handoffs_id">>(encodedRoutePath("social_get_v1_handoffs_id", {"id": encodeURIComponent(handoff.id)}),'GET',undefined,signal));
   }
   if(signal.aborted){void this.rollback(handoff);return false;}
   if(handoff.state!=='prepared'||handoff.outcome!=='pending')return fail(serviceText(handoff.state==='expired'?'playOn.didNotStartInTime':'playOn.couldNotStart',device));
   handoff=parseHandoff(await this.api.request<RouteResponse<"social_post_v1_handoffs_id_commit">>(encodedRoutePath("social_post_v1_handoffs_id_commit", {"id": encodeURIComponent(handoff.id)}),'POST',routeBody("social_post_v1_handoffs_id_commit", {protocolVersion:SOCIAL_PROTOCOL,expectedRevision:handoff.revision}),signal));
   if(handoff.state!=='committed')return fail(serviceText('playOn.couldNotTakeOver',device));
   this.publish({phase:'done',handoffId:handoff.id,receiverPlaybackId:handoff.receiverPlaybackId,message:serviceText('playOn.playingOn',device)});
   return true;
  }catch(e){
   if(signal.aborted){if(handoff)void this.rollback(handoff);return false;}
   if(handoff&&handoff.state==='prepared')void this.rollback(handoff);
   return fail(serviceText('playOn.notReachable',device));
  }
 }
 /** Reattach to the destination session of a committed handoff after an app restart. */
 async reconnect(handoffId:string):Promise<boolean>{
  try{const h=parseHandoff(await this.api.request<RouteResponse<"social_get_v1_handoffs_id">>(encodedRoutePath("social_get_v1_handoffs_id", {"id": encodeURIComponent(handoffId)})));
   if(h.state!=='committed')return false;
   this.publish({phase:'done',handoffId:h.id,receiverPlaybackId:h.receiverPlaybackId});return this.refreshRemote();
  }catch{return false;}
 }
 async refreshRemote():Promise<boolean>{
  const id=this.state.receiverPlaybackId;if(!id||!this.sessions)return false;
  try{const session=await this.sessions.get(id);if(this.state.receiverPlaybackId!==id)return false;this.publish({remoteSession:session});return session.state!=='ended';}catch{return false;}
 }
 play(){return this.control({state:'playing'});}
 pause(){return this.control({state:'paused'});}
 seek(positionSeconds:number){if(!Number.isFinite(positionSeconds)||positionSeconds<0)return Promise.resolve(false);return this.control({seek:{positionMs:Math.round(positionSeconds*1000),id:this.requestId()}});}
 stop(){return this.control();}
 private control(change?:SessionChange):Promise<boolean>{
  const id=this.state.receiverPlaybackId,sessions=this.sessions;if(!id||!sessions)return Promise.resolve(false);
  return this.controlWork=this.controlWork.catch(()=>false).then(async()=>{
   if(this.state.receiverPlaybackId!==id)return false;
   this.publish({controlling:true});
   try{
    if(!change){await sessions.stop(id);this.publish({receiverPlaybackId:undefined,remoteSession:undefined,phase:'idle'});return true;}
    let current=await sessions.get(id),next:Session;
    try{next=await sessions.patch(id,current.revision,change);}catch(e){
     if(!(e instanceof PlaybackApiError)||e.status!==412||!e.current)throw e;
     const fresh=e.current as Session;
     if(change.seek||fresh.state!==current.state||fresh.presentation.generation!==current.presentation.generation)throw e;
     next=await sessions.patch(id,fresh.revision,change);
    }
    if(this.state.receiverPlaybackId===id)this.publish({remoteSession:next});return true;
   }catch(e){this.publish({message:serviceProblem(e,'devices','playOn.controlUnavailable','action')});return false;}
   finally{this.publish({controlling:false});}
  });
 }
 private async rollback(handoff:Handoff){try{await this.api.request<RouteResponse<"social_post_v1_handoffs_id_rollback">>(encodedRoutePath("social_post_v1_handoffs_id_rollback", {"id": encodeURIComponent(handoff.id)}),'POST',routeBody("social_post_v1_handoffs_id_rollback", {protocolVersion:SOCIAL_PROTOCOL,expectedRevision:handoff.revision,reason:'controller-cancelled'}));}catch{/* it expires by itself */}}
}

/** What a television's player must offer to take a handoff. */
export type ReceiverPlayer=Readonly<{
 load(itemId:string,positionSeconds:number):void;
 state():{itemId:string;playbackId:string;playing:boolean;positionSeconds:number};
}>;
export type ReceiverPhase='off'|'registering'|'waiting'|'starting'|'error';
export type ReceiverSnapshot=Readonly<{phase:ReceiverPhase;receiver?:Receiver;asking?:ReceiverGrant;error?:string}>;
export type ReceiverDevice=Readonly<{deviceId:string;displayName:string;platform:string;keyFingerprint:string}>;

/** PERF-16: the idle inbox delay, `baseMs` ± 20%. `rand` is injected for tests. */
export function receiverPollDelay(baseMs:number,rand:()=>number=Math.random):number{
 const r=Math.min(0.999999,Math.max(0,rand()));
 return Math.round(baseMs*(0.8+0.4*r));
}

/** Receiver invalidations share the one device feed. */
export type ReceiverInboxEventKind='grant'|'handoff'|'resync';
export type ReceiverInboxEvent=Readonly<{kind:ReceiverInboxEventKind;receiverId?:string}>;
const RECEIVER_SAFETY_MS=60000;

const MAX_RECEIVER_ID=200;
/** Unknown kinds and shapes are skipped (a newer server), never applied. */
export function parseReceiverInboxEvent(value:unknown):ReceiverInboxEvent|undefined{
 if(!value||typeof value!=='object'||Array.isArray(value))return undefined;
 const v=value as {kind?:unknown;receiverId?:unknown};
 if(v.kind!=='grant'&&v.kind!=='handoff'&&v.kind!=='resync')return undefined;
 if(v.receiverId!==undefined&&(typeof v.receiverId!=='string'||!v.receiverId||v.receiverId.length>MAX_RECEIVER_ID))return undefined;
 return Object.freeze({kind:v.kind,...(typeof v.receiverId==='string'?{receiverId:v.receiverId}:{})});
}

export class ReceiverHost{
 private api:RemoteApi;private device:ReceiverDevice;private player:ReceiverPlayer;private pollMs:number;private heartbeatMs:number;private rand:()=>number;private events?:EventsClient;
 /** True while the event stream is connected: the inbox poll backs off to the safety interval. */
 private streamConnected=false;private releaseEvents?:()=>void;
 /** Whether playback is in front; approvals and handoffs remain reachable. */
 private playerActive=false;
 private state:ReceiverSnapshot=Object.freeze<ReceiverSnapshot>({phase:'off'});private listeners=new Set<()=>void>();
 private run?:AbortController;private handled=new Set<string>();private inboxWork?:Promise<void>;private taking?:string;
  constructor(options:{api:RemoteApi;device:ReceiverDevice;player:ReceiverPlayer;pollMs?:number;heartbeatMs?:number;rand?:()=>number;events?:EventsClient}){this.api=options.api;this.device=options.device;this.player=options.player;this.pollMs=options.pollMs??30000;this.heartbeatMs=options.heartbeatMs??45000;this.rand=options.rand??Math.random;this.events=options.events;}
  getSnapshot=()=>this.state;
  subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
  private publish(patch:Partial<ReceiverSnapshot>){this.state=Object.freeze({...this.state,...patch});this.listeners.forEach(f=>f());}
 /** Wakes the poll loop early (a dropped event stream falls back to the 30 s poll at once). */
 private sleeper?:{wake:()=>void};
 private sleep(ms:number,signal:AbortSignal){
  return new Promise<void>(resolve=>{
   if(signal.aborted){resolve();return;}
   const done=()=>{clearTimeout(t);signal.removeEventListener('abort',done);if(this.sleeper?.wake===wake)this.sleeper=undefined;resolve();};
   const wake=()=>done();
   const t=setTimeout(done,ms);
   this.sleeper={wake};
   signal.addEventListener('abort',done,{once:true});
  });
 }
  /** The player is in the foreground (something loaded: playing or paused). While set, the inbox
   * inbox remains live so a new handoff can be accepted while something is playing. */
  setPlayerActive(active:boolean){if(this.playerActive!==active){this.playerActive=active;this.sleeper?.wake();}}
 stop(){this.releaseEvents?.();this.releaseEvents=undefined;this.run?.abort();this.run=undefined;this.streamConnected=false;this.publish({phase:'off',asking:undefined});}
 dispose(){this.stop();this.listeners.clear();}

 /** Registers (again, harmlessly) and waits to be used. Failures back off and keep trying: a
  * television that lost its server for a minute should be available again without a visit. */
 start(){
  if(this.run)return;const run=this.run=new AbortController(),signal=run.signal;
  this.streamConnected=false;
  if(this.events)this.followEvents(signal);
  void(async()=>{
   let failures=0,lastBeat=0;
   while(!signal.aborted){
    try{
     if(!this.state.receiver){
      this.publish({phase:'registering'});
      this.publish({receiver:parseReceiver((await this.api.request<{receiver?:unknown}>(routePath("social_post_v1_receivers"),'POST',routeBody("social_post_v1_receivers", {protocolVersion:SOCIAL_PROTOCOL,displayName:this.device.displayName.slice(0,64),platform:this.device.platform,keyFingerprint:this.device.keyFingerprint,supportedCommands:['load','play','pause','seek','stop'],grantPolicy:'per-device'}),signal)).receiver),phase:'waiting',error:undefined});
      lastBeat=Date.now();
     }
      const id=this.state.receiver!.id;
      if(Date.now()-lastBeat>=this.heartbeatMs){await this.api.request<RouteResponse<"social_post_v1_receivers_id_heartbeat">>(encodedRoutePath("social_post_v1_receivers_id_heartbeat", {"id": encodeURIComponent(id)}),'POST',routeBody("social_post_v1_receivers_id_heartbeat", {protocolVersion:SOCIAL_PROTOCOL,keyFingerprint:this.device.keyFingerprint}),signal);lastBeat=Date.now();}
      // While the event stream is connected the poll backs off to the safety interval;
      // on a dropped stream the 30 s poll
      // (with jitter) covers the gap until it reconnects.
      await this.refreshInbox(id,signal);
      failures=0;if(this.state.phase==='error')this.publish({phase:'waiting',error:undefined});
    }catch(e){
     if(signal.aborted)return;
     // The server forgot this receiver (or never knew it): register again next turn.
     if(status(e)===404)this.publish({receiver:undefined});
     failures++;this.publish({phase:'error',error:serviceProblem(e,'devices','playOn.receiverOffline','load')});
    }
    await this.sleep(failures?Math.min(60000,this.pollMs*2**Math.min(failures,5)):receiverPollDelay(this.streamConnected?RECEIVER_SAFETY_MS:this.pollMs,this.rand),signal);
   }
  })();
 }
 /** Reads one receiver inbox and works it like a poll: a new grant is asked about, a waiting
  * handoff is taken once it is genuinely playing. The event stream calls this too: the hint
  * only decides *when* to read, never what to play. */
 private refreshInbox(id:string,signal:AbortSignal):Promise<void>{
 return this.inboxWork??=this.readInbox(id,signal).finally(()=>{this.inboxWork=undefined;});
 }
 private async readInbox(id:string,signal:AbortSignal){
  const request=()=>this.api.request<{grants?:unknown[];handoffs?:unknown[]}>(encodedRoutePath("social_get_v1_receivers_id_inbox", {"id": encodeURIComponent(id)}),'GET',undefined,signal);
  const inbox=await (this.events?.resyncReads?this.events.resyncReads.run(signal,request):request());
  if(signal.aborted)return;
  const asking=(inbox.grants??[]).map(g=>parseReceiverGrant({protocolVersion:SOCIAL_PROTOCOL,serverTime:'',grant:g}))[0];
  if(asking?.id!==this.state.asking?.id)this.publish({asking});
  const handoff=(inbox.handoffs??[]).map(h=>parseHandoff({protocolVersion:SOCIAL_PROTOCOL,serverTime:'',handoff:h})).find(h=>h.outcome==='waiting'&&!this.handled.has(h.id));
  if(handoff&&!this.taking){this.handled.add(handoff.id);if(this.handled.size>64)this.handled.delete(this.handled.values().next().value!);this.taking=handoff.id;
   void this.take(handoff,signal).catch(()=>{}).finally(()=>{if(this.taking===handoff.id)this.taking=undefined;this.sleeper?.wake();});
  }
 }
 /** Receiver features subscribe to the shared feed and never open a connection. */
 private followEvents(signal:AbortSignal){
  const events=this.events!;
  const refresh=()=>{const id=this.state.receiver?.id;if(id&&!signal.aborted)void this.refreshInbox(id,signal).catch(e=>{if(status(e)===404)this.publish({receiver:undefined});});};
  const hint=events.on('receiver.inbox_changed',event=>{const value=parseReceiverInboxEvent(event.data);if(value&&(value.kind==='resync'||!value.receiverId||value.receiverId===this.state.receiver?.id))refresh();});
  const recovery=events.onResyncRead?.(async context=>{const id=this.state.receiver?.id;if(id&&!signal.aborted&&!context.signal.aborted)await this.refreshInbox(id,signal);},{priority:0,admission:'external'});
  const resync=recovery?()=>recovery.dispose():events.onResync(refresh);
  const statusOff=events.subscribeStatus(()=>{const connected=events.getStatus().connected;if(connected!==this.streamConnected){this.streamConnected=connected;this.sleeper?.wake();}});
  this.streamConnected=events.getStatus().connected;
  this.releaseEvents=()=>{hint();resync();statusOff();};
 }
 async decide(grant:ReceiverGrant,decision:'accept'|'decline'):Promise<void>{
  const id=this.state.receiver?.id;if(!id)return;
  this.publish({asking:undefined});
  try{await this.api.request<RouteResponse<"social_post_v1_receivers_id_grants_grantId_decision">>(encodedRoutePath("social_post_v1_receivers_id_grants_grantId_decision", {"id": encodeURIComponent(id), "grantId": encodeURIComponent(grant.id)}),'POST',routeBody("social_post_v1_receivers_id_grants_grantId_decision", {protocolVersion:SOCIAL_PROTOCOL,decision}));}catch{/* it is asked again on the next poll */}
 }
 /** Loads what was sent and reports readiness only once it is genuinely playing. */
 private async take(handoff:Handoff,signal:AbortSignal){
  this.publish({phase:'starting'});
  this.player.load(handoff.itemId,Number(BigInt(handoff.requestedPositionUs)/1000n)/1000);
  const deadline=Date.parse(handoff.expiresAt)||Date.now()+120000;
  while(!signal.aborted&&Date.now()<deadline){
   await wait(500,signal);
   const now=this.player.state();
   if(now.itemId===handoff.itemId&&now.playing&&now.playbackId){
    try{await this.api.request<RouteResponse<"social_post_v1_handoffs_id_readiness">>(encodedRoutePath("social_post_v1_handoffs_id_readiness", {"id": encodeURIComponent(handoff.id)}),'POST',routeBody("social_post_v1_handoffs_id_readiness", {protocolVersion:SOCIAL_PROTOCOL,readiness:'playing',receiverPlaybackId:now.playbackId,positionUs:us(now.positionSeconds),expectedRevision:handoff.revision}),signal);}catch{/* the phone keeps playing; nothing to undo here */}
    break;
   }
  }
  if(!signal.aborted)this.publish({phase:'waiting'});
 }
}
