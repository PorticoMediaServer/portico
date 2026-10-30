import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {randomId} from './random-id.ts';
import {readEventStream} from './sse.ts';
import {presentError} from './presentation/errors.ts';
import type {I18n, MessageId} from '../../i18n/src/index.ts';
import {serviceI18n} from './presentation/service-text.ts';
import {
 SOCIAL_PROTOCOL,groupCorrection,groupTargetPositionUs,parseGroupInvite,parseGroupList,parseGroupQueue,parseGroupSnapshot,parseGroupTransportReceipt,
 type Group,type GroupHostAuthority,type GroupInvite,type GroupQueue,type GroupReadiness,type GroupRepeatMode,type GroupTimeline,type GroupTransportCommand,
} from './social-playback.ts';

/** Watch Together for one signed-in viewer.
 *
 * The server owns the group's clock; each member plays its own copy and follows that clock.
 * This service keeps the room current over the event stream (resuming by ordinal, falling back
 * to a re-read when the stream is quiet), sends commands fenced on the revision it last saw, and
 * steers a local player through `GroupFollower`. A dropped stream never empties the room: what
 * was last known stays on screen while it reconnects. */

export type GroupEnterLifetime=Readonly<{signal:AbortSignal;isCurrent():boolean}>;
export type GroupApi=Readonly<{baseUrl:string;request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}>;
/** Opens the authenticated event stream; the app supplies it because only it knows the bearer. */
export type GroupStreamOpener=(path:string,lastEventId:string,signal:AbortSignal)=>Promise<Response>;
export type GroupRoomPhase='idle'|'joining'|'live'|'reconnecting'|'ended'|'left';
export type GroupSessionSnapshot=Readonly<{
 directory:readonly Group[];directoryPhase:'idle'|'loading'|'ready'|'error';directoryError?:string;
 /** The catalogue ID behind `directoryError` / `error`, for clients that render in another language later. */
 directoryErrorId?:MessageId;errorId?:MessageId;
 /** Only the server's exact wrong/unknown-code refusal for this join attempt. */
 joinFailure?:Readonly<{code:'invite_not_found';inviteCode:string}>;
 phase:GroupRoomPhase;group?:Group;queue?:GroupQueue;invite?:GroupInvite;busy:boolean;error?:string;
 /** A command the server refused because somebody in the group cannot see the title. Only the
  * host may repeat it with `allowUnavailable`, and the group is told when they do. */
 refused?:Readonly<{command:GroupTransportCommand;input:Readonly<{positionUs?:string;entryId?:string;queuePosition?:number}>}>;
 /** Add to `Date.now()` for the server's clock. */
 clockOffsetMs:number;
}>;
type Options=Readonly<{api:GroupApi;stream:GroupStreamOpener;key?:()=>string;now?:()=>number;heartbeatMs?:number;
 /** The viewer's catalogue; messages default to en-US. */
 i18n?:I18n}>;

const HEARTBEAT_MS=10000,SILENCE_MS=40000;
/** BE-MEDIA-14: NTP-style clock. Each request that answers with `serverTime` is a sample: offset =
 * serverTime − the request's midpoint, error at most half its round trip. The clock follows the
 * sample with the least round trip among the last CLOCK_SAMPLES taken within CLOCK_WINDOW_MS, so one
 * slow answer never drags the group clock. A pushed stream heartbeat has no round trip: it is used
 * only while no request sample is in the window. */
const CLOCK_SAMPLES=8,CLOCK_WINDOW_MS=120000;
type ClockSample={offsetMs:number;rttMs:number;at:number};
/** Catalogue-backed service messages: a known code or status names the problem; otherwise the
 * shared presenter, or this action's own message when the failure has no category (X-04). */
const code=(e:unknown)=>(e as {code?:string}|null)?.code??'';
const status=(e:unknown)=>(e as {status?:number}|null)?.status??0;
const path=(id:string,rest='')=>encodedRoutePath("social_get_v1_groups_id", {"id": encodeURIComponent(id)})+rest;

export class GroupSessionService{
 private api:GroupApi;private stream:GroupStreamOpener;private key:()=>string;private now:()=>number;private heartbeatMs:number;
 private state:GroupSessionSnapshot=Object.freeze<GroupSessionSnapshot>({directory:[],directoryPhase:'idle',phase:'idle',busy:false,clockOffsetMs:0});
 private entering?:AbortController;
 private listeners=new Set<()=>void>();private disposed=false;private lastCode='';private clock:ClockSample[]=[];
 private room?:{id:string;abort:AbortController;lastEventId:string;beat?:ReturnType<typeof setInterval>;heartbeat?:AbortController;heartbeatRetryAt?:number;heartbeatFailures?:number;retry?:ReturnType<typeof setTimeout>;watchdog?:ReturnType<typeof setTimeout>;rereading?:Promise<void>;queueReading?:Promise<void>};
 constructor(options:Options){this.api=options.api;this.stream=options.stream;this.key=options.key??(()=>randomId());this.now=options.now??(()=>Date.now());this.heartbeatMs=options.heartbeatMs??HEARTBEAT_MS;this.ownI18n=options.i18n;}
 private ownI18n?:I18n;
 private get i18n():I18n{return this.ownI18n??serviceI18n();}
 /** Use the viewer's catalogue from now on (the region can change after the service starts). */
 setI18n(i18n:I18n){this.ownI18n=i18n;}
 private say(e:unknown,fallback:MessageId):MessageId|{text:string;id?:MessageId}{
  const own=(e as {messageId?:unknown}|null)?.messageId;if(typeof own==='string')return own as MessageId;
  const p=presentError(e,'together',{operation:'action',i18n:this.i18n});
  return p.category==='unknown'?fallback:{text:p.body,id:p.messageId as MessageId};
 }
 private problem(what:MessageId|{text:string;id?:MessageId},field:'error'|'directoryError'='error'){
  const text=typeof what==='string'?this.i18n.t(what):what.text,id=typeof what==='string'?what:what.id;
  return field==='error'?{error:text,errorId:id}:{directoryError:text,directoryErrorId:id};
 }
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(patch:Partial<GroupSessionSnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...patch});this.listeners.forEach(f=>f());}
 dispose(){this.close('idle');this.disposed=true;this.listeners.clear();}
 serverNow(){return this.now()+this.state.clockOffsetMs;}

 /** Groups this viewer belongs to. A failed refresh keeps the list it had. */
 async refreshDirectory():Promise<void>{
  if(this.state.directoryPhase==='idle')this.publish({directoryPhase:'loading'});
  try{this.publish({directory:parseGroupList(await this.api.request<RouteResponse<"social_get_v1_groups">>(routePath("social_get_v1_groups"))).filter(g=>g.state!=='ended'&&g.state!=='failed'),directoryPhase:'ready',directoryError:undefined});}
  catch(e){this.publish({directoryPhase:this.state.directory.length?'ready':'error',...(status(e)===404||status(e)===503?this.problem('together.error.unavailable','directoryError'):this.problem(this.say(e,'together.error.load'),'directoryError'))});}
 }

 async create(input:{name:string;displayName:string;hostAuthority:GroupHostAuthority},lifetime?:GroupEnterLifetime):Promise<boolean>{
  // The server binds the group to this signed-in device (B8a): hosting needs no playback authority.
  return this.enter(async signal=>parseGroupSnapshot(await this.api.request<RouteResponse<"social_post_v1_groups">>(routePath("social_post_v1_groups"),'POST',routeBody("social_post_v1_groups", {protocolVersion:SOCIAL_PROTOCOL,name:input.name.trim(),displayName:input.displayName.trim(),hostAuthority:input.hostAuthority}),signal)),'together.error.create',lifetime);
 }
 async join(inviteCode:string,displayName:string,lifetime?:GroupEnterLifetime):Promise<boolean>{
  const tidy=inviteCode.replace(/[\s-]/g,'').toUpperCase();
  return this.enter(async signal=>parseGroupSnapshot(await this.api.request<RouteResponse<"social_post_v1_groups_join">>(routePath("social_post_v1_groups_join"),'POST',routeBody("social_post_v1_groups_join", {protocolVersion:SOCIAL_PROTOCOL,code:tidy,displayName:displayName.trim()}),signal)),'together.error.join',lifetime,tidy);
 }
 /** Re-enters a group this viewer already belongs to. */
 async open(groupId:string,lifetime?:GroupEnterLifetime):Promise<boolean>{
  if(lifetime?.signal.aborted||lifetime?.isCurrent()===false)return false;
  if(this.room?.id===groupId&&this.state.group)return true;
  return this.enter(async signal=>parseGroupSnapshot(await this.api.request<unknown>(path(groupId),'GET',undefined,signal)),'together.error.open',lifetime);
 }
 private async enter(read:(signal:AbortSignal)=>Promise<{serverTime:string;group:Group}>,fallback:MessageId,lifetime?:GroupEnterLifetime,joinCode?:string):Promise<boolean>{
  if(this.disposed||lifetime?.signal.aborted||lifetime?.isCurrent()===false)return false;
  this.close('joining');const turn=new AbortController();this.entering=turn;
  const current=()=>!this.disposed&&this.entering===turn&&!turn.signal.aborted&&!lifetime?.signal.aborted&&lifetime?.isCurrent()!==false;
  const cancel=()=>{turn.abort();if(this.entering===turn){this.entering=undefined;this.publish({phase:'idle',busy:false,error:undefined});}};
  lifetime?.signal.addEventListener('abort',cancel,{once:true});
  this.publish({busy:true,error:undefined,invite:undefined,queue:undefined});
  try{
   const sent=this.now();const snapshot=await read(turn.signal);if(!current())return false;
   this.adopt(snapshot,{sent,received:this.now()});if(this.state.phase!=='ended')this.follow(snapshot.group.id);return true;
  }catch(e){if(current())this.publish({phase:'idle',...this.problem(this.say(e,fallback)),joinFailure:joinCode!==undefined&&code(e)==='invite_not_found'?Object.freeze({code:'invite_not_found' as const,inviteCode:joinCode}):undefined});return false;}
  finally{lifetime?.signal.removeEventListener('abort',cancel);if(this.entering===turn){const valid=current();this.entering=undefined;this.publish({busy:false,...(!valid&&!this.room?{phase:'idle' as const}:{})});}}
 }
 /** `timing`: when the request that brought this snapshot was sent and answered (absent for pushed frames). */
 private adopt(snapshot:{serverTime:string;group:Group},timing?:{sent:number;received:number}){
  const current=this.state.group,incoming=snapshot.group;
 if(current?.id===incoming.id&&(BigInt(incoming.revision)<BigInt(current.revision)||BigInt(incoming.playbackRevision)<BigInt(current.playbackRevision)||BigInt(incoming.eventOrdinal)<BigInt(current.eventOrdinal)))return;
 const at=Date.parse(snapshot.serverTime);
  const ended=snapshot.group.state==='ended'||snapshot.group.state==='failed';
  // Made host by someone else: the group follows no device until this one's heartbeat binds it.
  const promoted=!!this.state.group&&this.state.group.id===snapshot.group.id&&!this.state.group.permissions.isHost&&snapshot.group.permissions.isHost;
  this.publish({group:snapshot.group,queue:snapshot.group.queue??this.state.queue,phase:ended?'ended':this.state.phase==='joining'?'live':this.state.phase,...this.sample(at,timing)});
  if(ended)this.close('ended');
  else if(promoted&&this.room)this.beat(this.room);
 }

 /** Records one clock sample and returns the offset to publish (the least round trip in the window). */
 private sample(at:number,timing?:{sent:number;received:number}):{clockOffsetMs?:number}{
  if(!Number.isFinite(at))return {};
  const now=this.now();
  this.clock=this.clock.filter(x=>now-x.at<=CLOCK_WINDOW_MS);
  if(!timing||!(timing.received>=timing.sent)){
   // A pushed heartbeat: one-way, so its error is unknown; used only when nothing better is at hand.
   return this.clock.length?{}:{clockOffsetMs:at-now};
  }
  this.clock.push({offsetMs:at-(timing.sent+timing.received)/2,rttMs:timing.received-timing.sent,at:now});
  if(this.clock.length>CLOCK_SAMPLES)this.clock.splice(0,this.clock.length-CLOCK_SAMPLES);
  let best=this.clock[0]!;for(const x of this.clock)if(x.rttMs<best.rttMs)best=x;
  return {clockOffsetMs:Math.round(best.offsetMs)};
 }
 private follow(id:string){
  const room={id,abort:new AbortController(),lastEventId:''} as NonNullable<GroupSessionService['room']>;this.room=room;
  room.beat=setInterval(()=>this.beat(room),this.heartbeatMs);
  void this.connect(room,0);
 }
 /** A host's heartbeat also binds the group to the device it comes from. */
 private beat(room:NonNullable<GroupSessionService['room']>){
  if(this.room!==room||room.heartbeat||room.abort.signal.aborted||this.now()<(room.heartbeatRetryAt??0))return;
  const sent=this.now(),controller=new AbortController();room.heartbeat=controller;
  const abort=()=>controller.abort();room.abort.signal.addEventListener('abort',abort,{once:true});
  // Heartbeats are small lease updates: one outstanding request per room, bounded
  // even when the transport stalls. Leaving cancels the request with the room.
  const timer=setTimeout(abort,20000);
  void this.api.request<unknown>(path(room.id,'/heartbeat'),'POST',undefined,controller.signal).then(raw=>{
   if(this.room!==room||controller.signal.aborted)return;
   this.adopt(parseGroupSnapshot(raw),{sent,received:this.now()});
   room.heartbeatFailures=0;room.heartbeatRetryAt=undefined;
  }).catch(error=>{
   if(this.room!==room||room.abort.signal.aborted)return;
   room.heartbeatFailures=(room.heartbeatFailures??0)+1;
   const after=(error as {retryAfterSeconds?:unknown})?.retryAfterSeconds;
   const directed=typeof after==='number'&&Number.isFinite(after)&&after>0?after*1000:0;
   room.heartbeatRetryAt=this.now()+Math.max(directed,Math.min(30000,1000*2**Math.min(room.heartbeatFailures-1,5)));
  }).finally(()=>{
   clearTimeout(timer);room.abort.signal.removeEventListener('abort',abort);
   if(room.heartbeat===controller)room.heartbeat=undefined;
  });
 }
 private async connect(room:NonNullable<GroupSessionService['room']>,attempt:number){
  if(this.room!==room||room.abort.signal.aborted)return;
  const alive=()=>{clearTimeout(room.watchdog);room.watchdog=setTimeout(()=>turn.abort(),SILENCE_MS);};
  const turn=new AbortController();const stop=()=>turn.abort();room.abort.signal.addEventListener('abort',stop,{once:true});
  let refused=0,retryAfterMs=0;
  try{
   alive();
   const response=await this.stream(path(room.id,'/events'),room.lastEventId,turn.signal);
   if(!response.ok){
    refused=response.status;
    const after=response.headers.get('Retry-After')?.trim();
    const wait=after&&/^\d+$/.test(after)?Number(after)*1000:after?Date.parse(after)-this.now():0;
    if(Number.isFinite(wait)&&wait>0)retryAfterMs=wait;
   }
   alive();
   await readEventStream(response,event=>{
    if(this.room!==room)return;alive();attempt=0;
    if(event.id&&/^(0|[1-9][0-9]{0,18})$/.test(event.id)&&(!room.lastEventId||BigInt(event.id)>BigInt(room.lastEventId)))room.lastEventId=event.id;
    if(this.state.phase==='reconnecting'||this.state.phase==='joining')this.publish({phase:'live',error:undefined});
    this.receive(room,event.event,event.data);
   },turn.signal);
  }catch(e){refused=refused||status(e);}
  finally{turn.abort();clearTimeout(room.watchdog);room.abort.signal.removeEventListener('abort',stop);}
  if(this.room!==room||room.abort.signal.aborted)return;
  // Refused outright: this viewer is no longer in the group (removed, or it ended while away).
  if(refused===403||refused===404||refused===410){void this.reread(room);if(refused!==403){this.publish({phase:'ended'});this.close('ended');return;}}
  this.publish({phase:'reconnecting'});
  const delay=Math.max(retryAfterMs,Math.min(15000,500*2**Math.min(attempt,5))*(0.75+Math.random()/2));
  // Keep a long server floor without overflowing the platform's timer range.
  const wait=(remaining:number)=>{room.retry=setTimeout(()=>{
   if(this.room!==room||room.abort.signal.aborted)return;
   if(remaining>2147483647)wait(remaining-2147483647);else void this.connect(room,attempt+1);
  },Math.min(remaining,2147483647));};
  wait(delay);
 }
 private receive(room:NonNullable<GroupSessionService['room']>,kind:string,data:string){
  let body:unknown;try{body=JSON.parse(data);}catch{return;}
  try{
   if(kind==='group.snapshot'){this.adopt(parseGroupSnapshot(body));return;}
   if(kind==='heartbeat'){const clock=this.sample(Date.parse((body as {serverTime?:string}).serverTime??''));if(clock.clockOffsetMs!==undefined)this.publish(clock);return;}
   if(kind==='resume-gap'){room.lastEventId='';return;}
   if(kind==='group.ended'){void this.reread(room).finally(()=>{if(this.room===room){this.publish({phase:'ended'});this.close('ended');}});return;}
   // The clock is applied the moment it arrives; everything else in the room is re-read, because
   // members and the queue are projected per viewer and cannot ride in a shared event.
   if(kind==='group.readiness'&&this.state.group){
 const v=body as {memberId?:string;readiness?:GroupReadiness;positionUs?:string;reportedAt?:string|null;private?:boolean;displayName?:string;username?:string;aggregate?:Group['readiness']};
 if(v.memberId&&v.readiness&&v.positionUs&&v.aggregate)this.publish({group:Object.freeze({...this.state.group,readiness:v.aggregate,members:this.state.group.members.map(m=>m.id===v.memberId?Object.freeze({...m,readiness:v.readiness!,private:v.private??m.private,displayName:v.displayName??m.displayName,username:(v.private??m.private)?undefined:typeof v.username==='string'?v.username:m.username,positionUs:(v.private??m.private)?'0':v.positionUs!,reportedAt:(v.private??m.private)?null:v.reportedAt??m.reportedAt,presence:'connected' as const}):m)})});
 return;
 }
 if(kind==='group.transport'&&this.state.group){const timeline=timelineOf(body,this.state.group.timeline);if(timeline){const v=body as {groupState?:Group['state'];revision?:string;playbackRevision?:string};
 if(v.playbackRevision&&BigInt(v.playbackRevision)>=BigInt(this.state.group.playbackRevision)){const state=v.groupState??(timeline.waitUntil?'waiting':timeline.state==='playing'?'playing':'paused');this.publish({group:Object.freeze({...this.state.group,timeline,state,revision:v.revision??this.state.group.revision,playbackRevision:v.playbackRevision})});}}}
   if(kind==='group.queue')void this.readQueue(room);
   void this.reread(room);
  }catch{/* a frame this client cannot read is skipped; the next re-read corrects the room */}
 }
 private reread(room:NonNullable<GroupSessionService['room']>):Promise<void>{
  return room.rereading??=(async()=>{
   try{const sent=this.now();const snapshot=parseGroupSnapshot(await this.api.request<unknown>(path(room.id),'GET',undefined,room.abort.signal));if(this.room===room)this.adopt(snapshot,{sent,received:this.now()});}
   catch{/* the room keeps what it had */}finally{room.rereading=undefined;}
  })();
 }
 private readQueue(room:NonNullable<GroupSessionService['room']>):Promise<void>{
  return room.queueReading??=(async()=>{
   try{const raw=await this.api.request<{queue?:unknown}>(path(room.id,'/queue'),'GET',undefined,room.abort.signal);if(this.room===room)this.publish({queue:parseGroupQueue((raw as {queue?:unknown}).queue??raw)});}
   catch{}finally{room.queueReading=undefined;}
  })();
 }
 refreshQueue(){return this.room?this.readQueue(this.room):Promise.resolve();}
 /** Form owners call on edit/replacement; a late failed join cannot refill the new form. */
 clearJoinFailure(){if(this.entering)this.close('idle');this.publish({joinFailure:undefined,error:undefined,errorId:undefined});}
 private close(phase:GroupRoomPhase){
  this.entering?.abort();this.entering=undefined;
  const room=this.room;this.room=undefined;
  if(room){room.abort.abort();clearInterval(room.beat);clearTimeout(room.retry);clearTimeout(room.watchdog);}
  if(phase==='idle'||phase==='joining'||phase==='left')this.publish({phase,busy:false,group:undefined,queue:undefined,invite:undefined,joinFailure:undefined});else this.publish({phase,busy:false,joinFailure:undefined});
 }

 private async act<T>(work:(group:Group)=>Promise<T>,fallback:MessageId):Promise<T|undefined>{
  const group=this.state.group,room=this.room;if(!group||!room)return undefined;
  this.publish({busy:true,error:undefined,refused:undefined});this.lastCode='';
  try{const value=await work(group);return this.room===room&&!room.abort.signal.aborted&&!this.disposed?value:undefined;}
  catch(e){
   if(this.room!==room||room.abort.signal.aborted||this.disposed)return undefined;
   // A stale revision means somebody else moved the group first. Show what it is now and let the
   // person decide again; resending blindly is how a retried Play undoes someone's Pause.
   if(code(e)==='revision_conflict'){await this.reread(room);if(this.room===room)this.publish({...this.problem('together.error.changed')});}
   else this.publish({...this.problem(this.say(e,fallback))});
   this.lastCode=code(e);return undefined;
  }finally{if(this.room===room)this.publish({busy:false});}
 }
 async transport(command:GroupTransportCommand,input:{positionUs?:string;entryId?:string;queuePosition?:number;allowUnavailable?:boolean}={}):Promise<boolean>{
  const receipt=await this.act(async group=>parseGroupTransportReceipt(await this.api.request<unknown>(path(group.id,'/transport'),'POST',{protocolVersion:SOCIAL_PROTOCOL,idempotencyKey:this.key(),expectedRevision:group.playbackRevision,command,...(input.positionUs!==undefined?{positionUs:input.positionUs}:{}),...(input.entryId?{entryId:input.entryId}:{}),...(input.queuePosition!==undefined?{queuePosition:input.queuePosition}:{}),...(input.allowUnavailable?{allowUnavailable:true}:{})})),'together.error.notReached');
  if(receipt&&this.state.group)this.publish({group:Object.freeze({...this.state.group,timeline:receipt.timeline,state:receipt.groupState,revision:receipt.revision,playbackRevision:receipt.playbackRevision})});
  else if(!receipt&&this.lastCode==='media_no_longer_accessible'&&!input.allowUnavailable)this.publish({...this.problem('together.error.memberCantSee'),refused:Object.freeze({command,input:Object.freeze({positionUs:input.positionUs,entryId:input.entryId,queuePosition:input.queuePosition})})});
  if(receipt&&this.room)void this.reread(this.room);
  return !!receipt;
 }
 async settings(input:{shuffleEnabled?:boolean;repeatMode?:GroupRepeatMode}):Promise<boolean>{
  const done=await this.act(async group=>{await this.api.request<unknown>(path(group.id,'/settings'),'POST',{protocolVersion:SOCIAL_PROTOCOL,idempotencyKey:this.key(),expectedRevision:group.settingsRevision,...input});return true;},'together.error.setting');
  if(done&&this.room)void this.reread(this.room);return !!done;
 }
 async queueAdd(itemIds:readonly string[],replace=false):Promise<boolean>{return this.queueChange({operation:replace?'replace':'append',itemIds},'together.error.queueAdd');}
 async queueRemove(entryId:string):Promise<boolean>{return this.queueChange({operation:'remove',entryId},'together.error.queueRemove');}
 async queueMove(entryId:string,destinationEntryId:string,placement:'before'|'after'):Promise<boolean>{return this.queueChange({operation:'move',entryId,destinationEntryId,placement},'together.error.queueMove');}
 private async queueChange(body:Record<string,unknown>,fallback:MessageId):Promise<boolean>{
  const room=this.room;
  const done=await this.act(async group=>{
   const send=(revision:string)=>this.api.request<unknown>(path(group.id,'/queue'),'POST',{protocolVersion:SOCIAL_PROTOCOL,idempotencyKey:this.key(),expectedRevision:revision,...body});
   // Adding to a queue commutes with whatever else changed, so one retry on the new revision is safe.
   try{await send(this.state.queue?.revision??group.queueRevision);}catch(e){if(code(e)!=='revision_conflict'||!room)throw e;await this.readQueue(room);await send(this.state.queue?.revision??group.queueRevision);}
   return true;
  },fallback);
  if(done&&room){void this.readQueue(room);void this.reread(room);}
  return !!done;
 }
 /** Puts a title in front of the group: queued, then selected. Selecting never starts it; the
  * group prepares, and whoever may control presses Play once people are ready. */
 async watch(itemId:string):Promise<boolean>{
  const room=this.room;if(!room||!await this.queueAdd([itemId])||this.room!==room)return false;
  await this.readQueue(room);if(this.room!==room)return false;await this.reread(room);if(this.room!==room)return false;
  const entry=[...(this.state.queue?.entries??[])].reverse().find(e=>e.itemId===itemId);
  return entry?this.transport('load',{entryId:entry.entryId}):false;
 }
 /** This member's own state: evidence for the group, never a command. Failures are silent. */
 async readiness(readiness:GroupReadiness,positionUs:string,ended=false):Promise<void>{
  const room=this.room;if(!room)return;
  try{await this.api.request<unknown>(path(room.id,'/readiness'),'POST',{protocolVersion:SOCIAL_PROTOCOL,readiness,positionUs,playbackRevision:this.state.group?.playbackRevision,ended},room.abort.signal);}catch{}
 }
 async invite(input:{expiresInMs?:number;maxUses?:number}={}):Promise<boolean>{
  const invite=await this.act(async group=>parseGroupInvite(await this.api.request<unknown>(path(group.id,'/invites'),'POST',{protocolVersion:SOCIAL_PROTOCOL,expiresInMs:input.expiresInMs??900_000,maxUses:input.maxUses??8})),'together.error.invite');
  if(invite)this.publish({invite});return !!invite;
 }
 /** Hands the host role to another member, or (while the host is away) claims it for this one. */
 async makeHost(memberId:string):Promise<boolean>{
  const mine=this.state.group?.viewerMemberId===memberId;
  const done=await this.act(async group=>{
   const body={protocolVersion:SOCIAL_PROTOCOL,memberId,expectedRevision:group.governanceRevision};
   // Claiming binds this device at once; handing over leaves the incoming host's heartbeat to bind its own.
   const sent=this.now();
   const room=this.room,snapshot=parseGroupSnapshot(await this.api.request<unknown>(path(group.id,'/host-transfer'),'POST',body,room?.abort.signal));
   if(this.room!==room||room?.abort.signal.aborted)return false;
   this.adopt(snapshot,{sent,received:this.now()});
   return true;
  },'together.error.host');
  return !!done;
 }
 async leave():Promise<void>{
  const room=this.room;if(!room)return;
  this.close('left');
  try{await this.api.request<unknown>(path(room.id,'/leave'),'POST');}catch{/* the server ends a silent membership on its own */}
  void this.refreshDirectory();
 }
 async end():Promise<boolean>{
  const done=await this.act(async group=>{await this.api.request<unknown>(path(group.id,'/end'),'POST',{protocolVersion:SOCIAL_PROTOCOL,expectedRevision:group.governanceRevision});return true;},'together.error.end');
  if(done){this.publish({phase:'ended',...(this.state.group?{group:Object.freeze({...this.state.group,state:'ended' as const,endedReason:'host-ended'})}:{})});this.close('ended');void this.refreshDirectory();}
  return !!done;
 }
 /** Says something in the room's own voice, e.g. why a button did nothing. */
 notice(message:string){this.publish({error:message});}
 /** Forgets an ended or left room so the directory shows again. */
 dismiss(){this.close('idle');this.publish({error:undefined});}
}

function timelineOf(body:unknown,previous:GroupTimeline):GroupTimeline|undefined{
 const v=body as Record<string,unknown>|null;if(!v||typeof v!=='object')return undefined;
 const rate=v.rate as {numerator?:unknown;denominator?:unknown}|undefined;
 if(typeof v.anchorPositionUs!=='string'||!/^(0|[1-9][0-9]{0,18})$/.test(v.anchorPositionUs)||typeof v.anchorAt!=='string'||!['idle','playing','paused','stopped'].includes(v.state as string))return undefined;
 const valid=(x:unknown)=>typeof x==='string'&&/^[1-9][0-9]{0,18}$/.test(x);
 return Object.freeze({itemId:typeof v.itemId==='string'?v.itemId:previous.itemId,currentEntryId:typeof v.currentEntryId==='string'?v.currentEntryId:previous.currentEntryId,state:v.state as GroupTimeline['state'],anchorPositionUs:v.anchorPositionUs,anchorAt:v.anchorAt,
  rate:rate&&valid(rate.numerator)&&valid(rate.denominator)?Object.freeze({numerator:rate.numerator as string,denominator:rate.denominator as string}):previous.rate,waitUntil:typeof v.waitUntil==='string'?v.waitUntil:null,queuePosition:Number.isSafeInteger(v.queuePosition)?v.queuePosition as number:previous.queuePosition});
}

/** What a follower needs from whichever player the app has. Positions are seconds. */
export type GroupPlayer=Readonly<{
 /** Optional actual room-occurrence adoption. When supplied, a same-title source from a
  * different room/entry must be admitted afresh instead of inheriting its controls. */
 occurrence?(): Readonly<{groupId:string;entryId:string}> | null;
 /** The item loaded now, its position, and whether it is actually moving and able to.
  * `seeking` is true while a seek the player was asked for has not landed yet, and false once it
  * has landed or failed. Only the player knows this: one that leaves it out is given a fixed wait
  * after each seek, and its seeks are never aimed ahead.
  * `durationSeconds` is the length of the title, when known. Without it nothing is aimed ahead
  * either, because where the end is cannot be known. */
 state():{itemId:string;positionSeconds:number;playing:boolean;buffering:boolean;ready:boolean;seeking?:boolean;durationSeconds?:number};
 load(itemId:string,positionSeconds:number,playing:boolean):void;
 play():void;pause():void;seek(positionSeconds:number):void;
 /** 1 restores normal speed. A player that cannot change speed may ignore it; drift then resolves by seeking. */
 setRate(rate:number):void;
}>;

/** How long the follower gives a seek of its own before judging the player again: until the
 * player says the seek is over, or for this whole time when it does not say. Longer than the
 * player's own seek timeout, so by then the seek has landed or failed. It is also the slowest seek
 * the follower compensates for; a slower one is asked again and the member reports lagging. */
const SEEK_SETTLE_MS=20000;
/** A seek aimed ahead must leave at least this much of the title after it, and at least as much as
 * it was aimed ahead by: landing early must never carry the player to the end before the group. */
const SEEK_END_ROOM_SECONDS=5;

/** Keeps one local player on the group's clock, and tells the group how this member is doing.
 * It corrects gently (a small speed change) before it corrects visibly (a seek), in the bands
 * the server publishes, and it never commands the group.
 *
 * A seek takes time, and the group keeps moving while it does. The follower lets each seek of its
 * own finish before judging where the player is, and it learns how far behind a moving group this
 * player's seeks land: the next one is aimed that far ahead, so it lands with the group instead of
 * behind it again. It aims ahead only for a player that says when its seeks land, and only where
 * that leaves room before the end of the title. */
export class GroupFollower{
 private timer?:ReturnType<typeof setInterval>;private correctingUntil=0;private lastReport=0;private lastReadiness='';private loadedFor='';private unsubscribe?:()=>void;
 /** The follower's own seek that is not known to be over: when it was asked, the group clock it
  * was aimed at, and the position asked for. */
 private own?:{at:number;clock:string;aim:number};
 /** Seconds this player's seeks have been landing behind a moving group. */
 private lead=0;
 private epoch='';private endReported='';
 private service:GroupSessionService;private player:GroupPlayer;private now:()=>number;
 constructor(service:GroupSessionService,player:GroupPlayer,now:()=>number=()=>Date.now()){this.service=service;this.player=player;this.now=now;}
 start(){if(this.timer)return;this.timer=setInterval(()=>this.tick(),1000);this.unsubscribe=this.service.subscribe(()=>this.tick());this.tick();}
 stop(){clearInterval(this.timer);this.timer=undefined;this.unsubscribe?.();this.unsubscribe=undefined;this.player.setRate(1);}
 /** Where the group is now, in seconds, or undefined when nothing is loaded. */
 target():number|undefined{
  const group=this.service.getSnapshot().group;if(!group||!group.timeline.itemId)return undefined;
  const target=Number(groupTargetPositionUs(group.timeline,Date.parse(group.timeline.anchorAt),this.service.serverNow())/1000n)/1000;
 const local=this.player.state(),length=local.itemId===group.timeline.itemId?local.durationSeconds:undefined;
 return length!==undefined&&Number.isFinite(length)&&length>0?Math.min(target,length):target;
 }
 private tick(){
  const snapshot=this.service.getSnapshot(),group=snapshot.group;
  if(!group||(snapshot.phase!=='live'&&snapshot.phase!=='reconnecting'))return;
  const timeline=group.timeline,target=this.target();
 if(this.epoch!==group.playbackRevision){if(this.correctingUntil){this.correctingUntil=0;this.player.setRate(1);}this.epoch=group.playbackRevision;this.lastReadiness='';}
 if(!timeline.itemId&&timeline.currentEntryId){this.player.pause();this.report('buffering',0);return;}
  if(target===undefined||timeline.state==='idle'||timeline.state==='stopped'){if(timeline.state==='stopped'&&this.player.state().playing)this.player.pause();return;}
  const local=this.player.state(),wantPlaying=timeline.state==='playing';
  const occurrence=this.player.occurrence?.();
  if(local.itemId!==timeline.itemId||(this.player.occurrence&&(!occurrence||occurrence.groupId!==group.id||occurrence.entryId!==timeline.currentEntryId))){
   // Load once per entry; a player still starting up is not asked again every second.
   const key=group.id+'/'+timeline.itemId+'/'+timeline.currentEntryId;
   if(this.loadedFor!==key){this.loadedFor=key;this.own=undefined;this.lead=0;this.player.load(timeline.itemId,target,wantPlaying);}
   this.report('buffering',local.positionSeconds);return;
  }
  this.loadedFor=group.id+'/'+timeline.itemId+'/'+timeline.currentEntryId;
  const length=local.durationSeconds;
 if(length!==undefined&&Number.isFinite(length)&&length>0&&local.positionSeconds>=length-0.25&&!local.playing&&target>=length-0.25){
 this.player.setRate(1);this.report('ready',length);
 if(group.permissions.isHost&&timeline.state==='playing'&&this.endReported!==group.playbackRevision){this.endReported=group.playbackRevision;void this.service.readiness('ready',String(Math.round(length*1e6)),true);}
 return;
 }
 if(!local.ready){this.report('buffering',local.positionSeconds);return;}
  if(wantPlaying&&!local.playing&&!local.buffering)this.player.play();
  if(!wantPlaying&&local.playing)this.player.pause();
  const driftUs=BigInt(Math.round((target-local.positionSeconds)*1e6)),targetUs=BigInt(Math.round(target*1e6));
  const correction=groupCorrection(group.sync,driftUs,targetUs);
  const at=this.now();
  // A seek that has not landed is left to finish: the group moves on meanwhile, so seeking again
  // would restart it, and a seek slower than the seek band would never complete. A player that is
  // buffering is left alone too. Where the player is gets judged once it is somewhere.
  const clock=timeline.state+'/'+timeline.anchorAt+'/'+timeline.anchorPositionUs,band=group.sync.seekAtOrOverMs/1000;
  // A member that is known to be seeking or buffering says so. One that is only being given time (its
  // player does not say when a seek is over) reports as it always has, from where it appears to be.
  if(this.settling(local,wantPlaying,target,clock,band,at)||local.buffering){this.report(local.seeking||local.buffering?'buffering':correction.action==='none'||correction.action==='rate'?'ready':'lagging',local.positionSeconds);return;}
  if(correction.action==='seek'||(!wantPlaying&&correction.action==='rate'))this.seek(local,target,wantPlaying,clock,at);
  else if(correction.action==='rate'&&wantPlaying){if(!this.correctingUntil){this.correctingUntil=at+(correction.holdMs??4000);this.player.setRate(correction.rate??1);}else if(at>this.correctingUntil)this.seek(local,target,wantPlaying,clock,at);}
  else if(this.correctingUntil){this.player.setRate(1);this.correctingUntil=0;}
  this.report(correction.action==='none'||correction.action==='rate'?'ready':'lagging',local.positionSeconds);
 }
 /** A corrective seek at normal speed. While the group is moving it is aimed ahead by what this
  * player's seeks have cost, but never near the end of the title: there, or when the length of
  * the title is not known, it goes to where the group is and no further. */
 private seek(local:ReturnType<GroupPlayer['state']>,target:number,moving:boolean,clock:string,at:number){
  const length=local.durationSeconds,room=length!==undefined&&Number.isFinite(length)?length-(target+this.lead):0;
  const aim=moving&&this.lead>0&&room>=Math.max(this.lead,SEEK_END_ROOM_SECONDS)?target+this.lead:target;
  this.player.setRate(1);this.correctingUntil=0;this.own={at,clock,aim};
  this.player.seek(aim);
 }
 /** Whether the follower's last seek (or one the player reports) is still to be waited for. */
 private settling(local:ReturnType<GroupPlayer['state']>,moving:boolean,target:number,clock:string,band:number,at:number):boolean{
  const own=this.own;
  if(!own)return local.seeking===true;
  // The group was moved, paused or resumed since: that seek no longer answers where the group is,
  // so the player is judged afresh, and may be sent somewhere else at once.
  if(own.clock!==clock){this.own=undefined;return false;}
  const waited=at-own.at;
  // Only the player can say its seek is over. A player that does not say is given the whole wait:
  // its position moving proves nothing, since some players go on reporting where they were.
  if(local.seeking!==false&&waited<SEEK_SETTLE_MS)return true;
  this.own=undefined;
  // It said so, in time, and is where it was sent (a seek that failed leaves it somewhere else):
  // how far behind or ahead of the moving group that is adjusts the lead for the next seek.
  if(local.seeking===false&&waited<SEEK_SETTLE_MS&&moving&&Math.abs(local.positionSeconds-own.aim)<band)this.lead=Math.min(SEEK_SETTLE_MS/1000,Math.max(0,this.lead+target-local.positionSeconds));
  return false;
 }
 private report(readiness:GroupReadiness,positionSeconds:number){
  const at=this.now();
  // Often enough that the server never counts this member stale (30 s), and at once on a change.
  if(readiness===this.lastReadiness&&at-this.lastReport<10000)return;
  this.lastReadiness=readiness;this.lastReport=at;
  void this.service.readiness(readiness,String(Math.max(0,Math.round(positionSeconds*1e6))));
 }
}
