import {playbackFence, sameFence, sameScope, type PlaybackFence, type QueuePlaybackFence, type ChannelPlaybackFence} from './identity.ts';
import {parseQueueSnapshot, queueScope, type QueueScope, type QueueSnapshot} from './queues.ts';
import {clockMs, counter, invalidState, seconds, wireId} from './validation.ts';
import {localPlaybackScope,sameLocalScope,type LocalPlaybackScope,type LocalPlaybackFence} from './local-playback-identity.ts';
export type DockPlayback = Readonly<{
  positionSeconds:number; playing:boolean; expanded:boolean;
}> & (Readonly<{kind:'live';fence:ChannelPlaybackFence;queue:null}> | Readonly<{kind:'video'|'music'|'audiobook';fence:QueuePlaybackFence;queue:QueueSnapshot|null}> | Readonly<{kind:'video'|'music'|'audiobook';fence:LocalPlaybackFence;queue:null}>);
export type RemotePhase = 'connecting'|'pairing'|'waiting'|'playing'|'stopped'|'replaced'|'error';
export type RemoteDock = Readonly<{
  id:string; kind:'cast'|'play-on'; phase:RemotePhase; sequence:string;
  playback:DockPlayback; positionSeconds:number; stoppedAtMs?:number;
}>;
export type DockState = Readonly<{local:DockPlayback|null;parkedAudio:DockPlayback|null;resumeVideo:DockPlayback|null;remote:RemoteDock|null}>;
export type DockEffect = Readonly<{kind:'pause'|'stop'|'restore';playback:DockPlayback;playing:false}>;
export type DockTransition = Readonly<{state:DockState;effects:readonly DockEffect[]}>;
const audio = (p:DockPlayback) => p.kind==='music'||p.kind==='audiobook';
const paused = (p:DockPlayback):DockPlayback => Object.freeze({...p,playing:false,expanded:false});
const empty = ():DockState => Object.freeze({local:null,parkedAudio:null,resumeVideo:null,remote:null});
function capturedLocalScope(scope:QueueScope,value:LocalPlaybackScope|undefined):LocalPlaybackScope|undefined {
  if(value===undefined)return undefined;
  const local=localPlaybackScope(value);
  if(local.serverId!==scope.serverId||local.authority!==scope.authority||local.accountId!==scope.accountId||local.profileId!==scope.profileId)invalidState();
  return local;
}
/** One occupant, including remote connecting/stopped states. Local video never becomes PiP here.
 * Parked values are in-memory bookmarks only; restoring them is always paused. The engine
 * adapter creates a fresh session before using a retired bookmark, reading Settings again. */
export class PlaybackDock {
  private scope:QueueScope; private localScope?:LocalPlaybackScope; private state:DockState=empty(); private localRetired=false; private remoteNow=-1;
  constructor(scope:QueueScope,localScope?:LocalPlaybackScope) { this.scope=queueScope(scope);this.localScope=capturedLocalScope(this.scope,localScope); }
  getSnapshot=()=>this.state;
  private checked(p:DockPlayback):DockPlayback {
    const fence=playbackFence(p.fence);
    const current=fence.kind==='local'?!!this.localScope&&sameLocalScope(fence.playback.scope,this.localScope):sameScope(fence.playback.scope,this.scope);
    if (!current || !['video','live','music','audiobook'].includes(p.kind) || !seconds(p.positionSeconds) || typeof p.playing!=='boolean' || typeof p.expanded!=='boolean') invalidState();
    const common={positionSeconds:p.positionSeconds,playing:p.playing,expanded:p.expanded};
    if(p.kind==='live') {
      if(fence.kind!=='channel'||p.queue!==null)invalidState();
      return Object.freeze({...common,kind:p.kind,fence,queue:null});
    }
    if(fence.kind==='local') {
      if(p.queue!==null)invalidState();
      return Object.freeze({...common,kind:p.kind,fence,queue:null});
    }
    if(fence.kind!=='queue')invalidState();
    return Object.freeze({...common,fence,kind:p.kind,queue:p.queue?parseQueueSnapshot(p.queue,this.scope):null});
  }
  private commit(state:DockState,effects:DockEffect[]=[]):DockTransition { this.state=Object.freeze(state); return Object.freeze({state:this.state,effects:Object.freeze(effects.map(e=>Object.freeze(e)))}); }
  start(value:DockPlayback):DockTransition {
    const next=this.checked(value),old=this.state.local,effects:DockEffect[]=[];
    this.localRetired=false;
    let parkedAudio=this.state.parkedAudio,resumeVideo=this.state.resumeVideo;
    if (old && !sameFence(old.fence,next.fence)) {
      if (audio(old) && !audio(next)) { parkedAudio=paused(old); effects.push({kind:'pause',playback:parkedAudio,playing:false}); }
      else { effects.push({kind:'stop',playback:paused(old),playing:false}); if (!audio(old)&&audio(next)) resumeVideo=paused(old); }
    }
    if (audio(next)) parkedAudio=null;
    // Selecting local playback is an explicit takeover; disconnect remote before playing it.
    if (this.state.remote) effects.push({kind:'stop',playback:paused(this.state.remote.playback),playing:false});
    return this.commit({local:next,parkedAudio,resumeVideo,remote:null},effects);
  }
  update(value:DockPlayback):DockTransition {
    if (this.localRetired || !sameFence(this.state.local?.fence,value.fence)) return this.commit(this.state);
    return this.commit({...this.state,local:this.checked(value)});
  }
  collapse(fence:PlaybackFence):DockTransition {
    if (!sameFence(this.state.local?.fence,fence)) return this.commit(this.state);
    return this.commit({...this.state,local:Object.freeze({...this.state.local!,expanded:false})});
  }
  expand(fence:PlaybackFence):DockTransition {
    if (!sameFence(this.state.local?.fence,fence)) return this.commit(this.state);
    return this.commit({...this.state,local:Object.freeze({...this.state.local!,expanded:true})});
  }
  /** End/Close restores the music card paused, on every platform (answered Q9). */
  close(fence:PlaybackFence):DockTransition {
    if (!sameFence(this.state.local?.fence,fence)) return this.commit(this.state);
    const local=this.state.local!,restored=!audio(local)?this.state.parkedAudio:null;
    const effects:DockEffect[]=[{kind:'stop',playback:paused(local),playing:false}];
    this.localRetired=!!restored;
    if (restored) effects.push({kind:'restore',playback:paused(restored),playing:false});
    return this.commit({...this.state,local:restored?paused(restored):null,parkedAudio:null},effects);
  }
  /** Resume music is an explicit user action. The transition restores its queue/place paused;
   * the adapter may start it only in response to that user's action, with a fresh session. */
  restoreAudio():DockTransition {
    const saved=this.state.parkedAudio;
    if (!saved) return this.commit(this.state);
    const effects:DockEffect[]=[];
    if (this.state.local) effects.push({kind:'stop',playback:paused(this.state.local),playing:false});
    if (this.state.remote) effects.push({kind:'stop',playback:paused(this.state.remote.playback),playing:false});
    this.localRetired=true;
    effects.push({kind:'restore',playback:paused(saved),playing:false});
    return this.commit({local:paused(saved),parkedAudio:null,resumeVideo:this.state.local&&!audio(this.state.local)?paused(this.state.local):this.state.resumeVideo,remote:null},effects);
  }
  restoreVideo():DockTransition {
    const saved=this.state.resumeVideo;
    if(!saved)return this.commit(this.state);
    const effects:DockEffect[]=[];
    if(this.state.local)effects.push({kind:audio(this.state.local)?'pause':'stop',playback:paused(this.state.local),playing:false});
    if(this.state.remote)effects.push({kind:'stop',playback:paused(this.state.remote.playback),playing:false});
    this.localRetired=true;
    effects.push({kind:'restore',playback:paused(saved),playing:false});
    return this.commit({local:paused(saved),parkedAudio:this.state.local&&audio(this.state.local)?paused(this.state.local):this.state.parkedAudio,resumeVideo:null,remote:null},effects);
  }
  /** Only an explicit connection start can create/replace a remote occupant. Late updates
   * after Dismiss/timeout/sign-out cannot resurrect its bar. */
  beginRemote(value:RemoteDock,nowMs:number):DockTransition { return this.adoptRemote(value,nowMs,true); }
  remote(value:RemoteDock,nowMs:number):DockTransition { return this.adoptRemote(value,nowMs,false); }
  private adoptRemote(value:RemoteDock,nowMs:number,initial:boolean):DockTransition {
    clockMs(nowMs);
    if (!initial && nowMs<this.remoteNow) return this.commit(this.state);
    if (!wireId(value.id)||!counter(value.sequence)||!['cast','play-on'].includes(value.kind)||!['connecting','pairing','waiting','playing','stopped','replaced','error'].includes(value.phase)||!seconds(value.positionSeconds)) invalidState();
    const current=this.state.remote;
    if (!initial && (!current || current.id!==value.id || BigInt(value.sequence)<=BigInt(current.sequence))) return this.commit(this.state);
    if (!initial && current && ['stopped','replaced','error'].includes(current.phase) && !['stopped','replaced','error'].includes(value.phase)) return this.commit(this.state);
    const playback=this.checked(value.playback),effects:DockEffect[]=[];
    // This app's receipt-bound file is never a server/Cast/Play-on source.
    if(playback.fence.kind==='local')invalidState();
    let local=this.state.local;
    // Connecting leaves this phone playing; the confirmed receiver takeover retires local media.
    if (value.phase==='playing') this.localRetired=true;
    this.remoteNow=nowMs;
    if (value.phase==='playing' && local?.playing) { local=paused(local); effects.push({kind:'stop',playback:local,playing:false}); }
    const terminal=['stopped','replaced','error'].includes(value.phase);
    const remote=Object.freeze({id:value.id,kind:value.kind,phase:value.phase,sequence:value.sequence,positionSeconds:value.positionSeconds,playback,stoppedAtMs:terminal?(!initial&&current?.stoppedAtMs!==undefined?current.stoppedAtMs:nowMs):undefined});
    return this.commit({...this.state,local,remote},effects);
  }
  dismissRemote(id:string):DockTransition { return this.state.remote?.id===id?this.commit({...this.state,remote:null}):this.commit(this.state); }
  tick(nowMs:number):DockTransition {
    clockMs(nowMs);const remote=this.state.remote;
    return remote?.stoppedAtMs!==undefined && nowMs>=remote.stoppedAtMs+15000?this.dismissRemote(remote.id):this.commit(this.state);
  }
  continueHere(id:string):DockTransition {
    const remote=this.state.remote;
    if (!remote || remote.id!==id || remote.phase!=='stopped') return this.commit(this.state);
    const restored=paused({...remote.playback,positionSeconds:remote.positionSeconds});this.localRetired=true;
    return this.commit({...this.state,local:restored,remote:null},[{kind:'restore',playback:restored,playing:false}]);
  }
  /** Sign-out/profile/controller change discards all bookmarks; nothing crosses identities. */
  reset(scope:QueueScope,localScope?:LocalPlaybackScope):DockTransition {
    const next=queueScope(scope),local=capturedLocalScope(next,localScope);
    this.scope=next;this.localScope=local;this.localRetired=false;this.remoteNow=-1;
    const effects:DockEffect[]=[];
    if(this.state.local)effects.push({kind:'stop',playback:paused(this.state.local),playing:false});
    if(this.state.remote)effects.push({kind:'stop',playback:paused(this.state.remote.playback),playing:false});
    return this.commit(empty(),effects);
  }
  occupant():Readonly<{mode:'cast'|'play-on'|'video'|'music'|'audiobook';playback:DockPlayback;remote?:RemoteDock}>|null {
    const {remote,local}=this.state;
    if (remote) return Object.freeze({mode:remote.kind,playback:remote.playback,remote});
    if (!local || local.expanded) return null;
    return Object.freeze({mode:audio(local)?local.kind as 'music'|'audiobook':'video',playback:local});
  }
}
