import {clockMs, counter, invalidState, record, seconds, wireId} from './validation.ts';
import {queueScope, type QueueScope} from './queues.ts';
import {playbackFence, sameFence, sameScope, type QueuePlaybackFence} from './identity.ts';
export type GroupTimeline = Readonly<{itemId:string;currentEntryId:string;state:'idle'|'playing'|'paused'|'stopped';anchorPositionUs:string;anchorAt:string;rate:Readonly<{numerator:string;denominator:string}>;queuePosition:number;waitUntil:string|null}>;
export type GroupSync = Readonly<{noCorrectionUnderMs:number;rateCorrectionMinimum:string;rateCorrectionMaximum:string;rateCorrectionMaxMs:number;seekAtOrOverMs:number}>;
export function groupTargetPositionUs(timeline:GroupTimeline,serverTimeMs:number,atMs:number):bigint{
 const anchor=BigInt(timeline.anchorPositionUs);
 if(timeline.state!=='playing')return anchor;
 const elapsed=BigInt(Math.max(0,Math.round(atMs-serverTimeMs)));
 return anchor+elapsed*1000n*BigInt(timeline.rate.numerator)/BigInt(timeline.rate.denominator);
}

export type GroupCorrection=Readonly<{action:'none'|'rate'|'seek';rate?:number;seekToUs?:string;holdMs?:number}>;

/** Which correction the §13 bands call for, given the drift in microseconds
 * (positive when the member is behind the group). A client applies this rather
 * than inventing its own thresholds. */
export function groupCorrection(sync:GroupSync,driftUs:bigint,targetUs:bigint):GroupCorrection{
 const magnitude=driftUs<0n?-driftUs:driftUs;
 const ms=Number(magnitude/1000n);
 if(ms<sync.noCorrectionUnderMs)return Object.freeze({action:'none'});
 if(ms>=sync.seekAtOrOverMs)return Object.freeze({action:'seek',seekToUs:targetUs.toString()});
 const bound=driftUs>0n?Number(sync.rateCorrectionMaximum):Number(sync.rateCorrectionMinimum);
 return Object.freeze({action:'rate',rate:bound,holdMs:sync.rateCorrectionMaxMs});
}

/** Clock sampling extracted from GroupSessionService; eight recent samples, least RTT wins. */
export class GroupClock {
  private samples: {offsetMs:number;rttMs:number;at:number}[]=[];
  private offset=0; private lastAt=-1;
  get offsetMs():number { return this.offset; }
  sample(serverTimeMs:number,nowMs:number,timing?:Readonly<{sent:number;received:number}>):number {
    clockMs(nowMs);if(nowMs<this.lastAt)return this.offset;
    this.lastAt=nowMs;if(!Number.isFinite(serverTimeMs))return this.offset;
    this.samples=this.samples.filter(x=>nowMs-x.at<=120000);
    if(!timing || !Number.isFinite(timing.sent)||!Number.isFinite(timing.received)||timing.received<timing.sent) {
      if(!this.samples.length)this.offset=serverTimeMs-nowMs;
      return this.offset;
    }
    this.samples.push({offsetMs:serverTimeMs-(timing.sent+timing.received)/2,rttMs:timing.received-timing.sent,at:nowMs});
    if(this.samples.length>8)this.samples.shift();
    this.offset=Math.round(this.samples.reduce((best,s)=>s.rttMs<best.rttMs?s:best).offsetMs);
    return this.offset;
  }
  reset():void { this.samples=[];this.offset=0;this.lastAt=-1; }
}
export type WatchTogetherFence=Readonly<{scope:QueueScope;roomId:string;epoch:number}>;
export type WatchTogetherState=Readonly<{
  id:string;revision:string;playbackRevision:string;eventOrdinal:string;
  phase:'live'|'reconnecting'|'ended';hostPresence:'connected'|'reconnecting'|'absent';
  isHost:boolean;canControl:boolean;waiting:boolean;timeline:GroupTimeline;sync:GroupSync;
}>;
export type FollowerLocal=Readonly<{fence:QueuePlaybackFence;positionSeconds:number;playing:boolean;buffering:boolean;ready:boolean;seeking?:boolean;durationSeconds?:number}>;
export type FollowerEffect=Readonly<{fence:QueuePlaybackFence;room:WatchTogetherFence;playbackRevision:string} & (
  {kind:'load';itemId:string;entryId:string;positionSeconds:number;playing:boolean}|
  {kind:'play'|'pause'}|{kind:'seek';positionSeconds:number}|{kind:'rate';rate:number}|
  {kind:'readiness';readiness:'buffering'|'ready'|'lagging';positionUs:string;ended:boolean}
)>;
type FollowerIntent=FollowerEffect extends infer E ? E extends FollowerEffect ? Omit<E,'fence'|'room'|'playbackRevision'> : never : never;
const sameRoom=(a:WatchTogetherFence|undefined,b:WatchTogetherFence)=>!!a&&a.roomId===b.roomId&&a.epoch===b.epoch&&sameScope(a.scope,b.scope);
function parseState(raw:unknown,roomId:string):WatchTogetherState {
  if(!record(raw)||raw.id!==roomId||!counter(raw.revision)||!counter(raw.playbackRevision)||!counter(raw.eventOrdinal)||!['live','reconnecting','ended'].includes(String(raw.phase))||!['connected','reconnecting','absent'].includes(String(raw.hostPresence))||typeof raw.isHost!=='boolean'||typeof raw.canControl!=='boolean'||typeof raw.waiting!=='boolean'||raw.isHost&&!raw.canControl||!record(raw.timeline)||!record(raw.sync))invalidState();
  const t=raw.timeline,s=raw.sync;
  const stamp=(x:unknown)=>typeof x==='string'&&x.length<=64&&Number.isFinite(Date.parse(x));
  if(!(t.itemId===''||wireId(t.itemId))||!(t.currentEntryId===''||wireId(t.currentEntryId))||!['idle','playing','paused','stopped'].includes(String(t.state))||!counter(t.anchorPositionUs)||!stamp(t.anchorAt)||!record(t.rate)||!counter(t.rate.numerator)||!counter(t.rate.denominator)||t.rate.denominator==='0'||t.rate.numerator==='0'||!Number.isSafeInteger(t.queuePosition)||Number(t.queuePosition)<0||Number(t.queuePosition)>100000||t.waitUntil!==null&&!stamp(t.waitUntil))invalidState();
  const rate=Number(t.rate.numerator)/Number(t.rate.denominator);
  const ms=(v:unknown)=>Number.isSafeInteger(v)&&Number(v)>=0&&Number(v)<=600000;
  if(!Number.isFinite(rate)||rate<0.25||rate>4||!['noCorrectionUnderMs','rateCorrectionMaxMs','seekAtOrOverMs'].every(k=>ms(s[k]))||Number(s.noCorrectionUnderMs)>Number(s.seekAtOrOverMs)||typeof s.rateCorrectionMinimum!=='string'||typeof s.rateCorrectionMaximum!=='string'||!/^(?:0\.\d{1,8}|1(?:\.0{1,8})?)$/.test(s.rateCorrectionMinimum)||!/^[12](?:\.\d{1,8})?$/.test(s.rateCorrectionMaximum)||Number(s.rateCorrectionMinimum)<0.5||Number(s.rateCorrectionMaximum)>2)invalidState();
  return Object.freeze({id:roomId,revision:raw.revision,playbackRevision:raw.playbackRevision,eventOrdinal:raw.eventOrdinal,phase:raw.phase as WatchTogetherState['phase'],hostPresence:raw.hostPresence as WatchTogetherState['hostPresence'],isHost:raw.isHost,canControl:raw.canControl,waiting:raw.waiting,timeline:Object.freeze({itemId:t.itemId,currentEntryId:t.currentEntryId,state:t.state as GroupTimeline['state'],anchorPositionUs:t.anchorPositionUs,anchorAt:t.anchorAt as string,rate:Object.freeze({numerator:t.rate.numerator,denominator:t.rate.denominator}),queuePosition:t.queuePosition as number,waitUntil:t.waitUntil as string|null}),sync:Object.freeze({noCorrectionUnderMs:s.noCorrectionUnderMs as number,rateCorrectionMinimum:s.rateCorrectionMinimum,rateCorrectionMaximum:s.rateCorrectionMaximum,rateCorrectionMaxMs:s.rateCorrectionMaxMs as number,seekAtOrOverMs:s.seekAtOrOverMs as number})});
}
/** GroupFollower's correction/settling state without timers, HTTP or React. The existing group
 * service supplies viewer-projected snapshots; stale room/session callbacks are ignored. */
export class WatchTogetherSync {
  private room?:WatchTogetherFence; private state:WatchTogetherState|null=null; private local?:QueuePlaybackFence;
  readonly clock=new GroupClock();
  private correctingUntil=0; private own?:{at:number;clock:string;aim:number};private lead=0;
  private loadedFor='';private epoch='';private endReported='';private lastAt=-1;private lastReport=-Infinity;private lastReadiness='';
  join(room:WatchTogetherFence):readonly FollowerEffect[] {
    if(!wireId(room.roomId)||!Number.isSafeInteger(room.epoch)||room.epoch<1)invalidState();
    const effects=this.leave();this.room=Object.freeze({...room,scope:queueScope(room.scope)});return effects;
  }
  leave():readonly FollowerEffect[] { const effects:readonly FollowerEffect[]=this.room&&this.state&&this.local?Object.freeze([Object.freeze({kind:'rate' as const,rate:1,fence:this.local,room:this.room,playbackRevision:this.state.playbackRevision})]):Object.freeze([]); this.room=undefined;this.state=null;this.local=undefined;this.clock.reset();this.correctingUntil=0;this.own=undefined;this.lead=0;this.loadedFor='';this.epoch='';this.endReported='';this.lastAt=-1;this.lastReadiness='';this.lastReport=-Infinity;return effects; }
  bind(fence:QueuePlaybackFence):void {
    const next=playbackFence(fence);if(next.kind!=='queue'||!this.room||!sameScope(this.room.scope,next.playback.scope))invalidState();
    if(sameFence(this.local,next))return;
    this.local=next;this.correctingUntil=0;this.own=undefined;this.lead=0;
  }
  getSnapshot=()=>this.state;
  adopt(room:WatchTogetherFence,raw:unknown):boolean {
    if(!sameRoom(this.room,room))return false;
    const next=parseState(raw,room.roomId),old=this.state;
    // Connection lifecycle can change without a new server event; only lower counters are stale.
    if(old&&(old.phase==='ended'||BigInt(next.revision)<BigInt(old.revision)||BigInt(next.playbackRevision)<BigInt(old.playbackRevision)||BigInt(next.eventOrdinal)<BigInt(old.eventOrdinal)))return false;
    this.state=next;return true;
  }
  status():'in-step'|'waiting'|'host-away'|'reconnecting'|'ended'|null {
    const s=this.state;return !s?null:s.phase==='ended'?'ended':s.phase==='reconnecting'?'reconnecting':s.hostPresence!=='connected'?'host-away':s.waiting?'waiting':'in-step';
  }
  /** Local transport intentions go to the existing group service, never directly to an engine. */
  control(room:WatchTogetherFence,command:'play'|'pause'|'seek',positionSeconds?:number):Readonly<{kind:'group-transport';command:'play'|'pause'|'seek';positionUs?:string;expectedRevision:string}>|Readonly<{kind:'host-only';expand:true}>|null {
    if(!sameRoom(this.room,room)||!this.state||this.state.phase==='ended')return null;
    if(!this.state.canControl)return Object.freeze({kind:'host-only',expand:true});
    if(command==='seek'&&!seconds(positionSeconds))invalidState();
    return Object.freeze({kind:'group-transport',command,...(command==='seek'?{positionUs:String(Math.round(positionSeconds!*1e6))}:{}),expectedRevision:this.state.playbackRevision});
  }
  tick(local:FollowerLocal,nowMs:number):readonly FollowerEffect[] {
    const group=this.state,room=this.room;
    if(!room||!group||group.phase==='ended'||!sameFence(this.local,local.fence))return Object.freeze([]);
    clockMs(nowMs);if(nowMs<this.lastAt)return Object.freeze([]);this.lastAt=nowMs;
    if(!seconds(local.positionSeconds)||local.durationSeconds!==undefined&&!seconds(local.durationSeconds))invalidState();
    const effects:FollowerEffect[]=[];
    const emit=(v:FollowerIntent)=>effects.push(Object.freeze({...v,fence:this.local!,room,playbackRevision:group.playbackRevision}));
    const report=(readiness:'buffering'|'ready'|'lagging',position:number,ended=false)=>{
      if(!ended&&readiness===this.lastReadiness&&nowMs-this.lastReport<10000)return;
      this.lastReadiness=readiness;this.lastReport=nowMs;emit({kind:'readiness',readiness,positionUs:String(Math.max(0,Math.round(position*1e6))),ended});
    };
    const timeline=group.timeline;
    if(this.epoch!==group.playbackRevision){if(this.correctingUntil)emit({kind:'rate',rate:1});this.correctingUntil=0;this.epoch=group.playbackRevision;this.lastReadiness='';}
    if(!timeline.itemId&&timeline.currentEntryId){emit({kind:'pause'});report('buffering',0);return Object.freeze(effects);}
    if(!timeline.itemId||timeline.state==='idle'||timeline.state==='stopped'){if(timeline.state==='stopped'&&local.playing)emit({kind:'pause'});return Object.freeze(effects);}
    let target=Number(groupTargetPositionUs(timeline,Date.parse(timeline.anchorAt),nowMs+this.clock.offsetMs)/1000n)/1000;
    if(!seconds(target))invalidState();
    if(local.fence.itemId===timeline.itemId&&local.durationSeconds!==undefined&&local.durationSeconds>0)target=Math.min(target,local.durationSeconds);
    const key=timeline.itemId+'/'+timeline.currentEntryId,wantPlaying=timeline.state==='playing';
    if(local.fence.itemId!==timeline.itemId||local.fence.entryId!==timeline.currentEntryId){
      if(this.loadedFor!==key){this.loadedFor=key;this.own=undefined;this.lead=0;emit({kind:'load',itemId:timeline.itemId,entryId:timeline.currentEntryId,positionSeconds:target,playing:wantPlaying});}
      report('buffering',local.positionSeconds);return Object.freeze(effects);
    }
    this.loadedFor=key;
    const length=local.durationSeconds;
    if(length!==undefined&&length>0&&local.positionSeconds>=length-0.25&&!local.playing&&target>=length-0.25){
      emit({kind:'rate',rate:1});report('ready',length);
      if(group.isHost&&wantPlaying&&this.endReported!==group.playbackRevision){this.endReported=group.playbackRevision;report('ready',length,true);}
      return Object.freeze(effects);
    }
    if(!local.ready){report('buffering',local.positionSeconds);return Object.freeze(effects);}
    if(wantPlaying&&!local.playing&&!local.buffering)emit({kind:'play'});
    if(!wantPlaying&&local.playing)emit({kind:'pause'});
    const correction=groupCorrection(group.sync,BigInt(Math.round((target-local.positionSeconds)*1e6)),BigInt(Math.round(target*1e6)));
    const clock=timeline.state+'/'+timeline.anchorAt+'/'+timeline.anchorPositionUs,band=group.sync.seekAtOrOverMs/1000;
    let settling=local.seeking===true;
    if(this.own){
      const own=this.own,waited=nowMs-own.at;
      if(own.clock!==clock){this.own=undefined;settling=false;}
      else if(local.seeking!==false&&waited<20000)settling=true;
      else {this.own=undefined;settling=false;if(local.seeking===false&&waited<20000&&wantPlaying&&Math.abs(local.positionSeconds-own.aim)<band)this.lead=Math.min(20,Math.max(0,this.lead+target-local.positionSeconds));}
    }
    if(settling||local.buffering){report(local.seeking||local.buffering?'buffering':correction.action==='seek'?'lagging':'ready',local.positionSeconds);return Object.freeze(effects);}
    const seek=()=>{
      const room=length!==undefined?length-(target+this.lead):0;
      const aim=wantPlaying&&this.lead>0&&room>=Math.max(this.lead,5)?target+this.lead:target;
      this.correctingUntil=0;this.own={at:nowMs,clock,aim};emit({kind:'rate',rate:1});emit({kind:'seek',positionSeconds:aim});
    };
    if(correction.action==='seek'||!wantPlaying&&correction.action==='rate')seek();
    else if(correction.action==='rate'&&wantPlaying){if(!this.correctingUntil){this.correctingUntil=nowMs+(correction.holdMs??4000);emit({kind:'rate',rate:correction.rate??1});}else if(nowMs>this.correctingUntil)seek();}
    else if(this.correctingUntil){emit({kind:'rate',rate:1});this.correctingUntil=0;}
    report(correction.action==='seek'?'lagging':'ready',local.positionSeconds);
    return Object.freeze(effects);
  }
}
