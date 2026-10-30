import test from 'node:test';
import assert from 'node:assert/strict';
import {createWindowedCollection, type FetchPage} from '../src/collections/index.ts';

type Item = {id: string; n: number};
const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(setImmediate); };

/** A synthetic server: items are computed from the offset, so nothing here is O(total) either. */
function server(total: number, options: {prefix?: string; delay?: boolean; positionIndex?: Record<string, number>; fail?: (start: number) => boolean} = {}) {
  const calls: {start: number; count: number; signal: AbortSignal}[] = [];
  const held: {resolve: () => void}[] = [];
  const fetchPage: FetchPage<Item> = async (start, count, signal) => {
    calls.push({start, count, signal});
    if (options.delay) await new Promise<void>(resolve => held.push({resolve}));
    if (signal.aborted) throw Object.assign(new Error('aborted'), {name: 'AbortError'});
    if (options.fail?.(start)) throw new Error('boom');
    const n = Math.max(0, Math.min(count, total - start));
    const items: Item[] = [];
    for (let i = 0; i < n; i++) items.push({id: `${options.prefix ?? 'i'}${start + i}`, n: start + i});
    return {items, total, positionIndex: options.positionIndex};
  };
  return {fetchPage, calls, release: () => { for (const h of held.splice(0)) h.resolve(); }};
}

test('the first page sizes the collection; placeholders have stable keys', async () => {
  const s = server(1_000_000);
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 50, maxResidentPages: 8, keyOf: i => i.id});
  assert.equal(c.getSnapshot().total, undefined);
  assert.equal(c.keyAt(10), 'placeholder:10');
  c.ensureRange(0, 30);
  await settle();
  assert.equal(c.getSnapshot().total, 1_000_000);
  assert.equal(c.getSnapshot().status, 'ready');
  assert.equal(c.itemAt(10)?.n, 10);
  assert.equal(c.keyAt(10), 'i10');
  assert.equal(c.keyAt(999_999), 'placeholder:999999');
  assert.deepEqual(c.slotAt(999_998), {kind: 'placeholder', index: 999_998, key: 'placeholder:999998'});
  assert.equal(s.calls[0]!.start, 0);
});

test('1M items with random jumps: resident items stay within the budget and nothing is O(total)', async () => {
  const total = 1_000_000, pageSize = 60, maxResidentPages = 6;
  const s = server(total);
  // Catch any O(total) allocation through the array constructors.
  const RealArray = Array;
  const realFrom = Array.from;
  let biggest = 0;
  (globalThis as {Array: ArrayConstructor}).Array = new Proxy(RealArray, {construct(target, args) { if (typeof args[0] === 'number') biggest = Math.max(biggest, args[0]); return Reflect.construct(target, args); }, apply(target, self, args) { if (typeof args[0] === 'number') biggest = Math.max(biggest, args[0]); return Reflect.apply(target, self, args); }});
  RealArray.from = ((value: {length?: number}, ...rest: unknown[]) => { if (value && typeof value.length === 'number') biggest = Math.max(biggest, value.length); return (realFrom as (...a: unknown[]) => unknown[]).call(RealArray, value, ...rest); }) as typeof Array.from;
  try {
    const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize, maxResidentPages, keyOf: i => i.id});
    let seed = 7;
    const random = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
    for (let jump = 0; jump < 200; jump++) {
      const first = Math.floor(random() * (total - 40));
      c.ensureRange(first, first + 39);
      await settle();
      const {items, pages} = c.resident();
      assert.ok(items <= maxResidentPages * pageSize, `jump ${jump}: ${items} resident items`);
      assert.ok(pages <= maxResidentPages);
      assert.equal(c.itemAt(first)?.n, first, `item at ${first}`);
      assert.equal(c.itemAt(first + 39)?.n, first + 39);
    }
    assert.ok(biggest < total, `an array of length ${biggest} was created`);
    assert.ok(s.calls.length < 200 * 6, `${s.calls.length} page requests`);
  } finally {
    (globalThis as {Array: ArrayConstructor}).Array = RealArray;
    RealArray.from = realFrom;
  }
});

test('a billion-item collection costs the same as a small one', async () => {
  const s = server(1_000_000_000);
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 5, keyOf: i => i.id});
  const before = process.memoryUsage().heapUsed;
  for (const first of [0, 999_999_900, 500_000_000, 123_456_789]) { c.ensureRange(first, first + 50); await settle(); }
  assert.equal(c.itemAt(123_456_800)?.n, 123_456_800);
  assert.ok(c.resident().items <= 5 * 100);
  assert.ok(process.memoryUsage().heapUsed - before < 20 * 1024 * 1024);
});

test('prefetches one page either side, two ahead in the direction of travel; dedupes in-flight pages', async () => {
  const s = server(10_000, {delay: true});
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 20, keyOf: i => i.id, maxConcurrent: 10});
  c.ensureRange(0, 50);
  c.ensureRange(0, 60);
  assert.deepEqual(s.calls.map(x => x.start), [0], 'before the total is known only the visible page is requested, once');
  s.release(); await settle();
  assert.deepEqual(s.calls.map(x => x.start), [0, 100], 'once the total is known, one page ahead');
  s.release(); await settle();
  let before = s.calls.length;
  c.ensureRange(1000, 1050); // page 10, moving forward
  await settle();
  assert.deepEqual(s.calls.slice(before).map(x => x.start / 100).sort((a, b) => a - b), [9, 10, 11, 12]);
  s.release(); await settle();
  before = s.calls.length;
  c.ensureRange(500, 550); // page 5, moving backward
  await settle();
  assert.deepEqual(s.calls.slice(before).map(x => x.start / 100).sort((a, b) => a - b), [3, 4, 5, 6]);
});

test('pages that leave interest are aborted', async () => {
  const s = server(100_000, {delay: true});
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 10, keyOf: i => i.id});
  c.ensureRange(0, 10);
  s.release(); await settle();
  c.ensureRange(50_000, 50_050);
  await settle();
  const far = s.calls.filter(x => x.start >= 49_000);
  assert.ok(far.length > 0);
  c.ensureRange(90_000, 90_050);
  await settle();
  assert.ok(far.every(x => x.signal.aborted), 'abandoned pages were aborted');
  s.release(); await settle();
  assert.equal(c.itemAt(90_000)?.n, 90_000);
  assert.equal(c.itemAt(50_000), undefined);
});

test('per-page subscriptions fire only for their page', async () => {
  const s = server(10_000);
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 10, keyOf: i => i.id, prefetchPages: 0});
  let page0 = 0, page7 = 0;
  c.subscribePage(0, () => page0++);
  c.subscribePage(7, () => page7++);
  const v0 = c.getPageVersion(0);
  c.ensureRange(0, 10); await settle();
  assert.ok(page0 > 0);
  assert.equal(page7, 0);
  assert.notEqual(c.getPageVersion(0), v0);
  const v0Loaded = c.getPageVersion(0);
  c.ensureRange(700, 710); await settle();
  assert.ok(page7 > 0);
  assert.equal(c.getPageVersion(0), v0Loaded, 'page 0 is unchanged while page 7 loads');
});

test('invalidate keeps the old generation on screen until the new first page arrives, then swaps atomically', async () => {
  const s = server(10_000, {prefix: 'old'});
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 10, keyOf: i => i.id});
  c.ensureRange(200, 250); await settle();
  assert.equal(c.itemAt(200)?.id, 'old200');
  const next = server(500, {prefix: 'new', delay: true});
  let coarse = 0;
  c.subscribe(() => coarse++);
  c.invalidate(next.fetchPage);
  await settle();
  assert.equal(c.itemAt(200)?.id, 'old200', 'no flash to empty');
  assert.equal(c.getSnapshot().refreshing, true);
  assert.equal(c.getSnapshot().total, 10_000);
  assert.equal(next.calls[0]!.start, 200, 'the new generation starts where the viewer is');
  next.release(); await settle();
  assert.equal(c.itemAt(200)?.id, 'new200');
  assert.equal(c.getSnapshot().total, 500);
  assert.equal(c.getSnapshot().generation, 1);
  assert.equal(c.getSnapshot().refreshing, false);
  assert.ok(coarse > 0);
});

test('letter jumps use the position index', async () => {
  const s = server(26_000, {positionIndex: {A: 0, M: 12_000, Z: 25_000}});
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 10, keyOf: i => i.id});
  c.ensureRange(0, 20); await settle();
  const m = c.indexOfLetter('m');
  assert.equal(m, 12_000);
  c.ensureRange(m!, m! + 20); await settle();
  assert.equal(c.itemAt(12_000)?.n, 12_000);
  assert.equal(c.indexOfLetter('Q'), undefined);
});

test('a failed visible page reports an error, keeps other pages and retries', async () => {
  let failing = true;
  const s = server(1000, {fail: start => failing && start === 100});
  let clock = 0;
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 10, keyOf: i => i.id, prefetchPages: 0, now: () => clock});
  c.ensureRange(0, 10); await settle();
  c.ensureRange(100, 110); await settle();
  assert.equal(c.getSnapshot().status, 'error');
  assert.ok(c.getSnapshot().error instanceof Error);
  failing = false;
  c.ensureRange(100, 110); await settle();
  assert.equal(c.getSnapshot().status, 'error', 'not retried before retryAfterMs');
  clock = 6000;
  c.ensureRange(100, 110); await settle();
  assert.equal(c.getSnapshot().status, 'ready');
  assert.equal(c.itemAt(100)?.n, 100);
});

test('a page the server refused with a wait is not asked for again, even after scrolling away and back, until the wait is over', async () => {
  let failing = true;
  let clock = 0;
  const starts: number[] = [];
  const c = createWindowedCollection<{id: string; n: number}>({
    fetchPage: async (start, count) => {
      starts.push(start);
      if (failing && start === 100) throw Object.assign(new Error('busy'), {status: 503, retryAfterSeconds: 60});
      return {items: Array.from({length: count}, (_, i) => ({id: 'i' + (start + i), n: start + i})), total: 1000};
    },
    pageSize: 100, maxResidentPages: 10, keyOf: i => i.id, prefetchPages: 0, now: () => clock,
  });
  c.ensureRange(0, 10); await settle();
  c.ensureRange(100, 110); await settle();
  assert.equal(c.getSnapshot().status, 'error');
  failing = false;
  // Past the collection's own five seconds, and away and back (which forgets the failed page): still inside the server's minute.
  clock = 20_000;
  c.ensureRange(0, 10); await settle();
  c.ensureRange(100, 110); await settle();
  assert.deepEqual(starts.filter(start => start === 100).length, 1, 'no second request inside the wait');
  clock = 61_000;
  c.ensureRange(100, 110); await settle();
  assert.equal(c.itemAt(100)?.n, 100);
});

test('Try again from the viewer is not held back by the server wait', async () => {
  let failing = true;
  let clock = 0;
  const c = createWindowedCollection<{id: string; n: number}>({
    fetchPage: async (start, count) => {
      if (failing) throw Object.assign(new Error('busy'), {status: 429, retryAfterMs: 120_000});
      return {items: Array.from({length: count}, (_, i) => ({id: 'i' + (start + i), n: start + i})), total: 1000};
    },
    pageSize: 100, maxResidentPages: 10, keyOf: i => i.id, prefetchPages: 0, now: () => clock,
  });
  c.ensureRange(0, 10); await settle();
  assert.equal(c.getSnapshot().status, 'error');
  failing = false;
  c.retry(); await settle();
  assert.equal(c.itemAt(0)?.n, 0);
});

test('dispose aborts work in flight and stops notifying', async () => {
  const s = server(1000, {delay: true});
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 100, maxResidentPages: 10, keyOf: i => i.id});
  let calls = 0;
  c.subscribe(() => calls++);
  c.ensureRange(0, 10);
  c.dispose();
  assert.ok(s.calls[0]!.signal.aborted);
  const before = calls;
  s.release(); await settle();
  assert.equal(calls, before);
});

test('a quiet read swaps in whole, at the same place, and only once every page in view has arrived', async () => {
  const first = server(500), second = server(501, {prefix: 'n', delay: true});
  const c = createWindowedCollection({fetchPage: first.fetchPage, pageSize: 50, maxResidentPages: 8, keyOf: i => i.id});
  c.ensureRange(40, 70); // the view spans pages 0 and 1
  await settle();
  const generation = c.getSnapshot().generation;
  const statuses: string[] = [];
  c.subscribe(() => statuses.push(c.getSnapshot().status + ':' + c.getSnapshot().refreshing));
  const quiet = c.revalidate({fetchPage: second.fetchPage});
  await settle();
  assert.equal(c.itemAt(45)!.id, 'i45', 'the list on screen is still the old one');
  assert.equal(c.getSnapshot().refreshing, false, 'no refreshing state is shown for a quiet read');
  second.release();
  await quiet;
  assert.equal(c.itemAt(45)!.id, 'n45');
  assert.equal(c.itemAt(65)!.id, 'n65', 'both pages in view arrived before the swap: no placeholder flashes');
  assert.equal(c.getSnapshot().total, 501);
  assert.equal(c.getSnapshot().generation, generation + 1);
  assert.ok(statuses.every(s => s === 'ready:false'), statuses.join());
});

test('a quiet read that fails leaves the list exactly as it was and rejects', async () => {
  const good = server(500), bad = server(500, {fail: () => true});
  const c = createWindowedCollection({fetchPage: good.fetchPage, pageSize: 50, maxResidentPages: 8, keyOf: i => i.id});
  c.ensureRange(0, 30);
  await settle();
  const before = c.getSnapshot();
  await assert.rejects(c.revalidate({fetchPage: bad.fetchPage}), /boom/);
  assert.equal(c.getSnapshot(), before);
  assert.equal(c.itemAt(3)!.id, 'i3');
  // The list goes on working: scrolling still loads from the list on screen.
  c.ensureRange(200, 230);
  await settle();
  assert.equal(c.itemAt(205)!.id, 'i205');
  // Nothing shown yet: there is nothing to keep, so a quiet read is not needed.
  const empty = createWindowedCollection({fetchPage: good.fetchPage, pageSize: 50, maxResidentPages: 8, keyOf: i => i.id});
  await empty.revalidate();
});

test('a quiet read waits for the pause it is given, and a new filter supersedes it', async () => {
  const first = server(100), second = server(100, {prefix: 'n'}), third = server(100, {prefix: 'f'});
  const c = createWindowedCollection({fetchPage: first.fetchPage, pageSize: 50, maxResidentPages: 8, keyOf: i => i.id});
  c.ensureRange(0, 10);
  await settle();
  let release!: () => void; const pause = new Promise<void>(r => { release = r; });
  const quiet = c.revalidate({fetchPage: second.fetchPage, hold: () => pause});
  await settle();
  assert.equal(c.itemAt(0)!.id, 'i0', 'the person is mid-press: nothing moves yet');
  release(); await quiet;
  assert.equal(c.itemAt(0)!.id, 'n0');
  let hold!: () => void; const held = new Promise<void>(r => { hold = r; });
  const superseded = c.revalidate({fetchPage: first.fetchPage, hold: () => held});
  await settle();
  c.invalidate(third.fetchPage);
  await superseded;
  await settle();
  hold();
  await settle();
  assert.equal(c.itemAt(0)!.id, 'f0');
});

test('an open-ended total grows as pages arrive and settles on the end', async () => {
  // 10,000+ matches: each page reports the threshold or one page past what it has seen.
  const actual = 260;
  const fetchPage: FetchPage<Item> = async (start, count) => {
    const n = Math.max(0, Math.min(count, actual - start));
    const items: Item[] = [];
    for (let i = 0; i < n; i++) items.push({id: `i${start + i}`, n: start + i});
    const more = start + n < actual;
    return {items, total: more ? Math.max(100, start + n + count) : start + n, totalIsLowerBound: more};
  };
  const c = createWindowedCollection({fetchPage, pageSize: 50, maxResidentPages: 8, keyOf: i => i.id});
  c.ensureRange(0, 20);
  await settle();
  const first = c.getSnapshot().total!;
  assert.ok(first >= 100);
  assert.equal(c.getSnapshot().totalIsLowerBound, true);
  c.ensureRange(140, 149);
  await settle();
  const grown = c.getSnapshot().total!;
  assert.ok(grown > first && grown >= 200, 'the total grew past the deepest page read');
  c.ensureRange(0, 10);
  await settle();
  assert.equal(c.getSnapshot().total, grown, 'an earlier page does not shrink it back');
  c.ensureRange(240, 259);
  await settle();
  assert.equal(c.getSnapshot().total, 260);
  assert.equal(c.getSnapshot().totalIsLowerBound, false, 'the last page knows the end');
});

test('a pinned item keeps its page past the budget, is loaded when absent, and a new query clears the pin', async () => {
  const s = server(10_000);
  const c = createWindowedCollection({fetchPage: s.fetchPage, pageSize: 10, maxResidentPages: 2, prefetchPages: 0, keyOf: i => i.id});
  c.ensureRange(0, 5);
  await settle();
  c.pin(3);
  for (const first of [100, 200, 300, 400]) { c.ensureRange(first, first + 5); await settle(); }
  assert.equal(c.itemAt(3)?.n, 3, 'the focused item survived four pages of eviction');
  assert.ok(c.resident().pages <= 3, 'only the pinned page goes past the budget');
  // Moving the pin releases the old page to the ordinary budget.
  c.pin(405);
  c.ensureRange(500, 505); await settle();
  c.ensureRange(600, 605); await settle();
  assert.equal(c.itemAt(3), undefined);
  assert.equal(c.itemAt(405)?.n, 405);
  // A pin on a page that is not in memory loads it.
  c.pin(5_000); await settle();
  assert.equal(c.itemAt(5_000)?.n, 5_000);
  c.invalidate(server(10_000, {prefix: 'b'}).fetchPage); await settle();
  c.ensureRange(700, 705); await settle();
  c.ensureRange(800, 805); await settle();
  c.ensureRange(900, 905); await settle();
  assert.equal(c.itemAt(5_000), undefined, 'a new filter or sort clears the pin');
  c.pin(-1); c.pin(Number.NaN); c.pin(undefined);
  c.dispose();
});
