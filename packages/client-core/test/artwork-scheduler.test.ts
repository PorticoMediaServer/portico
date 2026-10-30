import test from 'node:test';
import assert from 'node:assert/strict';
import {ArtworkScheduler, ArtworkUnavailableError, artworkWidthBucket} from '../src/artwork/index.ts';

const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(setImmediate); };

function harness(options: {concurrency?: number; priorityExtra?: number; maxEntries?: number; onScreenSettled?: (s: unknown) => void} = {}) {
  const pending = new Map<string, {resolve: (v: string) => void; reject: (e: unknown) => void; signal: AbortSignal}[]>();
  const started: string[] = [];
  const released: string[] = [];
  let clock = 0;
  const timers: {fn: () => void; at: number; id: number}[] = [];
  let nextTimer = 0;
  const scheduler = new ArtworkScheduler<string>({
    load: (key, signal) => new Promise((resolve, reject) => { started.push(key); const list = pending.get(key) ?? []; list.push({resolve, reject, signal}); pending.set(key, list); }),
    classify: e => ({retryable: (e as {retryable?: boolean}).retryable ?? true, retryAfterSeconds: (e as {retryAfter?: number}).retryAfter}),
    release: v => released.push(v),
    concurrency: options.concurrency ?? 2,
    priorityExtra: options.priorityExtra ?? 1,
    maxEntries: options.maxEntries ?? 100,
    timeoutMs: 1_000_000,
    onScreenSettled: options.onScreenSettled,
    now: () => clock,
    setTimer: (fn, ms) => { const id = ++nextTimer; timers.push({fn, at: clock + ms, id}); return id; },
    clearTimer: id => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); },
  });
  const finish = (key: string, value = `url:${key}`) => { const p = pending.get(key)?.shift(); p?.resolve(value); };
  const fail = (key: string, error: unknown) => { const p = pending.get(key)?.shift(); p?.reject(error); };
  const advance = (ms: number) => { clock += ms; for (const t of timers.filter(x => x.at <= clock)) { timers.splice(timers.indexOf(t), 1); t.fn(); } };
  return {scheduler, started, released, finish, fail, advance, pending, tick: (ms: number) => { clock += ms; }};
}

test('respects the slot limit and lets high priority use the extra slot', async () => {
  const h = harness({concurrency: 2, priorityExtra: 1});
  for (const k of ['a', 'b', 'c', 'd']) void h.scheduler.load(k).catch(() => {});
  await settle();
  assert.deepEqual(h.started, ['a', 'b']);
  void h.scheduler.load('hero', {priority: 'high'});
  await settle();
  assert.deepEqual(h.started, ['a', 'b', 'hero'], 'the hero starts at once in the priority slot');
  h.finish('a'); await settle();
  assert.deepEqual(h.started.slice(3), [], 'the hero still occupies a slot');
  h.finish('hero'); await settle();
  assert.deepEqual(h.started.slice(3), ['c']);
});

test('queued work nobody waits for is dropped before it starts', async () => {
  const h = harness({concurrency: 1});
  void h.scheduler.load('a');
  const gone = new AbortController();
  const dropped = h.scheduler.load('b', {signal: gone.signal});
  gone.abort();
  await assert.rejects(dropped, {name: 'AbortError'});
  void h.scheduler.load('c');
  await settle();
  h.finish('a'); await settle();
  assert.deepEqual(h.started, ['a', 'c']);
});

test('retries after Retry-After or backoff without holding a slot, and callers keep waiting', async () => {
  const h = harness({concurrency: 1});
  const poster = h.scheduler.load('slow');
  await settle();
  h.fail('slow', {retryable: true, retryAfter: 20});
  await settle();
  void h.scheduler.load('next');
  await settle();
  assert.deepEqual(h.started, ['slow', 'next'], 'the retry wait does not block the queue');
  h.finish('next'); await settle();
  h.advance(5000);
  assert.equal(h.started.length, 2, 'server asked for 20 s, longer than the 5 s backoff');
  h.advance(15000); await settle();
  assert.deepEqual(h.started, ['slow', 'next', 'slow']);
  h.finish('slow');
  assert.equal(await poster, 'url:slow');
  assert.equal(h.scheduler.peek('slow'), 'url:slow');
});

test('after the retries run out the key is failed for a cooldown', async () => {
  const h = harness();
  const p = h.scheduler.load('x');
  await settle();
  for (const wait of [5000, 15000, 45000]) { h.fail('x', {retryable: true}); await settle(); h.advance(wait); await settle(); }
  h.fail('x', {retryable: true});
  await assert.rejects(p);
  await assert.rejects(h.scheduler.load('x'), ArtworkUnavailableError);
  h.advance(61000);
  void h.scheduler.load('x');
  await settle();
  assert.equal(h.started.filter(k => k === 'x').length, 5);
});

test('an attempt nobody waits for any more does not fail the key', async () => {
  const h = harness();
  const gone = new AbortController();
  const leaving = h.scheduler.load('poster', {signal: gone.signal});
  await settle();
  gone.abort();
  await assert.rejects(leaving, {name: 'AbortError'});
  // The loader settles after its caller left. The native image reports only through the
  // mounted view, so this is exactly what a card unmounted mid-load looks like: the
  // request ends with nobody waiting, at the timeout or before it.
  h.fail('poster', {retryable: false});
  await settle();
  const again = h.scheduler.load('poster');
  await settle();
  assert.deepEqual(h.started, ['poster', 'poster'], 'a later mount asks the server again at once');
  h.finish('poster');
  assert.equal(await again, 'url:poster');
});

test('the cache is bounded and releases least recently used values', async () => {
  const h = harness({concurrency: 10, maxEntries: 2});
  for (const k of ['a', 'b', 'c']) { const p = h.scheduler.load(k); await settle(); h.finish(k); await p; }
  assert.deepEqual(h.released, ['url:a']);
  assert.equal(h.scheduler.peek('a'), undefined);
  assert.equal(h.scheduler.peek('c'), 'url:c');
});

test('dispose rejects waiters, aborts requests and releases values', async () => {
  const h = harness();
  const done = h.scheduler.load('a'); await settle(); h.finish('a'); await done;
  const waiting = h.scheduler.load('b');
  await settle();
  h.scheduler.dispose();
  await assert.rejects(waiting, {name: 'AbortError'});
  assert.ok(h.pending.get('b')![0]!.signal.aborted);
  assert.deepEqual(h.released, ['url:a']);
});

/** A deterministic shuffle. */
function shuffled<T>(items: T[], seed = 11): T[] {
  const out = [...items];
  for (let i = out.length - 1; i > 0; i--) { seed = (seed * 1103515245 + 12345) % 2147483648; const j = seed % (i + 1); [out[i], out[j]] = [out[j]!, out[i]!]; }
  return out;
}

test('200 keys asked for in random order start in visual order at concurrency 6, whatever order they finish in', async () => {
  const h = harness({concurrency: 6});
  const keys = Array.from({length: 200}, (_, i) => `k${i}`);
  for (const k of shuffled(keys)) void h.scheduler.load(k, {order: Number(k.slice(1))});
  await settle();
  assert.deepEqual(h.started, keys.slice(0, 6), 'one render pass is ordered as a batch before anything starts');
  let finished = 0;
  while (finished < 200) {
    // Finish a random in-flight request, not the oldest.
    const inFlight = h.started.filter(k => h.pending.get(k)?.length);
    h.finish(shuffled(inFlight, finished + 3)[0]!); finished++;
    await settle();
    assert.ok(h.scheduler.stats().active <= 6);
  }
  assert.deepEqual(h.started, keys);
});

test('reorder moves waiting keys when the viewport scrolls; the priority lane stays ahead', async () => {
  const h = harness({concurrency: 1, priorityExtra: 0});
  void h.scheduler.load('blocker', {order: 0});
  await settle();
  for (let i = 1; i <= 5; i++) void h.scheduler.load(`k${i}`, {order: i});
  h.scheduler.reorder('k5', -1);
  void h.scheduler.load('k4', {order: -2});
  void h.scheduler.load('hero', {priority: 'high', order: 99});
  await settle();
  for (const k of ['blocker', 'hero', 'k4', 'k5', 'k1', 'k2']) { h.finish(k); await settle(); }
  assert.deepEqual(h.started, ['blocker', 'hero', 'k4', 'k5', 'k1', 'k2', 'k3']);
});

test('many reorders stay cheap and correct', async () => {
  const h = harness({concurrency: 1, priorityExtra: 0});
  void h.scheduler.load('blocker');
  await settle();
  for (let i = 0; i < 100; i++) void h.scheduler.load(`k${i}`, {order: i});
  for (let pass = 0; pass < 50; pass++) for (let i = 0; i < 100; i++) h.scheduler.reorder(`k${i}`, (i * 37 + pass) % 100 + pass * 100);
  // Final pass: order = (i*37+49) % 100 + 4900; k with the lowest value first.
  const expected = Array.from({length: 100}, (_, i) => i).sort((a, b) => (a * 37 + 49) % 100 - (b * 37 + 49) % 100).map(i => `k${i}`);
  h.finish('blocker'); await settle();
  for (const k of expected) { h.finish(k); await settle(); }
  assert.deepEqual(h.started.slice(1), expected);
});

test('with groups, a started row finishes before the next row starts', async () => {
  const run = async (grouped: boolean) => {
    const h = harness({concurrency: 2, priorityExtra: 0});
    // Row B (below) is on screen first and starts.
    for (let c = 0; c < 4; c++) void h.scheduler.load(`B${c}`, {order: 2000 + c, group: grouped ? 'rowB' : undefined});
    await settle();
    // Scrolling up reveals row A, above it.
    for (let c = 0; c < 4; c++) void h.scheduler.load(`A${c}`, {order: 1000 + c, group: grouped ? 'rowA' : undefined});
    await settle();
    while (h.started.length < 8) { const k = h.started.find(x => h.pending.get(x)?.length)!; h.finish(k); await settle(); }
    return h.started;
  };
  assert.deepEqual(await run(true), ['B0', 'B1', 'B2', 'B3', 'A0', 'A1', 'A2', 'A3']);
  assert.deepEqual(await run(false), ['B0', 'B1', 'A0', 'A1', 'A2', 'A3', 'B2', 'B3']);
});

test('screen stats: time to first image and to all visible', async () => {
  const reports: unknown[] = [];
  const h = harness({concurrency: 6, onScreenSettled: s => reports.push(s)});
  const cached = h.scheduler.load('seen'); await settle(); h.finish('seen'); await cached;
  h.tick(1000);
  h.scheduler.beginScreen('home');
  void h.scheduler.load('seen');
  for (const k of ['a', 'b', 'c']) void h.scheduler.load(k, {order: k.charCodeAt(0)}).catch(() => {});
  await settle();
  assert.equal(h.scheduler.stats().screen?.firstImageMs, 0, 'a cache hit is an image');
  assert.equal(h.scheduler.stats().screen?.allVisibleMs, undefined);
  h.tick(120); h.finish('a'); await settle();
  h.tick(200); h.fail('b', {retryable: false}); await settle();
  h.tick(80); h.finish('c'); await settle();
  assert.deepEqual(h.scheduler.stats().screen, {label: 'home', requested: 4, cached: 1, failed: 1, pending: 0, firstImageMs: 0, allVisibleMs: 400});
  assert.equal(reports.length, 1);
  h.scheduler.beginScreen('library');
  const gone = new AbortController();
  void h.scheduler.load('d', {signal: gone.signal}).catch(() => {});
  h.tick(50); gone.abort(); await settle();
  assert.equal(h.scheduler.stats().screen?.firstImageMs, undefined);
  assert.equal(h.scheduler.stats().screen?.pending, 0);
});

test('artworkWidthBucket picks one server variant per rendered size, by the long edge', () => {
  assert.equal(artworkWidthBucket(300), 400);
  assert.equal(artworkWidthBucket(360, {aspect: 2 / 3}), 800, 'a 180 px poster at 2x is 540 px tall');
  assert.equal(artworkWidthBucket(430), 400, 'within 10% of a variant uses it');
  assert.equal(artworkWidthBucket(1280 * 2, {aspect: 16 / 9}), 2560, 'capped at the largest');
  assert.equal(artworkWidthBucket(0), 400);
  assert.equal(artworkWidthBucket(Number.NaN), 400);
  assert.equal(artworkWidthBucket(500, {buckets: [256, 512]}), 512);
});
