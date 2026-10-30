import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {queueItemInput} from '../src/queue-controller.ts';
import type {V1Request, V1Response} from '../src/playback-v1/http.ts';
import type {QualityRequest} from '../src/playback-v1/quality.ts';
import {PlaybackSessionController} from '../src/playback-v1/sessions.ts';
import type {MediaPlayer, PlayerObservation, PlayerSource} from '../src/playback-v1/port.ts';

const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
type Intercept = (request: V1Request, response: V1Response) => Promise<V1Response> | V1Response;
function setup(t: TestContext, quality?: () => QualityRequest | undefined, retainQueueIds?: () => readonly string[], beforeFreshPlayback?: () => Promise<void>) {
  const server = new FakePlaybackServer(), inner = fakeHttp(server);
  const log: V1Request[] = [], sessions = new Set<string>();
  let intercept: Intercept = (_request, response) => response, serial = 0, resets = 0, pending = 0;
  const http = {send: async (request: V1Request): Promise<V1Response> => {
    pending++;
    try {
      log.push(request);
      const response = await inner.send(request as never) as V1Response;
      if (request.method === 'POST' && request.path === '/v1/playback/sessions' && response.status === 201) sessions.add((response.body as {id: string}).id);
      return await intercept(request, response);
    } finally { pending--; }
  }};
  // Observation ticks wait here until a test lets one run (`tick`); nothing fires by itself.
  const ticks: (() => void)[] = [];
  const api = new V1PlaybackApi(http, {timers: {setTimer: fn => { ticks.push(fn); return ticks.length; }, clearTimer: () => {}}, key: () => `session-${++serial}-key`});
  const tick = () => { for (const fn of ticks.splice(0)) fn(); };
  const playback = new PlaybackService(api, () => `intent-${++serial}`, {deferRecovery: true, beforeFreshPlayback: async () => { resets++; if (beforeFreshPlayback) await beforeFreshPlayback(); }});
  const player = new V1QueuePlayer({http, api, playback, viewer, quality, retainQueueIds, key: () => `queue-${++serial}-key`});
  player.connect();
  t.after(() => { playback.leave(); player.dispose(); for (const id of sessions) api.forget(id); });
  const seed = async () => {
    await player.playEntries(['first', 'second', 'third'].map(queueItemInput));
    const queue = player.getSnapshot().view!.queue;
    const snapshot = playback.getSnapshot();
    playback.ready(snapshot.intentId);
    if (snapshot.pendingSeek) playback.seekApplied(snapshot.intentId, snapshot.pendingSeek.revision, snapshot.pendingSeek.positionSeconds, 'playing');
    playback.fact(snapshot.intentId, 37.25, 'playing');
    return {queueId: queue.id, entryId: queue.currentEntryId!, entries: queue.entries};
  };
  const settle = async () => {
    // The service can enqueue the latest timeline fact behind a report already in flight.
    let count = -1;
    for (let turn = 0; turn < 20; turn++) {
      await new Promise(setImmediate);
      if (pending === 0 && count === log.length) return;
      count = log.length;
    }
    throw new Error('playback fixture did not settle');
  };
  return {server, http, log, player, playback, seed, settle, tick, resets: () => resets, intercept: (fn: Intercept) => { intercept = fn; }};
}
function gate() {
  let release!: () => void, entered!: () => void;
  const waiting = new Promise<void>(resolve => { release = resolve; });
  const started = new Promise<void>(resolve => { entered = resolve; });
  return {started, release, hold: async () => { entered(); await waiting; }};
}

test('retention callback is sampled once before the intent waits and genuine replacement then restores the original queue paused', async t => {
  let candidates: string[] = [], samples = 0, hold = false;
  const delayed = gate();
  const f = setup(t, undefined, () => { samples++; return candidates; }, async () => { if (hold) await delayed.hold(); });
  const saved = await f.seed(), firstSession = f.player.currentSession()!.id;
  candidates = [saved.queueId]; hold = true;
  const replacing = f.player.playSegments([{source: {items: {ids: ['other-video']}}}]);
  await delayed.started;
  candidates[0] = 'changed-after-intent';
  delayed.release(); await replacing; hold = false;
  assert.equal(samples, 2);
  const create = f.log.filter(r => r.method === 'POST' && r.path === '/v1/queues').at(-1)!;
  assert.deepEqual((create.body as {retainQueueIds: readonly string[]}).retainQueueIds, [saved.queueId]);
  assert.ok(f.server.queue(saved.queueId));
  const replacementSession = f.player.currentSession()!.id;
  await f.player.restorePaused(saved.queueId, saved.entryId, 37.25);
  assert.equal(samples, 2, 'restoration reads the real queue without another create or callback');
  const restored = f.player.currentSession()!, queue = f.player.getSnapshot().view!.queue;
  assert.notEqual(restored.id, firstSession); assert.notEqual(restored.id, replacementSession);
  assert.equal(restored.state, 'paused'); assert.equal(restored.presentation.startPositionMs, 37250);
  assert.equal(queue.id, saved.queueId); assert.equal(queue.currentEntryId, saved.entryId); assert.deepEqual(queue.entries, saved.entries);
  const before = f.log.length;
  await f.player.enqueue(queueItemInput('last-song'));
  await f.player.playItem('first', 0);
  assert.equal(f.player.currentSession()!.queue!.queueId, saved.queueId);
  assert.equal(f.log.slice(before).some(r => r.method === 'POST' && r.path === '/v1/queues'), false);
  assert.equal(samples, 2);
});

for (const start of ['item', 'entries', 'selector', 'segments'] as const) {
  test(`retention data reaches the genuine ${start} start without changing its public result`, async t => {
    let savedId: string | undefined, samples = 0;
    const f = setup(t, undefined, () => { samples++; return savedId ? [savedId] : []; });
    const saved = await f.seed(); savedId = saved.queueId;
    if (start === 'item') assert.equal(await f.player.playItem('replacement', 9), undefined);
    else if (start === 'entries') assert.equal(await f.player.playEntries([queueItemInput('replacement')]), undefined);
    else if (start === 'selector') assert.equal(await f.player.playSelector({items: {ids: ['replacement']}}), undefined);
    else assert.equal((await f.player.playSegments([{source: {items: {ids: ['replacement']}}}])).id, f.player.currentSession()!.queue!.queueId);
    const create = f.log.filter(r => r.method === 'POST' && r.path === '/v1/queues').at(-1)!;
    assert.deepEqual((create.body as {retainQueueIds: readonly string[]}).retainQueueIds, [saved.queueId]);
    assert.equal(samples, 2); assert.ok(f.server.queue(saved.queueId));
  });
}

test('idle enqueue forwards retention data once without starting playback', async t => {
  let samples = 0;
  const f = setup(t, undefined, () => { samples++; return []; });
  await f.player.enqueueMany([queueItemInput('song-a'), queueItemInput('song-b')]);
  assert.equal(samples, 1);
  const create = f.log.find(r => r.method === 'POST' && r.path === '/v1/queues')!;
  assert.deepEqual((create.body as {retainQueueIds: readonly string[]}).retainQueueIds, []);
  assert.equal(f.player.currentSession(), undefined);
});

test('retained create still abandons a late session when a newer intent has replaced it', async t => {
  let candidates: string[] = [];
  const f = setup(t, undefined, () => candidates), saved = await f.seed(); candidates = [saved.queueId];
  const delayed = gate(); let lateSession = '';
  f.intercept(async (request, response) => {
    if (request.method === 'POST' && request.path === '/v1/queues' && (request.body as {segments: {source: {items?: {ids: string[]}}}[]}).segments[0]!.source.items?.ids[0] === 'late-video') {
      lateSession = (response.body as {session: {id: string}}).session.id;
      await delayed.hold();
    }
    return response;
  });
  const late = f.player.playSegments([{source: {items: {ids: ['late-video']}}}]);
  const rejected = assert.rejects(late, {name: 'AbortError'});
  await delayed.started;
  await f.player.playSegments([{source: {items: {ids: ['current-video']}}}]);
  const currentSession = f.player.currentSession()!.id;
  delayed.release(); await rejected;
  assert.equal(f.player.currentSession()!.id, currentSession);
  assert.equal(f.playback.getSnapshot().session!.id, currentSession);
  assert.equal(f.server.session(lateSession)!.ended, true);
  assert.ok(f.server.queue(saved.queueId));
});

test('restorePaused keeps the original queue and position, with paused intent at every media adoption', async t => {
  const f = setup(t), saved = await f.seed(), run = f.playback.getSnapshot().playbackRun, resets = f.resets();
  const states: {session?: string; intent: string}[] = [], phases: string[] = [];
  const off = f.playback.subscribe(() => { const s = f.playback.getSnapshot(); states.push({session: s.session?.id, intent: s.intent}); });
  const offQueue = f.player.subscribe(() => phases.push(f.player.getSnapshot().phase));
  t.after(() => { off(); offQueue(); });
  const before = f.log.length;
  await f.player.restorePaused(saved.queueId, saved.entryId, 37.25);
  const calls = f.log.slice(before), starts = calls.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions');
  assert.equal(starts.length, 1);
  assert.deepEqual(starts[0]!.body, {queue: {queueId: saved.queueId, entryId: saved.entryId}, state: 'paused', startPositionMs: 37250});
  assert.equal(calls.some(r => r.method === 'POST' && (r.path === '/v1/queues' || r.path.endsWith(':advance'))), false);
  const snapshot = f.playback.getSnapshot(), queue = f.player.getSnapshot().view!.queue;
  assert.equal(snapshot.intent, 'paused');
  assert.equal(snapshot.session!.resumeSeconds, 37.25);
  assert.equal(snapshot.pendingSeek!.positionSeconds, 37.25);
  assert.equal(snapshot.playbackRun, run + 1);
  assert.equal(f.resets(), resets + 1);
  assert.equal(queue.id, saved.queueId);
  assert.equal(queue.currentEntryId, saved.entryId);
  assert.deepEqual(queue.entries, saved.entries);
  assert.equal(f.player.currentSession()!.state, 'paused');
  assert.equal(f.server.session(snapshot.session!.id)!.state, 'paused');
  assert.ok(states.every(s => s.intent === 'paused'), 'no transient playing intent, including before the session arrives');
  assert.deepEqual(phases.filter(p => p === 'ready'), ['ready']);
});

test('restorePaused reads Settings again and drops choices from the old playback', async t => {
  const f = setup(t), saved = await f.seed();
  await f.playback.changePlayback({quality: {mode: 'fixed', id: '720p-high'}, audioTrackId: 'a2', subtitleTrackId: 's4'});
  const before = f.log.length;
  await f.player.restorePaused(saved.queueId, saved.entryId, 44);
  const body = f.log.slice(before).find(r => r.method === 'POST' && r.path === '/v1/playback/sessions')!.body as Record<string, unknown>;
  assert.equal(body.quality, undefined);
  assert.equal(body.audio, undefined);
  assert.equal(body.subtitles, undefined);
  assert.deepEqual(f.playback.playbackChoices(), {});
});

test('restorePaused reaches the actual session controller without a media play or autoplay load', async t => {
  const f = setup(t), saved = await f.seed();
  f.playback.leave();
  const loads: PlayerSource[] = [];
  let plays = 0;
  const observation: PlayerObservation = {generation: 0, state: 'paused', positionMs: 0, rate: 1};
  const media: MediaPlayer = {
    load: source => { loads.push(source); if (source.autoplay) media.play(); },
    play: () => { plays++; }, pause: () => {}, seek: () => {}, setRate: () => {},
    selectSidecarSubtitle: () => {}, observe: () => observation, onEvent: () => () => {}, dispose: () => {},
  };
  const controller = new PlaybackSessionController({http: f.http, player: media, wantsPlaying: () => f.playback.getSnapshot().intent === 'playing', timers: {setTimer: () => 0, clearTimer: () => {}}});
  const detach = f.playback.attachAdapter({apply: snapshot => {
    const session = f.player.currentSession();
    if (session && snapshot.session?.id === session.id) controller.attach(session);
  }});
  t.after(() => { detach(); controller.dispose(); });
  await f.player.restorePaused(saved.queueId, saved.entryId, 37.25);
  assert.equal(loads.length, 1);
  assert.equal(loads[0]!.autoplay, false);
  assert.equal(loads[0]!.startPositionMs, 37250);
  assert.equal(plays, 0);
  assert.equal(f.playback.getSnapshot().intent, 'paused');
});

test('restorePaused intent prerequisite preserves the default and supports an existing session', async t => {
  const f = setup(t); await f.seed();
  f.playback.beginQueueIntent('first', false, 'paused');
  assert.equal(f.playback.getSnapshot().intent, 'paused');
  f.playback.beginQueueIntent('first');
  assert.equal(f.playback.getSnapshot().intent, 'playing', 'existing consumers keep their playing default');
});

for (const separateStart of [false, true]) {
  test(`restorePaused quality regression: undefined leaves the ${separateStart ? 'session start' : 'queue create'} request to the saved server default`, async t => {
    const f = setup(t, () => undefined);
    await f.player.playItem('first', separateStart ? 12 : undefined);
    const request = f.log.find(r => r.method === 'POST' && r.path === (separateStart ? '/v1/playback/sessions' : '/v1/queues'))!;
    const body = (separateStart ? request.body : (request.body as {startPlayback: unknown}).startPlayback) as Record<string, unknown>;
    assert.equal(Object.hasOwn(body, 'quality'), false, 'absence must reach SessionsClient instead of an invented original override');
  });
}

for (const choice of [{mode: 'original'}, {mode: 'fixed', id: '720p-high'}] as const) {
  test(`restorePaused quality regression: an explicit ${choice.mode} overrides Settings until a fresh playback`, async t => {
    let saved: QualityRequest | undefined = {mode: 'automatic'};
    const f = setup(t, () => saved);
    await f.player.playItem('first', 5, undefined, undefined, undefined, {quality: choice});
    const queue = f.player.getSnapshot().view!.queue;
    let starts = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions');
    assert.deepEqual((starts.at(-1)!.body as {quality: unknown}).quality, choice);
    await f.player.playItem('first', 8);
    starts = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions');
    assert.deepEqual((starts.at(-1)!.body as {quality: unknown}).quality, choice, 'restart of the current entry keeps its explicit override');
    saved = undefined;
    await f.player.restorePaused(queue.id, queue.currentEntryId!, 9);
    starts = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions');
    assert.equal(Object.hasOwn(starts.at(-1)!.body as object, 'quality'), false, 'fresh restore reads Settings rather than carrying the previous override');
    saved = {mode: 'fixed', id: '1080p-low'};
    await f.player.playItem('different', 10);
    starts = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions');
    assert.deepEqual((starts.at(-1)!.body as {quality: unknown}).quality, saved, 'a fresh item uses the current explicit saved preference');
  });
}

test('restorePaused reads the current explicit saved quality after fresh preparation', async t => {
  let saved: QualityRequest | undefined;
  const f = setup(t, () => saved), bookmark = await f.seed();
  saved = {mode: 'original'};
  await f.player.restorePaused(bookmark.queueId, bookmark.entryId, 15);
  const start = f.log.findLast(r => r.method === 'POST' && r.path === '/v1/playback/sessions')!;
  assert.deepEqual((start.body as {quality: unknown}).quality, {mode: 'original'});
});

test('restorePaused rejects a different current entry without selecting or replacing the queue', async t => {
  const f = setup(t), saved = await f.seed(), before = f.log.length;
  await assert.rejects(f.player.restorePaused(saved.queueId, saved.entries[1]!.id, 10), {code: 'queue_entry_changed'});
  assert.equal(f.log.slice(before).some(r => r.method === 'POST' && (r.path === '/v1/playback/sessions' || r.path === '/v1/queues' || r.path.endsWith(':advance'))), false);
  assert.equal(f.playback.getSnapshot().session, undefined);
  assert.equal(f.player.getSnapshot().phase, 'error');
});

test('restorePaused refuses an unavailable bookmark before starting a session', async t => {
  const f = setup(t), saved = await f.seed(), before = f.log.length;
  f.intercept((request, response) => request.path.includes('/window?') ? {...response, body: {...response.body as object, items: ((response.body as {items: object[]}).items).map(e => ({...e, available: false}))}} : response);
  await assert.rejects(f.player.restorePaused(saved.queueId, saved.entryId, 10), {code: 'not_found'});
  assert.equal(f.log.slice(before).some(r => r.method === 'POST' && (r.path === '/v1/playback/sessions' || r.path === '/v1/queues' || r.path.endsWith(':advance'))), false);
  assert.equal(f.player.getSnapshot().phase, 'error');
});

test('restorePaused ignores an old header that answers after a newer Play', async t => {
  const f = setup(t), saved = await f.seed(), delayed = gate();
  let once = true;
  f.intercept(async (request, response) => { if (once && request.method === 'GET' && request.path === `/v1/queues/${saved.queueId}`) { once = false; await delayed.hold(); } return response; });
  const restoring = f.player.restorePaused(saved.queueId, saved.entryId, 37.25);
  await delayed.started;
  await f.player.playItem('replacement', 8);
  const current = f.playback.getSnapshot().session!.id;
  delayed.release(); await restoring;
  assert.equal(f.playback.getSnapshot().itemId, 'replacement');
  assert.equal(f.playback.getSnapshot().session!.id, current);
  assert.notEqual(f.player.getSnapshot().view!.queue.id, saved.queueId);
});

for (const cancel of ['leave', 'cancel', 'dispose', 'new-play'] as const) {
  test(`restorePaused abandons a session whose start answers after ${cancel}`, async t => {
    const f = setup(t), saved = await f.seed(), delayed = gate();
    let staleId = '';
    f.intercept(async (request, response) => {
      if (!staleId && request.method === 'POST' && request.path === '/v1/playback/sessions' && (request.body as {state?: string}).state === 'paused') { staleId = (response.body as {id: string}).id; await delayed.hold(); }
      return response;
    });
    const restoring = f.player.restorePaused(saved.queueId, saved.entryId, 37.25);
    await delayed.started;
    if (cancel === 'leave') f.playback.leave();
    else if (cancel === 'cancel') await f.player.cancel();
    else if (cancel === 'dispose') f.player.dispose();
    else await f.player.playItem('replacement', 8);
    const current = f.playback.getSnapshot().session?.id;
    delayed.release(); await restoring;
    assert.equal(f.playback.getSnapshot().session?.id, current);
    assert.notEqual(f.playback.getSnapshot().session?.id, staleId);
    assert.equal(f.server.session(staleId)!.ended, true, 'the late server session was ended, rather than orphaned');
    if (cancel === 'new-play') assert.equal(f.playback.getSnapshot().itemId, 'replacement');
  });
}

test('restorePaused never publishes ready for a server response that names playing', async t => {
  const f = setup(t), saved = await f.seed(), phases: string[] = [];
  f.intercept((request, response) => request.method === 'POST' && request.path === '/v1/playback/sessions' ? {...response, body: {...response.body as object, state: 'playing'}} : response);
  const off = f.player.subscribe(() => phases.push(f.player.getSnapshot().phase)); t.after(off);
  await assert.rejects(f.player.restorePaused(saved.queueId, saved.entryId, 10), /paused restore session/);
  assert.equal(phases.includes('ready'), false);
  assert.equal(f.playback.getSnapshot().session, undefined);
});

test('restorePaused rejects invalid input before disturbing current playback', async t => {
  const f = setup(t), saved = await f.seed(), session = f.playback.getSnapshot().session!.id;
  await f.settle(); f.tick(); await f.settle();
  assert.equal(f.server.session(session)!.reports.at(-1)!.positionMs, 37250, 'the seeded progress report completed before the invalid operation');
  const before = f.log.length;
  for (const position of [-1, NaN, Infinity, 1e9 + 1]) await assert.rejects(f.player.restorePaused(saved.queueId, saved.entryId, position));
  await assert.rejects(f.player.restorePaused('../other', saved.entryId, 0));
  await assert.rejects(f.player.restorePaused(saved.queueId, '', 0));
  assert.equal(f.playback.getSnapshot().session!.id, session);
  assert.equal(f.log.length, before);
  assert.deepEqual(f.log.slice(before), [], 'invalid restore inputs dispatch no reads, starts, reports or cleanup');
});
