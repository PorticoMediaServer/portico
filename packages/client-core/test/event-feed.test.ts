import test from 'node:test';
import assert from 'node:assert/strict';
import {EventsClient, type RawStreamEvent, type StreamOpener} from '../src/playback-v1/events.ts';
import {eventStreamOpener, runEventFeed, sharedStreamOpener, type SharedStreamEnvironment} from '../src/event-feed.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';

const tick = async (n = 12) => { for (let i = 0; i < n; i++) await Promise.resolve(); };
const encoder = new TextEncoder();

/** A response whose body the test feeds piece by piece. */
function streamed(status = 200, headers: Record<string, string> = {}) {
  const waiting: ((piece: {value?: Uint8Array; done: boolean}) => void)[] = [];
  const queue: {value?: Uint8Array; done: boolean}[] = [];
  let cancelled = false;
  const give = (piece: {value?: Uint8Array; done: boolean}) => { const w = waiting.shift(); if (w) w(piece); else queue.push(piece); };
  const response = {
    ok: status >= 200 && status < 300, status, headers: new Headers(headers),
    body: {getReader: () => ({
      read: () => new Promise<{value?: Uint8Array; done: boolean}>(resolve => { const q = queue.shift(); if (q) resolve(q); else waiting.push(resolve); }),
      cancel: async () => { cancelled = true; give({done: true}); },
      releaseLock: () => {},
    }), cancel: async () => { cancelled = true; }},
  } as unknown as Response;
  return {response, send: (text: string) => give({value: encoder.encode(text), done: false}), end: () => give({done: true}), cancelled: () => cancelled};
}
function manualTimers() {
  let now = 0, next = 1;
  const timers = new Map<number, {at: number; fn: () => void}>();
  return {
    setTimer: (fn: () => void, ms: number) => { const id = next++; timers.set(id, {at: now + ms, fn}); return id; },
    clearTimer: (id: unknown) => { timers.delete(id as number); },
    async advance(ms: number) { now += ms; for (const [id, t] of [...timers]) if (t.at <= now) { timers.delete(id); t.fn(); } await tick(); },
  };
}

test('the opener sends the resume header, reads events and treats heartbeats as life', async () => {
  const timers = manualTimers(), s = streamed();
  let asked: Record<string, string> = {};
  const open = eventStreamOpener(async headers => { asked = {...headers}; return s.response; }, {...timers, firstByteMs: 1000, quietMs: 5000});
  const seen: RawStreamEvent[] = [];
  const running = open('41', event => seen.push(event), new AbortController().signal);
  await tick();
  assert.equal(asked['Last-Event-ID'], '41');
  assert.equal(asked.Accept, 'text/event-stream');
  s.send('retry: 1000\n\n');
  await tick();
  s.send('id: 42\nevent: library.changed\ndata: {"variant":"change","id":"42","type":"library.changed"}\n\n');
  await tick();
  assert.deepEqual(seen, [{id: '42', event: 'library.changed', data: '{"variant":"change","id":"42","type":"library.changed"}'}]);
  // Heartbeat comments keep the stream alive well past the quiet limit.
  for (let i = 0; i < 5; i++) { await timers.advance(4000); s.send(': keep-alive\n\n'); await tick(); }
  s.end();
  await running;
});

test('a stream that says nothing is a failure: no first bytes, or silence longer than the heartbeat', async () => {
  const timers = manualTimers(), held = streamed();
  const open = eventStreamOpener(async () => held.response, {...timers, firstByteMs: 1000, quietMs: 5000});
  const first = open(undefined, () => {}, new AbortController().signal);
  const failed = assert.rejects(first, {code: 'event_stream_silent'});
  await tick();
  await timers.advance(1500);
  await failed;
  assert.equal(held.cancelled(), true, 'the dead connection is let go');

  const quiet = streamed();
  const second = eventStreamOpener(async () => quiet.response, {...timers, firstByteMs: 1000, quietMs: 5000})(undefined, () => {}, new AbortController().signal);
  const later = assert.rejects(second, {code: 'event_stream_silent'});
  await tick();
  quiet.send('retry: 1000\n\n');
  await tick();
  await timers.advance(6000);
  await later;
});

test('a refusal carries its status and the wait the server asked for; stopping is not a failure', async () => {
  const open = eventStreamOpener(async () => streamed(503, {'Retry-After': '7'}).response);
  await assert.rejects(open(undefined, () => {}, new AbortController().signal), {status: 503, retryAfterMs: 7000});
  const s = streamed(), stop = new AbortController();
  const running = eventStreamOpener(async () => s.response)(undefined, () => {}, stop.signal);
  await tick();
  s.send('retry: 1000\n\n');
  stop.abort();
  await running;
});

test('the feed runs while the app is in front, stops a little after it leaves, and resumes from the last event', async () => {
  const timers = manualTimers();
  const cursors: (string | undefined)[] = [];
  let live: {onEvent: (e: RawStreamEvent) => void; end: () => void} | undefined;
  const openStream: StreamOpener = (lastEventId, onEvent, signal) => new Promise<void>(resolve => {
    cursors.push(lastEventId);
    live = {onEvent, end: resolve};
    signal.addEventListener('abort', () => resolve(), {once: true});
  });
  const events = new EventsClient({http: {send: async () => ({status: 200, headers: {}, body: {events: [], nextAfter: ''}})}, openStream, random: () => 0});
  const hub = new FreshnessHub({setTimer: timers.setTimer, clearTimer: timers.clearTimer});
  let visible = true, busy = false;
  const visibility = new Set<() => void>();
  const stop = runEventFeed(events, hub, {...timers, visible: () => visible, onVisibility: fn => { visibility.add(fn); return () => { visibility.delete(fn); }; }, busy: () => busy, graceMs: 30_000});
  await tick();
  assert.equal(events.getStatus().mode, 'stream');
  live!.onEvent({id: '7', data: JSON.stringify({variant: 'change', id: '7', type: 'library.changed', resource: {kind: 'library', id: 'films'}})});
  assert.equal(events.getStatus().cursor, '7');
  // Out of sight: documents stop reading at once, the feed is held for the grace period and then stopped.
  visible = false; for (const fn of visibility) fn();
  assert.equal(hub.isActive(), false);
  await timers.advance(10_000);
  assert.equal(events.getStatus().mode, 'stream');
  await timers.advance(25_000);
  assert.equal(events.getStatus().mode, 'stopped');
  assert.equal(cursors.length, 1, 'a stopped feed makes no requests');
  await timers.advance(600_000);
  assert.equal(cursors.length, 1);
  // Back in front: one connection, from the last event it saw.
  visible = true; for (const fn of visibility) fn();
  await tick();
  assert.equal(hub.isActive(), true);
  assert.deepEqual(cursors, [undefined, '7']);
  // Playing while hidden keeps the feed.
  busy = true; visible = false; for (const fn of visibility) fn();
  await timers.advance(120_000);
  assert.equal(events.getStatus().mode, 'stream');
  stop();
  assert.equal(events.getStatus().mode, 'stopped');
});

/** Windows of one browser: a shared lock queue and a channel that reaches every other window. */
function browser() {
  const queue: {signal: AbortSignal; work: () => Promise<void>; resolve: () => void; reject: (e: unknown) => void}[] = [];
  let held = false;
  const pump = () => {
    if (held) return;
    const next = queue.shift();
    if (!next) return;
    if (next.signal.aborted) { next.reject(new Error('aborted')); pump(); return; }
    held = true;
    void next.work().then(next.resolve, next.reject).finally(() => { held = false; pump(); });
  };
  const windows = new Set<(message: unknown) => void>();
  const tab = (): SharedStreamEnvironment => ({
    lock: (_name, signal, work) => new Promise<void>((resolve, reject) => {
      const entry = {signal, work, resolve, reject};
      signal.addEventListener('abort', () => { const i = queue.indexOf(entry); if (i >= 0) { queue.splice(i, 1); reject(new Error('aborted')); } }, {once: true});
      queue.push(entry); pump();
    }),
    channel: () => {
      let mine: ((message: unknown) => void) | undefined;
      return {
        post: message => { for (const w of [...windows]) if (w !== mine) queueMicrotask(() => w(JSON.parse(JSON.stringify(message)))); },
        listen: listener => { mine = listener; windows.add(listener); return () => { windows.delete(listener); }; },
        close: () => { if (mine) windows.delete(mine); },
      };
    },
  });
  return {tab};
}

test('two windows of one browser share one stream; the second takes over from the last event when the first leaves', async () => {
  const b = browser();
  const opened: {tab: string; from?: string; emit: (e: RawStreamEvent) => void; fail: (e: Error) => void}[] = [];
  const real = (tab: string): StreamOpener => (lastEventId, onEvent, signal) => new Promise<void>((resolve, reject) => {
    opened.push({tab, from: lastEventId, emit: onEvent, fail: reject});
    signal.addEventListener('abort', () => resolve(), {once: true});
  });
  const seenA: string[] = [], seenB: string[] = [];
  const stopA = new AbortController(), stopB = new AbortController();
  const a = sharedStreamOpener('feed', real('a'), b.tab())(undefined, e => { if (e.id) seenA.push(e.id); }, stopA.signal);
  await tick();
  let aliveB = 0;
  const bRun = sharedStreamOpener('feed', real('b'), b.tab())(undefined, e => { if (e.id) seenB.push(e.id); else aliveB++; }, stopB.signal);
  await tick();
  assert.equal(opened.length, 1, 'only the first window opens a stream');
  assert.ok(aliveB > 0, 'the second window is told the stream is alive');
  opened[0]!.emit({id: '1', data: '{"variant":"change","id":"1","type":"x"}'});
  opened[0]!.emit({id: '2', data: '{"variant":"change","id":"2","type":"x"}'});
  await tick();
  assert.deepEqual(seenA, ['1', '2']);
  assert.deepEqual(seenB, ['1', '2'], 'the second window hears every event');
  // The first window is closed (or hidden long enough to stop its feed).
  stopA.abort();
  await a;
  await tick();
  // The second window now holds the stream itself, from the last event it heard, so the server replays anything in between.
  assert.equal(opened.length, 2);
  assert.deepEqual([opened[1]!.tab, opened[1]!.from], ['b', '2']);
  opened[1]!.emit({id: '3', data: '{"variant":"change","id":"3","type":"x"}'});
  assert.deepEqual(seenB, ['1', '2', '3']);
  stopB.abort();
  await bRun;
});

test('a holder whose stream fails hands on: the next window tries, and a window that only listens is told the feed is down', async () => {
  const b = browser();
  const fails: ((e: Error) => void)[] = [];
  const real: StreamOpener = (_last, _on, signal) => new Promise<void>((resolve, reject) => { fails.push(reject); signal.addEventListener('abort', () => resolve(), {once: true}); });
  const first = sharedStreamOpener('feed', real, b.tab())(undefined, () => {}, new AbortController().signal);
  await tick();
  const second = sharedStreamOpener('feed', real, b.tab())(undefined, () => {}, new AbortController().signal);
  const third = sharedStreamOpener('feed', real, b.tab())(undefined, () => {}, new AbortController().signal);
  await tick();
  assert.equal(fails.length, 1);
  const outcomes = Promise.allSettled([first, second, third]);
  fails[0]!(Object.assign(new Error('network'), {code: 'network'}));
  await tick();
  // The server is down: whichever window took the stream next fails too.
  for (const fail of fails.slice(1)) fail(Object.assign(new Error('network'), {code: 'network'}));
  await tick();
  for (const fail of fails.slice(2)) fail(Object.assign(new Error('network'), {code: 'network'}));
  const settled = await outcomes;
  assert.deepEqual(settled.map(r => r.status), ['rejected', 'rejected', 'rejected'], 'no window is left believing the feed is up');
  assert.equal((settled[0] as PromiseRejectedResult).reason.code, 'network');
});

test('a holder that loses its lease (a plain-HTTP page) stops its stream and ends as a lost lock', async () => {
  const lease = new AbortController();
  const env: SharedStreamEnvironment = {
    lock: (_name, _signal, work) => work(lease.signal),
    channel: () => ({post: () => {}, listen: () => () => {}, close: () => {}}),
  };
  let streamSignal: AbortSignal | undefined;
  const real: StreamOpener = (_last, _on, signal) => new Promise<void>(resolve => { streamSignal = signal; signal.addEventListener('abort', () => resolve(), {once: true}); });
  const run = sharedStreamOpener('feed', real, env)(undefined, () => {}, new AbortController().signal);
  await tick();
  assert.ok(streamSignal && !streamSignal.aborted);
  lease.abort();
  await assert.rejects(run, (error: {code?: string}) => error.code === 'event_stream_lock');
  assert.ok(streamSignal!.aborted, 'the stream this window held is closed');
});

test('an overlapping SSE reconnect retries streaming without falling back to polling', async () => {
 const timers: (() => void)[]=[];
 let opened=0, polls=0;
 const client=new EventsClient({
  http:{send:async()=>{polls++;throw new Error('unexpected poll');}},
  openStream:async(_cursor,_event,signal)=>{
   opened++;
   if(opened===1)throw Object.assign(new Error('previous stream retired'),{status:409,code:'event_stream_exists'});
   await new Promise<void>(resolve=>signal.addEventListener('abort',()=>resolve(),{once:true}));
  },
  setTimer:(fn)=>{timers.push(fn);return fn;},clearTimer:()=>{},random:()=>0,
 });
 client.start();await tick();
 assert.equal(opened,1);assert.equal(timers.length,1);
 timers.shift()!();await tick();
 assert.equal(opened,2);assert.equal(polls,0);assert.equal(client.getStatus().mode,'stream');
 client.stop();await tick();
});

test('a follower joining after missed events resyncs its documents without moving its cursor',async()=>{
 const b=browser(),stopA=new AbortController(),stopB=new AbortController();
 let emit!:(event:RawStreamEvent)=>void;
 const real:StreamOpener=(_last,onEvent,signal)=>new Promise<void>(resolve=>{emit=onEvent;signal.addEventListener('abort',()=>resolve(),{once:true});});
 const a=sharedStreamOpener('feed',real,b.tab())('1',()=>{},stopA.signal);await tick();
 emit({id:'2',data:JSON.stringify({variant:'change',id:'2',type:'library.changed'})});await tick();
 const events=new EventsClient({cursor:'1',http:{send:async()=>{throw new Error('unexpected poll');}},openStream:sharedStreamOpener('feed',real,b.tab())});
 let resyncs=0;events.onResync(()=>resyncs++);events.start();await tick();
 try {assert.equal(resyncs,1);assert.equal(events.getStatus().cursor,'1','a local invalidation is not a received server event');
  emit({id:'3',data:JSON.stringify({variant:'change',id:'3',type:'library.changed'})});await tick();assert.equal(events.getStatus().cursor,'3');
 }finally{events.stop();stopA.abort();stopB.abort();await a;await tick();}
});

test('malformed long-poll documents fail into backoff instead of an immediate retry loop',async()=>{
 for(const body of [{},{events:'wrong',nextAfter:'1'},{events:[],nextAfter:7},{events:[],nextAfter:'x'.repeat(129)},{events:Array(513).fill(null),nextAfter:'1'}]){
  let calls=0;const waits:number[]=[];
  const events=new EventsClient({http:{send:async()=>{calls++;return calls===1?{status:200,headers:{},body}:new Promise(()=>{});}},setTimer:(_fn,ms)=>{waits.push(ms);return 1;},clearTimer:()=>{},random:()=>0});
  events.start();await tick();events.stop();
  assert.equal(calls,1);assert.equal(waits.length,1);assert.ok(waits[0]!>0);
 }
});

test('opt-in retained ownership keeps the background feed indefinitely without making display active', async () => {
  const timers = manualTimers();
  let visible = true, retained = false, opens = 0, ended = 0;
  const visibility = new Set<() => void>(), retention = new Set<() => void>();
  const events = new EventsClient({http: {send: async () => { throw new Error('unused'); }}, openStream: async (_last, _event, signal) => {
    opens++; await new Promise<void>(resolve => signal.addEventListener('abort', () => { ended++; resolve(); }, {once: true}));
  }});
  const hub = new FreshnessHub(timers);
  const stop = runEventFeed(events, hub, {...timers, visible: () => visible, busy: () => false,
    onVisibility: listener => { visibility.add(listener); return () => { visibility.delete(listener); }; },
    retain: () => retained, onRetain: listener => { retention.add(listener); return () => { retention.delete(listener); }; }});
  retained = true; retention.forEach(fn => fn()); visible = false; visibility.forEach(fn => fn());
  await timers.advance(24 * 60 * 60 * 1000);
  assert.equal(opens, 1); assert.equal(ended, 0); assert.equal(hub.isActive(), false);
  retained = false; retention.forEach(fn => fn());
  await timers.advance(29999); assert.equal(ended, 0);
  retained = true; retention.forEach(fn => fn()); await timers.advance(30000); assert.equal(ended, 0);
  retained = false; retention.forEach(fn => fn()); await timers.advance(30000); assert.equal(ended, 1);
  stop(); assert.equal(visibility.size, 0); assert.equal(retention.size, 0); hub.dispose();
});
