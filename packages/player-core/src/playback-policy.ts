import {activeSegment, followedByScene, skipsAutomatically, skipTarget, UNDO_SECONDS, type SkipMode} from './segment-skip.ts';
import {parseSegmentMarkers, type SegmentMarker} from './segment-markers.ts';
import {upNextOffer, upNextCountdown, type UpNextOffer, type UpNextCountdown} from './up-next.ts';
import {playbackFence, sameFence, type QueuePlaybackFence} from './identity.ts';
import {parseQueuePostPlay, type QueuePostPlay} from './post-play.ts';
import {clockMs, invalidState, seconds} from './validation.ts';
export type Attention = 'still-watching' | 'playback-error' | 'owner-stopped' | 'host-only';
export type PolicyFrame = Readonly<{
  fence: QueuePlaybackFence; nowMs: number; positionSeconds: number; durationSeconds: number;
  rate: number; playing: boolean; expanded: boolean; ended: boolean; kind: string;
  modes: Readonly<Record<string,SkipMode>>; postPlay: QueuePostPlay | null; attention?: Attention;
}>;
export type PolicyCommand = Readonly<{fence: QueuePlaybackFence} & (
  {kind:'seek';positionSeconds:number;reason:'automatic-skip'|'manual-skip'|'undo';markerId:string} |
  {kind:'advance';mode:'automatic'|'manual'|'still-watching';nextEntryId:string} |
  {kind:'expand';reason:Attention}
)>;
type PolicyIntent = PolicyCommand extends infer C ? C extends PolicyCommand ? Omit<C,'fence'> : never : never;
export type PolicyView = Readonly<{
  prompt: Readonly<{kind:'skip';marker:SegmentMarker}> | Readonly<{kind:'up-next';offer:UpNextOffer;countdown:UpNextCountdown|null;sceneAfterCredits:boolean}> | null;
  undo: Readonly<{marker:SegmentMarker;remainingSeconds:number}> | null;
  markers: readonly SegmentMarker[]; finished: boolean; replay: boolean; attention: Attention | null;
  /** Remaining canonical post-play time, only while the expanded end card can auto-advance. */
  endCountdown?: Readonly<{seconds: number; automatic: true}>;
}>;
export type PolicyResult = Readonly<{view:PolicyView;commands:readonly PolicyCommand[]}>;
/** Deterministic extraction of the skip/credits/end-card effects. Engines execute commands only
 * while their fence still matches. Renderer motion never changes these clocks. */
export class PlaybackPolicy {
  private fence?: QueuePlaybackFence;
  private markers: readonly SegmentMarker[] = [];
  private dismissed = new Set<string>();
  private undo?: {marker:SegmentMarker;from:number;until:number};
  private lastNow = -1;
  private offerId = ''; private offerElapsed = 0; private endElapsed = 0;
  private watchedCredits = ''; private advanced = false; private cancelled = false;
  private frame?: PolicyFrame;
  begin(fence: QueuePlaybackFence, markers: unknown, duration?: number): void {
    const next = playbackFence(fence);
    if(next.kind!=='queue')invalidState();
    const parsed = parseSegmentMarkers(markers,duration);
    if (sameFence(this.fence,next)) { this.markers=parsed; const ids=new Set(parsed.map(m=>m.id)); this.dismissed=new Set([...this.dismissed].filter(id=>ids.has(id))); if(this.undo&&!ids.has(this.undo.marker.id))this.undo=undefined; return; }
    this.fence=next; this.markers=parsed; this.dismissed.clear(); this.undo=undefined;
    this.lastNow=-1; this.offerId=''; this.offerElapsed=0; this.endElapsed=0;
    this.watchedCredits=''; this.advanced=false; this.cancelled=false; this.frame=undefined;
  }
  stop(fence: QueuePlaybackFence): void { if (sameFence(this.fence,fence)) { this.fence=undefined; this.frame=undefined; this.markers=[]; this.dismissed.clear(); this.undo=undefined; } }
  update(input: PolicyFrame): PolicyResult | null {
    if (!sameFence(this.fence,input.fence)) return null;
    const now=clockMs(input.nowMs);
    if (now<this.lastNow) return null;
    if (!seconds(input.positionSeconds) || !seconds(input.durationSeconds) || !Number.isFinite(input.rate) || input.rate<0.25 || input.rate>4 || input.durationSeconds>0 && input.positionSeconds>input.durationSeconds+0.001) invalidState();
    if (![input.playing,input.expanded,input.ended].every(v=>typeof v==='boolean') || input.attention!==undefined&&!['still-watching','playback-error','owner-stopped','host-only'].includes(input.attention)) invalidState();
    if (Object.values(input.modes).some(mode=>!['ask','auto','off'].includes(mode))) invalidState();
    const post=input.postPlay?parseQueuePostPlay({nextEntryId:input.postPlay.nextEntryId,available:input.postPlay.available,reason:input.postPlay.reason,countdownMs:input.postPlay.countdownSeconds*1000,autoplay:input.postPlay.autoplay,passoutCheckDue:input.postPlay.passoutCheckDue,automaticAdvances:input.postPlay.automaticAdvances,startDuringCredits:input.postPlay.startDuringCredits}):null;
    const elapsed=this.lastNow<0?0:(now-this.lastNow)/1000;
    const before=this.frame; this.lastNow=now; this.frame=Object.freeze({...input,modes:Object.freeze({...input.modes}),postPlay:post});
    const commands:PolicyCommand[]=[];
    const emit=(command:PolicyIntent) => { commands.push(Object.freeze({...command,fence:this.fence!}) as PolicyCommand); };

    const attention=input.attention??(input.ended && post?.passoutCheckDue?'still-watching':null);
    if (attention && !input.expanded) emit({kind:'expand',reason:attention});
    if (this.undo && now>=this.undo.until) this.undo=undefined;
    const marker=activeSegment(this.markers,input.positionSeconds,input.modes,this.dismissed);
    if (marker && input.playing && !input.ended && !attention && skipsAutomatically(marker,input.modes)) {
      this.dismissed.add(marker.id);
      // A silent skip never queues an Undo note to appear later on expansion.
      this.undo=input.expanded?{marker,from:input.positionSeconds,until:now+UNDO_SECONDS*1000}:undefined;
      emit({kind:'seek',positionSeconds:skipTarget(marker,input.durationSeconds),reason:'automatic-skip',markerId:marker.id});
    }
    const offer=post && !input.ended && !attention ? upNextOffer({markers:this.markers,positionSeconds:input.positionSeconds,durationSeconds:input.durationSeconds,kind:input.kind,mode:input.modes.credits??'ask',nextAvailable:post.available,passoutDue:post.passoutCheckDue}):null;
    if (offer?.id!==this.offerId) { this.offerId=offer?.id??''; this.offerElapsed=0; }
    else if (input.playing && before?.playing) this.offerElapsed+=elapsed;
    const visibleOffer=offer && offer.id!==this.watchedCredits?offer:null;
    const countdown=visibleOffer && post ? upNextCountdown({offer:visibleOffer,positionSeconds:input.positionSeconds,durationSeconds:input.durationSeconds,rate:input.rate,autoplay:post.autoplay,countdownSeconds:post.countdownSeconds,elapsedSeconds:this.offerElapsed,startDuringCredits:post.startDuringCredits}):null;
    // Quiet docks keep the same autoplay policy without exposing the routine card or countdown.
    if (!this.advanced && !this.cancelled && !attention && post?.available && post.nextEntryId) {
      if (input.ended) {
        if (before?.ended) this.endElapsed+=elapsed;
        if (post.autoplay && !post.passoutCheckDue && this.endElapsed>=post.countdownSeconds) { this.advanced=true; emit({kind:'advance',mode:'automatic',nextEntryId:post.nextEntryId}); }
      } else if (countdown?.automatic && countdown.seconds<=0) { this.advanced=true; emit({kind:'advance',mode:'automatic',nextEntryId:post.nextEntryId}); }
    }
    if (!input.ended) this.endElapsed=0;
    const show=input.expanded && !attention;
    const endCountdown=show && input.ended && !this.advanced && !this.cancelled && post?.available && post.nextEntryId && post.autoplay && !post.passoutCheckDue
      ? Object.freeze({seconds:Math.max(0,post.countdownSeconds-this.endElapsed),automatic:true as const}) : undefined;
    const prompt:PolicyView['prompt']=show && visibleOffer ? {kind:'up-next',offer:visibleOffer,countdown,sceneAfterCredits:!!visibleOffer.marker && followedByScene(visibleOffer.marker,input.durationSeconds)} : show && marker && !this.dismissed.has(marker.id) ? {kind:'skip',marker}:null;
    return Object.freeze({view:Object.freeze({prompt:prompt?Object.freeze(prompt):null,undo:show && this.undo?Object.freeze({marker:this.undo.marker,remainingSeconds:(this.undo.until-now)/1000}):null,markers:input.expanded?this.markers:Object.freeze([]),finished:input.ended,replay:input.ended && (!post?.available || !post.autoplay || this.cancelled),attention,...(endCountdown?{endCountdown}:{})}),commands:Object.freeze(commands)});
  }
  dismiss(fence: QueuePlaybackFence): boolean {
    if (!sameFence(this.fence,fence) || !this.frame?.expanded || this.frame.attention || this.frame.ended) return false;
    const marker=activeSegment(this.markers,this.frame.positionSeconds,this.frame.modes,this.dismissed);
    if (marker) this.dismissed.add(marker.id);
    if (this.offerId) this.watchedCredits=this.offerId;
    return !!marker || !!this.offerId;
  }
  skip(fence: QueuePlaybackFence, expectedMarkerId?: string): PolicyCommand | null {
    if (!sameFence(this.fence,fence) || !this.frame?.expanded || this.frame.attention || this.frame.ended) return null;
    const marker=activeSegment(this.markers,this.frame.positionSeconds,this.frame.modes,this.dismissed);
    if (!marker || expectedMarkerId !== undefined && marker.id !== expectedMarkerId) return null;
    this.dismissed.add(marker.id);
    return Object.freeze({kind:'seek',fence:this.fence!,positionSeconds:skipTarget(marker,this.frame.durationSeconds),reason:'manual-skip',markerId:marker.id});
  }
  undoSkip(fence: QueuePlaybackFence, nowMs:number): PolicyCommand | null {
    if (!sameFence(this.fence,fence) || !this.frame?.expanded || this.frame.attention || !this.undo || clockMs(nowMs)<this.lastNow || nowMs>=this.undo.until) return null;
    const undo=this.undo; this.undo=undefined; this.dismissed.add(undo.marker.id);
    return Object.freeze({kind:'seek',fence:this.fence!,positionSeconds:undo.from,reason:'undo',markerId:undo.marker.id});
  }
  cancelAutoplay(fence: QueuePlaybackFence): void { if (sameFence(this.fence,fence)) this.cancelled=true; }
  advance(fence: QueuePlaybackFence, mode:'manual'|'still-watching'): PolicyCommand | null {
    if (!sameFence(this.fence,fence) || !this.frame?.expanded || this.advanced) return null;
    const post=this.frame.postPlay;
    if (this.frame.attention && this.frame.attention!=='still-watching' || (post?.passoutCheckDue || this.frame.attention==='still-watching') && mode!=='still-watching' || !post?.available || !post.nextEntryId) return null;
    this.advanced=true;
    return Object.freeze({kind:'advance',fence:this.fence!,mode,nextEntryId:post.nextEntryId});
  }
}
