/**
 * The web `MediaPlayer` port: the only code that touches the `<video>` element's transport and
 * hls.js. `PlaybackSessionController` drives it; `WebPlaybackBinding` relays the viewer's intent.
 *
 * - `load` starts a presentation generation: the previous engine is destroyed and every event is
 *   tagged with the generation now loaded, so the core drops a late `timeupdate` from an older URL.
 * - Direct play sets `src`; streams use hls.js, or native HLS where hls.js cannot run.
 * - A start position is where loading begins (hls.js `startPosition`), not a seek after loading the
 *   beginning. A position is reported only once the picture has reached it: while a start or a
 *   seek is still on its way, the reading is the last settled position.
 * - Slow is not failed. Nothing here restarts a download that is still receiving bytes, and there
 *   is no wall-clock limit on a fragment that is making progress. A download that receives nothing
 *   for `silentMs` is restarted; playback is given up only after `giveUpMs` with an empty buffer and
 *   no sign of life from the server (bytes, or "still preparing" answers).
 * - A stream the server refuses (401/403/404/410) is `stream_gone`; media errors recover twice
 *   (the second swaps the audio codec) before `decode_error`. What happens next (another route,
 *   a lower quality) is the core's decision.
 * - A pause or play the port did not ask for (a media key, the Picture in Picture window, the
 *   system) is the viewer's doing and is reported as such, never undone.
 * - Subtitles are not attached here: the port tells the one `SubtitleSurface` where cues sit on
 *   the element's clock, and that surface is the single place a track meets the element.
 * - Everything DOM- or hls.js-shaped is behind small interfaces so the port is tested with fakes.
 */
import {airPlaySources, wirelessPlayback, type AirPlaySource} from './airplay.ts';
import type {MediaPlayer, PlayerEvent, PlayerObservation, PlayerSource, PlayerState} from '@core/playback-v1/index.ts';
import {channelMediaTime, channelSourceTime, type DatedFragment} from '@core/playback/channel-clock.ts';

type Ranges = {readonly length: number; start(i: number): number; end(i: number): number};

/** The parts of `HTMLVideoElement` the port uses. */
export type VideoLike = {
  src: string;
  readonly webkitCurrentPlaybackTargetIsWireless?: boolean;
  webkitShowPlaybackTargetPicker?: () => void;
  currentTime: number;
  playbackRate: number;
  volume: number;
  muted: boolean;
  readonly paused: boolean;
  readonly ended: boolean;
  readonly seeking: boolean;
  readonly readyState: number;
  readonly duration: number;
  readonly error: {readonly code: number; readonly message?: string} | null;
  readonly buffered: Ranges;
  readonly seekable: Ranges;
  play(): Promise<void> | void;
  pause(): void;
  load(): void;
  removeAttribute(name: string): void;
  canPlayType(type: string): string;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
  getVideoPlaybackQuality?(): {droppedVideoFrames: number};
  getStartDate?(): Date;
};

export type HlsFragmentLike = {start: number; startPTS?: number; stats?: {loaded: number; loading?: {first: number; end: number}}};
export type HlsErrorData = {fatal?: boolean; type?: string; details?: string; response?: {code?: number; data?: unknown}};
/** The parts of an hls.js instance the port uses. */
export type HlsLike = {
  attachMedia(media: unknown): void;
  loadSource(url: string): void;
  startLoad(position?: number): void;
  recoverMediaError(): void;
  swapAudioCodec(): void;
  destroy(): void;
  on(event: string, listener: (event: string, data: never) => void): void;
  readonly bandwidthEstimate?: number;
  readonly playingDate?: Date | null;
  readonly levels?: readonly {bitrate?: number; details?: {fragments: readonly DatedFragment[]}}[];
  readonly currentLevel?: number;
  loadLevel?: number;
  subtitleDisplay?: boolean;
  subtitleTrack?: number;
};
/** The hls.js constructor surface. */
export type HlsModule = {
  isSupported(): boolean;
  new (config: Record<string, unknown>): HlsLike;
  Events: {MEDIA_ATTACHING?: string; MEDIA_DETACHING?: string; ERROR: string; FRAG_LOADING?: string; FRAG_LOADED?: string; FRAG_CHANGED?: string; MANIFEST_PARSED?: string; SUBTITLE_TRACKS_UPDATED?: string};
  ErrorTypes: {NETWORK_ERROR: string; MEDIA_ERROR: string};
};

/**
 * Where subtitle cues sit on the element's clock. hls.js may place a stream's first frame at a
 * time other than its playlist time; cues are written in title time, so the surface that draws
 * them is told the difference. `null`: not known yet, show nothing.
 */
export interface SubtitleTimeline {
  reset(): void;
  timeline(offsetSeconds: number | null): void;
}

/** What the port tells the web binding beyond the protocol's events. */
export type WebPlayerNotice =
  /** A seek the element could not perform. */
  | Readonly<{type: 'seek-failed'; positionMs: number}>
  /** The buffer ran dry while media is being fetched again (true), or it is flowing again (false). */
  | Readonly<{type: 'starved'; active: boolean}>
  /** Media bytes arrived, or the server said it is still preparing: the start or seek in hand is alive. */
  | Readonly<{type: 'alive'}>
  /** One media download's throughput. */
  | Readonly<{type: 'throughput'; kbps: number}>
  /** The picture ran out of media while it should have been playing. */
  | Readonly<{type: 'stall'}>
  /** The browser refused to start playback without a gesture. */
  | Readonly<{type: 'autoplay-blocked'}>
  /** The element was paused or started by something other than this port. */
  | Readonly<{type: 'unsolicited'; state: 'paused' | 'playing'}>;

export type WebPlayerTimers = Readonly<{
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
  /** The 250 ms housekeeping tick; tests drive `tick()` themselves. */
  setTicker?: (fn: () => void, ms: number) => unknown;
  clearTicker?: (ticker: unknown) => void;
}>;

export type WebMediaPlayerOptions = Readonly<{
  /** Loads hls.js on first use (a dynamic import in the app). */
  loadHls?: () => Promise<HlsModule>;
  /** Extra hls.js tuning; the port sets start position, buffers, load policies and `xhrSetup`. */
  hlsConfig?: Record<string, unknown>;
  /** Makes a presentation URL absolute (`/v1/media/…` against the selected server's origin). */
  resolveUrl?: (url: string) => string;
  subtitles?: SubtitleTimeline;
  /** When the browser's own HLS player is used instead of hls.js (`native-hls.ts`); absent means never, except where hls.js cannot run at all. */
  nativeHls?: 'first' | 'fallback' | 'never';
  /** Device memory in GB when the browser says (smaller buffers on small devices). */
  deviceMemory?: number;
  /** Where the picture is shown, when not in the page. */
  externalRoute?: () => PlayerObservation['externalRoute'];
  timers?: WebPlayerTimers;
  /** A download that receives nothing for this long is restarted. */
  silentMs?: number;
  /** Playback is given up after this long with an empty buffer and no sign of life. */
  giveUpMs?: number;
}>;

const REFUSED = [401, 403, 404, 410];
const LANDING_SECONDS = 0.5;
/** What a browser lets one source buffer hold is on the order of 150 MB; small devices far less. */
const BUFFER_BUDGET_BYTES = 120 * 1024 * 1024, SMALL_BUFFER_BUDGET_BYTES = 40 * 1024 * 1024;

/** Forward and back buffer targets in seconds for a stream's bit rate (B: 30 s of an 80 Mbps
 * remux is 300 MB, more than a page may buffer). Unknown bit rates get the plain time targets. */
export function bufferTargets(bitrateKbps: number | undefined, deviceMemory?: number): {forward: number; max: number; back: number; bytes: number} {
  const small = typeof deviceMemory === 'number' && deviceMemory <= 2;
  const budget = small ? SMALL_BUFFER_BUDGET_BYTES : BUFFER_BUDGET_BYTES;
  const ceiling = small ? 20 : 30;
  const affordable = bitrateKbps && bitrateKbps > 0 ? (budget * 8) / (bitrateKbps * 1000) : ceiling;
  const forward = Math.max(6, Math.min(ceiling, Math.floor(affordable)));
  const back = Math.max(4, Math.min(small ? 8 : 30, Math.floor(forward / 2) + (forward >= ceiling ? forward / 2 : 0)));
  return {forward, max: Math.max(forward, Math.min(small ? 30 : 60, forward * 2)), back, bytes: Math.round(budget / 4)};
}

type Target = {ms: number; assigned: boolean; start: boolean};
type Recovery = {attempts: number; emptySince: number; retry: () => void; code: string; timer?: unknown; from: number};

export class WebMediaPlayer implements MediaPlayer {
  private readonly video: VideoLike;
  private readonly options: WebMediaPlayerOptions;
  private readonly listeners = new Set<(event: PlayerEvent) => void>();
  private readonly noticeListeners = new Set<(notice: WebPlayerNotice) => void>();
  private readonly now: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (timer: unknown) => void;
  private readonly silentMs: number;
  private readonly giveUpMs: number;
  private handlers: [string, () => void][] = [];
  private ticker?: unknown;
  private generation = 0;
  private state: PlayerState = 'idle';
  private source?: PlayerSource;
  private hls?: HlsLike;
  private module?: HlsModule;
  private airPlay?: ReturnType<typeof airPlaySources>;
  private receiver?: AirPlaySource;
  private receiverPlaying = false;
  private awaitingReceiver = false;
  private loadSerial = 0;
  private subtitle: string | null = null;
  private disposed = false;
  private suspended = false;
  /** Whether this port means the element to be playing: the difference from what it does is news. */
  private wantPlaying = false;
  private settledMs = 0;
  private target?: Target;
  private started = false;
  private rate = 1;
  private mediaRecoveries = 0;
  /** hls.js recovery detaches src and calls load(), which queues one internal pause.
   * Consume it only for this engine/load; metadata/readiness closes the reset window. */
  private mediaRecoveryPause?: {engine: HlsLike; serial: number};
  /** This load has been handed to the browser's own HLS player. */
  private handedToNative = false;
  private recoveredAtMs = 0;
  private recovery?: Recovery;
  private starved = false;
  private loading?: HlsFragmentLike;
  private loadedBytes = 0;
  private loadingAt = 0;
  private bytesAt = 0;
  private aliveAt = 0;
  private aliveToldAt = 0;
  private stallAt = 0;
  private waitingSince = 0;
  private subtitleOffset: number | null = null;

  constructor(video: VideoLike, options: WebMediaPlayerOptions = {}) {
    this.video = video;
    this.options = options;
    const t = options.timers ?? {};
    this.now = t.now ?? Date.now;
    this.setTimer = t.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = t.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>));
    this.silentMs = options.silentMs ?? 20_000;
    this.giveUpMs = options.giveUpMs ?? 90_000;
    const on = (type: string, fn: () => void) => { video.addEventListener(type, fn); this.handlers.push([type, fn]); };
    on('webkitcurrentplaybacktargetiswirelesschanged', () => this.wirelessChanged());
    on('playing', () => this.onPlaying());
    on('play', () => this.onPlay());
    on('pause', () => this.onPause());
    on('waiting', () => this.onWaiting());
    // A late event from the presentation before this one must not speak for this one.
    on('ended', () => { if (this.active() && !this.suspended && this.video.ended) this.setState('ended'); });
    on('loadedmetadata', () => { this.mediaRecoveryPause = undefined; this.attempt(); this.finish(); });
    on('loadeddata', () => this.onReady());
    on('canplay', () => this.onReady());
    on('durationchange', () => { this.attempt(); this.finish(); });
    on('progress', () => { if (this.active() && !this.hls) this.alive(true); this.attempt(); this.finish(); });
    on('timeupdate', () => this.onTime());
    on('seeked', () => this.onSeeked());
    on('error', () => this.nativeError());
    const setTicker = t.setTicker ?? ((fn, ms) => setInterval(fn, ms));
    this.ticker = setTicker(() => this.tick(), 250);
  }

  // ── The port ────────────────────────────────────────────────────────────

  load(source: PlayerSource): void {
    if (this.disposed) return;
    this.teardown();
    this.source = source;
    this.generation = source.generation;
    this.wantPlaying = source.autoplay;
    this.suspended = false;
    this.subtitle = null;
    this.mediaRecoveries = 0;
    this.handedToNative = false;
    this.started = false;
    this.settledMs = Math.max(0, source.startPositionMs);
    this.state = 'loading';
    this.awaitingReceiver = wirelessPlayback(this.video);
    if (!this.awaitingReceiver) this.open(this.settledMs);
    this.emit({type: 'state', observation: this.observe()});
  }

  play(): void {
    this.wantPlaying = true;
    if (!this.active() || this.state === 'loading' || this.state === 'error' || this.target) return;
    this.tryPlay();
  }

  pause(): void {
    this.wantPlaying = false;
    if (this.active()) this.video.pause();
  }

  seek(positionMs: number): void {
    if (!this.active()) return;
    this.target = {ms: Math.max(0, positionMs), assigned: false, start: false};
    this.attempt();
  }

  setRate(rate: number): void {
    this.rate = rate;
    if (this.video.playbackRate !== rate) this.video.playbackRate = rate;
  }

  /** The port records which sidecar the viewer has on (it is reported in the timeline); drawing it
   * belongs to the subtitle surface. */
  selectSidecarSubtitle(trackId: string | null): void {
    this.subtitle = trackId;
    this.emit({type: 'state', observation: this.observe()});
  }

  observe(): PlayerObservation {
    const v = this.video;
    const dropped = v.getVideoPlaybackQuality?.().droppedVideoFrames;
    const bandwidth = this.hls?.bandwidthEstimate;
    const external = this.options.externalRoute?.();
    const ahead = this.active() && !this.suspended ? this.bufferedAhead() : 0;
    return {
      generation: this.generation, state: this.state, positionMs: Math.max(0, Math.round(this.positionMs())), rate: this.rate,
      volume: v.volume, muted: v.muted, subtitleTrackId: this.subtitle,
      ...(ahead > 0 ? {bufferedMs: Math.round(ahead * 1000)} : {}),
      ...(dropped !== undefined ? {droppedFrames: dropped} : {}),
      ...(bandwidth !== undefined && Number.isFinite(bandwidth) ? {bandwidthKbps: Math.round(bandwidth / 1000)} : {}),
      ...(external ? {externalRoute: external} : {}),
    };
  }

  onEvent(listener: (event: PlayerEvent) => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  dispose(): void {
    if (this.disposed) return;
    this.teardown();
    for (const [type, fn] of this.handlers) this.video.removeEventListener(type, fn);
    this.handlers = [];
    (this.options.timers?.clearTicker ?? (t => clearInterval(t as ReturnType<typeof setInterval>)))(this.ticker);
    this.listeners.clear();
    this.noticeListeners.clear();
    this.disposed = true;
    this.state = 'idle';
  }

  // ── Beyond the port (the web binding) ───────────────────────────────────

  onNotice(listener: (notice: WebPlayerNotice) => void): () => void {
    this.noticeListeners.add(listener);
    return () => { this.noticeListeners.delete(listener); };
  }

  /** Only a server-prepared receipt for this private load may become an alternative. */
  setAirPlaySource(receiver: AirPlaySource | null): void {
    const source = this.source;
    if (!source || this.disposed || receiver && (receiver.sessionId !== source.sessionId || receiver.generation !== source.generation)) return;
    if (receiver && this.receiver?.sessionId === receiver.sessionId && this.receiver.generation === receiver.generation && this.receiver.url === receiver.url && !this.awaitingReceiver) return;
    if (!receiver && !this.receiver) return;
    this.receiver = receiver ?? undefined;
    if (receiver && wirelessPlayback(this.video)) this.openReceiver();
    else this.airPlay?.setReceiver(receiver ? this.resolve(receiver.url) : undefined);
    this.emit({type: 'state', observation: this.observe()});
  }
  private openReceiver(): void {
    const receiver = this.receiver;
    if (!receiver || !this.active() || this.suspended) return;
    const at = this.target?.ms ?? this.positionMs();
    // A module import belongs to the local open that requested it, not this native handoff.
    ++this.loadSerial;
    this.airPlay?.detach();this.hls?.destroy();this.hls = undefined;this.airPlay?.dispose();
    this.receiverPlaying = true;this.awaitingReceiver = false;
    this.state = 'loading';this.started = false;
    this.options.subtitles?.reset();this.options.subtitles?.timeline(0);
    this.target = at > 0 ? {ms: at, assigned: false, start: true} : undefined;
    this.video.src = this.resolve(receiver.url);this.video.load();
  }
  private wirelessChanged(): void {
    if (!this.active() || this.suspended) return;
    if (wirelessPlayback(this.video)) {if (this.receiver && !this.receiverPlaying) this.openReceiver();}
    else if (this.receiverPlaying || this.awaitingReceiver) {this.receiverPlaying = false;this.awaitingReceiver = false;this.reopen();}
    this.emit({type: 'state', observation: this.observe()});
  }

  /** Whether a start or a seek is still on its way to its position. */
  get settling(): boolean { return !!this.target; }

  /** The generation loaded, 0 when nothing is. */
  get loaded(): number { return this.source ? this.generation : 0; }

  /** Drop the presentation (the viewer left, the session was replaced). */
  unload(): void {
    if (this.disposed || !this.source) return;
    this.teardown();
    this.source = undefined;
    this.state = 'idle';
  }

  /**
   * The server's address is being verified again (the app moved between a server's routes):
   * media is let go and the position kept; `resume` loads the same presentation there again.
   */
  suspend(): void {
    if (!this.source || this.suspended) return;
    const at = this.target?.ms ?? this.positionMs();
    const playing = this.wantPlaying;
    this.teardown();
    this.wantPlaying = playing;
    this.settledMs = at;
    this.suspended = true;
    this.setState('loading');
  }

  resume(): void {
    if (!this.source || !this.suspended) return;
    this.suspended = false;
    this.state = 'loading';
    this.awaitingReceiver = wirelessPlayback(this.video);
    if (!this.awaitingReceiver) this.open(this.settledMs);
  }

  /** Housekeeping: position landing, byte progress, starvation. Runs four times a second. */
  tick(): void {
    if (this.disposed || !this.active() || this.suspended) return;
    this.pollBytes();
    this.attempt();
    this.finish();
    this.watch();
  }

  // ── Loading ─────────────────────────────────────────────────────────────

  private active(): boolean { return !this.disposed && !!this.source; }
  private resolve(url: string): string { return this.options.resolveUrl ? this.options.resolveUrl(url) : url; }
  private live(): number | undefined { return this.source?.liveOriginMs; }

  private open(startMs: number) {
    const source = this.source!;
    const serial = ++this.loadSerial;
    this.mediaRecoveryPause = undefined;
    this.started = false;
    // Opening a request is not evidence that the server is answering. Keep the
    // no-media deadline tied to actual progress across native/direct reloads.
    this.bytesAt = this.now();
    this.loadedBytes = 0;
    this.loading = undefined;
    this.waitingSince = 0;
    this.subtitleOffset = null;
    this.options.subtitles?.reset();
    const url = this.resolve(source.url);
    this.airPlay?.dispose();
    this.airPlay = airPlaySources(this.video, source.generation, () => !this.disposed && !this.suspended && this.source === source && serial === this.loadSerial);
    this.airPlay.setReceiver(this.receiver ? this.resolve(this.receiver.url) : undefined);
    if (source.mode === 'direct') {
      this.options.subtitles?.timeline(0);
      this.subtitleOffset = 0;
      this.target = startMs > 0 ? {ms: startMs, assigned: false, start: true} : undefined;
      this.video.src = url;
      this.airPlay.native(url);
      this.video.load();
      return;
    }
    const load = this.options.loadHls;
    if (!load || this.handedToNative || (this.options.nativeHls === 'first' && this.video.canPlayType('application/vnd.apple.mpegurl'))) { this.native(url, startMs); return; }
    void load().then(Hls => {
      if (this.disposed || serial !== this.loadSerial) return;
      if (!Hls.isSupported()) { this.native(url, startMs); return; }
      this.module = Hls;
      const origin = new URL(url).origin;
      const buffers = bufferTargets(source.bitrateKbps, this.options.deviceMemory);
      const live = this.live() !== undefined;
      const engine = new Hls({
        debug: false, enableWorker: true, lowLatencyMode: false,
        ...this.options.hlsConfig,
        // Loading begins where playback will: a resume never asks for the start of the title.
        // A live stream joins at its edge and is then moved to the server's join position.
        startPosition: live ? -1 : startMs / 1000,
        maxBufferLength: buffers.forward, maxMaxBufferLength: buffers.max, maxBufferSize: buffers.bytes, backBufferLength: buffers.back,
        // One presentation, one rendition. Quality changes only when the viewer or their
        // Automatic setting asks the server; the engine never switches by itself.
        capLevelToPlayerSize: false,
        manifestLoadPolicy: {default: {maxTimeToFirstByteMs: 20_000, maxLoadTimeMs: 60_000, timeoutRetry: {maxNumRetry: 3, retryDelayMs: 500, maxRetryDelayMs: 4000}, errorRetry: {maxNumRetry: 3, retryDelayMs: 1000, maxRetryDelayMs: 8000}}},
        playlistLoadPolicy: {default: {maxTimeToFirstByteMs: 20_000, maxLoadTimeMs: 60_000, timeoutRetry: {maxNumRetry: 3, retryDelayMs: 500, maxRetryDelayMs: 4000}, errorRetry: {maxNumRetry: 3, retryDelayMs: 1000, maxRetryDelayMs: 8000}}},
        // A fragment that is still receiving bytes is never timed out here: the total is a far
        // ceiling, and `watch` restarts a download only when it has received nothing for a while.
        fragLoadPolicy: {default: {maxTimeToFirstByteMs: 30_000, maxLoadTimeMs: 1_800_000, timeoutRetry: {maxNumRetry: 4, retryDelayMs: 500, maxRetryDelayMs: 4000}, errorRetry: {maxNumRetry: 6, retryDelayMs: 1000, maxRetryDelayMs: 8000}}},
        // Capability URLs never leave their origin and never carry cookies.
        xhrSetup: (xhr: {withCredentials: boolean}, requestUrl: string) => {
          if (new URL(requestUrl).origin !== origin) throw new Error('Unexpected stream origin');
          xhr.withCredentials = false;
        },
      });
      this.hls = engine;
      // The element does the positioning for a live join; hls.js does it for everything else.
      this.target = live ? {ms: startMs, assigned: false, start: true} : startMs > 0 ? {ms: startMs, assigned: true, start: true} : undefined;
      // Cues stay hidden until hls.js says where the stream sits on the element's clock.
      this.options.subtitles?.timeline(null);
      engine.subtitleDisplay = false;
      const mine = () => !this.disposed && this.hls === engine;
      const on = (name: string | undefined, fn: (data: never) => void) => { if (name) engine.on(name, (_e, data) => { if (mine()) fn(data); }); };
      on(Hls.Events.MEDIA_ATTACHING, () => this.airPlay?.attach());
      on(Hls.Events.MEDIA_DETACHING, () => this.airPlay?.detach());
      on(Hls.Events.SUBTITLE_TRACKS_UPDATED, () => { engine.subtitleTrack = -1; });
      on(Hls.Events.MANIFEST_PARSED, () => this.pinLevel(engine));
      on(Hls.Events.FRAG_LOADING, (data: {frag?: HlsFragmentLike}) => { this.loading = data.frag; this.loadingAt = this.now(); this.loadedBytes = data.frag?.stats?.loaded ?? 0; });
      on(Hls.Events.FRAG_LOADED, (data: {frag?: HlsFragmentLike}) => this.fragmentLoaded(data.frag));
      on(Hls.Events.FRAG_CHANGED, (data: {frag?: HlsFragmentLike}) => this.fragmentChanged(data.frag));
      on(Hls.Events.ERROR, (data: HlsErrorData) => this.hlsError(engine, Hls, data));
      engine.attachMedia(this.video);
      engine.loadSource(url);
    }, () => { if (!this.disposed && serial === this.loadSerial) this.fail('engine_unavailable', 'hls.js could not load'); });
  }

  /** Safari and iOS play HLS natively where hls.js cannot run. */
  private native(url: string, startMs: number) {
    if (!this.video.canPlayType('application/vnd.apple.mpegurl')) { this.fail('unsupported', 'no HLS support'); return; }
    this.options.subtitles?.timeline(0);
    this.subtitleOffset = 0;
    this.target = startMs > 0 || this.live() !== undefined ? {ms: startMs, assigned: false, start: true} : undefined;
    this.video.src = url;
    this.airPlay?.native(url);
    this.video.load();
  }

  /** A manifest with several renditions is played at its best one; nothing switches by itself. */
  private pinLevel(engine: HlsLike) {
    const levels = engine.levels;
    if (!levels || levels.length < 2) return;
    let best = 0;
    for (let i = 1; i < levels.length; i++) if ((levels[i]!.bitrate ?? 0) > (levels[best]!.bitrate ?? 0)) best = i;
    try { engine.loadLevel = best; } catch { /* the engine keeps its own choice */ }
  }

  private fragmentLoaded(frag?: HlsFragmentLike) {
    if (this.loading === frag) this.loading = undefined;
    this.alive(true);
    const stats = frag?.stats, timing = stats?.loading;
    if (!stats || !timing) return;
    const ms = timing.end - timing.first;
    // Small fragments finish inside one read and measure nothing.
    if (stats.loaded >= 64 * 1024 && ms >= 20) this.notify({type: 'throughput', kbps: (stats.loaded * 8) / ms});
  }

  /** hls.js names the active fragment's place on the element's clock. Cues move; the clock does not. */
  private fragmentChanged(frag?: HlsFragmentLike) {
    const pts = frag?.startPTS, start = frag?.start;
    if (typeof pts !== 'number' || !Number.isFinite(pts) || typeof start !== 'number' || !Number.isFinite(start)) return;
    const offset = pts - start;
    // Copied streams cut on source keyframes differ by a hair between fragments; rebuilding every
    // cue for that would flicker the line on screen.
    if (this.subtitleOffset !== null && Math.abs(offset - this.subtitleOffset) < 0.02) return;
    this.subtitleOffset = offset;
    this.options.subtitles?.timeline(offset);
  }

  // ── Position ────────────────────────────────────────────────────────────

  /** The element's position in title time (a live stream: seconds since the session's origin). */
  private observed(): number | null {
    const origin = this.live();
    if (origin === undefined) return this.video.currentTime;
    let date: number | null = null;
    if (this.hls) date = this.hls.playingDate?.getTime() ?? null;
    else {
      const start = this.video.getStartDate?.().getTime();
      if (start !== undefined && Number.isFinite(start)) date = start + this.video.currentTime * 1000;
    }
    return channelSourceTime(origin, date);
  }

  /** Title time as a time on the element's clock; null while the stream cannot say. */
  private mediaTime(seconds: number): number | null {
    const origin = this.live();
    if (origin === undefined) return seconds;
    if (this.hls) {
      const levels = this.hls.levels ?? [];
      const level = levels[this.hls.currentLevel ?? -1] ?? levels[this.hls.loadLevel ?? -1] ?? levels[0];
      return channelMediaTime(origin, seconds, level?.details?.fragments ?? []);
    }
    const start = this.video.getStartDate?.().getTime();
    return start !== undefined && Number.isFinite(start) ? (origin + seconds * 1000 - start) / 1000 : null;
  }

  private positionMs(): number {
    if (this.target || !this.started || this.suspended) return this.settledMs;
    const o = this.observed();
    return o !== null && Number.isFinite(o) ? o * 1000 : this.settledMs;
  }

  private attempt() {
    const t = this.target, v = this.video;
    if (!t || t.assigned || !this.active() || this.suspended || v.readyState < 1) return;
    const media = this.mediaTime(t.ms / 1000);
    if (media === null || !Number.isFinite(media)) return;
    const observed = this.observed();
    if (!v.seeking && v.readyState >= 2 && observed !== null && Math.abs(observed - t.ms / 1000) <= 0.05) { t.assigned = true; this.finish(); return; }
    let available = this.source?.mode === 'direct';
    for (let i = 0; i < v.seekable.length && !available; i++) if (media >= v.seekable.start(i) && media <= v.seekable.end(i)) available = true;
    if (!available) return;
    t.assigned = true;
    try { v.currentTime = media; } catch { this.seekFailed(t); }
  }

  private finish() {
    const t = this.target, v = this.video;
    if (!t || !t.assigned || !this.active() || v.seeking || v.readyState < 2) return;
    const observed = this.observed();
    if (observed === null) return;
    if (Math.abs(observed - t.ms / 1000) > LANDING_SECONDS) {
      // hls.js put a start somewhere else (the stream begins later, a keyframe snapped it): the
      // element is asked itself once the position is in reach.
      if (t.start && !this.video.seeking) { t.assigned = false; t.start = false; this.attempt(); }
      return;
    }
    this.target = undefined;
    this.started = true;
    this.settledMs = observed * 1000;
    this.emit({type: 'seeked', observation: this.observe()});
    this.reconcile();
  }

  private onSeeked() {
    const t = this.target;
    if (!t || !t.assigned || !this.active() || this.video.seeking) return;
    const observed = this.observed();
    if (observed !== null && Math.abs(observed - t.ms / 1000) > LANDING_SECONDS && !t.start) { this.seekFailed(t); return; }
    this.finish();
  }

  private seekFailed(t: Target) {
    if (this.target !== t) return;
    this.target = undefined;
    this.notify({type: 'seek-failed', positionMs: t.ms});
  }

  private onTime() {
    if (!this.active() || this.suspended) return;
    if (!this.target && !this.video.seeking && this.video.readyState >= 1) {
      const o = this.observed();
      if (o !== null && Number.isFinite(o)) {
        if (Math.abs(o * 1000 - this.settledMs) > 1000 && this.now() - this.recoveredAtMs > 5000) this.mediaRecoveries = 0;
        this.settledMs = o * 1000;
        this.started = true;
      }
    }
    this.finish();
    this.emit({type: 'time', observation: this.observe()});
  }

  // ── Transport state ─────────────────────────────────────────────────────

  private completed(): boolean {
    const v = this.video;
    return v.ended || (v.paused && Number.isFinite(v.duration) && v.duration > 0 && v.currentTime >= v.duration);
  }

  private onReady() {
    this.mediaRecoveryPause = undefined;
    if (!this.active() || this.suspended) return;
    this.attempt();
    this.finish();
    if (this.target) return;
    if (this.state !== 'loading') {
      // Media came back after the engine rebuilt its buffers (a recovered media error): the
      // element is left paused by that, and nothing else would start it again. A pause the viewer
      // made from outside is not undone here: it already changed what this port means.
      if (this.wantPlaying && this.video.paused && this.state !== 'error' && this.state !== 'ended') this.reconcile();
      return;
    }
    this.started = true;
    this.reconcile();
  }

  /** Make the element do what this port means it to, once it can. */
  private reconcile() {
    if (!this.active() || this.suspended || this.target || this.state === 'error' || this.video.readyState < 2) return;
    if (this.completed()) return;
    if (this.wantPlaying) { if (this.video.paused) this.tryPlay(); else this.setState('playing'); }
    else { if (!this.video.paused) this.video.pause(); this.setState('paused'); }
  }

  private tryPlay() {
    const serial = this.loadSerial;
    this.video.playbackRate = this.rate;
    void Promise.resolve(this.video.play()).catch(() => {
      // An interrupted play (a new load, a pause) is not a refusal.
      if (this.disposed || serial !== this.loadSerial || !this.wantPlaying || !this.video.paused) return;
      this.wantPlaying = false;
      this.setState('paused');
      this.notify({type: 'autoplay-blocked'});
    });
  }

  private onPlaying() {
    if (!this.active() || this.suspended || this.video.paused || this.video.readyState < 2) return;
    this.waitingSince = 0;
    this.setState('playing');
    if (this.recovery && this.bufferedAhead() > 2) this.recovered();
  }

  private onPlay() {
    // The element started without this port asking: a media key, the system's controls.
    if (!this.active() || this.suspended || this.wantPlaying || this.state !== 'paused') return;
    this.wantPlaying = true;
    this.notify({type: 'unsolicited', state: 'playing'});
  }

  private onPause() {
    const internal = this.mediaRecoveryPause;
    this.mediaRecoveryPause = undefined;
    if (internal && internal.engine === this.hls && internal.serial === this.loadSerial) return;
    if (!this.active() || this.suspended) return;
    // Browsers publish a final pause just before `ended`; that is completion, not a pause.
    if (this.completed() || this.video.error) return;
    const was = this.state;
    if (was === 'loading' || was === 'error' || was === 'ended' || was === 'idle') return;
    this.setState('paused');
    // Paused while this port meant it to play, with nothing of its own under way: the viewer did
    // it from somewhere this page does not see (a media key, Picture in Picture, a headset).
    if (this.wantPlaying && !this.target && (was === 'playing' || was === 'buffering')) {
      this.wantPlaying = false;
      this.notify({type: 'unsolicited', state: 'paused'});
    }
  }

  private onWaiting() {
    if (!this.active() || this.suspended) return;
    if (this.state === 'playing') this.setState('buffering');
    if (!this.wantPlaying || this.video.seeking || this.target || !this.started) return;
    const at = this.now();
    this.waitingSince ||= at;
    // One starvation is one stall however many `waiting` events it fires.
    if (at - this.stallAt > 2000) { this.stallAt = at; this.notify({type: 'stall'}); }
    if (this.recovery && this.bufferedAhead() < 0.5) this.starve(true);
  }

  private setState(state: PlayerState) {
    if (this.disposed || this.state === state) return;
    this.state = state;
    this.emit({type: 'state', observation: this.observe()});
  }

  // ── Slow, starved, failed ───────────────────────────────────────────────

  private bufferedAhead(): number {
    const v = this.video;
    if (v.error) return 0;
    const t = v.currentTime;
    for (let i = 0; i < v.buffered.length; i++) if (v.buffered.start(i) <= t + 0.25 && v.buffered.end(i) > t) return v.buffered.end(i) - t;
    return 0;
  }

  /** The server is answering: bytes arrived (`bytes`), or it said it is still preparing. */
  private alive(bytes: boolean) {
    const at = this.now();
    this.aliveAt = at;
    if (bytes) this.bytesAt = at;
    if (at - this.aliveToldAt >= 1000) { this.aliveToldAt = at; this.notify({type: 'alive'}); }
  }

  private pollBytes() {
    const loaded = this.loading?.stats?.loaded;
    if (loaded === undefined || loaded === this.loadedBytes) return;
    this.loadedBytes = loaded;
    this.alive(true);
  }

  private watch() {
    const at = this.now(), v = this.video;
    const wanting = this.wantPlaying || !!this.target || this.state === 'loading';
    if (!wanting || this.state === 'error' || this.state === 'ended' || this.completed()) return;
    const empty = v.readyState < 3 && this.bufferedAhead() < 0.5;
    if (!empty) { this.waitingSince = 0; return; }
    if (this.recovery) return;
    // A download in hand that has received nothing for a while is a dead transfer, not a slow one.
    if (this.hls && this.loading && at - Math.max(this.bytesAt, this.loadingAt) > this.silentMs) {
      const engine = this.hls;
      this.loading = undefined;
      this.recover(() => { if (this.hls === engine) engine.startLoad(this.resumeSeconds()); }, 'network_error');
      return;
    }
    // Nothing in hand and nothing arriving while the picture waits: ask again.
    if (!this.loading && this.started && this.waitingSince && at - this.waitingSince > 10_000 && at - this.aliveAt > 10_000) {
      const engine = this.hls;
      this.waitingSince = at;
      this.recover(() => { if (engine) { if (this.hls === engine) engine.startLoad(this.resumeSeconds()); } else this.reopen(); }, 'network_error');
    }
  }

  private resumeSeconds(): number {
    const seconds = (this.target?.ms ?? this.settledMs) / 1000;
    return this.mediaTime(seconds) ?? this.video.currentTime;
  }

  /**
   * A home server sleeps, reboots and sits behind bad Wi-Fi. Media that stops arriving is asked
   * for again, quietly, for as long as there is buffered media to play; the viewer is told only
   * when the buffer is empty, and what they are told is "Reconnecting", not an error. A retry
   * never interrupts a download that is receiving bytes.
   */
  private recover(retry: () => void, code: string) {
    if (this.recovery) { this.recovery.retry = retry; this.recovery.code = code; return; }
    this.recovery = {attempts: 0, emptySince: 0, retry, code, from: this.settledMs};
    this.recovery.timer = this.setTimer(() => this.recoveryTick(), 1000);
  }

  private recoveryTick() {
    const r = this.recovery;
    if (!r) return;
    r.timer = undefined;
    if (!this.active() || this.suspended || this.state === 'error') { this.recovery = undefined; return; }
    const at = this.now(), ahead = this.bufferedAhead();
    if (ahead < 0.5) {
      r.emptySince ||= at;
      this.starve(true);
      // Starved, and nothing has been heard from the server since: only then is it over.
      if (at - Math.max(r.emptySince, this.aliveAt) > this.giveUpMs) { this.recovery = undefined; this.fail(r.code, 'no media arrived'); return; }
    } else {
      r.emptySince = 0;
      // Media is there again. A paused player cannot prove it by advancing, and the end of a
      // title never has two seconds ahead of it: a refilled buffer or a moving picture is proof.
      const remaining = Number.isFinite(this.video.duration) ? this.video.duration - this.video.currentTime : Infinity;
      if (r.attempts > 0 && (ahead > 2 || ahead >= remaining - 0.25 || Math.abs(this.settledMs - r.from) > 1000)) { this.recovered(); return; }
    }
    // A download that is receiving bytes, or was only just asked for, is left alone: asking again
    // would throw away what has arrived.
    const receiving = this.loading
      ? at - Math.max(this.bytesAt, this.loadingAt) < this.silentMs
      : !this.hls && at - this.bytesAt < this.silentMs;
    if (!receiving) r.retry();
    r.attempts++;
    r.timer = this.setTimer(() => this.recoveryTick(), Math.min(1000 * 2 ** r.attempts, 10_000));
  }

  private recovered() {
    const r = this.recovery;
    if (!r) return;
    if (r.timer !== undefined) this.clearTimer(r.timer);
    this.recovery = undefined;
    this.starve(false);
  }

  private starve(active: boolean) {
    if (this.starved === active) return;
    this.starved = active;
    this.notify({type: 'starved', active});
  }

  private hlsError(engine: HlsLike, Hls: HlsModule, data: HlsErrorData) {
    if (engine !== this.hls) return;
    const status = data.response?.code;
    let preparing = false;
    try {
      const body = typeof data.response?.data === 'string' ? JSON.parse(data.response.data) : data.response?.data;
      preparing = status === 503 && (body as {error?: {code?: unknown}} | undefined)?.error?.code === 'segment_preparing';
    } catch { /* not a server envelope */ }
    // "Still preparing" is the server working, not the server failing.
    if (preparing) this.alive(false);
    if (!data.fatal) return;
    if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
      // The server answering "no" is a fact; not answering is weather.
      if (status !== undefined && REFUSED.includes(status)) { this.fail('stream_gone', `HTTP ${status}`); return; }
      this.loading = undefined;
      // A manifest that never arrived is asked for again whole; a fragment is asked for from where playback is.
      const manifest = /^manifest/i.test(String(data.details ?? ''));
      this.recover(() => { if (this.hls !== engine) return; if (manifest) this.reopen(); else engine.startLoad(this.resumeSeconds()); }, 'network_error');
      return;
    }
    // Codecs MediaSource will not take, or a decoding error two recoveries did not cure: the
    // browser's own HLS player may still play the stream, and is tried before anything is reported.
    if (data.type === Hls.ErrorTypes.MEDIA_ERROR && (this.mediaRecoveries >= 2 || /incompatiblecodecs/i.test(String(data.details ?? ''))) && this.handToNative()) return;
    if (data.type === Hls.ErrorTypes.MEDIA_ERROR && this.mediaRecoveries < 2) {
      // The second attempt also swaps the audio codec hint, which rescues a stream whose AAC
      // profile the browser's demuxer guessed wrong.
      if (this.mediaRecoveries++ === 1) engine.swapAudioCodec();
      this.recoveredAtMs = this.now();
      this.mediaRecoveryPause = !this.video.paused ? {engine, serial: this.loadSerial} : undefined;
      try {
        engine.recoverMediaError();
        // A detached/transferred engine that did not reset this element cannot own its next pause.
        if (!this.video.paused) this.mediaRecoveryPause = undefined;
      }
      catch (error) {this.mediaRecoveryPause = undefined;throw error;}
      return;
    }
    this.fail(data.type === Hls.ErrorTypes.MEDIA_ERROR ? 'decode_error' : 'engine_error', String(data.details ?? '').slice(0, 120));
  }

  /** Loads the same stream in the browser's own HLS player at the same position, once per load, where the host allows it. */
  private handToNative(): boolean {
    const policy = this.options.nativeHls ?? 'never';
    if (policy === 'never' || this.handedToNative || !this.active() || this.suspended || !this.video.canPlayType('application/vnd.apple.mpegurl')) return false;
    this.handedToNative = true;
    this.loading = undefined;
    this.reopen();
    return true;
  }

  private nativeError() {
    if (!this.active() || this.suspended || this.hls) return;
    if (wirelessPlayback(this.video) && !this.receiver) {this.awaitingReceiver = true;this.setState('loading');return;}
    const code = this.video.error?.code;
    // A browser reports an unreachable server as a network error, or as "source not supported"
    // when the reload itself gets no answer. Media that has already played did not become
    // unsupported, so both mean "try again", quietly, from where playback was.
    if (code === 2 || (code === 4 && (this.started || this.recovery))) { this.recover(() => this.reopen(), 'network_error'); return; }
    this.fail(code === 3 ? 'decode_error' : 'source_not_supported', String(this.video.error?.message ?? '').slice(0, 120));
  }

  /** Load the same presentation again where playback was (a native element after a network error). */
  private reopen() {
    if (!this.active() || this.suspended) return;
    const at = this.target?.ms ?? this.settledMs;
    this.airPlay?.detach();
    this.hls?.destroy();
    this.hls = undefined;
    this.airPlay?.dispose();
    this.video.removeAttribute('src');
    this.video.load();
    this.setState('loading');
    if (wirelessPlayback(this.video)) {
      if (this.receiver) this.openReceiver();
      else {this.awaitingReceiver = true;this.started = false;this.target = at > 0 ? {ms: at, assigned: false, start: true} : undefined;}
    } else this.open(at);
  }

  private fail(code: string, detail?: string) {
    this.mediaRecoveryPause = undefined;
    this.airPlay?.detach();
    this.hls?.destroy();
    this.hls = undefined;
    this.airPlay?.dispose();
    this.loading = undefined;
    if (this.recovery?.timer !== undefined) this.clearTimer(this.recovery.timer);
    this.recovery = undefined;
    this.starve(false);
    this.target = undefined;
    this.state = 'error';
    this.emit({type: 'error', observation: this.observe(), code, ...(detail ? {detail} : {})});
  }

  private teardown() {
    this.mediaRecoveryPause = undefined;
    this.loadSerial++;
    this.receiver = undefined;this.receiverPlaying = false;this.awaitingReceiver = false;
    this.airPlay?.detach();
    this.hls?.destroy();
    this.hls = undefined;
    this.airPlay?.dispose();
    this.loading = undefined;
    this.target = undefined;
    if (this.recovery?.timer !== undefined) this.clearTimer(this.recovery.timer);
    this.recovery = undefined;
    this.starve(false);
    this.wantPlaying = false;
    this.options.subtitles?.reset();
    this.video.pause();
    this.video.removeAttribute('src');
    this.video.load();
  }

  private emit(event: PlayerEvent) {
    if (this.disposed) return;
    for (const l of [...this.listeners]) l(event);
  }

  private notify(notice: WebPlayerNotice) {
    if (this.disposed) return;
    for (const l of [...this.noticeListeners]) l(notice);
  }
}
