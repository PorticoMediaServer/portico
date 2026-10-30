import test from 'node:test';
import assert from 'node:assert/strict';
import {FreshnessHub, dependsOn, isSharedReadSuperseded, type FreshnessInterest, type InterestOutcome} from '../src/freshness-hub.ts';
import {FreshnessHistory, FRESHNESS_HISTORY_BYTES, FRESHNESS_HISTORY_ENTRIES, sharedResultBytes, SHARED_RESULT_BYTES} from '../src/freshness-memory.ts';
import {EventsClient} from '../src/playback-v1/events.ts';

const tick = async () => { for (let i = 0; i < 40; i++) await Promise.resolve(); };
function clock() {
  let now = 0, next = 0;
  const tasks = new Map<number, {at: number; fn: () => void}>();
  const delays: number[] = [];
  return {
    env: {now: () => now, random: () => 0,
      setTimer(fn: () => void, ms: number) { assert.ok(Number.isFinite(ms)); delays.push(ms); const id = ++next; tasks.set(id, {at: now + ms, fn}); return id; },
      clearTimer(id: unknown) { tasks.delete(id as number); }},
    delays, pending: () => tasks.size,
    async advance(ms: number) {
      const end = now + ms;
      for (;;) {
        const due = [...tasks].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at || a[0] - b[0])[0];
        if (!due) break;
        tasks.delete(due[0]); now = due[1].at; due[1].fn();
        // Intentionally drain each individual timer, not all timers at one deadline.
        await tick();
      }
      now = end; await tick();
    },
  };
}
const answered: InterestOutcome = {kind: 'answered'};
const gate = <T>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return {promise, resolve}; };
const server = [dependsOn.settings('server')];

test('interests gather hints without input settlement and preserve post-dispatch demand', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), first = gate<InterestOutcome>();
  let reads = 0;
  const interest = hub.interest({name: 'server', dependencies: server, read: async () => ++reads === 1 ? first.promise : answered});
  hub.touch();
  for (let i = 0; i < 10; i++) interest.invalidate();
  await c.advance(399); assert.equal(reads, 0);
  hub.touch(); await c.advance(1); assert.equal(reads, 1);
  interest.invalidate(); first.resolve(answered); await tick();
  assert.equal(interest.getState().owed, true);
  await c.advance(400); assert.equal(reads, 2); assert.equal(interest.getState().owed, false);
  hub.dispose(); assert.equal(c.pending(), 0);
});

test('retained interests run in background only while open; defaults remain foreground only', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let display = 0, authority = 0;
  const a = hub.interest({name: 'display', dependencies: [], read: async () => { display++; return answered; }});
  const b = hub.interest({name: 'authority', dependencies: [], background: 'whenRetained', read: async () => { authority++; return answered; }});
  hub.setActive(false); a.setRetained(true); b.setRetained(true); a.invalidate(); b.invalidate();
  await c.advance(400); assert.deepEqual([display, authority], [0, 1]);
  b.setOpen(false); b.invalidate(); await c.advance(10000); assert.equal(authority, 1);
  b.setOpen(true); await c.advance(400); assert.equal(authority, 2);
  hub.setActive(true); await c.advance(400); assert.equal(display, 1);
  hub.dispose(); assert.equal(c.pending(), 0);
});

test('a paused read restores its captured debt and ignores a late completion', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), result = gate<InterestOutcome>();
  let reads = 0, signal!: AbortSignal;
  const a = hub.interest({name: 'pause', dependencies: [], read: async ctx => { signal = ctx.signal; return ++reads === 1 ? result.promise : answered; }});
  a.invalidate(); await c.advance(400); a.setOpen(false);
  assert.equal(signal.aborted, true); result.resolve(answered); await tick();
  assert.equal(a.getState().owed, true); assert.equal(a.getState().reading, false);
  a.setOpen(true); await c.advance(400); assert.equal(reads, 2); assert.equal(a.getState().owed, false);
  hub.dispose();
});

test('deferred write gates park debt until invalidation, while supersession retries live owners', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let reads = 0;
  const a = hub.interest({name: 'gate', dependencies: [], read: async () => {
    reads++; return reads === 1 ? {kind: 'deferred'} : reads === 2 ? {kind: 'superseded'} : answered;
  }});
  a.invalidate(); await c.advance(60000); assert.equal(reads, 1); assert.equal(a.getState().owed, true);
  a.invalidate(); await c.advance(800); assert.equal(reads, 3); assert.equal(a.getState().owed, false);
  a.dispose(); await c.advance(60000); assert.equal(reads, 3); hub.dispose();
});

test('terminal failure consumes only its ticket and never clears a newer pending hint', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), result = gate<InterestOutcome>();
  let reads = 0;
  const a = hub.interest({name: 'terminal', dependencies: [], read: async () => ++reads === 1 ? result.promise : answered});
  a.invalidate(); await c.advance(400); a.invalidate();
  result.resolve({kind: 'failed', error: new Error('unsupported role'), retryable: false}); await tick();
  assert.equal(a.getState().failures, 1); assert.equal(a.getState().owed, true);
  await c.advance(400); assert.equal(reads, 2); assert.equal(a.getState().failures, 0);
  hub.dispose();
});

test('thrown endpoint errors preserve retry classification; malformed guide proof stays owed', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let terminal = 0, malformed = 0;
  const a = hub.interest({name: '404', dependencies: [], read: async () => { terminal++; throw {status: 404}; }});
  const b = hub.interest({name: 'malformed', dependencies: [], read: async () => { if (++malformed === 1) throw {code: 'invalid_guide_directory', retryable: false}; return answered; }});
  a.invalidate(); b.invalidate(); await c.advance(2800);
  assert.equal(terminal, 1); assert.equal(a.getState().owed, false); assert.equal(a.getState().failures, 1);
  assert.equal(malformed, 2); assert.equal(b.getState().owed, false); hub.dispose();
});

test('two-hour server floor survives hide/show, wake, eviction and new catch-up hints', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let reads = 0;
  const error = Object.assign(new Error('busy'), {status: 429, retryAfterSeconds: 7200});
  const a = hub.interest({name: 'floor', dependencies: server, read: async () => { if (++reads === 1) throw error; return answered; }});
  a.invalidate(); await c.advance(400);
  assert.equal(a.getState().deferredUntil, 7_200_400);
  a.setOpen(false); hub.setActive(false); await c.advance(10000);
  hub.note('eviction:'.repeat(70000));
  a.invalidate(); a.setOpen(true); hub.setActive(true); a.invalidate();
  await c.advance(7_189_999); assert.equal(reads, 1);
  await c.advance(1); assert.equal(reads, 2); hub.dispose();
});

test('deferred absolute deadlines chunk safely, rechecking the floor at every chunk', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), deadline = 2_147_000_000 * 2 + 4000;
  let reads = 0;
  const a = hub.interest({name: 'long', dependencies: [], read: async () => ++reads === 1 ? {kind: 'deferred', nextAllowedAt: deadline} : answered});
  a.invalidate(); await c.advance(400); await c.advance(2_147_000_000);
  assert.equal(reads, 1); assert.ok(c.delays.every(ms => ms <= 2_147_000_000));
  a.invalidate(); await c.advance(deadline - c.env.now() - 1); assert.equal(reads, 1);
  await c.advance(1); assert.equal(reads, 2); hub.dispose();
});

for (const metadata of [{retryAfterSeconds: Infinity}, {retryAfterSeconds: Number.MAX_VALUE}, {retryAfterMs: Infinity}]) {
  test(`positive overflow floor saturates rather than retrying early: ${Object.keys(metadata)[0]}=${Object.values(metadata)[0]}`, async () => {
    const c = clock(), hub = new FreshnessHub(c.env); let reads = 0;
    const a = hub.interest({name: 'overflow', dependencies: [], read: async () => { reads++; throw Object.assign(new Error('busy'), metadata); }});
    a.invalidate(); await c.advance(400); assert.equal(a.getState().deferredUntil, Number.MAX_VALUE);
    hub.setActive(false); a.invalidate(); hub.setActive(true); await c.advance(2_147_000_000 * 2);
    assert.equal(reads, 1); assert.ok(c.delays.every(ms => ms <= 2_147_000_000)); hub.dispose();
  });
}

test('nonpositive/invalid retry metadata does not replace local backoff; jitter cannot shorten a floor', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, random: () => 1}); let reads = 0;
  const a = hub.interest({name: 'metadata', dependencies: [], read: async () => {
    reads++; if (reads === 1) throw {retryAfterMs: NaN, retryAfterSeconds: -1}; if (reads === 2) throw {retryAfterMs: 4000}; return answered;
  }});
  a.invalidate(); await c.advance(500); assert.equal(reads, 1);
  await c.advance(2499); assert.equal(reads, 1); await c.advance(1); assert.equal(reads, 2);
  await c.advance(4000); assert.equal(reads, 2); await c.advance(2250); assert.equal(reads, 3); hub.dispose();
});

test('immediately completed guide/preferences share across separately fired planned gather callbacks', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let guide = 0, preferences = 0;
  const values: unknown[] = [];
  const interests = [[dependsOn.libraries], server, [dependsOn.list('liveSources')], [dependsOn.list('libraryChannels')]].map((dependencies, i) => hub.interest({
    name: `group${i}`, dependencies, read: async ctx => {
      values.push(await ctx.shared('guide', async () => ({guide: ++guide})));
      values.push(await ctx.shared('preferences:web', async () => ({preferences: ++preferences})));
      return answered;
    },
  }));
  for (const interest of interests) interest.invalidate();
  await c.advance(400); assert.deepEqual([guide, preferences], [1, 1]);
  assert.equal(values.length, 8); assert.ok(interests.every(a => !a.getState().owed));
  // The cohort is released; explicit catch-up starts one new read per key.
  for (const interest of interests) interest.invalidate();
  await c.advance(400); assert.deepEqual([guide, preferences], [2, 2]); hub.dispose();
});

test('relevant explicit invalidation after dispatch supersedes old cohorts for early and late borrowers', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), old = gate<string>(); let reads = 0;
  const publications: string[] = [], superseded: unknown[] = []; let attempts = 0;
  const factory = async () => ++reads === 1 ? old.promise : 'current';
  const make = (name: string): FreshnessInterest => hub.interest({name, dependencies: server, read: async ctx => {
    attempts++; try { publications.push(await ctx.shared('guide', factory)); return answered; }
    catch (error) { superseded.push(error); throw error; }
  }});
  const a = make('early'), b = make('late');
  a.invalidate(); await c.advance(400);
  // No hub.note/server event: the new contract must own this catch-up witness.
  b.invalidate(); await c.advance(400);
  assert.equal(reads, 1); await c.advance(10000); assert.equal(attempts, 2);
  assert.equal(c.pending(), 0, 'waiting for the obsolete network read needs no retry timer');
  old.resolve('obsolete'); await tick(); assert.deepEqual(publications, []);
  assert.ok(superseded.every(isSharedReadSuperseded));
  await c.advance(800); assert.equal(reads, 2); assert.deepEqual(publications, ['current', 'current']);
  assert.ok(!a.getState().owed && !b.getState().owed); hub.dispose();
});

test('resync and relevant server events reject stale common results without treating them as HTTP failures', async () => {
  for (const move of [(hub: FreshnessHub) => hub.resync(), (hub: FreshnessHub) => hub.accept({id: 'hint', type: 'list.changed', resource: {kind: 'list', id: 'liveSources'}})]) {
    const c = clock(), hub = new FreshnessHub(c.env), result = gate<string>(); let reads = 0, published = 0;
    const a = hub.interest({name: 'guide', dependencies: [dependsOn.list('liveSources')], read: async ctx => {
      await ctx.shared('guide', async () => ++reads === 1 ? result.promise : 'new'); published++; return answered;
    }});
    a.invalidate(); await c.advance(400); move(hub); result.resolve('old'); await tick();
    assert.equal(published, 0); assert.equal(a.getState().failures, 0); assert.equal(a.getState().owed, true);
    await c.advance(400); assert.equal(published, 1); assert.equal(reads, 2); hub.dispose();
  }
});

test('one borrower cancellation cannot abort another; all borrowers withdrawing aborts the owned I/O', async () => {
  for (const cancelAll of [false, true]) {
    const c = clock(), hub = new FreshnessHub(c.env), result = gate<string>(); let io!: AbortSignal, reads = 0, published = 0;
    const make = (name: string): FreshnessInterest => hub.interest({name, dependencies: server, read: async ctx => {
      await ctx.shared('guide', async signal => { reads++; io = signal; return result.promise; }); published++; return answered;
    }});
    const a = make('a'), b = make('b'); a.invalidate(); b.invalidate(); await c.advance(400);
    a.setOpen(false); await tick(); assert.equal(io.aborted, false);
    if (cancelAll) { b.dispose(); await tick(); assert.equal(io.aborted, true); }
    result.resolve('ok'); await tick(); assert.equal(reads, 1); assert.equal(published, cancelAll ? 0 : 1);
    assert.equal(a.getState().owed, true); hub.dispose();
  }
});

test('pre-aborted shared calls start no factory and retired interests start no later read', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let factories = 0;
  let a!: FreshnessInterest;
  a = hub.interest({name: 'preabort', dependencies: [], read: async ctx => {
    a.setOpen(false);
    await ctx.shared('guide', async () => ++factories); return answered;
  }});
  a.invalidate(); await c.advance(400); assert.equal(factories, 0); a.dispose();
  a.invalidate(); a.setOpen(true); await c.advance(60000); assert.equal(factories, 0);
  hub.dispose(); const dead = hub.interest({name: 'dead', dependencies: [], read: async () => { factories++; return answered; }});
  dead.invalidate(); await c.advance(60000); assert.equal(factories, 0);
});

test('independent inputs publish separately while shared failures retain their original error and ticket', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let guides = 0, preferences = 0, confirmed = 0;
  const original = Object.assign(new Error('busy guide'), {retryAfterMs: 5000}); const errors: unknown[] = [];
  const interests = [0, 1].map(i => hub.interest({name: `partial${i}`, dependencies: server, read: async ctx => {
    const [guide, prefs] = await Promise.allSettled([
      ctx.shared('guide', async () => { guides++; throw original; }),
      ctx.shared('preferences:web', async () => { preferences++; return 'preferences'; }),
    ]);
    if (prefs.status === 'fulfilled') confirmed++;
    if (guide.status === 'rejected') { errors.push(guide.reason); return {kind: 'failed', error: guide.reason, retryable: true}; }
    return answered;
  }}));
  interests.forEach(a => a.invalidate()); await c.advance(400);
  assert.deepEqual([guides, preferences, confirmed], [1, 1, 2]); assert.ok(errors.every(e => e === original));
  assert.ok(interests.every(a => a.getState().owed && a.getState().deferredUntil === 5400));
  await c.advance(4999); assert.equal(guides, 1); await c.advance(1); assert.equal(guides, 2); hub.dispose();
});

test('oversized completed results reach current borrowers but are not retained for later planned borrowers', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let factories = 0, confirmed = 0;
  const value = 'x'.repeat(SHARED_RESULT_BYTES);
  const interests = [0, 1].map(i => hub.interest({name: `large${i}`, dependencies: server, read: async ctx => {
    assert.equal(await ctx.shared('guide', async () => { factories++; return value; }), value); confirmed++; return answered;
  }}));
  interests.forEach(a => a.invalidate()); await c.advance(400);
  assert.equal(factories, 2); assert.equal(confirmed, 2); hub.dispose();
});

test('two completed shared keys obey one aggregate byte cap and evict oldest result first', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let guides = 0, preferences = 0;
  const value = 'x'.repeat(80000);
  const a = hub.interest({name: 'first', dependencies: server, read: async ctx => {
    await ctx.shared('guide', async () => { guides++; return value; });
    await ctx.shared('preferences:web', async () => { preferences++; return value; }); return answered;
  }});
  const b = hub.interest({name: 'next', dependencies: server, read: async ctx => {
    await ctx.shared('preferences:web', async () => { preferences++; return value; });
    await ctx.shared('guide', async () => { guides++; return value; }); return answered;
  }});
  a.invalidate(); b.invalidate(); await c.advance(400);
  assert.deepEqual([guides, preferences], [2, 1]); hub.dispose();
});

test('combined dependency/counter history enforces entry AND byte limits with oldest-first eviction', () => {
  const history = new FreshnessHistory();
  for (let i = 0; i < FRESHNESS_HISTORY_ENTRIES; i++) assert.equal(history.set(`v:${i}`, i), false);
  assert.equal(history.set('counter', 'new'), true); assert.equal(history.get('v:0'), undefined); assert.equal(history.get('v:1'), 1);
  history.clear();
  for (let i = 0; i < 10; i++) history.set(`counter:${i}`, 'x'.repeat(50000));
  assert.ok(history.usage().entries < 10); assert.ok(history.usage().bytes <= FRESHNESS_HISTORY_BYTES);
  assert.equal(history.get('counter:0'), undefined); assert.ok(history.get('counter:9'));
  assert.equal(history.set('huge', 'x'.repeat(FRESHNESS_HISTORY_BYTES)), true);
  assert.equal(history.has('huge'), false); assert.ok(history.usage().bytes <= FRESHNESS_HISTORY_BYTES);
});

test('accounting handles cycles and refuses non-data graphs without invoking getters', () => {
  const cycle: {self?: unknown} = {}; cycle.self = cycle;
  assert.ok(sharedResultBytes(cycle) < SHARED_RESULT_BYTES);
  assert.ok(sharedResultBytes({get value() { throw new Error('must not execute'); }}) > SHARED_RESULT_BYTES);
  assert.ok(sharedResultBytes(new Map()) > SHARED_RESULT_BYTES);
});

test('history eviction invalidates documents, watchers and interests without version ABA or lost in-flight debt', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), first = gate<InterestOutcome>(); let reads = 0, watched = 0;
  const dependency = 'keep'; hub.note(dependency); const before = hub.version(dependency);
  const doc = hub.document({dependencies: ['unrelated'], revalidate: async () => {}});
  hub.watch(['unrelated'], () => watched++);
  const a = hub.interest({name: 'eviction', dependencies: ['unrelated'], read: async () => ++reads === 1 ? first.promise : answered});
  a.invalidate(); await c.advance(400);
  for (let i = 0; i < FRESHNESS_HISTORY_ENTRIES + 1; i++) hub.note(`transient:${i}`);
  assert.equal(doc.getState().stale, true); assert.ok(watched > 0); assert.ok(hub.version(dependency) > before);
  const forgotten = hub.version(dependency); hub.note(dependency); assert.ok(hub.version(dependency) > forgotten);
  first.resolve(answered); await tick(); assert.equal(a.getState().owed, true);
  await c.advance(400); assert.equal(reads, 2); hub.dispose();
});

test('byte eviction alone triggers normal refetch and invalidates an active shared cohort', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), first = gate<string>(); let factories = 0, publications = 0;
  const a = hub.interest({name: 'byte-bound', dependencies: server, read: async ctx => {
    await ctx.shared('guide', async () => ++factories === 1 ? first.promise : 'new'); publications++; return answered;
  }});
  a.invalidate(); await c.advance(400);
  hub.note('long'.repeat(FRESHNESS_HISTORY_BYTES)); first.resolve('old'); await tick();
  assert.equal(publications, 0); assert.equal(a.getState().owed, true);
  await c.advance(400); assert.equal(factories, 2); assert.equal(publications, 1); hub.dispose();
});

test('ordinary feed dedupe eviction stays local; an older replay follows normal delivery', () => {
  const c = clock(), hub = new FreshnessHub(c.env), events = new EventsClient({http: {send: async () => { throw new Error('unused'); }}});
  hub.attach(events);
  const a = hub.interest({name: 'feed', dependencies: ['unrelated'], read: async () => answered});
  const doc = hub.document({dependencies: ['unrelated'], revalidate: async () => {}});
  let watched = 0; hub.watch(['unrelated'], () => watched++);
  for (let i = 0; i < 512; i++) events.deliver({id: String(i), type: 'notification.changed'});
  assert.equal(doc.getState().stale, false); assert.equal(a.getState().owed, false);
  events.deliver({id: '512', type: 'notification.changed'});
  assert.equal(doc.getState().stale, false); assert.equal(a.getState().owed, false); assert.equal(watched, 0);
  let replayed = 0; events.onAny(event => { if (event.id === '0') replayed++; });
  events.deliver({id: '0', type: 'notification.changed'}); assert.equal(replayed, 1);
  events.deliver({id: '0', type: 'notification.changed'}); assert.equal(replayed, 1);
  assert.equal(events.getStatus().cursor, '0');
  events.deliver({id: 'x'.repeat(1024 * 1024), type: 'notification.changed'});
  assert.equal(events.getStatus().cursor, '0', 'an invalid platform envelope cannot retain an oversized cursor');
  hub.dispose();
});

test('far-future floored interests cannot pin another gathered cohort result', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let factories = 0, flooredReads = 0;
  const waiting = hub.interest({name: 'floored', dependencies: server, read: async () => { flooredReads++; return {kind: 'deferred', nextAllowedAt: 7_200_000}; }});
  waiting.invalidate(); await c.advance(400);
  const current = hub.interest({name: 'current', dependencies: server, read: async ctx => {
    await ctx.shared('guide', async () => ++factories); return answered;
  }});
  current.invalidate(); await c.advance(400); assert.equal(factories, 1);
  current.invalidate(); await c.advance(400); assert.equal(factories, 2);
  assert.equal(flooredReads, 1); hub.dispose();
});

test('a completed obsolete shared result releases network ownership before display settlement', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), display = gate<void>(); let factories = 0;
  const first = hub.interest({name: 'display', dependencies: server, read: async ctx => {
    await ctx.shared('guide', async () => ++factories); await display.promise; return answered;
  }});
  const second = hub.interest({name: 'authority', dependencies: server, read: async ctx => {
    await ctx.shared('guide', async () => ++factories); return answered;
  }});
  first.invalidate(); await c.advance(400); second.invalidate(); await c.advance(400);
  assert.equal(factories, 2); assert.equal(first.getState().reading, true); assert.equal(second.getState().owed, false);
  display.resolve(); await tick(); hub.dispose();
});

test('ordinary Documents keep their ten-minute retry cap rather than adopting the interest floor', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let reads = 0;
  const doc = hub.document({dependencies: [], revalidate: async () => { if (++reads === 1) throw {retryAfterSeconds: 7200}; }});
  doc.setOpen(true); doc.invalidate(); await c.advance(400);
  await c.advance(599999); assert.equal(reads, 1);
  await c.advance(1); assert.equal(reads, 2); hub.dispose();
});

test('superseded shared failures preserve server floors for both original and waiting borrowers', async () => {
  for (const key of ['guide', 'preferences:web'] as const) {
    const c = clock(), hub = new FreshnessHub(c.env), response = gate<unknown>(); let factories = 0;
    const error = Object.assign(new Error('server floor'), {retryAfterSeconds: 7200});
    const make = (name: string): FreshnessInterest => hub.interest({name, dependencies: server, read: async ctx => {
      await ctx.shared(key, async () => {
        factories++;
        const value = await response.promise;
        if (key === 'guide') throw value;
        return {kind: 'unreadable', error: value};
      });
      return answered;
    }});
    const a = make('original'), b = make('waiting'); a.invalidate(); await c.advance(400);
    b.invalidate(); await c.advance(400); response.resolve(error); await tick();
    assert.equal(a.getState().failures, 0); assert.equal(b.getState().failures, 0);
    assert.equal(a.getState().deferredUntil, 7_200_800); assert.equal(b.getState().deferredUntil, 7_200_800);
    a.invalidate(); b.invalidate(); hub.setActive(false); hub.setActive(true);
    await c.advance(7_199_999); assert.equal(factories, 1);
    hub.dispose();
  }
});

test('jittered planned gather callbacks still share one immediate guide request', async () => {
  const c = clock(); let random = 0, factories = 0;
  const hub = new FreshnessHub({...c.env, random: () => [0, 0.5, 1][random++] ?? 0});
  const interests = [server, [dependsOn.list('liveSources')], [dependsOn.list('libraryChannels')]].map((dependencies, i) =>
    hub.interest({name: `jitter${i}`, dependencies, read: async ctx => { await ctx.shared('guide', async () => ++factories); return answered; }}));
  interests.forEach(a => a.invalidate()); await c.advance(500); assert.equal(factories, 1); hub.dispose();
});

test('accounted results include non-enumerable and symbol data', () => {
  const value = 'x'.repeat(SHARED_RESULT_BYTES);
  assert.ok(sharedResultBytes(Object.defineProperty({}, 'hidden', {value})) > SHARED_RESULT_BYTES);
  assert.ok(sharedResultBytes({[Symbol('hidden')]: value}) > SHARED_RESULT_BYTES);
});

for (const key of ['guide', 'preferences:web'] as const) {
  test(`${key} floor survives switching groups, cohort release, wake and history eviction at its exact deadline`, async () => {
    const c = clock(), hub = new FreshnessHub(c.env); let factories = 0;
    const original = Object.assign(new Error('busy input'), {retryAfterSeconds: 7200});
    const blocked: unknown[] = [], failures: unknown[] = [];
    const make = (name: string, dependencies: readonly string[]): FreshnessInterest => hub.interest({name, dependencies, read: async ctx => {
      try {
        const result = await ctx.shared(key, async () => {
          if (++factories !== 1) return {kind: 'ok'};
          if (key === 'guide') throw original;
          return {kind: 'unreadable', error: original};
        });
        if (result.kind === 'unreadable') { failures.push(result.error); return {kind: 'failed', error: result.error, retryable: true}; }
        return answered;
      } catch (error) {
        if (isSharedReadSuperseded(error)) { blocked.push(error); throw error; }
        failures.push(error); return {kind: 'failed', error, retryable: true};
      }
    }});
    const first = make('server', server); first.invalidate(); await c.advance(400);
    assert.equal(first.getState().deferredUntil, 7_200_400); assert.deepEqual(failures, [original]);
    first.dispose(); // The entire original flight/result cohort has now gone away.
    await c.advance(1000);
    const second = make(key === 'guide' ? 'liveSources' : 'preferences',
      key === 'guide' ? [dependsOn.list('liveSources')] : [dependsOn.settings('preferences')]);
    second.invalidate(); await c.advance(400);
    assert.equal(factories, 1); assert.equal(second.getState().deferredUntil, 7_200_400);
    assert.equal(second.getState().failures, 0); assert.equal(second.getState().owed, true);
    hub.setActive(false); hub.resync(); hub.note('eviction:'.repeat(70000)); await c.advance(1000); hub.setActive(true);
    const third = make(key === 'guide' ? 'libraryChannels' : 'server-replacement',
      key === 'guide' ? [dependsOn.list('libraryChannels')] : server);
    third.invalidate(); await c.advance(400);
    assert.equal(factories, 1); assert.equal(third.getState().deferredUntil, 7_200_400);
    assert.ok(blocked.length >= 2 && blocked.every(isSharedReadSuperseded));
    second.invalidate(); third.invalidate();
    await c.advance(7_200_400 - c.env.now() - 1);
    assert.equal(factories, 1); assert.equal(second.getState().deferredUntil, 7_200_400);
    await c.advance(1);
    assert.equal(factories, 2, 'both groups share one request at the original absolute deadline');
    assert.equal(second.getState().owed, false); assert.equal(third.getState().owed, false);
    assert.deepEqual(failures, [original], 'a blocked borrower never sees a replayed HTTP failure');
    hub.dispose(); assert.equal(c.pending(), 0);
  });

  test(`${key} anchors Retry-After at response time despite delayed classification and later captured borrowers`, async () => {
    const c = clock(); let random = 0, factories = 0;
    const hub = new FreshnessHub({...c.env, random: () => [0, 1][random++] ?? 0}), classified = gate<void>();
    const original = Object.assign(new Error('busy'), {retryAfterSeconds: 7200}); const errors: unknown[] = [];
    const make = (name: string): FreshnessInterest => hub.interest({name, dependencies: server, read: async ctx => {
      let error: unknown;
      try {
        const result = await ctx.shared(key, async () => {
          factories++;
          if (key === 'guide') throw original;
          return {kind: 'unreadable', error: original};
        });
        error = result.error;
      } catch (failure) {
        if (isSharedReadSuperseded(failure)) throw failure;
        error = failure;
      }
      errors.push(error); await classified.promise;
      return {kind: 'failed', error, retryable: true};
    }});
    const a = make('first'), b = make('jittered'); a.invalidate(); b.invalidate();
    await c.advance(500); assert.equal(factories, 1); assert.deepEqual(errors, [original, original]);
    await c.advance(2000); classified.resolve(); await tick();
    assert.equal(a.getState().deferredUntil, 7_200_400); assert.equal(b.getState().deferredUntil, 7_200_400);
    a.dispose(); b.dispose();
    const next = make('new-group'); next.invalidate(); await c.advance(400);
    assert.equal(factories, 1); assert.equal(next.getState().deferredUntil, 7_200_400);
    hub.dispose();
  });

  test(`${key} retains only a scalar floor when an oversized error/result cannot be cached`, async () => {
    const c = clock(), hub = new FreshnessHub(c.env); let factories = 0;
    const original = Object.assign(new Error('large error'), {retryAfterMs: 10000, payload: 'x'.repeat(SHARED_RESULT_BYTES)});
    const make = (name: string): FreshnessInterest => hub.interest({name, dependencies: server, read: async ctx => {
      const result = await ctx.shared(key, async () => {
        factories++;
        if (key === 'guide') throw original;
        return {kind: 'unreadable', error: original};
      });
      return {kind: 'failed', error: result.error, retryable: true};
    }});
    const first = make('first'); first.invalidate(); await c.advance(400); first.dispose();
    const next = make('next'); next.invalidate(); await c.advance(400);
    assert.equal(factories, 1); assert.equal(next.getState().deferredUntil, 10400);
    hub.dispose();
  });

  for (const retry of [{retryAfterSeconds: Infinity}, {retryAfterSeconds: Number.MAX_VALUE}, {retryAfterMs: Infinity}]) {
    test(`${key} switching-group overflow floor saturates without inflation or early retry: ${Object.keys(retry)[0]}=${Object.values(retry)[0]}`, async () => {
      const c = clock(), hub = new FreshnessHub(c.env); let factories = 0;
      const original = Object.assign(new Error('overflow'), retry);
      const make = (name: string): FreshnessInterest => hub.interest({name, dependencies: server, read: async ctx => {
        const result = await ctx.shared(key, async () => {
          factories++;
          if (key === 'guide') throw original;
          return {kind: 'unreadable', error: original};
        });
        return {kind: 'failed', error: result.error, retryable: true};
      }});
      const first = make('first'); first.invalidate(); await c.advance(400); first.dispose();
      await c.advance(10000);
      const next = make('new-group'); next.invalidate(); await c.advance(400);
      assert.equal(next.getState().deferredUntil, Number.MAX_VALUE);
      hub.setActive(false); hub.resync(); hub.note('eviction:'.repeat(70000)); hub.setActive(true); next.invalidate();
      await c.advance(2_147_000_000 * 2);
      assert.equal(factories, 1); assert.equal(next.getState().deferredUntil, Number.MAX_VALUE);
      assert.ok(c.delays.every(ms => ms <= 2_147_000_000)); hub.dispose(); assert.equal(c.pending(), 0);
    });
  }
}

test('the two shared input floors remain independent', async () => {
  const c = clock(), hub = new FreshnessHub(c.env); let guide = 0, preferences = 0;
  const first = hub.interest({name: 'guide', dependencies: server, read: async ctx => {
    await ctx.shared('guide', async () => { guide++; throw {retryAfterSeconds: 7200}; }); return answered;
  }});
  first.invalidate(); await c.advance(400); first.dispose();
  const other = hub.interest({name: 'preferences', dependencies: server, read: async ctx => {
    await ctx.shared('preferences:web', async () => ++preferences); return answered;
  }});
  other.invalidate(); await c.advance(400);
  assert.deepEqual([guide, preferences], [1, 1]); assert.equal(other.getState().owed, false); hub.dispose();
});
