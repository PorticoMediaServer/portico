import test from 'node:test';
import assert from 'node:assert/strict';
import {WebMediaPlayer, bufferTargets, type WebPlayerNotice} from '../src/platform/playback/web-media-player.ts';
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
const source = (over: Partial<Parameters<WebMediaPlayer['load']>[0]> = {}) => ({url: '/v1/media/grant/1/master.m3u8', generation: 1, mode: 'stream' as const, startPositionMs: 90_000, subtitles: [], autoplay: true, ...over});

function setup(options: {supported?: boolean; subtitles?: {reset(): void; timeline(o: number | null): void}; silentMs?: number; giveUpMs?: number; hls?: boolean} = {}) {
  const video = new FakeVideo();
  const clock = manualClock();
  const {Hls, instances, Events} = fakeHls(options.supported ?? true);
  const player = new WebMediaPlayer(video, {...(options.hls === false ? {} : {loadHls: async () => Hls}), resolveUrl, timers: clock, subtitles: options.subtitles, silentMs: options.silentMs, giveUpMs: options.giveUpMs});
  const events: PlayerEvent[] = [], notices: WebPlayerNotice[] = [];
  player.onEvent(e => events.push(e));
  player.onNotice(n => notices.push(n));
  return {video, clock, Hls, instances, Events, player, events, notices};
}
const types = (list: WebPlayerNotice[]) => list.map(n => n.type);

test('a resume loads from the resume point, never from the start of the title', async () => {
  const {video, instances, player, events} = setup();
  player.load(source());
  await settle();
  const hls = instances[0]!;
  assert.equal(hls.source, 'https://portico.test/v1/media/grant/1/master.m3u8');
  assert.equal(hls.config.startPosition, 90, 'hls.js is told where playback begins');
  assert.equal(video.seeks.length, 0, 'no seek after loading the beginning');
  // Until the picture is there, the reading is the position asked for, not the element's zero.
  assert.equal(player.observe().state, 'loading');
  assert.equal(player.observe().positionMs, 90_000);
  video.ready(90);
  assert.equal(player.settling, false);
  assert.equal(events.filter(e => e.type === 'seeked').length, 1, 'the landing is announced once');
  assert.equal(player.observe().state, 'playing');
  assert.equal(player.observe().positionMs, 90_000);
  player.dispose();
  assert.equal(video.listenerCount(), 0);
});

test('capability URLs keep their origin and carry no cookies; the engine never switches quality by itself', async () => {
  const {instances, player} = setup();
  player.load(source({startPositionMs: 0}));
  await settle();
  const config = instances[0]!.config;
  const xhr = {withCredentials: true};
  (config.xhrSetup as (x: typeof xhr, u: string) => void)(xhr, 'https://portico.test/v1/media/grant/1/seg1.ts');
  assert.equal(xhr.withCredentials, false);
  assert.throws(() => (config.xhrSetup as (x: typeof xhr, u: string) => void)(xhr, 'https://elsewhere.test/x.ts'));
  assert.equal(config.capLevelToPlayerSize, false);
  // A manifest with two renditions is pinned to its best; no automatic switching.
  instances[0]!.engine.levels = [{bitrate: 2_000_000}, {bitrate: 8_000_000}];
  instances[0]!.emit('hlsManifestParsed');
  assert.equal(instances[0]!.engine.loadLevel, 1);
  player.dispose();
});

test('a fragment has no wall-clock limit: one that keeps receiving bytes for minutes is never restarted or failed', async () => {
  const {video, clock, instances, player, events, notices} = setup();
  player.load(source({startPositionMs: 0}));
  await settle();
  const hls = instances[0]!;
  const policy = (hls.config.fragLoadPolicy as {default: {maxLoadTimeMs: number}}).default;
  assert.ok(policy.maxLoadTimeMs >= 600_000, 'the total is a far ceiling, not a deadline');
  video.ready(0, 4);
  video.starve();
  const frag = hls.fragment(6);
  // Five minutes of a slow link: a trickle of bytes every tick.
  for (let i = 0; i < 300; i++) { frag.stats.loaded += 20_000; clock.advance(1000); player.tick(); }
  assert.deepEqual(hls.startLoads, [], 'the download in hand was never thrown away');
  assert.equal(events.some(e => e.type === 'error'), false);
  assert.ok(types(notices).includes('alive'), 'the start or seek in hand is reported alive');
  player.dispose();
});

test('a download that receives nothing is restarted; with no sign of life for long enough, playback is given up', async () => {
  const {video, clock, instances, player, events, notices} = setup({silentMs: 20_000, giveUpMs: 90_000});
  player.load(source({startPositionMs: 0}));
  await settle();
  const hls = instances[0]!;
  video.ready(0, 4);
  video.starve();
  hls.fragment(6);
  for (let i = 0; i < 19; i++) { clock.advance(1000); player.tick(); }
  assert.deepEqual(hls.startLoads, [], 'nineteen silent seconds are still patience');
  for (let i = 0; i < 4; i++) { clock.advance(1000); player.tick(); }
  assert.equal(hls.startLoads.length, 1, 'a dead transfer is asked for again');
  assert.ok(notices.some(n => n.type === 'starved' && n.active), 'the viewer is told once the buffer is empty');
  assert.equal(events.some(e => e.type === 'error'), false);
  for (let i = 0; i < 100; i++) { clock.advance(1000); player.tick(); }
  const error = events.find(e => e.type === 'error');
  assert.ok(error && error.type === 'error' && error.code === 'network_error');
  player.dispose();
});

for (const mode of ['direct', 'stream'] as const) {
  test(`${mode} through the native element gives up after reloads without real progress`, () => {
    const {video, clock, player, events} = setup({hls: false, silentMs: 5000, giveUpMs: 30_000});
    video.nativeHls = 'maybe';
    player.load(source({mode, startPositionMs: 0}));
    video.ready(0, 4);
    video.starve();
    video.fail(2);
    const before = video.loads;
    for (let i = 0; i < 50; i++) {clock.advance(1000);player.tick();}
    assert.ok(video.loads > before, 'recovery tried the request again');
    assert.equal(player.observe().state, 'error');
    assert.equal(events.filter(event => event.type === 'error' && event.code === 'network_error').length, 1);
    const failedLoads = video.loads;
    clock.advance(60_000);
    assert.equal(video.loads, failedLoads, 'terminal failure stops automatic reloads');
    player.dispose();
  });

  test(`${mode} through the native element preserves a slow request while progress arrives`, () => {
    const {video, clock, player, events} = setup({hls: false, silentMs: 5000, giveUpMs: 30_000});
    video.nativeHls = 'maybe';
    player.load(source({mode, startPositionMs: 0}));
    video.ready(0, 4);
    video.starve();
    video.fail(2);
    // Let recovery open a new request, then simulate a slow browser download.
    clock.advance(7000);
    const loads = video.loads;
    for (let i = 0; i < 90; i++) {video.fire('progress');clock.advance(1000);player.tick();}
    assert.equal(video.loads, loads, 'arriving bytes protect the request for longer than the give-up interval');
    assert.equal(events.some(event => event.type === 'error'), false);
    // Once that request goes silent, retries and the terminal deadline apply again.
    for (let i = 0; i < 50; i++) {clock.advance(1000);player.tick();}
    assert.ok(video.loads > loads);
    assert.equal(player.observe().state, 'error');
    player.dispose();
  });
}

test('recovery never interrupts a download that is receiving bytes, and "still preparing" keeps a slow server alive', async () => {
  const {video, clock, instances, player, events} = setup({giveUpMs: 30_000});
  player.load(source({startPositionMs: 0}));
  await settle();
  const hls = instances[0]!;
  video.ready(0, 4);
  video.starve();
  hls.error({fatal: true, type: 'networkError', details: 'fragLoadError', response: {code: 502}});
  clock.advance(1000);
  assert.equal(hls.startLoads.length, 1, 'a failed load is asked for again');
  const frag = hls.fragment(6);
  for (let i = 0; i < 60; i++) { frag.stats.loaded += 10_000; clock.advance(1000); player.tick(); }
  assert.equal(hls.startLoads.length, 1, 'while bytes arrive nothing is restarted');
  assert.equal(events.some(e => e.type === 'error'), false, 'slow is not failed, however long it takes');
  // The server says it is still preparing: that is the server working.
  hls.loaded(frag);
  for (let i = 0; i < 60; i++) { hls.error({fatal: false, type: 'networkError', response: {code: 503, data: JSON.stringify({error: {code: 'segment_preparing'}})}}); clock.advance(1000); player.tick(); }
  assert.equal(events.some(e => e.type === 'error'), false);
  // Media arrives: recovery ends.
  video.ready(0, 12);
  video.fire('playing');
  clock.advance(12_000);
  assert.equal(clock.pending(), 0, 'nothing keeps retrying once media flows');
  player.dispose();
});

test('errors: a refused stream fails at once; a media error recovers twice, then is a decode error', async () => {
  const a = setup();
  a.player.load(source());
  await settle();
  a.instances[0]!.error({fatal: true, type: 'networkError', response: {code: 410}});
  const gone = a.events.find(e => e.type === 'error');
  assert.ok(gone && gone.type === 'error' && gone.code === 'stream_gone');
  assert.equal(a.instances[0]!.destroyed, true);
  a.player.dispose();

  const b = setup();
  b.player.load(source());
  await settle();
  const hls = b.instances[0]!;
  hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  assert.equal(hls.recoveries, 2);
  assert.equal(hls.swaps, 1, 'the second attempt swaps the audio codec hint');
  hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  const decode = b.events.find(e => e.type === 'error');
  assert.ok(decode && decode.type === 'error' && decode.code === 'decode_error');
  hls.error({fatal: false, type: 'mediaError'});
  b.player.dispose();

  // A recovered media error leaves the element paused; playback picks up again by itself.
  const c = setup();
  c.player.load(source({startPositionMs: 0}));
  await settle();
  c.video.ready(0);
  assert.equal(c.video.paused, false);
  c.instances[0]!.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  c.video.paused = true; // the rebuild resets the element without a pause event
  c.video.readyState = 4;
  c.video.fire('canplay');
  assert.equal(c.video.paused, false, 'it plays on');
  assert.equal(c.notices.some(n => n.type === 'unsolicited'), false);
  c.player.dispose();
});

test('a pause from outside the page is the viewer’s; the port’s own pause and the end of a title are not', async () => {
  const {video, player, notices} = setup();
  player.load(source({startPositionMs: 0}));
  await settle();
  video.ready(0);
  assert.equal(player.observe().state, 'playing');
  // A media key, the Picture in Picture window, a headset: the element pauses by itself.
  video.pause();
  assert.deepEqual(notices.filter(n => n.type === 'unsolicited'), [{type: 'unsolicited', state: 'paused'}]);
  assert.equal(player.observe().state, 'paused');
  // …and starts again from outside.
  void video.play();
  assert.deepEqual(notices.filter(n => n.type === 'unsolicited').at(-1), {type: 'unsolicited', state: 'playing'});
  // The port's own pause is not news.
  const before = notices.length;
  player.pause();
  assert.equal(notices.length, before);
  player.play();
  await settle();
  // The browser's final pause just before `ended` is completion.
  video.finish();
  assert.equal(notices.filter(n => n.type === 'unsolicited').length, 2);
  assert.equal(player.observe().state, 'ended');
  player.dispose();
});

test('a refused autoplay is reported, not retried', async () => {
  const {video, player, notices} = setup();
  video.refusePlay = true;
  player.load(source({startPositionMs: 0}));
  await settle();
  video.ready(0);
  await settle();
  assert.ok(types(notices).includes('autoplay-blocked'));
  assert.equal(player.observe().state, 'paused');
  player.dispose();
});

test('direct play: the source is the file, the start position is set once metadata is there', async () => {
  const {video, instances, player, events} = setup();
  player.load(source({mode: 'direct', url: '/v1/media/grant/1/file.mp4', startPositionMs: 30_000}));
  await settle();
  assert.equal(instances.length, 0);
  assert.equal(video.src, 'https://portico.test/v1/media/grant/1/file.mp4');
  video.readyState = 1;
  video.duration = 600;
  video.fire('loadedmetadata');
  assert.deepEqual(video.seeks, [30]);
  video.land(30);
  video.fire('canplay');
  assert.equal(events.filter(e => e.type === 'seeked').length, 1);
  assert.equal(player.observe().positionMs, 30_000);
  // A network error after it has played is asked for again, quietly, from where it was.
  video.advance(45);
  video.fail(2);
  assert.equal(events.some(e => e.type === 'error'), false);
  // A file the browser cannot decode is the engine refusing this route.
  const fresh = setup();
  fresh.player.load(source({mode: 'direct', url: '/v1/media/grant/2/file.mkv', startPositionMs: 0}));
  fresh.video.fail(4);
  const refused = fresh.events.find(e => e.type === 'error');
  assert.ok(refused && refused.type === 'error' && refused.code === 'source_not_supported');
  player.dispose();
  fresh.player.dispose();
});

test('a seek is acknowledged where it lands; one the element cannot reach is reported', async () => {
  const {video, player, events, notices} = setup();
  player.load(source({startPositionMs: 0}));
  await settle();
  video.ready(0);
  player.seek(600_000);
  assert.deepEqual(video.seeks, [600]);
  assert.equal(player.settling, true);
  assert.equal(player.observe().positionMs, 0, 'the position is not reported before the picture is there');
  video.land(600);
  assert.equal(player.settling, false);
  assert.equal(events.filter(e => e.type === 'seeked').at(-1)!.observation.positionMs, 600_000);
  player.seek(900_000);
  video.land(30); // the element went somewhere else
  assert.deepEqual(notices.find(n => n.type === 'seek-failed'), {type: 'seek-failed', positionMs: 900_000});
  player.dispose();
});

test('no hls.js support: native HLS where the browser has it, else unsupported', async () => {
  const a = setup({supported: false});
  a.video.nativeHls = 'maybe';
  a.player.load(source());
  await settle();
  assert.equal(a.video.src, 'https://portico.test/v1/media/grant/1/master.m3u8');
  a.player.dispose();
  const b = setup({supported: false});
  b.player.load(source());
  await settle();
  const e = b.events.find(x => x.type === 'error');
  assert.ok(e && e.type === 'error' && e.code === 'unsupported');
  b.player.dispose();
});

test('the route to the server changing lets go of the media and comes back at the same position', async () => {
  const {video, instances, player} = setup();
  player.load(source({startPositionMs: 0}));
  await settle();
  video.ready(0);
  video.advance(321);
  player.suspend();
  assert.equal(instances[0]!.destroyed, true);
  assert.equal(player.observe().positionMs, 321_000, 'the position is kept, not the element’s zero');
  player.resume();
  await settle();
  assert.equal(instances[1]!.config.startPosition, 321);
  player.dispose();
});

test('cues are placed by the stream’s offset, and a hair’s difference between fragments does not move them', async () => {
  const offsets: (number | null)[] = [];
  let resets = 0;
  const {instances, player} = setup({subtitles: {reset: () => { resets++; }, timeline: o => { offsets.push(o); }}});
  player.load(source({startPositionMs: 0}));
  await settle();
  assert.deepEqual(offsets, [null], 'nothing is shown until the stream says where it sits');
  const hls = instances[0]!;
  hls.emit('hlsFragChanged', {frag: {start: 0, startPTS: 1.4}});
  hls.emit('hlsFragChanged', {frag: {start: 6, startPTS: 7.4004}});
  hls.emit('hlsFragChanged', {frag: {start: 12, startPTS: 13.409}});
  assert.deepEqual(offsets, [null, 1.4], 'sub-frame wobble is ignored');
  hls.emit('hlsFragChanged', {frag: {start: 18, startPTS: 20}});
  assert.equal(offsets.length, 3, 'a real change is followed');
  assert.ok(resets >= 1);
  player.dispose();
});

test('a live channel is positioned by the stream’s programme dates', async () => {
  const origin = Date.UTC(2026, 0, 1, 20, 0, 0);
  const {video, instances, player} = setup();
  player.load(source({startPositionMs: 600_000, liveOriginMs: origin}));
  await settle();
  const hls = instances[0]!;
  assert.equal(hls.config.startPosition, -1, 'a live stream joins at its edge');
  hls.engine.levels = [{details: {fragments: [{start: 100, duration: 6, programDateTime: origin + 598_000} as never, {start: 106, duration: 6, programDateTime: origin + 604_000} as never]}}];
  video.readyState = 4;
  video.seekableRanges = [[0, 400]];
  player.tick();
  assert.deepEqual(video.seeks, [102], 'ten minutes after the origin is two seconds into the first fragment');
  hls.engine.playingDate = new Date(origin + 600_000);
  video.land(102);
  assert.equal(player.observe().positionMs, 600_000);
  player.dispose();
});

test('a late event from the presentation before cannot speak for the one now loading', async () => {
  const {video, player, events} = setup();
  player.load(source({generation: 1, startPositionMs: 0}));
  await settle();
  video.ready(0);
  player.load(source({generation: 2, startPositionMs: 0}));
  const before = events.length;
  video.fire('ended');
  video.fire('playing');
  video.fire('pause');
  assert.equal(events.slice(before).some(e => e.type === 'state' && e.observation.state !== 'loading'), false);
  assert.equal(player.observe().generation, 2);
  player.dispose();
});

test('buffers are sized by bit rate: a high-rate stream holds fewer seconds than a page can afford', () => {
  assert.equal(bufferTargets(8000).forward, 30);
  assert.ok(bufferTargets(80_000).forward <= 12, 'an 80 Mbps remux is not given thirty seconds');
  assert.ok(bufferTargets(80_000).forward >= 6);
  assert.ok(bufferTargets(80_000).back <= bufferTargets(8000).back);
  assert.equal(bufferTargets(undefined).forward, 30);
  assert.ok(bufferTargets(8000, 2).forward <= 20, 'a small device buffers less');
});


test('hls.js media reset pause is internal once per recovery, including the codec swap retry', async () => {
  const f = setup();f.player.load(source({startPositionMs: 0}));await settle();f.video.ready(0);
  try {
    const hls = f.instances[0]!;
    // Installed buffer-controller detaches src then calls load(). Model the queued
    // pause from that reset, rather than the old fake's silent paused assignment.
    hls.engine.recoverMediaError = () => {
      hls.recoveries++;const playing = !f.video.paused;f.video.load();
      if (playing) queueMicrotask(() => f.video.fire('pause'));
    };
    for (let recovery = 1; recovery <= 2; recovery++) {
      hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
      await settle();
      assert.equal(f.notices.some(notice => notice.type === 'unsolicited'), false);
      assert.notEqual(f.player.observe().state, 'paused');
      f.video.ready(0);assert.equal(f.video.paused, false);
      assert.equal(hls.recoveries, recovery);
    }
    assert.equal(hls.swaps, 1);
    f.video.pause();
    assert.deepEqual(f.notices.filter(notice => notice.type === 'unsolicited'), [{type: 'unsolicited', state: 'paused'}]);
    assert.equal(f.player.observe().state, 'paused');
  } finally {f.player.dispose();}
});

test('a recovery without a reset pause cannot mask a later user pause or a replacement source', async () => {
  for (const replace of [false, true]) {
    const f = setup();f.player.load(source({startPositionMs: 0}));await settle();f.video.ready(0);
    try {
      f.instances[0]!.error({fatal: true, type: 'mediaError'});
      if (replace) {f.player.load(source({generation: 2, startPositionMs: 0}));await settle();}
      if (replace) f.video.ready(0);
      f.video.pause();
      assert.equal(f.player.observe().state, 'paused');
      assert.equal(f.notices.filter(notice => notice.type === 'unsolicited' && notice.state === 'paused').length, 1);
    } finally {f.player.dispose();}
  }
});

test('recovering already paused media never reserves a future user pause', async () => {
  const f = setup();f.player.load(source({startPositionMs: 0}));await settle();f.video.ready(0);f.player.pause();
  try {
    f.instances[0]!.error({fatal: true, type: 'mediaError'});
    f.player.play();f.video.pause();
    assert.equal(f.player.observe().state, 'paused');
    assert.equal(f.notices.filter(notice => notice.type === 'unsolicited' && notice.state === 'paused').length, 1);
  } finally {f.player.dispose();}
});
