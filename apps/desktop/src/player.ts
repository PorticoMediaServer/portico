import type {PlayerEvent, PlayerObservation, PlayerSource} from '../../../packages/client-core/src/playback-v1/port.ts';
import type {PlaybackMediaPort, VideoLike, WebMediaPlayerOptions, WebPlayerNotice} from '../../../web/src/platform/playback/index.ts';
import type {Bridge, NativeCommand} from './bridge.ts';
import {NativeHole} from './surface.ts';
import type {PlaybackSnapshot} from '../../../packages/client-core/src/index.ts';
import {audioGainLinear} from '../../../packages/client-core/src/playback-v1/audio-render.ts';
import {localPlayerSource,localPlayerLoadReceipt,type LocalPlayerSource,type LocalPlayerLoadContext,type LocalPlayerLoadReceipt} from '../../../packages/client-core/src/playback-v1/local-port.ts';
import type {DesktopLocalPlayback} from './local-playback.ts';

let nextNativeGeneration = 0;

/** The exact existing web MediaPlayer seam. Transport/session/queue/UI remain with their owners. */
export class DesktopMediaPlayer implements PlaybackMediaPort {
  private source?: PlayerSource|LocalPlayerSource;
  readonly loadLocal?: (source:LocalPlayerSource,context:LocalPlayerLoadContext)=>Promise<LocalPlayerLoadReceipt>;
  private localContext?:Readonly<{context:LocalPlayerLoadContext;abort:()=>void;generation:number}>;
  private reading: PlayerObservation = {generation: 0, state: 'idle', positionMs: 0, rate: 1};
  private listeners = new Set<(event: PlayerEvent) => void>();
  private notices = new Set<(event: WebPlayerNotice) => void>();
  private serial: Promise<void> = Promise.resolve();
  private disposed = false;
  private epoch = 0;
  private nativeGeneration = 0;
  private polling = false;
  private surfaceActive = false;
  private processing?:Readonly<{generation:number;eqApplied:boolean;levelingApplied:boolean}>;
  private effects?:PlaybackSnapshot;
  private effectsBinding?:Readonly<{sessionId:string;generation:number;url:string}>;
  private sentGain?:number;
  private hole?: NativeHole;
  private seeking = false;
  private frameStepping = false;
  private ticker?: ReturnType<typeof setInterval>;
  private readonly volumeChanged = () => this.send({action: 'volume', volume: this.video.volume, muted: this.video.muted});
  private readonly video: VideoLike;
  private readonly options: WebMediaPlayerOptions;
  private readonly bridge: Bridge;

  constructor(video: VideoLike, options: WebMediaPlayerOptions, bridge: Bridge, poll = true,private readonly local?:DesktopLocalPlayback) {
    this.video = video; this.options = options; this.bridge = bridge;
    video.addEventListener('volumechange', this.volumeChanged);
    // The factory receives the shell's fresh geometry/volume element. Native playback never
    // starts HTML media resource selection or measures an idle/unmounted surface.
    if (poll) this.ticker = setInterval(() => { void this.tick(); }, 200);
    if(local)this.loadLocal=(source,context)=>this.loadLocalSource(source,context,local);
  }
  get loaded() { return this.source?.generation ?? 0; }
  get nativeSurfaceActive() { return this.surfaceActive; }
  /** Extra structural port until the canonical checked factory hook lands. Facts belong to
   * this presentation generation, never to a stored preference or the previous song. */
  get audioProcessing() { return this.processing; }
  get settling() { return this.seeking || this.frameStepping || this.reading.state === 'loading'; }
  private emit(event: PlayerEvent) { for (const listener of [...this.listeners]) listener(event); }
  private fail(epoch: number) {
    if (this.disposed || epoch !== this.epoch || !this.source) return;
    this.reading = {...this.reading, state: 'error',observedPlayback:{}};
    this.surfaceActive = false;
    this.processing=undefined;
    this.emit({type: 'error', observation: this.reading, code: 'engine_unavailable'});
  }
  private enqueue(command: NativeCommand, epoch: number) {
    this.serial = this.serial.then(() => this.bridge.command(command)).catch(() => this.fail(epoch));
  }
  private send(command: NativeCommand) {
    if (!this.source || this.disposed) return;
    this.enqueue({...command, generation: this.nativeGeneration}, this.epoch);
  }
  applyAudioEffects(snapshot:PlaybackSnapshot):void {
    this.effects=snapshot.session&&!['idle','ended','error'].includes(snapshot.phase)?snapshot:undefined;
    if(!this.effects){if(this.effectsBinding){this.effectsBinding=undefined;this.applyGain(1);}return;}
    const session=this.effects.session!;
    if(this.effectsBinding?.sessionId===session.id&&this.effectsBinding.generation===session.generation&&this.effectsBinding.url===session.streamUrl)this.applyCurrentEffects();
  }
  private applyGain(gain:number):void {
    if(!this.source||this.disposed||this.sentGain===gain)return;
    this.sentGain=gain;this.send({action:'processing',gain});
  }
  private applyCurrentEffects():void {
    const snapshot=this.effects;
    if(!snapshot)return;
    const settings=snapshot.audioEffects.settings;
    this.applyGain(audioGainLinear(snapshot.session?.audio,settings.normalization,0,settings.neverBoost??false));
  }
  load(source: PlayerSource) {
    if (this.disposed) return;
    this.detachLocalContext();
    const epoch = ++this.epoch; this.source = source; this.seeking = false;this.frameStepping=false;
    this.surfaceActive = false;this.processing=undefined;
    this.nativeGeneration = ++nextNativeGeneration;
    this.sentGain=undefined;
    const session=this.effects?.session;
    this.effectsBinding=session&&session.generation===source.generation&&session.streamUrl===source.url?{sessionId:session.id,generation:session.generation,url:session.streamUrl}:undefined;
    if(!this.hole&&typeof HTMLVideoElement!=='undefined'&&this.video instanceof HTMLVideoElement)this.hole=new NativeHole(this.video,bounds=>this.bridge.surface?.(bounds,this.nativeGeneration)??Promise.reject(new Error('unsupported')),()=>this.surfaceActive);
    this.hole?.invalidate();
    this.reading = {generation: source.generation, state: 'loading', positionMs: source.startPositionMs, rate: this.reading.rate};
    this.emit({type: 'state', observation: this.reading});
    this.enqueue({action: 'load', source: {url: this.options.resolveUrl?.(source.url) ?? source.url, generation: this.nativeGeneration, mode: source.mode, startPositionMs: source.startPositionMs, autoplay: source.autoplay}}, epoch);
    if(this.effectsBinding)this.applyCurrentEffects();
  }
  private async loadLocalSource(input:LocalPlayerSource,context:LocalPlayerLoadContext,local:DesktopLocalPlayback):Promise<LocalPlayerLoadReceipt>{
    const source=localPlayerSource(input);
    if(this.disposed||context.signal.aborted||!context.isCurrent())throw new DOMException('Aborted','AbortError');
    this.detachLocalContext();
    const epoch=++this.epoch,native=++nextNativeGeneration;this.nativeGeneration=native;this.source=source;
    this.seeking=false;this.frameStepping=false;this.surfaceActive=false;this.processing=undefined;this.effects=undefined;this.effectsBinding=undefined;this.sentGain=undefined;
    if(!this.hole&&typeof HTMLVideoElement!=='undefined'&&this.video instanceof HTMLVideoElement)this.hole=new NativeHole(this.video,bounds=>this.bridge.surface?.(bounds,this.nativeGeneration)??Promise.reject(new Error('unsupported')),()=>this.surfaceActive);
    this.hole?.invalidate();
    this.reading={generation:source.generation,state:'loading',positionMs:0,rate:this.reading.rate};
    this.emit({type:'state',observation:this.reading});
    const current=()=>!this.disposed&&this.epoch===epoch&&this.source===source&&this.nativeGeneration===native&&!context.signal.aborted&&context.isCurrent();
    const abort=()=>{if(this.epoch===epoch)this.unload();void local.retire(native).catch(()=>{});};
    this.localContext={context,abort,generation:native};context.signal.addEventListener('abort',abort,{once:true});
    const task=this.serial.then(async()=>{
      if(!current())throw new DOMException('Aborted','AbortError');
      const receipt=await local.load(source,native,context);
      if(!current()){await local.retire(native);throw new DOMException('Aborted','AbortError');}
      return localPlayerLoadReceipt(receipt,source);
    }).catch(async error=>{await local.retire(native).catch(()=>{});throw error;});
    this.serial=task.then(()=>{},()=>{if(current())this.fail(epoch);});
    return task;
  }
  private detachLocalContext(){
    if(!this.localContext)return;
    this.localContext.context.signal.removeEventListener('abort',this.localContext.abort);
    this.localContext=undefined;
  }
  play() { this.send({action: 'play'}); }
  pause() { this.send({action: 'pause'}); }
  seek(positionMs: number) { if (Number.isFinite(positionMs) && positionMs >= 0) { this.seeking = true; this.send({action: 'seek', milliseconds: positionMs}); } }
  setRate(rate: number) { if (Number.isFinite(rate) && rate >= .25 && rate <= 4) { this.reading = {...this.reading, rate}; this.send({action: 'rate', rate}); } }
  /** Fixed native decoder commands only. The web runtime supplies its actual paused
   * fence once that optional hook is on MAIN; this adapter independently fences it. */
  stepFrame(direction:-1|1,generation:number):Promise<void> {
    const epoch=this.epoch,native=this.nativeGeneration;
    const current=()=>!this.disposed&&this.epoch===epoch&&this.source?.generation===generation&&this.nativeGeneration===native;
    if(!current()||this.reading.state!=='paused'||this.settling||![-1,1].includes(direction))return Promise.reject(new Error('invalid_command'));
    this.frameStepping=true;
    const task=this.serial.then(async()=>{
      if(!current()||this.reading.state!=='paused')throw new Error('stale_generation');
      await this.bridge.command({action:'frameStep',direction,generation:native});
      if(!current())throw new Error('stale_generation');
    });
    this.serial=task.catch(()=>{});
    return task.then(()=>this.tick()).finally(()=>{if(current())this.frameStepping=false;});
  }
  selectSidecarSubtitle(trackId: string | null) {
    // Embedded libass/bitmap rendering is native. Extracted sidecars require a constrained broker;
    // do not tell the controller an unavailable sidecar is visible.
    this.reading = {...this.reading, subtitleTrackId: trackId, subtitlesCarried: trackId === null};
  }
  observe() { return this.reading; }
  onEvent(listener: (event: PlayerEvent) => void) { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }
  onNotice(listener: (event: WebPlayerNotice) => void) { this.notices.add(listener); return () => { this.notices.delete(listener); }; }
  async tick() {
    if (this.disposed || !this.source || this.polling) return;
    if(this.localContext&&(!this.localContext.context.isCurrent()||this.localContext.context.signal.aborted)){this.unload();return;}
    this.polling = true; const epoch = this.epoch;
    try {
      await this.serial;
      const next = await this.bridge.observe();
      if (this.disposed || epoch !== this.epoch || next.generation !== this.nativeGeneration || !this.source) return;
      const active = next.nativeSurfaceActive && !['idle','loading','ended','error'].includes(next.state);
      const confirmed=!['idle','loading','ended','error'].includes(next.state);
      const processing=confirmed?Object.freeze({generation:this.source.generation,eqApplied:next.eqApplied,levelingApplied:next.levelingApplied}):undefined;
      const changed = this.reading.state !== next.state || this.surfaceActive !== active||this.processing?.eqApplied!==processing?.eqApplied||this.processing?.levelingApplied!==processing?.levelingApplied;
      this.surfaceActive = active;
      this.processing=processing;
      const {nativeSurfaceActive: _,eqApplied: _eq,levelingApplied: _leveling,...projected} = next;
      this.reading = {...projected,observedPlayback:confirmed?next.observedPlayback??{}:{},generation: this.source.generation, subtitleTrackId: this.reading.subtitleTrackId, subtitlesCarried: this.reading.subtitlesCarried};
      if (changed) this.emit({type: 'state', observation: this.reading});
      if (this.seeking && !['loading','buffering'].includes(next.state)) { this.seeking = false; this.emit({type: 'seeked', observation: this.reading}); }
      this.emit({type: 'time', observation: this.reading});
    } catch { this.fail(epoch); } finally { this.polling = false; }
  }
  unload() {
    if (this.source) this.send({action: 'stop'});
    const local=this.localContext;if(local)void this.local?.retire(local.generation).catch(()=>{});
    this.detachLocalContext();
    ++this.epoch; this.source = undefined; this.seeking = false;this.frameStepping=false; this.surfaceActive = false;this.processing=undefined;
    this.effects=undefined;this.effectsBinding=undefined;this.sentGain=undefined;
    this.hole?.dispose();this.hole=undefined;
    this.reading = {generation: 0, state: 'idle', positionMs: 0, rate: this.reading.rate};
  }
  suspend() { this.pause(); }
  resume() { /* A route recovery loads a fresh server capability; never reuse an expired URL. */ }
  dispose() {
    if (this.disposed) return;
    this.unload(); this.disposed = true;
    if (this.ticker) clearInterval(this.ticker);
    this.video.removeEventListener('volumechange', this.volumeChanged);
    this.hole?.dispose();
    this.listeners.clear(); this.notices.clear();
  }
}
