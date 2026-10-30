/**
 * Plan §9.3 C3: on Apple a v1 audio session is handed to the native listening owner, which reports
 * its timeline and moves the queue while JS sleeps. These tests stand a small reference owner in
 * for PorticoListeningOwner (it speaks the same wire to the fake server) and check the JS side of the
 * contract: one handover with the seq reached so far, no JS timeline or stop for a native session,
 * the viewer's intents and queue moves go to the owner, and the owner's moves are adopted.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import {EventsClient} from '../src/playback-v1/events.ts';
import {QueueClient} from '../src/playback-v1/queue.ts';
import {PlaybackApiError} from '../src/playback-v1/http.ts';
import {queueItemInput} from '../src/queue-controller.ts';
import type {V1Http} from '../src/playback-v1/http.ts';
import type {Session} from '../src/playback-v1/types.ts';
import type {ListeningAction, ListeningDeadlines, NativeListeningPolicy, NativeListeningV1Context, NativeListeningV1Control, NativeListeningV1Event, NativeQueueAdvance} from '../src/playback/native-listening.ts';

const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
const settle = () => new Promise(r => setTimeout(r, 20));

/** What PorticoListeningOwner does, reduced to the wire and the snapshot. */
class ReferenceOwner implements NativeListeningV1Control {
  contexts: NativeListeningV1Context[] = [];
  actions: {action: ListeningAction; value?: number}[] = [];
  advances: {reason: string; entryId?: string}[] = [];
  detached: boolean[] = [];
  updates: string[] = [];
  private sink?: (e: NativeListeningV1Event) => void;
  private sequence = 0;
  private root?: string;
  session?: Session;
  queue: {id: string; revision: string} | null = null;
  seq = 0;
  intent: 'playing' | 'paused' = 'playing';
  asleep = false;
  private held: NativeListeningV1Event[] = [];
  private readonly http: V1Http;
  private readonly client: QueueClient;
  constructor(http: V1Http, client: QueueClient) { this.http = http; this.client = client; }
  async adopt(context: NativeListeningV1Context, sink: (e: NativeListeningV1Event) => void) {
    this.contexts.push(context);
    this.sink = sink;
    this.session = context.session;
    this.queue = context.queueId && context.queueRevision ? {id: context.queueId, revision: context.queueRevision} : null;
    this.seq = context.lastSeq;
    this.epoch = undefined;
    this.intent = context.intent;
    this.root = undefined;
    await this.report('playing');
    this.emit();
  }
  epoch?: string;
  /** The owner's observation: it claims the session (naming the epoch JS handed over), then reports. */
  async report(state: 'playing' | 'paused' | 'ended', positionMs = 0) {
    const path = `/v1/playback/sessions/${this.session!.id}`;
    if (!this.epoch) {
      const replaces = this.contexts.at(-1)?.observerEpoch;
      const grant = await this.http.send({method: 'POST', path: `${path}/observer`, body: {generation: this.session!.presentation.generation, ...(replaces ? {replaces} : {})}});
      this.epoch = (grant.body as {epoch?: string} | undefined)?.epoch;
      this.observed = 0;
    }
    ++this.seq;
    await this.http.send({method: 'PUT', path: `${path}/observation`, body: {epoch: this.epoch, seq: ++this.observed, generation: this.session!.presentation.generation, state, positionMs, rate: 1, monotonicMs: this.observed * 1000, playClockMs: this.observed * 1000, seekSerial: 0}});
  }
  private observed = 0;
  emit(extra: Partial<NativeListeningV1Event> = {}) {
    const event = {
      owner: 'owner-1', sequence: ++this.sequence, intent: this.intent, rate: 1, positionSeconds: 1, observed: true, ready: true, seeking: false, ended: false,
      error: null, terminal: false, route: 'Speaker', deadlines: {sleepAtMs: null, passoutAtMs: null}, commandRevision: 1, binding: null,
      session: this.session!, durationSeconds: 180, queue: this.queue, lastSeq: this.seq,
      ...(this.root ? {transition: {previousSessionId: this.root, queue: null}} : {}), ...extra,
    } as NativeListeningV1Event;
    if (this.asleep) this.held.push(event); else this.sink?.(event);
  }
  /** JS wakes: the queued snapshots arrive in order. */
  wake() { this.asleep = false; for (const e of this.held.splice(0)) this.sink?.(e); this.root = undefined; }
  /** A completion (or any move) the owner makes itself. */
  async move(reason: 'completion' | NativeQueueAdvance, entryId?: string) {
    const q = this.queue!;
    const r = await this.client.advance(q.id, q.revision, reason, entryId);
    this.root ??= this.session!.id;
    this.session = r.session!;
    this.queue = {id: r.queue.id, revision: r.queue.revision};
    this.seq = 0;
    await this.report('playing');
    this.emit({transition: {previousSessionId: this.root, queue: r.queue}});
  }
  /**
   * A gapless edge as PorticoListeningSessionOwner makes it (spec §18.2–18.3): prepare near the end,
   * commit at the boundary (a lost answer is asked again: the commit is idempotent by token), and on
   * any refusal finish the track and `:advance {reason: "completion"}`. One try per track.
   */
  async edge(): Promise<'committed' | 'fallback'> {
    const q = this.queue!, current = this.session!;
    let token: string;
    try {
      token = (await this.client.prepareNext(q.id, q.revision, {sessionId: current.id, sessionGeneration: current.presentation.generation})).token;
    } catch { return this.fallback(); }
    for (let tries = 0; ; tries++) {
      try {
        const r = await this.client.commitNext(q.id, token);
        this.root ??= current.id;
        this.session = r.session;
        this.queue = {id: r.queue.id, revision: r.queue.revision};
        this.seq = 0;
        await this.report('playing');
        this.emit({transition: {previousSessionId: this.root, queue: r.queue}});
        return 'committed';
      } catch (e) {
        if (e instanceof PlaybackApiError || tries >= 3) return this.fallback();
      }
    }
  }
  private async fallback(): Promise<'fallback'> {
    await this.report('ended', 180_000);
    await this.move('completion');
    return 'fallback';
  }
  async action(action: ListeningAction, value?: number) { this.actions.push({action, value}); if (action === 'pause') { this.intent = 'paused'; await this.report('paused'); this.emit(); } }
  async advance(reason: NativeQueueAdvance, entryId?: string) { this.advances.push({reason, entryId}); await this.move(reason, entryId); }
  async refresh() {}
  async deadlines(_value: ListeningDeadlines) {}
  async detach(stop: boolean) { this.detached.push(stop); }
  async sessionUpdated(id: string) { this.updates.push(id); }
}

function setup(options: {nativeFor?: (kind: string) => boolean; refuse?: boolean; answered?: (r: {method: string; path: string}) => void} = {}) {
  const server = new FakePlaybackServer();
  const inner = fakeHttp(server);
  const log: {method: string; path: string}[] = [];
  const http: V1Http = {send: async r => { log.push({method: r.method, path: r.path}); const answer = await inner.send(r as never); options.answered?.(r); return answer as never; }};
  const ownerHttp: V1Http = {send: r => inner.send(r as never) as never};
  let serial = 0;
  const api = new V1PlaybackApi(http, {timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const playback = new PlaybackService(api, () => `r${++serial}`, {});
  const owner = new ReferenceOwner(ownerHttp, new QueueClient(ownerHttp, () => `n${++serial}-key-000000`));
  const events = new EventsClient({http});
  const emitEvent = (type: string, id: string) => events.deliver({id: `event-${++serial}`, type, resource: {kind: 'session', id}});
  if (options.refuse) owner.adopt = async () => { throw new Error('native owner closed'); };
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10, events: events as never, native: owner, nativeFor: options.nativeFor ?? (kind => kind === 'song')});
  const disconnect = player.connect();
  const stop = () => {disconnect(); events.resyncReads.dispose();};
  const jsTimeline = () => log.filter(r => r.path.endsWith('/observation')).length;
  return {server, api, playback, player, owner, log, stop, jsTimeline, ownerHttp, emitEvent, events};
}

test('C3: an audio session is handed to the native owner once, with the seq reached and the lease left', async () => {
  const {playback, player, owner, stop, jsTimeline} = setup();
  await player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
  assert.equal(owner.contexts.length, 1);
  const context = owner.contexts[0]!;
  assert.equal(context.session.kind, 'audio');
  assert.equal(context.lastSeq, 0, 'JS never reported this session');
  assert.ok(context.leaseMs > 110_000 && context.leaseMs <= 120_000);
  assert.equal(context.queueId, player.getSnapshot().view!.queue.id);
  assert.equal(context.intent, 'playing');
  const s = playback.getSnapshot();
  assert.equal(s.session?.id, context.session.id);
  assert.equal(s.nativeListening?.owner, 'owner-1');
  assert.equal(s.phase, 'ready');
  assert.equal(playback.isNativeListening(), true);
  assert.equal(jsTimeline(), 0, 'no JS timeline report');
  stop();
});

test('C3: video stays on the JS path', async () => {
  const {playback, player, owner, stop} = setup();
  await player.playItem('movie-1');
  assert.equal(owner.contexts.length, 0);
  assert.equal(playback.isNativeListening(), false);
  stop();
});

test('C3: the viewer’s intents and queue moves go to the owner; JS sends no PATCH, timeline or advance', async () => {
  const {playback, player, owner, log, stop, jsTimeline} = setup();
  await player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
  const before = log.length;
  playback.pause();
  playback.seek(30);
  playback.setPlaybackRate(1.5);
  await settle();
  assert.deepEqual(owner.actions.map(a => a.action), ['pause', 'seek', 'rate']);
  assert.equal(owner.actions[1]!.value, 30);
  const view = player.getSnapshot().view!;
  await player.next(view.queue.id, view.queue.revision);
  await settle();
  assert.deepEqual(owner.advances, [{reason: 'next', entryId: undefined}]);
  assert.equal(log.slice(before).filter(r => r.method === 'PATCH' || r.path.includes(':advance')).length, 0);
  assert.equal(jsTimeline(), 0);
  assert.equal(playback.getSnapshot().itemId, 'song-2', 'the owner’s new session is adopted');
  assert.equal(player.getSnapshot().view!.queue.currentPlaybackId, owner.session!.id);
  stop();
});

test('C3: moves the owner made while JS slept are adopted in order on waking; one live session', async () => {
  const {server, playback, player, owner, stop, jsTimeline} = setup();
  await player.playEntries(['song-1', 'song-2', 'song-3', 'song-4'].map(queueItemInput));
  const first = owner.session!.id;
  owner.asleep = true;
  await owner.move('completion');
  await owner.move('completion');
  await owner.move('completion');
  assert.equal(playback.getSnapshot().session?.id, first, 'nothing reaches JS while it sleeps');
  owner.wake();
  await settle();
  await settle();
  const last = owner.session!.id;
  assert.equal(playback.getSnapshot().session?.id, last);
  assert.equal(playback.getSnapshot().itemId, 'song-4');
  assert.equal(player.getSnapshot().view!.queue.currentPlaybackId, last);
  assert.equal(server.session(first)!.ended, true);
  assert.equal(server.session(last)!.ended, false);
  assert.equal(jsTimeline(), 0, 'no second report loop after the handover');
  stop();
});

test('C3: leaving stops through the owner; JS neither reports nor deletes the session', async () => {
  const {playback, player, owner, log, stop} = setup();
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  const before = log.length;
  playback.leave();
  await settle();
  assert.deepEqual(owner.detached, [true]);
  assert.equal(log.slice(before).filter(r => r.method === 'DELETE' || r.path.endsWith('/observation')).length, 0);
  assert.equal(playback.isNativeListening(), false);
  stop();
});

test('C3: an administrator’s terminate reaches the viewer with its message, and nothing advances', async () => {
  const {playback, player, owner, stop} = setup();
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  owner.emit({terminal: true, intent: 'paused', error: 'Stopped by the server owner.', end: {reason: 'terminated', message: 'Maintenance'}});
  await settle();
  const s = playback.getSnapshot();
  assert.equal(s.phase, 'error');
  assert.equal(s.ended?.reason, 'terminated');
  assert.equal(s.ended?.message, 'Maintenance');
  assert.equal(owner.advances.length, 0);
  stop();
});

// ── The owner's §18 edges against the fake (the wire the native owner speaks) ──

test('C3 §18: an edge the owner commits ends the previous session as completed; JS adopts the new one', async () => {
  const {server, playback, player, owner, stop} = setup();
  await player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
  const first = owner.session!.id;
  assert.equal(await owner.edge(), 'committed');
  await settle();
  assert.equal(server.commits, 1);
  assert.equal(server.session(first)!.endReason, 'completed');
  assert.equal(playback.getSnapshot().itemId, 'song-2');
  assert.equal(player.getSnapshot().view!.queue.currentPlaybackId, owner.session!.id);
  stop();
});

for (const [name, fail] of [['prepare 409', {prepare: 409}], ['prepare 503', {prepare: 503}], ['prepare offline', {prepare: 'offline'}], ['commit 409', {commit: [409]}], ['commit 410', {commit: [410]}], ['commit offline, again and again', {commit: ['offline', 'offline', 'offline', 'offline']}]] as const) {
  test(`C3 §18: ${name} falls back to the ordinary end and advance; the next track plays once`, async () => {
    const {server, playback, player, owner, stop} = setup();
    await player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
    const first = owner.session!.id;
    const f = fail as {prepare?: number | 'offline'; commit?: readonly (number | 'offline')[]};
    if (f.prepare !== undefined) server.failNext(f.prepare);
    else {
      // The prepare succeeds; the failures meet the commit.
      const client = (owner as unknown as {client: QueueClient}).client;
      const prepareNext = client.prepareNext.bind(client);
      client.prepareNext = async (...args) => { const r = await prepareNext(...args); server.failNext(...f.commit!); return r; };
    }
    assert.equal(await owner.edge(), 'fallback');
    await settle();
    assert.equal(server.commits, 0);
    assert.equal(server.session(first)!.ended, true);
    const second = owner.session!;
    assert.equal(second.itemId, 'song-2');
    assert.equal(playback.getSnapshot().session?.id, second.id);
    assert.equal(server.session(second.id)!.ended, false);
    stop();
  });
}

test('C3 §18: a lost commit answer is asked again and gets the same session (idempotent by token)', async () => {
  const {server, owner, player, stop} = setup();
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  const client = (owner as unknown as {client: QueueClient}).client;
  const prepareNext = client.prepareNext.bind(client);
  client.prepareNext = async (...args) => { const r = await prepareNext(...args); server.failNext('offline'); return r; };
  assert.equal(await owner.edge(), 'committed');
  assert.equal(server.commits, 1);
  assert.equal(owner.session!.itemId, 'song-2');
  stop();
});

test('C3 gate: a kind the app hasn’t turned on stays on the JS path, even for audio', async () => {
  const {api, playback, player, owner, stop, jsTimeline} = setup({nativeFor: () => false});
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  assert.equal(owner.contexts.length, 0);
  assert.equal(playback.isNativeListening(), false);
  assert.equal(playback.getSnapshot().itemId, 'song-1');
  const s = playback.getSnapshot();
  playback.ready(s.intentId);
  if (s.pendingSeek) playback.seekApplied(s.intentId, s.pendingSeek.revision, s.pendingSeek.positionSeconds, 'playing');
  playback.fact(playback.getSnapshot().intentId, 5, 'playing');
  await settle();
  // These timers never fire: a stop sends the latest reading first, which shows JS reports it.
  try { await api.stopPlayback(playback.getSnapshot().session!.id); assert.ok(jsTimeline() > 0, 'JS reports it, as before'); }
  finally { stop(); }
});

test('C3: a native handover that fails leaves the session with JS, never ownerless', async () => {
  const {server, playback, player, stop} = setup({refuse: true});
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  await settle();
  const s = playback.getSnapshot();
  assert.equal(playback.isNativeListening(), false);
  assert.ok(s.session, 'JS adopted the session');
  assert.equal(server.session(s.session!.id)!.ended, false);
  stop();
});

test('C3 (INT T4): a session whose playback was superseded before the handover is stopped, never left to lapse', async () => {
  // Another playback begins while the queue's create request is in flight.
  let ctx: ReturnType<typeof setup> | undefined;
  ctx = setup({answered: r => { if (r.method === 'POST' && r.path === '/v1/queues') ctx!.playback.leave(false); }});
  const {server, player, owner, log, stop} = ctx;
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput)).catch(() => {});
  await settle();
  assert.equal(owner.contexts.length, 0, 'no handover for a superseded intent');
  const deleted = log.filter(r => r.method === 'DELETE' && r.path.startsWith('/v1/playback/sessions/'));
  assert.equal(deleted.length, 1, 'the orphan is stopped once');
  assert.equal(server.session(deleted[0]!.path.split('/').pop()!)!.ended, true);
  stop();
});

test('C3: after a terminal snapshot the owner is let go, so Retry starts a new session with a new handover', async () => {
  const {playback, player, owner, stop} = setup();
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  owner.emit({terminal: true, intent: 'paused', error: 'Playback authorization is no longer valid.'});
  await settle();
  assert.equal(playback.hasNativeOwner(), false);
  assert.deepEqual(owner.detached, [true]);
  const before = owner.contexts.length;
  await playback.retry();
  await settle();
  assert.equal(owner.contexts.length, before + 1, 'a fresh handover');
  assert.equal(playback.hasNativeOwner(), true);
  stop();
});

test('C3: a session.updated event for the native session reaches the owner at once (an administrator’s terminate)', async () => {
  const {player, owner, stop, emitEvent} = setup();
  await player.playEntries(['song-1', 'song-2'].map(queueItemInput));
  emitEvent('session.updated', 'someone-else');
  emitEvent('session.updated', owner.session!.id);
  await settle();
  assert.deepEqual(owner.updates, [owner.session!.id]);
  stop();
});

// b4 source fixtures only. Verification remains held by Root's resource pause.
test('b4: failed policy pauses and each Play retries configuration before native resume', async () => {
  const {playback,player,owner,stop}=setup();
  let rejects=true;
  const policies:NativeListeningPolicy[]=[];
  (owner as ReferenceOwner&{configure(p:NativeListeningPolicy):Promise<boolean>}).configure=async p=>{policies.push(p);if(rejects)throw new Error('timer bridge refused');return true;};
  playback.setListeningPolicy({preferences:null,activityRevision:1,activityAtMs:Date.now(),timerRevision:1,timer:null});
  await player.playEntries(['song-1','song-2'].map(queueItemInput));await settle();
  assert.equal(playback.getSnapshot().intent,'paused');
  assert.equal(playback.getSnapshot().listeningNotice,'listening_policy_failed');
  owner.emit({intent:'paused',ready:false});
  assert.equal(playback.getSnapshot().listeningNotice,'listening_policy_failed','a loading observation cannot clear a pause notice');
  const before=policies.length;
  playback.resume();await settle();
  assert.ok(policies.length>before,'Play re-sends the rejected policy');
  assert.equal(owner.actions.filter(a=>a.action==='resume').length,0);
  assert.equal(playback.getSnapshot().listeningNotice,'listening_policy_failed');
  rejects=false;
  playback.resume();await settle();
  assert.equal(owner.actions.filter(a=>a.action==='resume').length,1);
  assert.equal(playback.getSnapshot().listeningNotice,undefined);
  stop();
});

test('b4: native applied facts are per-song and missing observations never inherit a true value',async()=>{
  const {playback,player,owner,stop}=setup();
  await player.playEntries(['song-1','song-2'].map(queueItemInput));
  owner.emit({rendering:true,eqApplied:true,levelingApplied:true});
  assert.equal(playback.getSnapshot().audioEffects.eqApplied,true);
  assert.equal(playback.getSnapshot().audioEffects.levelingApplied,true);
  owner.emit({rendering:false,eqApplied:false,levelingApplied:false});
  assert.equal(playback.getSnapshot().audioEffects.eqApplied,false);
  await owner.move('next');
  assert.equal(playback.getSnapshot().audioEffects.levelingApplied,false);
  assert.equal(playback.getSnapshot().audioEffects.eqApplied,false);
  stop();
});

test('b4: renderer facts reject stale session/generation/intent and clear on an error',async()=>{
  const {playback,player,stop}=setup({nativeFor:()=>false});
  await player.playEntries(['song-1','song-2'].map(queueItemInput));
  const s=playback.getSnapshot(),binding={intentId:s.intentId,sessionId:s.session!.id,generation:s.session!.generation};
  assert.equal(playback.audioProcessingApplied({...binding,generation:binding.generation+1},{eqApplied:true,levelingApplied:true}),false);
  assert.equal(playback.audioProcessingApplied(binding,{eqApplied:true,levelingApplied:true}),true);
  playback.pause();playback.listeningNotice('audio_autoplay_blocked');
  assert.equal(playback.getSnapshot().listeningNotice,'audio_autoplay_blocked');
  playback.fail(binding.intentId,'fixture playback error');
  assert.equal(playback.getSnapshot().listeningNotice,undefined);
  assert.equal(playback.getSnapshot().audioEffects.eqApplied,false);
  stop();
});


const drainResync = async () => {for (let i = 0; i < 80; i++) await Promise.resolve();};
test('resync recovers a lost native terminal through its exact current owner without replaying a queue command', async () => {
  const f = setup();
  try {
    await f.player.playEntries(['song-1', 'song-2'].map(queueItemInput));
    const original = f.owner.session!.id;
    await f.ownerHttp.send({method: 'POST', path: `/v1/admin/sessions/${original}:terminate`, body: {message: 'Maintenance'}});
    const before = f.log.length;
    f.owner.sessionUpdated = async id => {
      f.owner.updates.push(id);
      const session = await f.api.sessions.get(id);
      if (session.state === 'ended') f.owner.emit({terminal: true, intent: 'paused', end: session.end, error: 'Stopped by the server owner.'});
    };
    f.events.deliver({id: 'missed-terminal', type: 'stream.resync'}); await drainResync();
    assert.deepEqual(f.owner.updates, [original]);
    assert.equal(f.playback.getSnapshot().ended?.reason, 'terminated');
    assert.equal(f.playback.getSnapshot().ended?.message, 'Maintenance');
    assert.equal(f.owner.advances.length, 0);
    assert.equal(f.log.slice(before).some(r => r.method !== 'GET'), false);
  } finally {f.stop();}
});

test('resync adopts the committed native successor before reconciling authority, never the stale predecessor', async () => {
  const f = setup();
  try {
    await f.player.playEntries(['song-1', 'song-2', 'song-3'].map(queueItemInput));
    const predecessor = f.owner.session!.id;
    f.owner.asleep = true; await f.owner.move('completion');
    const successor = f.owner.session!.id; assert.notEqual(successor, predecessor);
    assert.equal(f.player.currentSession()!.id, predecessor);
    f.owner.refresh = async () => {f.owner.wake();};
    const before = f.log.length;
    f.events.deliver({id: 'missed-successor', type: 'stream.resync'}); await drainResync();
    assert.equal(f.player.currentSession()!.id, successor);
    assert.deepEqual(f.owner.updates, [successor]);
    assert.equal(f.owner.advances.length, 0);
    assert.equal(f.log.slice(before).some(r => r.method !== 'GET'), false);
  } finally {f.stop();}
});

test('retiring a queue during a held native snapshot cannot reconcile a replacement owner', async () => {
  const f = setup();
  let release!: () => void;
  try {
    await f.player.playEntries(['song-1', 'song-2'].map(queueItemInput));
    f.owner.refresh = () => new Promise<void>(resolve => {release = resolve;});
    f.events.deliver({id: 'held-snapshot', type: 'stream.resync'}); await drainResync();
    f.stop(); release(); await drainResync(); assert.deepEqual(f.owner.updates, []);
  } finally {release?.(); f.stop();}
});


test('resync awaits the current nonnative session GET and preserves the actual lost termination reason', async () => {
  const f = setup({nativeFor: () => false});
  try {
    await f.player.playEntries(['movie-1', 'movie-2'].map(queueItemInput));
    const original = f.player.currentSession()!.id;
    await f.ownerHttp.send({method: 'POST', path: `/v1/admin/sessions/${original}:terminate`, body: {message: 'Maintenance'}});
    const before = f.log.length;
    f.events.deliver({id: 'lost-local-terminal', type: 'stream.resync'}); await drainResync();
    assert.equal(f.playback.getSnapshot().ended?.reason, 'terminated');
    assert.equal(f.playback.getSnapshot().ended?.message, 'Maintenance');
    assert.equal(f.log.slice(before).some(r => r.method !== 'GET'), false);
  } finally {f.stop();}
});


test('a later resync barrier retires a held native recovery before owner reconciliation', async () => {
  const f = setup(); let release!: () => void;
  try {
    await f.player.playEntries(['song-1', 'song-2'].map(queueItemInput));
    f.owner.refresh = () => new Promise<void>(resolve => {release = resolve;});
    f.events.deliver({id: 'held-old-barrier', type: 'stream.resync'}); await drainResync();
    f.events.deliver({id: 'newer-barrier', type: 'stream.resync'}); release(); await drainResync();
    assert.deepEqual(f.owner.updates, [], 'stale barrier reconciled its owner');
  } finally {release?.(); f.stop();}
});
