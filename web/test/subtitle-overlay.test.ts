import test from 'node:test';
import assert from 'node:assert/strict';
import {overlayGeometry, pictureBox, SubtitleOverlay, type OverlayEnvironment, type OverlayVideo} from '../src/platform/playback/subtitles/overlay.ts';
import {ImageSubtitleRenderer, cueIndexAt, type ImageWorkerLike} from '../src/platform/playback/subtitles/image-renderer.ts';
import {StyledSubtitleRenderer, type LibassOptions} from '../src/platform/playback/subtitles/ass-renderer.ts';
import {declareSubtitleRendering, subtitleRenderingKinds} from '../src/platform/playback/subtitle-declaration.ts';
import {fallbackFontsFor, scriptsIn} from '../src/platform/playback/subtitles/fallback-fonts.ts';
import {decodeSubtitleText, type DrawnSubtitleRenderer, type OverlayClock, type OverlayGeometry, type ReadSubtitleFile} from '../src/platform/playback/subtitles/types.ts';
import type {ImageWorkerReply, ImageWorkerRequest} from '../src/platform/playback/subtitles/image-protocol.ts';
import {SubtitleSurface} from '../src/platform/playback/subtitle-surface.ts';

const settle = () => new Promise(resolve => setTimeout(resolve, 0));
const noRead: ReadSubtitleFile = () => Promise.reject(new Error('not read in this test'));

function canvas() {
  const calls: unknown[][] = [];
  const context = {imageSmoothingEnabled: false, imageSmoothingQuality: 'low', clearRect: (...a: number[]) => calls.push(['clear', ...a]), drawImage: (image: unknown, ...a: number[]) => calls.push(['draw', image, ...a.map(n => Math.round(n * 100) / 100)])};
  const element = {width: 300, height: 150, style: {} as Record<string, string>, getContext: () => context, offsetParent: null, parentElement: null};
  return {element: element as unknown as HTMLCanvasElement, style: element.style, calls, raw: element};
}

function video(initial: Partial<{currentTime: number; paused: boolean; seeking: boolean; readyState: number; playbackRate: number; videoWidth: number; videoHeight: number; box: {left: number; top: number; width: number; height: number}}> = {}) {
  const listeners = new Map<string, Set<() => void>>();
  let frame: (() => void) | undefined;
  const state = {currentTime: 0, paused: true, seeking: false, readyState: 4, playbackRate: 1, videoWidth: 1920, videoHeight: 1080, box: {left: 0, top: 0, width: 1280, height: 720}, ...initial};
  const element: OverlayVideo & {state: typeof state; fire(type: string): void; nextFrame(): boolean; listening(): number} = {
    state,
    get currentTime() { return state.currentTime; }, get paused() { return state.paused; }, get seeking() { return state.seeking; }, get readyState() { return state.readyState; }, get playbackRate() { return state.playbackRate; },
    get videoWidth() { return state.videoWidth; }, get videoHeight() { return state.videoHeight; },
    addEventListener(type, listener) { if (!listeners.has(type)) listeners.set(type, new Set()); listeners.get(type)!.add(listener); },
    removeEventListener(type, listener) { listeners.get(type)?.delete(listener); },
    getBoundingClientRect: () => state.box,
    requestVideoFrameCallback(callback) { frame = callback; return 1; },
    cancelVideoFrameCallback() { frame = undefined; },
    fire(type) { for (const listener of [...(listeners.get(type) ?? [])]) listener(); },
    nextFrame() { const run = frame; frame = undefined; run?.(); return !!run; },
    listening() { let n = 0; for (const set of listeners.values()) n += set.size; return n; },
  };
  return element;
}

function environment(overrides: Partial<{fit: 'contain' | 'cover'; density: number}> = {}) {
  const facts = {fit: 'contain' as 'contain' | 'cover', density: 2, watching: 0, changed: () => {}, ...overrides};
  const env: OverlayEnvironment = {
    watchLayout(_video, changed) { facts.watching++; facts.changed = changed; return () => { facts.watching--; }; },
    fit: () => facts.fit, density: () => facts.density, origin: () => ({left: 0, top: 0}), frame: () => 0, cancelFrame: () => {},
  };
  return {env, facts};
}

function recorder(area: 'picture' | 'frame' = 'picture') {
  const seen = {loads: 0, sizes: [] as OverlayGeometry[], clocks: [] as OverlayClock[], disposed: 0, failed: (_: unknown) => {}};
  const renderer: DrawnSubtitleRenderer = {
    area,
    load(_source, context) { seen.loads++; seen.failed = context.failed; return Promise.resolve(); },
    resize(geometry) { seen.sizes.push(geometry); }, sync(clock) { seen.clocks.push(clock); }, dispose() { seen.disposed++; },
  };
  return {renderer, seen};
}

// ── Where ──────────────────────────────────────────────────────────────
test('the picture inside the element, letterboxed or filling it', () => {
  const cases: {name: string; element: [number, number]; video: [number, number]; fit: 'contain' | 'cover'; want: [number, number, number, number]}[] = [
    {name: 'same shape', element: [1280, 720], video: [1920, 1080], fit: 'contain', want: [0, 0, 1280, 720]},
    {name: 'a wide film on a 16:9 screen has bars above and below', element: [1920, 1080], video: [1920, 800], fit: 'contain', want: [0, 140, 1920, 800]},
    {name: '4:3 on 16:9 has bars at the sides', element: [1920, 1080], video: [1440, 1080], fit: 'contain', want: [240, 0, 1440, 1080]},
    {name: 'filling the screen crops the sides of a wide film', element: [1920, 1080], video: [1920, 800], fit: 'cover', want: [-336, 0, 2592, 1080]},
    {name: 'a portrait window', element: [400, 800], video: [1920, 1080], fit: 'contain', want: [0, 287.5, 400, 225]},
    {name: 'no size yet', element: [1280, 720], video: [0, 0], fit: 'contain', want: [0, 0, 0, 0]},
    {name: 'an element with no box', element: [0, 0], video: [1920, 1080], fit: 'contain', want: [0, 0, 0, 0]},
  ];
  for (const c of cases) { const b = pictureBox({width: c.element[0], height: c.element[1]}, {width: c.video[0], height: c.video[1]}, c.fit); assert.deepEqual([b.x, b.y, b.width, b.height], c.want, c.name); }
});

test('the canvas is the picture or the whole player, at the screen’s density, and never more than a 4K frame', () => {
  const picture = pictureBox({width: 1920, height: 1080}, {width: 1920, height: 800}, 'contain');
  const styled = overlayGeometry({width: 1920, height: 1080}, picture, 'picture', 2);
  assert.deepEqual(styled.css, {x: 0, y: 140, width: 1920, height: 800});
  assert.deepEqual(styled.geometry, {width: 3840, height: 1600, picture: {x: 0, y: 0, width: 3840, height: 1600}});
  const images = overlayGeometry({width: 1920, height: 1080}, picture, 'frame', 1);
  assert.deepEqual(images.css, {x: 0, y: 0, width: 1920, height: 1080});
  assert.deepEqual(images.geometry, {width: 1920, height: 1080, picture: {x: 0, y: 140, width: 1920, height: 800}});
  const huge = overlayGeometry({width: 3840, height: 2160}, pictureBox({width: 3840, height: 2160}, {width: 3840, height: 2160}, 'contain'), 'frame', 3);
  assert.ok(huge.geometry.width * huge.geometry.height <= 3840 * 2160 + 4000, 'capped');
  assert.equal(huge.geometry.width, 3840);
});

// ── When ───────────────────────────────────────────────────────────────
test('the overlay places the canvas, tells the renderer the subtitle’s time, and releases it', async () => {
  const c = canvas(), v = video({currentTime: 100, box: {left: 10, top: 20, width: 1920, height: 1080}, videoHeight: 800}), {env, facts} = environment(), {renderer, seen} = recorder();
  const overlay = new SubtitleOverlay(v, c.element, {environment: env, renderers: () => Promise.resolve(renderer)});
  assert.equal(c.style.display, 'none');
  await overlay.present({kind: 'ass', script: 'https://server.example/x'}, noRead);
  assert.equal(seen.loads, 1);
  assert.deepEqual([c.style.left, c.style.top, c.style.width, c.style.height, c.style.display], ['10px', '160px', '1920px', '800px', 'block']);
  assert.deepEqual(seen.sizes, [{width: 3840, height: 1600, picture: {x: 0, y: 0, width: 3840, height: 1600}}]);
  assert.deepEqual(seen.clocks.at(-1), {timeSeconds: 100, paused: true, rate: 1});

  // The stream sits 1.5 s later on the element's clock and the viewer shows subtitles 0.3 s later.
  overlay.timeline(1.5);
  overlay.setDelay(0.3);
  assert.ok(Math.abs(seen.clocks.at(-1)!.timeSeconds - 98.2) < 1e-9);

  // Playing: every video frame. Speed, waiting for media and a seek are all told.
  v.state.paused = false; v.state.playbackRate = 1.5; v.state.currentTime = 101;
  v.fire('playing');
  assert.deepEqual([seen.clocks.at(-1)!.paused, seen.clocks.at(-1)!.rate], [false, 1.5]);
  const before = seen.clocks.length;
  v.state.currentTime = 101.04;
  assert.ok(v.nextFrame(), 'a frame callback was asked for');
  assert.equal(seen.clocks.length, before + 1);
  assert.ok(v.nextFrame(), 'and again after each frame');
  v.state.readyState = 2;
  v.fire('waiting');
  assert.equal(seen.clocks.at(-1)!.paused, true, 'waiting for media holds the subtitle');
  assert.equal(v.nextFrame(), false, 'no frame loop while nothing moves');
  v.state.readyState = 4; v.state.seeking = true; v.state.currentTime = 500;
  v.fire('seeking');
  assert.deepEqual([seen.clocks.at(-1)!.paused, Math.round(seen.clocks.at(-1)!.timeSeconds * 10) / 10], [true, 498.2]);

  // Until the port says where a new stream sits, nothing is shown.
  overlay.reset();
  assert.equal(c.style.display, 'none');
  const quiet = seen.clocks.length;
  v.fire('timeupdate');
  assert.equal(seen.clocks.length, quiet);
  overlay.timeline(0);
  assert.equal(c.style.display, 'block');

  // The window changes, the fit mode changes, the screen density changes.
  v.state.box = {left: 0, top: 0, width: 960, height: 540};
  facts.changed();
  assert.deepEqual([c.style.top, c.style.width, c.style.height], ['70px', '960px', '400px']);
  assert.deepEqual(seen.sizes.at(-1), {width: 1920, height: 800, picture: {x: 0, y: 0, width: 1920, height: 800}});
  const sizes = seen.sizes.length;
  facts.changed();
  assert.equal(seen.sizes.length, sizes, 'an unchanged picture is not resized again');
  facts.fit = 'cover';
  facts.changed();
  assert.deepEqual([c.style.left, c.style.top, c.style.width, c.style.height], ['-168px', '0px', '1296px', '540px']);
  facts.density = 1;
  facts.changed();
  assert.deepEqual([seen.sizes.at(-1)!.width, seen.sizes.at(-1)!.height], [1296, 540]);

  // The viewer's size and position.
  overlay.adjust({scale: 1.25, raise: 0.06});
  assert.equal(c.style.transform, 'translateY(-6.00%) scale(1.25)');
  assert.equal(c.style.transformOrigin, '50% 100%');
  overlay.adjust({scale: 1, raise: 0});
  assert.equal(c.style.transform, '');

  // Another subtitle replaces this one; none releases everything.
  const second = recorder('frame');
  const next = new SubtitleOverlay(v, c.element, {environment: env, renderers: () => Promise.resolve(second.renderer)});
  void next;
  overlay.clear();
  assert.equal(seen.disposed, 1);
  assert.equal(c.style.display, 'none');
  assert.equal(facts.watching, 0);
  assert.equal(v.listening(), 0, 'no listener is left on the element');
  assert.equal(overlay.active, false);
});

test('a subtitle that cannot be loaded, one that is overtaken, and one that stops later', async () => {
  const c = canvas(), v = video(), {env} = environment();
  const broken: DrawnSubtitleRenderer = {area: 'picture', load: () => Promise.reject(new Error('cannot')), resize() {}, sync() {}, dispose() { disposals.push('broken'); }};
  const disposals: string[] = [];
  let release: (() => void) | undefined;
  const slow: DrawnSubtitleRenderer = {area: 'frame', load: () => new Promise(resolve => { release = resolve; }), resize() {}, sync() {}, dispose() { disposals.push('slow'); }};
  const good = recorder('frame');
  const queue = [broken, slow, good.renderer];
  const overlay = new SubtitleOverlay(v, c.element, {environment: env, renderers: () => Promise.resolve(queue.shift()!)});
  await assert.rejects(overlay.present({kind: 'pgs', data: 'u'}, noRead), /cannot/);
  assert.deepEqual(disposals, ['broken']);
  assert.equal(overlay.active, false);
  // A second choice while the first is still loading: the first is dropped when it finishes.
  const first = overlay.present({kind: 'pgs', data: 'u'}, noRead);
  await settle();
  const second = overlay.present({kind: 'dvb', data: 'u'}, noRead);
  release?.();
  await assert.rejects(first, /cancelled/);
  await second;
  assert.deepEqual(disposals, ['broken', 'slow']);
  assert.equal(c.style.display, 'block');
  // Its worker dies afterwards: the canvas is cleared and the player is told once.
  const reasons: unknown[] = [];
  const again = recorder();
  queue.push(again.renderer);
  await overlay.present({kind: 'ass', script: 'u'}, noRead, reason => reasons.push(reason));
  assert.equal(good.seen.disposed, 1);
  again.seen.failed('worker gone');
  assert.deepEqual(reasons, ['worker gone']);
  assert.equal(again.seen.disposed, 1);
  assert.equal(c.style.display, 'none');
  again.seen.failed('twice');
  assert.equal(reasons.length, 1);
  overlay.dispose();
  await assert.rejects(overlay.present({kind: 'ass', script: 'u'}, noRead), /closed/);
});

test('the text surface passes the stream’s place on the clock to the overlay', () => {
  const told: (number | null | 'reset')[] = [];
  const host = {addTextTrack: () => ({mode: 'disabled', cues: null, addCue() {}, removeCue() {}}), textTracks: Object.assign([], {})};
  const surface = new SubtitleSurface(host, {cue: () => ({align: '', snapToLines: false, line: 0}), lift: false, companion: {reset: () => told.push('reset'), timeline: offset => told.push(offset)}});
  surface.reset(); surface.timeline(2.5); surface.timeline(null);
  assert.deepEqual(told, ['reset', 2.5, null]);
});

// ── Image subtitles ────────────────────────────────────────────────────
function imageWorker(ready: ImageWorkerReply) {
  const sent: ImageWorkerRequest[] = [];
  const worker: ImageWorkerLike & {reply(message: ImageWorkerReply): void; terminated: boolean} = {
    onmessage: null, onerror: null, terminated: false,
    postMessage(message) { sent.push(message); if (message.type === 'load') queueMicrotask(() => worker.onmessage?.({data: ready})); },
    terminate() { worker.terminated = true; },
    reply(message) { worker.onmessage?.({data: message}); },
  };
  return {worker, sent};
}
const track = (forced = [0, 0, 0]): ImageWorkerReply => ({type: 'ready', width: 1920, height: 1080, starts: Float64Array.from([1000, 3000, 9000]), ends: Float64Array.from([2500, 4000, 9500]), forced: Uint8Array.from(forced), skipped: 0});

test('image subtitles: the cue due now is asked for, drawn scaled onto the picture, and the next one is ready in time', async () => {
  const c = canvas(), {worker, sent} = imageWorker(track());
  const renderer = new ImageSubtitleRenderer({spawn: () => worker, fromPixels: (_rgba, width, height) => `pixels ${width}x${height}` as unknown as CanvasImageSource});
  const bytes = new Uint8Array([1, 2, 3]);
  await renderer.load({kind: 'pgs', data: bytes}, {canvas: c.element, read: noRead, signal: new AbortController().signal, failed: () => {}});
  assert.deepEqual([sent[0]!.type, (sent[0] as {kind: string}).kind, [...new Uint8Array((sent[0] as {data: ArrayBuffer}).data)]], ['load', 'pgs', [1, 2, 3]]);
  assert.deepEqual([...bytes], [1, 2, 3], 'the caller’s bytes are still the caller’s');
  // A 1920×800 picture letterboxed in a 1920×1080 player at density 1: the subtitle keeps its scale and is centred on the picture.
  renderer.resize({width: 1920, height: 1080, picture: {x: 0, y: 140, width: 1920, height: 800}});
  assert.deepEqual([c.raw.width, c.raw.height], [1920, 1080]);
  renderer.sync({timeSeconds: 0.2, paused: false, rate: 1});
  assert.deepEqual(sent.slice(1), [{type: 'paint', index: 0}], 'the cue 0.8 s away is painted ahead');
  worker.reply({type: 'frame', index: 0, rects: [{x: 100, y: 900, width: 200, height: 50, rgba: new ArrayBuffer(200 * 50 * 4)}]});
  assert.equal(c.calls.filter(call => call[0] === 'draw').length, 0, 'nothing is due yet');
  renderer.sync({timeSeconds: 1.0, paused: false, rate: 1});
  assert.deepEqual(c.calls.at(-1), ['draw', 'pixels 200x50', 100, 900, 200, 50], 'full-frame subtitle on a cropped picture: same scale, 140 px bar offset cancels the centring');
  assert.deepEqual(sent.at(-1), {type: 'paint', index: 1});
  const draws = c.calls.length;
  renderer.sync({timeSeconds: 1.5, paused: false, rate: 1});
  assert.equal(c.calls.length, draws, 'an unchanged cue is not drawn again');
  renderer.sync({timeSeconds: 2.6, paused: false, rate: 1});
  assert.deepEqual(c.calls.at(-1)!.slice(0, 1), ['clear'], 'between cues the canvas is clear');
  // A smaller window: half scale, and the picture now fills the player.
  renderer.sync({timeSeconds: 1.2, paused: true, rate: 1});
  renderer.resize({width: 960, height: 540, picture: {x: 0, y: 0, width: 960, height: 540}});
  assert.deepEqual(c.calls.at(-1), ['draw', 'pixels 200x50', 50, 450, 100, 25]);
  // A seek to a cue that has not been painted: clear at once, drawn when it arrives.
  renderer.sync({timeSeconds: 9.1, paused: true, rate: 1});
  assert.deepEqual(c.calls.at(-1)!.slice(0, 1), ['clear']);
  assert.deepEqual(sent.at(-1), {type: 'paint', index: 2});
  let closed = 0;
  worker.reply({type: 'frame', index: 2, rects: [{x: 0, y: 0, width: 10, height: 10, bitmap: {close: () => { closed++; }} as unknown as ImageBitmap}]});
  assert.equal(c.calls.at(-1)![0], 'draw');
  renderer.dispose();
  assert.equal(worker.terminated, true);
  assert.equal(closed, 1, 'bitmaps are released');
  assert.deepEqual(c.calls.at(-1)!.slice(0, 1), ['clear']);
});

test('image subtitles: "Forced only" shows the lines the disc marks forced and nothing else', async () => {
  const c = canvas(), {worker, sent} = imageWorker(track([0, 1, 0]));
  const renderer = new ImageSubtitleRenderer({spawn: () => worker, fromPixels: (_rgba, width, height) => `pixels ${width}x${height}` as unknown as CanvasImageSource});
  await renderer.load({kind: 'pgs', data: new Uint8Array(3)}, {canvas: c.element, read: noRead, signal: new AbortController().signal, failed: () => {}});
  assert.deepEqual(renderer.cues, {forced: 1, total: 3});
  renderer.resize({width: 1920, height: 1080, picture: {x: 0, y: 0, width: 1920, height: 1080}});
  renderer.setForcedOnly(true);
  renderer.sync({timeSeconds: 1.2, paused: false, rate: 1});
  assert.deepEqual(sent.slice(1), [{type: 'paint', index: 1}], 'an ordinary line is neither painted nor drawn; the forced one after it is painted ahead');
  worker.reply({type: 'frame', index: 1, rects: [{x: 0, y: 0, width: 10, height: 10, rgba: new ArrayBuffer(400)}]});
  renderer.sync({timeSeconds: 3.5, paused: false, rate: 1});
  assert.deepEqual(c.calls.at(-1), ['draw', 'pixels 10x10', 0, 0, 10, 10]);
  renderer.sync({timeSeconds: 9.1, paused: true, rate: 1});
  assert.equal(sent.some(m => m.type === 'paint' && m.index === 2), false);
  assert.deepEqual(c.calls.at(-1)!.slice(0, 1), ['clear']);
  // Every line again.
  renderer.setForcedOnly(false);
  renderer.sync({timeSeconds: 9.1, paused: true, rate: 1});
  assert.deepEqual(sent.at(-1), {type: 'paint', index: 2});
  renderer.dispose();
});

test('image subtitles: a file the worker cannot read, and a worker that dies', async () => {
  const context = (failed: (reason: unknown) => void = () => {}) => ({canvas: canvas().element, read: noRead, signal: new AbortController().signal, failed});
  const unreadable = imageWorker({type: 'failed', code: 'no_pictures'});
  await assert.rejects(new ImageSubtitleRenderer({spawn: () => unreadable.worker}).load({kind: 'dvb', data: new Uint8Array(4)}, context()), /no pictures/);
  assert.equal(unreadable.worker.terminated, true);
  const dying = imageWorker(track()), reasons: unknown[] = [];
  const renderer = new ImageSubtitleRenderer({spawn: () => dying.worker});
  await renderer.load({kind: 'vobsub', index: new TextEncoder().encode('size: 720x480'), data: new Uint8Array(4)}, context(reason => reasons.push(reason)));
  assert.equal((dying.sent[0] as {index?: string}).index, 'size: 720x480');
  dying.worker.onerror?.({});
  assert.equal(reasons.length, 1);
  await assert.rejects(new ImageSubtitleRenderer({spawn: () => imageWorker(track()).worker}).load({kind: 'ass', script: 'x'}, context()), /Not an image subtitle/);
  const cases: [number, number][] = [[0, -1], [1000, 0], [2499, 0], [2500, -1], [3000, 1], [9499, 2], [9500, -1]];
  for (const [time, want] of cases) assert.equal(cueIndexAt([1000, 3000, 9000], [2500, 4000, 9500], time), want, `at ${time}`);
});

// ── Styled subtitles ───────────────────────────────────────────────────
const script = '[Script Info]\nPlayResX: 1920\n\n[V4+ Styles]\n\n[Events]\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,مرحبا 你好';
function libass() {
  const calls: unknown[][] = [];
  let options: LibassOptions | undefined;
  class Engine {
    constructor(o: LibassOptions) { options = o; queueMicrotask(() => o.onReady()); }
    resize(width: number, height: number) { calls.push(['resize', width, height]); }
    setCurrentTime(seconds: number) { calls.push(['time', Math.round(seconds * 1000) / 1000]); }
    setIsPaused(paused: boolean, seconds: number) { calls.push([paused ? 'pause' : 'play', Math.round(seconds * 1000) / 1000]); }
    setRate(rate: number) { calls.push(['rate', rate]); }
    dispose() { calls.push(['dispose']); }
  }
  return {Engine, calls, options: () => options!};
}

test('styled subtitles: the script is decoded, fonts are listed, and the worker is told the time only when it needs to be', async () => {
  const c = canvas(), engine = libass();
  let now = 0;
  const renderer = new StyledSubtitleRenderer({load: () => Promise.resolve(engine.Engine), now: () => now});
  const read: ReadSubtitleFile = (url, max) => { assert.equal(url, 'https://server.example/original.ass'); assert.ok(max >= 1024 * 1024); return Promise.resolve({bytes: new TextEncoder().encode('﻿' + script), charset: 'windows-1252'}); };
  renderer.resize({width: 3840, height: 2160, picture: {x: 0, y: 0, width: 3840, height: 2160}});
  await renderer.load({kind: 'ass', script: 'https://server.example/original.ass', fonts: ['https://server.example/fonts/0'], cjkFont: 'https://server.example/v1/subtitle-fonts/cjk'}, {canvas: c.element, read, signal: new AbortController().signal, failed: () => {}});
  const o = engine.options();
  assert.ok(o.subContent.startsWith('[Script Info]'), 'a byte-order mark wins over the declared encoding and is dropped');
  assert.equal(o.fonts[0], 'https://server.example/fonts/0', 'the file’s own fonts first');
  assert.deepEqual(o.fonts.slice(1).map(url => url.split('/').pop()), ['NotoSansArabic-Regular.ttf', 'cjk'], 'then fonts for the scripts in the text (CJK from the server)');
  assert.ok(o.workerUrl.endsWith('/vendor/libass-wasm-4.2.4/subtitles-octopus-worker.js'));
  assert.ok(o.fallbackFont.endsWith('/vendor/libass-wasm-4.2.4/default.woff2'));
  assert.deepEqual(engine.calls, [['resize', 2560, 1440]], 'a 4K picture is drawn at 1440 lines and scaled up');
  engine.calls.length = 0;
  renderer.sync({timeSeconds: 10, paused: true, rate: 1});
  assert.deepEqual(engine.calls, [['rate', 1], ['pause', 10]]);
  renderer.sync({timeSeconds: 10, paused: true, rate: 1});
  assert.equal(engine.calls.length, 2, 'nothing changed, nothing sent');
  renderer.sync({timeSeconds: 10, paused: false, rate: 1});
  assert.deepEqual(engine.calls.at(-1), ['play', 10]);
  // Sixty frames of ordinary playback: a correction every half second, not one per frame.
  const sent = engine.calls.length;
  for (let frame = 1; frame <= 60; frame++) { now = frame * (1000 / 60); renderer.sync({timeSeconds: 10 + frame / 60, paused: false, rate: 1}); }
  assert.equal(engine.calls.length - sent, 2);
  // A seek is told at once; so is a change of speed; a paused frame step too.
  now += 10;
  renderer.sync({timeSeconds: 300, paused: false, rate: 1});
  assert.deepEqual(engine.calls.at(-1), ['time', 300]);
  renderer.sync({timeSeconds: 300, paused: false, rate: 2});
  assert.deepEqual(engine.calls.slice(-2), [['rate', 2], ['time', 300]]);
  now += 100;
  renderer.sync({timeSeconds: 300.2, paused: false, rate: 2});
  assert.deepEqual(engine.calls.at(-1), ['time', 300], 'at double speed 100 ms is 0.2 s of subtitle: on time, nothing sent');
  renderer.sync({timeSeconds: 300.2, paused: true, rate: 2});
  renderer.sync({timeSeconds: 300.242, paused: true, rate: 2});
  assert.deepEqual(engine.calls.slice(-2), [['pause', 300.2], ['time', 300.242]]);
  renderer.dispose();
  renderer.dispose();
  assert.equal(engine.calls.filter(call => call[0] === 'dispose').length, 1);
  assert.deepEqual(c.calls.at(-1)!.slice(0, 1), ['clear']);
});

test('styled subtitles: an engine that will not start, a file that is not a subtitle, and a late failure', async () => {
  const context = (failed: (reason: unknown) => void = () => {}) => ({canvas: canvas().element, read: noRead, signal: new AbortController().signal, failed});
  const bytes = new TextEncoder().encode(script);
  await assert.rejects(new StyledSubtitleRenderer({load: () => Promise.reject(new Error('blocked'))}).load({kind: 'ass', script: bytes}, context()), /blocked/);
  await assert.rejects(new StyledSubtitleRenderer({load: () => Promise.resolve(libass().Engine)}).load({kind: 'ass', script: new TextEncoder().encode('<html>')}, context()), /no lines/);
  class Refuses { constructor(o: LibassOptions) { queueMicrotask(() => o.onError(new Error('no WebAssembly'))); } resize() {} setCurrentTime() {} setIsPaused() {} setRate() {} dispose() {} }
  await assert.rejects(new StyledSubtitleRenderer({load: () => Promise.resolve(Refuses)}).load({kind: 'ass', script: bytes}, context()), /no WebAssembly/);
  class Silent { constructor(_: LibassOptions) {} resize() {} setCurrentTime() {} setIsPaused() {} setRate() {} dispose() {} }
  await assert.rejects(new StyledSubtitleRenderer({load: () => Promise.resolve(Silent), readyTimeoutMs: 5}).load({kind: 'ass', script: bytes}, context()), /too long/);
  let later: LibassOptions | undefined;
  class Dies { constructor(o: LibassOptions) { later = o; queueMicrotask(() => o.onReady()); } resize() {} setCurrentTime() {} setIsPaused() {} setRate() {} dispose() {} }
  const reasons: unknown[] = [];
  await new StyledSubtitleRenderer({load: () => Promise.resolve(Dies)}).load({kind: 'ass', script: bytes}, context(reason => reasons.push(reason)));
  later!.onError('worker error');
  assert.deepEqual(reasons, ['worker error']);
  // Legacy encodings: the server names them and the text is decoded before libass sees it.
  assert.equal(decodeSubtitleText(Uint8Array.from([0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2]), 'windows-1251'), 'Привет');
  assert.equal(decodeSubtitleText(Uint8Array.from([0xff, 0xfe, 0x41, 0x00]), ''), 'A');
  assert.equal(decodeSubtitleText(new TextEncoder().encode('é'), 'not-a-charset'), 'é');
});

// ── What the browser says it draws ─────────────────────────────────────
test('fallback fonts are chosen by the scripts in the text', () => {
  const cases: [string, string[]][] = [
    ['Hello, world. Привет. Γειά', []],
    ['مرحبا', ['arabic']], ['שלום', ['hebrew']], ['สวัสดี', ['thai']], ['नमस्ते', ['devanagari']], ['নমস্কার', ['bengali']], ['வணக்கம்', ['tamil']],
    ['こんにちは', ['cjk']], ['你好', ['cjk']], ['Ｈｅｌｌｏ', ['cjk']],
    ['안녕하세요', ['cjk']],
    ['Sign: 東京 — مرحبا', ['arabic', 'cjk']],
  ];
  for (const [text, want] of cases) assert.deepEqual(scriptsIn(text), want, text);
  // Chinese, Japanese and Korean are the server's font, and only when there is one to name.
  assert.deepEqual(fallbackFontsFor('東京'), []);
  assert.deepEqual(fallbackFontsFor('東京 서울', script => (script === 'cjk' ? 'https://server/v1/subtitle-fonts/cjk' : undefined)), ['https://server/v1/subtitle-fonts/cjk']);
});

test('the capability declaration: one list, written into either document shape', () => {
  const all = {canvas: true, webAssembly: true};
  const cases: {name: string; facts: typeof all; want: string[]}[] = [
    // Full screen, iPhone included, is the page's own player, so the canvas is always seen.
    {name: 'a browser', facts: all, want: ['text', 'ass', 'pgs', 'vobsub', 'dvb']},
    {name: 'WebAssembly blocked', facts: {...all, webAssembly: false}, want: ['text', 'pgs', 'vobsub', 'dvb']},
    {name: 'no canvas or workers', facts: {...all, canvas: false}, want: ['text']},
  ];
  for (const c of cases) assert.deepEqual(subtitleRenderingKinds(c.facts), c.want, c.name);
  assert.deepEqual(subtitleRenderingKinds(), ['text'], 'outside a browser nothing is claimed');
  const kinds = subtitleRenderingKinds(all);
  const before = {version: 2, engines: {native: {name: 'hls.js', subtitleRendering: ['text'], video: []}, mpv: {subtitleRendering: ['dvb']}}};
  const capability = declareSubtitleRendering(before, kinds);
  assert.deepEqual(capability.engines.native, {name: 'hls.js', subtitleRendering: ['text', 'ass', 'pgs', 'vobsub', 'dvb'], video: []});
  assert.deepEqual(capability.engines.mpv, {subtitleRendering: ['text', 'ass', 'pgs', 'vobsub', 'dvb']});
  assert.deepEqual(before.engines.native.subtitleRendering, ['text'], 'the caller capability document is not mutated');
  assert.deepEqual(declareSubtitleRendering(before, ['text']).engines.mpv, {subtitleRendering: ['text']}, 'a claim this browser cannot draw is removed');
  const older = declareSubtitleRendering({version: 1, subtitles: {text: ['srt', 'vtt'], styled: [] as string[], bitmap: [] as string[]}}, kinds);
  assert.deepEqual(older.subtitles, {text: ['srt', 'vtt'], styled: ['ass', 'ssa'], bitmap: ['pgs', 'vobsub', 'dvb']});
  assert.deepEqual(declareSubtitleRendering({version: 1, subtitles: {text: ['vtt'], styled: ['x'], bitmap: ['y']}}, ['text']).subtitles, {text: ['vtt'], styled: [], bitmap: []});
});

test('rapid subtitle seeks keep at most two worker frames in flight', async () => {
 const c=canvas(), sent:ImageWorkerRequest[]=[];
 const worker:ImageWorkerLike={postMessage:m=>{sent.push(m);if(m.type==='load')queueMicrotask(()=>worker.onmessage?.({data:{type:'ready',width:8,height:8,starts:Float64Array.from({length:20},(_,i)=>i*1000),ends:Float64Array.from({length:20},(_,i)=>(i+1)*1000),skipped:0}}));},terminate:()=>{},onmessage:null,onerror:null};
 const renderer=new ImageSubtitleRenderer({spawn:()=>worker});
 try {
  await renderer.load({kind:'pgs',data:new Uint8Array(1)},{canvas:c.element,read:noRead,signal:new AbortController().signal,failed:()=>{}});
  for(let i=0;i<20;i++)renderer.sync({timeSeconds:i,paused:true,rate:1});
  assert.equal(sent.filter(m=>m.type==='paint').length,2);
  worker.onmessage?.({data:{type:'frame',index:0,rects:[]}});
  renderer.sync({timeSeconds:19,paused:true,rate:1});
  assert.equal(sent.filter(m=>m.type==='paint').length,3,'latest wanted cue is asked once space is free');
 } finally {renderer.dispose();}
});
