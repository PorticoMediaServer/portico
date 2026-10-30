/**
 * The web's one playback path, driven the way a browser drives it: the real playback service and
 * queue player, the real session controller and media element port, a fake server, a fake
 * `<video>` and a fake hls.js. These are the behaviours the older adapter got wrong or had no
 * test for.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '@core/index.ts';
import {FakePlaybackServer, fakeHttp} from '@core/playback-v1/testing/fake-server.ts';
import {V1QueuePlayer} from '@core/playback-v1/v1-queue-player.ts';
import {queueItemInput} from '@core/queue-controller.ts';
import {offeredQualities} from '@core/playback-v1/choices.ts';
import type {V1Http} from '@core/playback-v1/http.ts';
import {WebPlayback, PlayerSlot, failure, type PlaybackMediaPort, type WebPlaybackHost, type WebPlaybackNotice} from '../src/platform/playback/web-playback.ts';
import {playbackFailure} from '@core/presentation/playback-failure.ts';
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


const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
type Logged = {method: string; path: string; body?: unknown};

function setup(ctx: {after(fn: () => void): void}, options: {escalates?: boolean; mount?: boolean; hold?: boolean} & Pick<WebPlaybackHost, 'createRender' | 'subscribeRoute' | 'getRouteSnapshot'> = {}) {
  const clock = manualClock();
  const server = new FakePlaybackServer({now: clock.now});
  const inner = fakeHttp(server);
  const log: Logged[] = [], farewells: Logged[] = [];
  const http: V1Http = {send: async r => {
    log.push({method: r.method, path: r.path, body: r.body});
    // A server with no other way to deliver the file refuses the failure report's re-plan.
    if (r.method === 'PATCH' && (r.body as {failure?: unknown} | undefined)?.failure && options.escalates === false) return {status: 422, headers: {}, body: {error: {code: 'unsupported_media', message: 'This title can\u2019t be played on this device.', retry: 'never'}}};
    return inner.send(r as never) as never;
  }};
  let serial = 0;
  const {Hls, instances} = fakeHls();
  const web = new WebPlayback({
    http, mediaUrl: p => new URL(p, 'https://portico.test').href, loadHls: async () => Hls, key: () => `key-${++serial}-0000000000`,
    timers: {now: clock.now, setTimer: clock.setTimer, clearTimer: clock.clearTimer}, playerTimers: clock,
    farewell: (path, method, body) => { farewells.push({path, method, body}); },
    createRender: options.createRender, subscribeRoute: options.subscribeRoute, getRouteSnapshot: options.getRouteSnapshot,
  });
  const playback = new PlaybackService(web.transport, () => `r${++serial}`, {queueRequired: true, startupTimeoutMs: 60_000, seekTimeoutMs: 60_000});
  const player = new V1QueuePlayer({http, api: web.transport, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10});
  if (options.hold !== false) player.setCompletionPolicy(() => 'hold');
  const unbind = web.bind(playback, player);
  const stop = player.connect();
  const video = new FakeVideo();
  const unmount = options.mount === false ? () => {} : web.mount(video);
  const notices: WebPlaybackNotice[] = [];
  web.onNotice(n => notices.push(n));
  // Whatever a test asserts, its timers and listeners go with it.
  ctx.after(() => { playback.leave(); stop(); unmount(); unbind(); web.dispose(); });
  /** The element has the media the newest engine asked for, at the position it was asked for. */
  const arrive = async () => {
    await settle();
    const hls = instances.at(-1)!;
    const at = Number(hls.config.startPosition);
    video.ready(at >= 0 ? at : 0);
    await settle();
    return hls;
  };
  const sessions = () => log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions');
  const queues = () => log.filter(r => r.method === 'POST' && r.path === '/v1/queues');
  return {clock, server, log, farewells, web, playback, player, video, instances, notices, arrive, sessions, queues};
}

test('a native paint surface belongs only to the loaded current source and disappears on hold or terminal observation', () => {
  let loaded = 0, generation = 0, state: import('@core/playback-v1/port.ts').PlayerState = 'playing';
  const slot = new PlayerSlot();
  const real: PlaybackMediaPort = {
    get loaded() { return loaded; }, settling: false, nativeSurfaceActive: true,
    load() {}, play() {}, pause() {}, seek() {}, setRate() {}, selectSidecarSubtitle() {},
    observe: () => ({generation, state, positionMs: 0, rate: 1}),
    onEvent: () => () => {}, onNotice: () => () => {}, unload() {}, suspend() {}, resume() {}, dispose() {},
  };
  slot.mount(real);
  const source = {url: '/first', generation: 1, mode: 'direct', startPositionMs: 0, subtitles: [], autoplay: true} as const;
  slot.load(source); assert.equal(slot.nativeSurfaceActive, false);
  loaded = generation = 1; assert.equal(slot.nativeSurfaceActive, true);
  slot.load({...source, url: '/second', generation: 2}); assert.equal(slot.nativeSurfaceActive, false, 'an old native frame cannot open the new source paint hole');
  loaded = 2; assert.equal(slot.nativeSurfaceActive, false, 'loaded and observed generation must both match');
  generation = 2; assert.equal(slot.nativeSurfaceActive, true);
  for (state of ['ended', 'error', 'idle']) assert.equal(slot.nativeSurfaceActive, false);
  state = 'playing'; slot.hold(true); assert.equal(slot.nativeSurfaceActive, false);
  slot.hold(false); assert.equal(slot.nativeSurfaceActive, true);
  slot.unload(); assert.equal(slot.nativeSurfaceActive, false); slot.dispose();
});

test('processing facts require the current loaded unheld port and actual observed generation', () => {
  const slot = new PlayerSlot(); let loaded = 0, generation = 0;
  let state: import('@core/playback-v1/port.ts').PlayerState = 'loading';
  let processing: PlaybackMediaPort['audioProcessing'];
  const real: PlaybackMediaPort = {get loaded() {return loaded;}, settling: false, get audioProcessing() {return processing;},
    load() {processing = undefined;loaded = 0;}, play() {}, pause() {}, seek() {}, setRate() {}, selectSidecarSubtitle() {},
    observe: () => ({generation, state, positionMs: 0, rate: 1}), onEvent: () => () => {}, onNotice: () => () => {}, unload() {}, suspend() {}, resume() {}, dispose() {}};
  const source = {url: '/current', generation: 1, mode: 'direct' as const, startPositionMs: 0, subtitles: [], autoplay: false};
  slot.mount(real);slot.load(source);assert.equal(slot.audioProcessing, undefined);
  processing = {generation: 1, eqApplied: true, levelingApplied: false};loaded = 1;
  assert.equal(slot.audioProcessing, undefined);generation = 1;
  assert.equal(slot.audioProcessing, undefined);state = 'paused';
  assert.deepEqual(slot.audioProcessing, processing);
  for (state of ['loading', 'idle', 'error', 'ended']) assert.equal(slot.audioProcessing, undefined);
  state = 'playing';slot.hold(true);assert.equal(slot.audioProcessing, undefined);slot.hold(false);
  processing = {generation: 2, eqApplied: true, levelingApplied: true};assert.equal(slot.audioProcessing, undefined);
  processing = {generation: 1, eqApplied: false, levelingApplied: true};assert.deepEqual(slot.audioProcessing, processing);
  slot.load({...source, url: '/other-session-same-generation'});assert.equal(slot.audioProcessing, undefined, 'adapter retires facts for its old private load, even when public generation repeats');
  slot.dispose();assert.equal(slot.audioProcessing, undefined);
});

test('frame commands require an unheld settled paused port with matching loaded and observed generations', async () => {
  let loaded=0,generation=0,settling=false,state:import('@core/playback-v1/port.ts').PlayerState='paused',calls=0;
  const slot=new PlayerSlot();
  const real:PlaybackMediaPort={
    get loaded(){return loaded},get settling(){return settling},
    load(){},play(){},pause(){},seek(){},setRate(){},selectSidecarSubtitle(){},
    stepFrame(direction,expected){assert.equal(direction,1);assert.equal(expected,2);calls++},
    observe:()=>({generation,state,positionMs:123,rate:1}),onEvent:()=>()=>{},onNotice:()=>()=>{},unload(){},suspend(){},resume(){},dispose(){},
  };
  slot.mount(real);slot.load({url:'/source',generation:2,mode:'direct',startPositionMs:123,subtitles:[],autoplay:false});
  assert.equal(slot.canStepFrame,false);loaded=2;assert.equal(slot.canStepFrame,false);generation=2;assert.equal(slot.canStepFrame,true);
  settling=true;assert.equal(slot.canStepFrame,false);await assert.rejects(slot.stepFrame(1,2),/playback_frame_step_unavailable/);settling=false;
  for(state of ['loading','playing','buffering','ended','error','idle'])assert.equal(slot.canStepFrame,false);
  state='paused';slot.hold(true);assert.equal(slot.canStepFrame,false);slot.hold(false);assert.equal(slot.canStepFrame,true);
  await assert.rejects(slot.stepFrame(1,1),/playback_frame_step_unavailable/);assert.equal(calls,0);
  await slot.stepFrame(1,2);assert.equal(calls,1);assert.equal(slot.observe().positionMs,123,'a command receipt cannot invent a resulting frame clock');
  real.stepFrame=()=>{slot.load({url:'/different-session-same-generation',generation:2,mode:'direct',startPositionMs:0,subtitles:[],autoplay:false});};
  await assert.rejects(slot.stepFrame(1,2),/stale_playback_command/,'generation reuse cannot validate a replaced source');
  slot.unload();assert.equal(slot.canStepFrame,false);slot.dispose();
});

test('a network interruption discards the actual prepared edge silently and retains the same queue session', async ctx => {
  let route = 'ready', changed = () => {}, disposed = 0;
  const t = setup(ctx, {
    getRouteSnapshot: () => ({phase: route}), subscribeRoute: listener => { changed = listener; return () => {}; },
    createRender: service => ({
      apply(snapshot) {
        service.ready(snapshot.intentId);
        if (snapshot.pendingSeek) service.seekApplied(snapshot.intentId, snapshot.pendingSeek.revision, snapshot.pendingSeek.positionSeconds, 'paused');
      }, transportPosition: () => 12, dispose() { disposed++; },
    }),
  });
  t.playback.setAudioEffects({gapless: true, crossfadeSeconds: 0, normalization: 'off'});
  await t.player.playItem('song_network'); await settle();
  const before = t.playback.getSnapshot(), audio = before.session?.audio;
  assert.ok(audio && audio.mode !== 'unavailable');
  t.playback.prepareAudio({token: 'prepared-network', expiresAtMs: Date.now() + 60000, audio});
  const args: unknown[][] = [], discard = t.playback.audioPreparationFailed.bind(t.playback);
  t.playback.audioPreparationFailed = (...values) => { args.push(values); discard(...values); };
  route = 'recovering'; changed(); await settle();
  assert.deepEqual(args, [[]], 'no transient presentation text crosses the discard port');
  assert.equal(t.playback.getSnapshot().audioEffects.prepared, undefined);
  assert.equal(Boolean(t.playback.getSnapshot().audioEffects.notice), false);
  assert.equal(t.playback.getSnapshot().session!.id, before.session!.id);
  assert.ok(disposed > 0);
});

test('WEB-03: turning the last effect off hands the same session from the renderer to the element under its own reporter', async ctx => {
  const t = setup(ctx, {createRender: service => ({
    apply(snapshot) {
      service.ready(snapshot.intentId);
      if (snapshot.pendingSeek) service.seekApplied(snapshot.intentId, snapshot.pendingSeek.revision, snapshot.pendingSeek.positionSeconds, 'playing');
    }, transportPosition: () => 30, dispose() {},
  })});
  // Music settings that need no processing; the device equalizer alone selects the renderer.
  t.playback.setAudioEffects({gapless: false, crossfadeSeconds: 0, normalization: 'off', neverBoost: false, smoothTransitions: false});
  t.playback.setDeviceAudioProcessing(true);
  await t.player.playItem('song_handoff'); await settle();
  assert.equal(t.playback.getSnapshot().audioEffects.rendering, true);
  const session = t.playback.getSnapshot().session!, starts = t.sessions().length, engines = t.instances.length;
  t.playback.seek(30); t.clock.advance(5_000); await settle();
  assert.equal(t.playback.getSnapshot().positionSeconds, 30);
  const server = t.server.session(session.id)!, before = server.lastSeq, sent = server.reports.length;
  assert.ok(before > 0, 'the renderer reported its timeline');
  t.playback.setDeviceAudioProcessing(false);
  const hls = await t.arrive();
  assert.equal(t.playback.getSnapshot().audioEffects.rendering, false);
  assert.ok(t.instances.length > engines, 'the element loads the same presentation');
  assert.equal(t.playback.getSnapshot().session!.id, session.id, 'the same session continues');
  assert.equal(t.sessions().length, starts, 'no new session is started for the handoff');
  assert.ok(Number(hls.config.startPosition) > 0, 'the element resumes where the renderer was, not from the start');
  t.video.advance(Number(hls.config.startPosition) + 4); t.clock.advance(5_000); await settle();
  const after = server.reports.slice(sent).map(report => report.seq as number);
  assert.ok(after.length > 0 && after.every(seq => seq > before), `reports after the handoff continue the same increasing seq (${before} then ${after.join(',')})`);
});

test('a start loads where the server says to resume, plays, and reports a full timeline', async ctx => {
  const t = setup(ctx);
  await t.player.playItem('movie', 100);
  const hls = await t.arrive();
  assert.equal(hls.config.startPosition, 100, 'loading begins at the start position');
  assert.equal(t.video.seeks.length, 0);
  const s = t.playback.getSnapshot();
  assert.equal(s.phase, 'ready');
  assert.equal(s.pendingSeek, undefined, 'the start is acknowledged once the picture is there');
  assert.equal(s.positionSeconds, 100);
  t.video.advance(104);
  await settle();
  t.clock.advance(1000); await settle();
  const reports = t.server.session(s.session!.id)!.reports;
  assert.ok(reports.length >= 1);
  const last = reports.at(-1)!;
  assert.equal(last.generation, s.session!.generation);
  assert.equal(typeof last.rate, 'number');
  assert.ok(typeof last.monotonicMs === 'number' && typeof last.playClockMs === 'number' && (last.playClockMs as number) <= (last.monotonicMs as number), 'each observation carries the device’s own clocks');
  assert.ok(reports.every(r => (r.positionMs as number) >= 100_000), 'no position before the resume point is ever reported');
  assert.equal(t.log.some(r => r.path.endsWith('/progress')), false, 'only the protocol’s observations are spoken');
});

test('a session adopted before the player screen exists loads when the element is mounted', async ctx => {
  const t = setup(ctx, {mount: false});
  await t.player.playItem('movie', 120);
  await settle();
  assert.equal(t.instances.length, 0);
  assert.equal(t.playback.getSnapshot().phase, 'starting');
  const unmount = t.web.mount(t.video);
  await t.arrive();
  assert.equal(t.instances.at(-1)!.config.startPosition, 120);
  assert.equal(t.playback.getSnapshot().phase, 'ready');
  unmount();
});

test('changing quality re-plans the playing session where it is and keeps the queue', async ctx => {
  const t = setup(ctx);
  await t.player.playEntries(['e1', 'e2', 'e3'].map(queueItemInput));
  await t.arrive();
  t.video.advance(132);
  await settle();
  const before = t.playback.getSnapshot(), queue = t.player.getSnapshot().view!.queue;
  await t.playback.changePlayback({quality: {mode: 'limit', maxHeight: 720, maxVideoBitrateKbps: 4000}});
  const patch = t.log.find(r => r.method === 'PATCH' && r.path === `/v1/playback/sessions/${before.session!.id}`);
  assert.deepEqual((patch!.body as {quality?: unknown}).quality, {mode: 'limit', maxHeight: 720, maxVideoBitrateKbps: 4000}, 'the choice reaches the server');
  assert.equal(t.queues().length, 1, 'no new queue was made');
  const hls = await t.arrive();
  const after = t.playback.getSnapshot();
  assert.equal(after.session!.id, before.session!.id, 'the same session');
  assert.equal(after.session!.generation, before.session!.generation + 1, 'a new presentation');
  assert.match(hls.source!, new RegExp(`/${after.session!.generation}/master\\.m3u8$`));
  assert.equal(hls.config.startPosition, 132, 'the new stream starts where the picture was');
  assert.equal(after.phase, 'ready');
  assert.equal(after.pendingSeek, undefined);
  const view = t.player.getSnapshot().view!;
  assert.equal(view.queue.id, queue.id);
  assert.equal(view.queue.entries.length, 3);
  assert.equal(view.next.available, true, 'Up next still has the next episode');
  assert.equal(t.web.session(after.session!.id)!.presentation.decision.video!.action, 'transcode');
});

test('an audio track choice re-plans the session too, and the menu ticks what was committed', async ctx => {
  const t = setup(ctx);
  await t.player.playEntries(['e1', 'e2'].map(queueItemInput));
  await t.arrive();
  const s = t.playback.getSnapshot();
  t.playback.installAudioTracks(s.intentId, [{streamIndex: 1, label: 'English', codec: 'ac3'}, {streamIndex: 2, label: 'French', codec: 'aac'}], 1);
  const picked = t.playback.selectAudioTrack(2);
  assert.equal(t.playback.getSnapshot().audioTracks!.pending, 2);
  await picked;
  const patch = t.log.find(r => r.method === 'PATCH');
  assert.deepEqual((patch!.body as {audio?: unknown}).audio, {trackId: 'a2'});
  assert.equal(t.queues().length, 1);
  assert.equal(t.playback.getSnapshot().audioTracks?.selected, 2);
  assert.equal(t.playback.getSnapshot().audioTracks?.pending, undefined);
});

test('Replay starts the same queue entry again; the rest of the queue stays', async ctx => {
  const t = setup(ctx);
  await t.player.playEntries(['e1', 'e2', 'e3'].map(queueItemInput));
  await t.arrive();
  const first = t.playback.getSnapshot().session!.id, queue = t.player.getSnapshot().view!.queue.id;
  t.video.finish();
  await settle();
  assert.equal(t.playback.getSnapshot().phase, 'ended');
  assert.ok(t.player.getSnapshot().completion, 'the end is held for the post-play card');
  t.playback.resume();
  await settle();
  const again = t.playback.getSnapshot();
  assert.notEqual(again.session!.id, first);
  assert.equal(again.itemId, 'e1');
  assert.equal(t.queues().length, 1, 'no one-title queue replaced the season');
  const start = t.sessions().at(-1)!.body as {queue?: {queueId: string}; startPositionMs?: number};
  assert.equal(start.queue!.queueId, queue);
  assert.equal(start.startPositionMs, 0);
  assert.equal(t.player.getSnapshot().view!.next.available, true);
});

test('the end of a title reaches the server before the queue moves on', async ctx => {
  const t = setup(ctx, {hold: false});
  await t.player.playEntries(['e1', 'e2'].map(queueItemInput));
  await t.arrive();
  const first = t.playback.getSnapshot().session!.id;
  t.video.finish();
  await settle();
  await settle();
  assert.equal(t.server.session(first)!.reports.at(-1)!.state, 'ended');
  const now = t.playback.getSnapshot();
  assert.equal(now.itemId, 'e2', 'the queue advanced by itself');
  assert.notEqual(now.session!.id, first);
  assert.equal(now.phase, 'starting');
  await t.arrive();
  assert.equal(t.playback.getSnapshot().phase, 'ready');
});

test('a pause from a media key or the Picture in Picture window stays paused', async ctx => {
  const t = setup(ctx);
  await t.player.playItem('movie', 0);
  await t.arrive();
  assert.equal(t.playback.getSnapshot().intent, 'playing');
  t.video.pause(); // not through Portico's own button
  assert.equal(t.playback.getSnapshot().intent, 'paused', 'the viewer’s pause is their intent');
  // Later ticks and snapshot updates do not start it again.
  t.video.fire('timeupdate');
  t.playback.listeningNotice('audio_autoplay_blocked');
  await settle();
  assert.equal(t.video.paused, true);
  // Portico's own Play resumes.
  t.playback.resume();
  await settle();
  assert.equal(t.video.paused, false);
  // A play from outside is intent as well.
  t.playback.pause();
  await settle();
  assert.equal(t.video.paused, true);
  void t.video.play();
  assert.equal(t.playback.getSnapshot().intent, 'playing');
});

test('a browser that refuses to start without a gesture leaves a paused player and says so', async ctx => {
  const t = setup(ctx);
  t.video.refusePlay = true;
  await t.player.playItem('movie', 0);
  await t.arrive();
  assert.deepEqual(t.notices, ['autoplayBlocked']);
  assert.equal(t.playback.getSnapshot().intent, 'paused');
  assert.equal(t.playback.getSnapshot().phase, 'ready');
});

test('a lease that lapsed while the laptop slept starts the title again where it was, with no error', async ctx => {
  const t = setup(ctx);
  await t.player.playEntries(['e1', 'e2'].map(queueItemInput));
  await t.arrive();
  t.video.advance(150);
  await settle();
  const asleep = t.playback.getSnapshot().session!.id;
  // Five minutes pass with nothing sent; the server ends the session for its lease.
  t.clock.sleep(300_000);
  t.clock.advance(1); // awake: the overdue report is sent, and finds the session gone
  await settle();
  await settle();
  const now = t.playback.getSnapshot();
  assert.notEqual(now.phase, 'error', 'no error card');
  assert.notEqual(now.session!.id, asleep, 'a new session');
  assert.equal(now.itemId, 'e1');
  const start = t.sessions().at(-1)!.body as {queue?: unknown; startPositionMs?: number};
  assert.ok(start.queue, 'the same queue entry');
  assert.equal(start.startPositionMs, 150_000, 'at the last position');
  assert.equal(t.queues().length, 1);
  const hls = await t.arrive();
  assert.equal(hls.config.startPosition, 150);
  assert.equal(t.playback.getSnapshot().phase, 'ready');
  assert.equal(t.playback.getSnapshot().recovering, undefined);
});

test('an owner’s stop is final and carries their message; nothing is restarted', async ctx => {
  const t = setup(ctx);
  await t.player.playItem('movie', 0);
  await t.arrive();
  const id = t.playback.getSnapshot().session!.id;
  t.web.transport.sessionUpdated(id, '99', {state: 'ended', reason: 'terminated', message: 'Maintenance tonight.'});
  await settle();
  const s = t.playback.getSnapshot();
  assert.equal(s.phase, 'error');
  assert.deepEqual(s.ended, {reason: 'terminated', message: 'Maintenance tonight.'});
  assert.equal(t.sessions().length, 1, 'no new session');
  // This device's own queue move is not news to it, even if the notice overtakes the answer.
  const u = setup(ctx);
  await u.player.playItem('movie', 0);
  await u.arrive();
  u.web.transport.sessionUpdated(u.playback.getSnapshot().session!.id, '99', {state: 'ended', reason: 'replaced'});
  await settle();
  assert.equal(u.playback.getSnapshot().phase, 'ready');
});

test('an engine failure is reported to the server and the title comes back on another route, in the same queue', async ctx => {
  const t = setup(ctx, {escalates: true});
  await t.player.playEntries(['e1', 'e2'].map(queueItemInput));
  const hls = await t.arrive();
  t.video.advance(60);
  await settle();
  const failed = t.playback.getSnapshot().session!.id;
  for (let i = 0; i < 3; i++) hls.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  await settle();
  const report = t.log.find(r => r.method === 'PATCH' && r.path === `/v1/playback/sessions/${failed}` && !!(r.body as {failure?: unknown}).failure);
  assert.ok(report, 'the server is told the player failed, on the session itself');
  assert.equal((report!.body as {failure: {code: string}}).failure.code, 'decode_error');
  assert.equal((report!.body as {positionMs?: number}).positionMs, 60_000, 'and where the picture was');
  const now = t.playback.getSnapshot();
  assert.notEqual(now.phase, 'error', 'the viewer sees recovery, not an error');
  assert.equal(now.session!.id, failed, 'the same session, delivered another way');
  assert.ok(now.session!.generation > 1);
  assert.equal(t.queues().length, 1, 'the queue was not replaced');
  assert.equal(t.sessions().length, 0, 'and nothing was started again: the queue\u2019s own session was re-planned');

  // With no route above it, the viewer is told plainly that it cannot be played here.
  const u = setup(ctx, {escalates: false});
  await u.player.playItem('movie', 0);
  const engine = await u.arrive();
  for (let i = 0; i < 3; i++) engine.error({fatal: true, type: 'mediaError', details: 'bufferAppendError'});
  await settle();
  const s = u.playback.getSnapshot();
  assert.equal(s.phase, 'error');
  assert.equal(s.error, failure.cannotPlay);
  const card = playbackFailure(s.error);
  assert.equal(card.permanent, true);
  assert.equal(card.title, 'web.player.error.cannotPlayTitle');
  assert.equal(card.body, 'web.player.error.cannotPlay');
});

test('a server that stops answering is a failure another try can fix, and is worded that way', async ctx => {
  const t = setup(ctx);
  await t.player.playItem('movie', 0);
  const hls = await t.arrive();
  hls.error({fatal: true, type: 'networkError', response: {code: 410}});
  await settle();
  const s = t.playback.getSnapshot();
  assert.equal(s.phase, 'error');
  assert.equal(s.error, failure.streamGone);
  const card = playbackFailure(s.error);
  assert.equal(card.permanent, false);
  assert.equal(card.title, 'web.player.error.stoppedTitle');
  assert.equal(playbackFailure('This title can’t be played on this device.').permanent, true);
  assert.equal(playbackFailure('The stream could not connect.').permanent, false);
});

test('two quick presses of Next move on twice instead of colliding', async ctx => {
  const t = setup(ctx);
  await t.player.playEntries(['e1', 'e2', 'e3', 'e4'].map(queueItemInput));
  await t.arrive();
  const q = t.player.getSnapshot().view!.queue;
  const a = t.player.next(q.id, q.revision), b = t.player.next(q.id, q.revision);
  await Promise.all([a, b]);
  await settle();
  const s = t.playback.getSnapshot();
  assert.equal(s.itemId, 'e3');
  assert.notEqual(s.phase, 'error');
  assert.equal(t.log.filter(r => r.path.endsWith(':advance')).length, 2);
  // Only the session that is playing is alive on the server.
  const live = (t.server as unknown as {sessions: Map<string, {ended: boolean; id: string}>}).sessions;
  assert.deepEqual([...live.values()].filter(x => !x.ended).map(x => x.id), [s.session!.id]);
});

test('closing the tab ends the session with its position; hiding it only reports', async ctx => {
  const t = setup(ctx);
  await t.player.playItem('movie', 0);
  await t.arrive();
  t.video.advance(77);
  await settle();
  const id = t.playback.getSnapshot().session!.id;
  t.web.pageHidden(false);
  assert.equal(t.farewells.length, 0, 'a hidden tab keeps its session');
  t.web.pageHidden(true);
  assert.deepEqual(t.farewells, [{path: `/v1/playback/sessions/${id}`, method: 'DELETE', body: {generation: 1, positionMs: 77_000}}]);
});

test('closing the tab on a live channel ends the channel\'s session too; hiding it does not', ctx => {
  const t = setup(ctx);
  // A channel's session is the channel control's, so the controller holds none: the playback service says what is tuned.
  (t.web as unknown as {service: {getSnapshot(): unknown}}).service = {getSnapshot: () => ({channel: {channelId: 'ch1'}, session: {id: 'live session/1', generation: 3}, positionSeconds: 0, audioEffects: {rendering: false}})};
  t.web.pageHidden(false);
  assert.equal(t.farewells.length, 0, 'a hidden tab keeps watching');
  t.web.pageHidden(true);
  assert.deepEqual(t.farewells, [{path: '/v1/playback/sessions/live%20session%2F1', method: 'DELETE', body: {generation: 3}}]);
});

test('leaving stops the session at the last position', async ctx => {
  const t = setup(ctx);
  await t.player.playItem('movie', 0);
  await t.arrive();
  t.video.advance(55);
  await settle();
  const id = t.playback.getSnapshot().session!.id;
  t.playback.leave();
  await settle();
  const stop = t.log.find(r => r.method === 'DELETE' && r.path === `/v1/playback/sessions/${id}`);
  assert.deepEqual(stop!.body, {generation: 1, positionMs: 55_000});
  assert.equal(t.instances.at(-1)!.destroyed, true, 'the media is let go');
});

test('Automatic steps down on a sustained shortfall, says so, and asks the server for the lower quality', async ctx => {
  const t = setup(ctx);
  await t.player.playEntries(['e1', 'e2'].map(queueItemInput));
  const hls = await t.arrive();
  t.web.setAutomatic({steps: offeredQualities({height: 1080, bitrateKbps: 20_000}), originalKbps: 20_000});
  assert.equal(t.web.automaticOn, true);
  assert.equal(t.web.automaticAt, undefined, 'it starts at Original');
  // Twelve seconds of fragments arriving at about 5 Mbps against a 20 Mbps original.
  for (let i = 0; i < 12; i++) {
    const frag = hls.fragment(i * 6);
    frag.stats.loaded = 625_000;
    hls.loaded(frag, 1000);
    t.clock.advance(1000);
    t.video.advance(1 + i);
  }
  await settle();
  assert.equal(t.web.automaticAt?.id, '720p-low', '4 Mbps usable includes audio: 4000+128 does not fit, 2000+128 does');
  assert.equal(t.web.automaticAt?.audioKbps, 128);
  assert.deepEqual(t.notices, ['qualityLowered']);
  const patch = t.log.find(r => r.method === 'PATCH');
  assert.deepEqual((patch!.body as {quality: unknown}).quality, {mode: 'automatic', id: '720p-low'}, 'Automatic, on the step the connection carries');
  assert.equal(t.queues().length, 1);
});

test('a presentation the service was handed by the subtitle route loads, and the session’s lease is kept alive', async ctx => {
  const t = setup(ctx);
  await t.player.playItem('movie', 0);
  await t.arrive();
  t.video.advance(40);
  await settle();
  const before = t.playback.getSnapshot().session!;
  const reports = () => t.server.session(before.id)!.reports.length;
  // A burned-in subtitle: the same session with a new stream, as the subtitle service installs it.
  assert.equal(t.playback.installSubtitlePresentation({sessionId: before.id, generation: before.generation + 1, streamUrl: '/v1/media/burned/master.m3u8', mode: 'hls', positionUs: '40000000'}), true);
  await settle();
  const hls = await t.arrive();
  assert.match(hls.source!, /\/v1\/media\/burned\/master\.m3u8$/);
  assert.equal(hls.config.startPosition, 40);
  assert.equal(t.playback.getSnapshot().phase, 'ready');
  assert.equal(t.playback.getSnapshot().pendingSeek, undefined);
  const sent = reports();
  t.video.advance(52);
  t.clock.advance(11_000);
  await settle();
  assert.ok(reports() > sent, 'the timeline keeps reporting');
  assert.equal(t.server.session(before.id)!.reports.at(-1)!.positionMs, 52_000);
  assert.equal(t.server.session(before.id)!.ended, false);
});

test('queued selection receipts cannot forget a replacement registration with the same session and generation', async ctx => {
  const t = setup(ctx, {mount: false});
  const {session} = await t.web.transport.start({itemId: 'movie'}, {startFrom: 'beginning', state: 'paused'});
  const first = t.web.transport.adopt(session, 7200);
  const old = t.web.transport.selectionRegistration?.(session.id, first);
  assert.ok(old?.current());
  assert.equal(t.web.transport.selectionRegistration?.(session.id), undefined, 'bare id cannot claim an existing registration');
  const second = t.web.transport.adopt(session, 7200);
  const current = t.web.transport.selectionRegistration?.(session.id, second);
  assert.ok(current?.current());
  assert.equal(old.current(), false, 'same generation re-registration retires the original receipt');
  assert.equal(old.forget(() => true), false);
  assert.equal(t.web.transport.session(session.id), session);
  assert.equal(current.forget(() => false), false, 'a refused ownership guard leaves the registration alone');
  assert.equal(current.forget(() => true), true);
  assert.equal(t.web.transport.session(session.id), undefined);
  assert.equal(current.current(), false);
  const absent = t.web.transport.selectionRegistration?.(session.id);
  assert.ok(absent?.current(), 'bare id can capture known absence');
  t.web.transport.adopt(session, 7200);
  assert.equal(absent.current(), false, 'a later registration retires known absence too');
  assert.equal(absent.forget(() => true), false);
});

test('queued selection receipt never detaches an installed controller or playback owner', async ctx => {
  const t = setup(ctx, {mount: false});
  await t.player.playItem('movie', 120);
  const installed = t.playback.getSnapshot().session;
  assert.ok(installed);
  const session = t.web.transport.session(installed.id);
  assert.ok(session);
  const adopted = t.web.transport.adopt(session, installed.duration);
  const receipt = t.web.transport.selectionRegistration?.(session.id, adopted);
  assert.ok(receipt);
  assert.equal(receipt.current(), false, 'installed occurrences have an owner');
  assert.equal(receipt.forget(() => true), false);
  assert.equal(t.playback.getSnapshot().session?.id, installed.id);
  assert.equal(t.web.transport.session(session.id), session);
});
