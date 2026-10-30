import test from 'node:test';
import assert from 'node:assert/strict';
import {WebMediaPlayer} from '../src/platform/playback/web-media-player.ts';
import {nativeHlsPolicy} from '../src/platform/playback/native-hls.ts';
import type {PlayerEvent} from '@core/playback-v1/index.ts';
/**
 * A media element and an hls.js that do only what a test tells them to, for the web media
 * player port and the playback path above it. Time is the test's own clock.
 */
import type {HlsErrorData, HlsFragmentLike, HlsLike, HlsModule, VideoLike} from '../src/platform/playback/web-media-player.ts';

export class FakeVideo implements VideoLike {
  src = ''; playbackRate = 1; volume = 1; muted = false;
  paused = true; ended = false; seeking = false; readyState = 0; duration = NaN;
  error: {code: number; message?: string} | null = null;
  bufferedRanges: [number, number][] = [];
  seekableRanges: [number, number][] = [];
  loads = 0;
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

export const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(setImmediate); };


const resolveUrl = (u: string) => new URL(u, 'https://portico.test').href;
const stream = 'https://portico.test/v1/media/grant/1/master.m3u8';
const source = (over: Partial<Parameters<WebMediaPlayer['load']>[0]> = {}) => ({url: '/v1/media/grant/1/master.m3u8', generation: 1, mode: 'stream' as const, startPositionMs: 90_000, subtitles: [], autoplay: true, ...over});

function setup(nativeHls: 'first' | 'fallback' | 'never' | undefined, plays = 'maybe') {
  const video = new FakeVideo();
  video.nativeHls = plays;
  const {Hls, instances} = fakeHls(true);
  let loads = 0;
  const offsets: (number | null | 'reset')[] = [];
  const player = new WebMediaPlayer(video, {loadHls: async () => { loads++; return Hls; }, resolveUrl, timers: manualClock(), nativeHls, subtitles: {reset: () => offsets.push('reset'), timeline: o => offsets.push(o)}});
  const events: PlayerEvent[] = [];
  player.onEvent(e => events.push(e));
  return {video, instances, player, events, offsets, loads: () => loads};
}
const errors = (events: PlayerEvent[]) => events.filter(e => e.type === 'error');

test('which browsers use their own HLS player, and when', () => {
  const cases: {name: string; userAgent: string; mediaSource: boolean; want: string}[] = [
    {name: 'Safari on iPhone (ManagedMediaSource only)', userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1', mediaSource: false, want: 'first'},
    {name: 'Chrome on iPhone is the same engine', userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/140.0.0.0 Mobile/15E148 Safari/604.1', mediaSource: false, want: 'first'},
    {name: 'Safari on a Mac', userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Safari/605.1.15', mediaSource: true, want: 'fallback'},
    {name: 'Safari on iPad', userAgent: 'Mozilla/5.0 (iPad; CPU OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1', mediaSource: true, want: 'fallback'},
    {name: 'Chrome on a Mac', userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36', mediaSource: true, want: 'never'},
    {name: 'Edge', userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0', mediaSource: true, want: 'never'},
    {name: 'Chrome on Android, which now plays HLS itself', userAgent: 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36', mediaSource: true, want: 'never'},
    {name: 'Firefox', userAgent: 'Mozilla/5.0 (X11; Linux x86_64; rv:143.0) Gecko/20100101 Firefox/143.0', mediaSource: true, want: 'never'},
  ];
  for (const c of cases) assert.equal(nativeHlsPolicy({userAgent: c.userAgent, mediaSource: c.mediaSource}), c.want, c.name);
});

test('iPhone: the system’s HLS player is used from the start and hls.js is never fetched', async () => {
  const {video, instances, player, offsets, loads} = setup('first');
  player.load(source());
  await settle();
  assert.equal(video.src, stream);
  assert.equal(loads(), 0);
  assert.equal(instances.length, 0);
  assert.equal(offsets.at(-1), 0, 'subtitle times are the element’s own');
  // A file played as it is has nothing to do with HLS either way.
  player.load(source({url: '/v1/media/grant/1', mode: 'direct', generation: 2}));
  await settle();
  assert.equal(video.src, 'https://portico.test/v1/media/grant/1');
  player.dispose();
});

test('Safari: hls.js first; a stream whose codecs MediaSource refuses goes to Safari’s player at the same position, unreported', async () => {
  const {video, instances, player, events} = setup('fallback');
  player.load(source());
  await settle();
  assert.equal(instances.length, 1, 'hls.js is the engine');
  assert.equal(video.src, '');
  instances[0]!.error({fatal: true, type: 'mediaError', details: 'manifestIncompatibleCodecsError'});
  await settle();
  assert.equal(instances[0]!.destroyed, true);
  assert.equal(instances[0]!.recoveries, 0, 'no pointless recovery attempts');
  assert.equal(video.src, stream);
  assert.equal(errors(events).length, 0, 'nothing failed as far as the server is concerned');
  assert.equal(player.observe().positionMs, 90_000);
  assert.equal(instances.length, 1, 'and hls.js is not tried again for this load');
  player.dispose();
});

test('Safari: a decoding error survives two recoveries, then Safari’s player; a new title starts with hls.js again', async () => {
  const {video, instances, player, events} = setup('fallback');
  player.load(source());
  await settle();
  const hls = instances[0]!;
  hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  assert.equal(hls.recoveries, 2);
  assert.equal(video.src, '');
  hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  await settle();
  assert.equal(video.src, stream);
  assert.equal(errors(events).length, 0);
  player.load(source({generation: 2}));
  await settle();
  assert.equal(instances.length, 2, 'the next load is hls.js first again');
  player.dispose();
});

test('everywhere else nothing changes: no native player, and the failure is reported as before', async () => {
  for (const policy of ['never', undefined] as const) {
    const {video, instances, player, events} = setup(policy);
    player.load(source());
    await settle();
    instances[0]!.error({fatal: true, type: 'mediaError', details: 'manifestIncompatibleCodecsError'});
    instances[0]!.error({fatal: true, type: 'mediaError', details: 'manifestIncompatibleCodecsError'});
    instances[0]!.error({fatal: true, type: 'mediaError', details: 'manifestIncompatibleCodecsError'});
    await settle();
    assert.equal(video.src, '');
    assert.equal(errors(events).length, 1);
    player.dispose();
  }
  // A browser the policy names that does not in fact play HLS: hls.js, and its failures are failures.
  const {video, instances, player, events} = setup('fallback', '');
  player.load(source());
  await settle();
  for (let i = 0; i < 3; i++) instances[0]!.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  await settle();
  assert.equal(video.src, '');
  assert.equal(errors(events).length, 1);
  player.dispose();
});
