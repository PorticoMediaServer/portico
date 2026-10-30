/** The native apps' one playback path: the shared playback service and session controller over a
 * player engine that JavaScript only commands and hears from (`src-next/platform/playback/native-playback.ts`).
 * These run the real service, queue player, session controller and coordinator against the shared
 * fake server and an engine that follows ENGINE-CONTRACT.md as far as these rules go. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../../../packages/client-core/src/index.ts';
import {FakePlaybackServer, fakeHttp, type FakeServerOptions} from '../../../packages/client-core/src/playback-v1/testing/fake-server.ts';
import {V1QueuePlayer} from '../../../packages/client-core/src/playback-v1/v1-queue-player.ts';
import {queueItemInput} from '../../../packages/client-core/src/queue-controller.ts';
import {NativePlaybackCoordinator} from '../src-next/platform/playback/NativePlaybackCoordinator.ts';
import {NativePlayback, automaticSteps} from '../src-next/platform/playback/native-playback.ts';
import {planEngineOf} from '../src-next/platform/playback/engine-media.ts';
import {PlaybackOptionsClient} from '../../../packages/client-core/src/playback-v1/options.ts';

const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
const settle = async (n = 40) => { for (let i = 0; i < n; i++) await new Promise(r => setImmediate(r)); };

class FakeEngine {
  commands: Record<string, any>[] = [];
  private listener?: (f: any) => void;
  private seq = 0;
  private seekRevision = 0;
  private loaded = '';
  private last?: Record<string, any>;
  position = 0;
  async acquire() { return {lease: 'lease-1', instanceId: 'inst-1'}; }
  async release() { return true; }
  subscribe(l: (f: any) => void) { this.listener = l; return () => { this.listener = undefined; }; }
  async command(c: Record<string, any>) {
    this.commands.push(c);
    this.last = c;
    queueMicrotask(() => this.apply(c));
    return true;
  }
  fact(type: string, extra: Record<string, unknown> = {}) {
    const c = this.last!;
    this.listener?.({type, instanceId: 'inst-1', binding: c.binding, intentId: c.binding.intentId, factSequence: ++this.seq, lastCommandRevision: c.commandRevision, position: this.position, paused: c.paused, ...extra});
  }
  private apply(c: Record<string, any>) {
    const key = JSON.stringify(c.binding) + c.source;
    if (key !== this.loaded) { this.loaded = key; this.position = 0; this.fact('ready'); }
    const pending = c.pendingSeek;
    if (pending && pending.revision > this.seekRevision) {
      this.seekRevision = pending.revision;
      this.position = pending.positionSeconds;
      this.fact('seekApplied', {seekRevision: pending.revision});
      return;
    }
    if (!c.paused && !pending) { this.position += 1; this.fact('progress'); }
  }
}

function device(options: FakeServerOptions = {}) {
  const server = new FakePlaybackServer(options);
  const inner = fakeHttp(server);
  const sent: string[] = [];
  const http: typeof inner = {send: request => { sent.push(`${request.method} ${request.path}`); return inner.send(request); }};
  let serial = 0;
  const path = new NativePlayback({http, timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}-key-0000000000`});
  const playback = new PlaybackService(path.transport, () => `r${++serial}`, {queueRequired: true, deferRecovery: true});
  const player = new V1QueuePlayer({http, api: path.transport, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10});
  const unbind = path.bind(playback, () => player);
  const stop = player.connect();
  const engine = new FakeEngine();
  const errors: string[] = [];
  // usePlayer's fact handler, the parts these rules reach.
  const accept = (f: any) => {
    const s = playback.getSnapshot();
    if (f.intentId !== s.intentId) return;
    path.engineFact(f);
    if (f.type === 'seekApplied') { playback.seekApplied(f.intentId, f.seekRevision, f.position, f.paused ? 'paused' : 'playing'); return; }
    if (f.type === 'ready') playback.ready(f.intentId);
    else if (f.type === 'error') playback.engineFailed(f.intentId, 'decode_error', String(f.errorDetail ?? ''), 'This cannot be played here.');
    else playback.fact(f.intentId, f.position, f.type === 'ended' ? 'ended' : f.paused ? 'paused' : 'playing');
  };
  const coordinator = new NativePlaybackCoordinator(playback, engine, () => `owner-${++serial}`, s => (s.session ? 'https://portico.test' + s.session.streamUrl : ''), accept);
  coordinator.start();
  const close = () => { coordinator.stop(); stop(); unbind(); player.dispose(); path.dispose(); };
  return {server, playback, player, path, engine, errors, sent, close};
}

test('a playing title reports observations through the session, with the generation and the engine\'s position', async () => {
  const d = device();
  try {
    await d.playback.play('movie-1', 30);
    await settle();
    const s = d.playback.getSnapshot();
    assert.equal(s.error, undefined, s.error);
    assert.equal(s.phase, 'ready');
    // These timers never fire: a flush sends the latest reading now.
    await d.path.controller.flush();
    const session = d.server.session(s.session!.id)!;
    assert.ok(session.reports.length >= 1, 'the reading is reported');
    const first = session.reports[0]!;
    assert.equal(first.generation, s.session!.generation);
    assert.ok(Number(first.positionMs) >= 30_000, `position ${first.positionMs}`);
    assert.equal(first.seq, 1);
    assert.equal(d.path.transport.handOverObserver(s.session!.id), first.epoch, 'a native owner taking the session over claims with this epoch');
  } finally { d.close(); }
});

test('the session the service plays is the one the server described: its engine and the quality in force', async () => {
  const d = device({engine: 'mpv'});
  try {
    await d.playback.play('movie-1', 0);
    await settle();
    const session = d.playback.getSnapshot().session!;
    const v1 = d.path.session(session.id)!;
    assert.equal(v1.presentation.engine, 'mpv');
    assert.deepEqual(planEngineOf(session, v1), {engine: 'mpv', stale: false});
    assert.equal(v1.presentation.quality.mode, 'original', 'the default quality is the original');
    assert.deepEqual(planEngineOf(session, {...v1, presentation: {...v1.presentation, generation: session.generation + 1}}), {stale: true}, 'a re-plan on its way keeps the engine in use');
    assert.deepEqual(planEngineOf(session, undefined), {stale: false});
  } finally { d.close(); }
});

test('a quality change re-plans the playing session in place: the same session, the next generation, the same queue', async () => {
  const d = device();
  try {
    await d.player.playEntries(['ep-1', 'ep-2', 'ep-3'].map(queueItemInput), {startSeconds: 0});
    await settle();
    const before = d.playback.getSnapshot();
    const queueBefore = d.player.getSnapshot().view!.queue;
    d.engine.position = 61;
    d.engine.fact('progress');
    await settle();
    await d.playback.changePlayback({quality: {mode: 'fixed', id: '720p-high'}});
    await settle();
    const after = d.playback.getSnapshot();
    assert.equal(after.session!.id, before.session!.id, 'the same session');
    assert.equal(after.session!.generation, before.session!.generation + 1, 'its next presentation');
    assert.notEqual(after.session!.streamUrl, before.session!.streamUrl);
    assert.equal(after.error, undefined, after.error);
    const queueAfter = d.player.getSnapshot().view!.queue;
    assert.equal(queueAfter.id, queueBefore.id, 'the same queue');
    assert.equal(queueAfter.currentEntryId, queueBefore.currentEntryId, 'at the same entry');
    assert.deepEqual(queueAfter.entries.map(e => (e.hidden ? '' : e.itemId)), ['ep-1', 'ep-2', 'ep-3'], 'Up Next is untouched');
    assert.deepEqual(d.path.session(after.session!.id)!.presentation.quality, {mode: 'fixed', id: '720p-high'}, 'the menu ticks what the server put in force');
    const reload = d.engine.commands.find(c => c.binding.generation === after.session!.generation)!;
    assert.ok(reload.pendingSeek?.positionSeconds >= 61, `the engine opens the new stream where the picture was (${reload.pendingSeek?.positionSeconds})`);
    assert.equal(d.server.session(after.session!.id)!.ended, false);
  } finally { d.close(); }
});

test('an audio track and a subtitle track are changes to the same session too', async () => {
  const d = device();
  try {
    await d.player.playEntries(['ep-1', 'ep-2'].map(queueItemInput), {startSeconds: 0});
    await settle();
    const first = d.playback.getSnapshot().session!;
    await d.player.setAudioTrack('a2');
    await settle();
    const withAudio = d.playback.getSnapshot().session!;
    assert.equal(withAudio.id, first.id);
    assert.equal(d.path.session(first.id)!.presentation.audioTrackId, 'a2', 'the Audio menu ticks the track the server is playing');
    await d.player.setSubtitleTrack(null);
    await settle();
    assert.equal(d.playback.getSnapshot().session!.id, first.id);
    assert.equal(d.player.getSnapshot().view!.queue.entries.length, 2);
  } finally { d.close(); }
});

test('an engine that cannot play what it was given is answered in place, and the queue keeps its place', async () => {
  const d = device();
  try {
    await d.player.playEntries(['ep-1', 'ep-2'].map(queueItemInput), {startSeconds: 0});
    await settle();
    const before = d.playback.getSnapshot().session!;
    d.engine.fact('error', {error: 'decode', engineCode: 'decode_error', errorDetail: 'hevc main10'});
    await settle();
    const after = d.playback.getSnapshot();
    assert.equal(after.session!.id, before.id, 'the same session, another way');
    assert.equal(after.session!.generation, before.generation + 1);
    assert.notEqual(after.phase, 'error');
    assert.equal(d.server.session(before.id)!.failures, 1, 'the server was told, once');
    assert.equal(d.player.getSnapshot().view!.queue.entries.length, 2);
  } finally { d.close(); }
});

test('the end of a title is confirmed to the server and the session is stopped with its last position', async () => {
  const d = device();
  try {
    await d.playback.play('movie-1', 0);
    await settle();
    const id = d.playback.getSnapshot().session!.id;
    d.engine.position = 5400;
    d.engine.fact('progress');
    d.engine.fact('ended');
    await settle();
    const session = d.server.session(id)!;
    assert.equal(session.reports.at(-1)!.state, 'ended', 'the server has the end');
    assert.equal(session.reports.at(-1)!.positionMs, 5_400_000);
  } finally { d.close(); }
});

test('leaving the player ends the session on the server where the picture was', async () => {
  const d = device();
  try {
    await d.playback.play('movie-1', 0);
    await settle();
    const id = d.playback.getSnapshot().session!.id;
    d.engine.position = 95;
    d.engine.fact('progress');
    await settle();
    d.playback.leave();
    await settle();
    const session = d.server.session(id)!;
    assert.equal(session.ended, true);
    assert.equal(session.positionMs, 95_000);
    assert.equal(d.path.session(id), undefined, 'nothing is kept of a session that ended');
  } finally { d.close(); }
});

test('a session the server ended from elsewhere stops at once, with its reason', async () => {
  const d = device();
  try {
    await d.playback.play('movie-1', 0);
    await settle();
    const id = d.playback.getSnapshot().session!.id;
    d.path.transport.sessionUpdated(id, '99', {state: 'ended', reason: 'terminated', message: 'Bedtime.'});
    await settle();
    const s = d.playback.getSnapshot();
    assert.equal(s.phase, 'error');
    assert.deepEqual(s.ended, {reason: 'terminated', message: 'Bedtime.'});
  } finally { d.close(); }
});

test('a session update that is only this device\'s own report asks the server nothing', async () => {
  const d = device();
  try {
    await d.playback.play('movie-1', 0);
    await settle();
    const session = d.playback.getSnapshot().session!;
    const requests = d.sent.length;
    d.path.transport.sessionUpdated(session.id, '7', {state: 'playing', generation: session.generation});
    await settle();
    assert.equal(d.sent.length, requests);
  } finally { d.close(); }
});

test('Automatic steps down when the engine measures a connection that cannot carry the picture', async () => {
  let now = 0;
  const server = new FakePlaybackServer();
  const inner = fakeHttp(server), changes: unknown[] = [];
  const http: typeof inner = {send: request => {
    if (request.method === 'PATCH' && request.body && typeof request.body === 'object' && 'quality' in request.body) changes.push(request.body.quality);
    return inner.send(request);
  }};
  let serial = 0;
  const path = new NativePlayback({http, timers: {now: () => now, setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}-key-0000000000`});
  const playback = new PlaybackService(path.transport, () => `r${++serial}`, {queueRequired: true, deferRecovery: true});
  const player = new V1QueuePlayer({http, api: path.transport, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10});
  const unbind = path.bind(playback, () => player);
  const stop = player.connect();
  const engine = new FakeEngine();
  const notices: string[] = [];
  path.onNotice(n => notices.push(`${n.type}:${n.step?.id ?? 'original'}`));
  const accept = (f: any) => {
    if (f.intentId !== playback.getSnapshot().intentId) return;
    path.engineFact(f);
    if (f.type === 'seekApplied') { playback.seekApplied(f.intentId, f.seekRevision, f.position, 'playing'); return; }
    if (f.type === 'ready') playback.ready(f.intentId); else playback.fact(f.intentId, f.position, 'playing');
  };
  const coordinator = new NativePlaybackCoordinator(playback, engine, () => `owner-${++serial}`, s => (s.session ? 'https://portico.test' + s.session.streamUrl : ''), accept);
  coordinator.start();
  try {
    await playback.play('movie-1', 0);
    await settle();
    const id = playback.getSnapshot().session!.id;
    await playback.changePlayback({quality: {mode: 'automatic'}});
    await settle();
    const offered = await new PlaybackOptionsClient(http).options('movie-1');
    const steps = automaticSteps(offered.qualities.filter(option => option.id === '1080p-low' || option.id === '720p-low'));
    assert.deepEqual(steps.map(s => s.id), ['1080p-low', '720p-low']);
    path.setAutomatic({steps, originalKbps: offered.versions[0]!.bitrateKbps});
    // Twelve seconds at 3 Mb/s against the actual offered original.
    for (let i = 0; i < 13; i++) { now += 1000; engine.position += 1; engine.fact('progress', {throughputKbps: 3000}); await settle(4); }
    await settle();
    assert.deepEqual(notices, ['qualityLowered:720p-low'], 'one change, to what the connection carries with room to spare');
    assert.deepEqual(changes.at(-1), {mode: 'automatic', id: '720p-low'}, 'the exact canonical offered target reaches the real session PATCH');
    assert.equal(playback.getSnapshot().session!.id, id, 'the same session');
    assert.deepEqual(path.session(id)!.presentation.quality, {mode: 'automatic', id: '720p-low'});
  } finally { coordinator.stop(); stop(); unbind(); player.dispose(); path.dispose(); }
});
