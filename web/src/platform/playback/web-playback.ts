/**
 * The web's one playback path.
 *
 *   PlaybackService            what the screens read and command (shared with the native apps)
 *        │  snapshot ▼   ▲ facts
 *   WebPlayback                this file: relays intent down and facts up
 *        │
 *   PlaybackSessionController  the server session: timeline, re-plan, lease, server end
 *        │
 *   WebMediaPlayer             the `<video>` element and hls.js
 *
 * `WebPlayback` is three things the rest of the stack already knows how to talk to:
 *
 * - the service's adapter (`apply`): when the snapshot names a session it attaches it to the
 *   controller, which loads it; a pending seek becomes a seek on the port and is acknowledged only
 *   once the picture has landed; intent becomes play and pause, on change, never on every tick;
 * - the service's `PlaybackApi` and the queue player's session transport (`transport`): sessions
 *   start and change over Playback Protocol v1 only, progress is the controller's timeline, an
 *   engine failure is reported to the server, a stop carries the last position;
 * - the owner of the element's lifetime (`mount`): the controller outlives the element, so a
 *   session adopted before the player screen has mounted loads when it does.
 *
 * Live channels keep their own session authority (`V1ChannelControl`) and only use the port.
 * Music with gapless, crossfade or normalisation is decoded by the device's own audio engine; the
 * element is idle then, and the controller is told the position from the service's readings.
 */
import type {PlaybackAdapter, PlaybackApi, PlaybackService, PlaybackSession, PlaybackSnapshot, SessionEndInfo} from '@core/index.ts';
import type {PlayerFailureCode} from '@core/client-profile.ts';
import {AutomaticQuality, qualityRequest, type FixedQuality} from '@core/playback-v1/choices.ts';
import {PlaybackApiError, idempotencyKey, type V1Http} from '@core/playback-v1/http.ts';
import {PlaybackOptionsClient} from '@core/playback-v1/options.ts';
import type {MediaPlayer, PlayerEvent, PlayerObservation, PlayerSource} from '@core/playback-v1/port.ts';
import {localPlayerSource, localPlayerLoadReceipt, type LocalPlayerSource, type LocalPlayerLoadContext, type LocalPlayerLoadReceipt} from '@core/playback-v1/local-port.ts';
import {PlaybackSessionController, SessionsClient, type SessionChange, type StartContext, type StartOptions, type StartTarget, type Timers} from '@core/playback-v1/sessions.ts';
import {requireSessionChangeCurrent, type SessionChangeContext} from '@core/playback-v1/change-context.ts';
import {playerFailureCommand} from '@core/player-failure-command.ts';
import {toPlaybackSession} from '@core/playback-v1/session-shape.ts';
import {parseEqualizer, type EqualizerSettings} from './audio/equalizer';
import {saveDeviceEqualizer} from './audio/equalizer-store';
import type {BrowserAudioFacts} from './audio/output';
import {listeningDiagnostic, type ListeningEffectsPort} from './listening-effects';
import type {DiscTitleRef, Session} from '@core/playback-v1/types.ts';
import type {QueueSessionTransport} from '@core/playback-v1/v1-queue-player.ts';
import {QueueSelectionRegistrations, type QueueSelectionRegistration} from '@core/playback-v1/selection-registration.ts';
import type {AirPlaySource} from './airplay.ts';
import {WebMediaPlayer, type HlsModule, type SubtitleTimeline, type VideoLike, type WebPlayerNotice, type WebPlayerTimers, type WebMediaPlayerOptions} from './web-media-player.ts';
import type {WebPreparingStarts, WebStartRegistration} from './preparing-start';

/** What the binding tells the player screen in words (the screen owns the wording). */
export type WebPlaybackNotice = 'autoplayBlocked' | 'qualityLowered' | 'qualityRaised';

/** Structural native/web media port. A platform adapter owns one generation at a time. */
export type PlaybackMediaPort = MediaPlayer & {
  /** Actual decoder frame command only. At execution the adapter must recheck its private
   * loaded entry/lifetime as well as this public generation, which can repeat across sessions.
   * Native readiness belongs to the adapter; ordinary observations supply the resulting clock. */
  setAirPlaySource?(source: AirPlaySource | null): void;
  stepFrame?(direction: -1 | 1, generation: number): void | Promise<void>;
  /** Validated session/processing input. Native adapters serialize commands and fence by session+generation. */
  applyAudioEffects?(snapshot: PlaybackSnapshot): void;
  /** Actual current-load observation. The adapter clears it on replacement, including reuse
   * of a public generation, and publishes only after its private engine has observed the graph. */
  readonly audioProcessing?: Readonly<{generation: number; eqApplied: boolean; levelingApplied: boolean; levelingGainDb?: number}>;
  readonly nativeSurfaceActive?: boolean;
  readonly loaded: number;
  readonly settling: boolean;
  onNotice(listener: (notice: WebPlayerNotice) => void): () => void;
  unload(): void;
  suspend(): void;
  resume(): void;
};

/** The device's own audio engine for music (`DecodeAudioRender`). */
export type RenderEngine = {apply(snapshot: PlaybackSnapshot): void; dispose(): void; transportPosition(): number; audioFacts?(): BrowserAudioFacts | undefined; applyEqualizer?(settings: EqualizerSettings): void};

/** Failure codes this path stores as the service's error; `playbackFailure` words them. */
export const failure = {
  streamGone: 'stream_gone',
  unreachable: 'server_unreachable',
  engineUnavailable: 'engine_unavailable',
  noStreaming: 'streaming_unsupported',
  cannotPlay: 'cannot_play_here',
  seek: 'That position could not be reached. Try seeking again.',
} as const;

export type WebPlaybackHost = Readonly<{
  preparingStarts?: WebPreparingStarts;
  /** Capture the actual stack/run/intent/queue witness before dispatch. */
  captureStartCurrent?(): () => boolean;
  recordDiagnostic?: import('../app-log').AppLogRecorder;
  http: V1Http;
  /** Makes a capability path absolute on the selected server's current route. */
  mediaUrl(path: string): string;
  subscribeRoute?(listener: () => void): () => void;
  getRouteSnapshot?(): Readonly<{phase: string}> | null;
  /** Ask the app to find the server again (it may have moved to another of its addresses). */
  recoverRoute?(): Promise<unknown> | void;
  loadHls?: () => Promise<HlsModule>;
  createPlayer?: (video: VideoLike, options: WebMediaPlayerOptions) => PlaybackMediaPort;
  createRender?: (service: PlaybackService, video: VideoLike) => RenderEngine;
  /** A request that outlives the page (`HttpLocalApi.farewell`). */
  farewell?(path: string, method: string, body?: unknown): void;
  deviceMemory?: number;
  /** When the browser's own HLS player is used instead of hls.js (`native-hls.ts`). */
  nativeHls?: 'first' | 'fallback' | 'never';
  timers?: Timers;
  /** The media element port's clock and timers (tests drive them by hand). */
  playerTimers?: WebPlayerTimers;
  key?: () => string;
}>;

/** Private DOM resource identity: effects in the player screen that belong to one presentation. */
export function webPlaybackResourceKey(snapshot: {audioEffects?: {rendering: boolean}; intentId: number; session?: {id: string; generation: number} | null}): string {
  if (snapshot.audioEffects?.rendering && snapshot.session) return 'audio-render';
  return JSON.stringify([snapshot.intentId, snapshot.session?.id ?? null, snapshot.session?.generation ?? null]);
}

/**
 * The controller's port. The controller lives as long as the viewer's playback stack; the
 * element comes and goes with the player screen. Commands given while no element is mounted are
 * remembered and applied when one is; while the device's own audio engine plays (`hold`), the
 * element is left idle.
 */
export class PlayerSlot implements MediaPlayer {
  private real?: PlaybackMediaPort;
  private off?: () => void;
  private offNotice?: () => void;
  private source?: PlayerSource | LocalPlayerSource;
  private localReceipt?: LocalPlayerLoadReceipt;
  private localLoad?: AbortController;
  private loadEpoch = 0;
  private want?: 'play' | 'pause';
  private rate = 1;
  private held = false;
  private readonly listeners = new Set<(event: PlayerEvent) => void>();
  private readonly notices = new Set<(notice: WebPlayerNotice) => void>();

  load(source: PlayerSource): void {
    this.invalidateLocal();
    this.source = source;
    this.want = undefined;
    if (!this.held) this.real?.load(source);
  }
  private invalidateLocal(): void {++this.loadEpoch;this.localReceipt = undefined;this.localLoad?.abort();this.localLoad = undefined;}
  get canLoadLocal(): boolean {return !this.held && typeof this.real?.loadLocal === 'function';}
  async loadLocal(input: LocalPlayerSource, context: LocalPlayerLoadContext): Promise<LocalPlayerLoadReceipt> {
    if (!this.canLoadLocal) throw new Error('local_playback_unavailable');
    const source = localPlayerSource(input), real = this.real!;
    if (context.signal.aborted || !context.isCurrent()) throw new DOMException('Local load retired', 'AbortError');
    this.invalidateLocal();
    const epoch = this.loadEpoch, life = new AbortController();this.localLoad = life;
    this.source = source;this.want = undefined;
    const signal = AbortSignal.any([context.signal, life.signal]);
    const current = () => !signal.aborted && this.loadEpoch === epoch && this.real === real && this.source === source && !this.held && context.isCurrent();
    try {
      const receipt = localPlayerLoadReceipt(await real.loadLocal!(source, {signal, isCurrent: current}), source);
      if (!current()) throw new DOMException('Local load retired', 'AbortError');
      this.localReceipt = receipt;
      return receipt;
    } catch (error) {
      // The adapter retires its accepted private load, never a newer replacement. The slot
      // must not issue a generic unload after an obsolete promise resolves.
      if (this.loadEpoch === epoch && this.source === source) {this.source = undefined;this.invalidateLocal();if (this.real === real) real.unload();}
      throw error;
    }
  }
  play(): void { this.want = 'play'; if (!this.held) this.real?.play(); }
  pause(): void { this.want = 'pause'; if (!this.held) this.real?.pause(); }
  seek(positionMs: number): void {
    if (this.real && !this.held) this.real.seek(positionMs);
    else if (this.source) this.source = {...this.source, startPositionMs: Math.max(0, positionMs)};
  }
  setRate(rate: number): void { this.rate = rate; this.real?.setRate(rate); }
  selectSidecarSubtitle(trackId: string | null): void { this.real?.selectSidecarSubtitle(trackId); }
  observe(): PlayerObservation {
    if (this.real && !this.held && this.loaded) return this.real.observe();
    if (this.real && !this.held && this.source && 'kind' in this.source && this.localReceipt) {
      const actual = this.real.observe();
      if (actual.generation === this.source.generation && actual.state === 'error') return actual;
    }
    return {generation: this.source?.generation ?? 0, state: 'idle', positionMs: this.source?.startPositionMs ?? 0, rate: this.rate};
  }
  onEvent(listener: (event: PlayerEvent) => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }
  onNotice(listener: (notice: WebPlayerNotice) => void): () => void {
    this.notices.add(listener);
    return () => { this.notices.delete(listener); };
  }
  dispose(): void { this.unmount(); this.listeners.clear(); this.notices.clear(); }

  setAirPlaySource(source: AirPlaySource | null): void {if (!this.held) this.real?.setAirPlaySource?.(source);}

  get settling(): boolean { return !!this.real && !this.held && this.real.settling; }
  applyAudioEffects(snapshot: PlaybackSnapshot): void { this.real?.applyAudioEffects?.(snapshot); }
  get nativeSurfaceActive(): boolean {
    const real = this.real, source = this.source;
    if (this.held || !source || !real || !this.loaded || real.loaded !== source.generation || real.nativeSurfaceActive !== true) return false;
    const observed = real.observe();
    return observed.generation === source.generation && !['idle', 'ended', 'error'].includes(observed.state);
  }
  get mounted(): boolean { return !!this.real; }
  get canStepFrame(): boolean {
    const real = this.real, source = this.source;
    if (this.held || !source || !real || !this.loaded || real.loaded !== source.generation || real.settling || typeof real.stepFrame !== 'function') return false;
    const observed = real.observe();
    return observed.generation === source.generation && observed.state === 'paused';
  }
  async stepFrame(direction: -1 | 1, generation: number): Promise<void> {
    if ((direction !== -1 && direction !== 1) || this.source?.generation !== generation || !this.canStepFrame) throw new Error('playback_frame_step_unavailable');
    const real = this.real!, source = this.source;
    await real.stepFrame!(direction, generation);
    if (this.real !== real || this.source !== source || this.source?.generation !== generation || !this.canStepFrame) throw new Error('stale_playback_command');
  }
  /** The generation the element has loaded (0: nothing). */
  get loaded(): number { return this.real && !this.held && (!this.source || !('kind' in this.source) || !!this.localReceipt) ? this.real.loaded : 0; }
  get audioProcessing(): PlaybackMediaPort['audioProcessing'] {
    const source = this.source, real = this.real;
    if (this.held || !source || !real || !this.loaded || real.loaded !== source.generation) return undefined;
    const facts = real.audioProcessing, observed = real.observe();
    if (this.real !== real || this.source !== source || !facts || facts.generation !== source.generation || observed.generation !== source.generation
      || !['playing', 'paused', 'buffering'].includes(observed.state) || typeof facts.eqApplied !== 'boolean' || typeof facts.levelingApplied !== 'boolean') return undefined;
    return Object.freeze({generation: facts.generation, eqApplied: facts.eqApplied, levelingApplied: facts.levelingApplied, ...(facts.levelingApplied&&facts.levelingGainDb!==undefined?{levelingGainDb: facts.levelingGainDb}:{})});
  }

  mount(real: PlaybackMediaPort): void {
    this.unmount();
    this.real = real;
    this.off = real.onEvent(e => {if (this.source && 'kind' in this.source && !this.localReceipt) return;for (const l of [...this.listeners]) l(e);});
    this.offNotice = real.onNotice(n => { for (const l of [...this.notices]) l(n); });
    real.setRate(this.rate);
    if (this.source && !('kind' in this.source) && !this.held) real.load({...this.source, autoplay: this.want ? this.want === 'play' : this.source.autoplay});
  }

  unmount(): void {
    const real = this.real;
    if (!real) return;
    this.invalidateLocal();
    if (this.source && 'kind' in this.source) this.source = undefined;
    // The presentation is remembered where it was, for the next element.
    if (this.source && !this.held && real.loaded === this.source.generation) this.source = {...this.source, startPositionMs: real.observe().positionMs};
    this.off?.();
    this.offNotice?.();
    this.real = undefined;
    real.dispose();
  }

  /** The device's own audio engine is playing (true): the element stays idle and silent. */
  hold(on: boolean): void {
    if (this.held === on) return;
    this.held = on;
    if (on) {this.invalidateLocal();this.real?.unload();}
  }

  unload(): void {
    this.invalidateLocal();
    this.source = undefined;
    this.want = undefined;
    this.real?.unload();
  }
  suspend(): void { this.real?.suspend(); }
  resume(): void { this.real?.resume(); }
}

/** Ends this device caused itself: a queue move replaced the session, or the title played out. */
const OWN_ENDS: readonly string[] = ['replaced', 'completed', 'ended'];

type Lease = {recoverLease(sessionId: string, positionSeconds: number): Promise<boolean>};
export type AutomaticConfig = Readonly<{steps: readonly FixedQuality[]; originalKbps?: number}>;

export class WebPlayback implements PlaybackAdapter {
  readonly slot = new PlayerSlot();
  readonly controller: PlaybackSessionController;
  readonly transport: PlaybackApi & QueueSessionTransport & {sessionUpdated(sessionId: string, revision?: string, data?: unknown): void; session(id: string): Session | undefined};
  private readonly host: WebPlaybackHost;
  private readonly client: SessionsClient;
  private readonly options: PlaybackOptionsClient;
  private readonly known = new Map<string, Session>();
  private readonly startRegistration?: WebStartRegistration;
  private readonly registrations = new QueueSelectionRegistrations();
  private readonly selectionAdoptions = new WeakMap<PlaybackSession, Readonly<{id: string; registration: QueueSelectionRegistration}>>();
  private readonly changedListeners = new Set<(session: Session) => void>();
  private readonly positions = new Map<string, number>();
  private readonly retired = new Set<string>();
  private readonly endedListeners = new Set<(sessionId: string, end: SessionEndInfo) => void>();
  private readonly noticeListeners = new Set<(notice: WebPlaybackNotice) => void>();
  private readonly automaticListeners = new Set<() => void>();
  private service?: PlaybackService;
  private lease?: Lease;
  private video?: VideoLike;
  private render?: RenderEngine;
  private renderRetained?: number;
  private renderState?: string;
  private renderUnavailableSession?: string;
  private renderCreating = false;
  private disposed = false;
  private blocked = false;
  private loadedKey = '';
  private carriedSeek?: number;
  private seekRevision?: number;
  private corrected?: number;
  private appliedIntent?: 'playing' | 'paused';
  /** The port holds a presentation the service loaded itself; the controller is told by `observe`. */
  private direct = false;
  private asked?: string;
  private automatic?: AutomaticQuality;
  private automaticConfig?: AutomaticConfig;
  private automaticItem?: string;
  private automaticStep?: FixedQuality;
  private decidedAt = 0;
  private offs: (() => void)[] = [];
  private localMode = false;

  constructor(host: WebPlaybackHost) {
    this.host = host;
    this.client = new SessionsClient(host.http, host.timers);
    this.options = new PlaybackOptionsClient(host.http);
    this.controller = new PlaybackSessionController({
      http: host.http, player: this.slot, timers: host.timers, key: host.key,
      // The server publishes the whole timeline and follows segment requests: a seek is local.
      serverSeek: () => false,
      wantsPlaying: () => this.service?.getSnapshot().intent === 'playing',
      onServerEnded: (session, end) => this.serverEnded(session.id, end),
      onLeaseExpired: (session, positionMs) => void this.leaseExpired(session, positionMs),
      // A pause or play from another device or an administrator goes through the service, which
      // owns the viewer's intent, so the player and the intent stay one (RN-09).
      onRemoteControl: state => { const service = this.service; if (!service) return; if (state === 'paused') service.pause(); else service.resume(); },
    });
    this.startRegistration = host.preparingStarts?.attach(id => this.controller.getSnapshot().session?.id === id || this.service?.getSnapshot().session?.id === id);
    this.transport = this.makeTransport();
    this.offs.push(this.slot.onEvent(e => this.playerEvent(e)), this.slot.onNotice(n => this.playerNotice(n)), this.controller.subscribe(() => this.controllerChanged()));
    const offRoute = host.subscribeRoute?.(() => this.routeChanged());
    if (offRoute) this.offs.push(offRoute);
  }

  // ── Wiring ──────────────────────────────────────────────────────────────

  /** Join the service and the queue player this path serves. Returns the undo. */
  bind(service: PlaybackService, lease?: Lease): () => void {
    this.service = service;
    this.lease = lease;
    const detach = service.attachAdapter(this);
    this.routeChanged();
    return () => { detach(); if (this.service === service) { this.service = undefined; this.lease = undefined; } };
  }

  /** The player screen's element is in the page. Returns the undo for when it leaves. */
  mount(video: VideoLike, subtitles?: SubtitleTimeline, externalRoute?: () => PlayerObservation['externalRoute']): () => void {
    const player = (this.host.createPlayer ?? ((element, options) => new WebMediaPlayer(element, options)))(video, {
      loadHls: this.host.loadHls, resolveUrl: path => this.host.mediaUrl(path), subtitles, deviceMemory: this.host.deviceMemory, nativeHls: this.host.nativeHls, externalRoute, timers: this.host.playerTimers,
    });
    this.video = video;
    this.slot.mount(player);
    if (this.service) this.apply(this.service.getSnapshot());
    return () => {
      if (this.video !== video) return;
      this.endRender();
      this.video = undefined;
      this.slot.unmount();
    };
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.startRegistration?.dispose();
    for (const off of this.offs.splice(0)) off();
    this.endRender();
    this.controller.dispose();
    this.slot.dispose();
    this.endedListeners.clear();
    this.noticeListeners.clear();
    this.automaticListeners.clear();
  }

  onNotice(listener: (notice: WebPlaybackNotice) => void): () => void {
    this.noticeListeners.add(listener);
    return () => { this.noticeListeners.delete(listener); };
  }
  private say(notice: WebPlaybackNotice) { for (const l of [...this.noticeListeners]) l(notice); }

  /** The speed the viewer chose for the element (the music engine has its own). */
  setRate(rate: number): void { if (Number.isFinite(rate) && rate >= 0.25 && rate <= 4) this.slot.setRate(rate); }

  /** Local queues use this same engine slot but no server-session adapter or renderer. */
  beginLocal(): void {if (this.localMode) return;this.localMode = true;this.release();}

  /** The v1 session behind the service's session, for screens that show what the server decided. */
  session(id: string | undefined): Session | undefined { return id ? this.known.get(id) : undefined; }

  // ── The service's adapter ───────────────────────────────────────────────

  apply = (next: PlaybackSnapshot): void => {
    if (this.disposed || this.localMode) return;
    this.slot.applyAudioEffects(next);
    const session = next.session;
    // Nothing to play, or a failed play: the media is let go. A delayed event cannot revive it.
    if (!session || next.phase === 'error') { this.release(); return; }
    if (this.blocked) return;
    if (this.host.createRender && next.audioEffects.rendering) { this.applyRender(next, session); return; }
    // The renderer is giving this same session back to the element: keep its reporter.
    const handoff = !!this.render && !next.channel && this.holdsPresentation(session);
    this.endRender(handoff);
    const key = `${session.id}:${session.generation}`;
    if (this.loadedKey !== key && this.load(next, session, handoff)) {
      this.loadedKey = key;
      this.carriedSeek = next.pendingSeek?.revision;
      this.seekRevision = next.pendingSeek?.revision;
      this.corrected = undefined;
      this.appliedIntent = next.intent;
    }
    if (this.loadedKey !== key) return;
    const pending = next.pendingSeek;
    if (pending && pending.revision !== this.seekRevision) {
      this.seekRevision = pending.revision;
      this.corrected = undefined;
      this.slot.seek(pending.positionSeconds * 1000);
    }
    // Intent is applied when it changes, never re-asserted: a pause the viewer made from outside
    // this page (a media key, Picture in Picture) is theirs, and is not undone by the next tick.
    const want = next.phase === 'ended' || next.intent === 'paused' || next.failedSeek || next.seekError ? 'paused' : 'playing';
    if (want !== this.appliedIntent) {
      this.appliedIntent = want;
      if (want === 'playing') this.slot.play(); else this.slot.pause();
    }
  };

  /** Hand the snapshot's session to the controller (or, for a live channel, straight to the port). */
  private holdsPresentation(session: PlaybackSession): boolean {
    const held = this.controller.getSnapshot().session;
    return !!held && held.id === session.id && held.presentation.generation === session.generation;
  }

  private load(next: PlaybackSnapshot, session: PlaybackSession, handoff = false): boolean {
    const startPositionMs = Math.max(0, Math.round((next.pendingSeek?.positionSeconds ?? session.resumeSeconds) * 1000));
    const autoplay = next.intent === 'playing';
    this.slot.hold(false);
    if (next.channel) {
      // A channel's session, lease and reports belong to the channel control.
      this.controller.detach();
      const origin = next.linear?.media.originMs;
      this.slot.load({url: session.streamUrl, generation: session.generation, mode: session.mode === 'direct' ? 'direct' : 'stream', startPositionMs, subtitles: [], autoplay, sessionId: session.id, ...(origin !== undefined ? {liveOriginMs: origin} : {})});
      return true;
    }
    const known = this.known.get(session.id);
    if (!known) return false;
    // The controller may already hold a newer presentation of this session (a change whose
    // answer has not reached the service yet): the service catches up, nothing loads twice.
    if (known.presentation.generation > session.generation) return false;
    if (known.presentation.generation === session.generation) {
      this.direct = false;
      // A same-session handoff resumes where the renderer was, under the same reporter and seq.
      if (handoff && this.holdsPresentation(session)) this.controller.reloadMedia({autoplay, startPositionMs: next.pendingSeek ? startPositionMs : Math.max(0, Math.round(next.positionSeconds * 1000))});
      else this.controller.attach(known, {autoplay, startPositionMs});
      return true;
    }
    // The service was handed a newer presentation than the controller has seen: a subtitle
    // choice that re-planned the stream through the subtitle route. The server is asked for the
    // session as it now stands, and the controller loads it when it agrees. When the session
    // itself did not move, what the service was given is loaded as it is, and the session's
    // timeline is kept from the service's readings so its lease does not lapse.
    const key = `${session.id}:${session.generation}`;
    if (this.asked !== key) {
      this.asked = key;
      void this.controller.sessionUpdated(session.id).catch(() => {}).then(() => { if (!this.disposed && this.service) this.apply(this.service.getSnapshot()); });
      return false;
    }
    this.direct = true;
    this.slot.load({url: session.streamUrl, generation: session.generation, mode: session.mode === 'direct' ? 'direct' : 'stream', startPositionMs, subtitles: [], autoplay, sessionId: session.id});
    return true;
  }

  private release() {
    const held = this.controller.getSnapshot();
    if (held.session && held.observation) this.positions.set(held.session.id, held.observation.positionMs);
    this.endRender();
    if (held.session) this.controller.detach();
    this.slot.hold(false);
    this.slot.unload();
    this.loadedKey = '';
    this.carriedSeek = this.seekRevision = this.corrected = undefined;
    this.appliedIntent = undefined;
    this.direct = false;
    this.asked = undefined;
  }

  // ── Facts from the element ──────────────────────────────────────────────

  private current(o: PlayerObservation): {service: PlaybackService; snapshot: PlaybackSnapshot} | undefined {
    if (this.localMode) return;
    const service = this.service, snapshot = service?.getSnapshot(), session = snapshot?.session;
    if (!service || !snapshot || !session || !!this.render || this.blocked || snapshot.phase === 'error') return undefined;
    if (o.generation !== session.generation || this.loadedKey !== `${session.id}:${session.generation}`) return undefined;
    return {service, snapshot};
  }

  private playerEvent(e: PlayerEvent) {
    const now = this.current(e.observation);
    if (!now) return;
    const {service, snapshot} = now, o = e.observation, intent = snapshot.intentId, seconds = o.positionMs / 1000;
    const processing = e.type !== 'error' && ['playing', 'paused', 'buffering'].includes(o.state) ? this.slot.audioProcessing : undefined;
    if (processing && snapshot.session) service.audioProcessingApplied({intentId: intent, sessionId: snapshot.session.id, generation: snapshot.session.generation}, {eqApplied: processing.eqApplied, levelingApplied: processing.levelingApplied, ...(processing.levelingApplied&&processing.levelingGainDb!==undefined?{levelingGainDb: processing.levelingGainDb}:{})});
    if (this.direct && !snapshot.channel && e.type !== 'error' && o.state !== 'idle' && o.state !== 'loading' && !this.slot.settling) this.controller.observe({state: o.state, positionMs: o.positionMs, rate: o.rate, volume: o.volume, muted: o.muted}, e.type);
    switch (e.type) {
      case 'error': this.engineError(service, intent, e.code, e.detail ?? ''); return;
      case 'seeked':
        service.ready(intent);
        this.settle(service, intent, o, true);
        return;
      case 'state':
        if (o.state === 'ended') { service.fact(intent, seconds, 'ended'); return; }
        if (o.state !== 'playing' && o.state !== 'paused') return;
        service.ready(intent);
        if (this.settle(service, intent, o, false)) service.fact(intent, seconds, o.state);
        return;
      case 'time':
        if (o.state !== 'playing' && o.state !== 'paused' && o.state !== 'buffering') return;
        if (this.settle(service, intent, o, false)) service.fact(intent, seconds, o.state === 'paused' ? 'paused' : 'playing');
        this.decide();
    }
  }

  /**
   * Whether the picture is where the service last asked it to be. A pending seek is acknowledged
   * only by a reading at its position; a presentation that landed somewhere else (a re-plan that
   * started where the server last heard) is moved once to where the viewer is.
   */
  private settle(service: PlaybackService, intent: number, o: PlayerObservation, landed: boolean): boolean {
    const pending = service.getSnapshot().pendingSeek;
    if (!pending) return true;
    if (this.slot.settling || (!landed && o.state !== 'playing' && o.state !== 'paused')) return false;
    const seconds = o.positionMs / 1000;
    if (service.seekApplied(intent, pending.revision, seconds, o.state === 'paused' ? 'paused' : 'playing')) return true;
    if (Math.abs(seconds - pending.positionSeconds) > 0.5 && this.corrected !== pending.revision) {
      this.corrected = pending.revision;
      this.slot.seek(pending.positionSeconds * 1000);
    }
    return false;
  }

  private engineError(service: PlaybackService, intent: number, code: string, detail: string) {
    switch (code) {
      case 'stream_gone': service.fail(intent, failure.streamGone); return;
      case 'network_error': service.fail(intent, failure.unreachable); return;
      case 'engine_unavailable': service.fail(intent, failure.engineUnavailable); return;
      case 'unsupported': service.fail(intent, failure.noStreaming); return;
      // The engine cannot play what it was given. That is not yet the viewer's problem: the
      // server is told which route failed, and the title comes back through the next one up.
      case 'decode_error': case 'source_not_supported': case 'engine_error':
        service.engineFailed(intent, code as PlayerFailureCode, detail, failure.cannotPlay); return;
      default: service.engineFailed(intent, 'engine_error', detail, failure.cannotPlay);
    }
  }

  private playerNotice(n: WebPlayerNotice) {
    const service = this.service, snapshot = service?.getSnapshot();
    if (!service || !snapshot?.session || !!this.render) return;
    const intent = snapshot.intentId;
    switch (n.type) {
      case 'alive': service.engineProgress(intent); return;
      case 'starved':
        service.recovering(intent, n.active);
        if (n.active) void Promise.resolve(this.host.recoverRoute?.()).catch(() => {});
        return;
      case 'seek-failed': {
        const pending = snapshot.pendingSeek;
        if (pending) service.seekFailed(intent, pending.revision, failure.seek);
        return;
      }
      case 'autoplay-blocked':
        this.appliedIntent = 'paused';
        this.say('autoplayBlocked');
        service.pause();
        return;
      case 'unsolicited':
        // The viewer paused or started from outside this page: that is their intent.
        this.appliedIntent = n.state;
        this.external(n.state);
        return;
      case 'throughput': this.automatic?.sample(n.kbps); this.decide(); return;
      case 'stall': this.automatic?.stalled(); this.decide(); return;
    }
  }

  /** How an outside pause or play becomes intent. The player engine replaces this while a group
   * owns play and pause. */
  external: (state: 'paused' | 'playing') => void = state => { if (state === 'paused') this.service?.pause(); else this.service?.resume(); };

  // ── The device's own audio engine (music) ───────────────────────────────

  private applyRender(next: PlaybackSnapshot, session: PlaybackSession) {
    const service = this.service, video = this.video;
    if (!service || !video || !this.host.createRender) return; // waits for the element
    if (this.renderUnavailableSession === session.id) return; // this song already fell back
    if (!this.render) {
      if (this.renderCreating) return;
      this.renderCreating = true;
      // The invocation that asked for this renderer owns the fact, captured before any
      // slot or renderer side effect can move the service to a successor.
      const invocation = {sessionId: session.id, generation: session.generation, itemId: next.itemId, intentId: next.intentId};
      try { this.slot.hold(true); this.render = this.host.createRender(service, video); }
      catch {
        this.renderUnavailableSession = session.id;
        this.slot.hold(false);
        listeningDiagnostic('audio_processing_unavailable', undefined, this.host.recordDiagnostic);
        service.listeningFallback('processing', invocation);
        if (!service.fallbackAudioRender()) service.audioRenderFailed('audio_render_failed');
        return;
      }
      finally { this.renderCreating = false; }
    }
    const key = `${session.id}:${session.generation}`;
    if (this.loadedKey !== key) {
      const known = this.known.get(session.id);
      // Attached for its timeline only: the port is held, so nothing loads in the element.
      if (known && known.presentation.generation === session.generation) { this.controller.attach(known, {autoplay: false}); this.loadedKey = key; this.renderState = undefined; }
    }
    const retained = this.renderRetained;
    this.renderRetained = undefined;
    this.render.apply(retained === undefined ? next : {...next, positionSeconds: retained});
    if (this.loadedKey !== key) return;
    // A position is reported once the engine has reached it, never while a start or seek is under way.
    if (next.phase === 'ended') { this.observeRender('ended', next); return; }
    if (next.phase !== 'ready' || next.pendingSeek || next.failedSeek) return;
    this.observeRender(next.intent === 'paused' ? 'paused' : next.audioEffects.waiting ? 'buffering' : 'playing', next);
  }

  private observeRender(state: 'playing' | 'paused' | 'buffering' | 'ended', next: PlaybackSnapshot) {
    const changed = state !== this.renderState;
    this.renderState = state;
    this.controller.observe({state, positionMs: next.positionSeconds * 1000, rate: next.audioEffects.rate, volume: this.video?.volume, muted: this.video?.muted}, changed ? 'state' : 'time');
  }

  audioFacts(): BrowserAudioFacts | undefined { return this.render?.audioFacts?.(); }
  async setEqualizer(settings: EqualizerSettings): Promise<void> {
    if (!this.host.createRender || !this.service) throw new Error('equalizer_unavailable');
    const value = await saveDeviceEqualizer(settings);
    this.applyEqualizer(value);
  }

  /** Same committed setting for the Settings entry and player shortcut, including while idle. */
  applyEqualizer(settings: EqualizerSettings): void {
    const value = parseEqualizer(settings), service = this.service;
    if (!this.host.createRender || !service || this.disposed) throw new Error('equalizer_unavailable');
    try {
      // Apply off before releasing an EQ-only renderer. Turning on can create it synchronously.
      const previous = this.render;
      if (previous) {
        if (!previous.applyEqualizer) throw new Error('equalizer_unavailable');
        previous.applyEqualizer(value);
      }
      service.setDeviceAudioProcessing(value.enabled);
      if (value.enabled && this.render && this.render !== previous) {
        if (!this.render.applyEqualizer) throw new Error('equalizer_unavailable');
        this.render.applyEqualizer(value);
      }
    } catch (error) { service.setDeviceAudioProcessing(false); throw error; }
  }

  /** `handoff` keeps the session attached for a same-session reload into the element;
   * every true retirement (unmount, release, dispose, replacement) still detaches. */
  private endRender(handoff = false) {
    if (!this.render) return;
    this.render.dispose();
    this.render = undefined;
    this.renderState = undefined;
    this.loadedKey = '';
    // Attached for its timeline only: whatever plays next attaches afresh and loads.
    if (!handoff && this.controller.getSnapshot().session) this.controller.detach();
    this.slot.hold(false);
  }

  // ── Routes (the app moving between a server's addresses) ────────────────

  private routeChanged() {
    if (this.localMode) return;
    const route = this.host.getRouteSnapshot?.(), service = this.service;
    if (!route || !service) return;
    if (route.phase !== 'ready') {
      if (this.blocked) return;
      this.blocked = true;
      if (this.render) {
        this.renderRetained = this.render.transportPosition();
        if (service.getSnapshot().audioEffects.prepared) {
          listeningDiagnostic('audio_transition_unavailable', undefined, this.host.recordDiagnostic);
          (service as unknown as ListeningEffectsPort).audioPreparationFailed();
        }
        this.render.dispose();
        this.render = undefined;
      }
      this.slot.suspend();
      return;
    }
    if (!this.blocked) return;
    this.blocked = false;
    this.slot.resume();
    this.apply(service.getSnapshot());
  }

  // ── The controller's news ───────────────────────────────────────────────

  /** A presentation the controller adopted by itself (a change made elsewhere, a 412 recovery)
   * reaches the screens through the service. */
  private controllerChanged() {
    const held = this.controller.getSnapshot().session, service = this.service;
    if (!held || !service) return;
    this.remember(held);
    const shown = service.getSnapshot().session;
    if (shown?.id === held.id && held.presentation.generation > shown.generation) service.installPresentation(toPlaybackSession(held, shown.duration));
  }

  private serverEnded(sessionId: string, end: SessionEndInfo) {
    if (this.retired.has(sessionId)) return;
    // This device's own doing (the queue moved on, the title finished) is not news to it, even
    // when the server's notice of it overtakes the answer that carries the next session.
    if (OWN_ENDS.includes(end.reason)) return;
    this.registrations.changed(sessionId);
    this.known.delete(sessionId);
    this.startRegistration?.forget(sessionId);
    for (const l of [...this.endedListeners]) l(sessionId, end);
  }

  /** The lease lapsed while this device could not report (a sleeping laptop): the queue starts
   * its entry again where it was. Only when that cannot be done does the viewer see why. */
  private async leaseExpired(session: Session, positionMs: number) {
    const service = this.service, snapshot = service?.getSnapshot();
    if (!service || snapshot?.session?.id !== session.id) return;
    // A title that had finished or already failed stays as it is.
    if (snapshot.phase !== 'starting' && snapshot.phase !== 'ready') { if (snapshot.phase !== 'ended') this.serverEnded(session.id, {reason: 'lease_expired'}); return; }
    service.recovering(snapshot.intentId, true);
    const recovered = await (this.lease?.recoverLease(session.id, positionMs / 1000).catch(() => false) ?? Promise.resolve(false));
    const now = service.getSnapshot();
    service.recovering(now.intentId, false);
    if (!recovered && now.session?.id === session.id && now.phase !== 'error') this.serverEnded(session.id, {reason: 'lease_expired'});
  }

  // ── Automatic quality ───────────────────────────────────────────────────

  /**
   * Automatic quality for the playing title: on with what the source offers, or off (null).
   * It starts at Original and asks the server to step down on a sustained shortfall or repeated
   * stalls, and back up after sustained headroom. A title that follows one stepped down (the
   * server carries the quality to the next queue entry) starts from that step.
   */
  setAutomatic(config: AutomaticConfig | null): void {
    const item = this.service?.getSnapshot().itemId;
    if (!config) { if (this.automatic) { this.automatic = undefined; this.automaticConfig = undefined; this.told(); } return; }
    const same = this.automatic && this.automaticItem === item && this.automaticConfig && JSON.stringify(this.automaticConfig) === JSON.stringify(config);
    if (same) return;
    const carried = this.automaticStep && config.steps.find(s => s.id === this.automaticStep!.id);
    this.automatic = new AutomaticQuality({...config, now: this.host.timers?.now, ...(carried ? {startAt: carried} : {})});
    this.automaticConfig = config;
    this.automaticItem = item;
    this.automaticStep = carried;
    this.told();
  }

  /** Automatic is on and playing at this step; undefined is Original. */
  get automaticAt(): FixedQuality | undefined { return this.automatic ? this.automaticStep : undefined; }
  get automaticOn(): boolean { return !!this.automatic; }
  /** The viewer picked a quality themselves: Automatic forgets where it had stepped to. */
  forgetAutomaticStep(): void { this.automaticStep = undefined; }
  subscribeAutomatic = (listener: () => void): (() => void) => { this.automaticListeners.add(listener); return () => { this.automaticListeners.delete(listener); }; };
  private told() { for (const l of [...this.automaticListeners]) l(); }

  private decide() {
    const auto = this.automatic, service = this.service;
    if (!auto || !service) return;
    const at = (this.host.timers?.now ?? Date.now)();
    if (at - this.decidedAt < 1000) return;
    this.decidedAt = at;
    const snapshot = service.getSnapshot();
    if (snapshot.channel || snapshot.phase !== 'ready' || snapshot.pendingSeek || !!this.render) return;
    const step = auto.decide();
    if (step === undefined) return;
    const lowered = !!step && (!this.automaticStep || step.height < this.automaticStep.height);
    this.automaticStep = step ?? undefined;
    this.told();
    this.say(lowered ? 'qualityLowered' : 'qualityRaised');
    void service.changePlayback({quality: qualityRequest('automatic', step ?? undefined)}).catch(() => {});
  }

  // ── The page going away ─────────────────────────────────────────────────

  /**
   * The page is being hidden. `final`: it is going for good (a close, a reload, a navigation),
   * so the session is stopped with its position in a request that outlives the page; the stream
   * slot and any converter are released at once instead of when the lease runs out. Otherwise
   * (a hidden tab, a page kept for Back) the newest position is reported and playback stays.
   */
  pageHidden(final: boolean): void {
    if (this.localMode) return;
    const held = this.controller.getSnapshot();
    // A live channel's session belongs to the channel control, not the controller: closing the tab
    // ends it too, so its tuner (or its conversion) is free at once instead of when its lease lapses.
    const tuned = this.service?.getSnapshot();
    if (final && this.host.farewell && tuned?.channel && tuned.session?.id && !held.session) {
      this.host.farewell(`/v1/playback/sessions/${encodeURIComponent(tuned.session.id)}`, 'DELETE', {generation: tuned.session.generation});
      return;
    }
    if (!held.session || held.phase === 'ended') return;
    if (!final || !this.host.farewell) { void this.controller.flush().catch(() => {}); return; }
    const service = this.service?.getSnapshot();
    const positionMs = this.render && service?.session?.id === held.session.id ? service.positionSeconds * 1000 : held.observation?.positionMs;
    this.host.farewell(`/v1/playback/sessions/${encodeURIComponent(held.session.id)}`, 'DELETE', {generation: held.session.presentation.generation, ...(positionMs !== undefined ? {positionMs: Math.max(0, Math.round(positionMs))} : {})});
  }

  // ── PlaybackApi and the queue player's transport ────────────────────────

  private remember(session: Session): Session {
    if (this.startRegistration?.remember(session.id) === false) throw new DOMException('Original preparation cleanup is settling', 'AbortError');
    this.registrations.changed(session.id);
    this.known.set(session.id, session);
    // Bounded: sessions leave by retire, forget and stop; this is the backstop.
    if (this.known.size > 16) {
      const oldest = this.known.keys().next().value!;
      this.registrations.changed(oldest);this.known.delete(oldest);this.startRegistration?.forget(oldest);
    }
    return session;
  }

  private dropped(id: string) {
    this.startRegistration?.forget(id);
    this.registrations.changed(id);
    this.known.delete(id);
    this.positions.delete(id);
    if (this.controller.getSnapshot().session?.id === id) {
      this.controller.detach();
      // The element keeps what it shows until the service names the next session.
    }
  }

  /** Private receipt for exactly this registration (or known absence). Never
   * detach the controller/service: an installed occurrence belongs to its owner. */
  private captureSelectionRegistration(id: string): QueueSelectionRegistration {
    const original = this.known.get(id);
    const uninstalled = () => !this.disposed && this.controller.getSnapshot().session?.id !== id && this.service?.getSnapshot().session?.id !== id;
    return this.registrations.capture(id, () => uninstalled() && this.known.get(id) === original, () => {
      if (!uninstalled() || this.known.get(id) !== original) return false;
      this.known.delete(id);this.positions.delete(id);this.registrations.changed(id);this.startRegistration?.forget(id);
      return true;
    });
  }

  private makeTransport(): WebPlayback['transport'] {
    const self = this;
    const duration = async (itemId: string, versionId?: string, signal?: AbortSignal, discTitle?: DiscTitleRef): Promise<number> => {
      if (!itemId) return 0;
      try {
        const o = await self.options.options(itemId, {versionId, discTitle}, signal);
        const v = o.versions.find(x => x.id === (versionId ?? o.preferred?.versionId));
        return v ? v.durationMs / 1000 : 0;
      } catch { return 0; } // The engine reports the duration once it has the media.
    };
    const start = async (target: StartTarget, options: StartOptions, key: string = (self.host.key ?? idempotencyKey)(), durationSeconds?: number, signal?: AbortSignal,
      context?: Pick<StartContext, 'isCurrent' | 'capturePreparing' | 'onPreparing'>) => {
      const callerCurrent = context?.isCurrent, callerPreparing = context?.onPreparing, callerCapture = context?.capturePreparing;
      const source = self.service, drawn = source?.getSnapshot(), originalCurrent = self.host.captureStartCurrent?.();
      const current = () => !self.disposed && self.service === source &&
        (!drawn || source?.getSnapshot().intentId === drawn.intentId && source.getSnapshot().playbackRun === drawn.playbackRun) &&
        (!originalCurrent || originalCurrent() === true) && (!callerCurrent || callerCurrent() === true);
      const original = self.startRegistration?.begin(target, options, key, signal, current);
      const lifetime = original?.context ?? {signal, isCurrent: current, capturePreparing: callerCapture};
      const startContext: StartContext = {...lifetime, onPreparing: (wait, notice) => {
        if (!current()) return;
        // The queue owns its own original preparing state; direct PlaybackApi
        // callers use the same canonical service publication for their intent.
        if (callerPreparing) callerPreparing(wait, notice);
        else if (source && drawn) source.reportStartPreparation(drawn.intentId, notice);
      }};
      let session: Session;
      try {
        // The canonical SDK alone decodes status/variants and owns the retry
        // policy. A late real 201 still enters the original core cleanup.
        session = self.remember(await self.client.start(original?.body ?? target, original ? {} : options, original?.key ?? key, startContext));
      } finally {
        original?.finish();
        if (!callerPreparing && source && drawn && current()) source.reportStartPreparation(drawn.intentId, undefined);
      }
      const seconds = session.discTitle
        ? await duration(session.itemId ?? '', session.versionId, signal, session.discTitle)
        : durationSeconds ?? await duration(session.itemId ?? '', session.versionId, signal);
      return {session, playback: toPlaybackSession(session, seconds)};
    };
    return {
      start,
      duration,
      adopt: (session: Session, durationSeconds: number) => {
        const playback = toPlaybackSession(self.remember(session), durationSeconds);
        self.selectionAdoptions.set(playback, {id: session.id, registration: self.captureSelectionRegistration(session.id)});
        return playback;
      },
      selectionRegistration(id: string, adopted?: PlaybackSession) {
        if (adopted) {const original = self.selectionAdoptions.get(adopted);return original?.id === id ? original.registration : undefined;}
        // A bare id cannot claim another caller's existing registration.
        return self.known.has(id) ? undefined : self.captureSelectionRegistration(id);
      },
      retire(id: string) { self.retired.add(id); if (self.retired.size > 64) self.retired.delete(self.retired.values().next().value!); self.dropped(id); },
      forget(id: string) { self.dropped(id); },
      handOverObserver: () => undefined,
      session: (id: string) => self.known.get(id),
      async change(id: string, change: SessionChange, durationSeconds: number, context?: SessionChangeContext) {
        requireSessionChangeCurrent(context);
        if (self.controller.getSnapshot().session?.id !== id) throw new PlaybackApiError(410, 'session_ended', 'never');
        const updated = await self.controller.change(change, context);
        requireSessionChangeCurrent(context);
        if (!updated) throw new PlaybackApiError(410, 'session_ended', 'never');
        if (context && (updated.id !== id || self.controller.getSnapshot().session?.presentation.generation !== updated.presentation.generation)) throw new Error('Playback session changed during replan');
        return {session: self.remember(updated), playback: toPlaybackSession(updated, durationSeconds)};
      },
      createPlayback: (itemId: string, requestId: string, signal?: AbortSignal) => start({itemId}, {startFrom: 'resume', state: 'playing'}, requestId, undefined, signal).then(r => r.playback),
      async stopPlayback(id: string, signal?: AbortSignal) {
        const held = self.controller.getSnapshot();
        if (held.session?.id === id && held.phase !== 'ended') {
          await self.controller.stop();
          self.controller.detach();
        } else if (!self.retired.has(id)) {
          await self.client.stop(id, self.positions.get(id), signal, self.known.get(id)?.presentation.generation);
        }
        self.registrations.changed(id);
        self.known.delete(id);
        self.startRegistration?.forget(id);
        self.positions.delete(id);
      },
      // Progress is the controller's timeline, fed by the port itself. Only the end is waited
      // for here: the queue moves on once the server has it.
      async progressPlayback(id: string, payload: {generation: number; sequence: number; positionSeconds: number; state: 'playing' | 'paused' | 'ended'}) {
        if (payload.state !== 'ended' || self.retired.has(id) || self.controller.getSnapshot().session?.id !== id) return;
        await self.controller.confirmEnded();
      },
      async markerSkipped(id: string, skipped: Readonly<{markerId: string; mode: 'automatic' | 'manual'; positionMs: number}>) {
        if (self.controller.getSnapshot().session?.id === id) self.controller.noteSkip(skipped);
      },
      // The engine could not play the presentation: the server is told, and re-plans the same
      // session another way where the picture was. The queue and its place are untouched.
      reportPlayerFailure: (sessionId: string, code: PlayerFailureCode, detail: string, positionSeconds: number) =>
        playerFailureCommand({
          current: () => self.controller.getSnapshot().session?.id,
          change: (failure, positionMs) => self.controller.change({failure, positionMs}),
          retain: session => self.remember(session),
          changed: session => { for (const listener of self.changedListeners) listener(session); },
          playback: session => toPlaybackSession(session, 0),
        }, {sessionId, code, detail, positionSeconds}),
      onSessionChanged(listener: (session: Session) => void) {
        self.changedListeners.add(listener);
        return () => { self.changedListeners.delete(listener); };
      },
      onServerEnded(listener: (sessionId: string, end: SessionEndInfo) => void) {
        self.endedListeners.add(listener);
        return () => { self.endedListeners.delete(listener); };
      },
      async reconcileSession(sessionId: string, signal?: AbortSignal) {
        if (self.retired.has(sessionId) || signal?.aborted) return;
        await self.controller.sessionUpdated(sessionId, undefined, undefined, {signal, strict: true});
      },
      sessionUpdated(sessionId: string, revision?: string, data?: unknown) {
        if (self.retired.has(sessionId)) return;
        const d = data && typeof data === 'object' ? data as {state?: unknown; reason?: unknown} : undefined;
        if (d?.state === 'ended' && typeof d.reason === 'string' && OWN_ENDS.includes(d.reason)) return;
        void self.controller.sessionUpdated(sessionId, revision, data);
      },
    };
  }
}
