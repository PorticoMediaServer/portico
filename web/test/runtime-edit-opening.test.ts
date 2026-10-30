import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {FakePlaybackServer, fakeHttp} from '@core/playback-v1/testing/fake-server.ts';
import type {V1Request, V1Response} from '@core/playback-v1/http.ts';
import {GroupSessionService} from '@core/group-session.ts';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {HomeSelectionPlaybackItem} from '@core/home-selection/types.ts';
import {WebShellPlaybackRuntime} from '../src/platform/playback/runtime.ts';
import type {WebPlaybackSettings, WebPlaybackSource} from '../src/platform/playback/types.ts';
import {FakeVideo, fakeHls, manualClock} from './runtime-media-fixture.ts';

const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
const homeViewerFence = 'home_viewer_fence';
const selectionContext: SelectionContext = {scope: {...viewer, viewId: 'home', privacyFence: homeViewerFence}, revision: {catalogRevision: '1', viewerRevision: '1'}};
const leaf = (id: string): HomeSelectionPlaybackItem => ({identity: {id, kind: 'movie', libraryId: 'library'},
  entry: {id, kind: 'movie', title: id, libraryId: 'library', playback: {itemId: id}}, target: {itemId: id}});
const settle = async () => {for (let i = 0; i < 12; i++) await new Promise(setImmediate);};

/** A real runtime with one local queue session, and the wire log, so an edit opening can be
 * exercised against the actual captureEditWindow path (no product fake, no second port). */
function queueRuntime(t: import('node:test').TestContext, preferences: Partial<WebPlaybackSettings> = {}) {
  const clock = manualClock(), server = new FakePlaybackServer({now: clock.now, itemKind: () => 'movie'}), inner = fakeHttp(server);
  const log: V1Request[] = [], hls = fakeHls(), video = new FakeVideo();
  let current = true, serial = 0, viewerFence = homeViewerFence;
  const fetcher = async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(String(input));
    const request: V1Request = {path: url.pathname + url.search, method: (init.method ?? 'GET') as V1Request['method'],
      headers: Object.fromEntries(new Headers(init.headers)), ...(typeof init.body === 'string' ? {body: JSON.parse(init.body)} : {}), signal: init.signal ?? undefined};
    log.push(request);
    const itemMatch = /^\/v1\/items\/([^/]+)$/.exec(url.pathname), offerMatch = /^\/v1\/items\/([^/]+)\/playback-offers$/.exec(url.pathname);
    let response: V1Response;
    if (itemMatch) {
      const itemId = decodeURIComponent(itemMatch[1]!);
      response = {status: 200, headers: {}, body: {id: itemId, kind: 'movie', title: itemId, libraryId: 'library', available: true, missing: false,
        durationMs: 7200000, progressMs: 0, sources: [{id: `source_${itemId}`, durationMs: 7200000, container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080}]}};
    } else if (offerMatch) {
      const itemId = decodeURIComponent(offerMatch[1]!), sessionId = url.searchParams.get('sessionId')!, session = server.session(sessionId)!;
      response = {status: 200, headers: {}, body: {
        scope: {serverId: 'server', libraryId: 'library', itemId, viewerFence}, revision: `offer_${session.generation}`,
        offersRevision: 'a'.repeat(64), transcodingEnabled: true,
        sources: [{id: `source_${itemId}`, available: true, container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080,
          durationMs: 7200000, factsRevision: 1, factsStatus: 'known', relation: 'unknown', boundary: 'whole_source', enabled: false,
          reason: 'source_selection_unavailable', streams: [{index: 1, type: 'audio', codec: 'aac', channels: 2, default: true, forced: false, enabled: true, reason: ''}],
          qualities: [{id: 'original', kind: 'original', label: 'Original', enabled: true},
            {id: 'auto', kind: 'automatic', label: 'Automatic', enabled: true}]}],
        current: {sessionId, generation: session.generation, state: session.state, sourceId: `source_${itemId}`, delivery: 'hls',
          audioSelection: 'server_first_audio', subtitleSelection: 'disabled', qualityId: 'original'},
        controls: [{id: 'source', enabled: false, reason: 'source_selection_unavailable'},
          {id: 'audio', enabled: false, reason: 'track_selection_unavailable'},
          {id: 'subtitles', enabled: false, reason: 'subtitle_delivery_unavailable'},
          {id: 'quality', enabled: true, reason: ''}]}};
    } else response = await inner.send(request as never) as V1Response;
    if (request.method === 'GET' && /^\/v1\/queues\/[^/]+$/.test(request.path)) {
      response = {...response, body: {...response.body as object, postPlay: {autoplay: true, countdownMs: 5000}}};
    } else if (request.path.includes('/window?')) {
      const body = response.body as {items: object[]};
      response = {...response, body: {...body, items: body.items.map(item => ({...item, durationMs: 7200000}))}};
    }
    return new Response(response.body === undefined ? null : JSON.stringify(response.body), {status: response.status, headers: {'Content-Type': 'application/json', ...response.headers}});
  };
  const api = new HttpLocalApi('https://portico.test', '', fetcher as typeof fetch);
  const source: WebPlaybackSource = {api, authority: {...viewer, epoch: 1, viewerFence: homeViewerFence},
    contentScope: {serverId: 'server', viewerId: 'local_account_profile'}, isCurrent: () => current,
    events: {on: () => () => {}},
    settings: {videoSpeed: () => 1, audiobookSpeed: () => 1.25, skipModes: () => ({intro: 'off', recap: 'off', credits: 'off'}),
      audioEffects: () => ({gapless: false, crossfadeSeconds: 0, normalization: 'off'}), webVolume: () => true, ...preferences}};
  const runtime = new WebShellPlaybackRuntime(source, {timers: clock, playerTimers: clock, loadHls: async () => hls.Hls, nativeHls: 'never', key: () => `key-${++serial}`});
  runtime.mount(video);
  t.after(() => runtime.dispose());
  const arrive = async () => {await settle(); const engine = hls.instances.at(-1)!; video.ready(Number(engine.config.startPosition)); await settle(); return engine;};
  return {runtime, api, server, log, video, hls, clock, arrive, invalidate: () => {current = false;},
    queueCalls: () => log.filter(request => /^\/v1\/queues\//.test(request.path) || request.path.includes('/window?')).length};
}

async function localQueue(f: ReturnType<typeof queueRuntime>) {
  await f.runtime.selectionPlayback(() => true).play({context: selectionContext, items: [leaf('first'), leaf('next')], signal: new AbortController().signal});
  await f.arrive();
  const fence = f.runtime.getSnapshot().fence!;
  assert.equal(fence.kind, 'queue');
  return fence;
}

/** Wraps ONLY the supplied context.readOutput of the real canonical reader. It stays
 * pass-through (disarmed) while the opening is constructed — the canonical constructor calls
 * readOutput synchronously — and is armed explicitly for the operation under test. */
function instrumentReadOutput(f: ReturnType<typeof queueRuntime>, service: GroupSessionService, mode: 'takeover' | 'control') {
  const queue = (f.runtime as unknown as {stack: {queue: {captureEditWindow: (...args: never[]) => unknown}}}).stack.queue;
  const real = queue.captureEditWindow.bind(queue) as (target: unknown, revision: string, context: {readOutput: () => unknown} & Record<string, unknown>) => unknown;
  const original = service.getSnapshot.bind(service);
  let armed = false, inRead = false, last: unknown, reads = 0, groupReadsArmed = 0, invalidated = false;
  (service as unknown as {getSnapshot: () => ReturnType<typeof original>}).getSnapshot = () => {
    const snapshot = original();
    if (!armed || !inRead) return snapshot;
    groupReadsArmed++;
    if (mode === 'takeover' && groupReadsArmed === 4) {invalidated = true; f.invalidate();}
    return snapshot;
  };
  (queue as unknown as {captureEditWindow: unknown}).captureEditWindow = (target: unknown, revision: string, context: {readOutput: () => unknown}) =>
    real(target, revision, {...context, readOutput: () => {
      if (!armed) return context.readOutput();           // pass-through before arming
      reads++; inRead = true;
      try {last = context.readOutput(); return last;} finally {inRead = false;}
    }});
  return {
    arm: () => {armed = true; reads = 0; groupReadsArmed = 0; invalidated = false; last = undefined;},
    get: () => ({last, reads, groupReadsArmed, invalidated}),
    restore: () => {(queue as unknown as {captureEditWindow: unknown}).captureEditWindow = real;
      (service as unknown as {getSnapshot: () => ReturnType<typeof original>}).getSnapshot = original;},
  };
}

test('the actual runtime refuses an edit opening when the final group getter retires the output', async t => {
  const f = queueRuntime(t), fence = await localQueue(f);
  const service = new GroupSessionService({api: f.api, now: () => Date.parse('2026-10-23T20:00:00Z'), heartbeatMs: 100000,
    stream: (_path, _last, signal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('closed', 'AbortError')), {once: true});
    })});
  const instrument = instrumentReadOutput(f, service, 'takeover');
  const unbind = f.runtime.bindGroup(service, () => true, error => {throw error;});
  t.after(() => {instrument.restore(); unbind(); service.dispose();});
  const lifetime = {signal: new AbortController().signal, isCurrent: () => true};
  const port = f.runtime.captureListening(fence, lifetime)!;
  assert.ok(port.queue, 'the local queue facade is admitted');
  const opening = port.queue!.edit(port.queue!.list.getSnapshot(), lifetime);   // constructor readOutput is pass-through
  t.after(() => opening.dispose());
  instrument.arm();
  const before = f.queueCalls();
  await assert.rejects(() => opening.open());
  const seen = instrument.get();
  assert.equal(seen.reads, 1, 'exactly the targeted readOutput call was observed');
  assert.equal(seen.groupReadsArmed, 4, 'the real group getter was read inside that one call (active then matches, twice)');
  assert.equal(seen.invalidated, true, 'the final group read retired the output');
  assert.equal((seen.last as {kind?: string} | undefined)?.kind, 'unknown', 'readOutput itself returned unknown, not a stale local sample');
  assert.equal(instrument.get().last, seen.last, 'no later call overwrote the targeted result');
  assert.equal(f.queueCalls(), before, 'zero canonical queue IO for the refused opening');
  assert.notEqual(opening.getSnapshot().phase, 'ready');
  opening.dispose();
});

test('the same runtime and group binding admit the opening when the output is unchanged', async t => {
  const f = queueRuntime(t), fence = await localQueue(f);
  const service = new GroupSessionService({api: f.api, now: () => Date.parse('2026-10-23T20:00:00Z'), heartbeatMs: 100000,
    stream: (_path, _last, signal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('closed', 'AbortError')), {once: true});
    })});
  const instrument = instrumentReadOutput(f, service, 'control');
  const unbind = f.runtime.bindGroup(service, () => true, error => {throw error;});
  t.after(() => {instrument.restore(); unbind(); service.dispose();});
  const lifetime = {signal: new AbortController().signal, isCurrent: () => true};
  const port = f.runtime.captureListening(fence, lifetime)!;
  assert.ok(port.queue);
  const opening = port.queue!.edit(port.queue!.list.getSnapshot(), lifetime);
  t.after(() => opening.dispose());
  instrument.arm();
  await opening.open();
  const seen = instrument.get();
  // A successful reader.read runs check() several times (exclusive, before/after entries, header),
  // so the control asserts a consistent ratio rather than the single-call refused shape.
  assert.ok(seen.reads > 0, 'the control readOutput ran');
  assert.equal(seen.groupReadsArmed, 4 * seen.reads, 'active then matches, twice, on every readOutput');
  assert.equal(seen.invalidated, false, 'the control never retires the output');
  assert.equal((seen.last as {kind?: string} | undefined)?.kind, 'local', 'the control readOutput returns the real local target');
  assert.equal(opening.getSnapshot().phase, 'ready');
  assert.ok(opening.getSnapshot().page, 'the unchanged local control adopts a real canonical page');
  opening.dispose();
});
