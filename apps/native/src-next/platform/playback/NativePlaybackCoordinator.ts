import type {
  PlaybackService,
  PlaybackSnapshot,
} from '../../../../../packages/client-core/src/index';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import type {PassthroughFormats} from './passthrough-formats';
const isLocalSource = (url: string | undefined): boolean => !!url && url.startsWith('file://');
export type NativeLease = Readonly<{ lease: string; instanceId: string }>;
export type NativeBinding = Readonly<{
  lease: string;
  intentId: number;
  sessionId: string;
  generation: number;
  itemId: string;
}>;
export interface NativePlaybackTransport {
  acquire(owner: string): Promise<NativeLease>;
  command(value: Record<string, unknown>): Promise<unknown>;
  release(lease: string): Promise<unknown>;
  subscribe(listener: (fact: any) => void): () => void;
}
/**
 * More than one player engine (ENGINE-CONTRACT.md). `choose` names the engine for what is playing
 * now; it is asked on every change of the playback snapshot, before a lease is taken, so a title
 * never starts on one engine only to be moved to another. `transport` is that engine's channel, or
 * undefined when this build does not have it (the coordinator then stays where it is).
 */
export type EngineSelection = Readonly<{
  initial: string;
  choose(snapshot: PlaybackSnapshot): string;
  transport(engine: string): NativePlaybackTransport | undefined;
}>;
const key = (b: NativeBinding) =>
  JSON.stringify([b.lease, b.intentId, b.sessionId, b.generation, b.itemId]);
/** Sole RN command adapter. Construction is inert; view lifetime is unrelated to this lease. */
export class NativePlaybackCoordinator {
  private active = false;
  private listeningOwner:string|undefined;
  private lifecycle = 0;
  private lease: NativeLease | null = null;
  private binding: NativeBinding | null = null;
  private acquiring = false;
  private pendingAcquisition?: Promise<void>;
  private releaseBarrier: Promise<unknown> = Promise.resolve();
  private commandRevision = 0;
  private factSequence = 0;
  private lastSemantic = '';
  private matchFrameRate?: 'auto' | 'off';
  private matchDynamicRange?: 'auto' | 'off';
  private passthrough?: Readonly<{disabled: boolean; formats: PassthroughFormats}>;
  private barrier = 0;
  private rate = 1;
  private audio: unknown = null;
  private subtitles: unknown = null;
  private audioTrack: unknown = null;
  private localAudioTrack: unknown = null;
  private engines?: EngineSelection;
  private engine = 'native';
  private engineEpoch = 0;
  private routeReady=true;
  private routeRevision=0;
  private routeSource='';
  private listeners = new Set<() => void>();
  private unsubscribe?: () => void;
  private unsubscribeFacts?: () => void;
  private queued = false;
  private service: PlaybackService;
  private transport: NativePlaybackTransport;
  private ownerId: () => string;
  private source: (snapshot: PlaybackSnapshot) => string;
  private accept: (fact: any) => void;
  constructor(
    service: PlaybackService,
    transport: NativePlaybackTransport,
    ownerId: () => string,
    source: (snapshot: PlaybackSnapshot) => string,
    accept: (fact: any) => void,
    engines?: EngineSelection,
  ) {
    this.service = service;
    this.transport = transport;
    this.ownerId = ownerId;
    this.source = source;
    this.accept = accept;
    this.engines = engines;
    if (engines) this.engine = engines.initial;
  }
  getSnapshot = () => this.lease?.lease ?? '';
  /** The engine in use (or about to be): "native" or "mpv". Published with the lease. */
  getEngine = () => this.engine;
  /** Identity of the actual acquired renderer instance, for read-only frame proof. */
  getInstanceId = () => this.lease?.instanceId;
  /** The exact command binding currently attached to the leased native view. */
  getBinding = (): NativeBinding | undefined => this.binding ? Object.freeze({...this.binding}) : undefined;
  subscribe = (f: () => void) => {
    this.listeners.add(f);
    return () => {
      this.listeners.delete(f);
    };
  };
  private publish() {
    this.listeners.forEach(f => f());
  }
  start() {
    if (this.active) return;
    this.active = true;
    this.lifecycle++;
    this.unsubscribe = this.service.subscribe(this.schedule);
    this.unsubscribeFacts = this.transport.subscribe(this.fact);
    this.schedule();
  }
  stop() {
    if (!this.active) return;
    this.active = false;
    this.lifecycle++;
    this.queued = false;
    this.unsubscribe?.();
    this.unsubscribeFacts?.();
    this.release();
    if (this.pendingAcquisition) this.releaseBarrier = Promise.all([this.releaseBarrier, this.pendingAcquisition]);
    this.pendingAcquisition = undefined;
    void this.releaseBarrier.catch(() => {});
  }
  /** OS-07: a downloaded file on this device plays whatever the server route is doing. */
  /** What a start is waiting on (PlaybackService.setStartupProbe; development builds log it). */
  diagnostics():Readonly<Record<string,unknown>>{
    const s=this.service.getSnapshot();
    return {engine:this.engine,active:this.active,routeReady:this.routeReady,connected:this.connected(),routeSource:!!this.routeSource,leaseAcquired:!!this.lease,acquiring:this.acquiring,commandSent:this.lastSemantic!=='',commandRevision:this.commandRevision,factSequence:this.factSequence,boundSession:this.binding?.sessionId??null,snapshotSession:s.session?.id??null,nativeListening:!!s.nativeListening};
  }
  private connected():boolean{return this.routeReady||isLocalSource(this.service.getSnapshot().session?.streamUrl);}
  setConnection(ready:boolean) {
    if(this.routeReady===ready)return;
    this.routeReady=ready;this.routeRevision++;
    // Fence queued facts immediately, not only after the next native command.
    // A route change preserves the item/lease, so binding identity alone cannot
    // distinguish progress/end/error emitted by the superseded AVPlayerItem.
    this.barrier=this.commandRevision+1;
    this.schedule();
  }
  setDynamicRangeMatching(value: 'auto' | 'off' | undefined) {
    if (this.matchDynamicRange === value) return;
    this.matchDynamicRange = value; this.schedule();
  }
  setDisplayMatching(value: 'auto' | 'off' | undefined) {
    if (this.matchFrameRate === value) return;
    this.matchFrameRate = value; this.schedule();
  }
  setPassthroughPolicy(value: Readonly<{disabled: boolean; formats: PassthroughFormats}>) {
    if (JSON.stringify(this.passthrough) === JSON.stringify(value)) return;
    this.passthrough = Object.freeze({disabled: value.disabled, formats: Object.freeze({...value.formats})}); this.schedule();
  }
  setOptions(rate: number, audio: unknown, subtitles: unknown = null) {
    this.rate = rate;
    this.audio = audio;
    this.subtitles = subtitles;
    this.schedule();
  }
  /** A direct-played file's audio track for an engine that switches tracks itself
   * (`{binding, revision, streamIndex}`, bound like `audio`), or null. */
  setAudioTrack(value: unknown) {
    this.audioTrack = value;
    this.schedule();
  }
  /** Actual native track identity from this downloaded file's current catalogue. */
  setLocalAudioTrack(value: unknown) {this.localAudioTrack = value; this.schedule();}
  /** Ask again which engine should be playing (the viewer changed the Player setting). */
  reconsiderEngine() {
    this.schedule();
  }
  /**
   * Moves playback to another engine. The lease on the old engine is released, which makes the
   * service ask for the position already reached as a seek; the new engine then starts there.
   * The session, and so the queue, are untouched.
   */
  private switchEngine(engine: string, transport: NativePlaybackTransport) {
    if (this.lease) this.release();
    // An earlier acquisition may still return a lease. Its stale path releases that lease
    // before resolving, so wait for it as well as the current engine's release.
    if (this.pendingAcquisition) this.releaseBarrier = Promise.all([this.releaseBarrier, this.pendingAcquisition]);
    this.pendingAcquisition = undefined;
    this.unsubscribeFacts?.();
    this.transport = transport;
    this.engine = engine;
    this.engineEpoch++;
    this.acquiring = false;
    this.lastSemantic = '';
    this.unsubscribeFacts = this.transport.subscribe(this.fact);
    this.publish();
  }
  private release() {
    const lease = this.lease;
    this.lease = null;
    this.binding = null;
    this.lastSemantic = '';
    this.routeSource='';
    this.acquiring = false;
    this.publish();
    if (lease) {
      const state = this.service.getSnapshot();
      this.service.engineLost(state.intentId);
      this.releaseBarrier = this.transport.release(lease.lease);
      // stop() has no next acquisition to observe a rejection.
      void this.releaseBarrier.catch(() => {});
    }
  }
  private schedule = () => {
    if (!this.active || this.queued) return;
    this.queued = true;
    const life = this.lifecycle;
    queueMicrotask(() => {
      if (!this.active || life !== this.lifecycle) return;
      this.queued = false;
      this.sync();
    });
  };
  private sync() {
    const state = this.service.getSnapshot();
    if (!state.session || !state.itemId) {
      if (this.lease) this.release();
      return;
    }
    if (this.engines) {
      const wanted = this.engines.choose(state);
      const next = wanted === this.engine ? undefined : this.engines.transport(wanted);
      if (next) this.switchEngine(wanted, next);
    }
    if (!this.lease) {
      if (state.phase === 'error'||!this.connected()) return;
      if (!this.acquiring) {
        this.pendingAcquisition = this.acquire();
        void this.pendingAcquisition.catch(() => {});
      }
      return;
    }
    const listening=state.nativeListening;
    if(listening&&this.listeningOwner!==listening.owner){
      if(this.listeningOwner&&this.lease){this.listeningOwner=listening.owner;this.release();this.schedule();return;}
      this.listeningOwner=listening.owner;
    // The native owner alone says a session is native.
    }else if(!listening){
      // The owner took the player's lease with it when it retired: what plays next takes a fresh one.
      if(this.listeningOwner&&this.lease){this.listeningOwner=undefined;this.release();this.schedule();return;}
      this.listeningOwner=undefined;
    }
    if(listening)this.commandRevision=Math.max(this.commandRevision,listening.commandRevision);
    const binding: NativeBinding = {
      lease: this.lease.lease,
      intentId: state.intentId,
      sessionId: state.session.id,
      generation: state.session.generation,
      itemId: state.itemId,
    };
    const audioBinding = (this.audio as any)?.binding;
    const audio =
      audioBinding &&
      audioBinding.intentId === binding.intentId &&
      audioBinding.sessionId === binding.sessionId &&
      audioBinding.generation === binding.generation
        ? this.audio
        : null;
    const subtitleBinding=(this.subtitles as any)?.binding;
    const subtitles=subtitleBinding && subtitleBinding.intentId===binding.intentId && subtitleBinding.sessionId===binding.sessionId && subtitleBinding.generation===binding.generation ? this.subtitles : null;
    const trackBinding=(this.audioTrack as any)?.binding;
    const audioTrack=trackBinding && trackBinding.intentId===binding.intentId && trackBinding.sessionId===binding.sessionId && trackBinding.generation===binding.generation
      ? {revision:(this.audioTrack as any).revision,streamIndex:(this.audioTrack as any).streamIndex} : null;
    const localTrackBinding=(this.localAudioTrack as any)?.binding;
    const localAudioTrack=state.session.mode==='local' && localTrackBinding && localTrackBinding.intentId===binding.intentId
      && localTrackBinding.sessionId===binding.sessionId && localTrackBinding.generation===binding.generation
      ? {revision:(this.localAudioTrack as any).revision,catalogRevision:(this.localAudioTrack as any).catalogRevision,trackId:(this.localAudioTrack as any).trackId} : null;
    const candidate=this.connected()?this.source(state):'';
    if(candidate)this.routeSource=candidate;
    if(!this.routeSource)return;
    const semantic = {
      binding,
      ...(this.matchFrameRate ? {matchFrameRate: this.matchFrameRate} : {}),
      ...(this.matchDynamicRange ? {matchDynamicRange: this.matchDynamicRange} : {}),
      ...(this.passthrough ? {passthrough: this.passthrough} : {}),
      listeningOwner:listening?.owner??null,
      source: this.routeSource,
      connectionState:this.connected()?'ready':'verifying',
      routeRevision:this.routeRevision,
      paused:
        !this.connected() || state.intent === 'paused' ||
        state.phase === 'error' ||
        state.phase === 'ended' ||
        !!state.failedSeek || !!state.seekError,
      rate: state.channel?1:this.rate,
      timelineOriginMs:state.linear?.media.originMs??null,
      // mpv places itself on a channel by the server's clock epochs rather than program dates.
      // Only the start of each epoch after the first is sent, so the command changes when an
      // epoch begins, not with every segment.
      ...(this.engine==='mpv'&&state.linear?.media.clocks?.length?{linearClock:state.linear.media.clocks.map((c,i)=>i===0?{offsetMs:c.offsetMs}:{startMs:c.startMs,offsetMs:c.offsetMs})}:{}),
      pendingSeek: listening?null:state.pendingSeek ?? null,
      audio,
      audioRender: null,
      subtitles,
      // Only an engine that switches a file's tracks itself is ever given this; the command an
      // engine without it receives is exactly what it always was.
      ...(audioTrack ? {audioTrack} : {}),
      ...(localAudioTrack ? {localAudioTrack} : {}),
    };
    const encoded = JSON.stringify(semantic);
    if (encoded === this.lastSemantic) return;
    this.lastSemantic = encoded;
    this.binding = binding;
    const revision = ++this.commandRevision;
    // Reject a progress event queued before the command that introduced or settled a seek.
    if (state.pendingSeek || state.failedSeek) this.barrier = revision;
    void this.transport
      .command({ ...semantic, commandRevision: revision })
      .catch(() => {
        if (
          this.active &&
          this.binding &&
          key(this.binding) === key(binding) &&
          this.commandRevision === revision
        )
          this.service.fail(
            binding.intentId,
            serviceText('queuePlayer.startFailed'),
          );
      });
  }
  private async acquire() {
    this.acquiring = true;
    const life = this.lifecycle,
      epoch = this.engineEpoch,
      transport = this.transport,
      intent = this.service.getSnapshot().intentId;
    let expired = false;
    const timer = setTimeout(() => {
      expired = true;
      if (this.active && life === this.lifecycle && epoch === this.engineEpoch) {
        this.acquiring = false;
        this.service.fail(
          intent,
          serviceText('queuePlayer.startFailed'),
        );
      }
    }, 5000);
    let staleReleaseFailed = false;
    try {
      await this.releaseBarrier;
      if (expired || !this.active || life !== this.lifecycle || epoch !== this.engineEpoch) return;
      const lease = await transport.acquire(this.ownerId());
      if (
        expired ||
        !this.active ||
        life !== this.lifecycle ||
        epoch !== this.engineEpoch ||
        !this.service.getSnapshot().session ||
        intent !== this.service.getSnapshot().intentId
      ) {
        // The lease belongs to the engine it was asked of, which may no longer be the current one.
        try { await transport.release(lease.lease); }
        catch (error) { staleReleaseFailed = true; throw error; }
        return;
      }
      this.lease = lease;
      this.commandRevision = 0;
      this.factSequence = 0;
      this.barrier = 0;
      this.lastSemantic = '';
      this.publish();
      this.schedule();
    } catch (error) {
      if (staleReleaseFailed) throw error;
      if (this.active && life === this.lifecycle && epoch === this.engineEpoch)
        this.service.fail(
          intent,
          serviceText('queuePlayer.startFailed'),
        );
    } finally {
      clearTimeout(timer);
      if (life === this.lifecycle && epoch === this.engineEpoch) {
        this.acquiring = false;
        this.schedule();
      }
    }
  }
  private fact = (f: any) => {
    const native=!!this.service.getSnapshot().nativeListening;
    if(native&&!(typeof f?.type==='string'&&(['audioMapped','audioObserved','audioApplied','audioFailed','subtitleStatus'].includes(f.type))))return;
    if (
      !this.active || !this.connected() ||
      !this.lease ||
      !this.binding ||
      !f?.binding ||
      f.instanceId !== this.lease.instanceId ||
      key(f.binding) !== key(this.binding) ||
      f.binding.sessionId !== this.service.getSnapshot().session?.id ||
      f.binding.generation !== this.service.getSnapshot().session?.generation ||
      !Number.isSafeInteger(f.factSequence) ||
      f.factSequence <= this.factSequence
    )
      return;
    if (!native&&(
      !Number.isSafeInteger(f.lastCommandRevision) ||
      f.lastCommandRevision < this.barrier ||
      f.lastCommandRevision > this.commandRevision
    ))
      return;
    this.factSequence = f.factSequence;
    if (f.type === 'progress' || f.type === 'ended') {
      const state = this.service.getSnapshot();
      if (f.seeking || state.pendingSeek || state.failedSeek) return;
    }
    if(f.type==='audioPrepared'){this.service.audioPrepared(f.token);return;}
    if(f.type==='audioCommit'){this.service.audioCommit(f.token);return;}
    if(f.type==='audioBoundary'){this.service.audioBoundary(f.token,f.position);return;}
    if(f.type==='audioPreparationFailed'){this.service.audioPreparationFailed();return;}
    if(f.type==='audioRenderFailed'){this.service.audioRenderFailed(serviceText('queuePlayer.startFailed'));return;}
    this.accept(f);
  };
}
