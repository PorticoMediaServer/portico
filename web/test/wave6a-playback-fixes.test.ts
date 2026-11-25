import test from 'node:test';
import assert from 'node:assert/strict';
import type {Room} from '@core/room-protocol.ts';
import type {RoomSessionService} from '@core/room-session.ts';
import {playbackFence, queueScope, type PlaybackFence} from '../../packages/player-core/src/index.ts';
import {RuntimeMediaSession, type MediaSessionActionName, type MediaSessionLike} from '../src/platform/playback/media-session.ts';
import {MediaCarrier, type CarrierElement} from '../src/platform/playback/audio/carrier.ts';
import {bindRuntimeGroup} from '../src/platform/playback/group-binding.ts';
import {bindRuntimeTogether, handleExternalTransport, type TogetherRuntimeHost} from '../src/platform/playback/runtime-together.ts';
import {WebShellPlaybackRuntime} from '../src/platform/playback/runtime.ts';
import {StyledSubtitleRenderer, loadLibass, type LibassOptions} from '../src/platform/playback/subtitles/ass-renderer.ts';
import {ImageSubtitleRenderer, type ImageWorkerLike} from '../src/platform/playback/subtitles/image-renderer.ts';
import {WatchReceivers} from '../src/screens/watch/receivers.ts';
import type {WebPlaybackPresentation, WebPlaybackScreenLifetime} from '../src/platform/playback/types.ts';

const scope = queueScope({
  serverId: 'srv', authority: 'local', accountId: 'acc', profileId: 'prof',
  controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1',
});
const queueFence = playbackFence({
  kind: 'queue', playback: {scope, run: 1}, itemId: 'itm_1', sourceId: 'ver_1',
  entryId: 'ent_1', sessionId: 'ses_1', generation: 1,
});
const settle = async () => { for (let i = 0; i < 6; i++) await Promise.resolve(); };

// ── 1. navigator.mediaSession integration ────────────────────────────────

test('RuntimeMediaSession sets metadata, playbackState, positionState, handlers, and clears cleanly', async () => {
  const handlers = new Map<MediaSessionActionName, MediaSessionActionHandler | null>();
  const positions: (MediaPositionState | undefined)[] = [];
  const calls: unknown[][] = [];
  let now = 10_000;
  const session: MediaSessionLike = {
    metadata: null,
    playbackState: 'none',
    setActionHandler(action, handler) {
      if (action === 'stop' && failStop) throw new TypeError('unsupported');
      handlers.set(action, handler);
    },
    setPositionState(state) { positions.push(state); },
  };
  let failStop = false;
  let metadataAllocations = 0;
  const ms = new RuntimeMediaSession({
    session,
    resolveArtwork: path => `https://server.test${path}`,
    createMetadata: init => { metadataAllocations++; return init as unknown as MediaMetadata; },
    now: () => now,
    actions: {
      play: async f => { calls.push(['play', f]); },
      pause: async f => { calls.push(['pause', f]); },
      replay: async f => { calls.push(['replay', f]); },
      seek: async (f, s) => { calls.push(['seek', f, s]); },
      skip: async (f, d) => { calls.push(['skip', f, d]); },
      previous: async f => { calls.push(['previous', f]); },
      next: async f => { calls.push(['next', f]); },
      stop: async f => { calls.push(['stop', f]); },
    },
  });

  const base: WebPlaybackPresentation = {
    fence: queueFence,
    subject: {kind: 'music', item: {id: 'itm_1', kind: 'song', title: 'Track One', artist: 'The Band', album: 'Album A'}},
    kind: 'music', posterUrl: '/v1/art/1', durationSeconds: 200, positionSeconds: 30,
    playing: true, expanded: false, finished: false,
    controls: ['pause', 'seek', 'previous', 'next'], volume: 1, muted: false, rate: 1, policy: null,
  };
  ms.sync(base);
  assert.equal(metadataAllocations, 1);
  assert.deepEqual(session.metadata, {
    title: 'Track One', artist: 'The Band', album: 'Album A',
    artwork: [{src: 'https://server.test/v1/art/1'}],
  });
  assert.equal(session.playbackState, 'playing');
  assert.deepEqual(positions.at(-1), {duration: 200, playbackRate: 1, position: 30});
  assert.equal(handlers.get('play'), null);
  assert.equal(typeof handlers.get('pause'), 'function');
  assert.equal(typeof handlers.get('seekbackward'), 'function');
  assert.equal(typeof handlers.get('seekforward'), 'function');
  assert.equal(typeof handlers.get('seekto'), 'function');
  assert.equal(typeof handlers.get('previoustrack'), 'function');
  assert.equal(typeof handlers.get('nexttrack'), 'function');
  assert.equal(typeof handlers.get('stop'), 'function');

  // Repeating sync with normal tick progress does not reallocate metadata or spam setPositionState.
  now += 500;
  ms.sync({...base, positionSeconds: 30.5});
  assert.equal(metadataAllocations, 1);
  assert.equal(positions.length, 1);

  // Trigger handlers and verify dispatch.
  handlers.get('pause')!({action: 'pause'});
  handlers.get('seekbackward')!({action: 'seekbackward', seekOffset: 15});
  handlers.get('seekforward')!({action: 'seekforward'});
  handlers.get('seekto')!({action: 'seekto', seekTime: 95});
  handlers.get('previoustrack')!({action: 'previoustrack'});
  handlers.get('nexttrack')!({action: 'nexttrack'});
  handlers.get('stop')!({action: 'stop'});
  await settle();
  assert.deepEqual(calls, [
    ['pause', queueFence],
    ['skip', queueFence, -15],
    ['skip', queueFence, 10],
    ['seek', queueFence, 95],
    ['previous', queueFence],
    ['next', queueFence],
    ['stop', queueFence],
  ]);

  // Finished playback routes 'play' handler to replay, and unsupported actions do not throw.
  calls.length = 0;
  failStop = true;
  ms.sync({...base, playing: false, finished: true, controls: ['replay']});
  assert.equal(session.playbackState, 'paused');
  handlers.get('play')!({action: 'play'});
  await settle();
  assert.deepEqual(calls, [['replay', queueFence]]);

  // Live channels clear positionState and map channel/programme names cleanly.
  failStop = false;
  ms.sync({
    ...base,
    subject: {kind: 'channel', id: 'ch_1', sourceId: 'src_1', channelName: 'News 24', programmeTitle: 'Evening Report'},
    kind: 'video', posterUrl: undefined, durationSeconds: 0, positionSeconds: 0, playing: true, finished: false, controls: ['pause'],
  });
  assert.deepEqual(session.metadata, {title: 'Evening Report', artist: 'News 24', album: '', artwork: []});
  assert.equal(positions.at(-1), undefined);

  // Clear resets metadata, playbackState, positionState, and all 8 handlers.
  ms.clear();
  assert.equal(session.metadata, null);
  assert.equal(session.playbackState, 'none');
  for (const action of ['play', 'pause', 'seekbackward', 'seekforward', 'seekto', 'previoustrack', 'nexttrack', 'stop'] as const) {
    assert.equal(handlers.get(action), null, `${action} handler cleared`);
  }

  // No-op when mediaSession is absent.
  const absent = new RuntimeMediaSession({
    session: null,
    actions: {play() {}, pause() {}, replay() {}, seek() {}, skip() {}, previous() {}, next() {}, stop() {}},
  });
  absent.sync(base);
  absent.clear();
});

// ── 2. MediaCarrier 'play' event/intent sync ─────────────────────────────

test('MediaCarrier syncs outside play events, suppresses page play, and clears oursPause so subsequent outside pause fires', () => {
  const listeners = new Map<string, () => void>();
  const destination = {name: 'speakers'} as unknown as AudioDestinationNode;
  const stream = {stream: {id: 'ms'} as unknown as MediaStream, disconnect() {}} as unknown as MediaStreamAudioDestinationNode;
  const output = {connect() {}, disconnect() {}} as unknown as AudioNode;
  const silent = {gain: {value: 1}, connect() {}, disconnect() {}} as unknown as GainNode;
  const context = {destination, createMediaStreamDestination: () => stream, createGain: () => silent};
  const element: CarrierElement & {paused: boolean} = {
    srcObject: null, paused: true,
    play() { this.paused = false; listeners.get('play')?.(); return Promise.resolve(); },
    pause() { this.paused = true; listeners.get('pause')?.(); },
    setAttribute() {},
    addEventListener(type, fn) { listeners.set(type, fn); },
    removeEventListener(type) { listeners.delete(type); },
  };
  let pauses = 0, plays = 0;
  const carrier = new MediaCarrier(context, output, () => { pauses++; }, () => element, () => { plays++; });
  assert.equal(listeners.has('pause'), true);
  assert.equal(listeners.has('play'), true);

  // Page play does not trigger onPlay.
  carrier.play();
  assert.equal(plays, 0);

  // Page pause does not trigger onPause.
  carrier.pause();
  assert.equal(pauses, 0);

  // Outside resume (media key / lock screen) fires onPlay and clears oursPause.
  element.paused = false;
  listeners.get('play')!();
  assert.equal(plays, 1);

  // Subsequent outside pause is NOT swallowed by stale oursPause!
  element.paused = true;
  listeners.get('pause')!();
  assert.equal(pauses, 1);

  // Delayed stale play event while element is already paused is ignored.
  listeners.get('play')!();
  assert.equal(plays, 1);

  // Dispose removes both listeners.
  carrier.dispose();
  assert.equal(listeners.size, 0);
});

// ── 3. Watch Together runtime fixes ──────────────────────────────────────

test('group-binding clears adopted on leave so rejoining the same room+entry reloads cleanly', async () => {
  const at = new Date().toISOString();
  let phase: 'live' | 'idle' = 'live';
  const room: Room = {
    id: 'g1', name: 'Night', control: 'host',
    you: {memberId: 'me', host: true, mayControl: true, mayBecomeHost: false},
    clock: {entryId: 'e1', positionMs: 5000, at, playing: true, ready: 1, waitingFor: 0},
    host: {present: true}, members: [],
    queue: [{entryId: 'e1', itemId: 'movie', available: true}],
    inStepMs: 750, jumpMs: 3000, reportEveryMs: 5000,
  };
  const listeners = new Set<() => void>();
  const emit = () => { for (const l of [...listeners]) l(); };
  const service = {
    getSnapshot: () => phase === 'live' ? {phase, group: room} : {phase, group: undefined},
    serverNow: () => Date.parse(at),
    subscribe: (l: () => void) => { listeners.add(l); return () => listeners.delete(l); },
    report: async () => true,
  } as unknown as RoomSessionService;
  const loads: {life: WebPlaybackScreenLifetime; finish: () => void}[] = [];
  const binding = bindRuntimeGroup({
    service, isCurrent: () => true, changed() {}, reportError() {},
    player: {
      state: () => ({itemId: 'movie', positionSeconds: 5, playing: true, buffering: false, ready: true, seeking: false}),
      play() {}, pause() {}, seek() {}, setRate() {},
    },
    load: (_item, _seconds, _playing, life) => new Promise<void>(resolve => loads.push({life, finish: resolve})),
  });
  const fence = {kind: 'queue', itemId: 'movie'} as PlaybackFence;
  try {
    assert.equal(loads.length, 1);
    loads[0]!.finish();
    await settle();
    assert.equal(binding.matches(fence), true);

    // Leave the room -> adopted is cleared.
    phase = 'idle';
    emit();
    assert.equal(binding.matches(fence), false);

    // Rejoin the exact same room and entry -> must trigger a fresh load (not think it's still adopted).
    phase = 'live';
    emit();
    assert.equal(binding.matches(fence), false);
    assert.equal(loads.length, 2, 'rejoining same room+entry must reload after leave cleared adopted');
  } finally {
    binding.dispose();
  }
  assert.equal(binding.matches(fence), false);
});

test('Watch Together resets groupRate to 1 on room leave and routes stack.web.external through group controller', async () => {
  const at = new Date().toISOString();
  let phase: 'live' | 'idle' = 'live';
  let mayControl = true;
  const room = (): Room => ({
    id: 'g1', name: 'Night', control: 'host',
    you: {memberId: 'me', host: mayControl, mayControl, mayBecomeHost: false},
    clock: {entryId: 'e1', positionMs: 5000, at, playing: true, ready: 1, waitingFor: 0},
    host: {present: true}, members: [],
    queue: [{entryId: 'e1', itemId: 'itm_1', available: true}],
    inStepMs: 750, jumpMs: 3000, reportEveryMs: 5000,
  });
  const listeners = new Set<() => void>();
  const transports: unknown[][] = [];
  const service = {
    getSnapshot: () => phase === 'live' ? {phase, group: room()} : {phase, group: undefined},
    serverNow: () => Date.parse(at),
    subscribe: (l: () => void) => { listeners.add(l); return () => listeners.delete(l); },
    report: async () => true,
    transport: async (command: string, payload: unknown) => { transports.push([command, payload]); return true; },
  } as unknown as RoomSessionService;

  let groupRate = 1.05;
  let boundGroup: ReturnType<typeof bindRuntimeGroup> | undefined;
  let detachFn: (() => void) | undefined;
  const groupServices = new WeakMap<ReturnType<typeof bindRuntimeGroup>, RoomSessionService>();
  const serviceCalls: string[] = [];
  let appliedSnapshots = 0;
  const fakeStack = {
    service: {
      getSnapshot: () => ({itemId: 'itm_1', positionSeconds: 5, phase: 'ready', intent: 'playing', duration: 100, audioEffects: {waiting: false}}),
      pause: () => { serviceCalls.push('local-pause'); },
      resume: () => { serviceCalls.push('local-resume'); },
      seek: () => {},
      setPlaybackRate: () => {},
    },
    web: {
      setRate: () => {},
      apply: () => { appliedSnapshots++; },
    },
    life: new AbortController(),
  } as any;

  const host: TogetherRuntimeHost = {
    stack: () => fakeStack,
    source: () => ({api: {}}) as never,
    life: () => new AbortController(),
    snapshot: () => ({fence: queueFence, playback: fakeStack.service.getSnapshot()}) as never,
    group: () => boundGroup,
    setGroup: b => { boundGroup = b; },
    setDetachGroup: d => { detachFn = d; },
    groupServices: () => groupServices,
    groupRate: () => groupRate,
    setGroupRate: r => { groupRate = r; },
    nextOperation: () => 1,
    operation: () => 1,
    assertCurrent: () => {},
    current: () => true,
    matches: () => true,
    require: () => fakeStack,
    retainMediaStart: () => () => {},
    replace: () => fakeStack,
    sync: () => {},
  };

  const detach = bindRuntimeTogether(host, service, () => true, () => {});
  assert.equal(detachFn, detach);
  // Pretend the room had adopted the fence and drifted groupRate to 1.05.
  const originalGroup = boundGroup!;
  boundGroup = {
    ...originalGroup,
    matches: () => phase === 'live',
  };
  groupServices.set(boundGroup, service);
  groupRate = 1.05;

  // External pause with mayControl=true propagates through room transport, NOT local pause.
  handleExternalTransport(host, fakeStack, 'paused');
  await settle();
  assert.deepEqual(transports, [['pause', {}]]);
  assert.deepEqual(serviceCalls, []);

  // External pause with mayControl=false does NOT call transport or local pause, and re-applies room state.
  mayControl = false;
  handleExternalTransport(host, fakeStack, 'paused');
  await settle();
  assert.equal(transports.length, 1);
  assert.deepEqual(serviceCalls, []);
  assert.equal(appliedSnapshots, 1);

  // Leaving the room resets groupRate back to 1 even while bindGroup remains mounted!
  phase = 'idle';
  for (const l of [...listeners]) l();
  assert.equal(groupRate, 1, 'leaving a Watch Together room resets groupRate to 1');

  // Outside a room, external pause goes directly to local service.pause().
  handleExternalTransport(host, fakeStack, 'paused');
  assert.deepEqual(serviceCalls, ['local-pause']);

  boundGroup = originalGroup;
  detach();
});

// ── 4. Subtitle renderers: CSP nonce & timer cleanup on abort/dispose ────

test('loadLibass applies CSP nonce from meta[name="portico-script-nonce"]', async t => {
  const prevDoc = Object.getOwnPropertyDescriptor(globalThis, 'document');
  const prevWin = Object.getOwnPropertyDescriptor(globalThis, 'window');
  const appended: any[] = [];
  const fakeWindow: Record<string, unknown> = {};
  const fakeDoc = {
    baseURI: 'https://portico.test/',
    querySelector: (sel: string) => sel === 'meta[name="portico-script-nonce"]' ? {content: 'csp-nonce-abc'} : null,
    createElement: (tag: string) => ({tag, src: '', async: false, nonce: '', onload: null as any, onerror: null as any, remove() {}}),
    head: {append: (el: any) => { appended.push(el); fakeWindow.SubtitlesOctopus = class {}; el.onload?.(); }},
  };
  Object.defineProperty(globalThis, 'window', {configurable: true, value: fakeWindow});
  Object.defineProperty(globalThis, 'document', {configurable: true, value: fakeDoc});
  t.after(() => {
    if (prevDoc) Object.defineProperty(globalThis, 'document', prevDoc); else Reflect.deleteProperty(globalThis, 'document');
    if (prevWin) Object.defineProperty(globalThis, 'window', prevWin); else Reflect.deleteProperty(globalThis, 'window');
  });
  await loadLibass();
  assert.equal(appended.length, 1);
  assert.equal(appended[0].nonce, 'csp-nonce-abc');
});

test('StyledSubtitleRenderer and ImageSubtitleRenderer clear readiness timers on abort and dispose', async t => {
  const origSetTimeout = globalThis.setTimeout, origClearTimeout = globalThis.clearTimeout;
  const activeTimers = new Set<number>();
  let nextId = 1;
  (globalThis as any).setTimeout = (_fn: () => void, _ms?: number) => {
    const id = nextId++;
    activeTimers.add(id);
    return id;
  };
  (globalThis as any).clearTimeout = (id: number) => {
    activeTimers.delete(id);
  };
  t.after(() => {
    globalThis.setTimeout = origSetTimeout;
    globalThis.clearTimeout = origClearTimeout;
  });

  const assBytes = new TextEncoder().encode('[Script Info]\n[Events]\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hi');
  class HangingLibass { constructor(_o: LibassOptions) {} resize() {} setCurrentTime() {} setIsPaused() {} setRate() {} dispose() {} }
  const fakeCanvas = {width: 100, height: 100, getContext: () => ({clearRect() {}, drawImage() {}})} as unknown as HTMLCanvasElement;
  const noRead = () => Promise.reject(new Error('unused'));

  // 1) ASS renderer aborted while awaiting onReady -> timer cleared!
  const abort1 = new AbortController();
  const ass1 = new StyledSubtitleRenderer({load: async () => HangingLibass, readyTimeoutMs: 45_000});
  const p1 = ass1.load({kind: 'ass', script: assBytes}, {canvas: fakeCanvas, read: noRead, signal: abort1.signal, failed() {}});
  await settle();
  assert.equal(activeTimers.size, 1);
  abort1.abort();
  await assert.rejects(p1, /cancelled/);
  assert.equal(activeTimers.size, 0, 'ASS timer cleared on abort');

  // 2) ASS renderer disposed while awaiting onReady -> timer cleared!
  const ass2 = new StyledSubtitleRenderer({load: async () => HangingLibass, readyTimeoutMs: 45_000});
  const p2 = ass2.load({kind: 'ass', script: assBytes}, {canvas: fakeCanvas, read: noRead, signal: new AbortController().signal, failed() {}});
  await settle();
  assert.equal(activeTimers.size, 1);
  ass2.dispose();
  await assert.rejects(p2, /cancelled/);
  assert.equal(activeTimers.size, 0, 'ASS timer cleared on dispose');

  // 3) Image renderer aborted while awaiting worker ready -> timer cleared and worker terminated!
  const makeHangingWorker = () => {
    const w: ImageWorkerLike & {terminated: boolean} = {onmessage: null, onerror: null, terminated: false, postMessage() {}, terminate() { this.terminated = true; }};
    return w;
  };
  const abort3 = new AbortController(), w3 = makeHangingWorker();
  const img1 = new ImageSubtitleRenderer({spawn: () => w3, readyTimeoutMs: 60_000});
  const p3 = img1.load({kind: 'pgs', data: new Uint8Array([1, 2])}, {canvas: fakeCanvas, read: noRead, signal: abort3.signal, failed() {}});
  await settle();
  assert.equal(activeTimers.size, 1);
  abort3.abort();
  await assert.rejects(p3, /cancelled/);
  assert.equal(activeTimers.size, 0, 'Image timer cleared on abort');
  assert.equal(w3.terminated, true);

  // 4) Image renderer disposed while awaiting worker ready -> timer cleared and worker terminated!
  const w4 = makeHangingWorker();
  const img2 = new ImageSubtitleRenderer({spawn: () => w4, readyTimeoutMs: 60_000});
  const p4 = img2.load({kind: 'pgs', data: new Uint8Array([1, 2])}, {canvas: fakeCanvas, read: noRead, signal: new AbortController().signal, failed() {}});
  await settle();
  assert.equal(activeTimers.size, 1);
  img2.dispose();
  await assert.rejects(p4, /cancelled/);
  assert.equal(activeTimers.size, 0, 'Image timer cleared on dispose');
  assert.equal(w4.terminated, true);
});

// ── 5. WatchReceivers catches rejected command promises ──────────────────

test('WatchReceivers catches rejected receiver command promises and reports errors without unhandled rejections', async () => {
  const reported: unknown[] = [];
  const unhandled: unknown[] = [];
  const onUnhandled = (reason: unknown) => { unhandled.push(reason); };
  process.on('unhandledRejection', onUnhandled);
  try {
    const snapshot = {
      directoryPhase: 'ready' as const, receivers: [], receiverPlaybackId: 'pbs_1',
      remote: {sessionId: 'pbs_1', deviceName: 'TV', state: 'playing', itemId: 'itm_1', positionMs: 1000, next: {title: 'Next'}},
    };
    const receivers = new WatchReceivers({
      api: {request: async () => ({})} as never,
      runtime: {subscribe: () => () => {}} as never,
      isCurrent: () => true,
      reportError: error => { reported.push(error); },
    });
    const boom = (name: string) => () => Promise.reject(new Error(`fail:${name}`));
    (receivers as any).handoff = {
      getSnapshot: () => snapshot,
      subscribe: () => () => {},
      inUse: () => ({}),
      remoteTracks: boom('remoteTracks'),
      play: boom('play'),
      pause: boom('pause'),
      seek: boom('seek'),
      next: boom('next'),
      audio: boom('audio'),
      subtitles: boom('subtitles'),
      stop: boom('stop'),
      release() {},
      dispose() {},
    };
    receivers.refreshPresentation();
    receivers.toggle();
    receivers.seek(42);
    receivers.next();
    receivers.audio('a1');
    receivers.subtitles('s1');
    receivers.stop();
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.deepEqual(unhandled, [], 'no unhandledRejection from receiver commands');
    const names = reported.map(e => (e as Error).message);
    for (const expected of ['fail:remoteTracks', 'fail:pause', 'fail:seek', 'fail:next', 'fail:audio', 'fail:subtitles', 'fail:stop']) {
      assert.ok(names.includes(expected), `reported ${expected}`);
    }
    assert.equal(receivers.getSnapshot().remote?.stopped, false, 'failed stop restores bar to playing');
    receivers.dispose();
  } finally {
    process.off('unhandledRejection', onUnhandled);
  }
});

// ── 6. AirPlay cleanup in runtime ────────────────────────────────────────

test('WebShellPlaybackRuntime.browserAirPlayReady returns false without dead AirPlay state', () => {
  const runtime = Object.create(WebShellPlaybackRuntime.prototype) as WebShellPlaybackRuntime;
  assert.equal(runtime.browserAirPlayReady(queueFence), false);
});
