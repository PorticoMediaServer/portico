import type {HlsErrorData, HlsFragmentLike, HlsLike, HlsModule, VideoLike} from '../src/platform/playback/web-media-player.ts';

export class FakeVideo implements VideoLike {
  src = ''; playbackRate = 1; volume = 1; muted = false;
  paused = true; ended = false; seeking = false; readyState = 0; duration = NaN;
  error: {code: number; message?: string} | null = null;
  bufferedRanges: [number, number][] = [];
  seekableRanges: [number, number][] = [];
  loads = 0;
  plays = 0;
  /** `play()` is refused, as a browser does without a gesture. */
  refusePlay = false;
  /** Every position the element was asked to move to. */
  seeks: number[] = [];
  private time = 0;
  private handlers = new Map<string, Set<() => void>>();

  get currentTime() { return this.time; }
  set currentTime(value: number) { this.time = value; this.seeking = true; this.seeks.push(value); }
  get buffered() { const r = this.bufferedRanges; return {length: r.length, start: (i: number) => r[i]![0], end: (i: number) => r[i]![1]}; }
  get seekable() { const r = this.seekableRanges; return {length: r.length, start: (i: number) => r[i]![0], end: (i: number) => r[i]![1]}; }

  play() {
    this.plays++;
    if (this.refusePlay) return Promise.reject(Object.assign(new Error('refused'), {name: 'NotAllowedError'}));
    if (this.paused) { this.paused = false; this.fire('play'); if (this.readyState >= 3) this.fire('playing'); }
    return Promise.resolve();
  }
  pause() { if (this.paused) return; this.paused = true; this.fire('pause'); }
  load() { this.loads++; this.readyState = 0; this.time = 0; this.ended = false; this.seeking = false; this.paused = true; this.error = null; this.bufferedRanges = []; this.seekableRanges = []; }
  removeAttribute(name: string) { if (name === 'src') this.src = ''; }
  canPlayType(type: string) { return type === 'application/vnd.apple.mpegurl' ? this.nativeHls : ''; }
  nativeHls = '';
  addEventListener(type: string, fn: () => void) { let s = this.handlers.get(type); if (!s) this.handlers.set(type, s = new Set()); s.add(fn); }
  removeEventListener(type: string, fn: () => void) { this.handlers.get(type)?.delete(fn); }
  getVideoPlaybackQuality() { return {droppedVideoFrames: 3}; }
  fire(type: string) { for (const fn of [...(this.handlers.get(type) ?? [])]) fn(); }
  listenerCount() { let n = 0; for (const s of this.handlers.values()) n += s.size; return n; }

  // ── What a browser would do ──────────────────────────────────────────
  /** Media is there around `at` (seconds): metadata, a buffered range, a playable frame. */
  ready(at = this.time, ahead = 12, duration = 7200) {
    this.duration = duration;
    this.seekableRanges = [[0, duration]];
    this.bufferedRanges = [[Math.max(0, at - 1), Math.min(duration, at + ahead)]];
    this.time = at;
    this.seeking = false;
    this.readyState = 4;
    this.fire('loadedmetadata');
    this.fire('canplay');
  }
  /** The seek in hand finishes where it was asked to go (or at `at`). */
  land(at = this.time, ahead = 12) {
    this.time = at;
    this.seeking = false;
    this.readyState = 4;
    this.bufferedRanges = [[Math.max(0, at - 1), at + ahead]];
    this.fire('seeked');
  }
  /** Playback moves on to `at`. */
  advance(at: number, ahead = 12) {
    this.time = at;
    this.bufferedRanges = [[Math.max(0, at - 1), at + ahead]];
    this.fire('timeupdate');
  }
  /** The buffer runs dry. */
  starve() { this.bufferedRanges = []; this.readyState = 2; this.fire('waiting'); }
  /** The title plays out: browsers publish a final pause, then `ended`. */
  finish() { this.time = this.duration; this.paused = true; this.fire('pause'); this.ended = true; this.fire('ended'); }
  fail(code: number, message = '') { this.error = {code, message}; this.fire('error'); }
}

export type FakeHlsInstance = {
  config: Record<string, unknown>; source?: string; destroyed: boolean; startLoads: number[]; recoveries: number; swaps: number;
  handlers: Map<string, (event: string, data: never) => void>; engine: HlsLike & {loadLevel?: number; levels: {bitrate?: number; details?: {fragments: never[]}}[]; playingDate: Date | null};
  emit(event: string, data?: unknown): void;
  error(data: HlsErrorData): void;
  /** A fragment starts downloading; returns it so the test can add bytes. */
  fragment(start?: number): Required<HlsFragmentLike>;
  loaded(frag: HlsFragmentLike, ms?: number): void;
};

export function fakeHls(supported = true) {
  const instances: FakeHlsInstance[] = [];
  const Events = {ERROR: 'hlsError', FRAG_LOADING: 'hlsFragLoading', FRAG_LOADED: 'hlsFragLoaded', FRAG_CHANGED: 'hlsFragChanged', MANIFEST_PARSED: 'hlsManifestParsed', SUBTITLE_TRACKS_UPDATED: 'hlsSubtitleTracksUpdated'};
  const Hls = class {
    static isSupported() { return supported; }
    static Events = Events;
    static ErrorTypes = {NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError'};
    bandwidthEstimate = 5_000_000;
    playingDate: Date | null = null;
    levels: {bitrate?: number; details?: {fragments: never[]}}[] = [];
    currentLevel = 0;
    loadLevel = 0;
    subtitleDisplay = true;
    subtitleTrack = 0;
    i: FakeHlsInstance;
    constructor(config: Record<string, unknown>) {
      const handlers = new Map<string, (event: string, data: never) => void>();
      this.i = {
        config, destroyed: false, startLoads: [], recoveries: 0, swaps: 0, handlers, engine: this as never,
        emit: (event, data) => handlers.get(event)?.(event, data as never),
        error: data => handlers.get(Events.ERROR)?.(Events.ERROR, data as never),
        fragment: (start = 0) => { const frag = {start, startPTS: start, stats: {loaded: 0, loading: {first: 0, end: 0}}}; handlers.get(Events.FRAG_LOADING)?.(Events.FRAG_LOADING, {frag} as never); return frag; },
        loaded: (frag, ms = 1000) => { if (frag.stats?.loading) { frag.stats.loading.first = 0; frag.stats.loading.end = ms; } handlers.get(Events.FRAG_LOADED)?.(Events.FRAG_LOADED, {frag} as never); },
      };
      instances.push(this.i);
    }
    attachMedia() {}
    loadSource(url: string) { this.i.source = url; }
    startLoad(position = -1) { this.i.startLoads.push(position); }
    recoverMediaError() { this.i.recoveries++; }
    swapAudioCodec() { this.i.swaps++; }
    destroy() { this.i.destroyed = true; }
    on(event: string, fn: (event: string, data: never) => void) { this.i.handlers.set(event, fn); }
  } as unknown as HlsModule;
  return {Hls, instances, Events};
}

/** A clock and timers a test moves by hand. */
export function manualClock(start = 1_000_000) {
  let now = start, serial = 0;
  const timers = new Map<number, {fn: () => void; at: number}>();
  return {
    now: () => now,
    setTimer: (fn: () => void, ms: number) => { timers.set(++serial, {fn, at: now + ms}); return serial; },
    clearTimer: (t: unknown) => { timers.delete(t as number); },
    setTicker: () => 0,
    clearTicker: () => {},
    /** Move time on, running every timer that comes due, in order. */
    advance(ms: number) {
      const until = now + ms;
      for (;;) {
        const due = [...timers.entries()].filter(([, t]) => t.at <= until).sort((a, b) => a[1].at - b[1].at)[0];
        if (!due) break;
        timers.delete(due[0]);
        now = Math.max(now, due[1].at);
        due[1].fn();
      }
      now = until;
    },
    /** The machine sleeps: time passes and no timer runs until it wakes (`advance`). */
    sleep(ms: number) { now += ms; },
    pending: () => timers.size,
  };
}
