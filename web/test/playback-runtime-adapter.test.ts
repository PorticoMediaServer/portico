import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {HttpLocalApi, isUserMutationKey} from '@core/index.ts';
import {routePath} from '../../packages/contracts/src/route-paths.ts';
import {FakePlaybackServer, fakeHttp} from '@core/playback-v1/testing/fake-server.ts';
import type {V1Request, V1Response} from '@core/playback-v1/http.ts';
import type {ServerEvent} from '@core/playback-v1/types.ts';
import {GroupSessionService} from '@core/group-session.ts';
import {SOCIAL_PROTOCOL} from '@core/social-playback.ts';
import {WebShellPlaybackRuntime} from '../src/platform/playback/runtime.ts';
import {ByteCache} from '../src/platform/playback/audio/bytes.ts';
import {PlayOnController, castSourcePosition, type WebCastPort} from '../src/platform/playback/play-on.ts';
import {playbackPolicyNext} from '../src/platform/playback/policy.ts';
import {dispatchWatchNext, watchUpNextOffer} from '../src/screens/watch/up-next.ts';
import {watchBodyTarget, watchBodyKey} from '../src/screens/watch/body-target.ts';
import {dispatchWatchFeedback, watchFeedbackTarget} from '../src/screens/watch/feedback.ts';
import {playbackVersionRows} from '../src/platform/playback/version-rows.ts';
import {WebCastSender, type CastBrowser, type CastSession} from '../src/platform/cast/index.ts';
import type {WebPlaybackSettings, WebPlaybackSource} from '../src/platform/playback/types.ts';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {HomeSelectionPlaybackItem} from '@core/home-selection/types.ts';
import type {HomeChannel} from '@core/home.ts';
import type {PlayerOffersService} from '@core/player-offers.ts';
import {transformTsx} from './transform-tsx.mjs';
import {playbackPresentation} from '../src/shell/playback-model.ts';
import {dockView} from '../src/kit/dock/model.ts';
import {ControlledLocalMedia, localPreparation, localScope} from './local-media-fixture.ts';
import type {DockAction} from '../src/kit/dock/types.ts';
import {createWebPreparingStarts} from '../src/platform/playback/start-owner.ts';

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

export const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(setImmediate); };


const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
const homeViewerFence = 'home_viewer_fence', detailViewerFence = 'detail_viewer_fence';
const selectionContext: SelectionContext = {scope: {...viewer, viewId: 'home', privacyFence: homeViewerFence}, revision: {catalogRevision: '1', viewerRevision: '1'}};
const selectionLeaf = (id: string): HomeSelectionPlaybackItem => ({identity: {id, kind: 'movie', libraryId: 'library'}, entry: {id, kind: 'movie', title: id, libraryId: 'library', playback: {itemId: id}}, target: {itemId: id}});
const selectionAlbum: HomeSelectionPlaybackItem = {identity: {id: 'song_release', kind: 'album', libraryId: 'library'}, entry: {id: 'song_release', kind: 'album', title: 'song_release', libraryId: 'library', playback: {itemId: 'song_release-0'}}, target: {itemId: 'song_release-0'}};
const homeChannel: HomeChannel = {source: {id: 'source', name: 'source', provenance: 'live-source', generation: 'g1', publishedAt: '', availableStart: '', availableEnd: '', refreshState: ''}, channel: {id: 'channel', sourceId: 'source', provenance: 'live-source', generation: 'g1', name: 'channel', number: '', group: '', programmes: [], tuneAvailable: true, recordAvailable: false, tuneUnavailableReason: '', recordUnavailableReason: 'not-recordable', favorite: false, hidden: false, preferenceRevision: 1, mark: {text: 'channel', seed: 'channel'}}};

/** The same channel wire seam used by client-core/v1-channel-control.test.ts: a real v1
 * session/join/seek contract, without a browser clock or a catalogue item standing for live. */
class ChannelPort {
  private serial = 0;
  windowStartMs = 0;
  private sessions = new Map<string, {id: string; revision: string; state: string; role: string; kind: string; channelId?: string; lease: {expiresAt: string; reportEveryMs: number}; presentation: {generation: number; url: string; mode: string; container?: string; engine: string; startPositionMs: number; subtitles: unknown[]; decision: {video?: {action: string; reasons: string[]}}; linear: {name: string; originMs: number; windowStartMs: number; windowEndMs: number; liveEdgeMs: number; state: string; seek?: {id: string; positionMs: number; acknowledged: boolean}; confirmedPositionMs?: number}}}>();
  send(request: V1Request): V1Response | undefined {
    const body = request.body as {channelId?: string; state?: string; seek?: {id: string; live?: boolean; positionMs?: number}; positionMs?: number} | undefined;
    const response = (value: unknown, status = 200): V1Response => ({status, headers: {}, ...(value === undefined ? {} : {body: value})});
    if (request.method === 'POST' && request.path === '/v1/playback/sessions' && body?.channelId) {
      const id = `live_session_${++this.serial}`, session = {id, revision: '1', role: 'local', kind: 'channel', channelId: body.channelId, state: body.state ?? 'playing',
        // The canonical session view a channel cleanup read must decode: the
        // general playable Session omits channelId, so this is the official view.
        lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10000},
        presentation: {generation: 1, url: `/v1/media/linear/${id}/1/tok/index.m3u8`, mode: 'stream', container: 'fmp4_hls', engine: 'native', startPositionMs: 0, subtitles: [], decision: {video: {action: 'copy', reasons: []}},
          linear: {name: 'channel', originMs: 1000, windowStartMs: this.windowStartMs, windowEndMs: 120000, liveEdgeMs: 60000, state: 'active'}}};
      this.sessions.set(id, session); return response(session, 201);
    }
    const match = /^\/v1\/playback\/sessions\/(live_session_\d+)(?:\/observer|\/observation)?$/.exec(request.path), session = match ? this.sessions.get(match[1]!) : undefined;
    if (!session) return;
    if (request.method === 'GET') return response(session);
    if (request.method === 'PATCH') {
      if (body?.state) session.state = body.state;
      if (body?.seek) session.presentation.linear.seek = {id: body.seek.id, positionMs: body.seek.live ? session.presentation.linear.liveEdgeMs : body.seek.positionMs!, acknowledged: false};
      session.revision = String(Number(session.revision) + 1); return response(session);
    }
    if (request.method === 'DELETE') { session.state = 'ended'; return response(undefined, 204); }
    if (request.method === 'POST' && request.path.endsWith('/observer')) return response({epoch: 'live-epoch-000000000001', everyMs: 1000});
    if (request.method === 'PUT' && request.path.endsWith('/observation')) {
      const linear = session.presentation.linear;
      if (linear.seek && Math.abs(body!.positionMs! - linear.seek.positionMs) <= 1000) linear.seek.acknowledged = true;
      linear.confirmedPositionMs = body!.positionMs; return response({acceptedSeq: (body as {seq?: number}).seq ?? 0, viewTag: JSON.stringify(linear).length.toString(), session});
    }
    return;
  }
}
function setup(t: TestContext, preferences: Partial<WebPlaybackSettings> = {}, environment: import('../src/platform/playback/types.ts').WebPlaybackEnvironment = {}, local?: WebPlaybackSource['localScope'], prepareStarts?: (api: HttpLocalApi) => WebPlaybackSource['preparingStarts'], options: Readonly<{homeFence?: boolean}> = {}) {
  const clock = manualClock(), server = new FakePlaybackServer({now: clock.now, audioItem: id => id.startsWith('song') || id.startsWith('book'), itemKind: id => id.startsWith('book') ? 'audiobook_file' : undefined}), inner = fakeHttp(server);
  const log: V1Request[] = [], callbacks = new Map<string, Set<(event: ServerEvent) => void>>();
  const hls = fakeHls(), video = new FakeVideo();
  const channels = new ChannelPort();
  let current = true, serial = 0, viewerFence = detailViewerFence;
  let intercept = async (_request: V1Request, response: V1Response): Promise<V1Response> => response;
  const fetcher = async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(String(input));
    const request: V1Request = {path: url.pathname + url.search, method: (init.method ?? 'GET') as V1Request['method'], headers: Object.fromEntries(new Headers(init.headers)), ...(typeof init.body === 'string' ? {body: JSON.parse(init.body)} : {}), signal: init.signal ?? undefined};
    log.push(request);
    const itemMatch = /^\/v1\/items\/([^/]+)$/.exec(url.pathname);
    const offerMatch = /^\/v1\/items\/([^/]+)\/playback-offers$/.exec(url.pathname);
    let response: V1Response;
    if (itemMatch) {
      const itemId = decodeURIComponent(itemMatch[1]!);
      response = {status: 200, headers: {}, body: {id: itemId, kind: itemId.startsWith('song') ? 'song' : itemId.startsWith('book') ? 'audiobook_file' : 'movie', title: itemId, libraryId: 'library', available: true, missing: false, durationMs: 7200000, progressMs: 0, sources: [{id: `source_${itemId}`, durationMs: 7200000, container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080}]}};
    } else if (offerMatch) {
      const itemId = decodeURIComponent(offerMatch[1]!), sessionId = url.searchParams.get('sessionId')!, session = server.session(sessionId)!;
      response = {status: 200, headers: {}, body: {
        scope: {serverId: 'server', libraryId: 'library', itemId, viewerFence}, revision: `offer_${session.generation}`,
        offersRevision: 'a'.repeat(64), transcodingEnabled: true,
        sources: [{id: `source_${itemId}`, available: true, container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080, durationMs: 7200000, factsRevision: 1, factsStatus: 'known', relation: 'unknown', boundary: 'whole_source', enabled: false, reason: 'source_selection_unavailable', streams: [{index: 1, type: 'audio', codec: 'aac', channels: 2, default: true, forced: false, enabled: true, reason: ''}], qualities: [{id: 'original', kind: 'original', label: 'Original', enabled: true}, {id: 'auto', kind: 'automatic', label: 'Automatic', enabled: true}, {id: '720p-high', kind: 'fixed', label: '720p', enabled: true, maxVideoBitrateBps: 4000000, maxAudioBitrateBps: 128000, targetDisplayHeight: 720}]}],
        current: {sessionId, generation: session.generation, state: session.state, sourceId: `source_${itemId}`, delivery: 'hls', audioSelection: 'server_first_audio', subtitleSelection: 'disabled', qualityId: 'original'},
        controls: [{id: 'source', enabled: false, reason: 'source_selection_unavailable'}, {id: 'audio', enabled: false, reason: 'track_selection_unavailable'}, {id: 'subtitles', enabled: false, reason: 'subtitle_delivery_unavailable'}, {id: 'quality', enabled: true, reason: ''}],
      }};
    } else response = channels.send(request) ?? await inner.send(request as never) as V1Response;
    response = await intercept(request, response);
    return new Response(response.body === undefined ? null : JSON.stringify(response.body), {status: response.status, headers: {'Content-Type': 'application/json', ...response.headers}});
  };
  const api = new HttpLocalApi('https://portico.test', '', fetcher as typeof fetch);
  const source: WebPlaybackSource = {
    ...(local ? {localScope: local} : {}),
    ...(prepareStarts ? {preparingStarts: prepareStarts(api)} : {}),
    api, authority: {...viewer, epoch: 1, ...(options.homeFence === false ? {} : {viewerFence: homeViewerFence})}, contentScope: {serverId: 'server', viewerId: 'local_account_profile'}, isCurrent: () => current,
    events: {on: (type, callback) => { let list = callbacks.get(type); if (!list) callbacks.set(type, list = new Set()); list.add(callback); return () => { list!.delete(callback); }; }},
    settings: {videoSpeed: () => 1, audiobookSpeed: () => 1.25, skipModes: () => ({intro: 'off', recap: 'off', credits: 'off'}), audioEffects: () => ({gapless: false, crossfadeSeconds: 0, normalization: 'off'}), webVolume: () => true, ...preferences},
  };
  const runtime = new WebShellPlaybackRuntime(source, {timers: clock, playerTimers: clock, loadHls: async () => hls.Hls, nativeHls: 'never', key: () => `runtime-key-${++serial}`, ...environment});
  runtime.mount(video);
  t.after(() => runtime.dispose());
  const arrive = async () => { await settle(); const engine = hls.instances.at(-1)!; video.ready(Number(engine.config.startPosition)); await settle(); return engine; };
  const arriveLive = async () => {
    await settle(); const instance = hls.instances.at(-1)!, seconds = runtime.getSnapshot().playback.pendingSeek?.positionSeconds ?? 60;
    instance.engine.levels = [{details: {fragments: [{start: seconds - 2, duration: 8, programDateTime: 1000 + (seconds - 2) * 1000} as never]}}];
    instance.engine.playingDate = new Date(1000 + seconds * 1000); video.ready(seconds, 12, 120); await settle(); video.land(seconds); await settle(); return instance;
  };
  // Exercise the real owned service's refresh/denial boundary without adding a public
  // renderer command or replacing its validated responses with snapshot doubles.
  const offers = () => (runtime as unknown as {stack: {offers: PlayerOffersService}}).stack.offers;
  return {runtime, source, api, server, log, video, hls, clock, arrive, arriveLive, offers, expireLiveBookmark: () => { channels.windowStartMs = 50000; }, invalidate: () => { current = false; }, callbacks: () => [...callbacks.values()].reduce((n, v) => n + v.size, 0), intercept: (fn: typeof intercept) => { intercept = fn; }, changeViewerFence: () => { viewerFence = 'other_detail_viewer'; }};
}
function deferred() { let resolve!: () => void, enter!: () => void; const promise = new Promise<void>(r => { resolve = r; }), started = new Promise<void>(r => { enter = r; }); return {started, resolve, hold: async () => { enter(); await promise; }}; }

const introMarker = {id: 'intro', kind: 'intro', startMs: 30000, endMs: 80000, automaticSafe: true};
const creditsMarker = {id: 'credits', kind: 'credits', startMs: 7100000, endMs: 7200000, automaticSafe: true};
function policyFixture(f: ReturnType<typeof setup>, markers = [introMarker, creditsMarker], postPlay: Record<string, unknown> = {}) {
  f.intercept(async (request, response) => {
    if (request.path.includes('/playback-offers?')) {
      const body = response.body as {current: {sourceId: string}};
      return {...response, body: {...body, markers: {sourceId: body.current.sourceId, revision: 'markers_1', markers}}};
    }
    if (request.method === 'GET' && /^\/v1\/queues\/[^/]+$/.test(request.path)) return {...response, body: {...response.body as object, postPlay: {autoplay: true, countdownMs: 5000, ...postPlay}}};
    if (request.method === 'GET' && request.path.includes('/window?')) {
      const body = response.body as {items: object[]};
      return {...response, body: {...body, items: body.items.map(item => ({...item, durationMs: 7200000}))}};
    }
    const body = response.body as {queue?: object; window?: object[]} | undefined;
    if (body?.queue) return {...response, body: {...body, queue: {...body.queue, postPlay: {autoplay: true, countdownMs: 5000, ...postPlay}}, ...(body.window ? {window: body.window.map(item => ({...item, durationMs: 7200000}))} : {})}};
    return response;
  });
}
async function policyQueue(f: ReturnType<typeof setup>) {
  await f.runtime.selectionPlayback(() => true).play({context: selectionContext, items: [selectionLeaf('first'), selectionLeaf('next')], signal: new AbortController().signal});
  await f.arrive();
  const fence = f.runtime.getSnapshot().fence!;
  assert.ok(fence, 'the coherent source/queue fixture must publish a real fence');
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  f.runtime.open(fence);
  return fence;
}

test('policy actions execute only the rendered marker and record a real manual skip on the actual session', async t => {
  const f = setup(t, {skipModes: () => ({intro: 'ask', recap: 'ask', credits: 'ask'})});
  policyFixture(f); const fence = await policyQueue(f);
  f.video.advance(40); await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.policy!.prompt?.kind, 'skip');
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'skip', markerId: 'old-intro'}), /playback_policy_unavailable/);
  const loads = f.video.loads;
  await f.runtime.policyAction(fence, {kind: 'skip', markerId: 'intro'});
  assert.equal(f.video.loads, loads, 'a skip seeks the same actual media attachment');
  assert.equal(f.runtime.getSnapshot().playback.pendingSeek!.positionSeconds, 79);
  assert.notEqual(f.runtime.getSnapshot().playback.positionSeconds, 79, 'the UI does not fabricate a seek landing');
  f.video.land(79); await settle(); f.clock.advance(10000); await settle();
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 79);
  assert.ok(f.log.some(request => request.path.endsWith('/observation') && JSON.stringify(request.body).includes('"skipped":{"markerId":"intro","mode":"manual","positionMs":40000}')));
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'skip', markerId: 'intro'}), /playback_policy_unavailable/);
});

test('policy dismissal is marker-scoped and automatic Undo returns to the actual pre-skip position', async t => {
  const f = setup(t, {skipModes: () => ({intro: 'ask', recap: 'ask'})});
  policyFixture(f, [introMarker, {id: 'recap', kind: 'recap', startMs: 100000, endMs: 120000, automaticSafe: true}]);
  const fence = await policyQueue(f); f.video.advance(40); await settle();
  await f.runtime.policyAction(fence, {kind: 'dismiss', markerId: 'intro'});
  assert.equal(f.runtime.getSnapshot().presentation!.policy!.prompt, null); assert.equal(f.video.seeking, false);
  f.video.advance(105); await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.policy!.prompt?.kind, 'skip');
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'dismiss', markerId: 'intro'}), /playback_policy_unavailable/);

  const a = setup(t, {skipModes: () => ({intro: 'auto'})}); policyFixture(a);
  const automatic = await policyQueue(a); a.video.advance(38); await settle();
  assert.equal(a.runtime.getSnapshot().presentation!.policy!.undo?.marker.id, 'intro');
  a.video.land(79); await settle(); await a.runtime.pause(automatic);
  a.clock.advance(3000); await a.runtime.policyAction(automatic, {kind: 'undo', markerId: 'intro'});
  assert.equal(a.runtime.getSnapshot().playback.pendingSeek!.positionSeconds, 38);
  a.video.land(38); await settle(); await a.runtime.resume(automatic);
  assert.equal(a.runtime.getSnapshot().presentation!.policy!.undo, null);
  assert.equal(a.runtime.getSnapshot().playback.pendingSeek, undefined, 'the undone marker does not automatically skip again');
});

test('Undo expires on the real policy clock while paused and the quiet dock never exposes it later', async t => {
  for (const collapsed of [false, true]) {
    const f = setup(t, {skipModes: () => ({intro: 'auto'})}); policyFixture(f);
    const fence = await policyQueue(f); if (collapsed) f.runtime.collapse(fence);
    f.video.advance(38); await settle(); f.video.land(79); await settle(); await f.runtime.pause(fence);
    if (collapsed) { f.runtime.open(fence); assert.equal(f.runtime.getSnapshot().presentation!.policy!.undo, null); }
    else assert.ok(f.runtime.getSnapshot().presentation!.policy!.undo);
    f.clock.advance(6000); await settle();
    assert.equal(f.runtime.getSnapshot().presentation!.policy!.undo, null);
    await assert.rejects(f.runtime.policyAction(fence, {kind: 'undo', markerId: 'intro'}), /playback_policy_unavailable/);
  }
});

test('Play now uses the actual next occurrence before completion and double activation cannot move twice', async t => {
  const f = setup(t, {skipModes: () => ({credits: 'ask'})}); policyFixture(f);
  const fence = await policyQueue(f); f.video.advance(7110); await settle();
  const next = playbackPolicyNext(f.runtime.getSnapshot(), fence)!;
  assert.equal(next.itemId, 'next'); assert.equal(next.title, 'Title next');
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'play-now', nextEntryId: 'unrelated'}), /playback_policy_unavailable/);
  const play = f.runtime.policyAction(fence, {kind: 'play-now', nextEntryId: next.entryId});
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'play-now', nextEntryId: next.entryId}), /stale_playback_command|playback_policy_unavailable/);
  await play; await f.arrive();
  const moves = f.log.filter(request => request.path.endsWith(':advance'));
  assert.equal(moves.length, 1); assert.deepEqual(moves[0]!.body, {reason: 'next'});
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'next'); assert.equal(f.video.paused, false);
});

test('Skip credits on an actual up-next offer uses its marker and never advances or guesses the next occurrence', async t => {
  const f=setup(t,{skipModes:()=>({credits:'ask'})});
  const credits={...creditsMarker,endMs:7150000};policyFixture(f,[credits],{startDuringCredits:true});
  const fence=await policyQueue(f);f.video.advance(7110);await settle();
  const before=f.runtime.getSnapshot(),prompt=before.presentation!.policy!.prompt;
  if(prompt?.kind!=='up-next'||!prompt.offer.marker)assert.fail('genuine marked up-next offer required');
  assert.equal(prompt.offer.marker.id,credits.id);assert.equal(prompt.sceneAfterCredits,true);
  await assert.rejects(f.runtime.policyAction(fence,{kind:'skip',markerId:'old-credits'}),/playback_policy_unavailable/);
  await assert.rejects(f.runtime.policyAction(fence,{kind:'dismiss',markerId:credits.id}),/playback_policy_unavailable/);
  const loads=f.video.loads;await f.runtime.policyAction(fence,{kind:'skip',markerId:prompt.offer.marker.id});
  assert.equal(f.runtime.getSnapshot().playback.pendingSeek?.positionSeconds,7150,'canonical credits skip lands on the actual post-credits scene');
  assert.equal(f.runtime.getSnapshot().session?.id,before.session?.id);assert.equal(f.video.loads,loads);
  assert.equal(f.log.some(request=>request.path.endsWith(':advance')),false,'Skip credits is not Play now');
  f.video.land(7150);await settle();f.clock.advance(10000);await settle();
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds,7150);
  assert.ok(f.log.some(request=>request.path.endsWith('/observation')&&JSON.stringify(request.body).includes('"skipped":{"markerId":"credits","mode":"manual","positionMs":7110000}')));
  await assert.rejects(f.runtime.policyAction(fence,{kind:'skip',markerId:credits.id}),/playback_policy_unavailable/);
});

test('an overlapping up-next marker cannot dismiss or skip a different active segment', async t => {
  const f=setup(t,{skipModes:()=>({intro:'ask',credits:'ask'})});
  policyFixture(f,[{...introMarker,endMs:7150000},creditsMarker]);const fence=await policyQueue(f);
  f.video.advance(7110);await settle();assert.equal(f.runtime.getSnapshot().presentation?.policy?.prompt?.kind,'up-next');
  const seeks=f.video.seeks.length;await assert.rejects(f.runtime.policyAction(fence,{kind:'skip',markerId:creditsMarker.id}),/playback_policy_unavailable/);
  assert.equal(f.video.seeks.length,seeks);
  f.video.advance(40);await settle();const prompt=f.runtime.getSnapshot().presentation?.policy?.prompt;
  assert.equal(prompt?.kind,'skip');if(prompt?.kind==='skip')assert.equal(prompt.marker.id,introMarker.id,'the canonical expected-ID guard refuses before dismissing the unrelated active intro');
});

test('explicit automatic credits advance uses the real completion request before media end in both expanded and quiet modes', async t => {
  for (const collapsed of [false, true]) {
    const f = setup(t, {skipModes: () => ({credits: 'ask'})}); policyFixture(f, [creditsMarker], {startDuringCredits: true});
    const fence = await policyQueue(f); if (collapsed) f.runtime.collapse(fence);
    f.video.advance(7110); await settle();
    assert.equal(f.runtime.getSnapshot().playback.phase, 'ready');
    f.clock.advance(5000); await f.arrive();
    const moves = f.log.filter(request => request.path.endsWith(':advance'));
    assert.equal(moves.length, 1); assert.deepEqual(moves[0]!.body, {reason: 'completion'});
    assert.equal(f.runtime.getSnapshot().playback.itemId, 'next'); assert.equal(f.video.ended, false);
    assert.equal(f.runtime.getSnapshot().presentation!.expanded, !collapsed);
  }
});

test('Watch credits suppresses only early advance; the real confirmed end still runs its queue policy', async t => {
  const f = setup(t, {skipModes: () => ({credits: 'ask'})}); policyFixture(f, [creditsMarker], {startDuringCredits: true});
  const fence = await policyQueue(f); f.video.advance(7110); await settle();
  const prompt = f.runtime.getSnapshot().presentation!.policy!.prompt!;
  if (prompt.kind !== 'up-next') assert.fail('real credits offer required');
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'watch-credits', offerId: 'old-offer'}), /playback_policy_unavailable/);
  await f.runtime.policyAction(fence, {kind: 'watch-credits', offerId: prompt.offer.id});
  f.clock.advance(10000); await settle();
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 0);
  assert.equal(f.runtime.getSnapshot().presentation!.policy!.prompt, null);
  f.video.finish(); await settle(); f.clock.advance(5000); await f.arrive();
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'next');
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 1);
});

test('credits followed by a scene never auto-advance early and pausing freezes the explicit early countdown', async t => {
  const scene = setup(t, {skipModes: () => ({credits: 'ask'})});
  policyFixture(scene, [{...creditsMarker, endMs: 7160000}], {startDuringCredits: true});
  await policyQueue(scene); scene.video.advance(7110); await settle();
  const prompt = scene.runtime.getSnapshot().presentation!.policy!.prompt;
  assert.equal(prompt?.kind === 'up-next' && prompt.sceneAfterCredits, true);
  scene.clock.advance(10000); await settle(); assert.equal(scene.log.filter(request => request.path.endsWith(':advance')).length, 0);
  const paused = setup(t, {skipModes: () => ({credits: 'ask'})}); policyFixture(paused, [creditsMarker], {startDuringCredits: true});
  const fence = await policyQueue(paused); paused.video.advance(7110); await settle(); await paused.runtime.pause(fence);
  paused.clock.advance(10000); await settle(); assert.equal(paused.log.filter(request => request.path.endsWith(':advance')).length, 0);
  await paused.runtime.resume(fence); paused.clock.advance(5000); await paused.arrive();
  assert.equal(paused.runtime.getSnapshot().playback.itemId, 'next');
});

test('Still watching Replay restarts the exact held occurrence at zero without advancing its queue', async t => {
  const f = setup(t);policyFixture(f, [], {passoutCheckDue: true});
  const fence = await policyQueue(f);
  await assert.rejects(f.runtime.replay(fence), /playback_command_unavailable/);
  f.video.finish();await settle();f.clock.advance(15000);await settle();
  const held = f.runtime.getSnapshot();
  assert.equal(held.presentation!.policy!.attention, 'still-watching');
  assert.equal(held.presentation!.finished, false, 'the modal is not an ordinary finished card');
  assert.equal(held.presentation!.controls.includes('replay'), true);
  const queueId = held.queueView!.queue.id, versionId = held.session!.versionId;
  await f.runtime.replay(fence);await f.arrive();
  const restarted = f.runtime.getSnapshot();
  assert.equal(restarted.playback.itemId, fence.itemId);
  assert.equal(restarted.queueView!.queue.id, queueId);
  assert.equal(restarted.queueView!.queue.currentEntryId, fence.entryId);
  assert.notEqual(restarted.session!.id, fence.sessionId);
  const start = f.log.filter(request => request.method === 'POST' && request.path === '/v1/playback/sessions').at(-1)!.body as {queue: {queueId: string; entryId: string}; startPositionMs: number; versionId: string};
  assert.deepEqual(start.queue, {queueId, entryId: fence.entryId});
  assert.equal(start.startPositionMs, 0);
  assert.equal(start.versionId, versionId, 'the actual held version is retained');
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 0);
  await assert.rejects(f.runtime.replay(fence), /stale_playback_command/);
});

test('Still watching waits for confirmed completion and only the explicit current response can continue', async t => {
  const f = setup(t); policyFixture(f, [], {passoutCheckDue: true}); const fence = await policyQueue(f);
  const next = playbackPolicyNext(f.runtime.getSnapshot(), fence)!;
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'continue', nextEntryId: next.entryId}), /playback_policy_unavailable/);
  f.video.finish(); await settle(); f.clock.advance(15000); await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.policy!.attention, 'still-watching');
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false);
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'play-now', nextEntryId: next.entryId}), /playback_policy_unavailable/);
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 0);
  await f.runtime.policyAction(fence, {kind: 'continue', nextEntryId: next.entryId}); await f.arrive();
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'next');
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 1);
});

test('canceling a confirmed end countdown produces real Replay without advancing or fabricating another session', async t => {
  const f = setup(t); policyFixture(f, []); const fence = await policyQueue(f);
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'cancel-autoplay'}), /playback_policy_unavailable/);
  f.video.finish(); await settle();
  await f.runtime.policyAction(fence, {kind: 'cancel-autoplay'}); f.clock.advance(10000); await settle();
  const snapshot = f.runtime.getSnapshot();
  assert.equal(snapshot.fence!.sessionId, fence.sessionId); assert.equal(snapshot.presentation!.finished, true);
  assert.ok(snapshot.presentation!.controls.includes('replay'));
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 0);
  await f.runtime.replay(fence); await f.arrive(); assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 0);
});

test('a button arriving after the actual countdown deadline cannot claim it canceled an already committed advance', async t => {
  const f = setup(t); policyFixture(f, []); const fence = await policyQueue(f);
  f.video.finish(); await settle(); f.clock.advance(5000);
  await assert.rejects(f.runtime.policyAction(fence, {kind: 'cancel-autoplay'}), /playback_policy_unavailable|stale_playback_command/);
  await f.arrive(); assert.equal(f.runtime.getSnapshot().playback.itemId, 'next');
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 1);
});

test('policy actions and next metadata reject stale full fences, collapsed prompts and retired authority', async t => {
  const f = setup(t, {skipModes: () => ({intro: 'ask'})}); policyFixture(f); const fence = await policyQueue(f);
  f.video.advance(40); await settle();
  for (const other of [{...fence, generation: fence.generation + 1}, {...fence, sourceId: 'other'}, {...fence, entryId: 'other'}, {...fence, sessionId: 'other'}, {...fence, playback: {...fence.playback, scope: {...fence.playback.scope, profileId: 'other'}}}]) {
    assert.equal(playbackPolicyNext(f.runtime.getSnapshot(), other), null);
    await assert.rejects(f.runtime.policyAction(other, {kind: 'skip', markerId: 'intro'}), /stale_playback_command/);
  }
  const snapshot = f.runtime.getSnapshot(), view = snapshot.queueView!, next = playbackPolicyNext(snapshot, fence)!;
  assert.ok(next); assert.equal(Object.isFrozen(next), true);
  assert.equal(playbackPolicyNext({...snapshot, queueView: {...view, current: {...view.current!, generation: fence.generation + 1}}}, fence), null);
  assert.equal(playbackPolicyNext({...snapshot, queueView: {...view, items: view.items.map(item => item.entryId === next.entryId ? {...item, title: ''} : item)}}, fence), null);
  assert.equal(playbackPolicyNext({...snapshot, queueView: {...view, postPlay: {...view.postPlay, nextEntryId: 'other'}}}, fence), null);
  f.runtime.collapse(fence); await assert.rejects(f.runtime.policyAction(fence, {kind: 'skip', markerId: 'intro'}), /playback_policy_unavailable/);
  f.runtime.open(fence); f.invalidate(); await assert.rejects(f.runtime.policyAction(fence, {kind: 'skip', markerId: 'intro'}), /stale_playback_command/);
});

/** The platform sender boundary; SDK transport/picker qualification stays with its owner. */
class ControlledCastSender implements WebCastPort {
  state: ReturnType<WebCastPort['getSnapshot']> = {available: true, phase: 'off', positionSeconds: 0, durationSeconds: 0, paused: false, audioTracks: [], textTracks: []};
  listeners = new Set<() => void>();
  starts: Parameters<WebCastPort['start']>[] = [];
  commands: unknown[][] = [];
  disposed = false;
  private finish?: (ok: boolean) => void;
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  prepareOnIdle() { return () => {}; }
  async prepareForPlayer() { return true; }
  publish(patch: Partial<typeof this.state>) { this.state = {...this.state, ...patch}; for (const listener of [...this.listeners]) listener(); }
  start(...args: Parameters<WebCastPort['start']>) { this.starts.push(args); this.publish({phase:'connecting',remoteStopped:false,itemId:args[0], title: args[1].title}); return new Promise<boolean>(resolve => { this.finish = resolve; }); }
  acknowledge(ok: boolean) { this.finish?.(ok); this.finish = undefined; }
  cancelConnecting() { this.commands.push(['cancel']); this.publish({phase: 'off'}); this.acknowledge(false); }
  play() { this.commands.push(['play']); }
  pause() { this.commands.push(['pause']); }
  toggle() { this.commands.push(['toggle']); }
  seek(seconds: number) { this.commands.push(['seek', seconds]); }
  setAudioTrack(id: number) { this.commands.push(['audio', id]); }
  setSubtitleTrack(id: number | null) { this.commands.push(['subtitles', id]); }
  answerTakeover(allow: boolean) { this.commands.push(['takeover', allow]); }
  stop() { this.commands.push(['stop']); this.publish({phase:'off',remoteStopped:true}); }
  dismiss() { this.commands.push(['dismiss']); this.publish({phase:'off',remoteStopped:false}); }
  dispose() { this.disposed = true; this.listeners.clear(); }
}
function castController(t: TestContext, f: ReturnType<typeof setup>) {
  const sender = new ControlledCastSender(), errors: unknown[] = [];
  const controller = new PlayOnController({sender, runtime: f.runtime, isCurrent: f.source.isCurrent, reportError: error => errors.push(error), now: f.clock.now, setTimer: f.clock.setTimer, clearTimer: f.clock.clearTimer, key: () => 'remote-attempt'});
  t.after(() => controller.dispose());
  return {controller, sender, errors};
}

test('WEB002 the MAIN SDK, canonical sender and mounted runtime retain through picker, receiver ACK and stopped continuation', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive();
  const local = f.hls.instances.at(-1)!, first = f.runtime.getSnapshot(), sessionId = first.session!.id;
  f.api.setUserMutationSession({accessToken: 'cast-test-token', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z',
    tokenGeneration: '1', sessionFamilyId: 'family', deviceId: 'sender-device', viewer: {...viewer, role: 'member'}});
  const policy = deferred();t.after(() => policy.resolve());
  f.intercept(async (request, response) => {
    if (request.path === routePath('get_user_mutation_policy')) {
      assert.equal(request.headers?.authorization, 'Bearer cast-test-token');
      await policy.hold();
      return {status: 200, headers: {}, body: {serverTimeMs: Date.now(), retiredBeforeMs: 0}};
    }
    return request.path === '/v1/cast/bootstrap'
    ? {status: 200, headers: {}, body: {bootstrap: {id: 'cbs_1', code: 'ABC123', state: 'pending', transfer: {id: 'cbs_1', state: 'pending'}}}}
    : request.method === 'GET' && request.path === '/v1/cast/bootstrap/cbs_1'
      ? {status: 200, headers: {}, body: {bootstrap: {id: 'cbs_1', transfer: {id: 'cbs_1', state: 'committed', itemId: 'movie', playbackState: 'playing'}}}}
      : response;
  });
  const stored = new Map<string, string>(), previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  Object.defineProperty(globalThis, 'localStorage', {configurable: true, value: {
    getItem: (key: string) => stored.get(key) ?? null, setItem: (key: string, value: string) => {stored.set(key, value);},
    get length() {return stored.size;}, key: (index: number) => [...stored.keys()][index] ?? null, removeItem: (key: string) => {stored.delete(key);},
  }});
  t.after(() => {if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage);else delete (globalThis as Record<string, unknown>).localStorage;});
  const messages: Record<string, unknown>[] = [], listeners = new Set<(namespace: string, body: string) => void>();
  let dispatched!: () => void;const stopDispatched = new Promise<void>(resolve => {dispatched = resolve;});
  const reply = (body: unknown) => { for (const listener of [...listeners]) listener('urn:x-cast:tv.getportico.portico', JSON.stringify(body)); };
  let picks = 0, selected: CastSession | null = null;
  const session: CastSession = {
    getCastDevice: () => ({friendlyName: 'Living Room'}),
    sendMessage: async (_namespace, message) => {
      const command = message as Record<string, unknown>;
      if (command.type === 'stop') {
        assert.equal(isUserMutationKey(String(command.requestId)), true);
        const retained = [...stored].filter(([key]) => key.startsWith('portico.cast.stop:')).flatMap(([,value]) => JSON.parse(value));
        assert.ok(retained.some(record => record.key === command.requestId && record.state === 'pending' && JSON.stringify(record.target) === JSON.stringify(command.target)), 'exact Stop identity is durable before SDK dispatch');
      }
      messages.push(command);if (command.type === 'stop') dispatched();
    },
    addMessageListener: (_namespace, listener) => { listeners.add(listener); },
    removeMessageListener: (_namespace, listener) => { listeners.delete(listener); }, endSession() {},
  };
  const picker=deferred();
  const context = {setOptions() {}, requestSession: () => { picks++; selected = session; return picker.hold(); }, getCurrentSession: () => selected, addEventListener() {}, removeEventListener() {}};
  const browser: CastBrowser = {
    // The already available controlled native SDK never touches a script or DOM image loader.
    document: new Proxy({} as Document, {get() { throw new Error('unexpected_sdk_document_read'); }}),
    navigator: {userAgent: 'Chrome/156.0 Linux'}, chrome: {},
    cast: {framework: {CastContext: {getInstance: () => context}, CastContextEventType: {SESSION_STATE_CHANGED: 'session'}, SessionState: {SESSION_RESUMED: 'resumed', SESSION_ENDED: 'ended'}}},
    setTimeout: f.clock.setTimer, clearTimeout: f.clock.clearTimer,
  };
  const sender = new WebCastSender({api: f.api, serverOrigin: 'https://portico.test', castApplicationId: 'APP123', browser});
  assert.equal(await sender.prepareForPlayer(), true);
  const errors: unknown[] = [];
  const controller = new PlayOnController({sender, runtime: f.runtime, isCurrent: f.source.isCurrent, reportError: error => errors.push(error), now: f.clock.now, setTimer: f.clock.setTimer, clearTimer: f.clock.clearTimer});
  t.after(() => controller.dispose());
  const changes:boolean[]=[];f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
  const started = controller.startCast(); assert.equal(picks, 1, 'native picker opens synchronously in the originating click');
  assert.equal(sender.getSnapshot().phase,'connecting');assert.equal(sender.retainsMedia(),true);assert.equal(f.runtime.retainsMedia(),true);picker.resolve();
  await settle(); assert.equal(sender.getSnapshot().phase, 'pairing');
  reply({type: 'pair-pending'}); assert.equal(sender.getSnapshot().phase, 'waiting');
  assert.equal(f.runtime.getSnapshot().session!.id, sessionId); assert.equal(local.destroyed, false);
  reply({type: 'paired'}); await settle();
  const load = messages.find(message => message.type === 'load')!;
  assert.ok(load); assert.deepEqual(load.queue, {...first.session!.queue, transferId: 'cbs_1'});
  assert.equal(controller.getSnapshot().remote!.positionKnown, false, 'requested start is not an observed receiver clock');
  reply({type: 'loaded', itemId: 'movie', requestId: 'wrong-request'}); await settle();
  assert.equal(f.runtime.getSnapshot().session!.id, sessionId); assert.equal(local.destroyed, false);
  reply({type: 'loaded', itemId: 'movie', requestId: load.requestId}); assert.equal(await started, true); await settle();
  assert.equal(f.runtime.getSnapshot().session, undefined); assert.equal(local.destroyed, true);
  assert.equal(f.runtime.retainsMedia(),true,'the actual sender/PlayOn owner retains after the local session and dock have drained');assert.deepEqual(changes,[]);
  reply({type:'status',controller:{bindingId:'binding',controllerGeneration:1,connectionGeneration:1},occurrence:{sessionId:'receiver',generation:1,itemId:'movie'},itemId:'movie',positionSeconds:57, durationSeconds: 3186, paused: false, audioTracks: [], textTracks: []});
  assert.equal(controller.getSnapshot().remote!.positionSeconds, 57);
  controller.seek(controller.getSnapshot().remote!, 100); assert.equal(controller.getSnapshot().remote!.positionSeconds, 57);
  controller.stop(controller.getSnapshot().remote!);
  await policy.started;
  assert.equal(messages.some(message => message.type === 'stop'), false, 'Stop waits for the original authenticated key policy');
  assert.equal(f.runtime.retainsMedia(), true);
  policy.resolve();await stopDispatched;
  const stop=messages.find(message=>message.type==='stop')!;assert.ok(stop.target);
  reply({type:'stopped',requestId:stop.requestId,target:stop.target,positionSeconds:57,durationSeconds:3186});
  assert.equal(controller.getSnapshot().remote!.phase, 'stopped'); assert.equal(controller.getSnapshot().remote!.positionSeconds, 57);
  assert.equal(messages.some(message => 'mediaUrl' in message || 'grant' in message || 'sessionId' in message), false);
  assert.deepEqual(errors, []);assert.equal(f.runtime.retainsMedia(),true);
  controller.dispose();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[false]);
});

test('Cast keeps the mounted local engine through pairing and waiting, handing off only on casting', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive();
  const session = f.runtime.getSnapshot().session!, local = f.hls.instances[0]!;
  const {controller, sender, errors} = castController(t, f), start = controller.startCast();
  assert.equal(sender.starts.length, 1, 'picker call stays synchronous with the row click');
  const queue = f.runtime.getSnapshot().queueView!.queue;
  // V01/V02: the exact fenced session's version and audio track travel with the load.
  assert.ok(session.versionId && session.presentation.audioTrackId);
  assert.deepEqual(sender.starts[0]![3], {versionId: session.versionId, audioTrackId: session.presentation.audioTrackId, queue: session.queue, source: {
    sessionId: session.id, generation: session.presentation.generation,
    queueId: queue.id, entryId: session.queue.entryId, queueRevision: queue.revision}});
  assert.deepEqual(Object.keys(sender.starts[0]![1]).sort(), ['artworkPath', 'kind', 'subtitle', 'title']);
  for (const phase of ['connecting', 'pairing', 'waiting'] as const) {
    sender.publish({phase});
    assert.equal(f.runtime.getSnapshot().session!.id, session.id); assert.equal(local.destroyed, false);
    assert.equal(controller.getSnapshot().remote!.positionKnown, false, 'requested start is never a receiver clock');
  }
  sender.publish({phase: 'casting', device: {name: 'Living Room'}, receiver: {itemId: 'movie', positionSeconds: 27, durationSeconds: 3186, paused: false}, acceptedSource: sender.starts[0]![3]!.source});
  sender.acknowledge(true); assert.equal(await start, true); await settle();
  assert.equal(f.runtime.getSnapshot().session, undefined); assert.equal(local.destroyed, true);
  assert.equal(controller.getSnapshot().remote!.positionSeconds, 27); assert.deepEqual(errors, []);
});

test('Cast cancellation preserves local playback and source replacement retires a pending attempt', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive();
  const session = f.runtime.getSnapshot().session!.id, {controller, sender} = castController(t, f);
  let start = controller.startCast(); controller.stop(controller.getSnapshot().remote!);
  assert.equal(await start, false); await settle();
  assert.equal(controller.getSnapshot().remote, undefined); assert.equal(f.runtime.getSnapshot().session!.id, session);
  assert.deepEqual(sender.commands, [['cancel']]);
  start = controller.startCast(); await f.runtime.play('another', 10); await f.arrive();
  assert.equal(await start, false); assert.equal(f.runtime.getSnapshot().session!.itemId, 'another');
  f.invalidate(); f.runtime.dispose();
  assert.equal(controller.getSnapshot().disposed, true); assert.equal(sender.disposed, true);
  assert.throws(() => controller.openPlayOn(), /playback_authority_retired/);
});

test('Cast controls require current receiver facts and tokens, never an optimistic seek position', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive();
  const {controller, sender} = castController(t, f), start = controller.startCast();
  sender.publish({phase: 'casting', positionSeconds: 999, durationSeconds: 9999}); sender.acknowledge(true); await start; await settle();
  let token = controller.getSnapshot().remote!;
  assert.equal(token.positionKnown, false); controller.seek(token, 100); assert.deepEqual(sender.commands, []);
  sender.publish({receiver: {itemId: 'movie', positionSeconds: 40, durationSeconds: 100, paused: true}, audioTracks: [{id: 2, name: 'English', active: true}]});
  controller.toggle(token); assert.deepEqual(sender.commands, [], 'a previous status token cannot command the receiver');
  token = controller.getSnapshot().remote!;
  controller.seek(token, 150); controller.seek(token, NaN); controller.audio(token, 3); controller.audio(token, 2); controller.subtitles(token, 3); controller.subtitles(token, null);
  assert.deepEqual(sender.commands, [['seek', 100], ['audio', 2], ['subtitles', null]]);
  assert.equal(controller.getSnapshot().remote!.positionSeconds, 40);
});

test('Cast stopped continuation uses retained receiver time, expires at fifteen seconds and never stops adopted local playback', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive();
  const {controller, sender} = castController(t, f), start = controller.startCast();
  sender.publish({phase: 'casting', receiver: {itemId: 'movie', positionSeconds: 57, durationSeconds: 3186, paused: false}}); sender.acknowledge(true); await start; await settle();
  sender.stop(); const token = controller.getSnapshot().remote!;
  assert.equal(token.phase, 'stopped'); assert.equal(token.positionSeconds, 57);
  f.clock.advance(14_999); assert.equal(controller.getSnapshot().remote!.phase, 'stopped');
  await controller.continueHere(token); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.itemId, 'movie'); assert.equal(f.hls.instances.at(-1)!.config.startPosition, 57);
  assert.equal(controller.getSnapshot().remote, undefined);
  sender.publish({phase: 'casting', itemId: 'movie', receiver: {itemId: 'movie', positionSeconds: 60, durationSeconds: 3186, paused: false}});
  sender.publish({phase: 'replaced'}); f.clock.advance(15_000);
  assert.equal(controller.getSnapshot().remote, undefined); assert.equal(f.runtime.getSnapshot().session!.itemId, 'movie');
});

test('Cast continue-here plays the version the receiver reported, not the server default (V02)', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive();
  const {controller, sender} = castController(t, f), start = controller.startCast();
  sender.publish({phase: 'casting', receiver: {itemId: 'movie', versionId: 'v2', positionSeconds: 57, durationSeconds: 3186, paused: false}}); sender.acknowledge(true); await start; await settle();
  sender.stop(); const token = controller.getSnapshot().remote!;
  assert.equal(token.versionId, 'v2');
  const before = f.log.length;
  await controller.continueHere(token); await f.arrive();
  const started = f.log.slice(before).filter(request => request.method === 'POST' && request.path === '/v1/playback/sessions').at(-1)?.body as {versionId?: string} | undefined;
  assert.equal(started?.versionId, 'v2');
});

test('entry version choice uses the real grouped version id and fresh title resume under section21', async t => {
  const f = setup(t);
  f.intercept(async (request, response) => {
    if (request.path === '/v1/items/movie/playback-options') {
      const body = response.body as {versions: {id: string}[]};
      return {...response, body: {...body, versions: [...body.versions, {...body.versions[0], id: 'v2'}]}};
    }
    return response;
  });
  await f.runtime.playVersion({itemId: 'movie', versionId: 'v2', context: selectionContext, signal: new AbortController().signal}, context => context === selectionContext);
  await f.arrive();
  const starts = f.log.filter(request => request.method === 'POST' && request.path.includes('/playback/sessions'));
  assert.ok(starts.some(request => (request.body as {versionId?: string}).versionId === 'v2'));
  assert.equal(f.runtime.getSnapshot().session!.versionId, 'v2');
  assert.equal(f.runtime.getSnapshot().playback.phase, 'ready');
  assert.equal(f.hls.instances.length, 1);
  assert.equal(f.hls.instances[0]!.config.startPosition, 120, 'binding21 supersedes the former caller-supplied25s fixture; fresh actual Options.resume is120s');
});

test('a captured screen starts the actual item and library, preserves ordinary start-over and uses fresh explicit-version resume', async t => {
  const f = setup(t), abort = new AbortController(), context = {signal: abort.signal, isCurrent: () => true};
  await f.runtime.playFromScreen({libraryId: 'library', itemId: 'movie', startSeconds: 0}, context); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.itemId, 'movie');
  assert.equal(f.hls.instances.at(-1)!.config.startPosition, 0);
  versionFixture(f, 120);
  await f.runtime.playFromScreen({libraryId: 'library', itemId: 'movie', startSeconds: 25, versionId: 'v1'}, context); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.versionId, 'v1');
  assert.equal(f.hls.instances.at(-1)!.config.startPosition, 120);
  const before = f.log.length;
  await f.runtime.playFromScreen({libraryId: 'library', itemId: 'movie', startSeconds: 9}, context); await f.arrive();
  assert.equal(f.hls.instances.at(-1)!.config.startPosition, 9);
  assert.ok(f.log.slice(before).every(request => !(request.body as {versionId?: string} | undefined)?.versionId));
});

test('foreign library and unavailable explicit version cannot replace still-playing media', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive();
  const session = f.runtime.getSnapshot().session!.id, before = f.log.length;
  const context = {signal: new AbortController().signal, isCurrent: () => true};
  await assert.rejects(f.runtime.playFromScreen({libraryId: 'foreign', itemId: 'movie', startSeconds: 0}, context), /invalid_playback_target/);
  await assert.rejects(f.runtime.playFromScreen({libraryId: 'library', itemId: 'movie', startSeconds: 0, versionId: 'not-offered'}, context), /playback_version_unavailable/);
  assert.equal(f.runtime.getSnapshot().session!.id, session); assert.equal(f.video.paused, false);
  assert.ok(f.log.slice(before).every(request => request.method !== 'POST' && request.method !== 'DELETE'));
});

test('screen cancellation or navigation during real start cannot adopt late output or stop a newer Play', async t => {
  for (const change of ['abort', 'navigation', 'new-play'] as const) {
    const f = setup(t), gate = deferred(), abort = new AbortController(); let current = true, held = false;
    f.intercept(async (request, response) => {
      // Device-queue starts carry queue/entry IDs, not a top-level itemId.
      if (!held && request.method === 'POST' && request.path === '/v1/playback/sessions') {held = true;await gate.hold();}
      return response;
    });
    const pending = f.runtime.playFromScreen({libraryId: 'library', itemId: 'movie', startSeconds: 0}, {signal: abort.signal, isCurrent: () => current});
    let settled = false; void pending.then(() => {settled = true;}, () => {settled = true;});
    const done = assert.rejects(pending, undefined, change);
    await gate.started;
    assert.equal(settled, false, `${change}: the actual start remains in flight`);
    if (change === 'navigation') current = false;
    else if (change === 'new-play') {await f.runtime.play('newer', 20); await f.arrive();abort.abort();}
    else abort.abort();
    gate.resolve(); await done; await settle();
    assert.notEqual(f.runtime.getSnapshot().session?.itemId, 'movie');
    if (change === 'new-play') assert.equal(f.runtime.getSnapshot().session!.itemId, 'newer');
  }
});

function versionFixture(f: ReturnType<typeof setup>, resume = 120) {
  let positionMs: number | undefined = resume * 1000, offered = ['v2', 'v1'];
  const handle = async (request: V1Request, response: V1Response) => {
    if (!request.path.includes('/playback-options')) return response;
    const body = response.body as {versions: {id: string}[]; preferred: object};
    return {...response, body: {...body, resume: positionMs === undefined ? undefined : {positionMs, partIndex: 0},
      versions: offered.map(id => ({...body.versions[0], id})), preferred: {...body.preferred, versionId: 'v2'}}};
  };
  f.intercept(handle);
  return {handle, resume: (seconds: number | undefined) => { positionMs = seconds === undefined ? undefined : seconds * 1000; }, versions: (ids: string[]) => { offered = ids; }};
}

test('the owned chooser reads fresh File-info order and actual selected ID, then re-reads resume at the real chosen start', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  const fixture = versionFixture(f), abort = new AbortController(), before = f.log.length, loads = f.video.loads;
  const choices = await f.runtime.readVersions(fence, abort.signal); assert.ok(choices);
  assert.equal(choices.selectedVersionId, 'v1'); assert.equal(choices.options.preferred!.versionId, 'v2');
  assert.deepEqual(choices.versions.map(version => version.id), ['v2', 'v1']);
  assert.equal(choices.failedVersionId, undefined); assert.equal(choices.isCurrent(), true);
  assert.equal(f.video.loads, loads); assert.equal(f.log.slice(before).filter(request => request.method === 'POST').length, 0);
  assert.equal(f.log.at(-1)!.path, '/v1/items/movie/playback-options', 'chooser freshness is a real options read, without a preview substituting for the selected ID');
  fixture.resume(85);
  await f.runtime.playVersion({choices, versionId: 'v2', signal: abort.signal}); await f.arrive();
  assert.equal(choices.isCurrent(), false, 'accepted replacement invalidates old row facts before the promise settles');
  assert.equal(f.runtime.getSnapshot().session!.versionId, 'v2');
  assert.equal(f.hls.instances.at(-1)!.config.startPosition, 85);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 85);
  const chosen = f.log.findLast(request => request.method === 'POST' && request.path === '/v1/playback/sessions')!;
  assert.equal((chosen.body as {versionId: string}).versionId, 'v2');
  assert.equal((chosen.body as {startPositionMs: number}).startPositionMs, 85000);
  const nextPlay = f.log.length; await f.runtime.play('movie'); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.versionId, 'v1', 'the next ordinary Play returns to the actual server automatic choice');
  assert.ok(f.log.slice(nextPlay).every(request => !(request.body as {versionId?: string} | undefined)?.versionId));
});

test('a chosen version removed by the fresh click read cannot stop the still playing source', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  const fixture = versionFixture(f), signal = new AbortController().signal, choices = await f.runtime.readVersions(fence, signal); assert.ok(choices);
  fixture.versions(['v1']); const before = f.log.length, session = f.runtime.getSnapshot().session!.id;
  await assert.rejects(f.runtime.playVersion({choices, versionId: 'v2', signal}), /playback_version_unavailable/);
  assert.equal(f.runtime.getSnapshot().session!.id, session); assert.equal(f.video.paused, false);
  assert.equal(f.log.slice(before).filter(request => request.method === 'POST' || request.method === 'DELETE').length, 0);
});

test('the controlled chooser row adapter preserves actual filtered order and the original command authority', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  versionFixture(f); const signal = new AbortController().signal, choices = await f.runtime.readVersions(fence, signal); assert.ok(choices);
  const seen: object[] = [];
  // Controlled canonical-formatter boundary only. No claim that these test strings are the
  // not-yet-MAIN VS3e formatter or product copy; the adapter must preserve them exactly.
  const rows = playbackVersionRows(choices, version => { seen.push(version); return {heading: 'heading:' + version.id, detail: 'detail:' + version.id}; });
  assert.deepEqual(seen, choices.versions);
  assert.deepEqual(rows, [{id: 'v2', heading: 'heading:v2', detail: 'detail:v2'}, {id: 'v1', heading: 'heading:v1', detail: 'detail:v1'}]);
  assert.equal(Object.isFrozen(rows), true); assert.equal(Object.isFrozen(rows![0]), true);
  await f.runtime.playVersion({choices, versionId: rows![0]!.id, signal}); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.versionId, 'v2', 'the renderer retains the original WeakMap-issued object, without a copied choices wrapper');
  assert.equal(playbackVersionRows(choices, () => { assert.fail('a stale selection cannot invoke the formatter'); }), null);
});

test('row adaptation drops stale private facts, rejects changed order and preserves unknown empty content without placeholders', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  versionFixture(f); const choices = await f.runtime.readVersions(fence, new AbortController().signal); assert.ok(choices);
  const format = () => ({heading: 'heading', detail: 'detail'});
  assert.throws(() => playbackVersionRows({...choices, versions: [...choices.versions].reverse()}, format), /playback_version_rows_invalid/);
  assert.throws(() => playbackVersionRows({...choices, failedVersionId: 'v1'}, format), /playback_version_rows_invalid/);
  assert.deepEqual(playbackVersionRows(choices, () => ({heading: '', detail: ''})), choices.versions.map(version => ({id: version.id, heading: '', detail: ''})), 'unknown facts remain empty, without Version1 or other synthesized labels');
  const single = {...choices, failedVersionId: 'v1', versions: [choices.versions[0]!]};
  assert.equal(playbackVersionRows(single, format)!.length, 1, 'one structurally surviving row remains drawable; this test object is not command authority');
  const stale = {...choices, isCurrent: choices.isCurrent};
  assert.equal(playbackVersionRows(stale, () => { f.invalidate(); return format(); }), null, 'authority change during formatting cannot expose even the first completed row');
});

test('failure chooser excludes the actual failed grouped version, including when the planner prefers another version', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  versionFixture(f); const signal = new AbortController().signal;
  assert.equal(await f.runtime.readVersions(fence, signal, {excludeFailed: true}), null, 'a healthy source cannot be called the failed version');
  // A recoverable engine event is not a failed title: exhaust the real controller's three
  // in-place recovery plans before offering the failure destination.
  for (let attempt = 0; attempt < 3; attempt++) {
    f.hls.instances.at(-1)!.error({fatal: true, type: 'otherError', details: 'internalException'});
    await f.arrive();
  }
  const failedFence = f.runtime.getSnapshot().fence!;
  if (failedFence.kind !== 'queue') assert.fail('queue fence required');
  f.hls.instances.at(-1)!.error({fatal: true, type: 'otherError', details: 'internalException'});
  await settle(); assert.equal(f.runtime.getSnapshot().playback.phase, 'error');
  const choices = await f.runtime.readVersions(failedFence, signal, {excludeFailed: true}); assert.ok(choices);
  assert.equal(choices.selectedVersionId, 'v1'); assert.equal(choices.failedVersionId, 'v1');
  assert.equal(choices.options.preferred!.versionId, 'v2'); assert.deepEqual(choices.versions.map(version => version.id), ['v2']);
  await assert.rejects(f.runtime.playVersion({choices, versionId: 'v1', signal}), /playback_version_unavailable/);
  await f.runtime.playVersion({choices, versionId: 'v2', signal}); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.versionId, 'v2');
});

test('copied or foreign chooser facts cannot become a version command authority', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  versionFixture(f); const signal = new AbortController().signal, choices = await f.runtime.readVersions(fence, signal); assert.ok(choices);
  const before = f.log.length;
  await assert.rejects(f.runtime.playVersion({choices: {...choices}, versionId: 'v2', signal}), /stale_playback_command/);
  assert.equal(f.log.length, before, 'a forged structural object cannot even issue the freshness read');
  const other = setup(t); await other.runtime.play('movie'); await other.arrive();
  await assert.rejects(other.runtime.playVersion({choices, versionId: 'v2', signal}), /stale_playback_command/);
});

test('a new media generation retires chooser facts, and a fresh title without a resume starts at zero', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  const fixture = versionFixture(f), signal = new AbortController().signal, choices = await f.runtime.readVersions(fence, signal); assert.ok(choices);
  await f.runtime.setQuality(fence, '720p-high'); await f.arrive();
  assert.equal(choices.isCurrent(), false); await assert.rejects(f.runtime.playVersion({choices, versionId: 'v2', signal}), /stale_playback_command/);
  fixture.resume(undefined);
  await f.runtime.playVersion({itemId: 'movie', versionId: 'v2', context: selectionContext, signal}, () => true); await f.arrive();
  assert.equal(f.hls.instances.at(-1)!.config.startPosition, 0);
  assert.equal(f.runtime.getSnapshot().session!.versionId, 'v2');
});

test('a refused actual version start stays unaccepted until its original failure is cancelled', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  const fixture = versionFixture(f), signal = new AbortController().signal, choices = await f.runtime.readVersions(fence, signal); assert.ok(choices);
  f.intercept(async (request, response) => request.method === 'POST' && request.path === '/v1/playback/sessions' && (request.body as {versionId?: string})?.versionId === 'v2'
    ? {status: 422, headers: {}, body: {error: {code: 'unsupported_media', retry: 'never', requestId: 'request'}}} : fixture.handle(request, response));
  type StartFailure = NonNullable<ReturnType<typeof f.runtime.getSnapshot>['startFailure']>;
  let off = () => {}, rejectPublication!: (error: unknown) => void;
  const published = new Promise<StartFailure>((resolve, reject) => {
    rejectPublication = reject;
    off = f.runtime.subscribe(() => {
      const failure = f.runtime.getSnapshot().startFailure;
      if (failure) {off(); resolve(failure);}
    });
  });
  const engines = f.hls.instances.length;
  const starts = () => f.log.filter(request => request.method === 'POST' && request.path === '/v1/playback/sessions'
    && (request.body as {versionId?: string})?.versionId === 'v2');
  let settled = false;
  const pending = f.runtime.playVersion({choices, versionId: 'v2', signal});
  // Observe the original promise before any cancellation or fixture cleanup.
  void pending.then(() => {settled = true; rejectPublication(new Error('A refused version was accepted'));}, error => {settled = true; rejectPublication(error);});
  const rejected = assert.rejects(pending, /stale_playback_command/);
  try {
    const failure = await published;
    assert.equal(failure.code, 'unsupported_media'); assert.equal(failure.retryable, false); assert.equal(failure.isCurrent(), true);
    assert.equal(settled, false, 'the original first-start failure remains owned while its failure is drawn');
    assert.equal(choices.isCurrent(), false);
    assert.notEqual(f.runtime.getSnapshot().session?.versionId, 'v2');
    assert.equal(f.runtime.getSnapshot().fence, undefined, 'a refused version supplies no admitted playback fence');
    assert.equal(f.hls.instances.length, engines, 'no v2 media engine is created or adopted');
    assert.equal(starts().length, 1);
    await assert.rejects(failure.retry(), error => error instanceof Error && error.name === 'AbortError');
    assert.equal(starts().length, 1, 'a permanent refusal has no retry authority');
    failure.cancel();
    await rejected;
    assert.equal(failure.isCurrent(), false); assert.equal(f.runtime.getSnapshot().startFailure, undefined);
    assert.notEqual(f.runtime.getSnapshot().session?.versionId, 'v2'); assert.equal(f.runtime.getSnapshot().fence, undefined);
    assert.equal(f.hls.instances.length, engines); assert.equal(starts().length, 1);
  } finally {off(); f.runtime.dispose(); await rejected;}
});

test('late chooser reads and click reads cannot cross abort, source replacement or authority retirement', async t => {
  for (const stage of ['read', 'click'] as const) for (const change of ['abort', 'new-play', 'authority'] as const) {
    const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
    if (fence.kind !== 'queue') assert.fail('queue fence required');
    versionFixture(f); const abort = new AbortController(), choices = stage === 'click' ? await f.runtime.readVersions(fence, abort.signal) : undefined;
    const gate = deferred(); f.intercept(async (request, response) => { if (request.path === '/v1/items/movie/playback-options') await gate.hold(); return response; });
    const pending = stage === 'click' ? f.runtime.playVersion({choices: choices!, versionId: 'v1', signal: abort.signal}) : f.runtime.readVersions(fence, abort.signal);
    const done = pending.then(value => { assert.equal(stage, 'read'); assert.equal(value, null); }, () => {});
    await gate.started; const before = f.log.length;
    if (change === 'abort') abort.abort(); else if (change === 'authority') f.invalidate(); else { await f.runtime.play('newer', 20); await f.arrive(); }
    gate.resolve(); await done; await settle();
    assert.equal(f.log.slice(before).some(request => request.method === 'POST' && (request.body as {versionId?: string})?.versionId), false);
    if (change === 'new-play') assert.equal(f.runtime.getSnapshot().playback.itemId, 'newer');
  }
});

test('accepted chooser replacement drops old facts while its actual start remains in flight; abort cannot stop a newer Play', async t => {
  const f = setup(t); await f.runtime.play('movie', 25); await f.arrive(); const fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  const fixture = versionFixture(f), abort = new AbortController(), choices = await f.runtime.readVersions(fence, abort.signal); assert.ok(choices);
  const gate = deferred(); f.intercept(async (request, response) => { if (request.method === 'POST' && request.path === '/v1/playback/sessions' && (request.body as {versionId?: string})?.versionId === 'v2') await gate.hold(); return fixture.handle(request, response); });
  const pending = f.runtime.playVersion({choices, versionId: 'v2', signal: abort.signal}), done = assert.rejects(pending);
  await gate.started; assert.equal(choices.isCurrent(), false);
  await f.runtime.play('newer', 20); await f.arrive(); const newer = f.runtime.getSnapshot().session!.id;
  abort.abort(); gate.resolve(); await done; await settle();
  assert.equal(f.runtime.getSnapshot().session!.id, newer); assert.equal(f.video.paused, false);
});

test('a changed genuine entry context abandons its late version POST before any engine adoption', async t => {
  const f=setup(t),fixture=versionFixture(f),gate=deferred();let current=true,created='';
  f.intercept(async(request,response)=>{
    if(request.method==='POST'&&request.path==='/v1/playback/sessions'&&(request.body as {versionId?:string})?.versionId==='v2'){created=(response.body as {id:string}).id;await gate.hold();}
    return fixture.handle(request,response);
  });
  const pending=f.runtime.playVersion({itemId:'movie',versionId:'v2',context:selectionContext,signal:new AbortController().signal},()=>current),rejected=assert.rejects(pending,/stale_selection_context|playback_version_failed/);
  await gate.started;assert.ok(created);current=false;gate.resolve();await rejected;await settle();
  assert.equal(f.hls.instances.length,0,'the obsolete genuine response never reaches a media engine');
  assert.equal(f.server.session(created)?.ended,true);assert.equal(f.log.some(request=>request.method==='DELETE'&&request.path===`/v1/playback/sessions/${created}`),true);
});

test('the original chooser read lifetime can cancel an in-flight version command independently of its action signal', async t => {
  const f=setup(t);await f.runtime.play('movie',25);await f.arrive();const fence=f.runtime.getSnapshot().fence!;if(fence.kind!=='queue')assert.fail('queue fence required');
  const fixture=versionFixture(f),read=new AbortController(),action=new AbortController(),choices=await f.runtime.readVersions(fence,read.signal);assert.ok(choices);
  const gate=deferred();let created='';f.intercept(async(request,response)=>{
    if(request.method==='POST'&&request.path==='/v1/playback/sessions'&&(request.body as {versionId?:string})?.versionId==='v2'){created=(response.body as {id:string}).id;await gate.hold();}
    return fixture.handle(request,response);
  });
  const engines=f.hls.instances.length,pending=f.runtime.playVersion({choices,versionId:'v2',signal:action.signal}),rejected=assert.rejects(pending);
  await gate.started;assert.equal(choices.isCurrent(),false,'intentional output replacement is allowed while the original read controller remains alive');
  read.abort();assert.equal(action.signal.aborted,false);gate.resolve();await rejected;await settle();
  assert.ok(created);assert.equal(f.server.session(created)?.ended,true);assert.equal(f.runtime.getSnapshot().session,undefined);assert.equal(f.hls.instances.length,engines,'the late response creates no new media engine; ordinary teardown may clear the old element');
});

test('unavailable or mismatched version offer never stops the existing playback or starts a source link', async t => {
  const f = setup(t); await f.runtime.play('current', 15); await f.arrive();
  const session = f.runtime.getSnapshot().session!.id, before = f.log.length;
  await assert.rejects(f.runtime.playVersion({itemId: 'movie', versionId: 'raw_source_link', context: selectionContext, signal: new AbortController().signal}, () => true), /playback_version_unavailable/);
  assert.equal(f.runtime.getSnapshot().session!.id, session);
  assert.equal(f.log.slice(before).some(request => request.method === 'POST'), false);
  f.intercept(async (request, response) => request.path === '/v1/items/movie/playback-options' ? {...response, body: {...response.body as object, itemId: 'another'}} : response);
  await assert.rejects(f.runtime.playVersion({itemId: 'movie', versionId: 'v1', context: selectionContext, signal: new AbortController().signal}, () => true), /playback_version_unavailable/);
  assert.equal(f.runtime.getSnapshot().session!.id, session);
});

test('version choice validates current route/viewer and cancellation after its real options read', async t => {
  for (const change of ['context', 'abort', 'other-play'] as const) {
    const f = setup(t), gate = deferred(), abort = new AbortController(); let current = true;
    f.intercept(async (request, response) => { if (request.path === '/v1/items/movie/playback-options') await gate.hold(); return response; });
    const pending = f.runtime.playVersion({itemId: 'movie', versionId: 'v1', context: selectionContext, signal: abort.signal}, () => current);
    // Attach rejection before releasing any asynchronous cancellation.
    const rejected = assert.rejects(pending);
    await gate.started;
    if (change === 'context') current = false;
    else if (change === 'abort') abort.abort();
    else { await f.runtime.play('newer', 40); await f.arrive(); }
    gate.resolve(); await rejected; await settle();
    assert.equal(f.log.some(request => request.method === 'POST' && (request.body as {itemId?: string})?.itemId === 'movie'), false);
    if (change === 'other-play') assert.equal(f.runtime.getSnapshot().playback.itemId, 'newer');
  }
  const f = setup(t);
  await assert.rejects(f.runtime.playVersion({itemId: 'movie', versionId: 'v1', context: {...selectionContext, scope: {...selectionContext.scope, profileId: 'other'}}, signal: new AbortController().signal}, () => true), /stale_selection_context/);
  assert.equal(f.log.some(request => request.path.includes('/items/') || request.path.includes('/playback/sessions') || request.path === '/v1/queues'), false, 'a wrong viewer cannot read title options or start playback; background capability publication is unrelated');
});

test('runtime Home play reaches the actual queue/service/element and publishes the real source fence', async t => {
  const f = setup(t); await f.runtime.play('movie', 12.5); const engine = await f.arrive();
  const snapshot = f.runtime.getSnapshot(), fence = snapshot.fence;
  assert.equal(snapshot.playback.phase, 'ready');
  assert.equal(snapshot.playback.positionSeconds, 12.5);
  assert.ok(fence);
  if (fence.kind !== 'queue') assert.fail('queue fence required');
  assert.equal(fence.sessionId, snapshot.playback.session!.id);
  assert.equal(fence.sourceId, 'source_movie');
  assert.notEqual(fence.sourceId, snapshot.session!.versionId);
  assert.equal(fence.entryId, snapshot.session!.queue!.entryId);
  assert.equal(snapshot.subject!.kind, 'video');
  assert.equal(engine.config.startPosition, 12.5);
  assert.ok(f.log.some(r => r.method === 'POST' && r.path === '/v1/queues'));
  f.video.advance(18.5); await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.positionSeconds, 18.5);
  const session = f.server.session(fence.sessionId);
  assert.ok(session);
  // A media reading updates the presentation immediately; the server hears the latest reading
  // on the next observation, within a second on the injected clock.
  f.clock.advance(1000); await settle();
  assert.ok(session.reports.some(report => report.positionMs === 18500 && report.generation === fence.generation && report.state === 'playing'));
});

test('runtime collapse and expansion preserve the same element, network and current position', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); const engine = await f.arrive();
  const fence = f.runtime.getSnapshot().fence!, loads = f.video.loads, listeners = f.video.listenerCount();
  f.runtime.collapse(fence); f.video.advance(24); await settle(); f.runtime.open(fence);
  assert.equal(f.video.loads, loads); assert.equal(f.video.listenerCount(), listeners);
  assert.equal(engine.destroyed, false); assert.equal(f.hls.instances.length, 1);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 24);
});

test('runtime commands pause and resume the real element; seek waits for the actual landing', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const fence = f.runtime.getSnapshot().fence!;
  await f.runtime.pause(fence); assert.equal(f.video.paused, true);
  await f.runtime.resume(fence); assert.equal(f.video.paused, false);
  await f.runtime.seek(fence, 60); assert.equal(f.runtime.getSnapshot().playback.pendingSeek!.positionSeconds, 60);
  assert.notEqual(f.runtime.getSnapshot().playback.positionSeconds, 60);
  f.video.land(60); await settle();
  assert.equal(f.runtime.getSnapshot().playback.pendingSeek, undefined);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 60);
});

test('dock completion waits for the actual end receipt and Replay restarts the fenced queue occurrence', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const fence = f.runtime.getSnapshot().fence!, queue = f.runtime.getSnapshot().session!.queue!, held = deferred();
  f.intercept(async (request, response) => {
    if (request.path.endsWith('/observation') && (request.body as {state?: string})?.state === 'ended') await held.hold();
    return response;
  });
  f.video.finish(); await held.started;
  assert.equal(f.runtime.getSnapshot().playback.phase, 'ended');
  const pending = f.runtime.getSnapshot().presentation!;
  held.resolve(); await settle();
  assert.equal(pending.finished, false, 'a raw media end is provisional while the queue confirms it');
  assert.equal(pending.controls.includes('replay'), false);
  assert.equal(f.runtime.getSnapshot().presentation!.finished, true);
  assert.ok(f.runtime.getSnapshot().presentation!.controls.includes('replay'));
  assert.ok(f.server.session(fence.sessionId)!.reports.some(report => report.state === 'ended'));
  const before = f.log.length;
  await f.runtime.onAction({kind: 'replay', fence}); await f.arrive();
  const replayed = f.runtime.getSnapshot();
  assert.notEqual(replayed.fence!.sessionId, fence.sessionId);
  assert.deepEqual(replayed.session!.queue, queue);
  assert.equal(replayed.playback.positionSeconds, 0);
  assert.equal(replayed.presentation!.finished, false);
  assert.equal(f.hls.instances.at(-1)!.config.startPosition, 0);
  assert.equal(f.log.slice(before).filter(request => request.method === 'POST' && request.path === '/v1/queues').length, 0);
  await assert.rejects(f.runtime.onAction({kind: 'replay', fence}), /stale_playback_command/);
});

test('dock completion never offers final Replay during the actual next-episode countdown or advance', async t => {
  const f = setup(t), port = f.runtime.selectionPlayback(() => true);
  await port.play({context: selectionContext, items: [selectionLeaf('episode-first'), selectionLeaf('episode-next')], signal: new AbortController().signal});
  await f.arrive();
  const first = f.runtime.getSnapshot().fence!, seen: {finished: boolean; replay: boolean}[] = [], held = deferred();
  const off = f.runtime.subscribe(() => {
    const current = f.runtime.getSnapshot().presentation;
    if (current?.fence.sessionId === first.sessionId) seen.push({finished: current.finished, replay: current.controls.includes('replay')});
  });
  t.after(off);
  f.intercept(async (request, response) => { if (request.path.endsWith(':advance')) await held.hold(); return response; });
  f.video.finish(); await settle();
  assert.equal(f.runtime.getSnapshot().playback.phase, 'ended');
  assert.equal(f.runtime.getSnapshot().queueView!.next.available, true);
  f.clock.advance(10_250); await settle();
  assert.ok(f.log.some(request => request.path.endsWith(':advance')), 'the actual core queue must advance automatically');
  await held.started;
  const during = f.runtime.getSnapshot().presentation!;
  held.resolve(); await f.arrive();
  assert.equal(during.finished, false);
  assert.ok(seen.length > 0); assert.ok(seen.every(value => !value.finished && !value.replay));
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'episode-next');
  assert.notEqual(f.runtime.getSnapshot().fence!.sessionId, first.sessionId);
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false);
  assert.equal(f.video.paused, false);
});

test('dock completion keeps real listening auto-advance and marks only the confirmed final item', async t => {
  const f = setup(t), port = f.runtime.selectionPlayback(() => true);
  await port.play({context: selectionContext, items: [selectionLeaf('song-first'), selectionLeaf('song-last')], signal: new AbortController().signal});
  await f.arrive();
  const first = f.runtime.getSnapshot().fence!, finishedSessions: string[] = [];
  const off = f.runtime.subscribe(() => { const p = f.runtime.getSnapshot().presentation; if (p?.finished) finishedSessions.push(p.fence.sessionId); });
  t.after(off);
  f.video.finish(); await f.arrive();
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'song-last');
  assert.ok(!finishedSessions.includes(first.sessionId), 'an advancing track must never advertise final Replay');
  const last = f.runtime.getSnapshot().fence!;
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false, 'being the last queue entry is not completion');
  f.video.finish(); await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.finished, true);
  assert.ok(finishedSessions.includes(last.sessionId));
});

test('E-web-14: a music queue start whose offers carry an unoffered "limit" quality still publishes a dock presentation', async t => {
  const f = setup(t), port = f.runtime.selectionPlayback(() => true);
  f.intercept(async (request, response) => {
    if (request.path.includes('/playback-offers?')) {
      const body = response.body as {current: Record<string, unknown>};
      return {...response, body: {...body, current: {...body.current, qualityId: 'limit'}}};
    }
    return response;
  });
  await port.play({context: selectionContext, items: [selectionLeaf('song-first'), selectionLeaf('song-last')], signal: new AbortController().signal});
  await f.arrive();
  const snapshot = f.runtime.getSnapshot();
  assert.ok(snapshot.fence, 'the queue fence exists');
  assert.ok(snapshot.presentation, 'the dock presentation is published');
  assert.equal(snapshot.playback.itemId, 'song-first');
});

test('dock completion with zero countdown advances only after the real controller publishes its held completion', async t => {
  const f = setup(t);
  f.intercept(async (request, response) => request.method === 'GET' && /^\/v1\/queues\/[^/]+$/.test(request.path)
    ? {...response, body: {...response.body as object, postPlay: {autoplay: true, countdownMs: 0}}} : response);
  await f.runtime.selectionPlayback(() => true).play({context: selectionContext, items: [selectionLeaf('first'), selectionLeaf('next')], signal: new AbortController().signal});
  await f.arrive(); const first = f.runtime.getSnapshot().fence!;
  f.video.finish(); await f.arrive();
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'next');
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false);
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 1);
  await assert.rejects(f.runtime.onAction({kind: 'replay', fence: first}), /stale_playback_command/);
});

test('dock completion with autoplay disabled offers Replay after confirmation and does not advance', async t => {
  const f = setup(t);
  f.intercept(async (request, response) => request.method === 'GET' && /^\/v1\/queues\/[^/]+$/.test(request.path)
    ? {...response, body: {...response.body as object, postPlay: {autoplay: false, countdownMs: 0}}} : response);
  await f.runtime.selectionPlayback(() => true).play({context: selectionContext, items: [selectionLeaf('first'), selectionLeaf('next')], signal: new AbortController().signal});
  await f.arrive(); f.video.finish(); await settle(); f.clock.advance(15_000); await settle();
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'first');
  assert.equal(f.runtime.getSnapshot().presentation!.finished, true);
  assert.ok(f.runtime.getSnapshot().presentation!.controls.includes('replay'));
  assert.equal(f.log.filter(request => request.path.endsWith(':advance')).length, 0);
});

test('dock completion restores the actual parked listening queue paused only after confirmed final video end', async t => {
  const f = setup(t); await f.runtime.play('song', 35); await f.arrive();
  const originalQueue = f.runtime.getSnapshot().session!.queue!.queueId;
  await f.runtime.play('movie', 20); await f.arrive();
  const held = deferred();
  f.intercept(async (request, response) => {
    if (request.path.endsWith('/observation') && (request.body as {state?: string})?.state === 'ended') await held.hold();
    return response;
  });
  f.video.finish(); await held.started;
  const provisional = f.runtime.getSnapshot();
  held.resolve(); await f.arrive();
  assert.equal(provisional.playback.itemId, 'movie');
  assert.equal(provisional.presentation!.finished, false);
  const restored = f.runtime.getSnapshot();
  assert.equal(restored.playback.itemId, 'song');
  assert.equal(restored.session!.queue!.queueId, originalQueue);
  assert.equal(restored.playback.positionSeconds, 35);
  assert.equal(restored.playback.intent, 'paused'); assert.equal(f.video.paused, true);
  assert.equal(restored.presentation!.finished, false);
});

test('dock completion does not infer an end from duration, pause, failure, or a stale replacement event', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const fence = f.runtime.getSnapshot().fence!;
  f.video.advance(f.video.duration); await f.runtime.pause(fence); await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false);
  assert.equal(f.runtime.getSnapshot().presentation!.controls.includes('replay'), false);
  await f.runtime.play('replacement', 12); await f.arrive();
  const replacement = f.runtime.getSnapshot().fence!;
  f.video.fire('ended'); await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false);
  assert.equal(f.runtime.getSnapshot().fence!.sessionId, replacement.sessionId);
  f.video.fail(3); await settle();
  assert.notEqual(f.runtime.getSnapshot().presentation?.finished, true);
  await assert.rejects(f.runtime.onAction({kind: 'replay', fence}), /stale_playback_command/);
});

test('dock completion ignores an old confirmed end after replacement and authority retirement', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const held = deferred();
  f.intercept(async (request, response) => {
    if (request.path.endsWith('/observation') && (request.body as {state?: string})?.state === 'ended') await held.hold();
    return response;
  });
  f.video.finish(); await held.started;
  await f.runtime.play('replacement', 12); await f.arrive();
  const replacement = f.runtime.getSnapshot().fence!;
  held.resolve(); await settle();
  assert.equal(f.runtime.getSnapshot().fence!.sessionId, replacement.sessionId);
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false);
  f.invalidate(); f.video.fire('volumechange');
  assert.equal(f.runtime.getSnapshot().disposed, true);
  assert.equal(f.runtime.getSnapshot().presentation, undefined);
  assert.deepEqual(f.runtime.getSnapshot().presentations, []);
});

test('dock completion never reinterprets a live-source end as a queue Replay', async t => {
  const f = setup(t);
  await f.runtime.selectionPlayback(() => true).playChannel(homeChannel, selectionContext, new AbortController().signal);
  await f.arriveLive(); const fence = f.runtime.getSnapshot().fence!;
  f.video.finish(); await settle();
  assert.notEqual(f.runtime.getSnapshot().presentation?.finished, true);
  assert.equal(f.runtime.getSnapshot().presentation?.controls.includes('replay'), false);
  await assert.rejects(f.runtime.onAction({kind: 'replay', fence}), /playback_command_unavailable/);
});

test('dock completion actual renderer Replay dispatches through the shell presentation to the fenced runtime API', async t => {
  // Execute the unchanged real Transport/Control callbacks. Leaf icons/artwork are not part
  // of this controlled event test; it makes no React DOM, browser or pixel claim.
  const loader = registerHooks({load(url, context, next) {
    const leaf = url.endsWith('/kit/Icon.tsx') ? 'Icon' : url.endsWith('/kit/artwork/index.ts') ? 'Artwork' : url.endsWith('/kit/controls/Slider.tsx') ? 'Slider' : url.endsWith('/kit/controls/Button.tsx') ? 'Button' : null;
    if (leaf) return {format: 'module', source: `export const ${leaf} = '${leaf}';`, shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  t.after(() => loader.deregister());
  const {Transport} = await import('../src/kit/dock/parts.tsx');
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  f.runtime.collapse(f.runtime.getSnapshot().fence!); f.video.finish(); await settle();
  const state = f.runtime.getSnapshot();
  const presentation = playbackPresentation(state, {t: id => id}, {foreground: () => null, ambient: () => null} as never, {back: 10, forward: 30})!;
  const view = dockView(state.dock, [presentation])!, actions: DockAction[] = [];
  let result: Promise<void> | undefined;
  const tree = Transport({view, binding: {getSnapshot: f.runtime.dock.getSnapshot, onAction: action => { actions.push(action); result = f.runtime.onAction(action); }}});
  const replay = (tree.props.children as any[]).find(child => child?.props?.primary);
  assert.equal(replay.props.label, 'Replay'); assert.equal(replay.props.disabled, false);
  replay.props.onClick(); assert.ok(result); await result; await f.arrive();
  assert.deepEqual(actions, [{kind: 'replay', fence: state.fence}]);
  assert.notEqual(f.runtime.getSnapshot().fence!.sessionId, state.fence!.sessionId);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 0);
  replay.props.onClick(); assert.equal(actions.length, 1, 'the old rendered callback is rejected after actual replacement');
});

test('dock completion cannot publish Finished when the actual end report fails', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  f.intercept(async (request, response) => request.path.endsWith('/observation') && (request.body as {state?: string})?.state === 'ended'
    ? {status: 503, headers: {}, body: {error: {code: 'temporarily_unavailable'}}} : response);
  f.video.finish(); await settle();
  assert.equal(f.runtime.getSnapshot().playback.phase, 'ended');
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false);
  assert.equal(f.runtime.getSnapshot().presentation!.controls.includes('replay'), false);
});

test('runtime rejects an old source callback after replacement and releases the previous network', async t => {
  const f = setup(t); await f.runtime.play('first', 10); const old = await f.arrive(), fence = f.runtime.getSnapshot().fence!, count = f.video.listenerCount();
  await f.runtime.play('second', 30); await f.arrive();
  assert.equal(old.destroyed, true); assert.equal(f.video.listenerCount(), count);
  await assert.rejects(f.runtime.seek(fence, 90), /stale_playback_command/);
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'second');
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 30);
});

test('runtime parks music with its queue and restores it through an actual fresh paused session', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  f.video.advance(35); await settle();
  const audio = f.runtime.getSnapshot(), queueId = audio.session!.queue!.queueId;
  await f.runtime.play('movie', 5); await f.arrive();
  const video = f.runtime.getSnapshot();
  assert.equal(video.dock.parkedAudio!.positionSeconds, 35);
  assert.equal(video.dock.parkedAudio!.queue, null, 'v1 occurrences are not fabricated into the strict player-core queue snapshot');
  assert.ok(video.presentations.some(p => p.subject.kind === 'music' && p.positionSeconds === 35 && p.playing === false));
  const plays = f.video.plays;
  await f.runtime.stop(video.fence!); await f.arrive();
  const restored = f.runtime.getSnapshot();
  assert.equal(restored.subject!.kind, 'music');
  assert.equal(restored.playback.intent, 'paused'); assert.equal(f.video.paused, true);
  assert.equal(f.video.plays, plays, 'the actual web adapter never calls media play during paused restoration');
  assert.equal(restored.playback.positionSeconds, 35);
  assert.equal(restored.session!.queue!.queueId, queueId);
  assert.notEqual(restored.session!.id, audio.session!.id);
  const starts = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions');
  assert.equal((starts.at(-1)!.body as {state: string}).state, 'paused');
});

test('runtime restores the original parked queue after live playback without creating a replacement queue', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  f.video.advance(35); await settle();
  const audio = f.runtime.getSnapshot(), bookmark = audio.fence, queue = audio.session?.queue;
  assert.ok(bookmark); assert.ok(queue);
  if (bookmark.kind !== 'queue') assert.fail('queue fence required');
  await f.runtime.selectionPlayback(() => true).playChannel(homeChannel, selectionContext, new AbortController().signal);
  const liveEngine = await f.arriveLive(), live = f.runtime.getSnapshot().fence;
  assert.ok(live); assert.equal(live.kind, 'channel');
  assert.equal(f.runtime.getSnapshot().dock.parkedAudio?.fence.sessionId, bookmark.sessionId);
  // A channel does not create a device queue. This exercises the actual adapter's restore
  // path independently of the server lifecycle of a queue replaced by another queue Play.
  assert.ok(f.server.queue(queue.queueId));
  const before = f.log.length, plays = f.video.plays;
  await f.runtime.stop(live); await f.arrive();
  const restored = f.runtime.getSnapshot(), accepted = restored.fence;
  assert.ok(accepted);
  if (accepted.kind !== 'queue') assert.fail('queue fence required');
  assert.notEqual(accepted.sessionId, bookmark.sessionId);
  assert.equal(accepted.entryId, bookmark.entryId); assert.equal(accepted.itemId, bookmark.itemId);
  assert.equal(restored.session?.queue?.queueId, queue.queueId);
  assert.equal(restored.playback.positionSeconds, 35);
  assert.equal(restored.playback.intent, 'paused'); assert.equal(f.video.paused, true);
  assert.equal(f.video.plays, plays); assert.equal(liveEngine.destroyed, true);
  const calls = f.log.slice(before);
  assert.equal(calls.some(request => request.path === '/v1/queues' && request.method === 'POST'), false);
  const starts = calls.filter(request => request.path === '/v1/playback/sessions' && request.method === 'POST');
  assert.equal(starts.length, 1);
  assert.deepEqual(starts[0]?.body, {queue: {queueId: queue.queueId, entryId: bookmark.entryId}, state: 'paused', startPositionMs: 35000});
});

test('runtime refuses a deleted parked queue without inventing another queue or starting media', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  const audio = f.runtime.getSnapshot(), bookmark = audio.fence, queue = audio.session?.queue;
  assert.ok(bookmark); assert.ok(queue);
  await f.runtime.selectionPlayback(() => true).playChannel(homeChannel, selectionContext, new AbortController().signal);
  await f.arriveLive();
  await f.api.request(`/v1/queues/${queue.queueId}`, 'DELETE');
  assert.equal(f.server.queue(queue.queueId), undefined);
  const before = f.log.length, plays = f.video.plays, engines = f.hls.instances.length;
  await assert.rejects(f.runtime.restoreBookmark(bookmark, true), /queue_not_found/);
  assert.equal(f.video.paused, true); assert.equal(f.video.plays, plays);
  assert.equal(f.hls.instances.length, engines);
  assert.equal(f.log.slice(before).some(request => request.method === 'POST'), false);
  assert.equal(f.runtime.getSnapshot().fence, undefined);
});

test('runtime retains all three actual dock queue candidates and retires superseded extras on the next Play', async t => {
  const f = setup(t); await f.runtime.play('movie_a', 5); await f.arrive();
  const first = f.runtime.getSnapshot(), videoA = first.fence, queueA = first.session?.queue;
  assert.ok(videoA); assert.ok(queueA);
  await f.runtime.play('song_b', 10); await f.arrive();
  const queueB = f.runtime.getSnapshot().session?.queue;
  assert.ok(queueB);
  await f.runtime.play('movie_c', 15); await f.arrive();
  const queueC = f.runtime.getSnapshot().session?.queue;
  assert.ok(queueC);
  assert.equal(f.runtime.getSnapshot().dock.resumeVideo?.fence.sessionId, videoA.sessionId);
  assert.equal(f.runtime.getSnapshot().dock.parkedAudio?.kind, 'music');
  await f.runtime.play('movie_d', 20); await f.arrive();
  const queueD = f.runtime.getSnapshot().session?.queue, createD = f.log.filter(request => request.path === '/v1/queues' && request.method === 'POST').at(-1);
  assert.ok(queueD); assert.ok(createD);
  assert.deepEqual(createD.body, {owner: 'device', segments: [{source: {items: {ids: ['movie_d']}}}], retainQueueIds: [queueC.queueId, queueB.queueId, queueA.queueId]});
  for (const queue of [queueA, queueB, queueC, queueD]) assert.ok(f.server.queue(queue.queueId));
  await f.runtime.play('movie_e', 25); await f.arrive();
  const createE = f.log.filter(request => request.path === '/v1/queues' && request.method === 'POST').at(-1);
  assert.ok(createE);
  assert.deepEqual(createE.body, {owner: 'device', segments: [{source: {items: {ids: ['movie_e']}}}], retainQueueIds: [queueD.queueId, queueB.queueId, queueA.queueId]});
  assert.equal(f.server.queue(queueC.queueId), undefined, 'an old current queue is not retained once no actual dock bookmark names it');
  const plays = f.video.plays, restored = await f.runtime.restoreBookmark(videoA, false);
  await f.arrive();
  assert.equal(f.runtime.getSnapshot().session?.queue?.queueId, queueA.queueId);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 5);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused'); assert.equal(f.video.plays, plays);
  await f.runtime.stop(restored); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session?.queue?.queueId, queueB.queueId);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 10);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused'); assert.equal(f.video.plays, plays);
});

test('runtime keeps an offered parked bookmark through more Plays than the private bookmark cache bound', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  f.video.advance(35); await settle();
  const audio = f.runtime.getSnapshot(), bookmark = audio.fence, queue = audio.session?.queue;
  assert.ok(bookmark); assert.ok(queue);
  for (let index = 0; index < 9; index++) { await f.runtime.play(`movie_${index}`, 5); await f.arrive(); }
  const current = f.runtime.getSnapshot().fence, plays = f.video.plays;
  assert.ok(current);
  assert.equal(f.runtime.getSnapshot().dock.parkedAudio?.fence.sessionId, bookmark.sessionId);
  await f.runtime.stop(current); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session?.queue?.queueId, queue.queueId);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 35);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused'); assert.equal(f.video.plays, plays);
});

test('runtime resets a session speed on a new play and rejects unsupported remote commands', async t => {
  const f = setup(t); await f.runtime.play('first', 5); await f.arrive();
  f.runtime.setSpeed(f.runtime.getSnapshot().fence!, 1.5); assert.equal(f.video.playbackRate, 1.5);
  await f.runtime.play('second', 5); await f.arrive(); assert.equal(f.video.playbackRate, 1);
  await assert.rejects(f.runtime.onAction({kind: 'play', fence: f.runtime.getSnapshot().fence!, remote: {id: 'unbound', sequence: '1'}}), /remote_playback_binding_unavailable/);
});

test('speed shortcuts use only the current subject choices and preserve the actual playback session', async t => {
  const f = setup(t); await f.runtime.play('movie', 5); await f.arrive();
  const fence = f.runtime.getSnapshot().fence!, sessionId = fence.sessionId, loads = f.video.loads;
  assert.deepEqual(f.runtime.speedChoices(fence), [.5,.75,1,1.25,1.5,1.75,2]);
  await f.runtime.onAction({kind:'step-speed',fence,direction:-1});
  assert.equal(f.video.playbackRate,.75); assert.equal(f.runtime.getSnapshot().playback.audioEffects.rate,.75);
  await f.runtime.onAction({kind:'step-speed',fence,direction:1}); assert.equal(f.video.playbackRate,1);
  for (let i=0;i<8;i++) f.runtime.stepSpeed(fence,1);
  assert.equal(f.video.playbackRate,2); assert.equal(f.runtime.getSnapshot().session?.id,sessionId); assert.equal(f.video.loads,loads);
  for (const rate of [NaN,Infinity,.3,2.5,0,-1]) assert.throws(()=>f.runtime.setSpeed(fence,rate),/playback_speed_unavailable/);
  f.runtime.collapse(fence); assert.throws(()=>f.runtime.stepSpeed(fence,-1),/playback_speed_unavailable/); f.runtime.open(fence);
  await f.runtime.play('book_file',5); await f.arrive();
  const book=f.runtime.getSnapshot().fence!; assert.equal(f.runtime.speedChoices(book).at(-1),3);
  f.runtime.setSpeed(book,2); f.runtime.stepSpeed(book,1); assert.equal(f.video.playbackRate,2.5);
  assert.throws(()=>f.runtime.stepSpeed(fence,-1),/stale_playback_command/);
  await f.runtime.play('song_one',5); await f.arrive();
  const song=f.runtime.getSnapshot().fence!; assert.deepEqual(f.runtime.speedChoices(song),[]);
  assert.throws(()=>f.runtime.stepSpeed(song,1),/playback_speed_unavailable/); assert.equal(f.video.playbackRate,1);
});

test('an ordinary browser without a real frame primitive never substitutes a seek or an assumed frame rate', async t => {
  const f=setup(t); await f.runtime.play('movie',5);await f.arrive();const fence=f.runtime.getSnapshot().fence!;
  await f.runtime.pause(fence);await settle();const seeks=f.video.seeks.length,position=f.runtime.getSnapshot().playback.positionSeconds;
  assert.equal(f.runtime.canStepFrame(fence),false);
  await assert.rejects(f.runtime.onAction({kind:'step-frame',fence,direction:1}),/playback_frame_step_unavailable/);
  assert.equal(f.video.seeks.length,seeks);assert.equal(f.runtime.getSnapshot().playback.positionSeconds,position);
});

test('paused frame shortcuts call only the actual loaded generation and publish only the engine observation', async t => {
  const {WebMediaPlayer}=await import('../src/platform/playback/web-media-player.ts');
  const calls:{direction:number;generation:number}[]=[];
  const f=setup(t,{}, {createRender:null,subtitleSurface:false,createPlayer:(video,options)=>{
    const player=new WebMediaPlayer(video,options);
    return Object.assign(player,{stepFrame:(direction:-1|1,generation:number)=>{
      assert.equal(player.loaded,generation);assert.equal(video.paused,true);calls.push({direction,generation});
      // The controlled decoder reports an actual irregular PTS, never runtime 1/fps arithmetic.
      f.video.advance(direction===1?5.037:4.991);
    }});
  }});
  await f.runtime.play('movie',5);await f.arrive();const fence=f.runtime.getSnapshot().fence!;
  assert.equal(f.runtime.canStepFrame(fence),false);await assert.rejects(f.runtime.stepFrame(fence,1),/playback_frame_step_unavailable/);
  await f.runtime.pause(fence);await settle();assert.equal(f.runtime.canStepFrame(fence),true);const seeks=f.video.seeks.length;
  await f.runtime.onAction({kind:'step-frame',fence,direction:1});await settle();assert.equal(f.runtime.getSnapshot().playback.positionSeconds,5.037);
  await f.runtime.stepFrame(fence,-1);await settle();assert.equal(f.runtime.getSnapshot().playback.positionSeconds,4.991);
  assert.deepEqual(calls,[{direction:1,generation:fence.generation},{direction:-1,generation:fence.generation}]);assert.equal(f.video.seeks.length,seeks);
  f.runtime.collapse(fence);assert.equal(f.runtime.canStepFrame(fence),false);await assert.rejects(f.runtime.stepFrame(fence,1),/playback_frame_step_unavailable/);
  f.runtime.open(fence);await f.runtime.seek(fence,10);assert.equal(f.runtime.canStepFrame(fence),false);
});

test('a delayed native frame command cannot publish into a replacement source or retired authority', async t => {
  const {WebMediaPlayer}=await import('../src/platform/playback/web-media-player.ts');const gate=deferred();let effects=0;
  const f=setup(t,{}, {createRender:null,subtitleSurface:false,createPlayer:(video,options)=>{
    const player=new WebMediaPlayer(video,options);
    return Object.assign(player,{stepFrame:async(_direction:-1|1,generation:number)=>{await gate.hold();if(player.loaded!==generation || player.observe().state!=='paused')throw new Error('native_frame_retired');effects++;}});
  }});
  await f.runtime.play('movie',5);await f.arrive();const old=f.runtime.getSnapshot().fence!;await f.runtime.pause(old);await settle();
  const pending=f.runtime.stepFrame(old,1),rejected=assert.rejects(pending,/native_frame_retired|stale_playback_command/);await gate.started;
  await f.runtime.play('replacement',20);await f.arrive();const next=f.runtime.getSnapshot().fence!;gate.resolve();await rejected;
  assert.equal(effects,0);assert.deepEqual(f.runtime.getSnapshot().fence,next);assert.equal(f.runtime.getSnapshot().playback.positionSeconds,20);
  assert.equal(f.runtime.canStepFrame(old),false);await assert.rejects(f.runtime.stepFrame(old,1),/stale_playback_command/);
  f.invalidate();assert.equal(f.runtime.canStepFrame(next),false);
});

test('runtime uses the resolved subject preference for both actual engine and service speed', async t => {
  const f = setup(t, {videoSpeed: () => 1.75, audiobookSpeed: () => 1.25});
  for (const [itemId, kind, rate] of [['movie', 'video', 1.75], ['book_file', 'audiobook', 1.25], ['song_one', 'music', 1]] as const) {
    await f.runtime.play(itemId, 5); await f.arrive();
    const snapshot = f.runtime.getSnapshot();
    assert.equal(snapshot.subject!.kind, kind);
    assert.equal(snapshot.presentation!.rate, rate);
    assert.equal(snapshot.playback.audioEffects.rate, rate);
    assert.equal(f.video.playbackRate, rate);
  }
  assert.throws(() => f.runtime.setSpeed(f.runtime.getSnapshot().fence!, 1.5), /playback_speed_unavailable/);
  assert.equal(f.video.playbackRate, 1);
});

test('runtime waits for the actual subject before choosing a speed preference', async t => {
  let videoReads = 0, bookReads = 0;
  const f = setup(t, {videoSpeed: () => { videoReads++; return 1.75; }, audiobookSpeed: () => { bookReads++; return 1.25; }}), delayed = deferred();
  f.intercept(async (request, response) => { if (request.path === '/v1/items/book_file') await delayed.hold(); return response; });
  await f.runtime.play('book_file', 5); await delayed.started;
  assert.equal(f.runtime.getSnapshot().subject, undefined);
  assert.equal(videoReads, 0); assert.equal(bookReads, 0);
  delayed.resolve(); await f.arrive();
  assert.equal(videoReads, 0); assert.ok(bookReads > 0);
  assert.equal(f.video.playbackRate, 1.25);
});

test('runtime leaves omitted quality absent and forwards a current explicit saved preference', async t => {
  let quality: ReturnType<NonNullable<WebPlaybackSettings['defaultQuality']>>;
  const f = setup(t, {defaultQuality: () => quality});
  await f.runtime.play('movie', 5); await f.arrive();
  let request = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!;
  assert.equal(Object.hasOwn(request.body as object, 'quality'), false);
  quality = {mode: 'fixed', id: '720p'};
  await f.runtime.play('second', 5); await f.arrive();
  request = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!;
  assert.deepEqual((request.body as {quality: unknown}).quality, quality);
});

test('runtime ignores late playback offers after another source starts', async t => {
  const f = setup(t), gate = deferred();
  f.intercept(async (request, response) => { if (request.path.includes('/first/playback-offers')) await gate.hold(); return response; });
  await f.runtime.play('first', 5); await gate.started;
  await f.runtime.play('second', 15); await f.arrive();
  gate.resolve(); await settle();
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'second');
  assert.equal(f.runtime.getSnapshot().presentation!.subject.kind, 'video');
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 15);
});

test('runtime requires the bound detail viewer fence before exposing source commands', async t => {
  const f = setup(t); await f.runtime.play('movie', 5); const engine = await f.arrive();
  const fence = f.runtime.getSnapshot().fence;
  assert.ok(fence);
  assert.equal(f.runtime.getSnapshot().offers?.scope.viewerFence, detailViewerFence);
  f.changeViewerFence(); await f.offers().refresh({quiet: true}); await settle();
  assert.equal(f.runtime.getSnapshot().fence, undefined);
  assert.equal(f.runtime.getSnapshot().presentation, undefined);
  assert.equal(f.offers().getSnapshot().error?.code, 'permission_changed');
  assert.equal(f.offers().getSnapshot().data, null);
  assert.equal(f.runtime.getSnapshot().offers, null);
  assert.equal(f.runtime.getSnapshot().disposed, true);
  assert.deepEqual(f.runtime.getSnapshot().presentations, []);
  assert.equal(f.runtime.getSnapshot().dock.local, null);
  assert.equal(engine.destroyed, true); assert.equal(f.video.listenerCount(), 0);
  await assert.rejects(f.runtime.resume(fence), /stale_playback_command/);
});

test('runtime source disappearance drops media, event listeners and all pending completion effects', async t => {
  const f = setup(t); await f.runtime.play('movie', 5); const engine = await f.arrive(), fence = f.runtime.getSnapshot().fence!;
  assert.ok(f.callbacks() > 0); f.invalidate(); f.runtime.dispose();
  assert.equal(engine.destroyed, true); assert.equal(f.video.listenerCount(), 0); assert.equal(f.callbacks(), 0);
  assert.equal(f.runtime.getSnapshot().disposed, true); assert.equal(f.runtime.getSnapshot().dock.local, null);
  await assert.rejects(f.runtime.resume(fence));
  f.clock.advance(30000); await settle(); assert.equal(f.runtime.getSnapshot().disposed, true);
});

test('runtime disposal clears the actual authority-owned audio byte cache', async t => {
  const original = ByteCache.prototype.clear, cleared: ByteCache[] = [];
  ByteCache.prototype.clear = function () { cleared.push(this); original.call(this); };
  t.after(() => { ByteCache.prototype.clear = original; });
  const first = setup(t), second = setup(t);
  first.runtime.dispose(); second.runtime.dispose();
  assert.equal(cleared.length, 2); assert.notEqual(cleared[0], cleared[1], 'different authorities/runtimes cannot share private cached media');
  assert.ok(cleared.every(cache => cache.size === 0));
});

test('runtime selection Play creates and adopts the real mixed queue in supplied display order', async t => {
  const f = setup(t), port = f.runtime.selectionPlayback(context => context === selectionContext);
  const receipt = await port.play({context: selectionContext, items: [selectionLeaf('first'), selectionAlbum, selectionLeaf('last')], signal: new AbortController().signal});
  await f.arrive();
  const state = f.runtime.getSnapshot();
  assert.equal(receipt.queue.id, state.session!.queue!.queueId);
  assert.equal(state.playback.session!.id, state.session!.id);
  assert.deepEqual(state.queueView!.queue.entries.map(e => e.hidden ? null : e.itemId), ['first', ...Array.from({length: 12}, (_, index) => `song_release-${index}`), 'last']);
  const create = f.log.find(r => r.method === 'POST' && r.path === '/v1/queues')!;
  assert.deepEqual((create.body as {segments: unknown}).segments, [{source: {items: {ids: ['first']}}}, {source: {container: {kind: 'album', id: 'song_release'}}}, {source: {items: {ids: ['last']}}}]);
});

test('runtime selection Play next inserts real segments in pick order and preserves current media', async t => {
  const f = setup(t); await f.runtime.play('original', 10); const engine = await f.arrive();
  const original = f.runtime.getSnapshot().fence!, port = f.runtime.selectionPlayback(() => true), before = f.log.length, plays = f.video.plays;
  const receipt = await port.enqueue({context: selectionContext, items: [selectionLeaf('picked-second'), selectionAlbum, selectionLeaf('picked-first')], placement: 'next', signal: new AbortController().signal});
  const state = f.runtime.getSnapshot();
  assert.equal(receipt.queue.id, state.session!.queue!.queueId);
  assert.equal(state.fence!.sessionId, original.sessionId); assert.equal(engine.destroyed, false);
  assert.deepEqual(state.fence, original); assert.equal(f.video.plays, plays);
  assert.equal(state.playback.positionSeconds, 10);
  assert.deepEqual(state.queueView!.queue.entries.map(e => e.hidden ? null : e.itemId), ['original', 'picked-second', ...Array.from({length: 12}, (_, index) => `song_release-${index}`), 'picked-first']);
  const next = state.queueView?.next;
  assert.ok(next?.entryId);
  assert.equal(state.queueView?.postPlay.nextEntryId, next.entryId);
  assert.ok(state.queueView?.queue.entries.some(entry => entry.id === next.entryId));
  assert.equal(f.log.slice(before).some(request => request.path === '/v1/playback/sessions' && request.method === 'POST'), false);
});

test('runtime enqueue while idle creates the actual queue without starting media', async t => {
  const f = setup(t), receipt = await f.runtime.selectionPlayback(() => true).enqueue({context: selectionContext, items: [selectionLeaf('second'), selectionLeaf('first')], placement: 'end', signal: new AbortController().signal});
  assert.equal(receipt.queue.total, 2);
  assert.equal(f.runtime.getSnapshot().playback.session, undefined);
  assert.equal(f.hls.instances.length, 0);
  assert.equal(f.log.some(r => r.method === 'POST' && r.path === '/v1/playback/sessions'), false);
  const create = f.log.find(r => r.method === 'POST' && r.path === '/v1/queues')!;
  assert.equal(Object.hasOwn(create.body as object, 'startPlayback'), false);
});

test('runtime idle enqueue retains an offered video queue without starting it or the enqueued media', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const video = f.runtime.getSnapshot(), bookmark = video.fence, queue = video.session?.queue;
  assert.ok(bookmark); assert.ok(queue);
  await f.runtime.play('song_one', 5); await f.arrive();
  const audio = f.runtime.getSnapshot().fence;
  assert.ok(audio);
  await f.runtime.stop(audio);
  assert.equal(f.runtime.getSnapshot().playback.session, undefined);
  assert.equal(f.runtime.getSnapshot().dock.resumeVideo?.fence.sessionId, bookmark.sessionId);
  const before = f.log.length, plays = f.video.plays;
  const receipt = await f.runtime.selectionPlayback(() => true).enqueue({context: selectionContext, items: [selectionLeaf('queued')], placement: 'end', signal: new AbortController().signal});
  const calls = f.log.slice(before), create = calls.find(request => request.path === '/v1/queues' && request.method === 'POST');
  assert.ok(create);
  assert.deepEqual(create.body, {owner: 'device', segments: [{source: {items: {ids: ['queued']}}}], retainQueueIds: [queue.queueId]});
  assert.ok(f.server.queue(receipt.queue.id)); assert.ok(f.server.queue(queue.queueId));
  assert.equal(calls.some(request => request.path === '/v1/playback/sessions' && request.method === 'POST'), false);
  assert.equal(f.runtime.getSnapshot().playback.session, undefined); assert.equal(f.video.plays, plays);
  await f.runtime.restoreBookmark(bookmark, false); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session?.queue?.queueId, queue.queueId);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 20);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused'); assert.equal(f.video.plays, plays);
});

test('runtime selection cancels a late queue before media adoption when the Home context changes', async t => {
  const f = setup(t); let current = true, sessionId = '';
  f.intercept(async (request, response) => { if (request.method === 'POST' && request.path === '/v1/queues') { sessionId = (response.body as {session: {id: string}}).session.id; current = false; } return response; });
  await assert.rejects(f.runtime.selectionPlayback(() => current).play({context: selectionContext, items: [selectionLeaf('first')], signal: new AbortController().signal}));
  assert.equal(f.hls.instances.length, 0);
  assert.equal(f.server.session(sessionId)!.ended, true);
  assert.equal(f.runtime.getSnapshot().playback.session, undefined);
});

test('runtime selection abort cannot stop a newer playback or prevent another Play', async t => {
  const f = setup(t), delayed = deferred(), abort = new AbortController(); let once = true;
  f.intercept(async (request, response) => { if (once && request.method === 'POST' && request.path === '/v1/queues') { once = false; await delayed.hold(); } return response; });
  const pending = f.runtime.selectionPlayback(() => true).play({context: selectionContext, items: [selectionLeaf('first')], signal: abort.signal});
  const rejected = assert.rejects(pending);
  await delayed.started; abort.abort(); await f.runtime.play('replacement', 20); await f.arrive();
  delayed.resolve(); await rejected;
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'replacement');
  assert.equal(f.video.paused, false);
});

test('runtime Home channel Play binds actual channel/session generations without a queue or catalogue reads', async t => {
  const f = setup(t); await f.runtime.selectionPlayback(() => true).playChannel(homeChannel, selectionContext, new AbortController().signal); await f.arriveLive();
  const state = f.runtime.getSnapshot(), fence = state.fence!;
  assert.equal(fence.kind, 'channel'); if (fence.kind !== 'channel') assert.fail('channel fence required');
  assert.equal(fence.channelId, 'channel'); assert.equal(fence.sourceId, 'source'); assert.equal(fence.channelGeneration, 'g1');
  assert.equal(fence.sessionId, state.playback.session!.id); assert.equal(fence.generation, state.playback.session!.generation);
  assert.equal(Object.hasOwn(fence, 'entryId'), false); assert.equal(Object.hasOwn(fence, 'itemId'), false);
  assert.equal(state.dock.local!.kind, 'live'); assert.equal(state.dock.local!.queue, null); assert.equal(state.presentation!.policy, null);
  assert.equal(f.log.some(r => r.path.startsWith('/v1/items/') || r.path === '/v1/queues'), false);
  await f.runtime.pause(fence); await settle(); assert.equal(f.video.paused, true);
  await f.runtime.resume(fence); await settle(); assert.equal(f.video.paused, false);
  await f.runtime.play('movie', 10); await f.arrive();
  await assert.rejects(f.runtime.pause(fence), /stale_playback_command/);
});

test('runtime restores a parked live session paused through the actual paused start and retained-window seek', async t => {
  const f = setup(t), port = f.runtime.selectionPlayback(() => true);
  await port.playChannel(homeChannel, selectionContext, new AbortController().signal); await f.arriveLive();
  await f.runtime.seek(f.runtime.getSnapshot().fence!, 30); await settle(); await f.arriveLive();
  const channel = f.runtime.getSnapshot().fence!;
  await f.runtime.play('song_one', 5); await f.arrive();
  assert.equal(f.runtime.getSnapshot().dock.resumeVideo!.fence.kind, 'channel');
  const before = f.log.length, plays = f.video.plays;
  await f.runtime.restoreBookmark(channel, false); await settle(); await f.arriveLive();
  const state = f.runtime.getSnapshot();
  assert.equal(state.fence!.kind, 'channel'); assert.equal(state.playback.intent, 'paused'); assert.equal(f.video.paused, true);
  assert.notEqual(state.fence!.sessionId, channel.sessionId); assert.equal(f.video.plays, plays);
  const calls = f.log.slice(before), start = calls.find(r => r.method === 'POST' && r.path === '/v1/playback/sessions')!;
  assert.equal((start.body as {state: string}).state, 'paused');
  assert.ok(calls.filter(r => r.method === 'PATCH').every(r => (r.body as {state?: string}).state !== 'playing'));
  assert.ok(calls.some(r => r.method === 'PATCH' && (r.body as {seek?: {positionMs: number}}).seek?.positionMs === 30000));
});

test('runtime reports an expired live bookmark instead of claiming a clamped restoration', async t => {
  const f = setup(t); await f.runtime.selectionPlayback(() => true).playChannel(homeChannel, selectionContext, new AbortController().signal); await f.arriveLive();
  await f.runtime.seek(f.runtime.getSnapshot().fence!, 30); await settle(); await f.arriveLive();
  const bookmark = f.runtime.getSnapshot().fence!;
  await f.runtime.play('song_one', 5); await f.arrive(); f.expireLiveBookmark();
  const before = f.log.length;
  await assert.rejects(f.runtime.restoreBookmark(bookmark, false), /channel_bookmark_expired/);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused');
  assert.equal(f.runtime.getSnapshot().problem, 'channel_bookmark_expired');
  assert.equal(f.log.slice(before).some(r => r.method === 'PATCH' && (r.body as {seek?: {positionMs: number}}).seek?.positionMs === 30000), false);
});

test('runtime rejects missing or invalid action values before changing the mounted media', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); const engine = await f.arrive();
  const fence = f.runtime.getSnapshot().fence;
  assert.ok(fence);
  const before = f.log.length, seeks = [...f.video.seeks], plays = f.video.plays;
  await assert.rejects(f.runtime.onAction({kind: 'seek', fence}), /invalid_playback_position/);
  for (const seconds of [NaN, Infinity, -1, 1e9 + 1]) await assert.rejects(f.runtime.onAction({kind: 'seek', fence, seconds}), /invalid_playback_position/);
  await assert.rejects(f.runtime.onAction({kind: 'volume', fence}), /invalid_playback_volume/);
  for (const volume of [NaN, Infinity, -0.1, 1.1]) await assert.rejects(f.runtime.onAction({kind: 'volume', fence, volume}), /invalid_playback_volume/);
  assert.deepEqual(f.video.seeks, seeks);
  assert.equal(f.video.volume, 1); assert.equal(f.video.plays, plays); assert.equal(engine.destroyed, false);
  assert.equal(f.log.slice(before).some(request => request.method === 'POST' || request.method === 'PATCH' || request.method === 'DELETE'), false);
  await f.runtime.onAction({kind: 'volume', fence, volume: 0});
  assert.equal(f.video.volume, 0); assert.equal(f.runtime.getSnapshot().presentation?.volume, 0);
  await f.runtime.onAction({kind: 'mute', fence}); assert.equal(f.video.muted, true);
  await f.runtime.onAction({kind: 'mute', fence}); assert.equal(f.video.muted, false);
});

test('runtime queue-only commands cannot reinterpret a real live fence as a catalogue item', async t => {
  const f = setup(t); await f.runtime.selectionPlayback(() => true).playChannel(homeChannel, selectionContext, new AbortController().signal);
  const engine = await f.arriveLive(), fence = f.runtime.getSnapshot().fence;
  assert.ok(fence); assert.equal(fence.kind, 'channel');
  const before = f.log.length, plays = f.video.plays;
  await assert.rejects(f.runtime.replay(fence), /playback_command_unavailable/);
  await assert.rejects(f.runtime.previous(fence), /playback_command_unavailable/);
  await assert.rejects(f.runtime.next(fence), /playback_command_unavailable/);
  await assert.rejects(f.runtime.setQuality(fence, 'original'), /playback_quality_unavailable/);
  await assert.rejects(f.runtime.selectAudio(fence, 'a1'), /playback_audio_unavailable/);
  await assert.rejects(f.runtime.selectSubtitle(fence, null), /playback_subtitle_unavailable/);
  await assert.rejects(f.runtime.restoreBookmark(fence, true), /stale_playback_command/);
  assert.deepEqual(f.runtime.getSnapshot().fence, fence);
  assert.equal(engine.destroyed, false); assert.equal(f.video.plays, plays);
  assert.equal(f.log.slice(before).some(request => request.method !== 'GET'), false);
  assert.equal(f.log.some(request => request.path.startsWith('/v1/items/') || request.path === '/v1/queues'), false);
});

test('runtime refuses restoring the current queue or a different expected bookmark without toggling playback', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  const audio = f.runtime.getSnapshot().fence;
  assert.ok(audio);
  if (audio.kind !== 'queue') assert.fail('queue fence required');
  await f.runtime.play('movie', 20); const engine = await f.arrive(), current = f.runtime.getSnapshot().fence;
  assert.ok(current);
  const before = f.log.length, plays = f.video.plays;
  await assert.rejects(f.runtime.restoreBookmark(current, true), /stale_playback_command/);
  await assert.rejects(f.runtime.restoreBookmark({...audio, sourceId: 'different_source'}, true), /stale_playback_command/);
  await assert.rejects(f.runtime.restoreBookmark({...audio, entryId: 'different_entry'}, true), /stale_playback_command/);
  await assert.rejects(f.runtime.restoreBookmark({...audio, generation: audio.generation + 1}, true), /stale_playback_command/);
  assert.deepEqual(f.runtime.getSnapshot().fence, current);
  assert.equal(f.runtime.getSnapshot().dock.parkedAudio?.fence.sessionId, audio.sessionId);
  assert.equal(f.video.paused, false); assert.equal(f.video.plays, plays); assert.equal(engine.destroyed, false);
  assert.equal(f.log.slice(before).some(request => request.method !== 'GET'), false);
});

test('runtime explicit Resume waits for the restored queue source and starts only its fresh paused session', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  f.video.advance(35); await settle();
  const audio = f.runtime.getSnapshot(), bookmark = audio.fence;
  assert.ok(bookmark);
  if (bookmark.kind !== 'queue') assert.fail('queue fence required');
  assert.ok(audio.session?.queue);
  await f.runtime.play('movie', 5); await f.arrive();
  const gate = deferred(), before = f.log.length, plays = f.video.plays;
  f.intercept(async (request, response) => { if (request.path.startsWith('/v1/items/song_one/playback-offers')) await gate.hold(); return response; });
  const pending = f.runtime.restoreBookmark(bookmark, true);
  await gate.started; await settle();
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused');
  assert.equal(f.runtime.getSnapshot().fence, undefined);
  assert.equal(f.video.paused, true); assert.equal(f.video.plays, plays);
  assert.equal(f.log.slice(before).some(request => request.path === '/v1/queues' && request.method === 'POST'), false);
  const starts = f.log.slice(before).filter(request => request.path === '/v1/playback/sessions' && request.method === 'POST');
  assert.equal(starts.length, 1);
  assert.deepEqual(starts[0]?.body, {queue: {queueId: audio.session.queue.queueId, entryId: bookmark.entryId}, state: 'paused', startPositionMs: 35000});
  gate.resolve();
  const accepted = await pending; await f.arrive();
  assert.equal(accepted.kind, 'queue');
  if (accepted.kind !== 'queue') assert.fail('queue fence required');
  assert.notEqual(accepted.sessionId, bookmark.sessionId);
  assert.equal(accepted.itemId, bookmark.itemId); assert.equal(accepted.entryId, bookmark.entryId);
  assert.equal(f.runtime.getSnapshot().session?.queue?.queueId, audio.session.queue.queueId);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'playing'); assert.equal(f.video.paused, false);
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 35);
});

test('runtime late explicit Resume cannot resume or replace a newer Play', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  const bookmark = f.runtime.getSnapshot().fence;
  assert.ok(bookmark);
  await f.runtime.play('movie', 5); await f.arrive();
  const gate = deferred();
  f.intercept(async (request, response) => { if (request.path.startsWith('/v1/items/song_one/playback-offers')) await gate.hold(); return response; });
  const rejected = assert.rejects(f.runtime.restoreBookmark(bookmark, true), /stale_playback_command/);
  await gate.started;
  await f.runtime.play('replacement', 20); const engine = await f.arrive();
  const current = f.runtime.getSnapshot().fence, plays = f.video.plays;
  assert.ok(current);
  await f.runtime.pause(current); assert.equal(f.video.paused, true);
  gate.resolve(); await rejected; await settle();
  assert.deepEqual(f.runtime.getSnapshot().fence, current);
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'replacement');
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 20);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused'); assert.equal(f.video.paused, true);
  assert.equal(f.video.plays, plays); assert.equal(engine.destroyed, false);
});

test('runtime rejects a late parked queue header after a competing Play', async t => {
  const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
  const audio = f.runtime.getSnapshot(), bookmark = audio.fence, queue = audio.session?.queue;
  assert.ok(bookmark); assert.ok(queue);
  await f.runtime.selectionPlayback(() => true).playChannel(homeChannel, selectionContext, new AbortController().signal);
  await f.arriveLive();
  const gate = deferred(), restoring: {signal?: AbortSignal} = {};
  f.intercept(async (request, response) => {
    if (request.method === 'GET' && request.path === `/v1/queues/${queue.queueId}`) { restoring.signal = request.signal; await gate.hold(); }
    return response;
  });
  const rejected = assert.rejects(f.runtime.restoreBookmark(bookmark, true), /stale_playback_command/);
  await gate.started;
  await f.runtime.play('replacement', 20); const engine = await f.arrive();
  const current = f.runtime.getSnapshot().fence;
  assert.ok(current); assert.ok(restoring.signal); assert.equal(restoring.signal.aborted, true);
  await f.runtime.pause(current);
  const before = f.log.length, plays = f.video.plays;
  gate.resolve(); await rejected; await settle();
  assert.deepEqual(f.runtime.getSnapshot().fence, current);
  assert.equal(f.runtime.getSnapshot().playback.itemId, 'replacement');
  assert.equal(f.runtime.getSnapshot().playback.positionSeconds, 20);
  assert.equal(f.runtime.getSnapshot().playback.intent, 'paused'); assert.equal(f.video.paused, true);
  assert.equal(f.video.plays, plays); assert.equal(engine.destroyed, false);
  assert.equal(f.log.slice(before).some(request => request.method === 'POST' && request.path === '/v1/playback/sessions'), false);
});

test('runtime quality changes require current options and an enabled rung on the actual source', async t => {
  const f = setup(t), gate = deferred();
  f.intercept(async (request, response) => { if (request.path === '/v1/items/movie/playback-options?versionId=v1') await gate.hold(); return response; });
  await f.runtime.play('movie', 20); await gate.started; await f.arrive();
  const fence = f.runtime.getSnapshot().fence;
  assert.ok(fence);
  assert.equal(f.runtime.getSnapshot().options, null);
  const before = f.log.length;
  await assert.rejects(f.runtime.setQuality(fence, '720p-high'), /playback_quality_unavailable/);
  assert.equal(f.log.slice(before).some(request => request.method === 'PATCH'), false);
  gate.resolve(); await settle();
  assert.ok(f.runtime.getSnapshot().options);
  // The older options endpoint offers 1080p, but this source's current offer does not.
  await assert.rejects(f.runtime.setQuality(fence, '1080p-high'), /playback_quality_unavailable/);
  assert.equal(f.log.slice(before).some(request => request.method === 'PATCH'), false);
  await f.runtime.setQuality(fence, '720p-high'); await settle();
  assert.ok(f.log.slice(before).some(request => request.path === `/v1/playback/sessions/${fence.sessionId}` && request.method === 'PATCH'));
  assert.equal(f.runtime.getSnapshot().playback.session?.id, fence.sessionId);
  assert.ok((f.runtime.getSnapshot().playback.session?.generation ?? 0) > fence.generation);
  await assert.rejects(f.runtime.setQuality(fence, '720p-high'), /stale_playback_command/);
});

test('runtime Home selection binds distinct Home and detail fences to the actual queue and persistent media', async t => {
  const f = setup(t), picked = selectionLeaf('movie');
  // The canonical fake's default queue leaf is three minutes, while this fixture's
  // actual movie media/options already declare two hours. Publish that same duration
  // in this test's queue receipt so 1040 seconds is a valid authoritative position.
  f.intercept(async (request, response) => {
    if (request.method === 'POST' && request.path === '/v1/queues' && response.status === 201) {
      const body = response.body as {window: {itemId: string; durationMs: number}[]};
      assert.ok(body.window.length > 0); assert.ok(body.window.every(entry => entry.itemId === 'movie' && entry.durationMs === 180000));
      return {...response, body: {...body, window: body.window.map(entry => ({...entry, durationMs: 7200000}))}};
    }
    return response;
  });
  assert.ok('target' in picked);
  const receipt = await f.runtime.selectionPlayback(context => context === selectionContext).play({context: selectionContext, items: [{...picked, startIntent: 'explicit', target: {...picked.target, startSeconds: 1040}}], signal: new AbortController().signal});
  const engine = await f.arrive(), state = f.runtime.getSnapshot(), fence = state.fence;
  assert.ok(fence); assert.equal(fence.kind, 'queue');
  assert.equal(state.authority.viewerFence, homeViewerFence);
  assert.equal(state.offers?.scope.viewerFence, detailViewerFence);
  assert.notEqual(state.offers?.scope.viewerFence, state.authority.viewerFence);
  assert.equal(state.offers, f.offers().getSnapshot().data, 'the actual detail response is preserved, including its own fence');
  assert.equal(state.session?.queue?.queueId, receipt.queue.id);
  assert.equal(fence.sessionId, state.session?.id); assert.equal(fence.generation, state.playback.session?.generation);
  assert.equal(state.dock.local?.fence.sessionId, fence.sessionId);
  assert.ok(state.presentation); assert.equal(state.presentation.expanded, true); assert.equal(state.presentation.positionSeconds, 1040);
  const loads = f.video.loads, listeners = f.video.listenerCount();
  f.runtime.collapse(fence); f.video.advance(1042); await settle();
  assert.equal(f.runtime.getSnapshot().presentation?.expanded, false);
  assert.equal(f.runtime.getSnapshot().dock.local?.positionSeconds, 1042);
  f.runtime.open(fence);
  assert.equal(f.runtime.getSnapshot().presentation?.expanded, true);
  assert.equal(f.runtime.getSnapshot().presentation?.positionSeconds, 1042);
  assert.equal(f.video.loads, loads); assert.equal(f.video.listenerCount(), listeners);
  assert.equal(engine.destroyed, false); assert.equal(f.hls.instances.length, 1);
});

test('runtime accepts the same actual detail fence after refresh and a new media generation', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const first = f.runtime.getSnapshot(), fence = first.fence;
  assert.ok(fence); assert.notEqual(first.offers?.scope.viewerFence, first.authority.viewerFence);
  await f.offers().refresh({quiet: true}); await settle();
  assert.deepEqual(f.runtime.getSnapshot().fence, fence);
  assert.equal(f.runtime.getSnapshot().offers?.scope.viewerFence, detailViewerFence);
  await f.runtime.setQuality(fence, '720p-high'); await f.arrive();
  const changed = f.runtime.getSnapshot(), next = changed.fence;
  assert.ok(next); assert.equal(next.sessionId, fence.sessionId); assert.ok(next.generation > fence.generation);
  assert.equal(changed.offers?.current?.generation, next.generation);
  assert.equal(changed.offers?.scope.viewerFence, detailViewerFence);
  assert.equal(changed.presentation?.fence.generation, next.generation);
  assert.equal(changed.dock.local?.fence.generation, next.generation);
  await assert.rejects(f.runtime.pause(fence), /stale_playback_command/);
  await f.runtime.pause(next); assert.equal(f.video.paused, true);
});

test('runtime cannot rebind a changed detail fence when the media generation changes', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const first = f.runtime.getSnapshot().fence;
  assert.ok(first);
  f.changeViewerFence(); await f.runtime.setQuality(first, '720p-high'); await settle();
  assert.equal(f.offers().getSnapshot().error?.code, 'permission_changed');
  assert.equal(f.offers().getSnapshot().data, null);
  assert.equal(f.runtime.getSnapshot().offers, null);
  assert.equal(f.runtime.getSnapshot().fence, undefined);
  assert.equal(f.runtime.getSnapshot().presentation, undefined);
  assert.deepEqual(f.runtime.getSnapshot().presentations, []);
  assert.equal(f.runtime.getSnapshot().disposed, true);
  assert.equal(f.runtime.getSnapshot().dock.local, null);
  await assert.rejects(f.runtime.resume(first), /stale_playback_command/);
});

test('runtime rejects cross-server, library, item, session and media-generation offer responses', async t => {
  type OfferWire = {scope: {serverId: string; libraryId: string; itemId: string}; current: {sessionId: string; generation: number}};
  const changes: readonly ((wire: OfferWire) => void)[] = [
    wire => { wire.scope.serverId = 'foreign_server'; },
    wire => { wire.scope.libraryId = 'foreign_library'; },
    wire => { wire.scope.itemId = 'foreign_item'; },
    wire => { wire.current.sessionId = 'foreign_session'; },
    wire => { wire.current.generation += 1; },
  ];
  for (const [index, change] of changes.entries()) {
    const f = setup(t);
    f.intercept(async (request, response) => { if (request.path.includes('/playback-offers')) change(response.body as OfferWire); return response; });
    await f.runtime.play('movie', 20); await f.arrive();
    assert.equal(f.runtime.getSnapshot().offers, null);
    assert.equal(f.runtime.getSnapshot().fence, undefined);
    assert.equal(f.runtime.getSnapshot().presentation, undefined);
    assert.equal(f.runtime.getSnapshot().dock.local, null);
    if (index === changes.length - 1) assert.equal(f.offers().getSnapshot().phase, 'ready', 'future media generation is rejected by the runtime after response parsing');
    f.runtime.dispose();
  }
});

test('runtime rejects an older media-generation offer even when the real offers parser accepts it', async t => {
  const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
  const first = f.runtime.getSnapshot().fence;
  assert.ok(first);
  f.intercept(async (request, response) => {
    if (request.path.includes('/playback-offers')) (response.body as {current: {generation: number}}).current.generation = first.generation;
    return response;
  });
  await f.runtime.setQuality(first, '720p-high'); await f.arrive();
  assert.ok(f.runtime.getSnapshot().playback.session!.generation > first.generation);
  assert.equal(f.offers().getSnapshot().phase, 'ready');
  assert.equal(f.offers().getSnapshot().data?.current?.generation, first.generation);
  assert.equal(f.runtime.getSnapshot().offers, null);
  assert.equal(f.runtime.getSnapshot().fence, undefined);
  assert.equal(f.runtime.getSnapshot().presentation, undefined);
  await assert.rejects(f.runtime.resume(first), /stale_playback_command/);
});

test('runtime drops late same-item offers from a replaced session even when transport ignores abort', async t => {
  const f = setup(t), gate = deferred(); let once = true;
  f.intercept(async (request, response) => {
    if (once && request.path.includes('/movie/playback-offers')) { once = false; await gate.hold(); }
    return response;
  });
  await f.runtime.play('movie', 5); await gate.started;
  const first = f.runtime.getSnapshot().playback.session;
  assert.ok(first);
  await f.runtime.play('movie', 30); const engine = await f.arrive();
  const second = f.runtime.getSnapshot(), fence = second.fence;
  assert.ok(fence); assert.notEqual(fence.sessionId, first.id);
  gate.resolve(); await settle();
  assert.deepEqual(f.runtime.getSnapshot().fence, fence);
  assert.equal(f.runtime.getSnapshot().offers?.current?.sessionId, fence.sessionId);
  assert.equal(f.runtime.getSnapshot().presentation?.positionSeconds, 30);
  assert.equal(f.server.session(first.id)?.ended, true); assert.equal(engine.destroyed, false);
});

test('runtime privacy denials purge current and parked metadata, media and command authority', async t => {
  for (const status of [401, 403]) {
    const f = setup(t); await f.runtime.play('song_one', 10); await f.arrive();
    await f.runtime.play('movie', 20); const engine = await f.arrive();
    const first = f.runtime.getSnapshot(), fence = first.fence;
    assert.ok(fence); assert.ok(first.dock.parkedAudio); assert.ok(first.presentations.length > 1);
    const offers = f.offers();
    f.intercept(async (request, response) => request.path.includes('/playback-offers') ? {status, headers: {}, body: {message: 'private denial payload'}} : response);
    await offers.refresh({quiet: true}); await settle();
    const denied = f.runtime.getSnapshot();
    assert.equal(offers.getSnapshot().error?.code, status === 401 ? 'unauthorized' : 'forbidden');
    assert.equal(offers.getSnapshot().data, null);
    assert.equal(denied.disposed, true); assert.equal(denied.fence, undefined);
    assert.equal(denied.subject, undefined); assert.equal(denied.presentation, undefined);
    assert.equal(denied.offers, null); assert.equal(denied.options, null); assert.equal(denied.queueView, null);
    assert.deepEqual(denied.presentations, []);
    assert.equal(denied.dock.local, null); assert.equal(denied.dock.parkedAudio, null); assert.equal(denied.dock.resumeVideo, null);
    assert.equal(engine.destroyed, true); assert.equal(f.video.listenerCount(), 0); assert.equal(f.callbacks(), 0);
    assert.equal(f.server.session(fence.sessionId)?.ended, true);
    assert.equal(JSON.stringify([offers.getSnapshot(), denied]).includes('private denial payload'), false);
    await assert.rejects(f.runtime.resume(fence), /stale_playback_command/);
    const before = f.log.length;
    await assert.rejects(f.runtime.play('other'), /playback_authority_retired/);
    assert.equal(f.log.length, before);
  }
});

test('runtime Home authority retirement prevents an outstanding detail response from publishing', async t => {
  const f = setup(t), gate = deferred();
  f.intercept(async (request, response) => { if (request.path.includes('/playback-offers')) await gate.hold(); return response; });
  await f.runtime.play('movie', 20); await gate.started;
  assert.equal(f.runtime.getSnapshot().fence, undefined);
  // bootstrap restarts assembly/runtime when Home's library-set fence changes. Its epoch
  // closure retires this source before the new Home authority can become current.
  f.invalidate(); f.video.advance(21); await settle();
  gate.resolve(); await settle();
  const retired = f.runtime.getSnapshot();
  assert.equal(retired.disposed, true); assert.equal(retired.offers, null);
  assert.equal(retired.fence, undefined); assert.equal(retired.subject, undefined); assert.equal(retired.presentation, undefined);
  assert.deepEqual(retired.presentations, []); assert.equal(retired.dock.local, null);
  assert.equal(f.video.listenerCount(), 0); assert.equal(f.callbacks(), 0);
  assert.equal(f.log.some(request => request.path.includes('/playback-options')), false);
  await assert.rejects(f.runtime.play('other'), /playback_authority_retired/);
  const video = new FakeVideo(), hls = fakeHls(), clock = manualClock(); let serial = 0;
  const next = new WebShellPlaybackRuntime({...f.source, authority: {...f.source.authority, epoch: 2, viewerFence: 'home_after_library_change'}, isCurrent: () => true}, {timers: clock, playerTimers: clock, loadHls: async () => hls.Hls, nativeHls: 'never', key: () => `next-home-key-${++serial}`});
  next.mount(video); t.after(() => next.dispose());
  await next.play('movie', 30); await settle();
  video.ready(Number(hls.instances.at(-1)!.config.startPosition)); await settle();
  assert.ok(next.getSnapshot().fence);
  assert.equal(next.getSnapshot().authority.viewerFence, 'home_after_library_change');
  assert.equal(next.getSnapshot().authority.epoch, 2);
  assert.equal(next.getSnapshot().offers?.scope.viewerFence, detailViewerFence);
  assert.equal(next.getSnapshot().presentation?.positionSeconds, 30);
  assert.equal(f.runtime.getSnapshot().disposed, true);
});

test('runtime selection still requires the Home authority fence rather than the bound detail fence', async t => {
  const f = setup(t), wrong = {...selectionContext, scope: {...selectionContext.scope, privacyFence: detailViewerFence}};
  await settle();
  assert.deepEqual(f.log.map(request => [request.method, request.path]), [['GET', '/v1/listening/preferences']]);
  const before = f.log.length;
  await assert.rejects(f.runtime.selectionPlayback(() => true).play({context: wrong, items: [selectionLeaf('movie')], signal: new AbortController().signal}), /stale_selection_context/);
  assert.equal(f.log.length, before); assert.deepEqual(f.log.slice(before), []); assert.equal(f.hls.instances.length, 0);
  assert.equal(f.runtime.getSnapshot().fence, undefined); assert.equal(f.runtime.getSnapshot().offers, null);
});


test('canonical offered quality IDs reach the real replan request without suffixes or aliases', async t => {
  for (const choice of ['original', 'automatic', '720p-high'] as const) {
    const f = setup(t); await f.runtime.play('movie', 20); await f.arrive();
    const fence = f.runtime.getSnapshot().fence!;
    await f.runtime.setQuality(fence, choice); await settle();
    const patch = f.log.findLast(r => r.method === 'PATCH' && r.path === `/v1/playback/sessions/${fence.sessionId}`)!;
    assert.deepEqual((patch.body as {quality: unknown}).quality, choice === 'automatic' ? {mode:'automatic', id:'original'} : choice === 'original' ? {mode:'original'} : {mode:'fixed',id:'720p-high'});
  }
});

test('a disabled actual source rung cannot be selected even when options offer it', async t => {
  const f = setup(t);
  f.intercept(async (r, response) => {
    if (r.path.includes('/playback-offers') && response.status === 200) {
      const body = structuredClone(response.body) as any;
      const rung = body.sources[0].qualities.find((v: any) => v.kind === 'fixed');
      rung.enabled = false; rung.reason = 'transcoding_disabled';
      return {...response, body};
    }
    return response;
  });
  await f.runtime.play('movie'); await f.arrive();
  const before = f.log.length;
  await assert.rejects(f.runtime.setQuality(f.runtime.getSnapshot().fence!, '720p-high'), /playback_quality_unavailable/);
  assert.equal(f.log.slice(before).some(r => r.method === 'PATCH'), false);
});

test('platform factory owns media with browser renderer disabled and publishes its capability document', async t => {
  const {webDeclaredProfile} = await import('../../packages/client-core/src/client-profile.ts');
  const {WebMediaPlayer} = await import('../src/platform/playback/web-media-player.ts');
  let created = 0, asked = 0;
  const f = setup(t, {audioEffects: () => ({gapless:true,crossfadeSeconds:0,normalization:'off'})}, {
    createPlayer: (video, options) => { created++; return new WebMediaPlayer(video, options); },
    createRender: null, subtitleSurface: false,
    capabilities: async () => { asked++; return webDeclaredProfile({app:'native-test'}); },
  });
  await f.runtime.play('song_factory'); await f.arrive();
  assert.equal(created, 2, 'empty stack retired before the active music stack'); assert.ok(asked > 0);
  assert.ok(f.log.some(r => r.method === 'PUT' && r.path.endsWith('/capabilities') && (r.body as any).client.app === 'native-test'));
  assert.equal(f.runtime.getSnapshot().playback.phase, 'ready');
  assert.ok(f.log.some(r => r.path.endsWith('/playback-options?versionId=v1')), 'factory media receives music instead of an idle browser slot');
});


test('native surface paint is false until the actual current video port reports a surface', async t => {
  const {WebMediaPlayer} = await import('../src/platform/playback/web-media-player.ts');
  let active = false;
  const f = setup(t, {}, {createRender:null,subtitleSurface:false,createPlayer:(video,options)=>{
    const player = new WebMediaPlayer(video,options);
    Object.defineProperty(player,'nativeSurfaceActive',{get:()=>active});
    return player;
  }});
  assert.equal(f.runtime.getSnapshot().nativeVideoSurfaceActive,false);
  await f.runtime.play('movie'); await f.arrive();
  assert.equal(f.runtime.getSnapshot().nativeVideoSurfaceActive,false,'a factory alone grants no paint hole');
  active=true; f.video.advance(10); await settle();
  assert.equal(f.runtime.getSnapshot().nativeVideoSurfaceActive,true);
  await f.runtime.play('song_surface'); await f.arrive();
  assert.equal(f.runtime.getSnapshot().nativeVideoSurfaceActive,false,'audio never gets a native video hole');
  f.runtime.dispose(); assert.equal(f.runtime.getSnapshot().nativeVideoSurfaceActive,false);
});

test('device processing alone selects the renderer and preserves the actual queue/session when toggled', async t => {
  const applied: string[] = [], equalizers: import('../src/platform/playback/audio/equalizer.ts').EqualizerSettings[] = []; let disposed=0;
  const f=setup(t,{audioEffects:()=>({gapless:false,crossfadeSeconds:0,normalization:'off'})},{createRender:(service)=>({
    applyEqualizer(settings){equalizers.push(settings);},
    apply(snapshot){
      if(!snapshot.session)return;applied.push(snapshot.session.id);
      service.ready(snapshot.intentId);
      if(snapshot.pendingSeek)service.seekApplied(snapshot.intentId,snapshot.pendingSeek.revision,snapshot.pendingSeek.positionSeconds,'paused');
    },transportPosition:()=>0,dispose(){disposed++;}
  })});
  await f.runtime.play('song_device');await f.arrive();
  const first=f.runtime.getSnapshot(), id=first.playback.session!.id, queue=first.session!.queue;
  assert.equal(first.playback.audioEffects.rendering,false);assert.equal(applied.length,0);
  assert.equal(f.runtime.browserMediaReady(first.fence!),true);
  const web=(f.runtime as unknown as {stack:{web: import('../src/platform/playback/web-playback.ts').WebPlayback}}).stack.web;
  const enabled={enabled:true,preampDb:-3,bandsDb:Array(10).fill(2)};
  web.applyEqualizer(enabled);await settle();
  assert.deepEqual(equalizers.at(-1),enabled,'the committed device setting reaches the actual renderer seam');
  assert.equal(f.runtime.getSnapshot().playback.audioEffects.rendering,true);assert.ok(applied.includes(id));
  assert.equal(f.runtime.browserMediaReady(f.runtime.getSnapshot().fence!),false,'the separate renderer holds the HTML media slot');
  assert.equal(f.runtime.getSnapshot().playback.session!.id,id);assert.deepEqual(f.runtime.getSnapshot().session!.queue,queue);
  assert.equal(f.runtime.audioEngineFacts(f.runtime.getSnapshot().fence!),undefined,'an unconfirmed renderer has no invented processing facts');
  web.applyEqualizer({...enabled,enabled:false});await f.arrive();
  assert.equal(equalizers.at(-1)!.enabled,false,'off reaches the graph before the ordinary path resumes');
  assert.equal(f.runtime.getSnapshot().playback.audioEffects.rendering,false);assert.ok(disposed>0);
  assert.equal(f.runtime.getSnapshot().playback.session!.id,id);assert.deepEqual(f.runtime.getSnapshot().session!.queue,queue);
  assert.deepEqual(f.runtime.getSnapshot().playback.audioEffects.settings,{gapless:false,crossfadeSeconds:0,normalization:'off'});
});


test('native media factory receives the validated audio plan and unchanged saved processing snapshot',async t=>{
 const {WebMediaPlayer}=await import('../src/platform/playback/web-media-player.ts');
 const received:import('../../packages/client-core/src/index.ts').PlaybackSnapshot[]=[];
 const settings={gapless:true,crossfadeSeconds:3,normalization:'album'} as const;
 const f=setup(t,{audioEffects:()=>settings},{createRender:null,subtitleSurface:false,createPlayer:(video,options)=>Object.assign(new WebMediaPlayer(video,options),{applyAudioEffects:(snapshot:import('../../packages/client-core/src/index.ts').PlaybackSnapshot)=>received.push(snapshot)})});
 await f.runtime.play('song_native_effects');await f.arrive();
 const snapshot=received.findLast(snapshot=>snapshot.session?.audio);
 assert.ok(snapshot);assert.equal(snapshot.session!.audio!.version,2);
 assert.deepEqual(snapshot.audioEffects.settings,settings);
 assert.equal(snapshot.session!.id,f.runtime.getSnapshot().playback.session!.id);
 assert.ok(snapshot.session!.audio!.trim,'native engine sees raw/audible trim contract, not just a URL');
});

test('unsupported EQ renderer continues the same session and queue through ordinary playback without processing facts', async t => {
  let attempts = 0;
  const f = setup(t, {audioEffects:()=>({gapless:false,crossfadeSeconds:0,normalization:'off'})}, {createRender:()=>{attempts++;throw new Error('unsupported');}});
  await f.runtime.play('song_unsupported_eq',12); await f.arrive();
  const first = f.runtime.getSnapshot(), sessionId = first.session!.id, queue = first.session!.queue;
  const web = (f.runtime as unknown as {stack:{web:import('../src/platform/playback/web-playback.ts').WebPlayback}}).stack.web;
  web.applyEqualizer({enabled:true,preampDb:0,bandsDb:Array(10).fill(0)});
  await settle(); await f.arrive();
  const current = f.runtime.getSnapshot();
  assert.equal(attempts,1); assert.equal(current.session!.id,sessionId); assert.deepEqual(current.session!.queue,queue);
  assert.equal(current.playback.audioEffects.rendering,false); assert.equal(current.playback.phase,'ready');
  assert.equal(f.runtime.audioEngineFacts(current.fence!),undefined);
  assert.equal(current.playback.error,undefined);
});


test('the retained Watch body uses only the actual current title and offered library, with stable identity across source replacement', async t => {
  const f = setup(t);await f.runtime.play('movie', 0);await f.arrive();
  const shown = f.runtime.getSnapshot(), target = watchBodyTarget(shown);
  assert.deepEqual(target, {libraryId: 'library', itemId: 'movie', kind: 'movie'});
  assert.equal(watchBodyTarget({...shown, offers: null}), undefined);
  assert.equal(watchBodyTarget({...shown, offers: {...shown.offers!, scope: {...shown.offers!.scope, serverId: 'foreign'}}}), undefined);
  assert.equal(watchBodyTarget({...shown, session: {...shown.session!, id: 'other-session'}}), undefined);
  assert.equal(watchBodyTarget({...shown, offers: {...shown.offers!, current: {...shown.offers!.current!, generation: shown.offers!.current!.generation + 1}}}), undefined);
  await f.runtime.play('movie', 20);await f.arrive();
  assert.equal(watchBodyKey(watchBodyTarget(f.runtime.getSnapshot())!), watchBodyKey(target!));
  await f.runtime.play('different', 0);await f.arrive();
  assert.notEqual(watchBodyKey(watchBodyTarget(f.runtime.getSnapshot())!), watchBodyKey(target!));
});


test('the player EQ command preserves the injected app device port and refuses a retired fence', async t => {
  const changes: import('../src/platform/playback/audio/equalizer.ts').EqualizerSettings[] = [];
  const value = {enabled: true, preampDb: -3, bandsDb: Array(10).fill(1)};
  const port: import('../src/kit/media/equalizer-port.ts').EqualizerPort = {
    getSnapshot: () => value, subscribe: () => () => {}, start: () => () => {},
    change: async settings => {changes.push(settings);}, reset: async () => {},
  };
  const f = setup(t, {}, {createRender: null, equalizerPort: port});
  assert.equal(f.runtime.equalizerPort, port, 'Settings and the shortcut receive the original app singleton');
  await f.runtime.play('song_device'); await f.arrive();
  const fence = f.runtime.getSnapshot().fence!;
  await f.runtime.setEqualizer(fence, value);
  assert.deepEqual(changes, [value], 'the native port commits the change without a browser renderer/store');
  await f.runtime.play('movie'); await f.arrive();
  await assert.rejects(f.runtime.setEqualizer(fence, value));
  await assert.rejects(f.runtime.setEqualizer(f.runtime.getSnapshot().fence!, value), /equalizer_unavailable/);
  assert.equal(changes.length, 1);
});


test('captured resource queue playback delegates the complete selector and original lifetime to the canonical queue', async t => {
  const f = setup(t), life = new AbortController();
  const source = {container: {kind: 'playlist' as const, id: 'ordered_release'}};
  const task = f.runtime.playFromQueue(source, {signal: life.signal, isCurrent: () => true});
  source.container.id = 'mutated_after_press';
  await task; await f.arrive();
  const created = f.log.find(value => value.method === 'POST' && value.path === '/v1/queues')!;
  const body = created.body as {segments: unknown[]};
  assert.deepEqual(body.segments, [{source: {container: {kind: 'playlist', id: 'ordered_release'}}, order: {mode: 'source'}}]);
  const snapshot = f.runtime.getSnapshot();
  assert.equal(snapshot.session!.itemId, 'ordered_release-0');
  assert.equal(snapshot.queueView!.total, 12, 'the engine retains the complete server container, not one visible item');
  assert.ok(!f.log.some(value => value.method === 'POST' && value.path === '/v1/playback/sessions'), 'the actual queue creates and supplies its chosen current session');
});

test('a canceled or retired captured resource start cannot adopt a late queue or stop newer Play', async t => {
  for (const retire of ['abort', 'navigation', 'new-play'] as const) {
    const f = setup(t), life = new AbortController(), gate = deferred();let active = true, held = false;
    f.intercept(async (request, response) => {
      if (!held && request.method === 'POST' && request.path === '/v1/queues') {held = true;await gate.hold();}
      return response;
    });
    const task = f.runtime.playFromQueue({container: {kind: 'collection', id: 'complete_resource'}}, {signal: life.signal, isCurrent: () => active}, {shuffle: true});
    const rejected = assert.rejects(task);
    await gate.started;
    if (retire === 'new-play') {await f.runtime.play('newer_movie');await f.arrive();life.abort();}
    else if (retire === 'abort') life.abort();
    else active = false;
    gate.resolve();await rejected;await settle();
    assert.equal(f.runtime.getSnapshot().session?.itemId, retire === 'new-play' ? 'newer_movie' : undefined);
  }
});


test('a captured GuideChannel tunes its real source/generation without a Home action or catalogue placeholder', async t => {
  const f = setup(t), life = new AbortController();
  await f.runtime.playFromGuide(homeChannel.channel, {signal: life.signal, isCurrent: () => true});
  await f.arriveLive();
  const start = f.log.find(request => request.method === 'POST' && request.path === '/v1/playback/sessions')!;
  assert.deepEqual(start.body, {channelId: 'live:source:channel', channelGeneration: 'g1', state: 'playing'});
  assert.ok(start.signal && !start.signal.aborted);
  const fence = f.runtime.getSnapshot().fence!;
  assert.equal(fence.kind, 'channel');
  if (fence.kind !== 'channel') return;
  assert.equal(fence.channelGeneration, 'g1');assert.equal(fence.sourceId, 'source');assert.equal(fence.channelId, 'channel');
  assert.ok(!f.log.some(request => request.path.startsWith('/v1/items/') || request.path === '/v1/queues'));
  await assert.rejects(f.runtime.playFromGuide({...homeChannel.channel, tuneAvailable: false}, {signal: life.signal, isCurrent: () => true}), /invalid_channel_target/);
  assert.deepEqual(f.runtime.getSnapshot().fence, fence, 'an unavailable row does not retire the genuine current channel');
});

test('a guide tune cannot adopt a late channel after abort/navigation or stop a newer Play', async t => {
  for (const retirement of ['abort', 'navigation', 'new-play'] as const) {
    // The Shell's original-start custody: a genuine principal/pin-owned API over
    // this same wire, so a late channel 201 is retired by its real cleanup.
    const f = setup(t, {}, {}, undefined, api => {
      const session = {accessToken: 'token', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z',
        tokenGeneration: '1', sessionFamilyId: 'family', deviceId: 'device', installationId: 'installation',
        viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}};
      const route = {logicalOrigin: 'https://portico.test', pin: {serverId: 'server', publicKey: 'pin'}, getSnapshot: () => ({phase: 'ready'}),
        fetch: api.routeFetch, subscribe: () => () => {}, recover: () => Promise.reject(new Error('no recovery'))};
      const ownerApi = {baseUrl: api.baseUrl, getRouteConnection: () => route,
        requestRaw: (path: string, method: string, options: {headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}) => api.requestRaw(path, method, options)};
      return createWebPreparingStarts(ownerApi as unknown as HttpLocalApi, session as never, () => session as never, () => true);
    });
    const life = new AbortController(), gate = deferred();let active = true;
    f.intercept(async (request, response) => {
      if (request.method === 'POST' && request.path === '/v1/playback/sessions' && (request.body as {channelId?: string}).channelId) await gate.hold();
      return response;
    });
    const rejected = assert.rejects(f.runtime.playFromGuide(homeChannel.channel, {signal: life.signal, isCurrent: () => active}));
    await gate.started;
    if (retirement === 'new-play') {await f.runtime.play('newer_movie');await f.arrive();life.abort();}
    else if (retirement === 'abort') life.abort();
    else active = false;
    gate.resolve();await rejected;await settle();
    assert.equal(f.runtime.getSnapshot().session?.itemId, retirement === 'new-play' ? 'newer_movie' : undefined);
    assert.equal(f.runtime.getSnapshot().fence?.kind, retirement === 'new-play' ? 'queue' : undefined);
    assert.ok(f.log.some(request => request.method === 'DELETE' && request.path === '/v1/playback/sessions/live_session_1'), 'the actual late channel session is abandoned');
  }
});


test('the drawn Up next card follows the actual offered next occurrence and only its canonical countdown', async t => {
  const f = setup(t, {skipModes: () => ({credits: 'ask'})});
  policyFixture(f, [{...creditsMarker, endMs: 7150000}]);
  const fence = await policyQueue(f);f.video.advance(7110);await settle();
  const binding = {getSnapshot: f.runtime.getSnapshot, isCurrent: () => true, policyAction: f.runtime.policyAction.bind(f.runtime), reportError: (error: unknown) => {throw error;}};
  const shown = f.runtime.getSnapshot(), offer = watchUpNextOffer(binding, shown)!;
  assert.ok(offer);assert.equal(offer.next.itemId, 'next');assert.equal(offer.next.title, 'Title next');
  assert.equal(offer.prompt.sceneAfterCredits, true);
  assert.deepEqual(offer.countdown, {automatic: false, seconds: 95});assert.equal(offer.progress, undefined);
  await f.runtime.pause(fence);f.clock.advance(3000);await settle();
  assert.equal(watchUpNextOffer(binding, shown)!.countdown!.seconds, 95, 'pause never runs a renderer timer');
  assert.equal(await dispatchWatchNext(binding, shown, 'watch-credits'), true);
  assert.equal(watchUpNextOffer(binding, shown), null);assert.equal(await dispatchWatchNext(binding, shown, 'play-now'), false);
  assert.equal(f.runtime.getSnapshot().session!.itemId, 'first');
});

test('the drawn automatic Up next progress uses the real policy clock and rejects replacement media', async t => {
  const f = setup(t, {skipModes: () => ({credits: 'ask'})});policyFixture(f, [creditsMarker], {startDuringCredits: true});
  await policyQueue(f);f.video.advance(7110);await settle();
  const binding = {getSnapshot: f.runtime.getSnapshot, isCurrent: () => true, policyAction: f.runtime.policyAction.bind(f.runtime), reportError: (error: unknown) => {throw error;}};
  f.clock.advance(2000);await settle();
  const shown = f.runtime.getSnapshot(), offer = watchUpNextOffer(binding, shown)!;
  assert.deepEqual(offer.countdown, {automatic: true, seconds: 3});assert.equal(offer.progress, .4);
  assert.equal(await dispatchWatchNext(binding, shown, 'play-now'), true);await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.itemId, 'next');assert.equal(watchUpNextOffer(binding, shown), null);
  assert.equal(await dispatchWatchNext(binding, shown, 'play-now'), false);
});


// Controlled room wire contracts exercise the actual canonical service, follower and media
// runtime together. This is source qualification, never a real cohort/frame/receiver claim.
async function runtimeRoom(t: TestContext, playing = false, canControl = true, markers = false) {
  const f = setup(t, markers ? {skipModes: () => ({intro: 'auto'})} : {}), at = '2026-10-23T20:00:00Z';let roomId = 'room_first', revision = 1, state = playing ? 'playing' : 'paused', positionUs = '10000000';
  const group = () => ({id: roomId, name: 'Actual room', state, hostAuthority: 'host-only', hostMemberId: 'member', revision: String(revision), playbackRevision: String(revision),
    settingsRevision: '1', governanceRevision: '1', queueRevision: '1', reconnectGeneration: '1', lastCommand: state === 'playing' ? 'play' : 'pause', lastCommandId: 'command', endedReason: '',
    eventOrdinal: String(revision), createdAt: at, updatedAt: at, permissions: {isHost: canControl, canControl, canManageQueue: canControl},
    authority: {deviceId: 'device', playbackId: null, state: 'fresh'}, host: {presence: 'connected', lastSeenAt: at, pauseAt: null, endAt: null},
    timeline: {itemId: 'movie', currentEntryId: 'group_entry', state, anchorPositionUs: positionUs, anchorAt: at, rate: {numerator: '1', denominator: '1'}, queuePosition: 0, waitUntil: null},
    settings: {shuffleEnabled: false, repeatMode: 'none'}, sync: {noCorrectionUnderMs: 750, rateCorrectionMinimum: '0.90', rateCorrectionMaximum: '1.10', rateCorrectionMaxMs: 4000, seekAtOrOverMs: 3000},
    readiness: {aggregate: 'ready', ready: 1, buffering: 0, lagging: 0, stale: 0, memberCount: 1},
    members: [{id: 'member', displayName: 'Actual member', private: false, role: canControl ? 'host' : 'member', state: 'joined', readiness: 'ready', positionUs: '10000000', reportedAt: at, presence: 'connected', joinedAt: at}], queue: null, viewerMemberId: 'member'});
  f.intercept(async (request, response) => {
    if (markers && request.path.includes('/playback-offers?')) {const body = response.body as {current: {sourceId: string}};return {...response, body: {...body, markers: {sourceId: body.current.sourceId, revision: 'group_markers', markers: [introMarker]}}};}
    if (!request.path.startsWith('/v1/groups/')) return response;
    const ok = (body: unknown) => ({status: 200, headers: {}, body});
    if (request.path.endsWith('/transport')) {
      const command = (request.body as {command: string}).command;if (command === 'play') state = 'playing';else if (command === 'pause') state = 'paused';else if (command === 'seek') positionUs = (request.body as {positionUs: string}).positionUs;revision++;
      return ok({protocolVersion: SOCIAL_PROTOCOL, groupId: roomId, idempotencyKey: (request.body as {idempotencyKey: string}).idempotencyKey,
        disposition: 'accepted', groupState: state, command, revision: String(revision), playbackRevision: String(revision), queueRevision: '1', serverTime: at, recordedAt: at, timeline: group().timeline, settings: group().settings, override: null});
    }
    if (request.path.endsWith('/queue')) return ok({protocolVersion: SOCIAL_PROTOCOL, serverTime: at, groupId: roomId,
      queue: {revision: '1', position: 0, entries: [{entryId: 'group_entry', position: 0, itemId: 'movie', unavailable: false, addedBy: 'member'}], eligibility: null}});
    return ok({protocolVersion: SOCIAL_PROTOCOL, serverTime: at, group: group()});
  });
  const service = new GroupSessionService({api: f.api, now: () => Date.parse(at), heartbeatMs: 100000, stream: (_path, _last, signal) => new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(new DOMException('closed', 'AbortError')), {once: true});
  })});
  const errors: unknown[] = [], unbind = f.runtime.bindGroup(service, () => true, error => errors.push(error));
  t.after(() => {unbind();service.dispose();});
  assert.equal(await service.open(roomId), true);await settle();await f.arrive();f.video.land();await settle();
  return {...f, service, errors, nextRoom: async () => {roomId = 'room_second';await service.open(roomId);await settle();await f.arrive();}};
}

test('Together loads a paused room paused before the first picture and sends user controls through the real room revision', async t => {
  const f = await runtimeRoom(t);
  assert.deepEqual(f.errors, []);const shown = f.runtime.getSnapshot(), fence = shown.fence!;
  assert.equal(shown.session!.state, 'paused');assert.equal(shown.playback.intent, 'paused');assert.equal(f.video.plays, 0, 'no play-then-pause first frame');
  const started = f.log.find(request => request.method === 'POST' && request.path === '/v1/playback/sessions')!;
  assert.equal((started.body as {state: string}).state, 'paused');assert.equal((started.body as {startPositionMs: number}).startPositionMs, 10000);
  await f.runtime.resume(fence);await settle();
  const commands = f.log.filter(request => request.path.endsWith('/transport'));
  assert.equal(commands.length, 1);assert.equal((commands[0]!.body as {command: string}).command, 'play');
  assert.equal((commands[0]!.body as {expectedRevision: string}).expectedRevision, '1');
  assert.equal(f.runtime.getSnapshot().playback.intent, 'playing');
});

test('Together read-only members cannot issue local transport, and the same title in another room is admitted afresh', async t => {
  const f = await runtimeRoom(t, true, false), first = f.runtime.getSnapshot().fence!;
  assert.deepEqual(f.errors, []);
  assert.equal(f.runtime.groupPlaybackMatches(first, 'room_first', 'group_entry'), true);
  assert.equal(f.runtime.groupPlaybackMatches(first, 'foreign_room', 'group_entry'), false);
  assert.equal(f.runtime.groupPlaybackMatches(first, 'room_first', 'foreign_entry'), false);
  assert.equal(f.runtime.getSnapshot().presentation!.controls.includes('pause'), false);
  await assert.rejects(f.runtime.pause(first), /playback_command_unavailable/);
  assert.equal(f.log.filter(request => request.path.endsWith('/transport')).length, 0);
  await f.nextRoom();
  const second = f.runtime.getSnapshot().fence!;
  assert.notEqual(second.sessionId, first.sessionId, 'room occurrence cannot inherit a same-item old engine fence');
  assert.equal(f.runtime.groupPlaybackMatches(first, 'room_first', 'group_entry'), false);
  assert.equal(f.runtime.groupPlaybackMatches(second, 'room_second', 'group_entry'), true);
  await assert.rejects(f.runtime.pause(first), /stale_playback_command/);
  assert.equal(f.log.filter(request => request.method === 'POST' && request.path === '/v1/queues').length, 2);
});


test('Together never executes an unattended member skip locally; a permitted manual skip uses the real room transport', async t => {
  const f = await runtimeRoom(t, true, true, true), fence = f.runtime.getSnapshot().fence!;
  if (fence.kind !== 'queue') assert.fail('actual queue fence');
  await f.service.transport('seek', {positionUs: '40000000'});await settle();
  f.video.land(40);f.video.advance(40);await settle();
  assert.equal(f.runtime.getSnapshot().presentation!.policy!.prompt?.kind, 'skip');
  assert.equal(f.runtime.getSnapshot().playback.pendingSeek, undefined, 'device autoplay preferences cannot change the group clock');
  await f.runtime.policyAction(fence, {kind: 'skip', markerId: 'intro'});await settle();
  const commands = f.log.filter(request => request.path.endsWith('/transport') && (request.body as {command: string}).command === 'seek');
  assert.deepEqual(commands.map(request => (request.body as {positionUs: string}).positionUs), ['40000000', '79000000']);
  assert.equal(f.runtime.getSnapshot().playback.pendingSeek!.positionSeconds, 79, 'the canonical follower applies the accepted room clock');
  assert.equal(f.log.some(request => request.path.endsWith(':advance')), false);
});

test('Together completion reports the actual room end without a private replay or queue advance', async t => {
  const f = await runtimeRoom(t, true), fence = f.runtime.getSnapshot().fence!;
  f.video.finish();await settle();
  assert.equal(f.runtime.getSnapshot().playback.phase, 'ended');
  assert.equal(f.runtime.getSnapshot().presentation!.finished, false, 'the room owns its next occurrence');
  assert.equal(f.runtime.getSnapshot().presentation!.controls.includes('replay'), false);
  assert.equal(f.runtime.getSnapshot().presentation!.policy!.endCountdown, undefined);
  f.clock.advance(60000);await settle();
  assert.deepEqual(f.runtime.getSnapshot().fence, fence);
  assert.equal(f.log.filter(request => request.method === 'POST' && request.path === '/v1/queues').length, 1);
  assert.equal(f.log.some(request => request.path.endsWith(':advance')), false);
  await f.service.transport('seek', {positionUs: String(f.video.duration * 1000000)});await settle();
  assert.ok(f.log.some(request => request.path.endsWith('/readiness') && (request.body as {ended?: boolean}).ended === true), 'the canonical follower reports a genuine ended observation against the current room revision');
});

test('Watch Feedback retains the actual item and full session fence and retires on a newer source', async t => {
  const f = setup(t);await f.runtime.play('movie');await f.arrive();
  const shown = f.runtime.getSnapshot(), binding = {runtime: f.runtime, environment: {locale: 'en-US'}, artwork: {} as never,
    skips: {back: 10, forward: 30}, reportError: () => {}};
  const target = watchFeedbackTarget(binding, shown)!;
  assert.ok(target);assert.equal(target.item.id, shown.session!.itemId);assert.equal(target.item.title, 'movie');
  assert.equal(target.playback.fence, shown.fence);assert.equal(target.playback.isCurrent(), true);
  let received: unknown, allowed = true;
  const port = {available: () => allowed, open: (value: unknown) => {received = value;}};
  assert.equal(dispatchWatchFeedback(target, port), true);assert.equal(received, target, 'one domain dialog receives the original target');
  received = undefined;allowed = false;
  assert.equal(dispatchWatchFeedback(target, port), false);assert.equal(received, undefined);allowed = true;
  let changes = 0;const off = target.playback.subscribe(() => {changes++;});
  await f.runtime.play('next_movie');await f.arrive();off();
  assert.ok(changes > 0);assert.equal(target.playback.isCurrent(), false);assert.equal(watchFeedbackTarget(binding, shown), undefined);
  assert.equal(dispatchWatchFeedback(target, port), false);assert.equal(received, undefined, 'a retired entry cannot become anonymous feedback or target newer playback');
  assert.equal(watchFeedbackTarget(binding, {...f.runtime.getSnapshot(), session: {...f.runtime.getSnapshot().session!, itemId: 'foreign'}}), undefined);
  await f.runtime.playFromGuide(homeChannel.channel, {signal: new AbortController().signal, isCurrent: () => true});await f.arriveLive();
  assert.equal(watchFeedbackTarget(binding, f.runtime.getSnapshot()), undefined, 'a channel is not a fabricated title/session target');
});

test('a complete opaque local queue uses the same media owner and real local receipts without server sessions or first-track aliasing', async t => {
  const port = new ControlledLocalMedia(), f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
  let releases = 0;
  await f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {releases++;}}, {signal: new AbortController().signal, isCurrent: () => true});
  const shown = f.runtime.getSnapshot(), first = shown.fence!;
  assert.equal(f.runtime.canPlayLocalQueue(), true);
  assert.equal(first.kind, 'local');assert.equal(shown.session, undefined);assert.equal(shown.queueView, null);assert.equal(shown.offers, null);assert.equal(shown.options, null);
  assert.equal(shown.presentation!.subject.kind, 'music');assert.equal(shown.presentation!.positionSeconds, 0);
  assert.equal(shown.presentation!.controls.includes('volume'), false, 'the native local port has no browser-video volume capability');
  await f.runtime.pause(first);assert.equal(f.runtime.getSnapshot().presentation!.playing, false);
  await f.runtime.seek(first, 12);await f.runtime.skip(first, 3);assert.equal(f.runtime.getSnapshot().presentation!.positionSeconds, 15);
  await f.runtime.resume(first);assert.equal(f.runtime.getSnapshot().presentation!.playing, true);
  await f.runtime.next(first);const second = f.runtime.getSnapshot().fence!;
  assert.notEqual(second.sessionId, first.sessionId);await assert.rejects(f.runtime.pause(first), /stale_playback_command/);
  port.emit({state: 'ended', positionMs: 100000});await settle();
  assert.equal(f.runtime.getSnapshot().local!.index, 2);assert.equal(f.runtime.getSnapshot().presentation!.positionSeconds, 0);
  assert.deepEqual(port.loads.map(source => source.entryId), ['download_1', 'download_2', 'download_3']);
  assert.equal(f.log.some(request => request.method === 'POST' && (request.path === '/v1/queues' || request.path === '/v1/playback/sessions')), false);
  await f.runtime.stop(f.runtime.getSnapshot().fence!);await settle();assert.equal(releases, 1);assert.equal(f.runtime.getSnapshot().presentation, undefined);
});

test('local audio bookmarks keep their own verified preparation and restore paused through a fresh native acknowledgement', async t => {
  const port = new ControlledLocalMedia(), f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
  let audioReleases = 0, videoReleases = 0, actionCurrent = true;
  const action = new AbortController();
  await f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {audioReleases++;}}, {signal: action.signal, isCurrent: () => actionCurrent});
  actionCurrent = false;action.abort();
  assert.equal(port.contexts.at(-1)!.signal.aborted, false, 'the retired initiating action no longer owns the acknowledged native load');
  assert.equal(port.contexts.at(-1)!.isCurrent(), true, 'the actual captured runtime owns playback after successful handoff');
  const audio = f.runtime.getSnapshot().fence!;await f.runtime.seek(audio, 22);
  const movie = localPreparation(), row = movie.items[0]!;
  await f.runtime.playLocalQueue({preparation: {...movie, preparationId: 'movie_preparation', items: [{...row, title: {...row.title, kind: 'movie'}, itemId: 'stored_movie'}]},
    release: async () => {videoReleases++;}}, {signal: new AbortController().signal, isCurrent: () => true});
  assert.equal(audioReleases, 0);assert.deepEqual(f.runtime.getSnapshot().dock.parkedAudio!.fence, audio);
  const restored = await f.runtime.restoreBookmark(audio);await settle();
  assert.equal(restored.kind, 'local');assert.notEqual(restored.sessionId, audio.sessionId);assert.equal(f.runtime.getSnapshot().presentation!.playing, false);
  assert.equal(f.runtime.getSnapshot().presentation!.positionSeconds, 22);assert.equal(port.loads.at(-1)!.autoplay, false);
  assert.equal(port.loads.at(-1)!.preparationId, 'actual_preparation');assert.equal(port.loads.at(-1)!.generation, 1);
  await f.runtime.stop(restored);await settle();assert.equal(audioReleases, 1);
  f.runtime.dispose();await settle();assert.equal(videoReleases, 1);
});


test('an absent local media port refuses before replacing the real current server source and releases only the offered preparation', async t => {
  const f = setup(t, {}, {}, localScope);await f.runtime.play('movie');await f.arrive();
  const current = f.runtime.getSnapshot().fence!;let releases = 0;
  assert.equal(f.runtime.canPlayLocalQueue(), false);
  await assert.rejects(f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {releases++;}},
    {signal: new AbortController().signal, isCurrent: () => true}), /local_playback_unavailable/);
  assert.equal(releases, 1);assert.deepEqual(f.runtime.getSnapshot().fence, current);
});


test('the app local queue attachment captures the actual family, follows mounted support and retires with its runtime', async t => {
  const port = new ControlledLocalMedia();
  let attached: import('../src/platform/playback/types.ts').WebLocalQueueController | undefined;
  let captured: WebPlaybackSource['localScope'], detached = 0, changes = 0, initialSupport: boolean | undefined;
  const scope = {...localScope};
  const f = setup(t, {}, {createPlayer: () => port, createRender: null, attachLocalQueue: (actual, controller) => {
    captured = actual;attached = controller;initialSupport = controller.canPlayLocalQueue();controller.subscribe(() => {changes++;});
    return () => {detached++;assert.equal(controller.canPlayLocalQueue(), false);};
  }}, scope);
  assert.deepEqual(captured, localScope);assert.notEqual(captured, scope);assert.ok(Object.isFrozen(captured));
  scope.sessionFamilyId = 'replacement_family';assert.equal(captured!.sessionFamilyId, 'local_family');
  assert.equal(initialSupport, false, 'registration does not advertise a native port before the persistent host mounts');
  assert.equal(attached!.canPlayLocalQueue(), true);assert.ok(changes > 0);
  let released = 0;
  await attached!.playLocalQueue({preparation: localPreparation(), release: async () => {released++;}}, {signal: new AbortController().signal, isCurrent: () => true});
  assert.equal(f.runtime.getSnapshot().fence!.kind, 'local');
  f.runtime.dispose();f.runtime.dispose();await settle();assert.equal(detached, 1);assert.equal(released, 1);
  let staleReleased = 0;
  await assert.rejects(attached!.playLocalQueue({preparation: localPreparation(), release: async () => {staleReleased++;}},
    {signal: new AbortController().signal, isCurrent: () => true}), /playback_authority_retired/);
  assert.equal(staleReleased, 1);assert.equal(attached!.canPlayLocalQueue(), false);
});

test('no app local attachment is installed without a genuine captured family', t => {
  let attachments = 0;
  setup(t, {}, {attachLocalQueue: () => {attachments++;return () => {};}});
  assert.equal(attachments, 0);
});


test('late canceled local adoption releases only its own preparation and preserves the newer local queue', async t => {
  const port = new ControlledLocalMedia(), pending = deferred();
  port.gate = source => source.preparationId === 'actual_preparation' ? pending.hold() : Promise.resolve();
  const f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
  const life = new AbortController();let oldReleased = 0, newReleased = 0;
  const old = f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {oldReleased++;}}, {signal: life.signal, isCurrent: () => true});
  await pending.started;
  const rejected = assert.rejects(old, /retired|stale_selection_context/);
  life.abort();
  await f.runtime.playLocalQueue({preparation: {...localPreparation(), preparationId: 'replacement_preparation'}, release: async () => {newReleased++;}},
    {signal: new AbortController().signal, isCurrent: () => true});
  const current = f.runtime.getSnapshot().fence!, unloads = port.unloads;
  pending.resolve();await rejected;await settle();
  assert.deepEqual(f.runtime.getSnapshot().fence, current);assert.equal(port.unloads, unloads);
  assert.equal(f.runtime.getSnapshot().presentation!.playing, true);assert.equal(oldReleased, 1);assert.equal(newReleased, 0);
  f.runtime.dispose();await settle();assert.equal(newReleased, 1);
});

test('foreign-family or already canceled local preparations cannot replace current playback', async t => {
  const port = new ControlledLocalMedia(), f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
  await f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {}}, {signal: new AbortController().signal, isCurrent: () => true});
  const current = f.runtime.getSnapshot().fence!, loads = port.loads.length;let released = 0;
  await assert.rejects(f.runtime.playLocalQueue({preparation: {...localPreparation(), scope: {...localScope, sessionFamilyId: 'foreign_family'}}, release: async () => {released++;}},
    {signal: new AbortController().signal, isCurrent: () => true}), /local_playback_unavailable/);
  const life = new AbortController();life.abort();
  await assert.rejects(f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {released++;}},
    {signal: life.signal, isCurrent: () => true}), /stale_selection_context/);
  assert.deepEqual(f.runtime.getSnapshot().fence, current);assert.equal(port.loads.length, loads);assert.equal(released, 2);
});

test('a strict same-session queue replan reaches the mounted web controller and adopts its actual new generation',async t=>{
 const f=setup(t);await f.runtime.play('movie');await f.arrive();
 const queue=(f.runtime as unknown as {stack:{queue:import('@core/playback-v1/v1-queue-player.ts').V1QueuePlayer}}).stack.queue;
 const before=f.runtime.getSnapshot(),session=queue.currentSession()!,starts=f.log.filter(r=>r.method==='POST'&&r.path==='/v1/playback/sessions').length;
 const actual=await queue.changePlayback({subtitleTrackId:'s1',subtitleDelivery:'burn',subtitleOffsetMs:-1500},{recoverEnded:false,isCurrent:()=>f.source.isCurrent()});
 assert.equal(actual,queue.currentSession());assert.equal(actual!.id,session.id);assert.deepEqual(actual!.queue,session.queue);assert.ok(actual!.presentation.generation>session.presentation.generation);
 await f.arrive();assert.equal(f.runtime.getSnapshot().session!.id,session.id);assert.equal(f.runtime.getSnapshot().fence!.generation,actual!.presentation.generation);
 assert.equal(f.runtime.getSnapshot().fence!.kind,'queue');assert.equal(f.log.filter(r=>r.method==='POST'&&r.path==='/v1/playback/sessions').length,starts);
 assert.equal(before.fence!.sessionId,f.runtime.getSnapshot().fence!.sessionId);
});

test('a canceled strict web queue replan cannot load a late generation or trigger ended-session restart',async t=>{
 for(const mode of ['late','ended'] as const){
  const f=setup(t);await f.runtime.play('movie');await f.arrive();
  const queue=(f.runtime as unknown as {stack:{queue:import('@core/playback-v1/v1-queue-player.ts').V1QueuePlayer}}).stack.queue;
  const before=f.runtime.getSnapshot().fence!,instances=f.hls.instances.length,starts=f.log.filter(r=>r.method==='POST'&&r.path==='/v1/playback/sessions').length;
  const gate=deferred(),abort=new AbortController();
  f.intercept(async(request,response)=>{if(request.method==='PATCH'&&request.path===`/v1/playback/sessions/${before.sessionId}`){if(mode==='late')await gate.hold();else return {status:410,headers:{},body:{error:{code:'session_ended',retry:'never'}}};}return response;});
  const pending=queue.changePlayback({subtitleTrackId:'s1',subtitleDelivery:'burn'},{recoverEnded:false,signal:abort.signal,isCurrent:()=>f.source.isCurrent()});
  if(mode==='late'){const rejected=assert.rejects(pending,{name:'AbortError'});await gate.started;abort.abort();gate.resolve();await rejected;}
  else await assert.rejects(pending,error=>error instanceof Error&&'status'in error&&error.status===410);
  await settle();assert.equal(f.hls.instances.length,instances);assert.deepEqual(f.runtime.getSnapshot().fence,before);
  assert.equal(f.log.filter(r=>r.method==='POST'&&r.path==='/v1/playback/sessions').length,starts);
 }
});


test('browser media readiness follows the actual loaded HTML source and rejects seeking, retirement and native factories',async t=>{
 const f=setup(t);await f.runtime.play('movie');
 const loading=f.runtime.getSnapshot().fence;if(loading)assert.equal(f.runtime.browserMediaReady(loading),false);
 await f.arrive();const first=f.runtime.getSnapshot().fence!;assert.equal(f.runtime.browserMediaReady(first),true);
 f.video.bufferedRanges=[[0,12],[20,30]];assert.deepEqual(f.runtime.bufferedRanges(first),[{start:0,end:12},{start:20,end:30}]);
 await f.runtime.seek(first,17);assert.equal(f.runtime.browserMediaReady(first),false);assert.deepEqual(f.runtime.bufferedRanges(first),[]);f.video.land(17);await settle();assert.equal(f.runtime.browserMediaReady(first),true);
 await f.runtime.play('other');assert.equal(f.runtime.browserMediaReady(first),false);await f.arrive();assert.equal(f.runtime.browserMediaReady(f.runtime.getSnapshot().fence!),true);
 f.runtime.dispose();assert.equal(f.runtime.browserMediaReady(first),false);assert.deepEqual(f.runtime.bufferedRanges(first),[]);
 const {WebMediaPlayer}=await import('../src/platform/playback/web-media-player.ts');
 const native=setup(t,{}, {createPlayer:(video,options)=>new WebMediaPlayer(video,options),createRender:null});await native.runtime.play('movie');await native.arrive();
 assert.equal(native.runtime.getSnapshot().playback.phase,'ready');assert.equal(native.runtime.browserMediaReady(native.runtime.getSnapshot().fence!),false,'an app-installed media factory never borrows browser transport capabilities');
 assert.deepEqual(native.runtime.bufferedRanges(native.runtime.getSnapshot().fence!),[],'native buffer ranges are never guessed from an idle browser element');
});

test('AirPlay rechecks its actual source at activation instead of trusting cached availability',async t=>{
 const f=setup(t),sender=new ControlledCastSender();let eligible=true,picks=0;
 const controller=new PlayOnController({sender,runtime:f.runtime,isCurrent:f.source.isCurrent,reportError:error=>{throw error;},airPlay:{available:()=>eligible,subscribe:()=>()=>{},showPicker:()=>{picks++;}}});
 t.after(()=>controller.dispose());assert.equal(controller.getSnapshot().airPlayAvailable,true);
 eligible=false;controller.startAirPlay();assert.equal(picks,0);assert.equal(controller.getSnapshot().airPlayAvailable,false);
 eligible=true;controller.startAirPlay();assert.equal(picks,1,'a fresh actual source may be used even before a cached availability event');
});


test('subtitle choices preserve the offered clock exactly and refuse fractional milliseconds before a session write',async t=>{
 for(const [offsetUs,expected] of [['500',undefined],['-1500',undefined],['0',0],['-2000',-2],['2500',undefined]] as const){
  const f=setup(t);
  f.intercept(async(request,response)=>{
   if(!request.path.includes('/playback-offers?'))return response;
   const body=response.body as {current:{sessionId:string;generation:number;sourceId:string};controls:{id:string;enabled:boolean;reason:string}[]};
   const current=body.current;
   const resource={id:'s4',sourceId:current.sourceId,revision:1,scope:'personal',language:'en',title:'English',format:'vtt',origin:'upload',rights:'Own transcript',offsetUs,canManage:true,enabled:true};
   return {...response,body:{...body,current:{...current,subtitleSelection:'track'},controls:body.controls.map(control=>control.id==='subtitles'?{...control,enabled:true,reason:''}:control),subtitlePlan:{version:1,sessionId:current.sessionId,generation:current.generation,sourceId:current.sourceId,revision:1,catalogRevision:1,
    renderer:'external_text',mode:'track',offAvailable:true,offsetUs,selected:{...resource,pinned:true,renderer:'external_text'},documentUrl:'/v1/media/subtitles/grant/s4/1',resources:[resource],discovered:[]}}};
  });
  await f.runtime.play('movie');await f.arrive();const fence=f.runtime.getSnapshot().fence!;
  assert.equal(f.runtime.getSnapshot().offers!.subtitlePlan!.resources[0]!.offsetUs,offsetUs);
  const before=f.log.filter(request=>request.method==='PATCH').length;
  if(expected===undefined){
   await assert.rejects(f.runtime.selectSubtitle(fence,'s4'),/playback_subtitle_unavailable/);
   assert.equal(f.log.filter(request=>request.method==='PATCH').length,before,'an unrepresentable resource correction cannot reach the integer-ms wire');
   await f.runtime.selectSubtitle(fence,null);
   const off=f.log.filter(request=>request.method==='PATCH').at(-1)!.body as {subtitles:{trackId:string|null};subtitleOffsetMs?:number};
   assert.equal(off.subtitles.trackId,null);assert.equal(off.subtitleOffsetMs,undefined,'turning captions off does not need a correction');
  }else{
   await f.runtime.selectSubtitle(fence,'s4');
   const selected=f.log.filter(request=>request.method==='PATCH').at(-1)!.body as {subtitles:{trackId:string};subtitleOffsetMs:number};
   assert.equal(selected.subtitles.trackId,'s4');assert.equal(selected.subtitleOffsetMs,expected);
  }
 }
});

test('catalogue playback uses the same admitted runtime and returns the actual whole-resource QueueHeader',async t=>{
 const {webDeclaredProfile}=await import('../../packages/client-core/src/client-profile.ts');let probes=0;
 const f=setup(t,{}, {capabilities:async()=>{probes++;return webDeclaredProfile({app:'catalogue-test'});}});await settle();
 const before=f.log.length,port=f.runtime.cataloguePlayback();assert.equal(port,f.runtime.cataloguePlayback());assert.equal(f.log.length,before,'obtaining the port starts no work');
 const segments=Object.freeze([{source:{items:{ids:['movie']}}},{source:{container:{kind:'collection',id:'ordered_release'}}}]);
 const header=await port.playSegments(segments,{signal:new AbortController().signal,isCurrent:()=>true});await f.arrive();
 assert.equal(header.id,f.runtime.getSnapshot().queueView!.queue.id);assert.equal(header.total,13,'all server collection members remain in the queue');
 assert.equal(f.runtime.getSnapshot().session!.queue!.queueId,header.id);assert.equal(f.runtime.getSnapshot().session!.itemId,'movie');
 const request=f.log.find(r=>r.method==='POST'&&r.path==='/v1/queues')!;
 assert.deepEqual((request.body as {segments:unknown}).segments,segments);assert.ok(probes>0);assert.ok(f.log.some(r=>r.method==='PUT'&&r.path.endsWith('/capabilities')));
 assert.equal(f.log.some(r=>r.path.startsWith('/v1/home')),false,'catalogue commands do not fabricate a Home context');
 const resumed=await port.playSegments([{source:{items:{ids:['movie']}}}],{startSeconds:33,signal:new AbortController().signal,isCurrent:()=>true});await f.arrive();
 assert.equal(resumed.id,f.runtime.getSnapshot().session!.queue!.queueId);
 assert.equal((f.log.filter(r=>r.method==='POST'&&r.path==='/v1/playback/sessions').at(-1)!.body as {startPositionMs:number}).startPositionMs,33000);
});

test('catalogue playback requires the original signal and currentness before changing media or issuing a request',async t=>{
 const f=setup(t);await f.runtime.play('movie');await f.arrive();const current=f.runtime.getSnapshot().fence!,port=f.runtime.cataloguePlayback(),count=f.log.length;
 const segments=[{source:{items:{ids:['other']}}}],abort=new AbortController();abort.abort();
 for(const options of [undefined,{signal:new AbortController().signal},{isCurrent:()=>true},{signal:abort.signal,isCurrent:()=>true},{signal:new AbortController().signal,isCurrent:()=>false}]){
  await assert.rejects(port.playSegments(segments,options),/stale_selection_context/);assert.equal(f.log.length,count);assert.deepEqual(f.runtime.getSnapshot().fence,current);
 }
 await assert.rejects(port.playSegments([],{signal:new AbortController().signal,isCurrent:()=>true}),/invalid_queue_playback_source/);
 await assert.rejects(port.playSegments(segments,{startSeconds:NaN,signal:new AbortController().signal,isCurrent:()=>true}),/invalid_queue_playback_source/);
 assert.equal(f.log.length,count);assert.deepEqual(f.runtime.getSnapshot().fence,current);
 f.invalidate();await assert.rejects(port.playSegments(segments,{signal:new AbortController().signal,isCurrent:()=>true}),/playback_authority_retired/);assert.equal(f.log.length,count);
});

test('catalogue playback retains original caller guards through queue POST and adoption without stopping a newer Play',async t=>{
 for(const retire of ['abort','member','new-play'] as const){
  const f=setup(t),gate=deferred(),life=new AbortController();let current=true,held=false,sessionId='',signal:AbortSignal|undefined;
  f.intercept(async(request,response)=>{if(!held&&request.method==='POST'&&request.path==='/v1/queues'){
   held=true;signal=request.signal;sessionId=(response.body as {session:{id:string}}).session.id;await gate.hold();
  }return response;});
  const pending=f.runtime.cataloguePlayback().playSegments([{source:{container:{kind:'collection',id:'ordered_release'}}}],{signal:life.signal,isCurrent:()=>current});
  const rejected=assert.rejects(pending,/AbortError|cancelled|retired|stale_selection_context/);await gate.started;
  if(retire==='abort'){life.abort();assert.equal(signal!.aborted,true);}else if(retire==='member')current=false;
  else{await f.runtime.play('newer');await f.arrive();}
  const newer=retire==='new-play'?f.runtime.getSnapshot().fence:undefined;
  gate.resolve();await rejected;await settle();
  assert.equal(f.server.session(sessionId)!.ended,true,'the late actual session is abandoned, not left holding a lease');
  if(newer)assert.deepEqual(f.runtime.getSnapshot().fence,newer);else assert.equal(f.runtime.getSnapshot().presentation,undefined);
 }
});

test('catalogue playback cannot manufacture a queue when actual capability admission fails',async t=>{
 const {webDeclaredProfile}=await import('../../packages/client-core/src/client-profile.ts');let rejected=true;
 const f=setup(t,{}, {capabilities:async()=>{if(rejected)throw new Error('capability probe unavailable');return webDeclaredProfile({app:'catalogue-test'});}});
 const port=f.runtime.cataloguePlayback();
 await assert.rejects(port.playSegments([{source:{items:{ids:['movie']}}}],{signal:new AbortController().signal,isCurrent:()=>true}),/playback_start_failed/);
 assert.equal(f.log.some(r=>r.method==='POST'&&r.path==='/v1/queues'),false);
 assert.equal(f.runtime.getSnapshot().queueView,null);assert.equal(f.runtime.getSnapshot().presentation,undefined);
 rejected=false;const header=await port.playSegments([{source:{items:{ids:['movie']}}}],{signal:new AbortController().signal,isCurrent:()=>true});await f.arrive();
 assert.equal(f.runtime.getSnapshot().queueView!.queue.id,header.id,'the same captured port can retry after a genuine successful admission');
});


test('a start is admitted only by an accepted capability publication, retried once (FMT-02)',async t=>{
 const {webDeclaredProfile}=await import('../../packages/client-core/src/client-profile.ts');let refusals=2,puts=0;
 const f=setup(t,{}, {capabilities:async()=>webDeclaredProfile({app:'fmt02-test'})});
 f.intercept(async(request,response)=>{
  if(request.method!=='PUT'||!request.path.includes('capabilities'))return response;
  puts++;if(refusals<=0)return response;refusals--;
  return {status:503,headers:{},body:{error:{code:'unavailable',message:'unavailable'}}};
 });
 const port=f.runtime.cataloguePlayback();
 await assert.rejects(port.playSegments([{source:{items:{ids:['movie']}}}],{signal:new AbortController().signal,isCurrent:()=>true}),/playback_start_failed/);
 assert.equal(puts,2,'the same document is published at most twice');
 assert.equal(f.log.some(r=>r.method==='POST'&&r.path==='/v1/queues'),false,'a refused publication never creates a queue');
 refusals=1;puts=0;
 const header=await port.playSegments([{source:{items:{ids:['movie']}}}],{signal:new AbortController().signal,isCurrent:()=>true});await f.arrive();
 assert.equal(puts,2,'one failure is retried once');
 assert.equal(f.runtime.getSnapshot().queueView!.queue.id,header.id);
});

const committedInfo=()=>({route:'original',routes:{picture:'original',sound:'original'},reason:{kind:'compatible'},
 source:{video:{codec:'h264',width:1920,height:1080,interlaced:false},audio:{codec:'flac',sampleRate:96000,bitDepth:24,channels:2}},
 delivered:{video:{codec:'h264',width:1920,height:1080,interlaced:false},audio:{codec:'flac',sampleRate:96000,bitDepth:24,channels:2}},
 losses:[],network:'home',basis:'committed_delivery_plan'});

test('Info reads a fresh authorized session without adopting it or merging output with media facts',async t=>{
 const f=setup(t);await f.runtime.play('movie',60);await f.arrive();
 const fence=f.runtime.getSnapshot().fence!;assert.equal(fence.kind,'queue');if(fence.kind!=='queue')return;
 const original=f.runtime.getSnapshot().session;let report:unknown=committedInfo(),observed:unknown={audio:{handling:'decoded',channels:2,sampleRate:48000}};
 const path=`/v1/playback/sessions/${fence.sessionId}`;
 f.intercept(async(request,response)=>request.method==='GET'&&request.path===path?{...response,body:{...(response.body as object),playbackInfo:report,observedPlayback:observed}}:response);
 const start=f.log.length,caller=new AbortController();
 const first=await f.runtime.readPlaybackInfo(fence,caller.signal);assert.ok(first);assert.equal(first.isCurrent(),true);
 assert.deepEqual(first.playbackInfo,committedInfo());assert.equal(first.playbackInfo?.delivered.audio?.sampleRate,96000);
 assert.equal(first.observedPlayback?.audio?.sampleRate,48000);assert.equal(first.sessionRevision,original?.revision);
 assert.equal(f.runtime.getSnapshot().session,original,'GET never adopts a report response into the queue');
 assert.deepEqual(f.log.slice(start).map(r=>[r.method,r.path]),[['GET',path]]);
 observed={};report=undefined;
 const cleared=await f.runtime.readPlaybackInfo(fence,caller.signal);assert.ok(cleared);assert.equal(cleared.playbackInfo,undefined);assert.deepEqual(cleared.observedPlayback,{});
 report={...committedInfo(),route:'not-a-route'};
 const malformed=await f.runtime.readPlaybackInfo(fence,caller.signal);assert.ok(malformed);assert.equal(malformed.playbackInfo,undefined,'malformed optional facts never replace a valid current session');
 await f.runtime.play('another',0);assert.equal(first.isCurrent(),false);assert.equal(cleared.isCurrent(),false);
});

test('Info rejects fresh resources outside the captured occurrence, revision, generation and selected version',async t=>{
 const f=setup(t);await f.runtime.play('movie',60);await f.arrive();const fence=f.runtime.getSnapshot().fence!;if(fence.kind!=='queue')throw Error('missing queue fence');
 const original=f.runtime.getSnapshot().session!,path=`/v1/playback/sessions/${fence.sessionId}`;
 for(const patch of [{id:'foreign'},{revision:String(Number(original.revision)+1)},{itemId:'foreign'},
   {versionId:'foreign'},{role:'receiver'},{state:'ended'},
   {presentation:{...original.presentation,generation:fence.generation+1}},
   {queue:{...original.queue,entryId:'foreign'}},{queue:{...original.queue,queueId:'foreign'}}]){
  f.intercept(async(request,response)=>request.method==='GET'&&request.path===path?{...response,body:{...(response.body as object),...patch,playbackInfo:committedInfo()}}:response);
  assert.equal(await f.runtime.readPlaybackInfo(fence,new AbortController().signal),null);
  assert.equal(f.runtime.getSnapshot().session,original);
 }
});

test('Info discards delayed reads on original abort, authority retirement or replacement Play',async t=>{
 for(const retire of ['abort','authority','play'] as const)await t.test(retire,async sub=>{
  const f=setup(sub);await f.runtime.play('movie',60);await f.arrive();const fence=f.runtime.getSnapshot().fence!;if(fence.kind!=='queue')throw Error('missing queue fence');
  const path=`/v1/playback/sessions/${fence.sessionId}`,gate=deferred(),caller=new AbortController();
  f.intercept(async(request,response)=>{if(request.method==='GET'&&request.path===path){await gate.hold();return {...response,body:{...(response.body as object),playbackInfo:committedInfo()}};}return response;});
  const pending=f.runtime.readPlaybackInfo(fence,caller.signal).catch(error=>{assert.equal(error.name,'AbortError');return null;});
  await gate.started;
  if(retire==='abort')caller.abort();else if(retire==='authority')f.invalidate();else await f.runtime.play('new',0);
  const after=f.runtime.getSnapshot().session;gate.resolve();assert.equal(await pending,null);
  assert.equal(f.runtime.getSnapshot().session,after,'old Info cannot adopt or stop a replacement');
 });
});

test('Info advertises no VOD report for an audio session or an already canceled caller',async t=>{
 const f=setup(t);await f.runtime.play('movie',0);await f.arrive();let fence=f.runtime.getSnapshot().fence!;if(fence.kind!=='queue')throw Error('missing queue fence');
 const abort=new AbortController();abort.abort();let before=f.log.length;
 assert.equal(await f.runtime.readPlaybackInfo(fence,abort.signal),null);assert.equal(f.log.length,before);
 const old=fence;await f.runtime.play('song-one',0);await f.arrive();fence=f.runtime.getSnapshot().fence!;if(fence.kind!=='queue')throw Error('missing audio queue fence');
 before=f.log.length;assert.equal(await f.runtime.readPlaybackInfo(fence,new AbortController().signal),null);assert.equal(f.log.length,before);
 await assert.rejects(f.runtime.readPlaybackInfo(old,new AbortController().signal),/stale_playback_command/);
});

test('an audiobook stored resume reaches the server as intent while chapter and restart positions stay explicit',async t=>{
 const f=setup(t),abort=new AbortController(),context={signal:abort.signal,isCurrent:()=>true};
 await f.runtime.playFromScreen({libraryId:'library',itemId:'book-part',startSeconds:0,storedResume:true},context);
 const first=f.log.find(request=>request.method==='POST'&&request.path==='/v1/queues')!;
 assert.ok((first.body as any).startPlayback,'atomic queue start uses the server default stored-resume intent');
 assert.equal((first.body as any).startPlayback.startPositionMs,undefined);
 const before=f.log.length;
 await f.runtime.playFromScreen({libraryId:'library',itemId:'book-part',startSeconds:9},context);
 const chapter=f.log.slice(before).find(request=>request.method==='POST'&&request.path==='/v1/playback/sessions')!;
 assert.equal((chapter.body as any).startPositionMs,9000);
 assert.equal((chapter.body as any).startFrom,undefined);
 await assert.rejects(f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0,storedResume:true},context),/invalid_playback_target/);
});

test('Root50 disclosure refusal clears Web remote caches and ignores a late metadata response without Stop',async t=>{
 const f=setup(t),sender=new ControlledCastSender();let resolve!:(value:{title:string})=>void;
 const metadata=new Promise<{title:string}>(done=>{resolve=done;});
 const controller=new PlayOnController({sender,runtime:f.runtime,isCurrent:f.source.isCurrent,reportError:error=>{throw error;},describeItem:()=>metadata,now:f.clock.now,setTimer:f.clock.setTimer,clearTimer:f.clock.clearTimer});t.after(()=>controller.dispose());
 sender.publish({phase:'casting',controlRevision:1,itemId:'movie',title:'Admitted title',receiver:{itemId:'movie',positionSeconds:9,durationSeconds:200,paused:false}});
 assert.equal(controller.getSnapshot().remote?.itemId,'movie');
 sender.publish({phase:'off',controlRevision:1,disclosureRefused:true,itemId:undefined,title:undefined,receiver:undefined});
 resolve({title:'Late title'});await settle();assert.equal(controller.getSnapshot().remote,undefined);assert.equal(controller.getSnapshot().panel,null);assert.equal(sender.commands.some(command=>command[0]==='stop'),false);
});


test('Cast transport loss clears the remote instead of offering a false stopped continuation',async t=>{
 const f=setup(t),{controller,sender}=castController(t,f);
 sender.publish({phase:'casting',itemId:'movie',title:'Actual title',receiver:{itemId:'movie',positionSeconds:57,durationSeconds:3186,paused:false}});
 const token=controller.getSnapshot().remote!;assert.equal(token.phase,'playing');
 sender.publish({phase:'off',remoteStopped:false,itemId:undefined,reported:undefined});
 assert.equal(controller.getSnapshot().remote,undefined);await controller.continueHere(token);assert.equal(f.runtime.getSnapshot().session,undefined);
});


test('Cast picker refresh follows playback and seeks while refusing every replaced source or queue witness', async t => {
  const f = setup(t);await f.runtime.play('movie', 25);await f.arrive();
  const original = f.runtime.getSnapshot(), {controller, sender} = castController(t, f);
  const started = controller.startCast(), read = sender.starts[0]![4]!;
  assert.equal(read(), original.presentation!.positionSeconds);
  f.video.advance(64);await settle();assert.equal(read(), 64);
  await f.runtime.seek(original.fence!, 120);f.video.land(120);await settle();assert.equal(read(), 120);
  const latest = f.runtime.getSnapshot(), queue = latest.queueView!.queue, session = latest.session!;
  for (const changed of [
    {...latest, authority: {...latest.authority}}, {...latest, disposed: true},
    {...latest, playback: {...latest.playback, playbackRun: latest.playback.playbackRun + 1}},
    {...latest, fence: {...latest.fence!, sourceId: 'replaced'}},
    {...latest, session: {...session, id: 'replaced'}},
    {...latest, session: {...session, presentation: {...session.presentation, generation: session.presentation.generation + 1}}},
    {...latest, queueView: {...latest.queueView!, queue: {...queue, revision: 'replaced'}}},
    {...latest, queueView: {...latest.queueView!, queue: {...queue, currentEntryId: 'duplicate-item-occurrence'}}},
    {...latest, queueView: {...latest.queueView!, queue: {...queue, currentPlaybackId: 'replaced'}}},
  ]) assert.equal(castSourcePosition(original, changed), undefined);
  sender.cancelConnecting();assert.equal(await started, false);assert.equal(read(), undefined);
  const next = controller.startCast(), nextRead = sender.starts.at(-1)![4]!;
  await f.runtime.play('another', 10);await f.arrive();assert.equal(await next, false);assert.equal(nextRead(), undefined);
});

// WEB002 uses the existing real runtime/service/queue/player fixture. Only I/O,
// timers and the media element's external events are controlled by these tests.
test('WEB002 real admission retains before the first await and dispose balances every original start', async t => {
  const lifetime = (signal: AbortSignal) => ({signal, isCurrent: () => true});
  const cases: {name: string; path: string; run(f: ReturnType<typeof setup>, signal: AbortSignal): Promise<unknown>}[] = [
    {name:'play',path:'/v1/queues',run:(f)=>f.runtime.play('movie')},
    {name:'start',path:'/v1/queues',run:(f)=>f.runtime.start({entry:selectionLeaf('movie').entry,target:{itemId:'movie'}})},
    {name:'screen item preflight',path:'/v1/items/movie',run:(f,signal)=>f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0},lifetime(signal))},
    {name:'screen options preflight',path:'/v1/items/movie/playback-options',run:(f,signal)=>f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0},lifetime(signal))},
    {name:'version options preflight',path:'/v1/items/movie/playback-options',run:(f,signal)=>f.runtime.playVersion({itemId:'movie',versionId:'v1',context:selectionContext,signal},()=>true)},
    {name:'catalogue queue',path:'/v1/queues',run:(f,signal)=>f.runtime.cataloguePlayback().playSegments([{source:{items:{ids:['movie']}}}],lifetime(signal))},
    {name:'resource queue',path:'/v1/queues',run:(f,signal)=>f.runtime.playFromQueue({container:{kind:'collection',id:'ordered_release'}},lifetime(signal))},
    {name:'selection',path:'/v1/queues',run:(f,signal)=>f.runtime.selectionPlayback(()=>true).play({context:selectionContext,items:[selectionLeaf('movie')],signal})},
    {name:'selection enqueue',path:'/v1/queues',run:(f,signal)=>f.runtime.selectionPlayback(()=>true).enqueue({context:selectionContext,items:[selectionLeaf('movie')],placement:'end',signal})},
    {name:'catalogue enqueue',path:'/v1/queues',run:(f,signal)=>f.runtime.catalogueQueue()!.enqueueSegments([{source:{items:{ids:['movie']}}}],'end',lifetime(signal))},
    {name:'Home channel',path:'/v1/playback/sessions',run:(f,signal)=>f.runtime.playChannel(homeChannel,selectionContext,signal,()=>true)},
    {name:'guide channel',path:'/v1/playback/sessions',run:(f,signal)=>f.runtime.playFromGuide(homeChannel.channel,lifetime(signal))},
  ];
  for (const entry of cases) await t.test(entry.name, async sub => {
    const f=setup(sub),gate=deferred(),caller=new AbortController(),changes:boolean[]=[];
    f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
    await settle();assert.equal(f.runtime.retainsMedia(),false,'constructed and mounted idle runtime is not media');
    let held=false;
    f.intercept(async(request,response)=>{if(!held&&request.path===entry.path){held=true;await gate.hold();}return response;});
    const pending=entry.run(f,caller.signal).catch(()=>{});
    assert.equal(f.runtime.retainsMedia(),true,'same synchronous admission turn, before any request settles');
    assert.deepEqual(changes,[true]);await gate.started;
    assert.equal(f.runtime.retainsMedia(),true);assert.equal(f.runtime.getSnapshot().session,undefined);
    f.runtime.dispose();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
    caller.abort();gate.resolve();await pending;await settle();
    assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false],'late response/finally cannot resurrect or decrement another start');
  });
});

test('WEB002 pre-abort, failed preflight and superseded flights never leak retention', async t => {
  const f=setup(t),changes:boolean[]=[];f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));await settle();
  await f.runtime.onTransition({state:f.runtime.dock.getSnapshot(),effects:[]});
  assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[],'an empty dock transition is not media admission');
  const caller=new AbortController();caller.abort();const before=f.log.length;
  await assert.rejects(f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0},{signal:caller.signal,isCurrent:()=>true}));
  await assert.rejects(f.runtime.playVersion({context:selectionContext,itemId:'movie',versionId:'v1',signal:caller.signal},()=>true));
  assert.equal(f.runtime.retainsMedia(),false);assert.equal(f.log.length,before);assert.deepEqual(changes,[]);
  f.intercept(async(request,response)=>request.path==='/v1/items/movie'?{status:404,headers:{},body:{error:{code:'not_found'}}}:response);
  await assert.rejects(f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0},{signal:new AbortController().signal,isCurrent:()=>true}));
  assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
  const gate=deferred();f.intercept(async(request,response)=>{if(request.path==='/v1/items/movie')await gate.hold();return response;});
  const old=f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0},{signal:new AbortController().signal,isCurrent:()=>true});
  const rejected=assert.rejects(old);await gate.started;
  await f.runtime.play('newer');await f.arrive();const fence=f.runtime.getSnapshot().fence!;
  await f.runtime.stop(fence);await settle();
  assert.equal(f.runtime.retainsMedia(),false,'a superseded preflight ignoring abort cannot retain a drained successor');
  gate.resolve();await rejected;assert.equal(f.runtime.retainsMedia(),false);
  assert.deepEqual(changes,[true,false,true,false]);
});

test('WEB002 caller abort releases an item/options preflight even if its I/O ignores abort', async t => {
  for(const path of ['/v1/items/movie','/v1/items/movie/playback-options'])await t.test(path,async sub=>{
    const f=setup(sub),gate=deferred(),caller=new AbortController(),changes:boolean[]=[];
    f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
    f.intercept(async(request,response)=>{if(request.path===path)await gate.hold();return response;});
    const pending=f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0},{signal:caller.signal,isCurrent:()=>true});
    const rejected=assert.rejects(pending);assert.equal(f.runtime.retainsMedia(),true);await gate.started;
    caller.abort();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
    gate.resolve();await rejected;assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
  });
});

test('WEB002 actual playing, paused, expanded and retained bookmark resources notify only retention transitions', async t => {
  const f=setup(t),changes:boolean[]=[];const off=f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
  assert.equal(f.runtime.retainsMedia(),false);await f.runtime.play('movie',20);const engine=await f.arrive();
  const fence=f.runtime.getSnapshot().fence!;assert.equal(f.runtime.retainsMedia(),true);
  await f.runtime.pause(fence);assert.equal(f.runtime.getSnapshot().presentation!.playing,false);assert.equal(f.runtime.retainsMedia(),true);
  f.runtime.collapse(fence);f.runtime.open(fence);f.video.advance(24);f.video.fire('progress');await settle();
  assert.equal(f.runtime.retainsMedia(),true);assert.equal(engine.destroyed,false);assert.deepEqual(changes,[true]);
  await f.runtime.play('song_one',10);await f.arrive();assert.ok(f.runtime.getSnapshot().dock.resumeVideo);
  await f.runtime.play('other',30);await f.arrive();assert.ok(f.runtime.getSnapshot().dock.parkedAudio);assert.ok(f.runtime.getSnapshot().dock.resumeVideo);
  assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
  f.runtime.dispose();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);off();
});

test('WEB002 actual local preparation, paused lease and final drain retain without a server session', async t => {
  const port=new ControlledLocalMedia(),gate=deferred();port.gate=()=>gate.hold();
  const f=setup(t,{}, {createPlayer:()=>port,createRender:null},localScope),changes:boolean[]=[];let released=0;
  f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
  const pending=f.runtime.playLocalQueue({preparation:localPreparation(),release:async()=>{released++;}},{signal:new AbortController().signal,isCurrent:()=>true});
  assert.equal(f.runtime.retainsMedia(),true);await gate.started;assert.equal(f.runtime.getSnapshot().session,undefined);
  gate.resolve();await pending;const fence=f.runtime.getSnapshot().fence!;assert.equal(fence.kind,'local');
  await f.runtime.pause(fence);assert.equal(port.observe().state,'paused');assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
  await f.runtime.stop(fence);await settle();assert.equal(released,1);assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
});

test('WEB002 actual capacity wait and preparation retain until the original start is cancelled', async t => {
  for(const mode of ['capacity','preparation'] as const)await t.test(mode,async sub=>{
    const f=setup(sub),caller=new AbortController(),changes:boolean[]=[];
    f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
    f.intercept(async(request,response)=>request.method==='POST'&&request.path===(mode==='capacity'?'/v1/queues':'/v1/playback/sessions')
      ? mode==='capacity'?{status:429,headers:{'Retry-After':'1'},body:{error:{code:'conversion_capacity',retry:'same_request'}}}
        :{status:202,headers:{},body:{retryAfterMs:1000}}:response);
    const pending=f.runtime.cataloguePlayback().playSegments([{source:{items:{ids:['movie']}}}],{signal:caller.signal,isCurrent:()=>true,...(mode==='preparation'?{startSeconds:0}:{})});
    const rejected=assert.rejects(pending);await settle();const state=f.runtime.getSnapshot();
    assert.equal(f.runtime.retainsMedia(),true);assert.equal(state.session,undefined);
    if(mode==='capacity')assert.equal(state.capacityWait?.isCurrent(),true);else assert.ok(state.playback.preparation|| (f.runtime as unknown as {stack:{queue:{getSnapshot():{phase:string}}}}).stack.queue.getSnapshot().phase==='preparing');
    caller.abort();await rejected;await settle();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
  });
});

// These imports deliberately name the final integration paths. The fact owner
// and its additive Home/core APIs are supplied by P2-50b-shell/P2-50a.
test('WEB002 EACH five same-authority hints preserve actual media owners in all retained states; real Home fence retires it', async t => {
  const {createShellFactOwner,shellAccount}=await import('../src/shell/facts.ts');
  const {FreshnessHub}=await import('@core/freshness-hub.ts');
  const {HomeService}=await import('@core/home-service.ts');
  const {HomeDocumentResources}=await import('../src/shell/home-resources.ts');
  const {parseDirectSnapshot}=await import('@core/profile-management.ts');
  const {GUIDE_PROTOCOL}=await import('@core/channel-guide.ts');
  const {WebMediaPlayer}=await import('../src/platform/playback/web-media-player.ts');
  for(const state of ['playing','paused','expanded','parked','resume','local','remote','item-preflight','options-preflight','version-preflight','preparing','capacity'] as const)await t.test(state,async sub=>{
    let player!:InstanceType<typeof WebMediaPlayer>|ControlledLocalMedia;
    const local=state==='local'?new ControlledLocalMedia():undefined;
    const f=setup(sub,{}, {createPlayer:(video,options)=>player=local??new WebMediaPlayer(video,options),createRender:null,subtitleSurface:false},local?localScope:undefined);
    const caller=new AbortController(),gate=deferred();let pending:Promise<unknown>|undefined;
    let remote:ReturnType<typeof castController>|undefined;
    sub.after(async()=>{caller.abort();gate.resolve();await pending;});
    if(state==='local'){
      await f.runtime.playLocalQueue({preparation:localPreparation(),release:async()=>{}},{signal:caller.signal,isCurrent:()=>true});
      await f.runtime.pause(f.runtime.getSnapshot().fence!);
    }else if(state==='remote'){
      remote=castController(sub,f);
      remote.sender.publish({phase:'casting',itemId:'movie',title:'Actual remote',receiver:{itemId:'movie',positionSeconds:43,durationSeconds:3186,paused:true}});
    }else if(['item-preflight','options-preflight','version-preflight','preparing','capacity'].includes(state)){
      f.intercept(async(request,response)=>{
        if(state==='capacity'&&request.method==='POST'&&request.path==='/v1/queues')return {status:429,headers:{'Retry-After':'1'},body:{error:{code:'conversion_capacity',retry:'same_request'}}};
        if(state==='preparing'&&request.method==='POST'&&request.path==='/v1/playback/sessions')return {status:202,headers:{},body:{retryAfterMs:1000}};
        if((state==='item-preflight'&&request.path==='/v1/items/movie')||(['options-preflight','version-preflight'].includes(state)&&request.path==='/v1/items/movie/playback-options'))await gate.hold();
        return response;
      });
      pending=(state==='version-preflight'
        ?f.runtime.playVersion({context:selectionContext,itemId:'movie',versionId:'v1',signal:caller.signal},()=>true)
        :state==='capacity'||state==='preparing'
          ?f.runtime.cataloguePlayback().playSegments([{source:{items:{ids:['movie']}}}],{signal:caller.signal,isCurrent:()=>true,...(state==='preparing'?{startSeconds:0}:{})})
          :f.runtime.playFromScreen({libraryId:'library',itemId:'movie',startSeconds:0},{signal:caller.signal,isCurrent:()=>true})).catch(()=>{});
      if(['preparing','capacity'].includes(state))await settle();else await gate.started;
    }else{
      if(state==='parked'){await f.runtime.play('song_one',12);await f.arrive();}
      await f.runtime.play('movie',37);await f.arrive();
      if(state==='resume'){await f.runtime.play('song_two',12);await f.arrive();await f.runtime.stop(f.runtime.getSnapshot().fence!);}
      else{
        const fence=f.runtime.getSnapshot().fence!;
        if(state!=='playing')await f.runtime.pause(fence);
        if(state==='expanded')f.runtime.open(fence);else f.runtime.collapse(fence);
        f.video.advance(43);await settle();
      }
    }
    const engine=f.hls.instances.findLast(instance=>!instance.destroyed);
    assert.equal(f.runtime.retainsMedia(),true,state+' must have real retained ownership before any hint');
    if(state==='local')assert.equal(local!.observe().state,'paused');
    if(state==='remote')assert.equal(remote!.controller.getSnapshot().remote!.paused,true);
    if(state==='resume'){assert.equal(f.runtime.getSnapshot().session,undefined);assert.ok(f.runtime.getSnapshot().dock.resumeVideo);}
    if(state==='parked')assert.ok(f.runtime.getSnapshot().dock.parkedAudio);
    if(state==='capacity')assert.equal(f.runtime.getSnapshot().capacityWait?.isCurrent(),true);
    if(state==='preparing')assert.ok(f.runtime.getSnapshot().playback.preparation);
    const stack=(f.runtime as unknown as {stack:{service:import('@core/index.ts').PlaybackService;queue:object;web:{dispose():void;slot:object}}}).stack;
    const pause=sub.mock.method(stack.service,'pause'),leave=sub.mock.method(stack.service,'leave'),dispose=sub.mock.method(stack.web,'dispose'),playerDispose=sub.mock.method(player,'dispose'),videoPause=sub.mock.method(f.video,'pause');
    const localPause=local?sub.mock.method(local,'pause'):undefined;
    const original={runtime:f.runtime,source:f.source,authority:f.runtime.authority,service:stack.service,queue:stack.queue,web:stack.web,slot:stack.web.slot,player,video:f.video,dock:f.runtime.dock,session:f.runtime.getSnapshot().session,queueView:f.runtime.getSnapshot().queueView,loads:f.video.loads,plays:f.video.plays,position:f.video.currentTime,listeners:f.video.listenerCount(),fence:f.runtime.getSnapshot().fence,presentation:f.runtime.getSnapshot().presentation,dockState:f.runtime.dock.getSnapshot(),remote:remote?.controller.getSnapshot().remote,remoteCommands:remote?.sender.commands.length,local:local?.observe(),localLoads:local?.loads.length};
    const time=manualClock(),hub=new FreshnessHub({...time,random:()=>0});
    let retired=0,homeFence=homeViewerFence,revision=1;
    const session={sessionFamilyId:'family',viewer:{...viewer,role:'owner'}} as import('@core/index.ts').LocalSession;
    const system={id:'server',name:'Study',version:'1',signIn:'local' as const,move:null,setupRequired:false,hostedConfigured:false,hostedAttached:false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
    const direct=()=>({authority:'local',serverId:'server',canManage:true,capabilities:{manageAccount:true,managePeople:true,manageServer:true,manageSignIn:true},account:{id:'account',username:'viewer '+revision,primaryProfileId:'profile',role:'owner',revision,disabled:false,allowedLibraries:[]},profiles:[{id:'profile',name:'Evening '+revision,primary:true,position:0,art:'mint',revision,pinRevision:1,pinRequired:false}]});
    const witnesses={system,direct:parseDirectSnapshot(direct())};
    const preferences={effective:{},deviceClass:'web'} as import('../src/shell/bootstrap.ts').ShellFacts['preferences'];
    const initial={serverName:'Study',preferences,identity:{viewer:session.viewer,serverName:'Study',serverVersion:'1',libraries:[{id:'library',name:'Films',kind:'movies'}],...shellAccount(witnesses.direct,'profile'),liveAvailable:false,unread:0,showNotificationDot:false}} as import('../src/shell/bootstrap.ts').ShellFacts;
    const retire=()=>{if(retired)return;retired++;f.invalidate();f.runtime.dispose();};
    const home=new HomeService(f.api,{observation:{serverId:'server',isCurrent:f.source.isCurrent,observe:observation=>observation.viewerFence===homeViewerFence?'accept':'retire',retire,mismatch:retire}});
    const reads:string[]=[];
    f.intercept(async(request,response)=>{
      const ok=(body:unknown)=>({status:200,headers:{},body});
      if(request.path==='/v1/libraries'){reads.push('directory');return ok({items:[{id:'library',name:'Films '+revision,kind:'movies'}]});}
      if(request.path==='/v1/system'){reads.push('server');return ok({...system,name:'Study '+revision,version:String(revision)});}
      if(request.path==='/v1/direct'){reads.push('members');return ok(direct());}
      if(request.path.startsWith('/v1/guide/sources?')){reads.push('guide');return ok({protocolVersion:GUIDE_PROTOCOL,serverId:'server',viewerFence:'guide',revision:String(revision),sources:[]});}
      if(request.path==='/v1/home'){reads.push('home');return ok({serverId:'server',viewerFence:homeFence,rows:[],spotlight:[],layout:{revision:1,rowOrder:[],hiddenRowIds:[]},revision:{catalog:revision,viewer:1},generatedAt:'2026-10-28T12:00:00Z'});}
      return response;
    });
    await home.load();
    const resources=new HomeDocumentResources(home,{isCurrent:f.source.isCurrent,create:()=>({value:f.runtime,dispose:()=>f.runtime.dispose()}),changed(){},failed:assert.fail});
    const owner=createShellFactOwner({api:f.api,session:()=>session,hub,home,initial,witnesses,isCurrent:f.source.isCurrent,retire,
      auth:{reconcilePermissions:async()=>({kind:'confirmed-unchanged'})} as never,
      preferences:{observePreferences:async()=>({kind:'ok',value:preferences})} as never});
    owner.setRetained(f.runtime.retainsMedia());const off=f.runtime.onRetention(()=>owner.setRetained(f.runtime.retainsMedia()));
    sub.after(()=>{off();owner.dispose();resources.dispose();home.dispose();hub.dispose();});
    let factsChanged=0;owner.source.subscribe(()=>factsChanged++);
    const hints=[{type:'libraries.changed',resource:{kind:'libraries',id:'all'},read:'directory'},
      {type:'settings.changed',resource:{kind:'settings',id:'server'},read:'server'},
      {type:'list.changed',resource:{kind:'list',id:'members'},read:'members'},
      {type:'list.changed',resource:{kind:'list',id:'liveSources'},read:'guide'},
      {type:'list.changed',resource:{kind:'list',id:'libraryChannels'},read:'guide'}];
    const advance=async()=>{for(let i=0;i<4;i++){time.advance(500);await settle();}};
    for(const hint of hints){
      revision++;const before=reads.length;hub.accept({id:'hint-'+revision,type:hint.type,resource:hint.resource,revision:String(revision)});await advance();
      assert.ok(reads.slice(before).includes(hint.read),hint.type+'/'+hint.resource.id+' must actually refresh its fact group');
      const current=f.runtime.getSnapshot(),now=(f.runtime as unknown as {stack:typeof stack}).stack;
      assert.equal(resources.get(),original.runtime);assert.equal(f.source,original.source);assert.equal(f.runtime.authority,original.authority);
      assert.equal(now.service,original.service);assert.equal(now.queue,original.queue);assert.equal(now.web,original.web);assert.equal(now.web.slot,original.slot);assert.equal(player,original.player);
      assert.equal(f.video,original.video);assert.equal(f.runtime.dock,original.dock);assert.equal(current.session,original.session);assert.equal(current.queueView,original.queueView);
      assert.deepEqual(current.fence,original.fence);assert.equal(current.presentation,original.presentation);assert.equal(f.runtime.dock.getSnapshot(),original.dockState);
      assert.equal(f.video.currentTime,original.position);assert.equal(f.video.loads,original.loads);assert.equal(f.video.plays,original.plays);assert.equal(f.video.listenerCount(),original.listeners);
      if(engine)assert.equal(engine.destroyed,false);assert.equal(f.hls.instances.filter(instance=>!instance.destroyed).length,engine?1:0);
      assert.equal(remote?.controller.getSnapshot().remote,original.remote);assert.equal(remote?.sender.commands.length,original.remoteCommands);assert.equal(local?.observe(),original.local);assert.equal(local?.loads.length,original.localLoads);assert.equal(f.runtime.retainsMedia(),true);assert.equal(retired,0);
      for(const spy of [pause,leave,dispose,playerDispose,videoPause,...(localPause?[localPause]:[])])assert.equal(spy.mock.calls.length,0,hint.type+'/'+hint.resource.id+' cannot pause/leave/dispose actual media');
      if(original.session)assert.equal(f.server.session(original.session.id)!.ended,false);
    }
    assert.ok(factsChanged>=3,'fresh names are published through the same fact source');
    assert.equal(owner.source.getSnapshot().identity.libraries[0].name,'Films 2');assert.equal(owner.source.getSnapshot().serverName,'Study 3');assert.equal(owner.source.getSnapshot().identity.profileName,'Evening 4');
    // A genuine authority manifest with a different visible-library/restriction
    // fence uses the real HomeService observation/retirement boundary immediately.
    homeFence='changed_visible_library_fence';revision++;
    hub.accept({id:'real-fence',type:'libraries.changed',resource:{kind:'libraries',id:'all'},revision:String(revision)});await advance();
    assert.equal(retired,1);assert.equal(f.runtime.getSnapshot().disposed,true);assert.equal(f.runtime.retainsMedia(),false);assert.equal(resources.get(),undefined);
    if(localPause)assert.ok(localPause.mock.calls.length>0);else assert.ok(pause.mock.calls.length>0);assert.ok(leave.mock.calls.length>0);assert.equal(dispose.mock.calls.length,1);assert.equal(playerDispose.mock.calls.length,1);
    if(engine)assert.equal(engine.destroyed,true);assert.equal(f.video.listenerCount(),0);
    if(original.session)assert.equal(f.server.session(original.session.id)!.ended,true);
    if(remote)assert.equal(remote.sender.disposed,true);
    gate.resolve();await pending;
  });
});

test('WEB002 real remote owner retains while playing, paused, stopped or transfer-pending without a local session', async t => {
  const f=setup(t),{sender,controller}=castController(t,f),changes:boolean[]=[];
  f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));assert.equal(f.runtime.retainsMedia(),false);
  sender.publish({phase:'casting',itemId:'movie',title:'Actual title',receiver:{itemId:'movie',positionSeconds:57,durationSeconds:3186,paused:false}});
  assert.equal(controller.getSnapshot().remote!.confirmed,true);assert.equal(f.runtime.getSnapshot().session,undefined);assert.equal(f.runtime.getSnapshot().dock.remote,null);
  assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
  sender.publish({phase:'casting',paused:true,receiver:{itemId:'movie',positionSeconds:57,durationSeconds:3186,paused:true}});
  assert.equal(controller.getSnapshot().remote!.paused,true);assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
  sender.publish({phase:'off',remoteStopped:true});assert.equal(controller.getSnapshot().remote!.phase,'stopped');assert.equal(f.runtime.retainsMedia(),true);
  controller.dismiss(controller.getSnapshot().remote!);assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
  sender.publish({phase:'off',transferPending:true,remoteStopped:false});assert.equal(f.runtime.retainsMedia(),true);
  controller.dispose();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false,true,false]);
});

test('WEB002 actual group item preflight and capability admission retain before any media resource exists', async t => {
  const {webDeclaredProfile}=await import('@core/client-profile.ts');
  for(const mode of ['group-item','capability'] as const)await t.test(mode,async sub=>{
    const gate=deferred(),caller=new AbortController();
    const f=setup(sub,{},mode==='capability'?{capabilities:async()=>{await gate.hold();return webDeclaredProfile({app:'WEB002'});}}:{}),changes:boolean[]=[];
    f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
    if(mode==='group-item')f.intercept(async(request,response)=>{if(request.path==='/v1/items/movie')await gate.hold();return response;});
    const pending=mode==='group-item'
      ?(f.runtime as unknown as {playGroupItem(id:string,seconds:number,playing:boolean,context:{signal:AbortSignal;isCurrent():boolean}):Promise<void>}).playGroupItem('movie',0,false,{signal:caller.signal,isCurrent:()=>true})
      :f.runtime.play('movie');
    const settled=pending.catch(()=>{});assert.equal(f.runtime.retainsMedia(),true);await gate.started;
    assert.equal(f.runtime.getSnapshot().session,undefined);assert.deepEqual(changes,[true]);
    f.runtime.dispose();assert.equal(f.runtime.retainsMedia(),false);caller.abort();gate.resolve();await settled;assert.deepEqual(changes,[true,false]);
  });
});

test('WEB002 actual bookmark restoration holds continuous retention and preserves a resumable video after local drain', async t => {
  const f=setup(t),changes:boolean[]=[];f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));
  await f.runtime.play('song_one',12);await f.arrive();const audio=f.runtime.getSnapshot().session!;
  await f.runtime.play('movie',30);await f.arrive();const gate=deferred();let held=false;
  f.intercept(async(request,response)=>{if(!held&&request.method==='POST'&&request.path==='/v1/playback/sessions'){held=true;await gate.hold();}return response;});
  const stopping=f.runtime.stop(f.runtime.getSnapshot().fence!);assert.equal(f.runtime.retainsMedia(),true);await gate.started;assert.deepEqual(changes,[true]);
  gate.resolve();await stopping;await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.queue!.queueId,audio.queue!.queueId);assert.equal(f.runtime.getSnapshot().presentation!.playing,false);assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
  // A real resumeVideo bookmark alone must keep the feed after its current
  // music session closes, without any invented server session or player count.
  await f.runtime.play('video_again',22);await f.arrive();const video=f.runtime.getSnapshot().fence!;
  await f.runtime.play('song_two',3);await f.arrive();await f.runtime.stop(f.runtime.getSnapshot().fence!);
  assert.equal(f.runtime.getSnapshot().session,undefined);assert.equal(f.runtime.getSnapshot().dock.resumeVideo!.fence.sessionId,video.sessionId);
  assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
  f.runtime.dispose();assert.deepEqual(changes,[true,false]);
});

test('WEB002 actual SDK adoption retains validation and metadata preflight while off; disposal cannot leak it', async t => {
  const {castProofKey}=await import('@core/cast/controller-proof.ts');
  for(const ending of ['adopt','dispose-validation','dispose-metadata'] as const)await t.test(ending,async sub=>{
    const f=setup(sub),validation=deferred(),metadata=deferred(),changes:boolean[]=[];
    const scope={...viewer,senderDeviceId:'sender-device',senderFamilyId:'family'},contextProof={...scope,origin:'https://portico.test',publicKey:'a'.repeat(43)};
    const binding={id:'binding',bootstrapId:'bootstrap',receiverId:'tv',targetId:'target',targetFamilyId:'target-family',targetGeneration:'1',scope};
    const saved=new Map([[castProofKey(contextProof,'tv'),JSON.stringify({version:2,context:contextProof,binding,controlToken:'a'.repeat(48)})]]);
    const previous=Object.getOwnPropertyDescriptor(globalThis,'localStorage');
    Object.defineProperty(globalThis,'localStorage',{configurable:true,value:{getItem:(key:string)=>saved.get(key)??null,setItem:(key:string,value:string)=>saved.set(key,value),length:0,key:()=>null,removeItem:(key:string)=>saved.delete(key)}});
    sub.after(()=>{if(previous)Object.defineProperty(globalThis,'localStorage',previous);else delete(globalThis as Record<string,unknown>).localStorage;validation.resolve();metadata.resolve();});
    let first=true;
    f.intercept(async(request,response)=>{
      if(request.path!=='/v1/cast/controllers/validate')return response;
      assert.equal(f.runtime.retainsMedia(),true,'real validation is retained before its first await');
      if(first){first=false;await validation.hold();}
      return {status:200,headers:{},body:{binding,attemptId:(request.body as {attemptId:string}).attemptId,ticket:'b'.repeat(43)}};
    });
    const messages:Record<string,unknown>[]=[],listeners=new Set<(namespace:string,body:string)=>void>();
    const session:CastSession={getCastDevice:()=>({friendlyName:'TV',label:'tv'}),sendMessage:async(_namespace,message)=>{messages.push(message as Record<string,unknown>);},addMessageListener:(_namespace,listener)=>{listeners.add(listener);},removeMessageListener:(_namespace,listener)=>{listeners.delete(listener);},endSession(){}};
    const context={setOptions(){},requestSession:async()=>{},getCurrentSession:()=>session,addEventListener(){},removeEventListener(){}};
    const browser:CastBrowser={document:new Proxy({} as Document,{get(){throw Error('unexpected_sdk_document_read');}}),navigator:{userAgent:'Chrome/156.0 Linux'},chrome:{},cast:{framework:{CastContext:{getInstance:()=>context},CastContextEventType:{SESSION_STATE_CHANGED:'session'},SessionState:{SESSION_RESUMED:'resumed',SESSION_ENDED:'ended'}}},setTimeout:f.clock.setTimer,clearTimeout:f.clock.clearTimer};
    const sender=new WebCastSender({api:f.api,serverOrigin:contextProof.origin,controllerContext:contextProof,isCurrent:f.source.isCurrent,castApplicationId:'APP123',browser,describe:async()=>{await metadata.hold();return 'Actual title';}});
    const controller=new PlayOnController({sender,runtime:f.runtime,isCurrent:f.source.isCurrent,reportError:assert.fail});sub.after(()=>controller.dispose());
    f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));const sequence=f.runtime.getSnapshot().sequence;
    const ready=sender.prepareForPlayer();await validation.started;assert.equal(await ready,true);
    assert.equal(sender.getSnapshot().phase,'off');assert.equal(controller.getSnapshot().remote,undefined);assert.equal(f.runtime.getSnapshot().session,undefined);assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
    assert.equal(f.runtime.getSnapshot().sequence,sequence,'retention observation adds no runtime snapshot cadence');
    if(ending==='dispose-validation'){controller.dispose();assert.equal(f.runtime.retainsMedia(),false);validation.resolve();await settle();assert.deepEqual(changes,[true,false]);assert.equal(messages.length,0);return;}
    validation.resolve();await settle();const adopt=messages.find(message=>message.type==='adopt')!;assert.ok(adopt);
    for(const listener of listeners)listener('urn:x-cast:tv.getportico.portico',JSON.stringify({type:'status',attemptId:adopt.attemptId,controller:{bindingId:'binding',controllerGeneration:1,connectionGeneration:1},occurrence:{sessionId:'receiver-session',generation:1,itemId:'movie'},itemId:'movie',positionSeconds:43,durationSeconds:3186,paused:true,audioTracks:[],textTracks:[]}));
    await metadata.started;assert.equal(sender.getSnapshot().phase,'off');assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
    if(ending==='dispose-metadata'){controller.dispose();assert.equal(f.runtime.retainsMedia(),false);metadata.resolve();await settle();assert.deepEqual(changes,[true,false]);return;}
    metadata.resolve();await settle();assert.equal(sender.getSnapshot().phase,'casting');assert.equal(controller.getSnapshot().remote!.paused,true);assert.equal(f.runtime.retainsMedia(),true);assert.deepEqual(changes,[true]);
    controller.dispose();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[true,false]);
  });
});

test('WEB002 cancelled actual SDK picker cannot retain a drained runtime while the SDK ignores cancellation', async t => {
  const f=setup(t);await f.runtime.play('movie',25);await f.arrive();const fence=f.runtime.getSnapshot().fence!,gate=deferred(),changes:boolean[]=[];
  const session:CastSession={getCastDevice:()=>({friendlyName:'TV'}),sendMessage:async()=>{},addMessageListener(){},removeMessageListener(){},endSession(){}};
  const context={setOptions(){},requestSession:()=>gate.hold(),getCurrentSession:()=>session,addEventListener(){},removeEventListener(){}};
  const browser:CastBrowser={document:new Proxy({} as Document,{get(){throw Error('unexpected_sdk_document_read');}}),navigator:{userAgent:'Chrome/156.0 Linux'},chrome:{},cast:{framework:{CastContext:{getInstance:()=>context},CastContextEventType:{SESSION_STATE_CHANGED:'session'},SessionState:{SESSION_RESUMED:'resumed',SESSION_ENDED:'ended'}}},setTimeout:f.clock.setTimer,clearTimeout:f.clock.clearTimer};
  // No controller proof or catalogue reader means this idle SDK session cannot
  // silently adopt a receiver before the explicit user picker admission.
  const sender=new WebCastSender({api:f.api,serverOrigin:'https://portico.test',castApplicationId:'APP123',browser});await sender.prepareForPlayer();
  const controller=new PlayOnController({sender,runtime:f.runtime,isCurrent:f.source.isCurrent,reportError:assert.fail});t.after(()=>{gate.resolve();controller.dispose();});
  f.runtime.onRetention(()=>changes.push(f.runtime.retainsMedia()));const pending=controller.startCast();await gate.started;
  assert.equal(sender.retainsMedia(),true);assert.equal(f.runtime.retainsMedia(),true);
  await f.runtime.stop(fence);await settle();assert.equal(await pending,false);
  assert.equal(sender.retainsMedia(),false);assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[false]);
  gate.resolve();await settle();assert.equal(f.runtime.retainsMedia(),false);assert.deepEqual(changes,[false]);
});

function airPlayCaptionFixture(f: ReturnType<typeof setup>, initial: 'off' | 'external_text' | 'client') {
 let renderer: 'off'|'external_text'|'client'|'burn_in' = initial, refuse = false, stale = false, selectedId = 's3', hold = false;
 const preparations: {body:{generation:number;choiceRevision:number;positionMs:number}; release():void}[] = [];
 const plan = (sessionId: string, generation: number) => {
  const resource = {id:selectedId,sourceId:'source_movie',revision:1,scope:'personal',language:'en',title:'Selected',format:renderer==='client'||renderer==='burn_in'?'ass':'vtt',origin:'upload',rights:'Own transcript',offsetUs:'-2000',canManage:true,enabled:true,pinned:true,renderer:renderer==='off'?'external_text':renderer,default:false,forced:false,
   deliveries:renderer==='client'?[{format:'ass',url:`/v1/media/subtitles/grant/${selectedId}/1/original.ass`}]:[{format:'text'}]};
  return {version:1,sessionId,generation:stale?generation+100:generation,sourceId:'source_movie',revision:1,catalogRevision:1,renderer:renderer==='off'?'external_text':renderer,mode:renderer==='off'?'off':'track',offAvailable:true,offsetUs:renderer==='off'?'0':'-2000',selected:renderer==='off'?null:resource,resources:[resource,{...resource,id:selectedId==='s3'?'s4':'s3'}],discovered:[],...(renderer==='external_text'?{documentUrl:`/v1/media/subtitles/grant/${selectedId}/1`}:{})};
 };
 Object.assign(f.video,{webkitShowPlaybackTargetPicker:()=>{},nativeHls:'probably'});
 f.intercept(async(request,response)=>{
  if(request.path.endsWith('/receivers/airplay')) {
   if(refuse)return {status:403,headers:{},body:{error:{code:'transcoding_disabled',retry:'never'}}};
   const input=request.body as {generation:number;choiceRevision:number;positionMs:number},sessionId=request.path.split('/')[4]!;
   const result = {status:200,headers:{},body:{sessionId,generation:stale?input.generation+100:input.generation,choiceRevision:input.choiceRevision,presentation:{generation:input.generation,choiceRevision:input.choiceRevision,mode:'stream',engine:'native',audioTrackId:'a1',url:'/v1/media/receiver_'+input.generation+'/master.m3u8',startPositionMs:input.positionMs,subtitles:[],decision:{},...(renderer==='off'?{}:{subtitle:{trackId:selectedId,kind:renderer==='external_text'?'text':'ass',delivery:renderer==='external_text'?'sidecar':'burn'}})}}};
   if(hold)await new Promise<void>(resolve=>{preparations.push({body:input,release:resolve});});
   return result;
  }
  if(request.method==='PATCH'&&(request.body as {subtitles?:{delivery?:string}})?.subtitles?.delivery==='burn') {
   if(refuse)return {status:403,headers:{},body:{error:{code:'transcoding_disabled',retry:'never'}}};
   renderer='burn_in';
  }
  const installChoice = (value: unknown): unknown => {
   if(Array.isArray(value))return value.map(installChoice);
   if(!value||typeof value!=='object')return value;
   const object=value as Record<string,unknown>;
   const updated=Object.fromEntries(Object.entries(object).map(([key,child])=>[key,installChoice(child)]));
   if(typeof object.id==='string'&&object.presentation&&typeof object.presentation==='object') {
    updated.presentation={...(object.presentation as object),...(renderer==='off'?{subtitle:undefined}:{subtitle:{trackId:selectedId,kind:renderer==='client'||renderer==='burn_in'?'ass':'text',delivery:renderer==='burn_in'?'burn':renderer==='client'?'device':'sidecar'}})};
   }
   return updated;
  };
  response={...response,body:installChoice(response.body)};
  if(request.path.includes('/playback-offers?')) {
   const body=response.body as {current:{sessionId:string;generation:number};controls:{id:string}[]};
   return {...response,body:{...body,current:{...body.current,subtitleSelection:renderer==='off'?'off':'track'},controls:body.controls.map(control=>control.id==='subtitles'?{...control,enabled:true,reason:''}:control),subtitlePlan:plan(body.current.sessionId,body.current.generation)}};
  }
  const match=/\/playback\/([^/]+)\/subtitles/.exec(request.path);
  if(match)return {status:200,headers:{},body:plan(match[1]!,f.server.session(match[1]!)!.generation)};
  if(request.path==='/v1/items/movie/subtitles')return {status:200,headers:{},body:{version:1,itemId:'movie',revision:1,canShare:false,sources:[],resources:[],discovered:[],provider:{id:'opensubtitles',enabled:false}}};
  return response;
 });
 return {hold:()=>{hold=true;},preparations,set: (next: typeof initial,id='s3') => {renderer=next;selectedId=id;},refuse:()=>{refuse=true;},stale:()=>{stale=true;},refresh:async()=>{
  const stack=(f.runtime as unknown as {stack:{subtitles?:{refresh():Promise<void>}}}).stack;await stack.subtitles?.refresh();await settle();
 }};
}

for(const captions of ['off','external_text'] as const)test(`AirPlay ${captions} never requests a burn, including system activation and route return`,async t=>{
 const f=setup(t),caption=airPlayCaptionFixture(f,captions),video=f.video as FakeVideo&{webkitCurrentPlaybackTargetIsWireless:boolean};
 await f.runtime.play('movie');await f.arrive();await caption.refresh();video.webkitCurrentPlaybackTargetIsWireless=true;
 assert.equal(f.runtime.getSnapshot().offers?.subtitlePlan?.mode,captions==='off'?'off':'track');
 video.fire('webkitcurrentplaybacktargetiswirelesschanged');video.webkitCurrentPlaybackTargetIsWireless=false;video.fire('webkitcurrentplaybacktargetiswirelesschanged');await settle();
 assert.equal(f.log.some(request=>request.method==='PATCH'),false);
});

test('AirPlay off to drawn while wireless burns the exact track and offset once, and retains delivery after route return only in this run',async t=>{
 const f=setup(t),caption=airPlayCaptionFixture(f,'off'),video=f.video as FakeVideo&{webkitCurrentPlaybackTargetIsWireless:boolean};
 await f.runtime.play('movie');await f.arrive();
 video.webkitCurrentPlaybackTargetIsWireless=true;video.fire('webkitcurrentplaybacktargetiswirelesschanged');
 caption.set('client');await caption.refresh();await f.runtime.selectSubtitle(f.runtime.getSnapshot().fence!,'s3');await settle();
 assert.equal(f.log.some(request=>request.method==='PATCH'&&(request.body as {subtitles?:{delivery?:string}}).subtitles?.delivery==='burn'),false,'prepared captioned receiver gets its first frame before a local burn mutation');
 assert.match(f.video.src,/receiver_/);f.video.ready(0);await settle();
 const writes=f.log.filter(request=>request.method==='PATCH'&&(request.body as {subtitles?:{delivery?:string}}).subtitles?.delivery==='burn');assert.equal(writes.length,1,JSON.stringify({problem:f.runtime.getSnapshot().problem,session:f.runtime.getSnapshot().session,preparations:f.log.filter(r=>r.path.endsWith('/receivers/airplay')).map(r=>r.body)}));
 assert.deepEqual((writes[0]!.body as {subtitles:unknown}).subtitles,{trackId:'s3',delivery:'burn'});assert.equal((writes[0]!.body as {subtitleOffsetMs:number}).subtitleOffsetMs,-2);
 video.webkitCurrentPlaybackTargetIsWireless=false;video.fire('webkitcurrentplaybacktargetiswirelesschanged');await f.arrive();
 const stack=(f.runtime as unknown as {stack:{service:{playbackChoices():{subtitleDelivery?:string}};undrawable?:{run:number;ids:Set<string>}}}).stack;
 assert.equal(stack.service.playbackChoices().subtitleDelivery,'burn');assert.equal(stack.undrawable?.ids.has('s3'),true);
 await f.runtime.play('other');await f.arrive();
 const next=(f.runtime as unknown as {stack:typeof stack}).stack;
 assert.equal(next.undrawable,undefined);assert.equal(next.service.playbackChoices().subtitleDelivery,undefined);
});

test('AirPlay drawn burn refusal remains explicit, does not retry on route changes, and stale subtitle generations cannot request a burn',async t=>{
 for(const stale of [false,true]){
  const f=setup(t),caption=airPlayCaptionFixture(f,'off'),video=f.video as FakeVideo&{webkitCurrentPlaybackTargetIsWireless:boolean};
  await f.runtime.play('movie');await f.arrive();caption.refuse();if(stale)caption.stale();
  video.webkitCurrentPlaybackTargetIsWireless=true;caption.set('client');await caption.refresh();
  if(!stale)await f.runtime.selectSubtitle(f.runtime.getSnapshot().fence!,'s3');await settle();
  const writes=()=>f.log.filter(request=>request.method==='PATCH'&&(request.body as {subtitles?:{delivery?:string}}).subtitles?.delivery==='burn');
  assert.equal(writes().length,0);
  if(!stale)assert.equal(f.runtime.getSnapshot().problem,'playback_subtitles_failed');
  video.webkitCurrentPlaybackTargetIsWireless=false;video.fire('webkitcurrentplaybacktargetiswirelesschanged');video.webkitCurrentPlaybackTargetIsWireless=true;video.fire('webkitcurrentplaybacktargetiswirelesschanged');await settle();
  assert.equal(writes().length,0);
 }
});


test('AirPlay rapid text to Off to different text to drawn ignores out-of-order prepared receivers',async t=>{
 const f=setup(t),caption=airPlayCaptionFixture(f,'external_text'),video=f.video as FakeVideo&{webkitCurrentPlaybackTargetIsWireless:boolean};
 caption.hold();await f.runtime.play('movie');await f.arrive();
 video.webkitCurrentPlaybackTargetIsWireless=true;video.fire('webkitcurrentplaybacktargetiswirelesschanged');
 for(const [renderer,id] of [['off','s3'],['external_text','s4'],['client','s3']] as const){
  caption.set(renderer,id);await caption.refresh();await f.runtime.selectSubtitle(f.runtime.getSnapshot().fence!,renderer==='off'?null:id);await settle();
 }
 assert.equal(caption.preparations.length,4);
 const latest=caption.preparations[3]!;latest.release();await settle();
 const expected=f.video.src;assert.match(expected,new RegExp('receiver_'+latest.body.generation));
 for(const index of [0,2,1]){caption.preparations[index]!.release();await settle();assert.equal(f.video.src,expected,'a retired choice cannot replace the newest receiver');}
 assert.equal(f.log.some(request=>request.method==='PATCH'&&(request.body as {subtitles?:{delivery?:string}}).subtitles?.delivery==='burn'),false,'drawn native first frame is still pending');
 f.runtime.dispose();for(const preparation of caption.preparations)preparation.release();await settle();
});

test('AirPlay drawn to Off to text to the same drawn track keeps burn only for that track in the same session',async t=>{
 const f=setup(t),caption=airPlayCaptionFixture(f,'off'),video=f.video as FakeVideo&{webkitCurrentPlaybackTargetIsWireless:boolean};
 await f.runtime.play('movie');await f.arrive();const session=f.runtime.getSnapshot().session!.id;
 video.webkitCurrentPlaybackTargetIsWireless=true;video.fire('webkitcurrentplaybacktargetiswirelesschanged');
 const choose=async(renderer:'off'|'external_text'|'client',id='s3')=>{
  caption.set(renderer,id);await caption.refresh();await f.runtime.selectSubtitle(f.runtime.getSnapshot().fence!,renderer==='off'?null:id);await settle();video.ready(0);await settle();
 };
 await choose('client');video.ready(0);await settle();
 const patches=()=>f.log.filter(request=>request.method==='PATCH').map(request=>request.body as {subtitles?:{trackId:string|null;delivery?:string}});
 assert.equal(patches().filter(body=>body.subtitles?.delivery==='burn').length,1);
 await choose('off');assert.deepEqual(patches().at(-1)!.subtitles,{trackId:null});
 assert.equal(f.runtime.getSnapshot().session!.presentation.subtitle,undefined,'Off removes the burned track');
 await choose('external_text','s4');assert.deepEqual(patches().at(-1)!.subtitles,{trackId:'s4',delivery:'sidecar'});
 assert.equal(f.runtime.getSnapshot().session!.presentation.subtitle?.trackId,'s4');
 await choose('client','s3');assert.deepEqual(patches().at(-1)!.subtitles,{trackId:'s3',delivery:'burn'});
 assert.equal(f.runtime.getSnapshot().session!.id,session,'all choices belong to the same playback session');
});

test('P1: the player is the session\'s, not Home\'s: with no Home document it plays an item, an album and a queue', async t => {
  // A cold deep link, or Home answering 503: the shell builds the player from the session alone.
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  assert.equal(f.source.authority.viewerFence, undefined);
  await f.runtime.playFromScreen({libraryId: 'library', itemId: 'movie', startSeconds: 0}, {signal: life.signal, isCurrent: () => true}); await f.arrive();
  assert.equal(f.runtime.getSnapshot().session!.itemId, 'movie', 'Play on an item page starts playback');
  await f.runtime.playFromQueue({container: {kind: 'album', id: 'ordered_release'}}, {signal: life.signal, isCurrent: () => true}); await f.arrive();
  const created = f.log.findLast(value => value.method === 'POST' && value.path === '/v1/queues')!;
  assert.deepEqual((created.body as {segments: unknown[]}).segments, [{source: {container: {kind: 'album', id: 'ordered_release'}}, order: {mode: 'source'}}]);
  assert.equal(f.runtime.getSnapshot().session!.itemId, 'ordered_release-0', 'Play on an album page starts the album');
});

test('P2-74 E-web-14: music whose item facts never answer still mounts its dock, named from the queue entry', async t => {
  const {dockView} = await import('../src/kit/dock/model.ts');
  const {playbackPresentation} = await import('../src/shell/playback-model.ts');
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  let itemReads = 0;
  f.intercept(async (request, response) => /^\/v1\/items\/[^/?]+$/.test(request.path) && request.method === 'GET'
    ? (itemReads++, {status: 503, headers: {}, body: {error: {code: 'server_unavailable', retryable: true}}}) : response);
  await f.runtime.playFromQueue({container: {kind: 'album', id: 'song_album'}}, {signal: life.signal, isCurrent: () => true});
  for (let step = 0; step < 6; step++) { await f.clock.advance?.(3000); await f.arrive(); }
  const s = f.runtime.getSnapshot();
  assert.equal(itemReads, 3, 'a passing failure is read again twice, then the start stops waiting for it');
  assert.equal(s.presentation?.kind, 'music');
  assert.equal(s.presentation?.subject.kind === 'music' ? s.presentation.subject.item.title : undefined, 'Title song_album-0', 'named from the queue\'s own entry');
  assert.equal(s.dock.local?.expanded, false, 'music sits in the dock');
  const environment = {t: (key: string) => key, locale: 'en-US'} as never, artwork = {foreground: () => undefined, ambient: () => undefined} as never;
  const presentation = playbackPresentation(s, environment, artwork, {back: 10, forward: 30});
  assert.ok(presentation && dockView(s.dock, [presentation]), 'the dock mounts: DockHost has a view to draw, with Pause there');
});

test('P2-74 E-web-14: one 503 on the item facts is read again and the real facts draw the dock', async t => {
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  let itemReads = 0;
  f.intercept(async (request, response) => /^\/v1\/items\/[^/?]+$/.test(request.path) && request.method === 'GET' && itemReads++ === 0
    ? {status: 503, headers: {}, body: {error: {code: 'server_unavailable', retryable: true}}} : response);
  await f.runtime.playFromQueue({container: {kind: 'album', id: 'song_album'}}, {signal: life.signal, isCurrent: () => true});
  for (let step = 0; step < 3; step++) { await f.clock.advance?.(1500); await f.arrive(); }
  const s = f.runtime.getSnapshot();
  assert.equal(itemReads, 2);
  assert.equal(s.presentation?.kind, 'music');
  assert.equal(s.dock.local?.expanded, false);
  assert.ok(!s.problem, 'recovered, nothing to report');
});

test('P2-74 E-web-17: a playback start the server never answers is reported after the deadline, never a silent stall', async t => {
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  f.intercept(async (request, response) => request.method === 'POST' && request.path.startsWith('/v1/playback/sessions')
    ? new Promise<never>((_, reject) => {const stop = () => reject(Object.assign(new Error('aborted'), {name: 'AbortError'})); if (request.signal?.aborted) stop(); else request.signal?.addEventListener('abort', stop, {once: true});})
    : response);
  const start = f.runtime.playFromScreen({libraryId: 'library', itemId: 'movie', startSeconds: 0}, {signal: life.signal, isCurrent: () => true});
  const outcome = start.then(() => 'started', (error: Error) => error.message);
  // The deadline arms once the start reaches its session request; let that happen, then pass 20 s.
  for (let step = 0; step < 10; step++) { for (let spin = 0; spin < 20; spin++) await new Promise(setImmediate); f.clock.advance?.(2500); }
  assert.equal(await outcome, 'playback_start_timeout');
  assert.equal(f.runtime.getSnapshot().session, undefined, 'the stalled start is retired, nothing half-started is left');
});

test('P2-74 E-web-14: artist and album Play mount the music dock AND start the media element, with real-shaped queue entries', async t => {
  const {dockView} = await import('../src/kit/dock/model.ts');
  const {playbackPresentation} = await import('../src/shell/playback-model.ts');
  for (const kind of ['artist', 'album'] as const) {
    const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
    // The real server names a song in a queue a "track" (fake windows say "song").
    f.intercept(async (request, response) => /\/v1\/queues\/[^/]+\/window/.test(request.path) && response.status === 200
      ? {...response, body: {...(response.body as {items: {kind: string}[]}), items: (response.body as {items: {kind: string}[]}).items.map(item => ({...item, kind: item.kind === 'song' ? 'track' : item.kind}))}}
      : response);
    await f.runtime.playFromQueue({container: {kind, id: `song_${kind}`}}, {signal: life.signal, isCurrent: () => true});
    for (let step = 0; step < 3; step++) await f.arrive();
    const s = f.runtime.getSnapshot();
    const environment = {t: (key: string) => key, locale: 'en-US'} as never, artwork = {foreground: () => undefined, ambient: () => undefined} as never;
    const presentation = playbackPresentation(s, environment, artwork, {back: 10, forward: 30});
    assert.equal(s.presentation?.kind, 'music', kind);
    assert.ok(presentation && dockView(s.dock, [presentation]), `${kind}: the dock mounts`);
    assert.equal(s.dock.local?.expanded, false, `${kind}: music sits in the dock`);
    assert.ok(f.video.plays > 0, `${kind}: the media element was told to play`);
  }
});

// O-159 watchdog: an accepted start is drawn at once, and is never left silent.
const startingDisplay = {subject: {kind: 'music' as const, item: {id: 'song_album', kind: 'song' as const, title: 'The pressed album'}}};
const hangOffers = (request: {path: string; signal?: AbortSignal}) => new Promise<never>((_, reject) => {
  const stop = () => reject(Object.assign(new Error('aborted'), {name: 'AbortError'}));
  if (request.signal?.aborted) stop(); else request.signal?.addEventListener('abort', stop, {once: true});
});
const spin = async () => {for (let i = 0; i < 20; i++) await new Promise(setImmediate);};

test('O-159: offers that never become current draw the starting dock at once, then stop with a diagnostic at the deadline', async t => {
  const {openAppLogContext, captureAppLog} = await import('../src/platform/app-log.ts');
  const closeLog = openAppLogContext(() => true); t.after(closeLog);
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  f.intercept(async (request, response) => /\/playback-offers/.test(request.path) ? hangOffers(request) : response);
  const start = f.runtime.playFromQueue({container: {kind: 'album', id: 'song_album'}}, {signal: life.signal, isCurrent: () => true}, {display: startingDisplay});
  // Drawn within the press itself, before any answer: the card's own title.
  const drawn = f.runtime.getSnapshot().starting;
  assert.equal(drawn?.display.subject.item.title, 'The pressed album');
  await start; await f.arrive(); await spin();
  let s = f.runtime.getSnapshot();
  assert.ok(s.starting, 'accepted but not drawable: still starting');
  assert.equal(s.presentation, undefined, 'never claims playing: no Pause, no progress');
  for (let step = 0; step < 10; step++) {f.clock.advance?.(2500); await spin();}
  s = f.runtime.getSnapshot();
  assert.equal(s.starting, undefined);
  assert.ok(s.startTimedOut, '"Couldn\'t start playback" with Try again');
  assert.equal(s.session, undefined, 'the accepted start was ended, nothing half-started is left');
  assert.ok(captureAppLog().entries.some(entry => entry.code === 'playback_not_drawn_offers'), 'the diagnostic names the read that never became current');
});

test('O-159: a slow but successful start goes from the starting dock to the real dock with no gap', async t => {
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  let release!: () => void;
  const gate = new Promise<void>(resolve => {release = resolve;});
  f.intercept(async (request, response) => {if (/\/playback-offers/.test(request.path)) await gate; return response;});
  const seen: {starting: boolean; presented: boolean}[] = [];
  const off = f.runtime.subscribe(() => {const s = f.runtime.getSnapshot(); seen.push({starting: !!s.starting, presented: !!s.presentation});});
  t.after(off);
  await f.runtime.playFromQueue({container: {kind: 'album', id: 'song_album'}}, {signal: life.signal, isCurrent: () => true}, {display: startingDisplay});
  await f.arrive(); f.clock.advance?.(3000); await spin();
  assert.ok(f.runtime.getSnapshot().starting, 'starting while the offers are slow');
  release(); for (let step = 0; step < 3; step++) await f.arrive();
  const s = f.runtime.getSnapshot();
  assert.equal(s.starting, undefined); assert.equal(s.presentation?.kind, 'music'); assert.equal(s.startTimedOut, undefined);
  const first = seen.findIndex(value => value.starting);
  assert.ok(first >= 0 && seen.slice(first).every(value => value.starting || value.presented), 'no publication between them draws nothing');
  f.clock.advance?.(25000); await spin();
  assert.equal(f.runtime.getSnapshot().startTimedOut, undefined, 'a drawn dock ends the watch; buffering never trips it');
});

test('O-159: Stop on the starting dock cancels the accepted start cleanly', async t => {
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  f.intercept(async (request, response) => /\/playback-offers/.test(request.path) ? hangOffers(request) : response);
  await f.runtime.playFromQueue({container: {kind: 'album', id: 'song_album'}}, {signal: life.signal, isCurrent: () => true}, {display: startingDisplay});
  await f.arrive(); await spin();
  f.runtime.getSnapshot().starting!.stop(); await spin();
  let s = f.runtime.getSnapshot();
  assert.equal(s.starting, undefined); assert.equal(s.session, undefined); assert.equal(s.dock.local, null);
  f.clock.advance?.(25000); await spin();
  s = f.runtime.getSnapshot();
  assert.equal(s.startTimedOut, undefined, 'a stopped start is never reported as a failure');
  // Before acceptance, Stop is the viewer's own cancel.
  const g = setup(t, {}, {}, undefined, undefined, {homeFence: false});
  g.intercept(async (request, response) => request.method === 'POST' && request.path === '/v1/queues' ? hangOffers(request) : response);
  const early = g.runtime.playFromQueue({container: {kind: 'album', id: 'song_album'}}, {signal: life.signal, isCurrent: () => true}, {display: startingDisplay});
  g.runtime.getSnapshot().starting!.stop();
  await assert.rejects(early, (error: Error) => error.name === 'AbortError');
  assert.equal(g.runtime.getSnapshot().starting, undefined);
});

// P2-61: every playback start is drawn at once and watched, not only the O-159 reference paths.
type StartFixture = ReturnType<typeof setup>;
const videoDisplay = {subject: {kind: 'video' as const, item: {id: 'movie', kind: 'movie' as const, title: 'The pressed movie'}}};
const startPaths: {name: string; title: string; post: string; live?: boolean; early?: boolean;
  prepare?: (f: StartFixture) => Promise<void>; run(f: StartFixture, life: AbortSignal): Promise<unknown>; drawn(f: StartFixture): Promise<unknown>}[] = [
  {name: 'start', title: 'movie', post: '/v1/queues', early: true,
    run: f => f.runtime.start({entry: selectionLeaf('movie').entry, target: {itemId: 'movie'}}), drawn: f => f.arrive()},
  {name: 'play (Cast continue here)', title: 'The pressed movie', post: '/v1/queues', early: true,
    run: f => f.runtime.play('movie', 0, undefined, videoDisplay), drawn: f => f.arrive()},
  {name: 'play a different version', title: 'movie', post: '/v1/queues',
    prepare: async f => {
      f.intercept(async (request, response) => {
        if (request.path === '/v1/items/movie/playback-options') {
          const body = response.body as {versions: {id: string}[]};
          return {...response, body: {...body, versions: [...body.versions, {...body.versions[0], id: 'v2'}]}};
        }
        return response;
      });
      await f.runtime.play('movie', 25); await f.arrive();
    },
    run: (f, signal) => f.runtime.playVersion({itemId: 'movie', versionId: 'v2', context: selectionContext, signal}, () => true), drawn: f => f.arrive()},
  {name: 'guide channel', title: 'channel', post: '/v1/playback/sessions', live: true, early: true,
    run: (f, signal) => f.runtime.playFromGuide(homeChannel.channel, {signal, isCurrent: () => true}), drawn: f => f.arriveLive()},
  {name: 'Home channel', title: 'channel', post: '/v1/playback/sessions', live: true, early: true,
    run: (f, signal) => f.runtime.playChannel(homeChannel, selectionContext, signal, () => true), drawn: f => f.arriveLive()},
];
const spinUntil = async (f: StartFixture, done: () => boolean) => {for (let i = 0; i < 40 && !done(); i++) await spin();};
const startTitle = (f: StartFixture) => {const subject = f.runtime.getSnapshot().starting?.display.subject; return subject?.kind === 'channel' ? subject.channelName : subject?.item.title;};

// A second edition for the version path; every path's fixture passes through this.
const withSecondVersion = (request: V1Request, response: V1Response): V1Response => {
  if (request.path !== '/v1/items/movie/playback-options') return response;
  const body = response.body as {versions: {id: string}[]};
  return {...response, body: {...body, versions: [...body.versions, {...body.versions[0], id: 'v2'}]}};
};
for (const path of startPaths) {
  /** After the server accepts, the start can't be drawn: the offers never become current, or the live join never answers. */
  const undrawable = (request: V1Request) => path.live ? request.method === 'PATCH' && request.path.startsWith('/v1/playback/sessions/live') : /\/playback-offers/.test(request.path);
  const hold = (f: StartFixture, gate?: Promise<void>) => f.intercept(async (request, response) => {
    if (undrawable(request)) {if (!gate) return hangOffers(request); await gate;}
    return withSecondVersion(request, response);
  });
  test(`P2-61 ${path.name}: the starting dock is drawn, then the real dock replaces it in place`, async t => {
    const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
    await path.prepare?.(f);
    let release!: () => void;
    hold(f, new Promise<void>(resolve => {release = resolve;}));
    const seen: {starting: boolean; presented: boolean}[] = [];
    const off = f.runtime.subscribe(() => {const s = f.runtime.getSnapshot(); seen.push({starting: !!s.starting, presented: !!s.presentation});});
    t.after(off);
    const start = path.run(f, life.signal);
    if (path.early) assert.equal(startTitle(f), path.title, 'drawn within the press itself, from the facts the start truly has');
    await start; await spinUntil(f, () => !!f.runtime.getSnapshot().starting);
    const waiting = f.runtime.getSnapshot();
    assert.equal(startTitle(f), path.title);
    assert.equal(waiting.presentation, undefined, 'never claims playing before the real dock exists');
    assert.equal(waiting.startTimedOut, undefined);
    release(); for (let step = 0; step < 3; step++) await path.drawn(f);
    const s = f.runtime.getSnapshot();
    assert.equal(s.starting, undefined); assert.ok(s.presentation, 'the real dock is drawn'); assert.equal(s.startTimedOut, undefined);
    const first = seen.findIndex(value => value.starting);
    assert.ok(first >= 0 && seen.slice(first).every(value => value.starting || value.presented), 'no publication between them draws nothing');
    f.clock.advance?.(25000); await spin();
    assert.equal(f.runtime.getSnapshot().startTimedOut, undefined, 'a drawn dock ends the watch');
  });

  test(`P2-61 ${path.name}: Stop on the starting dock cancels the accepted start`, async t => {
    const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
    await path.prepare?.(f);
    hold(f);
    const outcome = path.run(f, life.signal).then(() => 'started', (error: Error) => error.name);
    await spinUntil(f, () => !!f.runtime.getSnapshot().starting); await spin();
    let s = f.runtime.getSnapshot();
    assert.ok(s.starting, 'accepted and not drawable: still starting');
    s.starting!.stop(); await spin();
    s = f.runtime.getSnapshot();
    assert.equal(s.starting, undefined); assert.equal(s.session, undefined); assert.equal(s.fence, undefined);
    if (!path.prepare) assert.equal(s.dock.local, null, 'nothing half-started is left'); // a different version keeps the title it replaced as its paused bookmark
    f.clock.advance?.(25000); await spin();
    assert.equal(f.runtime.getSnapshot().startTimedOut, undefined, 'a stopped start is never reported as a failure');
    assert.ok(['started', 'AbortError'].includes(await outcome));
  });

  if (path.early) test(`P2-61 ${path.name}: Stop before the server answers is the viewer's own cancel`, async t => {
    const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
    await path.prepare?.(f);
    let posted: AbortSignal | undefined;
    f.intercept(async (request, response) => request.method === 'POST' && request.path === path.post ? (posted = request.signal, hangOffers(request)) : withSecondVersion(request, response));
    const rejected = assert.rejects(path.run(f, life.signal), (error: Error) => error.name === 'AbortError');
    await spinUntil(f, () => !!f.runtime.getSnapshot().starting && !!posted);
    f.runtime.getSnapshot().starting!.stop();
    await rejected;
    assert.equal(f.runtime.getSnapshot().starting, undefined); assert.equal(f.runtime.getSnapshot().session, undefined);
    f.clock.advance?.(25000); await spin();
    assert.equal(f.runtime.getSnapshot().startTimedOut, undefined);
  });

  if (!path.live) test(`P2-61 ${path.name}: an accepted start never drawn stops at the deadline with a named diagnostic, and Try again reruns it`, async t => {
    const {openAppLogContext, captureAppLog} = await import('../src/platform/app-log.ts');
    const closeLog = openAppLogContext(() => true); t.after(closeLog);
    const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
    await path.prepare?.(f);
    hold(f);
    await path.run(f, life.signal).catch(() => {});
    await spinUntil(f, () => !!f.runtime.getSnapshot().starting);
    let s = f.runtime.getSnapshot();
    assert.ok(s.starting, 'accepted but not drawable: still starting'); assert.equal(s.startTimedOut, undefined);
    for (let step = 0; step < 10; step++) {f.clock.advance?.(2500); await spin();}
    s = f.runtime.getSnapshot();
    assert.equal(s.starting, undefined);
    assert.ok(s.startTimedOut, '"Couldn\'t start playback" with Try again');
    assert.equal(s.session, undefined, 'the accepted start was ended, nothing half-started is left');
    assert.ok(captureAppLog().entries.some(entry => /^playback_not_drawn_(session|offers|presentation)$/.test(entry.code)), 'a named diagnostic is recorded');
    // Try again reruns the same start, now unobstructed.
    f.intercept(async (_request, response) => withSecondVersion(_request, response));
    const posts = () => f.log.filter(request => request.method === 'POST' && request.path === path.post).length, before = posts();
    await s.startTimedOut!.retry();
    for (let step = 0; step < 3; step++) await path.drawn(f);
    assert.equal(posts(), before + 1, 'the same start ran again');
    assert.equal(f.runtime.getSnapshot().startTimedOut, undefined); assert.ok(f.runtime.getSnapshot().presentation, 'and drew its real dock');
  });
  const slowLiveToBound = async (t: TestContext) => {
    // Mock time before setup: a real timer armed by setup's own requests could not be cleared by a
    // mocked clearTimeout, and would hold the process open after the file's tests end.
    t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: Date.now()});
    const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
    slowChannel(f, {ready: false});
    const run = path.run(f, life.signal).catch(() => {});
    await spinUntil(f, () => !!f.runtime.getSnapshot().starting);
    assert.ok(f.runtime.getSnapshot().starting, 'the starting dock is drawn on acceptance');
    // Past the 20 s watchdog with no stream: still starting, never timed out.
    await tickLive(t, f, 25_000, () => {
      const s = f.runtime.getSnapshot();
      assert.ok(s.starting, 'the starting dock stays'); assert.equal(s.startTimedOut, undefined);
    });
    // Past the channel's own 60 s bound.
    await tickLive(t, f, 40_000);
    await run;
    return f;
  };
  if (path.live) test(`P2-61 ${path.name}: a Live tune never trips the 20 s watchdog and is ended at the channel's 60 s bound`, async t => {
    const f = await slowLiveToBound(t), s = f.runtime.getSnapshot();
    assert.equal(s.startTimedOut, undefined, 'it is the channel bound, not the watchdog');
    assert.equal(s.presentation, undefined);
    assert.ok(f.log.some(r => r.method === 'DELETE' && r.path.startsWith('/v1/playback/sessions/live')), 'the tuner is freed');
  });
  if (path.live) test(`P2-61 ${path.name}: channel_start_timeout ends the starting dock and is reported`, async t => {
    const f = await slowLiveToBound(t), s = f.runtime.getSnapshot();
    assert.equal(s.starting, undefined, 'the start ended');
  });
}

/** A Live tune answered at generation 0 with no url, as the live server does: reads stay without a
 * url (generation 1) until `ready` is set, then the real session shows. `reason` makes reads say
 * the source is recoverable. Only the channel's own polling is on the (mocked) real timers. */
function slowChannel(f: ReturnType<typeof setup>, state: {ready: boolean; reason?: string}) {
  const early = (body: any, generation: number) => ({...body, presentation: {...body.presentation, generation, url: '',
    linear: {...body.presentation.linear, windowEndMs: 0, liveEdgeMs: 0, state: state.reason ? 'recoverable' : 'preparing', ...(state.reason ? {errorCode: state.reason} : {})}}});
  f.intercept(async (request, response) => {
    if (!/^\/v1\/playback\/sessions(\/live_session_\d+.*)?$/.test(request.path) || !response.body) return response;
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') return {...response, body: early(response.body, 0)};
    if (state.ready) return response;
    if (request.method === 'GET') return {...response, body: early(response.body, 1)};
    if (request.method === 'PUT') { const b = response.body as {session?: unknown}; return b.session ? {...response, body: {...b, session: early(b.session, 1)}} : response; }
    return response;
  });
}
/** Moves the mocked channel clock and the runtime's manual clock together in 500 ms polls. */
async function tickLive(t: TestContext, f: ReturnType<typeof setup>, totalMs: number, each?: () => void) {
  for (let elapsed = 0; elapsed < totalMs; elapsed += 500) { t.mock.timers.tick(500); f.clock.advance(500); await settle(); each?.(); }
}

test('P2-61 Live: a slow stream keeps the starting dock with no watchdog, then the real dock replaces it', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: Date.now()}); // before setup (see above)
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  const state = {ready: false};
  slowChannel(f, state);
  const run = f.runtime.playFromGuide(homeChannel.channel, {signal: life.signal, isCurrent: () => true}).catch(() => {});
  await spin();
  assert.ok(f.runtime.getSnapshot().starting, 'starting from acceptance');
  await tickLive(t, f, 8_000, () => {
    const s = f.runtime.getSnapshot();
    assert.ok(s.starting, 'the starting dock stays while the stream is absent'); assert.equal(s.startTimedOut, undefined);
    assert.equal(s.presentation, undefined);
  });
  state.ready = true;
  await tickLive(t, f, 1_500);
  await f.arriveLive(); await run;
  const s = f.runtime.getSnapshot();
  assert.equal(s.starting, undefined, 'the start clears');
  assert.equal(s.startTimedOut, undefined);
  assert.ok(s.presentation, 'the real dock arrives');
  assert.equal(f.log.filter(r => r.method === 'PATCH' && (r.body as {seek?: {live?: boolean}})?.seek?.live === true).length, 1, 'exactly one go-live');
});

test('P2-61 Live: a recoverable session reads as waiting on the channel source within one poll', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: Date.now()}); // before setup (see above)
  const f = setup(t, {}, {}, undefined, undefined, {homeFence: false}), life = new AbortController();
  const state: {ready: boolean; reason?: string} = {ready: false};
  slowChannel(f, state);
  const run = f.runtime.playFromGuide(homeChannel.channel, {signal: life.signal, isCurrent: () => true}).catch(() => {});
  await spin();
  assert.equal(f.runtime.getSnapshot().starting?.waiting, undefined, 'no reason is claimed before the server gives one');
  state.reason = 'source_unavailable';
  await tickLive(t, f, 500);
  assert.equal(f.runtime.getSnapshot().starting?.waiting, 'channel_source');
  f.runtime.getSnapshot().starting!.stop(); await tickLive(t, f, 1_000); await run;
});

test('P2-61 local queue: the stored title is drawn at once, replaced in place, Stopped and watched', async t => {
  const {openAppLogContext, captureAppLog} = await import('../src/platform/app-log.ts');
  const closeLog = openAppLogContext(() => true); t.after(closeLog);
  const lifetime = () => ({signal: new AbortController().signal, isCurrent: () => true});
  // 1. Drawn from the prepared queue's first stored title while the native load is outstanding, then replaced in place.
  {
    const port = new ControlledLocalMedia(), f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
    let release!: () => void;
    port.gate = () => new Promise<void>(resolve => {release = resolve;});
    const seen: {starting: boolean; presented: boolean}[] = [];
    const off = f.runtime.subscribe(() => {const s = f.runtime.getSnapshot(); seen.push({starting: !!s.starting, presented: !!s.presentation});}); t.after(off);
    const start = f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {}}, lifetime());
    assert.equal(startTitle(f), 'Stored song 1');
    assert.equal(f.runtime.getSnapshot().presentation, undefined, 'never claims playing');
    await spin(); release(); await start; await spin();
    const s = f.runtime.getSnapshot();
    assert.equal(s.starting, undefined); assert.equal(s.presentation?.kind, 'music');
    const first = seen.findIndex(value => value.starting);
    assert.ok(first >= 0 && seen.slice(first).every(value => value.starting || value.presented), 'no publication between them draws nothing');
  }
  // 2. Stop while the native load is outstanding is the viewer's own cancel and releases the preparation.
  {
    const port = new ControlledLocalMedia(), f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
    let released = 0;
    port.gate = () => new Promise<void>((_, reject) => port.contexts.at(-1)!.signal.addEventListener('abort', () => reject(new DOMException('Local selection retired', 'AbortError')), {once: true}));
    const early = f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {released++;}}, lifetime());
    const rejected = assert.rejects(early, (error: Error) => error.name === 'AbortError');
    f.runtime.getSnapshot().starting!.stop(); await rejected; await spin();
    assert.equal(f.runtime.getSnapshot().starting, undefined); assert.equal(f.runtime.getSnapshot().local, undefined);
    assert.equal(released, 1, 'the preparation is released exactly once');
    f.clock.advance?.(25000); await spin();
    assert.equal(f.runtime.getSnapshot().startTimedOut, undefined);
  }
  // 3. An accepted load whose dock is never drawn stops at the deadline with a diagnostic; Stop after acceptance also cancels.
  for (const how of ['deadline', 'stop'] as const) {
    const port = new ControlledLocalMedia(), f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
    let released = 0;
    // The native port accepted the load but has not yet reported the title as playing.
    const observed = port.observe.bind(port); port.observe = () => ({...observed(), state: 'loading'});
    await f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {released++;}}, lifetime()); await spin();
    let s = f.runtime.getSnapshot();
    assert.equal(s.presentation, undefined);
    if (how === 'stop') {
      assert.ok(s.starting, 'accepted but not drawable: still starting');
      s.starting!.stop(); await spin();
      s = f.runtime.getSnapshot();
      assert.equal(s.starting, undefined); assert.equal(s.local, undefined);
      f.clock.advance?.(25000); await spin();
      assert.equal(f.runtime.getSnapshot().startTimedOut, undefined);
    } else {
      for (let step = 0; step < 10; step++) {f.clock.advance?.(2500); await spin();}
      s = f.runtime.getSnapshot();
      assert.equal(s.starting, undefined); assert.ok(s.startTimedOut, '"Couldn\'t start playback" with Try again');
      assert.ok(captureAppLog().entries.some(entry => /^playback_not_drawn_(session|offers|presentation)$/.test(entry.code)));
    }
    assert.equal(released, 1, `${how}: the preparation is released exactly once`);
  }
});

test('P2-61 local queue: Try again after the watchdog re-acquires a fresh resource and never replays the released one', async t => {
  const lifetime = () => ({signal: new AbortController().signal, isCurrent: () => true});
  const stall = async () => {
    const port = new ControlledLocalMedia(), f = setup(t, {}, {createPlayer: () => port, createRender: null}, localScope);
    const observed = port.observe.bind(port); port.observe = () => ({...observed(), state: 'loading'});
    return {port, f};
  };
  const expire = async (f: ReturnType<typeof setup>) => {for (let step = 0; step < 10; step++) {f.clock.advance?.(2500); await spin();}};
  // 1. reacquire supplies resource #2, which is what plays; #1 is released once and never re-attached.
  {
    const {port, f} = await stall();
    const log: string[] = [];
    const second = {preparation: {...localPreparation(), preparationId: 'fresh_preparation'}, release: async () => {log.push('release2');}};
    const first = {preparation: localPreparation(), release: async () => {log.push('release1');}, reacquire: async () => {log.push('reacquire'); return second;}};
    await f.runtime.playLocalQueue(first, lifetime()); await spin(); await expire(f);
    const timedOut = f.runtime.getSnapshot().startTimedOut;
    assert.ok(timedOut); assert.deepEqual(log, ['release1']);
    port.observe = ControlledLocalMedia.prototype.observe.bind(port);
    await timedOut!.retry(); await spin();
    assert.deepEqual(log, ['release1', 'reacquire'], 'resource #1 is released once and not touched again; #2 is live');
    assert.equal(f.runtime.getSnapshot().startTimedOut, undefined);
    assert.equal(f.runtime.getSnapshot().local?.preparation.preparationId, 'fresh_preparation');
  }
  // 2. No reacquire: Try again fails with the named error, not a silent no-op.
  {
    const {f} = await stall();
    let released = 0;
    await f.runtime.playLocalQueue({preparation: localPreparation(), release: async () => {released++;}}, lifetime()); await spin(); await expire(f);
    const timedOut = f.runtime.getSnapshot().startTimedOut;
    assert.ok(timedOut);
    await assert.rejects(timedOut!.retry(), /local_playback_unavailable/);
    assert.equal(released, 1);
  }
});
