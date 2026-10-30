import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService, tunerHandover} from '../src/index.ts';
import {V1ChannelControl, channelEndMessage, parseChannelSession, v1ChannelId, type OriginalChannelStart} from '../src/playback-v1/channel-control.ts';
import {parseStartPreparation} from '../src/playback-v1/sessions.ts';
import type {V1Http, V1Request} from '../src/playback-v1/http.ts';
import type {ChannelReference} from '../src/playback/linear-protocol.ts';

const settle = async () => { for (let i = 0; i < 40; i++) await new Promise(r => setImmediate(r)); };
const channel: ChannelReference = {kind: 'library-channel', sourceId: 'news', channelId: 'news', generation: 'g1'};

/** A v1 channel session as spec §18.6 serves it: the stream appears after a read; a live seek
 * resolves to the live edge; a report within 1 s of the pending seek acknowledges it. */
class ChannelServer implements V1Http {
  log: V1Request[] = [];
  consumed: string[] = [];
  async acknowledgeUserMutation(intentId: string) { this.consumed.push(intentId); }
  refuse?: string;
  private reads = 0;
  private s: any;
  async send(r: V1Request) {
    this.log.push(r);
    const ok = (body: unknown, status = 200) => ({status, headers: {}, body});
    if (r.method === 'POST' && r.path === '/v1/playback/sessions') {
      if (this.refuse) return {status: 409, headers: {}, body: {error: {code: this.refuse, retry: 'after_refresh'}}};
      this.s = {id: 'ps_1', revision: '1', kind: 'channel', state: (r.body as {state:string}).state, presentation: {generation: 0, url: '', subtitles: [], decision: {},
        linear: {name: 'News', programme: {id: 'p1', title: 'Morning', startMs: 0, endMs: 3_600_000}, originMs: 1000, windowStartMs: 0, windowEndMs: 0, liveEdgeMs: 0, state: 'preparing'}}};
      return ok(this.s, 201);
    }
    const s = this.s;
    const ready = () => {
      if (++this.reads >= 1 && !s.presentation.url) {
        s.presentation.generation = 1; s.presentation.url = '/v2/media/linear/ps_1/1/tok/index.m3u8';
        Object.assign(s.presentation.linear, {windowStartMs: 0, windowEndMs: 60_000, liveEdgeMs: 60_000, state: 'active'});
      }
    };
    if (r.method === 'GET') { ready(); return ok(s); }
    if (r.method === 'POST' && r.path.endsWith('/observer')) return ok({epoch: 'channel-epoch-00000001', everyMs: 1000});
    if (r.method === 'PATCH') {
      if (r.headers?.['If-Match'] !== `"${s.revision}"`) return {status: 412, headers: {}, body: {error: {code: 'revision_mismatch', current: s}}};
      const b = r.body as any;
      if (b.state) s.state = b.state;
      if (b.seek) s.presentation.linear.seek = {id: b.seek.id, positionMs: b.seek.live ? s.presentation.linear.liveEdgeMs : b.seek.positionMs, acknowledged: false};
      s.revision = String(Number(s.revision) + 1);
      return ok(s);
    }
    if (r.method === 'PUT' && r.path.endsWith('/observation')) {
      // The ack carries the channel's current view, as the real server's does when it changed.
      const b = r.body as any, seek = s.presentation.linear.seek;
      if (b.generation === s.presentation.generation && s.presentation.url) {
        if (seek && Math.abs(b.positionMs - seek.positionMs) <= 1000) seek.acknowledged = true;
        s.presentation.linear.confirmedPositionMs = b.positionMs;
      }
      ready();
      return ok({acceptedSeq: b.seq, viewTag: 'v' + this.reads, session: s});
    }
    if (r.method === 'DELETE') { s.state = 'ended'; return {status: 204, headers: {}}; }
    return {status: 404, headers: {}, body: {error: {code: 'not_found'}}};
  }
}

function setup() {
  const server = new ChannelServer();
  let serial = 0;
  const control = new V1ChannelControl({http: server, key: () => `key-${String(++serial).padStart(16, '0')}`, pollMs: 5});
  const playback = new PlaybackService({createPlayback: async () => { throw new Error('no VOD'); }, stopPlayback: async () => {}, progressPlayback: async () => {}}, () => `r${++serial}`, {});
  playback.useChannelPlayback(control);
  return {server, control, playback};
}

test('v1 channels: a tune starts a channel session, joins at the live edge, and the reached seek is acknowledged', async () => {
  const {server, playback} = setup();
  try {
    await playback.playChannel(channel);
    await new Promise(r => setTimeout(r, 40));
    await settle();
    const start = server.log.find(r => r.method === 'POST' && r.path === '/v1/playback/sessions')!;
    assert.deepEqual(start.body, {channelId: v1ChannelId(channel), channelGeneration: 'g1', state: 'playing'});
    const join = server.log.find(r => r.method === 'PATCH')!;
    assert.equal((join.body as any).seek.live, true);
    let s = playback.getSnapshot();
    assert.equal(s.session?.streamUrl, '/v2/media/linear/ps_1/1/tok/index.m3u8');
    assert.equal(s.linear?.name, 'News');
    assert.equal(s.pendingSeek?.positionSeconds, 60, 'the engine attaches at the resolved live edge');
    // The engine reached it: the report acknowledges the seek and the pending seek clears.
    playback.ready(s.intentId);
    playback.seekApplied(s.intentId, s.pendingSeek!.revision, 60, 'playing');
    await new Promise(r => setTimeout(r, 40));
    await settle();
    s = playback.getSnapshot();
    assert.equal(s.pendingSeek, undefined);
    assert.ok(server.log.some(r => r.path.endsWith('/observation') && (r.body as any).positionMs === 60_000));
  } finally { playback.leave(); }
  await settle();
  assert.ok(server.log.some(r => r.method === 'DELETE'), 'leaving stops the session');
  const stopped = server.log.find(r => r.method === 'DELETE')!;
  assert.deepEqual(server.consumed, [`stop:ps_1:${(stopped.body as {generation:number}).generation}`], 'a consumed channel Stop releases its exact durable receipt');
});

test('v1 channels: pause and a seek in the window are PATCHes on the current revision', async () => {
  const {server, playback} = setup();
  try {
    await playback.playChannel(channel);
    await new Promise(r => setTimeout(r, 40));
    playback.pause();
    await settle();
    playback.seek(12);
    await new Promise(r => setTimeout(r, 30));
    const patches = server.log.filter(r => r.method === 'PATCH').map(r => r.body as any);
    assert.ok(patches.some(b => b.state === 'paused'));
    assert.ok(patches.some(b => b.seek?.positionMs === 12_000));
    assert.equal(playback.getSnapshot().pendingSeek?.positionSeconds, 12);
  } finally { playback.leave(); }
});

test('v1 channels: a restore POST and its resolved join stay paused until an explicit resume',async()=>{
 const {server,playback}=setup();
 try{
  const intents:string[]=[];const unsubscribe=playback.subscribe(()=>intents.push(playback.getSnapshot().intent));
  try{
   await playback.playChannel(channel,{state:'paused'});
   for(let i=0;i<100&&!playback.getSnapshot().session;i++)await new Promise(r=>setTimeout(r,2));
   const start=server.log.find(r=>r.method==='POST'&&r.path==='/v1/playback/sessions')!;
   assert.deepEqual(start.body,{channelId:v1ChannelId(channel),channelGeneration:'g1',state:'paused'});
   let snapshot=playback.getSnapshot();assert.ok(snapshot.session);assert.equal(snapshot.intent,'paused');assert.equal(snapshot.playbackRun,1);
   const join=server.log.find(r=>r.method==='PATCH')!;assert.equal((join.body as any).state,'paused');assert.equal((join.body as any).seek.live,true);
   playback.ready(snapshot.intentId);playback.seekApplied(snapshot.intentId,snapshot.pendingSeek!.revision,60,'paused');await settle();
   // Restore only inside the real retained window, while the actual session is paused.
   snapshot=playback.getSnapshot();assert.equal(snapshot.intent,'paused');assert.equal(snapshot.linear?.desired.state,'paused');
   assert.ok(Number(snapshot.linear!.media.windowStartUs)/1e6<=12&&Number(snapshot.linear!.media.windowEndUs)/1e6>=12);
   playback.seek(12);await settle();
   const seek=server.log.filter(r=>r.method==='PATCH').at(-1)!;assert.equal((seek.body as any).state,'paused');assert.equal((seek.body as any).seek.positionMs,12000);
   assert.ok(intents.every(intent=>intent==='paused'));assert.ok(server.log.filter(r=>r.method==='POST'||r.method==='PATCH').every(r=>(r.body as any).state===undefined||(r.body as any).state==='paused'));
   playback.resume();await settle();assert.equal(playback.getSnapshot().intent,'playing');assert.ok(server.log.some(r=>r.method==='PATCH'&&(r.body as any).state==='playing'));
  }finally{unsubscribe();}
 }finally{playback.leave();}
});

test('v1 channels: every tuner in use is said as such, and the start is over', async () => {
  const {server, playback} = setup();
  server.refuse = 'no_tuner_available';
  await playback.playChannel(channel);
  await settle();
  const s = playback.getSnapshot();
  assert.equal(s.phase, 'error');
  assert.match(s.error ?? '', /tuners/);
  playback.leave();
});

test('v1 channels: an end names why: the administrator’s message, Live TV off for the profile, else the generic end', () => {
  assert.equal(channelEndMessage({reason: 'terminated', message: 'Server maintenance'}), 'Server maintenance');
  assert.equal(channelEndMessage({reason: 'feature_restricted'}), 'Live TV is turned off for this profile.');
  assert.match(channelEndMessage({reason: 'something_new'}), /has ended/);
  assert.match(channelEndMessage(undefined), /has ended/);
});

// B-11.27: with the owner's "Stop watching" setting a recording takes a watched tuner. The viewer
// is counted down, then the channel stops by itself and says why; it is not retried.
test('v1 channels: a tuner deadline counts down, then the channel stops once with the reason', async () => {
  const {server, playback} = setup();
  try {
    await playback.playChannel(channel);
    await new Promise(r => setTimeout(r, 40));
    await settle();
    const deadline = Date.now() + 90_000;
    (server as any).s.presentation.linear.reservationRequiredAtMs = deadline;
    await new Promise(r => setTimeout(r, 20));
    await settle();
    let s = playback.getSnapshot();
    assert.equal(s.linear?.media.reservationRequiredAtMs, deadline);
    assert.deepEqual(tunerHandover(deadline, Date.now()), {kind: 'later', atMs: deadline});
    assert.deepEqual(tunerHandover(deadline, deadline - 12_300), {kind: 'countdown', secondsLeft: 13});
    assert.notEqual(s.phase, 'error');
    // The server gives the tuner to the recording.
    (server as any).s.presentation.linear.errorCode = 'tuner_reservation_changed';
    await new Promise(r => setTimeout(r, 20));
    await settle();
    s = playback.getSnapshot();
    assert.equal(s.phase, 'error');
    assert.equal(s.ended?.reason, 'tuner_reserved');
    assert.equal(s.linear?.name, 'News', 'the stop still names the channel');
    assert.ok(server.log.some(r => r.method === 'DELETE'), 'the channel session is stopped');
    const reads = server.log.length;
    await new Promise(r => setTimeout(r, 30));
    await settle();
    assert.equal(server.log.length, reads, 'nothing polls or retries after the stop');
  } finally { playback.leave(); }
});

// N18: the countdown runs on server time. A device clock five minutes slow still sees the tuner
// go ninety seconds after the server said so.
test('v1 channels: a tuner deadline is moved onto this device clock with the server clock sent beside it', () => {
  const server = 1_800_000_000_000, device = server - 300_000;
  const raw = {id: 's', revision: '1', state: 'playing', presentation: {generation: 1, url: '/x', linear: {name: 'One', originMs: 0, windowStartMs: 0, windowEndMs: 0, liveEdgeMs: 0, state: 'live', reservationRequiredAtMs: server + 90_000, nowMs: server}}};
  const parsed = parseChannelSession(raw, () => device);
  assert.equal(parsed.presentation.linear?.reservationRequiredAtMs, device + 90_000);
  assert.deepEqual(tunerHandover(parsed.presentation.linear?.reservationRequiredAtMs, device + 30_000), {kind: 'countdown', secondsLeft: 60});
  // Without the server clock, the deadline is taken as sent.
  const {nowMs: _n, ...plain} = raw.presentation.linear;
  assert.equal(parseChannelSession({...raw, presentation: {...raw.presentation, linear: plain}}, () => device).presentation.linear?.reservationRequiredAtMs, server + 90_000);
});


test('a channel start forwards captured cancellation and refuses late adoption after its screen retires', async () => {
  for (const abort of [false, true]) {
    const server = new ChannelServer(), life = new AbortController();let active = true, enter!: () => void, release!: () => void;
    const entered = new Promise<void>(resolve => {enter = resolve;}), held = new Promise<void>(resolve => {release = resolve;});
    const states: unknown[] = [], errors: string[] = []; let sentSignal: AbortSignal | undefined;
    const control = new V1ChannelControl({http: {send: async request => {
      const response = await server.send(request);
      if (request.method === 'POST' && request.path === '/v1/playback/sessions') {sentSignal = request.signal;enter();await held;}
      return response;
    }}});
    const opening = control.open(channel, {state: value => states.push(value), error: value => errors.push(value)}, {signal: life.signal, isCurrent: () => active});
    await entered;
    assert.ok(sentSignal); assert.equal(sentSignal.aborted, false);
    if (abort) life.abort();else active = false;
    release();await opening;await settle();
    assert.equal(sentSignal.aborted, true, 'the channel releases only its own pending request when the opening retires');
    assert.deepEqual(states, []);assert.deepEqual(errors, []);
    assert.ok(server.log.some(request => request.method === 'DELETE' && request.path === '/v1/playback/sessions/ps_1'));
    control.close();
  }
});

function preparationQueue() {
  const jobs = new Map<number, () => void>(); const cleared: unknown[] = []; let serial = 0;
  return {timers: {setTimer: (fn: () => void) => {jobs.set(++serial, fn); return serial;}, clearTimer: (timer: unknown) => {cleared.push(timer); jobs.delete(timer as number);}},
    fire() {const job = jobs.entries().next().value; if (!job) throw new Error('no channel preparation wait'); jobs.delete(job[0]); job[1]();},
    pending: () => jobs.size, cleared};
}

/** An official channel session as §18.6 serves it at 201. `cold` is the first
 * projection the start returns: generation 0, no stream yet, linear preparing.
 * `active` is that same session after the producer's buffer exists: generation
 * 1 with its own canonical linear media URL
 * (`/v1/media/linear/<id>/<generation>/<token>/index.m3u8`). A channel 202 is
 * retry-only; the declared session-backed 202 variants remain vod/audio. */
function channelBody(id: string, shape: 'cold' | 'active'): Record<string, unknown> {
  const active = shape === 'active';
  return {id, revision: '1', kind: 'channel', role: 'local', state: 'playing', channelId: v1ChannelId(channel),
    lease: {expiresAt: '2030-01-01T00:00:00Z', reportEveryMs: 10_000},
    presentation: {generation: active ? 1 : 0, mode: 'stream', engine: 'native', startPositionMs: 0, quality: {mode: 'original'}, subtitles: [],
      url: active ? `/v1/media/linear/${id}/1/original-grant/index.m3u8` : '',
      decision: {video: {action: 'copy', reasons: []}, audio: {action: 'copy', reasons: []}},
      linear: {name: 'News', programme: {id: 'p1', title: 'Morning', startMs: 0, endMs: 3_600_000}, originMs: 1000,
        windowStartMs: 0, windowEndMs: active ? 120_000 : 0, liveEdgeMs: active ? 60_000 : 0, state: active ? 'active' : 'preparing'}}};
}
const coldChannelBody = (id: string) => channelBody(id, 'cold');
const activeChannelBody = (id: string) => channelBody(id, 'active');

test('a channel retry-only 202 prepares the original start before any channel state', async () => {
  const server = new ChannelServer(), clock = preparationQueue();
  const posts: {key?: string; body: unknown}[] = [];
  const http: V1Http = {send: async request => {
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') {
      posts.push({key: request.headers?.['Idempotency-Key'], body: request.body});
      if (posts.length <= 2) return {status: 202, headers: {}, body: {variant: 'wait', retryAfterMs: 500}};
    }
    return server.send(request);
  }};
  const control = new V1ChannelControl({http, key: () => 'channel-key-0000001', pollMs: 5, timers: clock.timers});
  const states: any[] = [], errors: string[] = [], preparation: any[] = [];
  let opening: Promise<void> | undefined;
  try {
    opening = control.open(channel, {state: value => states.push(value), error: message => errors.push(message), preparation: value => preparation.push(value)});
    await settle();
    assert.deepEqual(preparation, [{messageId: 'player.preparing.slowStart', retryAfterMs: 500}]);
    assert.deepEqual(states, [], 'no channel state exists before a real 201');
    assert.equal(clock.pending(), 1);
    clock.fire(); await settle();
    assert.deepEqual(preparation, [{messageId: 'player.preparing.slowStart', retryAfterMs: 500}, {messageId: 'player.preparing.slowStart', retryAfterMs: 500}]);
    assert.deepEqual(states, []);
    clock.fire(); await settle(); await opening;
    assert.equal(posts.length, 3);
    for (const post of posts) {assert.equal(post.key, 'channel-key-0000001'); assert.deepEqual(post.body, posts[0].body);}
    assert.deepEqual(posts[0].body, {channelId: v1ChannelId(channel), channelGeneration: 'g1', state: 'playing'});
    assert.equal(preparation.at(-1), undefined, 'acceptance clears the notice');
    assert.deepEqual(errors, []);
    assert.ok(states.length >= 1);
    assert.equal(states[0].playbackId, 'ps_1');
    assert.equal(states[0].linear.name, 'News');
    // The first samples' acks carry the view that turns ready; the join follows it.
    await new Promise(resolve => setTimeout(resolve, 40)); await settle();
    assert.ok(server.log.some(request => request.method === 'PATCH' && (request.body as any).seek?.live === true), 'the honest generation-0/empty-URL 201 still joins');
  } finally { control.close(); await opening?.catch(() => {}); }
});

test('a channel retry-only 202 keeps one original start and publishes the actual 201 projection once', async () => {
  const clock = preparationQueue();
  let posts = 0, preparingOffers = 0, abandoned = 0;
  const sent: V1Request[] = [];
  const http: V1Http = {send: async request => {
    if (request.method === 'GET') return {status: 200, headers: {}, body: activeChannelBody('ps_1')};
    if (request.method !== 'POST' || request.path !== '/v1/playback/sessions') return {status: 204, headers: {}};
    sent.push(request); posts++;
    if (posts === 1) return {status: 202, headers: {}, body: {variant: 'wait', retryAfterMs: 500}};
    return {status: 201, headers: {}, body: coldChannelBody('ps_1')};
  }};
  const control = new V1ChannelControl({http, key: () => 'channel-key-0000002', pollMs: 10_000, timers: clock.timers});
  const states: any[] = [], errors: string[] = [], preparation: any[] = [];
  let opening: Promise<void> | undefined;
  try {
    opening = control.open(channel, {state: value => states.push(value), error: message => errors.push(message), preparation: value => preparation.push(value)},
      {capturePreparing: () => {preparingOffers++; return () => {abandoned++;};}});
    await settle();
    // The channel branch of the 202 union is retry-only: no declared session is
    // offered, so no preparing resource can be captured or abandoned.
    assert.equal(preparingOffers, 0, 'a channel 202 is retry-only and offers no preparing session');
    assert.deepEqual(preparation, [{messageId: 'player.preparing.slowStart', retryAfterMs: 500}]);
    assert.deepEqual(states, [], 'no channel state exists before the real 201');
    assert.equal(clock.pending(), 1);
    clock.fire(); await settle(); await opening;
    assert.equal(posts, 2); assert.equal(abandoned, 0);
    assert.equal(sent.length, 2);
    for (const request of sent) {
      assert.equal(request, sent[0], 'the retry is the same original request');
      assert.deepEqual(request.body, {channelId: v1ChannelId(channel), channelGeneration: 'g1', state: 'playing'});
      assert.equal(request.headers?.['Idempotency-Key'], 'channel-key-0000002');
    }
    assert.equal(preparation.at(-1), undefined, 'acceptance clears the notice');
    assert.equal(states.length, 1);
    assert.equal(states[0].playbackId, 'ps_1');
    assert.equal(states[0].linear.name, 'News');
    assert.deepEqual(errors, []);
  } finally { control.close(); await opening?.catch(() => {}); }
});

test('caller retirement during a channel retry-only 202 wait clears its timer and sends nothing more', async () => {
  const clock = preparationQueue(), life = new AbortController();
  let posts = 0;
  const http: V1Http = {send: async request => {
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') {posts++; return {status: 202, headers: {}, body: {variant: 'wait', retryAfterMs: 500}};}
    return {status: 204, headers: {}};
  }};
  const control = new V1ChannelControl({http, key: () => 'channel-key-0000005', pollMs: 10_000, timers: clock.timers});
  const states: any[] = [], errors: string[] = [], preparation: any[] = [];
  const opening = control.open(channel, {state: value => states.push(value), error: (message, terminal) => errors.push(`${message}:${terminal}`),
    preparation: value => preparation.push(value)}, {signal: life.signal});
  try {
    await settle();
    assert.equal(posts, 1); assert.equal(clock.pending(), 1);
    assert.deepEqual(preparation, [{messageId: 'player.preparing.slowStart', retryAfterMs: 500}]);
    life.abort(); await opening; await settle();
    assert.equal(clock.pending(), 0); assert.ok(clock.cleared.length >= 1);
    assert.equal(posts, 1, 'a retry-only wait owns no resource and dispatches nothing more');
    assert.deepEqual(states, []); assert.deepEqual(errors, []);
    assert.deepEqual(preparation, [{messageId: 'player.preparing.slowStart', retryAfterMs: 500}, undefined], 'the retirement clears the notice');
  } finally { control.close(); await opening.catch(() => {}); }
});

test('an impossible channel session 202 is refused by the strict start decoder without install, timer or cleanup', async () => {
  const clock = preparationQueue();
  let posts = 0;
  const http: V1Http = {send: async request => {
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') {posts++; return {status: 202, headers: {},
      body: {...coldChannelBody('ps_impossible'), variant: 'session', retryAfterMs: 500}};}
    return {status: 204, headers: {}};
  }};
  const control = new V1ChannelControl({http, key: () => 'channel-key-0000006', pollMs: 10_000, timers: clock.timers});
  const states: any[] = [], errors: {message: string; terminal: boolean}[] = [], preparation: any[] = [];
  const opening = control.open(channel, {state: value => states.push(value),
    error: (message, terminal) => errors.push({message, terminal}), preparation: value => preparation.push(value)});
  try {
    assert.throws(() => parseStartPreparation({...coldChannelBody('ps_impossible'), variant: 'session', retryAfterMs: 500}), {name: 'ContractError'},
      'only vod/audio may carry a session in a 202');
    await opening; await settle();
    assert.equal(posts, 1); assert.equal(clock.pending(), 0); assert.deepEqual(states, []);
    assert.deepEqual(preparation, [undefined]);
    assert.equal(errors.length, 1); assert.equal(errors[0].terminal, true); assert.ok(errors[0].message.length > 0);
  } finally { control.close(); await opening.catch(() => {}); }
});

test('closing a channel during a valid preparation wait clears its timer and sends nothing more', async () => {
  const server = new ChannelServer(), clock = preparationQueue();
  let posts = 0;
  const http: V1Http = {send: async request => {
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') {posts++; return {status: 202, headers: {}, body: {variant: 'wait', retryAfterMs: 500}};}
    return server.send(request);
  }};
  const control = new V1ChannelControl({http, key: () => 'channel-key-0000003', pollMs: 10_000, timers: clock.timers});
  const states: any[] = [], errors: string[] = [], preparation: any[] = [];
  const opening = control.open(channel, {state: value => states.push(value), error: message => errors.push(message), preparation: value => preparation.push(value)});
  try {
    await settle();
    assert.equal(posts, 1); assert.equal(clock.pending(), 1); assert.equal(preparation.length, 1);
  } finally {
    control.close(); await opening.catch(() => {});
  }
  await settle();
  assert.equal(clock.pending(), 0); assert.ok(clock.cleared.length >= 1);
  assert.equal(posts, 1); assert.deepEqual(states, []); assert.deepEqual(errors, []);
  assert.deepEqual(preparation, [{messageId: 'player.preparing.slowStart', retryAfterMs: 500}, undefined], 'the close clears the notice');
});

test('a channel notice callback can retire its own opening before a wait is installed', async () => {
  const server = new ChannelServer(), clock = preparationQueue();
  let posts = 0, notices = 0, clears = 0;
  const http: V1Http = {send: async request => {
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') {posts++; return {status: 202, headers: {}, body: {variant: 'wait', retryAfterMs: 500}};}
    return server.send(request);
  }};
  const control = new V1ChannelControl({http, key: () => 'channel-key-0000004', pollMs: 10_000, timers: clock.timers});
  const states: any[] = [], errors: string[] = [];
  let opening: Promise<void> | undefined;
  try {
    opening = control.open(channel, {state: value => states.push(value), error: message => errors.push(message),
      preparation: value => {if (value === undefined) clears++; else {notices++; control.close();}}});
  } finally {
    await opening?.catch(() => {});
  }
  await settle();
  assert.equal(notices, 1); assert.equal(clears, 1); assert.equal(posts, 1); assert.equal(clock.pending(), 0);
  assert.deepEqual(states, []); assert.deepEqual(errors, []);
});

test('an older channel completion cannot publish over its successor', async () => {
  let posts = 0, release!: () => void;
  const held = new Promise<void>(resolve => {release = resolve;});
  const requests: V1Request[] = [];
  const http: V1Http = {send: async request => {
    requests.push(request);
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') {
      posts++;
      if (posts === 1) {await held; return {status: 201, headers: {}, body: coldChannelBody('ps_old')};}
      return {status: 201, headers: {}, body: coldChannelBody('ps_new')};
    }
    if (request.method === 'GET' || request.method === 'PATCH') return {status: 200, headers: {}, body: activeChannelBody(request.path.includes('ps_old') ? 'ps_old' : 'ps_new')};
    return {status: 204, headers: {}};
  }};
  const control = new V1ChannelControl({http, key: () => `channel-key-${String(posts + 5).padStart(7, '0')}`, pollMs: 10_000});
  const statesOld: any[] = [], statesNew: any[] = [], errorsNew: string[] = [];
  let opening: Promise<void> | undefined, successor: Promise<void> | undefined;
  try {
    opening = control.open(channel, {state: value => statesOld.push(value), error: () => {}});
    await settle();
    assert.equal(posts, 1);
    successor = control.open(channel, {state: value => statesNew.push(value), error: message => errorsNew.push(message)});
    await successor;
    release();
    await opening; await settle();
    assert.ok(statesNew.length >= 1, 'the successor publishes (a generation 0 start also publishes once its stream exists)');
    assert.ok(statesNew.every(state => state.playbackId === 'ps_new'));
    assert.equal(statesNew[0].linear.name, 'News');
    assert.deepEqual(statesOld, [], 'the older held start never published');
    assert.deepEqual(errorsNew, []);
    const deletes = requests.filter(request => request.method === 'DELETE').map(request => request.path);
    assert.deepEqual(deletes, ['/v1/playback/sessions/ps_old'], 'only the older completion is stopped before teardown');
  } finally {
    release();
    await Promise.allSettled([opening, successor]);
    control.close();
  }
});

test('the channel receipt issuer captures the actual original key and body before dispatch', async () => {
  const clock = preparationQueue(), originals: OriginalChannelStart[] = [], sent: V1Request[] = [];
  let keys = 0, preparingOffers = 0, abandoned = 0, finished = 0, installed: unknown;
  const accepted = coldChannelBody('ps_original');
  const control = new V1ChannelControl({key: () => {keys++; return 'channel-original-key-0001';}, timers: clock.timers, pollMs: 10_000,
    http: {send: async request => {
      if (request.method === 'GET') return {status: 200, headers: {}, body: activeChannelBody('ps_original')};
      if (request.method !== 'POST' || request.path !== '/v1/playback/sessions') return {status: 204, headers: {}};
      assert.equal(originals.length, 1, 'host custody exists before the first POST');
      sent.push(request);
      return sent.length === 1 ? {status: 202, headers: {}, body: {variant: 'wait', retryAfterMs: 500}}
        : {status: 201, headers: {}, body: accepted};
    }}});
  let opening: Promise<void> | undefined;
  try {
    opening = control.open(channel, {state: () => {}, error: message => assert.fail(message)}, {captureStart: original => {
      assert.equal(sent.length, 0); originals.push(original);
      return {isCurrent: original.isCurrent, capturePreparing: () => {
        preparingOffers++; return () => {abandoned++;};
      }, claimInstall: session => {installed = session; return true;}, retireUninstalled: () => {assert.fail('the accepted original needs no retirement');}, finish: () => {finished++;}};
    }});
    await settle();
    // The channel 202 is retry-only: the receipt is never offered a session to capture.
    assert.equal(originals.length, 1); assert.equal(keys, 1); assert.equal(preparingOffers, 0); assert.equal(finished, 0);
    const original = originals[0];
    assert.ok(Object.isFrozen(original)); assert.ok(Object.isFrozen(original.body));
    assert.deepEqual(original.body, {channelId: v1ChannelId(channel), channelGeneration: 'g1', state: 'playing'});
    assert.equal(original.key, 'channel-original-key-0001'); assert.equal(original.isCurrent(), true);
    clock.fire(); await opening;
    assert.equal(sent.length, 2); assert.equal(keys, 2, 'one start key, one go-live seek id'); assert.equal(preparingOffers, 0); assert.equal(abandoned, 0); assert.equal(finished, 1);
    assert.ok(Object.isFrozen(installed)); assert.deepEqual(installed, {id: 'ps_original', generation: 0});
    for (const request of sent) {
      assert.equal(request, sent[0], 'the retry is the same original request');
      assert.deepEqual(request.body, original.body); assert.equal(request.headers?.['Idempotency-Key'], original.key);
      assert.equal(request.signal, original.signal);
    }
  } finally {control.close(); await opening?.catch(() => {});}
});

test('retirement inside the original channel receipt issuer cannot dispatch a request', async () => {
  let posts = 0, finished = 0;
  const control = new V1ChannelControl({key: () => 'channel-original-key-0002', http: {send: async () => {
    posts++; throw new Error('a retired original cannot send');
  }}});
  await control.open(channel, {state: () => assert.fail('no session is installed'), error: message => assert.fail(message)}, {
    captureStart: original => {
      control.close(); assert.equal(original.signal.aborted, true);
      return {isCurrent: () => false, capturePreparing: () => {assert.fail('no preparation was received');}, claimInstall: () => {assert.fail('no resource was returned');}, retireUninstalled: () => {assert.fail('no real session was returned');}, finish: () => {finished++;}};
    },
  });
  assert.equal(posts, 0); assert.equal(finished, 1);
});

test('a late channel 201 uses the original host refusal without a bare stop fallback', async () => {
  let posts = 0, enter!: () => void, release!: () => void, finished = 0;
  const entered = new Promise<void>(resolve => {enter = resolve;}), held = new Promise<void>(resolve => {release = resolve;});
  const deletes: string[] = [], refused: Readonly<{id: string; generation: number}>[] = [];
  const control = new V1ChannelControl({key: () => 'channel-original-shared-key', pollMs: 10_000, http: {send: async request => {
    if (request.method === 'DELETE') {deletes.push(request.path); return {status: 204, headers: {}};}
    const body = activeChannelBody('ps_shared');
    if (request.method === 'PATCH') return {status: 200, headers: {}, body};
    if (++posts === 1) {enter(); await held;}
    return {status: 201, headers: {}, body};
  }}});
  let original: Promise<void> | undefined, successor: Promise<void> | undefined;
  try {
    original = control.open(channel, {state: () => assert.fail('the retired opening cannot install'), error: message => assert.fail(message)}, {
      captureStart: start => ({isCurrent: start.isCurrent, capturePreparing: () => undefined, claimInstall: () => {assert.fail('retired start cannot claim installation');},
        // The actual host must prove noninstallation. This fake refuses to stop
        // a resource its successor just adopted; it supplies no authority.
        retireUninstalled: session => {refused.push(session);}, finish: () => {finished++;}}),
    });
    await entered;
    const states: string[] = [];
    successor = control.open(channel, {state: value => states.push(value.playbackId), error: message => assert.fail(message)});
    await successor;
    release(); await original; await settle();
    assert.deepEqual(states, ['ps_shared']);
    assert.deepEqual(refused, [{id: 'ps_shared', generation: 1}]);
    assert.ok(Object.isFrozen(refused[0])); assert.equal(finished, 1);
    assert.deepEqual(deletes, [], 'host refusal never downgrades to DELETE of the adopted occurrence');
  } finally {release(); await Promise.allSettled([original, successor]); control.close();}
});


test('channel installation is claimed before the internal go-live PATCH, with the callback captured before await', async () => {
  const events: string[] = [];
  let enter!: () => void, release!: () => void;
  const entered = new Promise<void>(resolve => {enter = resolve;}), held = new Promise<void>(resolve => {release = resolve;});
  const body = activeChannelBody('ps_install');
  const receipt = {isCurrent: () => true, capturePreparing: () => undefined,
    claimInstall: (session: Readonly<{id: string; generation: number}>) => {
      assert.ok(Object.isFrozen(session)); assert.deepEqual(session, {id: 'ps_install', generation: 1});
      events.push('claim'); return true;
    }, retireUninstalled: () => assert.fail('accepted installation must not retire'), finish: () => {events.push('finish');}};
  const control = new V1ChannelControl({key: () => 'channel-install-key-0001', pollMs: 60_000, http: {send: async request => {
    if (request.method === 'POST') {enter(); await held; return {status: 201, headers: {}, body};}
    if (request.method === 'PATCH') {events.push('patch'); return {status: 200, headers: {}, body};}
    return {status: 204, headers: {}};
  }}});
  let opening: Promise<void> | undefined;
  try {
    opening = control.open(channel, {state: () => {events.push('state');}, error: message => assert.fail(message)}, {captureStart: () => receipt});
    await entered;
    receipt.claimInstall = () => assert.fail('a caller mutation cannot replace the captured callback');
    release(); await opening; await settle();
    assert.equal(events.filter(event => event === 'claim').length, 1);
    assert.ok(events.indexOf('claim') < events.indexOf('patch'));
    assert.ok(events.indexOf('claim') < events.indexOf('state'));
    assert.equal(events.filter(event => event === 'finish').length, 1);
  } finally {release(); await opening?.catch(() => {}); control.close();}
});

for (const refusal of ['false', 'throw', 'retire'] as const) {
  test(`a channel install claim that ${refusal === 'retire' ? 'retires the original' : `returns ${refusal}`} cannot adopt or use bare cleanup`, async () => {
    const requests: V1Request[] = [], retired: Readonly<{id: string; generation: number}>[] = [];
    let claims = 0, finished = 0;
    const body = activeChannelBody('ps_refused');
    const control = new V1ChannelControl({key: () => 'channel-install-key-0002', pollMs: 60_000, http: {send: async request => {
      requests.push(request); return {status: 201, headers: {}, body};
    }}});
    try {
      await control.open(channel, {state: () => assert.fail('no sink adoption'), error: message => assert.fail(message)}, {
        captureStart: original => ({isCurrent: original.isCurrent, capturePreparing: () => undefined,
          claimInstall: session => {
            claims++; assert.ok(Object.isFrozen(session));
            if (refusal === 'throw') throw new Error('claim unavailable');
            if (refusal === 'retire') {control.close(); return true;}
            return false;
          }, retireUninstalled: session => {retired.push(session);}, finish: () => {finished++;}}),
      });
      await settle();
      assert.equal(claims, 1); assert.equal(finished, 1);
      assert.deepEqual(retired, [{id: 'ps_refused', generation: 1}]);
      assert.ok(Object.isFrozen(retired[0]));
      assert.equal(await control.observe(1), false, 'no internal session was installed');
      control.pause(); control.goLive(); await settle();
      assert.deepEqual(requests.map(request => request.method), ['POST'], 'no go-live, controls, report, or bare DELETE');
    } finally {control.close();}
  });
}

test('channel close reports a refused Stop without leaving its completion barrier unhandled', async () => {
  const server = new ChannelServer(), errors: string[] = [];
  let deletes = 0;
  const control = new V1ChannelControl({pollMs: 5, key: () => 'channel-refused-stop-0001', http: {
    send: async request => {
      if (request.method === 'DELETE') {deletes++;throw new Error('Playback authority retired.');}
      return server.send(request);
    },
    acknowledgeUserMutation: id => server.acknowledgeUserMutation(id),
  }});
  try {
    await control.open(channel, {state: () => {}, error: value => {errors.push(value);}});
    control.close();
    await settle();
    assert.equal(deletes, 1);
    assert.equal(errors.length, 1, 'the original Stop refusal reaches the owner');
    assert.deepEqual(server.consumed, [], 'refusal cannot consume an unconfirmed Stop');
  } finally {control.close();}
});

test('resync rereads the current channel without another join, seek, start or mutation', async () => {
  const {EventsClient} = await import('../src/playback-v1/events.ts');
  const server = new ChannelServer(), feed = new EventsClient({http: server});
  const control = new V1ChannelControl({http: server, events: feed, key: () => 'channel-resync-key-000000', pollMs: 1_000_000});
  try {
    await control.open(channel, {state: () => {}, error: () => {}}); await settle();
    const before = server.log.length;
    feed.deliver({id: 'channel-gap', type: 'stream.resync'}); await settle();
    const requests = server.log.slice(before);
    assert.equal(requests.filter(r => r.method === 'GET').length, 1);
    assert.equal(requests.some(r => r.method !== 'GET'), false);
  } finally {control.close(); feed.resyncReads.dispose();}
});

// P2-61 E-web-14/25 regressions: a Live start answered at generation 0 with no URL (live QA sequence).
const earlyChannelHttp = (id: string, reads: Array<{generation: number; url: boolean}>, log: V1Request[]): V1Http => {
  let read = 0;
  const view = (generation: number, url: boolean) => {
    const b = channelBody(id, url ? 'active' : 'cold') as any;
    b.presentation.generation = generation;
    return b;
  };
  return {send: async request => {
    log.push(request);
    if (request.method === 'POST' && request.path === '/v1/playback/sessions') return {status: 201, headers: {}, body: view(0, false)};
    if (request.method === 'GET') { const r = reads[Math.min(read++, reads.length - 1)]; return {status: 200, headers: {}, body: view(r.generation, r.url)}; }
    if (request.method === 'PATCH') {
      const b = channelBody(id, 'active') as any;
      b.presentation.linear.seek = {id: (request.body as any).seek.id, positionMs: 60_000, acknowledged: false};
      return {status: 200, headers: {}, body: b};
    }
    if (request.method === 'POST' && request.path.endsWith('/observer')) return {status: 200, headers: {}, body: {epoch: 'channel-epoch-00000001', everyMs: 1000}};
    if (request.method === 'PUT') return {status: 200, headers: {}, body: {acceptedSeq: (request.body as any).seq, viewTag: 'v', session: channelBody(id, 'active')}};
    return {status: 204, headers: {}};
  }};
};

test('E-web-25: a start answered at generation 0 follows its session with GETs until the URL exists, then joins once', async () => {
  const log: V1Request[] = [], views: Array<{url?: string}> = [], errors: string[] = [];
  const control = new V1ChannelControl({key: () => 'channel-early-key-00001', pollMs: 60_000,
    http: earlyChannelHttp('ps_early', [{generation: 1, url: false}, {generation: 1, url: true}], log)});
  try {
    await control.open(channel, {state: (v: any) => views.push({url: v.linear?.media?.streamUrl}), error: m => errors.push(m)});
    await settle();
    assert.deepEqual(errors, []);
    assert.equal(log.filter(r => r.method === 'GET').length, 2, 'it polled until the URL existed');
    const joins = log.filter(r => r.method === 'PATCH' && (r.body as any).seek?.live === true);
    assert.equal(joins.length, 1, 'exactly one go-live PATCH');
    assert.ok(views.length > 0, 'the sink received the view');
    assert.ok(views.at(-1)?.url?.includes('/v1/media/linear/ps_early/1/'), 'the last view carries the stream url');
  } finally {control.close();}
});

test('E-web-25: an admitted start whose host refuses installation is a terminal error, never silent', async () => {
  const log: V1Request[] = [], errors: Array<[string, boolean | undefined]> = [];
  let retired = 0;
  const control = new V1ChannelControl({key: () => 'channel-refuse-key-0001', pollMs: 60_000, http: earlyChannelHttp('ps_ref', [{generation: 1, url: true}], log)});
  try {
    await control.open(channel, {state: () => assert.fail('no view is installed'), error: (m, t) => errors.push([m, t])}, {
      captureStart: original => ({isCurrent: original.isCurrent, capturePreparing: () => undefined, claimInstall: () => false,
        retireUninstalled: () => {retired++;}, finish: () => {}}),
    });
    await settle();
    assert.equal(errors.length, 1, 'the refusal reaches the sink');
    assert.equal(errors[0][1], true, 'as a terminal error');
    assert.equal(retired, 1);
  } finally {control.close();}
});

test('E-web-25: a cancelled start whose host refuses installation still ends quietly', async () => {
  const log: V1Request[] = [], errors: string[] = [];
  let admitted = true;
  const control = new V1ChannelControl({key: () => 'channel-cancel-key-0001', pollMs: 60_000, http: earlyChannelHttp('ps_can', [{generation: 1, url: true}], log)});
  try {
    await control.open(channel, {state: () => assert.fail('no view'), error: m => errors.push(m)}, {
      captureStart: original => ({isCurrent: () => admitted && original.isCurrent(), capturePreparing: () => undefined,
        claimInstall: () => {admitted = false; return false;}, retireUninstalled: () => {}, finish: () => {}}),
    });
    await settle();
    assert.deepEqual(errors, []);
  } finally {control.close();}
});

test('E-web-25: a stream that never appears fails terminally at CHANNEL_STREAM_WAIT_MS instead of waiting silently', async () => {
  const {CHANNEL_STREAM_WAIT_MS} = await import('../src/playback-v1/channel-control.ts');
  const log: V1Request[] = [], errors: Array<[string, boolean | undefined]> = [];
  const control = new V1ChannelControl({key: () => 'channel-timeout-key-001', pollMs: 60_000, http: earlyChannelHttp('ps_to', [{generation: 1, url: false}], log)});
  const realNow = Date.now;
  let calls = 0;
  const base = realNow();
  try {
    // The first reading sets the deadline; every later one is past it (no 60 s real wait).
    Date.now = () => base + (calls++ === 0 ? 0 : CHANNEL_STREAM_WAIT_MS + 1);
    await control.open(channel, {state: () => assert.fail('no stream, so no view'), error: (m, t) => errors.push([m, t])});
    Date.now = realNow;
    await settle();
    assert.equal(errors.length, 1);
    assert.equal(errors[0][1], true);
    assert.equal(log.some(r => r.method === 'PATCH'), false, 'never joined');
  } finally {Date.now = realNow; control.close();}
});

const stopBodies = (log: V1Request[]) => log.filter(r => r.method === 'DELETE').map(r => r.body);

test('E-web-25: Stop during a slow start at generation 0 sends DELETE at once with no generation', async () => {
  const log: V1Request[] = [];
  const control = new V1ChannelControl({key: () => 'channel-slowstop-key-01', pollMs: 60_000, http: earlyChannelHttp('ps_slow', [{generation: 0, url: false}], log)});
  const opening = control.open(channel, {state: () => {}, error: () => {}});
  try {
    await settle();
    assert.equal(log.some(r => r.method === 'POST' && r.path === '/v1/playback/sessions'), true);
    assert.equal(log.some(r => r.method === 'DELETE'), false);
    control.close();
    await settle();
    const deletes = log.filter(r => r.method === 'DELETE');
    assert.equal(deletes.length, 1, 'sent immediately, no timer advance');
    assert.ok(deletes[0].path.includes('ps_slow'));
    assert.equal((deletes[0].body as any)?.generation, undefined, 'no generation in the body');
  } finally {control.close(); await opening;}
});

test('E-web-25: Stop after a start at generation 1 sends DELETE with that generation', async () => {
  const log: V1Request[] = [];
  const control = new V1ChannelControl({key: () => 'channel-gen1stop-key-01', pollMs: 60_000, http: earlyChannelHttp('ps_g1', [{generation: 1, url: true}], log)});
  await control.open(channel, {state: () => {}, error: () => {}});
  await settle();
  control.close();
  await settle();
  assert.deepEqual(stopBodies(log), [{generation: 1}]);
});

test('E-web-25: a 412 on Stop with a generation retries once with no generation', async () => {
  const log: V1Request[] = [], errors: string[] = [];
  const inner = earlyChannelHttp('ps_412', [{generation: 1, url: true}], log);
  let deletes = 0;
  const control = new V1ChannelControl({key: () => 'channel-412stop-key-001', pollMs: 60_000, http: {send: async r => {
    if (r.method === 'DELETE' && deletes++ === 0) { log.push(r); return {status: 412, headers: {}, body: {error: {code: 'revision_mismatch'}}}; }
    return inner.send(r);
  }}});
  await control.open(channel, {state: () => {}, error: m => errors.push(m)});
  await settle();
  control.close();
  await settle();
  const bodies = stopBodies(log) as any[];
  assert.equal(bodies.length, 2, 'one retry only');
  assert.equal(bodies[0].generation, 1);
  assert.equal(bodies[1]?.generation, undefined);
  assert.deepEqual(errors, []);
});
