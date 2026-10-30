import test from 'node:test';
import assert from 'node:assert/strict';
import {
  AdminSessionsClient, CapabilityPublisher, EventsClient, NowPlayingStore, PlaybackApiError,
  PlaybackOptionsClient, PlaybackSessionController, QUEUE_WINDOW_MAX, QueueClient, QueueView, SessionsClient, ShufflePermutation,
  defaultSelection, markerAt, seekNeedsServer, summarizePlan,
  type MediaPlayer, type PlayerEvent, type PlayerObservation, type PlayerSource, type ServerEvent,
} from '../src/playback-v1/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {androidDeclaredProfile, withMpvEngine, withViewerSettings} from '../src/client-profile.ts';

const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(setImmediate); };

function clockwork() {
  let now = 1_000_000;
  const timers: {fn: () => void; at: number; id: number}[] = [];
  let next = 0;
  return {
    now: () => now,
    setTimer: (fn: () => void, ms: number) => { const id = ++next; timers.push({fn, at: now + ms, id}); return id; },
    clearTimer: (id: unknown) => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); },
    async advance(ms: number) {
      const end = now + ms;
      for (;;) {
        timers.sort((a, b) => a.at - b.at);
        const t = timers[0];
        if (!t || t.at > end) break;
        timers.shift(); now = t.at; t.fn(); await settle();
      }
      now = end;
      await settle();
    },
  };
}

class FakePlayer implements MediaPlayer {
  loads: PlayerSource[] = [];
  seeks: number[] = [];
  sidecar: (string | null)[] = [];
  rate = 1;
  state: PlayerObservation['state'] = 'idle';
  generation = 0;
  positionMs = 0;
  private listeners = new Set<(e: PlayerEvent) => void>();
  load(s: PlayerSource) { this.loads.push(s); this.generation = s.generation; this.positionMs = s.startPositionMs; this.state = s.autoplay ? 'playing' : 'paused'; }
  play() { this.state = 'playing'; this.emit('state'); }
  pause() { this.state = 'paused'; this.emit('state'); }
  seek(ms: number) { this.seeks.push(ms); this.positionMs = ms; this.emit('seeked'); }
  setRate(r: number) { this.rate = r; }
  subtitle: string | null | undefined;
  selectSidecarSubtitle(id: string | null) { this.sidecar.push(id); this.subtitle = id; }
  observe(): PlayerObservation { return {generation: this.generation, state: this.state, positionMs: this.positionMs, rate: this.rate, ...(this.subtitle !== undefined ? {subtitleTrackId: this.subtitle} : {})}; }
  onEvent(l: (e: PlayerEvent) => void) { this.listeners.add(l); return () => { this.listeners.delete(l); }; }
  emit(type: 'state' | 'time' | 'seeked') { for (const l of [...this.listeners]) l({type, observation: this.observe()} as PlayerEvent); }
  dispose() { this.listeners.clear(); }
}

let keys = 0;
const key = () => 'key_' + String(++keys).padStart(16, '0');

test('queue-backed session start preserves the same explicit choices as direct item start', async () => {
  const server = new FakePlaybackServer();
  const http = fakeHttp(server);
  const queues = new QueueClient(http, key);
  const sessions = new SessionsClient(http);
  const created = await queues.create([{source: {items: {ids: ['movie-1']}}}]);
  const entry = created.window[0]!;
  const choices = {versionId: 'v2', startPositionMs: 17_000, state: 'paused' as const, audio: {trackId: 'a2', channels: 2}, subtitles: {trackId: null}, quality: {mode: 'fixed' as const, id: '720p-high' as const}};
  const queued = await sessions.start({queue: {queueId: created.queue.id, entryId: entry.entryId}}, choices, key());
  const direct = await sessions.start({itemId: 'movie-1'}, choices, key());
  assert.equal(queued.versionId, 'v2');
  assert.equal(queued.presentation.startPositionMs, 17_000);
  assert.equal(queued.presentation.audioTrackId, 'a2');
  assert.deepEqual(queued.presentation.quality, choices.quality);
  assert.equal(queued.state, 'paused');
  for (const field of ['versionId', 'positionMs', 'state', 'audio', 'subtitles', 'quality'] as const) assert.deepEqual(server.session(queued.id)?.[field], server.session(direct.id)?.[field], field);
});

// ── Options (§4) ───────────────────────────────────────────────────
test('options: tolerant parse, plan summary, default selection, markers and preview queries', async () => {
  const server = new FakePlaybackServer();
  const client = new PlaybackOptionsClient(fakeHttp(server));
  const o = await client.options('movie-1');
  assert.equal(o.versions.length, 1);
  assert.equal(o.markers.length, 2, 'an unknown marker type is kept with a fallback, not dropped or fatal');
  const sel = defaultSelection(o);
  assert.equal(sel.version?.id, 'v1');
  assert.equal(sel.audio?.id, 'a1');
  assert.equal(markerAt(o, 30_000)?.type, 'intro');
  const original = summarizePlan(o.plan!, sel.version);
  assert.equal(original.convertsVideo, false);
  assert.equal(original.convertsAudio, true, 'TrueHD converts on this device');
  const limited = await client.options('movie-1', {quality: {mode: 'limit', maxHeight: 1080}, audioId: 'a2'});
  const s = summarizePlan(limited.plan!, sel.version);
  assert.equal(s.kind, 'convert');
  assert.equal(s.convertsVideo, true);
  assert.equal(s.convertsAudio, false);
  // The quality menu, the saved default and the engine come with the options.
  assert.deepEqual(o.qualities.map(q => q.mode === 'fixed' ? q.id : q.mode), ['original','automatic','2160p','1080p-high','1080p-low','720p-high','720p-low','480p','240p']);
  assert.equal(o.preferred?.quality.mode, 'original');
  assert.equal(o.plan?.engine, 'native');
  assert.equal(o.plan?.streams[1]?.to, 'eac3');
  const fixed = await client.options('movie-1', {quality: {mode: 'fixed', id: '720p-high'}});
  assert.deepEqual(fixed.plan?.quality, {mode: 'fixed', id: '720p-high'});
  assert.equal(summarizePlan(fixed.plan!, sel.version).convertsVideo, true);
  // The capability document goes up only when it changes, and never when the server would refuse it.
  const publisher = new CapabilityPublisher(fakeHttp(server));
  const document = withMpvEngine(androidDeclaredProfile({television: true}));
  assert.equal(await publisher.publish(document), 'accepted');
  assert.equal(await publisher.publish(document), 'current');
  assert.equal(await publisher.publish(withViewerSettings(document, {engine: 'mpv', overrides: {forceStereo: true}})), 'accepted');
  assert.equal(await publisher.publish({...document, version: 1} as never), 'failed', 'a document the server would refuse is not sent');
  assert.deepEqual((server.capabilities.get('device-1') as {overrides?: unknown} | undefined)?.overrides ?? (([...server.capabilities.values()][0]) as {overrides?: unknown}).overrides, {forceStereo: true, disablePassthrough: false, disableHdr: false});
  assert.equal(server.capabilities.size, 1);
});

// ── Sessions (§5) ──────────────────────────────────────────────────
test('sessions: idempotent start replays, a reused key with a new body is 422, 202 preparation is polled', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now, prepareRounds: body => (body.itemId === 'big-remux' ? 2 : 0)});
  const client = new SessionsClient(fakeHttp(server), c);
  const a = await client.start({itemId: 'movie-1'}, {startFrom: 'resume'}, 'key_same_000000001');
  const b = await client.start({itemId: 'movie-1'}, {startFrom: 'resume'}, 'key_same_000000001');
  assert.equal(a.id, b.id);
  assert.equal(a.presentation.startPositionMs, 120_000);
  await assert.rejects(client.start({itemId: 'movie-2'}, {}, 'key_same_000000001'), (e: unknown) => e instanceof PlaybackApiError && e.status === 422 && e.code === 'idempotency_key_reused');
  const preparing: number[] = [];
  const started = client.start({itemId: 'big-remux'}, {}, 'key_prepare_0000001', {onPreparing: ms => preparing.push(ms)});
  await settle();
  assert.equal(preparing.length, 1);
  await c.advance(500);
  await c.advance(500);
  const s = await started;
  assert.equal(s.itemId, 'big-remux');
  assert.deepEqual(preparing, [500, 500]);
});

test('controller: start loads generation 1, reports, and replaces its own previous session', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key});
  const first = (await ctl.start({itemId: 'movie-1'}))!;
  assert.equal(player.loads.length, 1);
  assert.equal(player.loads[0]!.generation, 1);
  assert.equal(ctl.getSnapshot().phase, 'active');
  const second = (await ctl.start({itemId: 'movie-2'}))!;
  assert.notEqual(second.id, first.id);
  assert.equal(server.session(first.id)!.ended, true, 'replacesSessionId ended the old session');
  ctl.dispose();
});

test('controller: track and quality changes reload a new generation; stale player events are ignored', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key});
  await ctl.start({itemId: 'movie-1'});
  await ctl.setAudio('a2');
  assert.equal(player.loads.length, 2);
  assert.equal(player.loads[1]!.generation, 2);
  await ctl.setQuality({mode: 'limit', maxHeight: 720});
  assert.equal(player.loads[2]!.generation, 3);
  // A late event from generation 1 must not move the observation.
  const before = ctl.getSnapshot().observation;
  player.generation = 1; player.emit('time'); player.generation = 3;
  assert.equal(ctl.getSnapshot().observation, before);
  ctl.dispose();
});

test('controller: seek is local for direct play and goes through the server once something converts', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key});
  const s = (await ctl.start({itemId: 'movie-1'}))!;
  assert.equal(seekNeedsServer(s), false);
  await ctl.seek(60_000);
  assert.deepEqual(player.seeks, [60_000]);
  await ctl.setQuality({mode: 'limit', maxHeight: 720});
  assert.equal(seekNeedsServer(ctl.getSnapshot().session!), true);
  const loads = player.loads.length;
  await ctl.seek(90_000);
  assert.equal(player.seeks.length, 1, 'no local seek');
  assert.equal(player.loads.length, loads + 1, 'the server re-produced from the new position');
  assert.equal(player.loads.at(-1)!.startPositionMs, 90_000);
  // Sidecar subtitles switch locally; others need the server.
  const sent = server.session(ctl.getSnapshot().session!.id)!.reports.length;
  ctl.setSubtitles('s4');
  await settle();
  assert.deepEqual(player.sidecar, ['s4']);
  await c.advance(1000);
  const reports = server.session(ctl.getSnapshot().session!.id)!.reports;
  assert.equal(reports.length, sent + 1, 'the local switch is in the next sample');
  assert.equal(reports.at(-1)!.subtitleTrackId, 's4');
  await ctl.setSubtitles('s3');
  assert.equal(player.loads.length, loads + 2);
  ctl.dispose();
});

test('controller: a 412 adopts the current session and re-applies only the newest intent', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key});
  const s = (await ctl.start({itemId: 'movie-1'}))!;
  server.bump(s.id, {audio: {trackId: 'a2'}}); // another controller changed it
  const updated = await ctl.setVersion('v1b');
  assert.ok(updated);
  const live = server.session(s.id)!;
  assert.equal(live.versionId, 'v1b');
  assert.deepEqual(live.audio, {trackId: 'a2'}, 'the other change is kept');
  assert.equal(ctl.getSnapshot().session!.revision, String(live.revision));
  // ETag is the quoted revision; If-Match accepts either form.
  const got = await server.handle({method: 'GET', path: `/v1/playback/sessions/${s.id}`});
  assert.equal(got.headers.ETag, `"${live.revision}"`);
  const quoted = await server.handle({method: 'PATCH', path: `/v1/playback/sessions/${s.id}`, headers: {'If-Match': got.headers.ETag!}, body: {state: 'paused'}});
  assert.equal(quoted.status, 200);
  await ctl.sessionUpdated(s.id);
  // Two changes queued behind a conflict: the older one is not re-sent.
  server.bump(s.id, {state: 'paused'});
  const older = ctl.setAudio('a1');
  const newer = ctl.setAudio('a2');
  await Promise.all([older, newer]);
  assert.deepEqual(server.session(s.id)!.audio, {trackId: 'a2'});
  ctl.dispose();
});

test('controller: the server ending the session (410, stop elsewhere) pauses and reports once', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const ended: string[] = [];
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key, onServerEnded: s => ended.push(s.id)});
  const s = (await ctl.start({itemId: 'movie-1'}))!;
  await server.handle({method: 'DELETE', path: `/v1/playback/sessions/${s.id}`, body: {}});
  assert.equal(await ctl.setAudio('a2'), undefined);
  await settle();
  assert.equal(ctl.getSnapshot().phase, 'ended');
  assert.equal(player.state, 'paused');
  assert.deepEqual(ended, [s.id]);
  ctl.dispose();
});

test('controller: stop flushes the position and ends the session (idempotent)', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key});
  const s = (await ctl.start({itemId: 'movie-1'}))!;
  player.positionMs = 42_000; player.emit('time');
  await ctl.stop();
  await ctl.stop();
  const live = server.session(s.id)!;
  assert.equal(live.ended, true);
  assert.equal(live.positionMs, 42_000);
  ctl.dispose();
});

// ── Queues (§8) ────────────────────────────────────────────────────
test('shuffle permutation is a bijection with a working inverse, sampled up to 2^20', () => {
  for (const n of [1, 2, 3, 17, 1000, 65_537]) {
    const p = new ShufflePermutation(n, 12345);
    const seen = new Set<number>();
    for (let i = 0; i < n; i++) { const v = p.at(i); assert.ok(v >= 0 && v < n); seen.add(v); assert.equal(p.indexOf(v), i); }
    assert.equal(seen.size, n);
  }
  const big = new ShufflePermutation(1 << 20, 7), seen = new Set<number>();
  for (let i = 0; i < 5000; i++) { const at = (i * 2654435761) % (1 << 20); const v = big.at(at); assert.equal(big.indexOf(v), at); assert.ok(!seen.has(v)); seen.add(v); }
  assert.notDeepEqual([0, 1, 2, 3, 4].map(i => new ShufflePermutation(1000, 1).at(i)), [0, 1, 2, 3, 4].map(i => new ShufflePermutation(1000, 2).at(i)));
});

test('queue: shuffled Play is stable for a seed, windows stay ≤ 200, and a 10M queue costs only what is read', async () => {
  const server = new FakePlaybackServer({containerSize: (_k, id) => (id === 'everything' ? 10_000_000 : 50)});
  const client = new QueueClient(fakeHttp(server), key);
  const a = await client.create([{source: {container: {kind: 'library', id: 'music'}}, order: {mode: 'shuffle', seed: '99'}}], {startPlayback: {}});
  assert.ok(a.session, 'Play and the first session are one request');
  assert.equal(a.session!.queue?.entryId, a.queue.current?.entryId);
  const order1 = (await client.entries(a.queue.id, 0, 50)).items.map(e => e.itemId);
  const b = await client.create([{source: {container: {kind: 'library', id: 'music'}}, order: {mode: 'shuffle', seed: '99'}}]);
  const order2 = (await client.entries(b.queue.id, 0, 50)).items.map(e => e.itemId);
  assert.deepEqual(order1, order2, 'same seed, same order');
  assert.equal(new Set(order1).size, 50);
  const big = await client.create([{source: {container: {kind: 'library', id: 'everything'}}, order: {mode: 'shuffle', seed: '5'}}]);
  assert.equal(big.queue.total, 10_000_000);
  const page = await client.entries(big.queue.id, 9_999_900, 500);
  assert.equal(page.items.length, 100);
  const w = await client.entries(big.queue.id, 0, 1000);
  assert.equal(w.items.length, QUEUE_WINDOW_MAX, 'the client never asks for more than 200');
  // The view is O(visible): a scroll deep into the queue holds a few pages, not the queue.
  const view = new QueueView(client, big.queue, {pageSize: 100, maxResidentPages: 4});
  view.entries.ensureRange(5_000_000, 5_000_030);
  await settle();
  assert.ok(view.entries.itemAt(5_000_010));
  for (const at of [100, 2_000_000, 7_000_000, 9_000_000, 9_999_000]) { view.entries.ensureRange(at, at + 20); await settle(); }
  assert.ok(view.entries.resident().items <= 4 * 100);
  view.dispose();
});

test('queue: play next, move and remove carry If-Match; a 412 adopts the current header', async () => {
  const server = new FakePlaybackServer();
  const client = new QueueClient(fakeHttp(server), key);
  const created = await client.create([{source: {items: {ids: ['t1', 't2', 't3', 't4']}}}]);
  const view = new QueueView(client, created.queue);
  const ids = async () => (await client.entries(view.header.id, 0, 50)).items.map(e => e.itemId);
  await view.run((id, rev) => client.addSegment(id, rev, 'next', {source: {items: {ids: ['x1']}}}));
  assert.deepEqual(await ids(), ['t1', 't2', 't3', 't4', 'x1'], 'the segment after the current one (one segment here)');
  const entries = (await client.entries(view.header.id, 0, 50)).items;
  await view.run((id, rev) => client.move(id, rev, entries[3]!.entryId, {before: entries[0]!.entryId}));
  assert.deepEqual(await ids(), ['t4', 't1', 't2', 't3', 'x1']);
  const now = (await client.entries(view.header.id, 0, 50)).items;
  await view.run((id, rev) => client.remove(id, rev, now[2]!.entryId));
  assert.deepEqual(await ids(), ['t4', 't1', 't3', 'x1']);
  // Another device changes the queue; our stale revision gets 412 and the view adopts the new header.
  const stale = view.header.revision;
  await client.update(view.header.id, view.header.revision, {repeat: 'all'});
  await assert.rejects(view.run((id) => client.update(id, stale, {repeat: 'one'})), (e: unknown) => e instanceof PlaybackApiError && e.status === 412);
  assert.equal(view.header.repeat, 'all');
  view.dispose();
});

test('queue: advance moves the current entry and starts the next session; repeat all wraps', async () => {
  const server = new FakePlaybackServer();
  const client = new QueueClient(fakeHttp(server), key);
  const created = await client.create([{source: {items: {ids: ['t1', 't2']}}}], {startPlayback: {}});
  let q = created.queue;
  const next = await client.advance(q.id, q.revision, 'completion');
  assert.equal(next.queue.current?.position, 1);
  assert.equal(next.session?.itemId, 't2');
  assert.equal(server.session(created.session!.id)!.ended, true);
  q = next.queue;
  await assert.rejects(client.advance(q.id, q.revision, 'completion'), (e: unknown) => e instanceof PlaybackApiError && e.code === 'queue_ended');
  q = (await client.update(q.id, q.revision, {repeat: 'all'})).queue;
  const wrapped = await client.advance(q.id, q.revision, 'completion');
  assert.equal(wrapped.queue.current?.position, 0);
});

// ── Events (§15) ───────────────────────────────────────────────────
test('events: one stream, prefix subscriptions, reconnect from the cursor after stream.closed, no duplicates', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now, setTimer: c.setTimer, clearTimer: c.clearTimer});
  const events = new EventsClient({http: fakeHttp(server), openStream: server.openStream, random: () => 0, setTimer: c.setTimer, clearTimer: c.clearTimer});
  const seen: string[] = [];
  events.on('queue.', e => seen.push(e.type + '#' + e.id));
  events.on('session.updated', e => seen.push(e.type + '#' + e.id));
  events.start();
  await settle();
  assert.equal(events.getStatus().mode, 'stream');
  server.publish('queue.updated', {kind: 'queue', id: 'q1'}, '2');
  server.publish('session.updated', {kind: 'session', id: 's1'}, '3');
  await settle();
  assert.deepEqual(seen, ['queue.updated#1', 'session.updated#2']);
  server.closeStreams(1000);
  await settle();
  server.publish('queue.updated', {kind: 'queue', id: 'q1'}, '4'); // while disconnected
  await c.advance(1000);
  assert.deepEqual(seen, ['queue.updated#1', 'session.updated#2', 'queue.updated#4'], 'resumed from the cursor; the closed marker is not an event');
  events.deliver({variant: 'change', id: '4', type: 'queue.updated'});
  assert.equal(seen.length, 3, 'deduplicated by id');
  events.stop();
});

test('events: an unsupported stream (404) falls back to long-poll; an old cursor resyncs', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now, setTimer: c.setTimer, clearTimer: c.clearTimer, ring: 5});
  server.refuseStreams(404);
  const events = new EventsClient({http: fakeHttp(server), openStream: server.openStream, waitMs: 25000, setTimer: c.setTimer, clearTimer: c.clearTimer, cursor: '0'});
  const seen: string[] = [];
  let resyncs = 0;
  events.on('admin.sessions', e => seen.push(e.id));
  events.onResync(() => resyncs++);
  for (let i = 0; i < 8; i++) server.publish('admin.sessions'); // the ring keeps only 5
  events.start();
  await settle();
  assert.equal(events.getStatus().mode, 'poll');
  assert.equal(resyncs, 1, 'the cursor fell out of the ring: refetch');
  server.publish('admin.sessions');
  await settle();
  assert.deepEqual(seen, ['9'], 'a parked long-poll answers as soon as an event arrives');
  await c.advance(25_000);
  events.stop();
});

// ── Admin Now Playing (§14) ────────────────────────────────────────
test('admin: Now Playing refreshes on events (coalesced) and terminate ends the viewer’s session', async () => {
  const server = new FakePlaybackServer();
  const http = fakeHttp(server);
  const sessions = new SessionsClient(http);
  const a = await sessions.start({itemId: 'movie-1'}, {}, key());
  await sessions.start({itemId: 'movie-2'}, {}, key());
  const client = new AdminSessionsClient(http, key);
  const store = new NowPlayingStore(client);
  await Promise.all([store.refresh(), store.refresh(), store.refresh()]);
  assert.equal(store.getSnapshot().sessions.length, 2);
  assert.equal(store.getSnapshot().sessions[0]!.decision.video?.action, 'copy');
  await store.terminate(a.id, 'Maintenance tonight');
  await store.refresh();
  assert.equal(store.getSnapshot().sessions.length, 1);
  assert.equal(server.session(a.id)!.ended, true);
  assert.equal(server.session(a.id)!.terminatedMessage, 'Maintenance tonight');
});

test('controller: an administrator’s terminate ends it with the reason and message, from the event or a re-read', async () => {
  for (const withPayload of [true, false]) {
    const c = clockwork();
    const server = new FakePlaybackServer({now: c.now});
    const player = new FakePlayer();
    const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key});
    const s = (await ctl.start({itemId: 'movie-1'}))!;
    await fakeHttp(server).send({method: 'POST', path: `/v1/admin/sessions/${s.id}:terminate`, body: {message: 'Maintenance tonight'}} as never);
    await ctl.sessionUpdated(s.id, undefined, withPayload ? {state: 'ended', reason: 'terminated', message: 'Maintenance tonight'} : undefined);
    await c.advance(0);
    assert.equal(ctl.getSnapshot().phase, 'ended');
    assert.deepEqual(ctl.getSnapshot().ended, {reason: 'terminated', message: 'Maintenance tonight'});
    ctl.dispose();
  }
});

// ── The controller handed sessions it did not start (queues), and a lapsed lease ──────────────

test('attach: a session another request made is taken over, loads at the viewer’s position, and is reported', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const sessions = new SessionsClient(fakeHttp(server));
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key, wantsPlaying: () => false});
  const first = await sessions.start({itemId: 'movie'}, {state: 'playing'}, key());
  ctl.attach(first, {startPositionMs: 42_000});
  assert.equal(player.loads.length, 1);
  assert.equal(player.loads[0]!.startPositionMs, 42_000, 'an explicit position wins over the presentation’s');
  assert.equal(player.loads[0]!.autoplay, false, 'the viewer’s intent decides, not the server’s default');
  player.state = 'playing'; player.positionMs = 43_000; player.emit('state');
  await settle();
  await c.advance(1000);
  assert.equal(server.session(first.id)!.reports.at(-1)!.positionMs, 43_000);
  // The same session again loads nothing; another session replaces it without stopping it here.
  ctl.attach(first);
  assert.equal(player.loads.length, 1);
  const second = await sessions.start({itemId: 'other'}, {state: 'playing', replacesSessionId: first.id}, key());
  ctl.attach(second, {autoplay: true});
  assert.equal(player.loads.length, 2);
  assert.equal(ctl.getSnapshot().session!.id, second.id);
  const before = server.session(first.id)!.reports.length;
  player.emit('time');
  await c.advance(11_000);
  assert.equal(server.session(first.id)!.reports.length, before, 'nothing more is reported for the session let go');
  // detach lets go without ending it on the server.
  ctl.detach();
  assert.equal(ctl.getSnapshot().phase, 'idle');
  assert.equal(server.session(second.id)!.ended, false);
  ctl.dispose();
});

test('a lapsed lease is given back to the owner with the last position; an administrator’s stop is not', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now, leaseMs: 120_000});
  const player = new FakePlayer();
  const lapsed: {id: string; at: number}[] = [], ended: string[] = [];
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key, onLeaseExpired: (s, at) => lapsed.push({id: s.id, at}), onServerEnded: (s, end) => ended.push(`${s.id}:${end.reason}`)});
  const s = await ctl.start({itemId: 'movie'}, {});
  player.positionMs = 600_000; player.emit('time');
  await settle();
  // The laptop slept: no report failed, so nothing here knew the lease was at risk. The server says it lapsed.
  await ctl.sessionUpdated(s!.id, 'later', {state: 'ended', reason: 'lease_expired'});
  await settle();
  assert.deepEqual(lapsed, [{id: s!.id, at: 600_000}]);
  assert.deepEqual(ended, [], 'the viewer is not shown an end');
  assert.notEqual(ctl.getSnapshot().phase, 'ended');
  // An administrator's stop is final.
  const d = clockwork();
  const server2 = new FakePlaybackServer({now: d.now});
  const ctl2 = new PlaybackSessionController({http: fakeHttp(server2), player: new FakePlayer(), timers: d, key, onLeaseExpired: (x, at) => lapsed.push({id: x.id, at}), onServerEnded: (x, end) => ended.push(`${x.id}:${end.reason}`)});
  const t = await ctl2.start({itemId: 'movie'}, {});
  await ctl2.sessionUpdated(t!.id, 'later', {state: 'ended', reason: 'terminated', message: 'Maintenance'});
  await settle();
  assert.equal(lapsed.length, 1);
  assert.deepEqual(ended, [`${t!.id}:terminated`]);
  assert.equal(ctl2.getSnapshot().phase, 'ended');
  ctl.dispose(); ctl2.dispose();
});

test('readings from an engine outside the port are reported, and the end is confirmed before anything moves on', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, timers: c, key});
  const s = await new SessionsClient(fakeHttp(server)).start({itemId: 'song-1'}, {state: 'playing'}, key());
  ctl.attach(s, {autoplay: false});
  ctl.observe({state: 'playing', positionMs: 12_345, rate: 1.25}, 'state');
  await settle();
  await c.advance(1000);
  const report = server.session(s.id)!.reports.at(-1)!;
  assert.deepEqual({state: report.state, positionMs: report.positionMs, rate: report.rate, generation: report.generation}, {state: 'playing', positionMs: 12_345, rate: 1.25, generation: s.presentation.generation});
  ctl.noteSkip({markerId: 'm1', mode: 'manual', positionMs: 13_000});
  await c.advance(1000); await settle(); await c.advance(1000); await settle();
  assert.ok(server.session(s.id)!.reports.some(r => JSON.stringify(r.skipped) === JSON.stringify({markerId: 'm1', mode: 'manual', positionMs: 13_000})), 'the skip rode a sample');
  // The end cannot be confirmed while the server cannot be reached…
  server.failNext('offline');
  ctl.observe({state: 'ended', positionMs: 180_000}, 'state');
  await assert.rejects(ctl.confirmEnded(), /timeline_unconfirmed/);
  // …and is once the report is through.
  await c.advance(1500);
  await ctl.confirmEnded();
  assert.equal(server.session(s.id)!.reports.at(-1)!.state, 'ended');
  ctl.dispose();
});

test('a re-plan starts the new presentation where the picture is now, and a local seek stays local when the platform says so', async () => {
  const c = clockwork();
  const server = new FakePlaybackServer({now: c.now});
  const player = new FakePlayer();
  const requests: string[] = [];
  const inner = fakeHttp(server);
  const ctl = new PlaybackSessionController({http: {send: r => { requests.push(`${r.method} ${r.path}`); return inner.send(r as never) as never; }}, player, timers: c, key, serverSeek: () => false});
  const s = await ctl.start({itemId: 'movie'}, {});
  player.positionMs = 50_000; player.emit('time');
  await settle();
  const changing = ctl.setQuality({mode: 'limit', maxHeight: 720});
  // The old stream keeps playing while the server prepares the new one.
  player.positionMs = 53_000;
  await changing;
  assert.equal(player.loads.at(-1)!.generation, s!.presentation.generation + 1);
  assert.equal(player.loads.at(-1)!.startPositionMs, 53_000);
  // The session is now converted; this platform still seeks locally (the server follows segment requests).
  const patches = requests.filter(r => r.startsWith('PATCH')).length;
  ctl.seek(90_000);
  assert.deepEqual(player.seeks, [90_000]);
  assert.equal(requests.filter(r => r.startsWith('PATCH')).length, patches);
  ctl.dispose();
});


test('batch queue uses the committed route, revision and whole mixed selection',async()=>{
 const sent: unknown[]=[];
 const client=new QueueClient({async send(r){sent.push(r);return {status:200,headers:{},body:{queue:{id:'queue',revision:'2',total:3,remainingCount:2,totalDurationMs:3000,durationKnown:true,repeat:'off',segments:[]},window:[]}};}});
 const source={items:{ids:['film','song']}} as const;
 const result=await client.batch('queue','1',source,'next');
 assert.deepEqual(sent,[{method:'POST',path:'/v1/queues/queue/batch',headers:{'If-Match':'1'},body:{placement:'next',source},signal:undefined}]);
 assert.equal(result.queue.totalDurationMs,3000);assert.equal(result.queue.remainingCount,2);
});

test('playback options retain verified alternatives and collection successor facts',async()=>{
 const {parseOptions}=await import('../src/playback-v1/types.ts');
 const out=parseOptions({itemId:'film',kind:'movie',versions:[],chapters:[],markers:[],qualities:[],alternatives:[{versionId:'v1',label:'1080p-low'}],collection:{id:'collection',title:'Films',next:{id:'next',title:'Next film',year:2026,genres:['Drama'],backdropUrl:'/v1/items/next/art/backdrop'}}});
 assert.deepEqual(out.alternatives,[{versionId:'v1',label:'1080p-low'}]);assert.equal(out.collection?.next.id,'next');assert.equal(out.collection?.next.backdropUrl,'/v1/items/next/art/backdrop');
 const error=new PlaybackApiError(409,'stream_limit_reached','after_refresh',{scope:'account',limit:2,playing:2});assert.deepEqual(error.streamLimit,{scope:'account',limit:2,playing:2});
 assert.equal(new PlaybackApiError(409,'stream_limit_reached','after_refresh',{scope:'account',limit:-1,playing:2}).streamLimit,undefined);
});


test('controller scoped changes reject abort-ignoring replies before the media generation loads',async()=>{
 for(const retire of ['abort','host','play'] as const){
  const c=clockwork(),server=new FakePlaybackServer({now:c.now}),inner=fakeHttp(server),player=new FakePlayer();
  let hold=false,entered!:()=>void,release!:()=>void,signal:AbortSignal|undefined;
  const reached=new Promise<void>(resolve=>{entered=resolve}),gate=new Promise<void>(resolve=>{release=resolve});
  const ctl=new PlaybackSessionController({http:{send:async request=>{const response=await inner.send(request as never);if(hold&&request.method==='PATCH'){signal=request.signal;entered();await gate;}return response as never;}},player,timers:c,key});
  try{await ctl.start({itemId:'movie'});const abort=new AbortController();let current=true;hold=true;
   const pending=ctl.change({subtitles:{trackId:'s1',delivery:'burn'}},{recoverEnded:false,signal:abort.signal,isCurrent:()=>current});const rejected=assert.rejects(pending,{name:'AbortError'});await reached;
   if(retire==='abort')abort.abort();else if(retire==='host')current=false;else await ctl.start({itemId:'other'});
   if(retire==='abort')assert.equal(signal!.aborted,true);const loads=player.loads.length,shown=ctl.getSnapshot().session;
   release();await rejected;assert.equal(player.loads.length,loads);assert.equal(ctl.getSnapshot().session,shown);assert.equal(ctl.getSnapshot().changing,false,retire);
  }finally{ctl.dispose();}
 }
});

test('controller strict ended refusal never invokes lease recovery or end-driven queue replacement',async()=>{
 for(const status of [404,410]){
  const c=clockwork(),server=new FakePlaybackServer({now:c.now}),inner=fakeHttp(server),player=new FakePlayer();let recovering=0,ended=0,refuse=false;
  const ctl=new PlaybackSessionController({http:{send:async request=>refuse&&request.method==='PATCH'?{status,headers:{},body:{error:{code:'session_ended',retry:'never'}}}:await inner.send(request as never) as never},player,timers:c,key,onLeaseExpired:()=>{recovering++;},onServerEnded:()=>{ended++;}});
  try{await ctl.start({itemId:'movie'});refuse=true;
   await assert.rejects(ctl.change({subtitles:{trackId:'s1',delivery:'burn'}},{recoverEnded:false}),error=>error instanceof PlaybackApiError&&error.status===status);
   await settle();assert.equal(recovering,0);assert.equal(ended,0);assert.equal(player.loads.length,1);assert.equal(ctl.getSnapshot().phase,'active');assert.equal(ctl.getSnapshot().changing,false);
  }finally{ctl.dispose();}
 }
});

test('a canceled controller change queued behind another command never sends its PATCH',async()=>{
 const c=clockwork(),server=new FakePlaybackServer({now:c.now}),inner=fakeHttp(server),player=new FakePlayer();let hold=false,patches=0,entered!:()=>void,release!:()=>void;
 const reached=new Promise<void>(resolve=>{entered=resolve}),gate=new Promise<void>(resolve=>{release=resolve});
 const ctl=new PlaybackSessionController({http:{send:async request=>{const response=await inner.send(request as never);if(request.method==='PATCH'){patches++;if(hold){hold=false;entered();await gate;}}return response as never;}},player,timers:c,key});
 try{await ctl.start({itemId:'movie'});hold=true;const first=ctl.setAudio('a1');await reached;
  const abort=new AbortController(),queued=ctl.change({subtitles:{trackId:'s1',delivery:'burn'}},{recoverEnded:false,signal:abort.signal});const rejected=assert.rejects(queued,{name:'AbortError'});abort.abort();release();await first;await rejected;
  assert.equal(patches,1);assert.equal(player.loads.length,2);assert.equal(ctl.getSnapshot().changing,false);
 }finally{ctl.dispose();}
});

test('a retired controller change cannot adopt a 412 recovery presentation or reapply its intent',async()=>{
 const c=clockwork(),server=new FakePlaybackServer({now:c.now}),inner=fakeHttp(server),player=new FakePlayer();let current=true,entered!:()=>void,release!:()=>void,patches=0;
 const reached=new Promise<void>(resolve=>{entered=resolve}),gate=new Promise<void>(resolve=>{release=resolve});
 const ctl=new PlaybackSessionController({http:{send:async request=>{const response=await inner.send(request as never);if(request.method==='PATCH'){patches++;if(response.status===412){entered();await gate;}}return response as never;}},player,timers:c,key});
 try{const session=(await ctl.start({itemId:'movie'}))!;server.bump(session.id,{audio:{trackId:'a2'}});
  const pending=ctl.change({subtitles:{trackId:'s1',delivery:'burn'}},{recoverEnded:false,isCurrent:()=>current});const rejected=assert.rejects(pending,{name:'AbortError'});await reached;current=false;release();await rejected;
  assert.equal(patches,1);assert.equal(player.loads.length,1);assert.equal(ctl.getSnapshot().session,session);
 }finally{ctl.dispose();}
});

test('WEB-03: reloading media for the same session preserves timeline sequence and position',async()=>{
 const c=clockwork(),server=new FakePlaybackServer({now:c.now}),player=new FakePlayer();
 const ctl=new PlaybackSessionController({http:fakeHttp(server),player,timers:c,key});
 try{
  const session=(await ctl.start({itemId:'song'}))!;
  player.positionMs=42000;player.emit('state');await settle();await c.advance(11000);
  const before=server.session(session.id)!.reports.at(-1)!;assert.ok(Number(before.seq)>=2);
  ctl.reloadMedia({autoplay:false});assert.equal(player.loads.length,2);assert.equal(player.loads[1].generation,session.presentation.generation);assert.equal(player.loads[1].startPositionMs,42000);assert.equal(player.loads[1].autoplay,false);
  player.emit('state');await settle();await c.advance(1000);await settle();await c.advance(1000);await settle();const after=server.session(session.id)!.reports.at(-1)!;
  assert.ok(Number(after.seq)>Number(before.seq));assert.equal(after.epoch,before.epoch,'the same observer epoch');assert.equal(after.positionMs,42000);assert.equal(after.state,'paused');assert.equal(server.session(session.id)!.observedSeq,after.seq);
  ctl.detach();ctl.reloadMedia();assert.equal(player.loads.length,2,'a detached session cannot be reloaded');
 }finally{ctl.dispose();}
});

test('resync strict current-session reads retain transient failures and bypass an unchanged row revision', async () => {
  const server = new FakePlaybackServer(), inner = fakeHttp(server), c = clockwork(), player = new FakePlayer();
  let fail = false, reads = 0;
  const http = {send: async (request: Parameters<typeof inner.send>[0]) => {
    if (request.method === 'GET' && request.path.startsWith('/v1/playback/sessions/')) {reads++; if (fail) throw new Error('unavailable');}
    return inner.send(request);
  }};
  const controller = new PlaybackSessionController({http, player, timers: c, key});
  try {
    const session = await controller.start({itemId: 'movie-1'});
    const before = player.loads.length;
    fail = true;
    await assert.rejects(controller.sessionUpdated(session.id, undefined, undefined, {strict: true}), /unavailable/);
    fail = false;
    await controller.sessionUpdated(session.id, undefined, undefined, {strict: true});
    assert.equal(reads, 2); assert.equal(player.loads.length, before, 'same projection never reloads the engine');
  } finally {controller.dispose();}
});
