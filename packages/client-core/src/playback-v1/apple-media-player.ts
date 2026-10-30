/**
 * Playback v1 on Apple: the `MediaPlayer` port over the native video engine (PorticoPlaybackModule
 * + PorticoPlaybackController: one AVPlayer, leased, driven by full-state commands, reporting
 * sequenced facts). Spec: Plan — Client Playback Migration §8. Platform-neutral: the React
 * Native glue (`NativeModules`, the event emitter) is injected as an `AppleVideoEngine`, so this
 * is unit-tested with a fake engine and F-apple wires it in `bridge/`.
 *
 * - Every `load` is a new binding `{lease, intentId, sessionId, generation, itemId}`; `intentId`
 *   rises per load, `generation` is the presentation generation. A fact is accepted only for the
 *   current lease instance and binding, with a rising `factSequence` and a `lastCommandRevision`
 *   at or after the last seek barrier (the native controller's own rules, mirrored).
 * - Commands are the controller's full state; each change sends the whole state with a higher
 *   `commandRevision`, and identical states are not resent.
 * - Seeks carry a rising revision until `seekApplied`/`seekFailed` for it, then are retired.
 * - Sidecar subtitles use the native WebVTT sidecar mode (§8 N1) when `sidecar: 'webvtt'`;
 *   otherwise the capability profile must not ask for sidecars and selection is ignored.
 */
import type {MediaPlayer, PlayerEvent, PlayerObservation, PlayerSource, PlayerState, SidecarSubtitle} from './port.ts';

export type AppleLease = Readonly<{lease: string; instanceId: string}>;

/** The native engine surface (`PorticoPlaybackModule` + its `PorticoPlaybackFact` events). */
export type AppleVideoEngine = Readonly<{
  acquire(owner: string): Promise<AppleLease>;
  command(command: Readonly<Record<string, unknown>>): Promise<unknown>;
  release(lease: string): Promise<unknown>;
  onFact(listener: (fact: AppleFact) => void): () => void;
}>;

/** A native fact (PorticoPlaybackController `emit`/`publish`). Extra fields are ignored. */
export type AppleFact = Readonly<{
  type: string;
  instanceId?: string;
  binding?: Readonly<{lease?: string; intentId?: number; sessionId?: string; generation?: number; itemId?: string}>;
  factSequence?: number;
  lastCommandRevision?: number;
  position?: number;
  paused?: boolean;
  effectiveRate?: number;
  seeking?: boolean;
  seekRevision?: number;
  engineCode?: string;
  networkError?: boolean;
  errorDetail?: string;
  /** §8 N4 (optional until native adds them). */
  bufferedSeconds?: number;
  observedBitrateKbps?: number;
  droppedFrames?: number;
}>;

export type ApplePlayerOptions = Readonly<{
  /** The lease owner name (unique per player surface). */
  owner: string;
  /** Makes presentation and sidecar URLs absolute against the selected route (`api.mediaUrl`). */
  resolveUrl: (url: string) => string;
  /** Native sidecar support (§8 N1). `none` until F-apple ships it. */
  sidecar?: 'webvtt' | 'none';
  /** Called when the engine refused a command or could not attach (diagnostics). */
  onEngineError?: (message: string) => void;
}>;

type Binding = {lease: string; intentId: number; sessionId: string; generation: number; itemId: string};

export class AppleMediaPlayer implements MediaPlayer {
  private engine: AppleVideoEngine;
  private o: ApplePlayerOptions;
  private listeners = new Set<(e: PlayerEvent) => void>();
  private lease?: AppleLease;
  private acquiring?: Promise<void>;
  private source?: PlayerSource;
  private binding?: Binding;
  private intentId = 0;
  private commandRevision = 0;
  private lastSent = '';
  private factSequence = 0;
  private barrier = 0;
  private paused = true;
  private rate = 1;
  private pendingSeek?: {revision: number; positionSeconds: number};
  private seekRevision = 0;
  private subtitle: SidecarSubtitle | null = null;
  private subtitleRevision = 0;
  private state: PlayerState = 'idle';
  private positionMs = 0;
  private extras: {bufferedMs?: number; bandwidthKbps?: number; droppedFrames?: number} = {};
  private route?: 'airplay' | 'hdmi' | 'pip';
  private disposed = false;
  private offFacts: () => void;

  constructor(engine: AppleVideoEngine, options: ApplePlayerOptions) {
    this.engine = engine;
    this.o = options;
    this.offFacts = engine.onFact(f => this.fact(f));
  }

  // ── Port ────────────────────────────────────────────────────────
  load(source: PlayerSource): void {
    if (this.disposed) return;
    this.source = source;
    this.paused = !source.autoplay;
    this.pendingSeek = undefined;
    this.subtitle = null;
    this.positionMs = source.startPositionMs;
    this.extras = {};
    this.state = 'loading';
    this.emit('state');
    void this.ensureLease().then(() => {
      if (this.disposed || this.source !== source || !this.lease) return;
      this.intentId++;
      this.binding = {lease: this.lease.lease, intentId: this.intentId, sessionId: source.sessionId ?? 'v1', generation: source.generation, itemId: source.itemId ?? 'v1'};
      // The native engine starts at 0; a resume position is the first seek of the binding.
      if (source.startPositionMs > 0) this.pendingSeek = {revision: ++this.seekRevision, positionSeconds: source.startPositionMs / 1000};
      this.send();
    });
  }

  play(): void { this.paused = false; this.send(); }
  pause(): void { this.paused = true; this.send(); }
  setRate(rate: number): void { this.rate = rate; this.send(); }
  seek(positionMs: number): void { this.pendingSeek = {revision: ++this.seekRevision, positionSeconds: Math.max(0, positionMs) / 1000}; this.send(); }

  selectSidecarSubtitle(trackId: string | null): void {
    if ((this.o.sidecar ?? 'none') === 'none') return;
    this.subtitle = trackId === null ? null : this.source?.subtitles.find(s => s.trackId === trackId) ?? null;
    this.subtitleRevision++;
    this.send();
    this.emit('state');
  }

  observe(): PlayerObservation {
    const overlay = this.subtitle !== null;
    return {
      generation: this.source?.generation ?? 0, state: this.state, positionMs: Math.max(0, Math.round(this.positionMs)), rate: this.rate,
      subtitleTrackId: this.subtitle?.trackId ?? null,
      ...this.extras,
      ...(this.route ? {externalRoute: this.route, subtitlesCarried: !overlay} : {}),
    };
  }

  onEvent(listener: (event: PlayerEvent) => void): () => void { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }

  /** From `PorticoSystemMediaRoute` and the view's PiP events: the picture is elsewhere. */
  setExternalRoute(route: 'airplay' | 'hdmi' | 'pip' | undefined): void {
    if (this.route === route) return;
    this.route = route;
    this.emit('state');
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.offFacts();
    this.listeners.clear();
    const lease = this.lease;
    this.lease = undefined;
    if (lease) void this.engine.release(lease.lease).catch(() => {});
  }

  // ── Engine ──────────────────────────────────────────────────────
  private ensureLease(): Promise<void> {
    if (this.lease) return Promise.resolve();
    this.acquiring ??= this.engine.acquire(this.o.owner).then(lease => {
      if (this.disposed) { void this.engine.release(lease.lease).catch(() => {}); return; }
      this.lease = lease;
      // A new lease resets the controller's revisions and sequences.
      this.commandRevision = 0; this.factSequence = 0; this.barrier = 0; this.lastSent = '';
    }, e => {
      this.acquiring = undefined;
      this.o.onEngineError?.(String((e as Error)?.message ?? e));
      this.fail('engine_unavailable', 'the player could not attach');
    });
    return this.acquiring;
  }

  private command(): Record<string, unknown> | undefined {
    if (!this.binding || !this.source) return undefined;
    const b = this.binding;
    const sidecar = this.subtitle && (this.o.sidecar ?? 'none') === 'webvtt'
      ? {binding: {intentId: b.intentId, sessionId: b.sessionId, generation: b.generation}, mode: 'sidecar', revision: this.subtitleRevision, trackId: this.subtitle.trackId, format: this.subtitle.format, url: this.o.resolveUrl(this.subtitle.url)}
      : null;
    return {
      binding: b, listeningOwner: null, source: this.o.resolveUrl(this.source.url), connectionState: 'ready', routeRevision: 0,
      paused: this.paused || this.state === 'error' || this.state === 'ended', rate: this.rate, timelineOriginMs: null,
      pendingSeek: this.pendingSeek ?? null, audio: null, subtitles: sidecar,
    };
  }

  private send() {
    if (this.disposed || !this.lease) return;
    const semantic = this.command();
    if (!semantic) return;
    const encoded = JSON.stringify(semantic);
    if (encoded === this.lastSent) return;
    this.lastSent = encoded;
    const revision = ++this.commandRevision;
    if (this.pendingSeek) this.barrier = revision;
    const binding = this.binding;
    void this.engine.command({...semantic, commandRevision: revision}).catch(e => {
      this.o.onEngineError?.(String((e as Error)?.message ?? e));
      if (binding === this.binding && revision === this.commandRevision) this.fail('engine_error', 'the player refused a control change');
    });
  }

  private fact(f: AppleFact) {
    const b = this.binding, lease = this.lease;
    if (this.disposed || !b || !lease || f.instanceId !== lease.instanceId || !f.binding) return;
    if (f.binding.lease !== b.lease || f.binding.intentId !== b.intentId || f.binding.sessionId !== b.sessionId || f.binding.generation !== b.generation || f.binding.itemId !== b.itemId) return;
    if (!Number.isSafeInteger(f.factSequence) || f.factSequence! <= this.factSequence) return;
    if (!Number.isSafeInteger(f.lastCommandRevision) || f.lastCommandRevision! < this.barrier || f.lastCommandRevision! > this.commandRevision) return;
    this.factSequence = f.factSequence!;
    if (typeof f.position === 'number' && Number.isFinite(f.position) && !(this.pendingSeek && (f.type === 'progress' || f.seeking))) this.positionMs = f.position * 1000;
    this.extras = {
      ...this.extras,
      ...(typeof f.bufferedSeconds === 'number' ? {bufferedMs: Math.round(f.bufferedSeconds * 1000)} : {}),
      ...(typeof f.observedBitrateKbps === 'number' ? {bandwidthKbps: Math.round(f.observedBitrateKbps)} : {}),
      ...(typeof f.droppedFrames === 'number' ? {droppedFrames: f.droppedFrames} : {}),
    };
    switch (f.type) {
      case 'progress': {
        if (this.pendingSeek) return; // a progress from before the seek settled says nothing
        const next = this.derive(f);
        if (next !== this.state) { this.state = next; this.emit('state'); }
        this.emit('time');
        return;
      }
      case 'ready': case 'firstFrame': case 'rateStatus': {
        const next = this.derive(f);
        if (next !== this.state) { this.state = next; this.emit('state'); }
        return;
      }
      case 'stalled': this.state = 'buffering'; this.emit('state'); return;
      case 'ended': this.state = 'ended'; this.emit('state'); return;
      case 'seekApplied': case 'seekFailed': {
        if (!this.pendingSeek || f.seekRevision !== this.pendingSeek.revision) return;
        this.pendingSeek = undefined;
        this.send(); // retires the seek natively
        if (f.type === 'seekApplied') this.emitType('seeked');
        else this.emit('state');
        return;
      }
      case 'error': this.fail(f.engineCode ?? (f.networkError ? 'network_error' : 'engine_error'), f.errorDetail); return;
      default: return;
    }
  }

  private derive(f: AppleFact): PlayerState {
    if (this.state === 'error' || this.state === 'ended') return this.state;
    if (f.paused) return 'paused';
    return f.effectiveRate === 0 ? 'buffering' : 'playing';
  }

  private fail(code: string, detail?: string) {
    this.state = 'error';
    const observation = this.observe();
    for (const l of [...this.listeners]) l({type: 'error', observation, code, ...(detail ? {detail: detail.slice(0, 500)} : {})});
    this.send();
  }

  private emit(type: 'state' | 'time') { this.emitType(type); }
  private emitType(type: 'state' | 'time' | 'seeked') {
    if (this.disposed) return;
    const observation = this.observe();
    for (const l of [...this.listeners]) l({type, observation} as PlayerEvent);
  }
}
