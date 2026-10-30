import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi, toPlaybackSession} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer, movedEntry, playbackV1ForKind, serverSupportsPlaybackV1, serverSupportsQueueTransitions} from '../src/playback-v1/v1-queue-player.ts';
import {queueItemInput} from '../src/queue-controller.ts';
import {parseSession, type Session} from '../src/playback-v1/types.ts';
import type {QueuedSelectionRetirer} from '../src/playback-v1/selection-registration.ts';
import {QueueClient} from '../src/playback-v1/queue.ts';
import {playbackUserMessage, sessionEndMessage} from '../src/playback-messages.ts';
import {PlaybackApiError, type V1Http, type V1Request, type V1Response} from '../src/playback-v1/http.ts';
import {serviceI18n,setServiceI18n} from '../src/presentation/service-text.ts';

const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};

function setup(options: {retireQueuedSelection?: QueuedSelectionRetirer; postPlay?: {autoplay: boolean; countdownMs: number; passoutCheckDue: boolean; automaticAdvances: number; startDuringCredits?: boolean}; log?: {method: string; path: string; body?: unknown}[]; server?: ConstructorParameters<typeof FakePlaybackServer>[0]; beforeFreshPlayback?: () => Promise<void>; afterResponse?: (request: V1Request, response: V1Response) => Promise<V1Response | void>} = {}) {
  const server = new FakePlaybackServer(options.server);
  const inner = fakeHttp(server);
  const log = options.log ?? [];
  const http: V1Http = {send: async r => { log.push({method: r.method, path: r.path, body: r.body}); const response = await inner.send(r as never);
    if (options.postPlay && r.path.startsWith('/v1/queues') && response.body && typeof response.body === 'object') {
      const body = response.body as {queue?: Record<string, unknown>; id?: string};
      if (body.queue) body.queue.postPlay = options.postPlay;
      else if (body.id) (body as Record<string, unknown>).postPlay = options.postPlay;
    }
    const altered = await options.afterResponse?.(r, response);
    return (altered ?? response) as never; }};
  const timers: {fn: () => void; ms: number}[] = [];
  let serial = 0;
  const api = new V1PlaybackApi(http, {timers: {setTimer: (fn, ms) => { timers.push({fn, ms}); return timers.length; }, clearTimer: () => {}}, key: () => `k${++serial}`});
  const playback = new PlaybackService(api, () => `r${++serial}`, options.beforeFreshPlayback ? {beforeFreshPlayback: options.beforeFreshPlayback} : {});
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10, retireQueuedSelection: options.retireQueuedSelection});
  const stop = player.connect();
  return {server, api, playback, player, log, timers, stop};
}

/** The engine plays the adopted session to its end. */
function playToEnd(playback: PlaybackService) {
  const s = playback.getSnapshot();
  playback.ready(s.intentId);
  if (s.pendingSeek) playback.seekApplied(s.intentId, s.pendingSeek.revision, s.pendingSeek.positionSeconds, 'playing');
  const now = playback.getSnapshot();
  playback.fact(now.intentId, now.duration, 'ended');
}

const settle = () => new Promise(r => setTimeout(r, 20));

test('playItem caller lifetime refuses stale input before changing an intent or issuing a request', async () => {
  const {player, playback, log, stop} = setup();
  try {
    await settle(); // The initial current-queue read is independent of a Play intent.
    const beforeRequests = log.length;
    const before = playback.getSnapshot();
    const controller = new AbortController(); controller.abort();
    for (const context of [{signal: controller.signal}, {isCurrent: () => false}]) {
      await assert.rejects(player.playItem('movie', 17, undefined, undefined, undefined, {versionId: 'v2'}, context), {name: 'AbortError'});
      assert.equal(log.length, beforeRequests);
      assert.equal(playback.getSnapshot(), before);
    }
  } finally { stop(); }
});

test('playItem caller lifetime abandons late new-queue and current-entry starts after abort or host retirement', async () => {
  for (const restart of [false, true]) for (const retire of ['abort', 'host'] as const) {
    let hold = false, entered!: () => void, release!: () => void;
    const reached = new Promise<void>(resolve => { entered = resolve; });
    const gate = new Promise<void>(resolve => { release = resolve; });
    const f = setup({afterResponse: async (request, response) => {
      if (hold && request.method === 'POST' && request.path === '/v1/playback/sessions' && response.status === 201) { entered(); await gate; }
    }});
    try {
      if (restart) await f.player.playItem('movie');
      const beforeCreates = f.log.filter(r => r.method === 'POST' && r.path === '/v1/queues').length;
      const controller = new AbortController(); let current = true;
      hold = true;
      const pending = f.player.playItem('movie', 17, undefined, undefined, undefined, {versionId: 'v2'}, {signal: controller.signal, isCurrent: () => current});
      await reached;
      const request = f.log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!.body as Record<string, unknown>;
      assert.equal(request.versionId, 'v2'); assert.equal(request.startPositionMs, 17000);
      assert.equal(f.log.filter(r => r.method === 'POST' && r.path === '/v1/queues').length, beforeCreates + (restart ? 0 : 1));
      if (retire === 'abort') controller.abort(); else current = false;
      release(); await pending; await settle();
      const sessions = [...(f.server as unknown as {sessions: Map<string, {id: string; versionId: string; ended: boolean}>}).sessions.values()];
      const chosen = sessions.find(s => s.versionId === 'v2');
      assert.ok(chosen, 'the held request created a real chosen-version session');
      assert.ok(chosen.ended, 'the retired caller left no chosen session holding its lease');
      assert.notEqual(f.playback.getSnapshot().session?.id, chosen.id);
      assert.notEqual(f.player.currentSession()?.id, chosen.id);
    } finally { release(); f.stop(); }
  }
});

test('playItem caller lifetime permits its own accepted output change and keeps exact version/resume choices', async () => {
  const f = setup();
  try {
    for (const restart of [false, true]) {
      const controller = new AbortController();
      await f.player.playItem('movie', restart ? 4.5 : 27, undefined, undefined, undefined, {versionId: 'v2'}, {signal: controller.signal, isCurrent: () => true});
      assert.equal(f.player.currentSession()?.versionId, 'v2');
      assert.equal(f.playback.getSnapshot().session?.resumeSeconds, restart ? 4.5 : 27);
    }
  } finally { f.stop(); }
});

test('Play is one request: the queue is created with its first session, which the service plays', async () => {
  const {player, playback, log, stop} = setup();
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const creates = log.filter(r => r.method === 'POST' && r.path === '/v1/queues');
  assert.equal(creates.length, 1);
  assert.deepEqual((creates[0]!.body as {startPlayback?: unknown}).startPlayback, {state: 'playing'});
  assert.equal(log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').length, 0, 'no separate start');
  const view = player.getSnapshot().view!;
  assert.equal(view.queue.entries.length, 3);
  assert.equal(view.next.available, true);
  assert.equal(playback.getSnapshot().session?.id, view.queue.currentPlaybackId);
  assert.equal(playback.getSnapshot().itemId, 'a');
  stop();
});

test('an explicit start position starts the entry separately, at that position', async () => {
  const {player, playback, log, stop} = setup();
  await player.playItem('movie', 42);
  const create = log.find(r => r.method === 'POST' && r.path === '/v1/queues')!;
  assert.equal((create.body as {startPlayback?: unknown}).startPlayback, undefined);
  const start = log.find(r => r.method === 'POST' && r.path === '/v1/playback/sessions')!;
  assert.equal((start.body as {startPositionMs?: number}).startPositionMs, 42_000);
  assert.ok((start.body as {queue?: unknown}).queue);
  assert.equal(playback.getSnapshot().session?.resumeSeconds, 42);
  stop();
});

test('Next advances on the server, which starts the next session and ends the old one', async () => {
  const {player, playback, server, stop} = setup();
  await player.playEntries(['a', 'b'].map(queueItemInput));
  const first = playback.getSnapshot().session!.id;
  const q = player.getSnapshot().view!.queue;
  await player.next(q.id, q.revision);
  const second = playback.getSnapshot().session!;
  assert.notEqual(second.id, first);
  assert.equal(playback.getSnapshot().itemId, 'b');
  assert.equal((server as unknown as {sessions: Map<string, {ended?: unknown}>}).sessions.get(first)?.ended !== undefined, true, 'the replaced session ended');
  assert.equal(player.getSnapshot().view!.next.available, false);
  stop();
});

test('one playback runs through the queue: Next and completion stay in it, a new Play begins another', async () => {
  const {player, playback, stop} = setup();
  const before = playback.getSnapshot().playbackRun;
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const run = playback.getSnapshot().playbackRun;
  assert.equal(run, before + 1, 'Play begins a playback');
  const q = player.getSnapshot().view!.queue;
  await player.next(q.id, q.revision);
  assert.equal(playback.getSnapshot().itemId, 'b');
  assert.equal(playback.getSnapshot().playbackRun, run, 'Next keeps the playback (the player keeps its choices)');
  playToEnd(playback);
  await settle(); await settle();
  assert.equal(playback.getSnapshot().itemId, 'c');
  assert.equal(playback.getSnapshot().playbackRun, run, 'autoplay keeps the playback');
  await player.playEntries(['d'].map(queueItemInput));
  assert.equal(playback.getSnapshot().playbackRun, run + 1, 'a new Play goes back to the Settings');
  stop();
});

test('a new playback waits for the player\u2019s own choices to go back to the Settings; the queue moving on does not', async () => {
  const log: {method: string; path: string; body?: unknown}[] = [];
  let asked = 0, release!: () => void;
  const {player, playback, stop} = setup({log, beforeFreshPlayback: () => { asked++; log.push({method: 'RESET', path: ''}); return new Promise<void>(r => { release = r; }); }});
  const playing = player.playEntries(['a', 'b'].map(queueItemInput));
  await settle();
  assert.equal(asked, 1);
  assert.equal(log.filter(r => r.path === '/v1/queues').length, 0, 'nothing is started before the Settings are back');
  release();
  await playing;
  assert.equal(log.findIndex(r => r.method === 'RESET') < log.findIndex(r => r.path === '/v1/queues'), true);
  const q = player.getSnapshot().view!.queue;
  await player.next(q.id, q.revision);
  assert.equal(playback.getSnapshot().itemId, 'b');
  assert.equal(asked, 1, 'Next is the same playback');
  stop();
});

test('completion advances by itself, or holds for a post-play decision when the policy says so', async () => {
  const {player, playback, stop} = setup();
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  playToEnd(playback);
  await settle(); await settle();
  assert.equal(playback.getSnapshot().itemId, 'b', 'advanced to the next entry');
  player.setCompletionPolicy(() => 'hold');
  playToEnd(playback);
  await settle(); await settle();
  const held = player.getSnapshot().completion;
  assert.ok(held && held.nextAvailable);
  assert.equal(playback.getSnapshot().itemId, 'b');
  await player.continueCompletion('manual');
  assert.equal(playback.getSnapshot().itemId, 'c');
  stop();
});

// P1 (web, 24 Sep): the engine reports the end itself, then the completion check reports it
// again. The server ended the session on the first one, so the second answered 410 and the queue
// stalled on track 1. The end is reported once, and an end the server already has is confirmed.
test('an end already reported still advances to the next entry', async () => {
  const {player, playback, api, log, stop} = setup();
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const first = playback.getSnapshot().session!;
  playToEnd(playback);
  // The engine's own end report, before the queue's completion check gets to report it.
  await api.progressPlayback(first.id, {generation: first.generation, sequence: 1000, positionSeconds: playback.getSnapshot().duration, state: 'ended'});
  await settle(); await settle();
  assert.equal(playback.getSnapshot().itemId, 'b', 'advanced to the next entry');
  const ends = log.filter(r => r.method === 'PUT' && r.path.endsWith(`/${first.id}/observation`) && (r.body as {state?: string} | undefined)?.state === 'ended');
  assert.equal(ends.length, 1, 'the end is reported once');
  stop();
});

// NEW-44 (web, 24 Sep): the real server's queue window names a song "track" (v1 vocabulary), so
// a song queue fell into the video post-play hold, whose countdown the music player never shows:
// the album stopped after track 1. An audio session goes on at once, without the viewer.
test('a song queue advances by itself at the end of a track, whatever the window calls the entry', async () => {
  const log: {method: string; path: string; body?: unknown}[] = [];
  const {player, playback, api, stop} = setup({log, server: {itemKind: id => (id.startsWith('song') ? 'track' : 'movie')}});
  player.setCompletionPolicy(() => 'hold'); // the web player holds video for its post-play countdown
  await player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
  const first = playback.getSnapshot().session!;
  playToEnd(playback);
  await settle(); await settle();
  assert.equal(player.getSnapshot().completion, null, 'no post-play hold for audio');
  assert.equal(log.filter(r => r.method === 'POST' && r.path.endsWith(':advance')).length, 1, 'the queue advanced without the viewer');
  assert.equal(playback.getSnapshot().itemId, 'song-2');
  // The replaced session is retired: a late report from the engine for it is dropped, not sent.
  const before = log.length;
  await api.progressPlayback(first.id, {generation: first.generation, sequence: 2000, positionSeconds: 1, state: 'paused'});
  assert.equal(log.slice(before).filter(r => r.path.includes(first.id)).length, 0, 'no report for the replaced session');
  playToEnd(playback);
  await settle(); await settle();
  assert.equal(playback.getSnapshot().itemId, 'song-3', 'and on again');
  stop();
});

test('Up Next edits go to the server: remove, reorder (one moved entry), repeat, add', async () => {
  const {player, stop} = setup();
  await player.playEntries(['a', 'b', 'c', 'd'].map(queueItemInput));
  const service = player.workspace.service;
  const ids = () => service.getSnapshot().queue!.entries.map(e => (e as {itemId?: string}).itemId);
  await service.mutate({action: 'remove', entryId: service.getSnapshot().queue!.entries[3]!.id} as never);
  assert.deepEqual(ids(), ['a', 'b', 'c']);
  const e = service.getSnapshot().queue!.entries.map(x => x.id);
  await service.mutate({action: 'reorder', entryIds: [e[0], e[2], e[1]]} as never);
  assert.deepEqual(ids(), ['a', 'c', 'b']);
  await service.mutate({action: 'repeat', repeat: 'all'} as never);
  assert.equal(service.getSnapshot().queue!.repeat, 'all');
  await player.enqueue(queueItemInput('z'));
  assert.deepEqual(ids(), ['a', 'c', 'b', 'z']);
  stop();
});

test('movedEntry finds the one entry a drag moved', () => {
  assert.deepEqual(movedEntry(['a', 'b', 'c', 'd'], ['a', 'c', 'd', 'b']), {entryId: 'b', after: 'd'});
  assert.deepEqual(movedEntry(['a', 'b', 'c'], ['c', 'a', 'b']), {entryId: 'c', before: 'a'});
  assert.equal(movedEntry(['a', 'b'], ['a', 'b']), undefined);
});

test('the bridge observes with its own rising seq, keeps a paused session alive, and stops at the last position', async () => {
  const {api, player, playback, log, timers, stop} = setup();
  await player.playItem('movie');
  const id = playback.getSnapshot().session!.id;
  await api.progressPlayback(id, {generation: 1, sequence: 1, positionSeconds: 12, state: 'paused'});
  await settle();
  // The paused session is still sampled each tick: that is what keeps its lease.
  for (let i = 0; i < 2; i++) { timers.at(-1)!.fn(); await settle(); }
  const reports = log.filter(r => r.path.endsWith('/observation')).map(r => r.body as {seq: number; positionMs: number; state: string});
  assert.deepEqual(reports.map(r => r.seq), [1, 2]);
  assert.equal(reports[1]!.positionMs, 12_000);
  assert.equal(reports[1]!.state, 'paused');
  await api.stopPlayback(id);
  const del = log.find(r => r.method === 'DELETE' && r.path === `/v1/playback/sessions/${id}`)!;
  assert.deepEqual(del.body, {positionMs: 12_000});
  stop();
});

test('the switch: v1 for video, audiobooks and music on a v1 server; channels switch on their own capability', () => {
  assert.equal(serverSupportsPlaybackV1({features: {playback_v1: 'enabled'}}), true);
  assert.equal(serverSupportsPlaybackV1({features: {}}), false);
  assert.equal(playbackV1ForKind(true, 'movie'), true);
  assert.equal(playbackV1ForKind(true, 'episode'), true);
  assert.equal(playbackV1ForKind(true, 'audiobook_file'), true);
  assert.equal(playbackV1ForKind(true, 'song'), true);
  assert.equal(playbackV1ForKind(true, 'channel'), false);
  assert.equal(playbackV1ForKind(false, 'movie'), false);
});

test('a v1 session becomes the service session: stream is HLS, direct stays direct', () => {
  const s = toPlaybackSession({id: 's1', revision: '1', kind: 'vod', role: 'local', state: 'playing', lease: {reportEveryMs: 10000}, presentation: {generation: 2, mode: 'stream', url: '/v1/media/x/index.m3u8', startPositionMs: 5000, subtitles: [], decision: {}}} as never, 100);
  assert.deepEqual(s, {id: 's1', generation: 2, streamUrl: '/v1/media/x/index.m3u8', mode: 'hls', duration: 100, resumeSeconds: 5, protocol: 'v1'});
});

// ── Slice 2: events, queue_building, a deferred start ──────────────────────────

type Hook = (r: {method: string; path: string; body?: unknown}, response: {status: number; headers: Record<string, string>; body?: unknown}) => {status: number; headers: Record<string, string>; body?: unknown};

function setupWith(hook: Hook, before?: (r: {method: string; path: string}) => {status: number; headers: Record<string, string>; body?: unknown} | undefined) {
  const server = new FakePlaybackServer();
  const inner = fakeHttp(server);
  const http: V1Http = {send: async r => (before?.(r as never) ?? hook(r as never, await inner.send(r as never) as never)) as never};
  const listeners: ((e: {id: string; type: string; resource?: {kind: string; id: string}; revision?: string}) => void)[] = [];
  const resyncs: (() => void)[] = [];
  const events = {on: (_type: string, fn: (e: never) => void) => { listeners.push(fn as never); return () => {}; }, onResync: (fn: () => void) => { resyncs.push(fn); return () => {}; }};
  let serial = 0;
  const api = new V1PlaybackApi(http, {timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const playback = new PlaybackService(api, () => `r${++serial}`, {});
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 60_000, events, buildingRetryMs: 5000});
  const stop = player.connect();
  const emit = (queueId: string, revision?: string) => { for (const fn of listeners) fn({id: String(Date.now()), type: 'queue.updated', resource: {kind: 'queue', id: queueId}, revision}); };
  const resync = () => { for (const fn of resyncs) fn(); };
  return {server, http: inner, player, playback, emit, resync, stop};
}

test('queue.updated from another device refreshes Up Next', async () => {
  const {player, http, emit, stop} = setupWith((_r, res) => res);
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const q = player.getSnapshot().view!.queue;
  // Another device (same profile) removes the last entry.
  await http.send({method: 'DELETE', path: `/v1/queues/${q.id}/entries/${encodeURIComponent(q.entries[2]!.id)}`, headers: {'If-Match': q.revision}} as never);
  emit(q.id, String(Number(q.revision) + 1));
  await new Promise(r => setTimeout(r, 250));
  assert.equal(player.getSnapshot().view!.queue.entries.length, 2);
  stop();
});

test('a resync rereads the queue as it is now, without moving playback', async () => {
  const commands: string[] = [];
  const {player, playback, http, resync, stop} = setupWith((r, res) => { if (r.method !== 'GET') commands.push(r.method + ' ' + r.path); return res; });
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const q = player.getSnapshot().view!.queue;
  // Another device's edit whose queue.updated fell in the gap the resync covers.
  await http.send({method: 'DELETE', path: `/v1/queues/${q.id}/entries/${encodeURIComponent(q.entries[2]!.id)}`, headers: {'If-Match': q.revision}} as never);
  const sent = commands.length, item = playback.getSnapshot().itemId;
  resync();
  await new Promise(r => setTimeout(r, 250));
  assert.equal(player.getSnapshot().view!.queue.entries.length, 2);
  assert.equal(commands.length, sent, 'a resync sends no command');
  assert.equal(playback.getSnapshot().itemId, item);
  stop();
});

test('an entry still being snapshotted is retried until it can play (503 queue_building)', async () => {
  let refusals = 1;
  const {player, playback, stop} = setupWith((_r, res) => res, r => {
    if (r.method === 'POST' && r.path.endsWith(':advance') && refusals > 0) { refusals--; return {status: 503, headers: {'Retry-After': '0'}, body: {error: {code: 'queue_building', retry: 'same_request'}}}; }
    return undefined;
  });
  await player.playEntries(['a', 'b'].map(queueItemInput));
  const q = player.getSnapshot().view!.queue;
  await player.next(q.id, q.revision);
  assert.equal(refusals, 0);
  assert.equal(playback.getSnapshot().itemId, 'b');
  stop();
});

test('a start the server chooses later (a very large shuffle) waits for the build, woken by the event', async () => {
  let built = false;
  const building = (b: Record<string, unknown>) => ({...b, current: undefined, segments: (b.segments as Record<string, unknown>[]).map(s => ({...s, state: 'building'}))});
  const {player, playback, emit, stop} = setupWith((r, res) => {
    if (built || res.status >= 300) return res;
    if (r.method === 'POST' && r.path === '/v1/queues') {
      const b = res.body as {queue: Record<string, unknown>; window: unknown};
      return {...res, body: {queue: building(b.queue), window: b.window}};
    }
    if (r.method === 'GET' && /^\/v1\/queues\/[^/]+$/.test(r.path)) return {...res, body: building(res.body as Record<string, unknown>)};
    return res;
  });
  const started = player.playEntries(['a', 'b', 'c'].map(queueItemInput), {shuffle: true});
  await new Promise(r => setTimeout(r, 50));
  assert.equal(playback.getSnapshot().session, undefined, 'nothing plays before the server chooses');
  built = true;
  emit(player.getSnapshot().view!.queue.id);
  await started;
  assert.ok(playback.getSnapshot().session, 'the chosen entry plays');
  assert.ok(player.getSnapshot().view!.queue.currentEntryId);
  stop();
});

// ── Slice 3: the migration plan's queue scenarios ──────────────────────────────

test('a 1M-entry shuffled container plays in one request and the player holds only a window', async () => {
  const server = new FakePlaybackServer({containerSize: () => 1_000_000});
  const inner = fakeHttp(server);
  const log: string[] = [];
  const http: V1Http = {send: async r => { log.push(`${r.method} ${r.path}`); return inner.send(r as never) as never; }};
  let serial = 0;
  const api = new V1PlaybackApi(http, {timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const playback = new PlaybackService(api, () => `r${++serial}`, {});
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`});
  const stop = player.connect();
  await player.playSelector({container: {kind: 'collection', id: 'everything'}}, {shuffle: true});
  assert.equal(log.filter(l => l === 'POST /v1/queues').length, 1);
  assert.ok(playback.getSnapshot().session);
  for (let i = 0; i < 5; i++) {
    const q = player.getSnapshot().view!.queue;
    assert.ok(q.entries.length <= 71, `window ${q.entries.length}`);
    await player.next(q.id, q.revision);
  }
  const header = await new QueueClient(http).header(player.getSnapshot().view!.queue.id);
  assert.equal(header.total, 1_000_000);
  assert.ok(player.getSnapshot().view!.queue.entries.length <= 71);
  stop();
});

test('a start at a position, then choosing another entry, replaces the playing session', async () => {
  const log: {method: string; path: string; body?: unknown}[] = [];
  const {player, playback, stop} = setup({log});
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput), {startSeconds: 30});
  const first = playback.getSnapshot().session!.id;
  const q = player.getSnapshot().view!.queue;
  await player.playEntry(q.id, q.entries[2]!.id, q.revision);
  assert.equal(playback.getSnapshot().itemId, 'c');
  assert.notEqual(playback.getSnapshot().session!.id, first);
  const start = log.find(r => r.method === 'POST' && r.path === '/v1/playback/sessions')!;
  assert.equal((start.body as {startPositionMs?: number}).startPositionMs, 30_000);
  stop();
});

test('a relaunch shows the device queue without playing it', async () => {
  const first = setup();
  await first.player.playEntries(['a', 'b'].map(queueItemInput));
  const id = first.player.getSnapshot().view!.queue.id;
  const again = new V1QueuePlayer({http: fakeHttp(first.server) as never, api: first.api, playback: new PlaybackService(first.api, () => 'x', {}), viewer});
  await again.open(id);
  assert.equal(again.getSnapshot().view!.queue.entries.length, 2);
  assert.equal(again.getSnapshot().view!.queue.id, id);
  first.stop();
  again.dispose();
});

// ── The server ends a playback (spec §14: an administrator terminates it) ──
test('an administrator’s terminate stops the service with their message, from the event, and nothing advances', async () => {
  const {server, player, playback, api, log, stop} = setup();
  await player.playEntries(['a', 'b'].map(queueItemInput));
  const sid = playback.getSnapshot().session!.id;
  playback.ready(playback.getSnapshot().intentId);
  const terminated = await fakeHttp(server).send({method: 'POST', path: `/v1/admin/sessions/${sid}:terminate`, body: {message: 'Maintenance tonight.'}} as never);
  assert.equal((terminated as {status: number}).status, 204);
  api.sessionUpdated(sid, {state: 'ended', reason: 'terminated', message: 'Maintenance tonight.'});
  const s = playback.getSnapshot();
  assert.equal(s.phase, 'error');
  assert.equal(s.intent, 'paused');
  assert.equal(s.error, 'Maintenance tonight.');
  assert.deepEqual(s.ended, {reason: 'terminated', message: 'Maintenance tonight.'});
  await settle();
  assert.equal(log.filter(r => r.path.endsWith(':advance')).length, 0, 'a terminated playback does not advance');
  // The next play clears it.
  await player.playEntries(['c'].map(queueItemInput));
  assert.equal(playback.getSnapshot().ended, undefined);
  stop();
});

test('a report that finds the session gone re-reads it and says why (an event missed while offline)', async () => {
  const {server, player, playback, api, stop} = setup();
  await player.playEntries(['a'].map(queueItemInput));
  const sid = playback.getSnapshot().session!.id;
  playback.ready(playback.getSnapshot().intentId);
  await fakeHttp(server).send({method: 'POST', path: `/v1/admin/sessions/${sid}:terminate`, body: {}} as never);
  await api.progressPlayback(sid, {generation: 1, sequence: 1, positionSeconds: 5, state: 'playing'}).catch(() => {});
  await settle();
  const s = playback.getSnapshot();
  assert.deepEqual(s.ended, {reason: 'terminated', message: undefined});
  assert.equal(s.error, 'The server owner stopped this playback.');
  stop();
});

test('the terminated failure code and the end reasons read as the owner wrote them', () => {
  assert.equal(playbackUserMessage({code: 'terminated', message: 'Back at\u0007 six.'}), 'Back at six.');
  assert.equal(playbackUserMessage({code: 'terminated', message: ''}), 'The server owner stopped this playback.');
  assert.equal(sessionEndMessage({reason: 'transferred'}), 'Playback continued on another device.');
});

// ── Rendered-audio edges on v1 (spec §18; plan §9) ──────────────────
function transitionsSetup(now: () => number = Date.now) {
  const server = new FakePlaybackServer({now});
  const http = fakeHttp(server) as unknown as V1Http;
  let serial = 0;
  const api = new V1PlaybackApi(http, {timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const playback = new PlaybackService(api, () => `r${++serial}`, {});
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10, transitions: true});
  const stop = player.connect();
  return {server, api, playback, player, stop};
}
/** The engine is playing the current track, `left` seconds from its end. */
function nearEnd(playback: PlaybackService, left: number) {
  const s = playback.getSnapshot();
  playback.ready(s.intentId);
  if (s.pendingSeek) playback.seekApplied(s.intentId, s.pendingSeek.revision, s.pendingSeek.positionSeconds, 'playing');
  const now = playback.getSnapshot();
  playback.fact(now.intentId, Math.max(0, now.duration - left), 'playing');
}

test('§18: near the end of a rendered track the next one is prepared, committed and adopted at the boundary', async () => {
  const {server, api, playback, player, stop} = transitionsSetup();
  await player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
  const first = playback.getSnapshot().session!;
  assert.equal(first.audio?.mode, 'direct', 'the v1 presentation carries its version 2 render plan');
  assert.equal(playback.getSnapshot().audioEffects.rendering, true, 'gapless is on, so the engine renders');
  nearEnd(playback, 10);
  await settle();
  const prepared = playback.getSnapshot().audioEffects.prepared!;
  assert.ok(prepared, 'prepared');
  assert.equal(server.preparations, 1);
  assert.equal(prepared.audio!.durationFrames, 180 * 44100);
  // The engine buffered the next window, then reaches the edge.
  player.audioPrepared(prepared.token);
  await player.commitAudio(prepared.token);
  assert.equal(server.commits, 1);
  const committed = playback.getSnapshot().audioEffects.committed!;
  assert.ok(committed, 'committed');
  assert.equal(server.session(first.id)!.endReason, 'completed', 'the previous session ends as completed');
  assert.equal(server.session(committed.session.id)!.state, 'playing', 'the next session inherits the state');
  // Still the old track until the boundary; then the next. Its last reports go nowhere (the
  // server ended it), and they don't end playback.
  assert.equal(playback.getSnapshot().session!.id, first.id);
  const reports = server.session(first.id)!.reports.length;
  await api.progressPlayback(first.id, {generation: 1, sequence: 99, positionSeconds: 175, state: 'playing'});
  await settle();
  assert.equal(server.session(first.id)!.reports.length, reports);
  assert.equal(playback.getSnapshot().phase, 'ready');
  await player.audioBoundary(prepared.token, 0.05);
  assert.equal(playback.getSnapshot().session!.id, committed.session.id);
  assert.equal(playback.getSnapshot().itemId, 'song-2');
  assert.equal(player.getSnapshot().view?.queue.currentPlaybackId, committed.session.id);
  // One try per track: the new track prepares again only near its own end.
  assert.equal(server.preparations, 1);
  stop();
});

test('§18: a canceled or expired edge falls back to the ordinary end and advance', async () => {
  let clock = Date.now();
  const {server, playback, player, stop} = transitionsSetup(() => clock);
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  const first = playback.getSnapshot().session!;
  nearEnd(playback, 10);
  await settle();
  const prepared = playback.getSnapshot().audioEffects.prepared!;
  player.audioPrepared(prepared.token);
  // Another command on the queue cancels the preparation on the server.
  await player.workspace.service.mutate({action: 'repeat', repeat: 'all'} as never).catch(() => {});
  await player.commitAudio(prepared.token);
  assert.equal(server.commits, 0);
  assert.equal(playback.getSnapshot().audioEffects.prepared, undefined, 'the edge is dropped');
  assert.equal(playback.getSnapshot().listeningNotice, undefined, 'transient recovery is silent');
  assert.equal(playback.getSnapshot().session!.id, first.id, 'the current track keeps playing');
  // Expiry is checked before asking: no request, the same fallback.
  const again = transitionsSetup(() => clock);
  await again.player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  nearEnd(again.playback, 10);
  await settle();
  const late = again.playback.getSnapshot().audioEffects.prepared!;
  again.player.audioPrepared(late.token);
  clock += 61_000;
  await again.player.commitAudio(late.token);
  assert.equal(again.server.commits, 0);
  assert.equal(again.playback.getSnapshot().listeningNotice, undefined, 'expiry is silent');
  stop(); again.stop();
});

test('§18: without the server’s queue transitions (or for video) nothing is prepared', async () => {
  const {server, playback, player, stop} = setup();
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  nearEnd(playback, 10);
  await settle();
  assert.equal(playback.getSnapshot().audioEffects.prepared, undefined);
  stop();
  const video = transitionsSetup();
  await video.player.playEntries(['a', 'b'].map(queueItemInput));
  nearEnd(video.playback, 10);
  await settle();
  assert.equal(video.server.preparations, 0);
  video.stop();
  void server;
});

test('§18 (B7): music plays on v1 by default, /v2 only when asked; the transitions capability is read', () => {
  assert.equal(playbackV1ForKind(true, 'song'), true, 'music is on v1');
  assert.equal(playbackV1ForKind(true, 'album'), true);
  assert.equal(playbackV1ForKind(true, 'song', {music: false}), false);
  assert.equal(playbackV1ForKind(false, 'song', {music: true}), false);
  assert.equal(playbackV1ForKind(true, 'movie'), true);
  assert.equal(serverSupportsQueueTransitions({features: {queueTransitions: 'enabled'}}), true);
  assert.equal(serverSupportsQueueTransitions({features: {playback_v1: 'enabled'}}), false);
});

// F-title, 23 Sep: on Apple (queue required) every v1 session was refused with "The server did
// not return canonical playback authority". A v1 session is its own authority (its lease is the
// timeline), so it plays.
test('with Apple’s options (queue required), a v1 session plays', async () => {
  const server = new FakePlaybackServer();
  const http = fakeHttp(server);
  let serial = 0;
  const api = new V1PlaybackApi(http, {timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const playback = new PlaybackService(api, () => `r${++serial}`, {queueRequired: true, deferRecovery: true});
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10});
  const stop = player.connect();
  try {
    await player.playEntries(['movie-1', 'movie-2'].map(queueItemInput));
    const s = playback.getSnapshot();
    assert.equal(s.error, undefined, s.error);
    assert.ok(s.session?.id, 'the v1 session was adopted');
    assert.equal(s.session?.protocol, 'v1');
    assert.equal(s.phase, 'starting');
  } finally { stop(); player.dispose(); }
});

// Web's retry (PlaybackService.retry → V1QueuePlayer.check) restarts the entry where the failed
// start meant to be, not from the server's resume or 0:00.
test('a retry after a start that never played restarts at the requested position', async () => {
  const {server, playback, player, stop} = setup();
  await player.playEntries(['movie-1'].map(queueItemInput), {startSeconds: 6});
  const first = playback.getSnapshot().session!.id;
  playback.fail(playback.getSnapshot().intentId, 'The selected queue item did not become ready.');
  assert.equal(playback.recoveryTarget(), 6);
  await playback.retry();
  const s = playback.getSnapshot();
  assert.notEqual(s.session?.id, first);
  assert.equal(server.session(s.session!.id)!.positionMs, 6000);
  assert.equal(s.pendingSeek?.positionSeconds, 6);
  stop();
});

// B7: a music container played (not shuffled) starts with the viewer's defaults; an
// explicit Shuffle is a shuffle whatever the default; video is untouched.
test('a music queue started with Play takes the viewer\'s shuffle and repeat defaults', async () => {
  const log: {method: string; path: string; body?: unknown}[] = [];
  const server = new FakePlaybackServer();
  const inner = fakeHttp(server);
  const http: V1Http = {send: async r => { log.push({method: r.method, path: r.path, body: r.body}); return inner.send(r as never) as never; }};
  let serial = 0;
  const api = new V1PlaybackApi(http, {timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const playback = new PlaybackService(api, () => `r${++serial}`, {});
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10, musicDefaults: () => ({shuffle: true, repeat: 'all'})});
  const stop = player.connect();
  try {
    await player.playSelector({container: {kind: 'album', id: 'album-1'}});
    const create = log.find(r => r.method === 'POST' && r.path === '/v1/queues')!;
    assert.deepEqual((create.body as any).segments[0].order, {mode: 'shuffle'});
    const patch = log.find(r => r.method === 'PATCH' && r.path.startsWith('/v1/queues/'));
    assert.deepEqual(patch?.body, {repeat: 'all'});
    assert.equal(player.getSnapshot().view?.queue.repeat, 'all');
    log.length = 0;
    await player.playSelector({container: {kind: 'season', id: 'season-1'}});
    assert.equal((log.find(r => r.path === '/v1/queues')!.body as any).segments[0].order, undefined, 'video keeps its order');
    assert.equal(log.some(r => r.method === 'PATCH'), false);
  } finally { stop(); }
});

test('sameAlbum: the next entry follows the current one on its album', async () => {
  const {player, stop} = setup();
  try {
    await player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
    const held = player as unknown as {window: readonly Record<string, unknown>[]};
    const album = [{albumId: 'a', disc: 1, track: 1}, {albumId: 'a', disc: 1, track: 2}, {albumId: 'b', disc: 1, track: 1}];
    held.window = held.window.map((e, i) => ({...e, ...album[i]}));
    assert.equal(player.sameAlbum('song-1', 'song-2'), true);
    assert.equal(player.sameAlbum('song-2', 'song-3'), false);
  } finally { stop(); }
});

test('a failed optional audio renderer hands the same session and position to ordinary playback',async t=>{
 const f=transitionsSetup();t.after(()=>f.stop());await f.player.playEntries([queueItemInput('song-a','song')]);const p=f.playback.getSnapshot();f.playback.ready(p.intentId);if(p.pendingSeek)f.playback.seekApplied(p.intentId,p.pendingSeek.revision,p.pendingSeek.positionSeconds,'playing');f.playback.fact(p.intentId,30,'playing');const before=f.playback.getSnapshot();f.player.audioRenderFailed('Decoder failed');const after=f.playback.getSnapshot();assert.equal(after.session?.id,before.session?.id);assert.equal(after.audioEffects.rendering,false);assert.notEqual(after.phase,'error');assert.equal(after.pendingSeek?.positionSeconds,30);
});

// ── Choices, restarts in place and overtaken sessions ───────────────────────────────────────

test('changing quality re-plans the live session through the bridge and keeps the queue', async () => {
  const {player, playback, log, stop} = setup();
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const before = playback.getSnapshot().session!;
  const s = playback.getSnapshot();
  playback.ready(s.intentId);
  playback.seekApplied(s.intentId, s.pendingSeek!.revision, s.pendingSeek!.positionSeconds, 'playing');
  playback.fact(s.intentId, 77, 'playing');
  await settle();
  await playback.changePlayback({quality: {mode: 'limit', maxHeight: 720, maxVideoBitrateKbps: 4000}});
  const patch = log.find(r => r.method === 'PATCH' && r.path === `/v1/playback/sessions/${before.id}`)!;
  assert.deepEqual((patch.body as {quality: unknown}).quality, {mode: 'limit', maxHeight: 720, maxVideoBitrateKbps: 4000});
  const after = playback.getSnapshot();
  assert.equal(after.session!.id, before.id);
  assert.equal(after.session!.generation, before.generation + 1);
  assert.equal(after.pendingSeek?.positionSeconds, 77, 'the engine loads the new stream where the picture was');
  assert.equal(log.filter(r => r.method === 'POST' && r.path === '/v1/queues').length, 1);
  assert.equal(player.getSnapshot().view!.queue.entries.length, 3);
  // The quality chosen for this viewing travels with the next start in the same queue…
  await player.check();
  const again = log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!;
  assert.deepEqual((again.body as {quality?: unknown}).quality, {mode: 'limit', maxHeight: 720, maxVideoBitrateKbps: 4000});
  // …and a new play starts from the default again.
  await player.playItem('z', 5);
  const fresh = log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!;
  assert.equal((fresh.body as {quality?: unknown}).quality, undefined);
  stop();
});

test('playing the current entry’s title again restarts that entry in place, with the choices given', async () => {
  const {player, playback, log, stop} = setup();
  await player.playEntries(['a', 'b'].map(queueItemInput));
  const queue = player.getSnapshot().view!.queue.id, first = playback.getSnapshot().session!.id;
  await playback.play('a', 12, undefined, undefined, 3, {versionId: 'v2'});
  assert.equal(log.filter(r => r.method === 'POST' && r.path === '/v1/queues').length, 1, 'the queue is kept');
  const start = log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!.body as Record<string, unknown>;
  assert.deepEqual(start.queue, {queueId: queue, entryId: player.getSnapshot().view!.queue.currentEntryId});
  assert.equal(start.startPositionMs, 12_000);
  assert.equal(start.versionId, 'v2');
  assert.deepEqual(start.audio, {trackId: 'a3'});
  assert.equal(start.replacesSessionId, first);
  assert.notEqual(playback.getSnapshot().session!.id, first);
  assert.equal(player.getSnapshot().view!.next.available, true);
  // A different title is a new queue, and a version or track picked for it goes with its start.
  await playback.play('other', undefined, undefined, undefined, undefined, {audioTrackId: 'a2', quality: {mode: 'original'}});
  assert.equal(log.filter(r => r.method === 'POST' && r.path === '/v1/queues').length, 2);
  const other = log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!.body as Record<string, unknown>;
  assert.deepEqual(other.audio, {trackId: 'a2'});
  assert.deepEqual(other.quality, {mode: 'original'});
  stop();
});

test('a session whose answer arrives after the viewer left is stopped, not left to its lease', async () => {
  const {player, playback, server, stop} = setup();
  await player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const q = player.getSnapshot().view!.queue;
  const moving = player.next(q.id, q.revision);
  playback.leave(); // the viewer closed the player while Next was in flight
  await moving;
  await settle();
  const live = [...(server as unknown as {sessions: Map<string, {ended: boolean}>}).sessions.values()].filter(s => !s.ended);
  assert.equal(live.length, 0, 'nothing is left holding a stream');
  assert.equal(playback.getSnapshot().phase, 'idle');
  stop();
  // The same once the request is already with the server when the viewer leaves.
  const second = setup();
  await second.player.playEntries(['a', 'b', 'c'].map(queueItemInput));
  const q2 = second.player.getSnapshot().view!.queue;
  const inFlight = second.player.next(q2.id, q2.revision);
  for (let i = 0; i < 4; i++) await Promise.resolve();
  assert.equal(second.log.filter(r => r.path.endsWith(':advance')).length, 1, 'the move reached the server');
  second.playback.leave();
  await inFlight;
  await settle();
  assert.equal([...(second.server as unknown as {sessions: Map<string, {ended: boolean}>}).sessions.values()].filter(s => !s.ended).length, 0);
  assert.equal(second.playback.getSnapshot().phase, 'idle');
  second.stop();
});

test('a retry after the queue moved on does not start the new entry at the old title’s position', async () => {
  const {player, playback, log, stop} = setup();
  await player.playEntries(['a', 'b'].map(queueItemInput));
  const s = playback.getSnapshot();
  playback.ready(s.intentId);
  playback.seekApplied(s.intentId, s.pendingSeek!.revision, s.pendingSeek!.positionSeconds, 'playing');
  playback.fact(s.intentId, 150, 'playing');
  // The queue is moved by another device; this one then fails and retries.
  const header = player.getSnapshot().view!.queue;
  await new QueueClient((player as unknown as {http: V1Http}).http, () => 'other-device-key-0001').advance(header.id, header.revision, 'next');
  await player.refresh();
  playback.fail(playback.getSnapshot().intentId, 'Playback stopped.');
  await player.check();
  const start = log.filter(r => r.method === 'POST' && r.path === '/v1/playback/sessions').at(-1)!.body as Record<string, unknown>;
  assert.equal(start.startPositionMs, undefined, 'entry b starts where its own progress says');
  assert.equal(start.startFrom, 'resume');
  stop();
});

test('the server still-watching policy holds completion and a manual Continue releases it', async () => {
  const {player, playback, log, stop} = setup({postPlay: {autoplay: true, countdownMs: 5000, passoutCheckDue: true, automaticAdvances: 2}});
  await player.playEntries(['a', 'b'].map(queueItemInput));
  playToEnd(playback);
  await settle(); await settle();
  const held = player.getSnapshot().completion;
  assert.equal(held?.postPlay.passoutCheckDue, true);
  assert.equal(held?.postPlay.countdownSeconds, 5);
  assert.equal(held?.postPlay.automaticAdvances, 2);
  assert.equal(held?.postPlay.startDuringCredits, false, 'starting during the credits is off unless the server says on');
  await player.continueCompletion('automatic');
  assert.ok(player.getSnapshot().completion, 'an automatic callback must leave the question open');
  assert.equal(log.filter(r => r.path.endsWith(':advance')).length, 0);
  await player.continueCompletion('still-watching');
  assert.equal(playback.getSnapshot().itemId, 'b');
  assert.equal((log.find(r => r.path.endsWith(':advance'))?.body as {reason: string}).reason, 'next');
  stop();
});

test('a subtitle choice is a change of the live session: the track, how it is delivered and its timing', async () => {
  const {player, playback, log, stop} = setup();
  assert.equal(playback.changesInPlace(), false, 'nothing is playing yet');
  await player.playEntries(['a', 'b'].map(queueItemInput));
  const session = playback.getSnapshot().session!;
  assert.equal(playback.changesInPlace(), true);
  const patches = () => log.filter(r => r.method === 'PATCH' && r.path === `/v1/playback/sessions/${session.id}`).map(r => { const body = r.body as Record<string, unknown>; return {subtitles: body.subtitles, subtitleOffsetMs: body.subtitleOffsetMs}; });
  // A styled or image track this device draws itself: the original is asked for, with the track's saved timing.
  await playback.changePlayback({subtitleTrackId: 'sub-1', subtitleDelivery: 'embeddedClient', subtitleOffsetMs: -300.4});
  // A text track: no delivery named, the server decides.
  await playback.changePlayback({subtitleTrackId: 'sub-2', subtitleOffsetMs: 0});
  assert.equal(playback.playbackChoices().subtitleDelivery, undefined, 'another track cannot inherit the prior delivery');
  // An image track that could not be drawn here: in the picture.
  await playback.changePlayback({subtitleTrackId: 'sub-1', subtitleDelivery: 'burn'});
  assert.equal(playback.playbackChoices().subtitleDelivery, 'burn');
  await playback.changePlayback({subtitleTrackId:'sub-2'});
  assert.equal(playback.playbackChoices().subtitleDelivery, undefined, 'text must not inherit a drawn track burn');
  // Off: no delivery and no timing travel with it.
  await playback.changePlayback({subtitleTrackId: null, subtitleDelivery: 'embeddedClient', subtitleOffsetMs: 500});
  assert.deepEqual(patches(), [
    {subtitles: {trackId: 'sub-1', delivery: 'embeddedClient'}, subtitleOffsetMs: -300},
    {subtitles: {trackId: 'sub-2'}, subtitleOffsetMs: 0},
    {subtitles: {trackId: 'sub-1', delivery: 'burn'}, subtitleOffsetMs: undefined},
    {subtitles: {trackId:'sub-2'},subtitleOffsetMs:undefined},
    {subtitles: {trackId: null}, subtitleOffsetMs: undefined},
  ]);
  assert.equal(playback.getSnapshot().session!.id, session.id, 'the same session throughout');
  assert.equal(log.filter(r => r.method === 'POST' && r.path === '/v1/queues').length, 1, 'and the same queue');
  stop();
});


test('republishing capabilities re-plans the same session without remembering the one-shot action', async () => {
  const {player, playback, log, stop} = setup();
  try {
    await player.playEntries(['a', 'b'].map(queueItemInput));
    const session = playback.getSnapshot().session!;
    const queue = player.getSnapshot().view!.queue.id;
    await playback.changePlayback({quality: {mode: 'fixed', id: '720p-high'}, audioTrackId: 'a2'});
    await playback.changePlayback({replan: true});
    const patch = log.filter(r => r.method === 'PATCH' && r.path === `/v1/playback/sessions/${session.id}`).at(-1)!;
    assert.equal((patch.body as {replan?: boolean}).replan, true);
    assert.equal(playback.getSnapshot().session!.id, session.id);
    assert.equal(player.getSnapshot().view!.queue.id, queue);
    assert.equal(playback.playbackChoices().audioTrackId, 'a2');
    assert.deepEqual(playback.playbackChoices().quality, {mode: 'fixed', id: '720p-high'});
    assert.equal(playback.playbackChoices().replan, undefined);
    assert.equal(log.filter(r => r.method === 'POST' && r.path === '/v1/queues').length, 1);
  } finally { stop(); }
});

test('the queue view carries the viewer\'s choice to start the next title during the credits', async () => {
  for (const startDuringCredits of [true, false]) {
    const {player, stop} = setup({postPlay: {autoplay: true, countdownMs: 10000, passoutCheckDue: false, automaticAdvances: 0, startDuringCredits}});
    await player.playEntries(['a', 'b'].map(queueItemInput));
    await settle();
    assert.equal(player.getSnapshot().view?.postPlay.startDuringCredits, startDuringCredits);
    stop();
  }
});


test('episode picks beyond the loaded window continue one run across the full show',async()=>{
 const {player,playback,stop,log}=setup({server:{containerSize:()=>120,itemKind:()=> 'episode'}});
 try {
  await player.playEpisode('show','show-0');
  const run=playback.getSnapshot().playbackRun;
  playback.setPlaybackRate(1.5);
  await player.selectEpisode('show-100');
  assert.equal(playback.getSnapshot().itemId,'show-100');
  assert.equal(playback.getSnapshot().playbackRun,run);
  assert.ok(log.some(r=>r.method==='GET'&&r.path.includes('/entry?itemId=show-100')));
  assert.equal(log.filter(r=>r.method==='POST'&&r.path==='/v1/queues').length,1);
  await player.playEpisode('show','show-1');
  assert.equal(playback.getSnapshot().playbackRun,run+1,'a fresh outside play resets the run');
 } finally {stop();}
});


test('the player view preserves batched identity and display facts plus whole queue totals',async()=>{
 const server=new FakePlaybackServer({containerSize:()=>5});const inner=fakeHttp(server);
 const facts={albumId:'album',album:'Record',artistId:'artist',artist:'Band',showId:'show',seasonId:'season',episode:{seasonNumber:2,number:7},backdropUrl:'/v1/items/show-0/art/backdrop',segmentId:'segment'};
 const http:V1Http={async send(r){const response=await inner.send(r);const b=response.body as {window?: Record<string,unknown>[];queue?:Record<string,unknown>;items?:Record<string,unknown>[];id?:string;segments?:unknown}|undefined;if(b?.queue){Object.assign(b.queue,{totalDurationMs:900000,durationKnown:true,remainingCount:4});if(b.window)b.window=b.window.map(e=>({...e,...facts}));}if(b?.id&&b.segments)Object.assign(b,{totalDurationMs:900000,durationKnown:true,remainingCount:4});if(b?.items)b.items=b.items.map(e=>({...e,...facts}));return response;}};
 let key=0;const api=new V1PlaybackApi(http,{key:()=>`view-${++key}-00000000`});const playback=new PlaybackService(api,()=> 'device');const player=new V1QueuePlayer({http,api,playback,viewer,key:()=>`queue-view-${++key}-0000`});const stop=player.connect();
 try {await player.playEpisode('show','show-0');const view=player.getSnapshot().view;assert.equal(view?.totalDurationMs,900000);assert.equal(view?.remainingCount,4);assert.deepEqual(view?.items[0]?.episode,{seasonNumber:2,number:7});assert.equal(view?.items[0]?.artistId,'artist');assert.equal(view?.items[0]?.backdropUrl,facts.backdropUrl);assert.equal(view?.items[0]?.durationMs,180000);assert.equal(view?.items[0]?.segmentId,'segment');} finally{stop();}
});


test('strict queue changes return the actual same session and occurrence without rebuilding or remembering a substitute',async()=>{
 const f=setup();try{
  await f.player.playItem('movie');const before=f.player.currentSession()!,queue=f.player.getSnapshot().view!.queue;
  const reports=f.log.filter(r=>r.method==='PUT'&&r.path.endsWith('/observation')).length;
  const actual=await f.player.changePlayback({versionId:before.versionId,subtitleTrackId:'s1',subtitleDelivery:'burn',subtitleOffsetMs:-300.4},{recoverEnded:false,isCurrent:()=>true});
  assert.equal(f.log.filter(r=>r.method==='PUT'&&r.path.endsWith('/observation')).length,reports,'strict replan must not send a separate observation first');
  assert.equal(actual,f.player.currentSession());assert.equal(actual!.id,before.id);assert.deepEqual(actual!.queue,before.queue);
  assert.ok(actual!.presentation.generation>before.presentation.generation);assert.equal(f.playback.getSnapshot().session!.generation,actual!.presentation.generation);
  assert.equal(f.player.getSnapshot().view!.queue.id,queue.id);
  const patch=f.log.filter(r=>r.method==='PATCH').at(-1)!.body as {subtitles:unknown;subtitleOffsetMs:number};
  assert.deepEqual(patch.subtitles,{trackId:'s1',delivery:'burn'});assert.equal(patch.subtitleOffsetMs,-300);
  assert.equal(f.log.filter(r=>r.method==='POST'&&r.path==='/v1/queues').length,1);
 }finally{f.stop();}
});

test('strict queue changes refuse ended 404 and 410 instead of creating a replacement while ordinary changes still recover',async()=>{
 for(const status of [404,410])for(const strict of [false,true]){
  let reject=false;const f=setup({afterResponse:async(request,response)=>reject&&request.method==='GET'&&request.path.startsWith('/v1/playback/sessions/')?
    {status,headers:{},body:{error:{code:status===404?'session_not_found':'session_ended',retry:'never'}}}:response});
  try{await f.player.playItem('movie');const original=f.player.currentSession()!;const starts=f.log.filter(r=>r.method==='POST'&&r.path==='/v1/playback/sessions').length;
   reject=true;
   if(strict){await assert.rejects(f.player.changePlayback({subtitleTrackId:'s1',subtitleDelivery:'burn'},{recoverEnded:false}),error=>error instanceof PlaybackApiError&&error.status===status);assert.equal(f.player.currentSession()!.id,original.id);}
   else{await f.player.changePlayback({subtitleTrackId:'s1',subtitleDelivery:'burn'});assert.notEqual(f.player.currentSession()!.id,original.id);}
   assert.equal(f.log.filter(r=>r.method==='POST'&&r.path==='/v1/playback/sessions').length,starts+(strict?0:1));
  }finally{f.stop();}
 }
});

test('strict queue changes discard a late native-bridge replan before adoption after abort, host retirement or newer Play',async()=>{
 for(const retire of ['abort','host','play'] as const){
  let hold=false,entered!:()=>void,release!:()=>void,signal:AbortSignal|undefined;
  const reached=new Promise<void>(resolve=>{entered=resolve}),gate=new Promise<void>(resolve=>{release=resolve});
  const f=setup({afterResponse:async(request,response)=>{if(hold&&request.method==='PATCH'&&response.status===200){signal=request.signal;entered();await gate;}return response;}});
  try{await f.player.playItem('movie');const original=f.player.currentSession()!,abort=new AbortController();let current=true;
   hold=true;const pending=f.player.changePlayback({subtitleTrackId:'s1',subtitleDelivery:'burn'},{recoverEnded:false,signal:abort.signal,isCurrent:()=>current});const rejected=assert.rejects(pending,{name:'AbortError'});await reached;
   if(retire==='abort')abort.abort();else if(retire==='host')current=false;else await f.player.playItem('other');
   if(retire==='abort')assert.equal(signal!.aborted,true);
   const shown=f.playback.getSnapshot().session!,deletes=f.log.filter(r=>r.method==='DELETE').length;
   release();await rejected;assert.equal(f.playback.getSnapshot().session,shown);assert.equal(f.log.filter(r=>r.method==='DELETE').length,deletes,'late same-session replies must never stop a replacement');
   if(retire!=='play')assert.equal(f.player.currentSession(),original,'native bridge and queue cannot adopt the canceled generation');
  }finally{f.stop();}
 }
});

test('strict queue lifetime is checked before transport and is superseded by a newer ordinary change',async()=>{
 let hold=false,entered!:()=>void,release!:()=>void;
 const reached=new Promise<void>(resolve=>{entered=resolve}),gate=new Promise<void>(resolve=>{release=resolve});
 const f=setup({afterResponse:async(request,response)=>{if(hold&&request.method==='PATCH'){hold=false;entered();await gate;}return response;}});
 try{await f.player.playItem('movie');const abort=new AbortController();abort.abort();const count=f.log.length;
  await assert.rejects(f.player.changePlayback({subtitleTrackId:'s1'},{recoverEnded:false,signal:abort.signal}),{name:'AbortError'});
  await assert.rejects(f.player.changePlayback({subtitleTrackId:'s1'},{recoverEnded:false,isCurrent:()=>false}),{name:'AbortError'});assert.equal(f.log.length,count);
  hold=true;const old=f.player.changePlayback({subtitleTrackId:'s1',subtitleDelivery:'burn'},{recoverEnded:false});const rejected=assert.rejects(old,{name:'AbortError'});await reached;
  await f.player.changePlayback({audioTrackId:'a2'});const actual=f.player.currentSession();release();await rejected;assert.equal(f.player.currentSession(),actual);
 }finally{f.stop();}
});


test('a lifetime-scoped ended recovery cannot return an unrelated newer Play as its result',async()=>{
 let retiredSession='',hold=true,entered!:()=>void,release!:()=>void;
 const reached=new Promise<void>(resolve=>{entered=resolve}),gate=new Promise<void>(resolve=>{release=resolve});
 const f=setup({afterResponse:async(request,response)=>{
  if(retiredSession&&request.method==='GET'&&request.path===`/v1/playback/sessions/${retiredSession}`)return {status:410,headers:{},body:{error:{code:'session_ended',retry:'never'}}};
  if(retiredSession&&hold&&request.method==='POST'&&request.path==='/v1/playback/sessions'){hold=false;entered();await gate;}
  return response;
 }});
 try{await f.player.playItem('movie');retiredSession=f.player.currentSession()!.id;
  const pending=f.player.changePlayback({subtitleTrackId:'s1'},{isCurrent:()=>true});const rejected=assert.rejects(pending,{name:'AbortError'});await reached;
  await f.player.playItem('other');const current=f.player.currentSession();release();await rejected;assert.equal(f.player.currentSession(),current);
 }finally{f.stop();}
});

// These regression sources are intentionally not executed during campaign item work.
function queuedSelection(f: ReturnType<typeof setup>) {
  const session = f.player.currentSession()!, view = f.player.getSnapshot().view!;
  const row = view.window!.entries.find(e => e.entryId !== session.queue!.entryId && e.available === true)!;
  assert.ok(row);
  return {target: {sessionId: session.id, generation: session.presentation.generation,
    playbackRun: f.playback.getSnapshot().playbackRun, itemId: session.itemId!, versionId: session.versionId!,
    queueId: session.queue!.queueId, entryId: session.queue!.entryId},
  request: {queueId: view.queue.id, expectedRevision: view.queue.revision, entryId: row.entryId}};
}

test('queued selection never stops or forgets a same-id same-generation occurrence accepted by a later intent/run', async () => {
  for (const freshRun of [false, true]) {
    let admitted: Session | undefined, retired = 0;
    const forgottenIds: string[] = [];
    const f = setup({server: {itemKind: () => 'movie'}, retireQueuedSelection: () => {retired++; return true;},
      afterResponse: async (request, response) => {
        if (request.path.endsWith(':advance') && response.status === 200) admitted = parseSession((response.body as {session: unknown}).session);
      }});
    try {
      await f.player.playEntries(['movie-a', 'movie-b'].map(queueItemInput));
      const selection = queuedSelection(f);
      const forget = f.api.forget.bind(f.api);
      f.api.forget = id => {forgottenIds.push(id); forget(id);};
      let owner: ReturnType<typeof f.api.selectionRegistration> | undefined;
      f.api.duration = async () => {
        assert.ok(admitted);
        const playback = f.api.adopt(admitted, 120);
        owner = f.api.selectionRegistration(admitted.id, playback);
        const intent = f.playback.beginQueueIntent(admitted.itemId, freshRun);
        await f.playback.adoptQueueSession(admitted.itemId!, playback, intent);
        throw new Error('late original duration failed');
      };
      await assert.rejects(f.player.selectQueuedEntry(selection.target, selection.request, {isCurrent: () => true}));
      assert.equal(f.playback.getSnapshot().session?.id, admitted!.id);
      assert.equal(f.playback.getSnapshot().session?.generation, admitted!.presentation.generation);
      assert.notEqual(f.playback.getSnapshot().intentId, 0);
      assert.equal(owner?.current(), true, 'later registration remains owned');
      assert.equal(retired, 0);
      assert.ok(admitted);
      assert.equal(forgottenIds.includes(admitted!.id), false, 'the admitted/live occurrence is never forgotten');
      for (const id of forgottenIds) assert.equal(id, selection.target.sessionId, 'only the original replaced selection is forgotten');
    } finally {f.stop();}
  }
});

test('queued selection cannot clean a re-registered occurrence even before another output is adopted', async () => {
  let admitted: Session | undefined, retired = 0;
  const f = setup({server: {itemKind: () => 'movie'}, retireQueuedSelection: () => {retired++; return true;},
    afterResponse: async (request, response) => {
      if (request.path.endsWith(':advance') && response.status === 200) admitted = parseSession((response.body as {session: unknown}).session);
    }});
  try {
    await f.player.playEntries(['movie-a', 'movie-b'].map(queueItemInput));
    const selection = queuedSelection(f);
    let owner: ReturnType<typeof f.api.selectionRegistration> | undefined;
    f.api.duration = async () => {
      assert.ok(admitted); const playback = f.api.adopt(admitted, 120); owner = f.api.selectionRegistration(admitted.id, playback);
      throw new Error('original selection failed after registration changed');
    };
    await assert.rejects(f.player.selectQueuedEntry(selection.target, selection.request, {isCurrent: () => true}));
    assert.equal(owner?.current(), true); assert.equal(retired, 0);
  } finally {f.stop();}
});

test('queued selection original-principal retirement refusal/throw/absence never falls back to raw stop or forget', async () => {
  for (const outcome of ['refuse', 'throw', 'absent'] as const) {
    let retired = 0, stops = 0, forgets = 0;
    const f = setup({server: {itemKind: () => 'movie'}, retireQueuedSelection: outcome === 'absent' ? undefined : () => {
      retired++; if (outcome === 'throw') throw new Error('original principal retired'); return false;
    }});
    try {
      await f.player.playEntries(['movie-a', 'movie-b'].map(queueItemInput));
      const selection = queuedSelection(f);
      f.api.duration = async () => {throw new Error('duration failed');};
      f.api.stopPlayback = async () => {stops++;}; f.api.forget = () => {forgets++;};
      await assert.rejects(f.player.selectQueuedEntry(selection.target, selection.request, {isCurrent: () => true}));
      assert.equal(retired, outcome === 'absent' ? 0 : 1);
      assert.equal(stops, 0); assert.equal(forgets, 0);
    } finally {f.stop();}
  }
});


test('queued selection cleanup cannot use a changed original core operation after another Play', async () => {
  let retired = 0;
  const f = setup({server: {itemKind: () => 'movie'}, retireQueuedSelection: () => {retired++; return true;}});
  try {
    await f.player.playEntries(['movie-a', 'movie-b'].map(queueItemInput));
    const selection = queuedSelection(f), duration = f.api.duration.bind(f.api);
    f.api.duration = async () => {
      f.api.duration = duration;
      await f.player.playItem('replacement-movie');
      throw new Error('late original operation');
    };
    await assert.rejects(f.player.selectQueuedEntry(selection.target, selection.request, {isCurrent: () => true}));
    assert.equal(f.playback.getSnapshot().itemId, 'replacement-movie');
    assert.equal(retired, 0);
  } finally {f.stop();}
});

function watchedPreparation(pending: Promise<void>) {
  let done = false;
  const promise = pending.finally(() => {done = true;});
  void promise.catch(() => {}); // Tests join the original result after advancing its supplied clock.
  return {promise, done: () => done};
}
async function drainPreparation(f: ReturnType<typeof setup>, runs: readonly ReturnType<typeof watchedPreparation>[]) {
  for (let round = 0; round < 24 && runs.some(run => !run.done()); round++) {
    f.timers.shift()?.fn();
    await settle();
  }
  assert.ok(runs.every(run => run.done()), 'original starts settle within the bounded preparation clock drain');
  await Promise.allSettled(runs.map(run => run.promise));
}

test('a queue start carries the real preparation notice on its original intent and clears it on acceptance', async () => {
  const f = setup({server: {prepareRounds: () => 2}});
  const started = watchedPreparation(f.player.playEntries(['movie'].map(queueItemInput), {startSeconds: 0}));
  try {
    assert.equal(f.playback.getSnapshot().preparation, undefined, 'nothing prepares before a real 202');
    assert.equal(f.player.getSnapshot().preparation, undefined);
    for (let i = 0; i < 60 && !f.playback.getSnapshot().preparation; i++) await new Promise(resolve => setTimeout(resolve, 5));
    const notice = {messageId: 'player.preparing.slowStart', retryAfterMs: 500};
    assert.deepEqual(f.playback.getSnapshot().preparation, notice);
    assert.equal(f.playback.getSnapshot().session, undefined, 'a retry-only 202 installs no session');
    assert.deepEqual(f.player.getSnapshot().preparation, notice);
    assert.ok(f.log.some(request => request.method === 'POST' && request.path === '/v1/playback/sessions'), 'the separate session start POST was reached');
    await drainPreparation(f, [started]); await started.promise;
    assert.equal(f.playback.getSnapshot().preparation, undefined, 'acceptance clears the notice');
    assert.equal(f.player.getSnapshot().preparation, undefined);
    assert.ok(f.playback.getSnapshot().session);
  } finally { f.stop(); await drainPreparation(f, [started]); }
});

test('a terminal queue start failure clears the preparation notice', async () => {
  const f = setup({server: {prepareRounds: () => 3}});
  const started = watchedPreparation(f.player.playEntries(['movie'].map(queueItemInput), {startSeconds: 0}));
  try {
    for (let i = 0; i < 60 && !f.playback.getSnapshot().preparation; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.ok(f.playback.getSnapshot().preparation);
    f.server.failNext(500);
    await drainPreparation(f, [started]);
    assert.equal(f.playback.getSnapshot().preparation, undefined);
    assert.equal(f.player.getSnapshot().preparation, undefined);
    // A queue start that fails leaves the queue itself usable and names the failure on both
    // owners; the playback service is the one that moves to error.
    assert.equal(f.playback.getSnapshot().phase, 'error');
    assert.equal(f.player.getSnapshot().phase, 'ready');
    assert.ok(f.player.getSnapshot().error);
    assert.equal(f.player.getSnapshot().error, f.playback.getSnapshot().error);
  } finally { f.stop(); await drainPreparation(f, [started]); }
});

test('leaving a preparing queue start clears its notice and installs nothing', async () => {
  const f = setup({server: {prepareRounds: () => 3}});
  const started = watchedPreparation(f.player.playEntries(['movie'].map(queueItemInput), {startSeconds: 0}));
  try {
    for (let i = 0; i < 60 && !f.playback.getSnapshot().preparation; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.ok(f.playback.getSnapshot().preparation);
    const posts = f.log.filter(request => request.method === 'POST' && request.path === '/v1/playback/sessions').length;
    assert.equal(posts, 1);
    f.player.leave();
    assert.equal(f.playback.getSnapshot().preparation, undefined);
    assert.equal(f.player.getSnapshot().preparation, undefined);
    assert.equal(f.playback.getSnapshot().session, undefined);
    await drainPreparation(f, [started]);
    assert.equal(f.log.filter(request => request.method === 'POST' && request.path === '/v1/playback/sessions').length, posts, 'retirement prevents another POST after the pending wait');
  } finally { f.stop(); await drainPreparation(f, [started]); }
});

test('a stale older queue preparation cannot publish over its successor', async () => {
  const f = setup({server: {prepareRounds: () => 4}});
  const first = watchedPreparation(f.player.playEntries(['movie'].map(queueItemInput), {startSeconds: 0}));
  const runs = [first];
  try {
    for (let i = 0; i < 60 && !f.playback.getSnapshot().preparation; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.ok(f.playback.getSnapshot().preparation);
    const second = watchedPreparation(f.player.playEntries(['other'].map(queueItemInput), {startSeconds: 0}));
    runs.push(second);
    await drainPreparation(f, runs); await second.promise;
    const snapshot = f.playback.getSnapshot();
    assert.equal(snapshot.preparation, undefined);
    assert.equal(snapshot.itemId, 'other', 'the successor owns the current intent');
    assert.ok(snapshot.session);
  } finally { f.stop(); await drainPreparation(f, runs); }
});

test('V15: a transient final confirmation failure retries and advances only after confirmation',async t=>{
 const f=setup();
 try{
  await f.player.playEntries(['a','b'].map(queueItemInput));
  const confirm=f.playback.confirmQueueEnd.bind(f.playback);let calls=0;
  t.mock.method(f.playback,'confirmQueueEnd',async(intent:number)=>{if(++calls===1)throw new PlaybackApiError(503,'timeline_unconfirmed','same_request');await confirm(intent);});
  t.mock.timers.enable({apis:['setTimeout']});
  const flush=async()=>{for(let n=0;n<100;n++)await Promise.resolve();};
  playToEnd(f.playback);await flush();assert.equal(calls,1);assert.equal(f.playback.getSnapshot().itemId,'a');
  t.mock.timers.tick(999);await flush();assert.equal(calls,1);
  t.mock.timers.tick(1);await flush();assert.equal(calls,2);assert.equal(f.playback.getSnapshot().itemId,'b');
  assert.equal(f.log.filter(r=>r.path.endsWith(':advance')).length,1);
 }finally{f.stop();}
});

test('V15: a pending end-confirmation retry cannot advance a replacement playback',async t=>{
 const f=setup();
 try{
  await f.player.playEntries(['a','b'].map(queueItemInput));let calls=0;
  t.mock.method(f.playback,'confirmQueueEnd',async()=>{calls++;throw new PlaybackApiError(503,'timeline_unconfirmed','same_request');});
  t.mock.timers.enable({apis:['setTimeout']});const flush=async()=>{for(let n=0;n<100;n++)await Promise.resolve();};
  playToEnd(f.playback);await flush();assert.equal(calls,1);
  await f.player.playEntries(['replacement'].map(queueItemInput));t.mock.timers.tick(5000);await flush();
  assert.equal(calls,1);assert.equal(f.playback.getSnapshot().itemId,'replacement');assert.equal(f.log.filter(r=>r.path.endsWith(':advance')).length,0);
 }finally{f.stop();}
});

test('V15: final confirmation honors Retry-After and exposes failure after three attempts',async t=>{
 const f=setup(),previous=serviceI18n();setServiceI18n({...previous,t:id=>id,has:()=>true});t.after(()=>setServiceI18n(previous));
 try{
  await f.player.playEntries(['a','b'].map(queueItemInput));let calls=0;
  t.mock.method(f.playback,'confirmQueueEnd',async()=>{calls++;throw new PlaybackApiError(503,'timeline_unconfirmed','same_request',undefined,5000);});
  t.mock.timers.enable({apis:['setTimeout']});const flush=async()=>{for(let n=0;n<100;n++)await Promise.resolve();};
  playToEnd(f.playback);await flush();assert.equal(calls,1);
  t.mock.timers.tick(4999);await flush();assert.equal(calls,1);
  t.mock.timers.tick(1);await flush();assert.equal(calls,2);
  t.mock.timers.tick(5000);await flush();assert.equal(calls,3);assert.ok(f.player.workspace.service.getSnapshot().error);
  t.mock.timers.tick(60000);await flush();assert.equal(calls,3);assert.equal(f.playback.getSnapshot().itemId,'a');assert.equal(f.log.filter(r=>r.path.endsWith(':advance')).length,0);
 }finally{f.stop();}
});
