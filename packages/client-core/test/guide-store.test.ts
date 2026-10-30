import test from 'node:test';
import assert from 'node:assert/strict';
import {BLOCK_MS, DAY_MS, HOUR_MS, GuideWindowStore, layoutRow, normalizePrograms, pxPerMsFor, type GuideDataSource} from '../src/guide/index.ts';
import {demoChannels, demoPrograms, syntheticSource} from '../src/guide/testing/fixture.ts';

const settle = async () => { for (let i = 0; i < 30; i++) await new Promise(setImmediate); };
const START = Date.parse('2026-09-15T00:00:00Z');
const NOW = START + 7 * DAY_MS + 20 * HOUR_MS;

/** Wraps a source to record the peak number of concurrent requests. */
function peaked(source: GuideDataSource) {
  let live = 0, peak = 0;
  const wrap = <A extends unknown[], R>(fn: (...a: A) => Promise<R>) => async (...a: A) => { live++; peak = Math.max(peak, live); try { return await fn(...a); } finally { live--; } };
  return {source: {channels: wrap(source.channels), programs: wrap(source.programs)} as GuideDataSource, peak: () => peak};
}

test('1,000 channels × 14 days: the first screen loads only its tiles plus the margin', async () => {
  const source = syntheticSource({channels: 1000, start: START, days: 14, latency: () => new Promise(setImmediate)});
  const store = new GuideWindowStore({source});
  store.setViewport({firstRow: 0, lastRow: 11, start: NOW - 30 * 60_000, end: NOW + 150 * 60_000});
  await settle();
  assert.equal(store.getSnapshot().total, 1000);
  assert.equal(store.channelAt(0)!.id, 'c0');
  assert.equal(store.channelAt(11)!.name, 'Channel 11');
  const row = store.programsFor(3, NOW - 30 * 60_000, NOW + 150 * 60_000);
  assert.equal(row.complete, true);
  assert.ok(row.programs.length > 0);
  const r = store.resident();
  assert.ok(r.channelPages <= 2, `pages ${r.channelPages}`); // page 0 plus the margin page 1
  assert.ok(r.tiles <= 2 * 4, `tiles ${r.tiles}`); // 2 pages × (2 visible + 2 margin blocks)
  assert.equal(source.calls.programs, r.tiles, 'one request per tile');
  store.dispose();
});

test('scrolling all 1,000 channels keeps the resident set and concurrency bounded', async () => {
  const synthetic = syntheticSource({channels: 1000, start: START, days: 14, latency: () => new Promise(setImmediate)});
  const {source, peak} = peaked(synthetic);
  const store = new GuideWindowStore({source});
  let maxTiles = 0, maxPages = 0;
  for (let first = 0; first < 1000; first += 12) {
    store.setViewport({firstRow: first, lastRow: first + 11, start: NOW, end: NOW + 2 * HOUR_MS});
    await settle();
    const r = store.resident();
    maxTiles = Math.max(maxTiles, r.tiles); maxPages = Math.max(maxPages, r.channelPages);
    const row = Math.min(first + 5, 999);
    assert.ok(store.programsFor(row, NOW, NOW + 2 * HOUR_MS).complete, `row ${row} loaded`);
  }
  assert.ok(maxPages <= 6, `pages ${maxPages}`);
  assert.ok(maxTiles <= 24, `tiles ${maxTiles}`);
  assert.ok(peak() <= 4, `in flight ${peak()}`);
  // 20 channel pages, each fetched once; each page's 4 tiles (2 visible blocks + 2 margin) fetched once.
  assert.equal(synthetic.calls.channels, 20);
  assert.equal(synthetic.calls.programs, 20 * 4, 'every tile exactly once while scrolling down');
  store.dispose();
});

test('scrolling 14 days keeps bounds; rows merged across tiles have no duplicates and match the source', async () => {
  const synthetic = syntheticSource({channels: 1000, start: START, days: 14, latency: () => new Promise(setImmediate)});
  const store = new GuideWindowStore({source: synthetic});
  let maxTiles = 0, programs = 0;
  for (let t = START; t < START + 14 * DAY_MS - 2 * HOUR_MS; t += 2 * HOUR_MS) {
    store.setViewport({firstRow: 480, lastRow: 491, start: t, end: t + 2 * HOUR_MS});
    await settle();
    maxTiles = Math.max(maxTiles, store.resident().tiles);
    programs = Math.max(programs, store.resident().programs);
  }
  assert.ok(maxTiles <= 24, `tiles ${maxTiles}`);
  assert.ok(programs < 10_000, `resident programs ${programs}`);
  // A span crossing a tile boundary: merged, de-duplicated, equal to asking the source directly.
  const span = {start: START + 9 * DAY_MS + 2 * HOUR_MS, end: START + 9 * DAY_MS + 4 * HOUR_MS}; // crosses 03:00Z
  store.setViewport({firstRow: 480, lastRow: 491, ...span});
  await settle();
  const merged = store.programsFor(485, span.start, span.end);
  assert.equal(merged.complete, true);
  assert.equal(new Set(merged.programs.map(p => p.id)).size, merged.programs.length);
  const direct = (await synthetic.programs(['c485'], span.start, span.end, new AbortController().signal)).c485!;
  assert.deepEqual(merged.programs.map(p => p.id), normalizePrograms(direct).map(p => p.id));
  store.dispose();
});

test('fast scrolling aborts what left the view and never exceeds the in-flight cap', async () => {
  let release: (() => void)[] = [];
  const synthetic = syntheticSource({channels: 1000, start: START, days: 14, latency: () => new Promise<void>(r => release.push(r))});
  const {source, peak} = peaked(synthetic);
  const store = new GuideWindowStore({source});
  store.setViewport({firstRow: 0, lastRow: 11, start: NOW, end: NOW + 2 * HOUR_MS});
  for (const r of release.splice(0)) r();
  await settle();
  for (const r of release.splice(0)) r();
  await settle();
  // Flick through 40 positions without letting anything finish.
  for (let i = 1; i <= 40; i++) store.setViewport({firstRow: i * 24, lastRow: i * 24 + 11, start: NOW + i * HOUR_MS, end: NOW + (i + 2) * HOUR_MS});
  assert.ok(peak() <= 4, `in flight ${peak()}`);
  for (let round = 0; round < 20 && release.length; round++) { for (const r of release.splice(0)) r(); await settle(); }
  assert.ok(synthetic.calls.aborted > 0, 'passed-over requests were aborted');
  const last = {firstRow: 960, lastRow: 971, start: NOW + 40 * HOUR_MS, end: NOW + 42 * HOUR_MS};
  assert.ok(store.programsFor(965, last.start, last.end).complete, 'the final screen loaded');
  assert.ok(store.resident().tiles <= 24);
  store.dispose();
});

test('invalidate keeps programs on screen while it refetches; reset starts over', async () => {
  const synthetic = syntheticSource({channels: 200, start: START, days: 14, latency: () => new Promise(setImmediate)});
  const store = new GuideWindowStore({source: synthetic});
  store.setViewport({firstRow: 0, lastRow: 11, start: NOW, end: NOW + 2 * HOUR_MS});
  await settle();
  const before = synthetic.calls.programs;
  store.invalidate();
  const during = store.programsFor(2, NOW, NOW + 2 * HOUR_MS);
  assert.ok(during.programs.length > 0, 'still on screen');
  await settle();
  assert.ok(synthetic.calls.programs > before, 'refetched');
  assert.equal(store.programsFor(2, NOW, NOW + 2 * HOUR_MS).complete, true);
  store.reset();
  assert.equal(store.channelAt(0), undefined);
  assert.equal(store.getSnapshot().total, undefined);
  await settle();
  assert.equal(store.channelAt(0)!.id, 'c0', 'reloads for the same viewport');
  store.dispose();
});

test('a failed tile is marked, retried with backoff, and recovers; per-page listeners fire', async () => {
  let failing = true;
  const timers: {fn: () => void; ms: number}[] = [];
  const synthetic = syntheticSource({channels: 100, start: START, days: 14, latency: () => new Promise(setImmediate), failPrograms: () => failing});
  const store = new GuideWindowStore({source: synthetic, setTimer: (fn, ms) => { timers.push({fn, ms}); return timers.length; }, clearTimer: () => {}});
  let pageEvents = 0;
  store.subscribePage(0, () => pageEvents++);
  store.setViewport({firstRow: 0, lastRow: 11, start: NOW, end: NOW + 2 * HOUR_MS});
  await settle();
  const failed = store.programsFor(1, NOW, NOW + 2 * HOUR_MS);
  assert.equal(failed.failed, true);
  assert.equal(failed.complete, false);
  assert.ok(timers.length > 0 && timers.every(t => t.ms === 5_000), 'first retry after 5 s');
  failing = false;
  for (const t of timers.splice(0)) t.fn();
  await settle();
  assert.equal(store.programsFor(1, NOW, NOW + 2 * HOUR_MS).complete, true);
  assert.ok(pageEvents > 0);
  store.dispose();
});

test('M25-2: a 429 on one guide block retries automatically before showing an error', async () => {
  const timers: {fn: () => void; ms: number}[] = [];
  let programsCalls = 0;
  const row = {id: 'c0', kind: 'live', number: '1', name: 'C0', group: '', favorite: false, tuneAvailable: true, recordAvailable: false, guide: 'full'} as const;
  const source: GuideDataSource = {
    async channels(from, limit) { return {items: [row], total: 1}; },
    async programs(ids, start, end) {
      programsCalls++;
      if (programsCalls === 1) throw {status: 429, retryAfterSeconds: 0};
      return Object.fromEntries(ids.map(id => [id, []]));
    },
  };
  const store = new GuideWindowStore({source, setTimer: (fn, ms) => { timers.push({fn, ms}); return timers.length; }, clearTimer: () => {}});
  store.setViewport({firstRow: 0, lastRow: 0, start: NOW, end: NOW + 2 * HOUR_MS});
  await settle();
  const during = store.programsFor(0, NOW, NOW + 2 * HOUR_MS);
  assert.equal(during.failed, false, 'no failed band while the fast retry is pending');
  assert.ok(timers.length > 0, 'a fast retry was scheduled');
  assert.ok(timers.every(t => t.ms < 5000), 'fast retry honours Retry-After, not the 5 s schedule');
  for (const t of timers.splice(0)) t.fn();
  await settle();
  const after = store.programsFor(0, NOW, NOW + 2 * HOUR_MS);
  assert.equal(after.complete, true, 'the block loads after one automatic retry');
  assert.equal(after.failed, false);
  assert.ok(programsCalls > 1, 'retried without a manual retry() call');
  store.dispose();
});

test('the demo fixture through the store: 14 channels, a no-guide row and gaps render honestly', async () => {
  const source: GuideDataSource = {
    async channels(from, limit) { return {items: demoChannels.slice(from, from + limit), total: demoChannels.length}; },
    async programs(ids, start, end) { return Object.fromEntries(ids.map(id => [id, demoPrograms(id, start, end)])); },
  };
  const store = new GuideWindowStore({source});
  const day = Date.parse('2026-09-22T00:00:00Z');
  const view = {start: day + 18 * HOUR_MS, end: day + 21 * HOUR_MS};
  store.setViewport({firstRow: 0, lastRow: 13, ...view});
  await settle();
  assert.equal(store.getSnapshot().total, 14);
  for (let row = 0; row < 14; row++) {
    const channel = store.channelAt(row)!;
    const {programs, complete} = store.programsFor(row, view.start, view.end);
    assert.equal(complete, true);
    const cells = layoutRow(programs, {viewStart: view.start, viewEnd: view.end, pxPerMs: pxPerMsFor(240), now: day + 19 * HOUR_MS});
    if (channel.guide === 'none') assert.deepEqual(cells.map(c => c.kind), ['gap']);
    else assert.ok(cells.some(c => c.kind === 'program'));
    const covered = cells.reduce((sum, c) => sum + (Math.min(c.end, view.end) - Math.max(c.start, view.start)), 0);
    assert.equal(covered, view.end - view.start, `${channel.id} covers the span`);
  }
  assert.equal(BLOCK_MS, 3 * HOUR_MS);
  store.dispose();
});

test('disposing a loaded guide cancels work without launching a replacement page', async () => {
  const source = syntheticSource({channels: 100, start: START, days: 14});
  const store = new GuideWindowStore({source});
  store.setViewport({firstRow: 0, lastRow: 11, start: NOW, end: NOW + HOUR_MS});
  await settle();
  const before = {...source.calls};
  store.dispose();
  await settle();
  assert.equal(source.calls.channels, before.channels);
  assert.equal(source.calls.programs, before.programs);
  assert.deepEqual(store.resident(), {channelPages: 0, tiles: 0, programs: 0, inFlight: 0, queued: 0});
});

test('a program invalidation while channel pages are loading does not strand the initial screen', async () => {
  const release: (() => void)[] = [];
  const source = syntheticSource({channels: 100, start: START, days: 14, latency: () => new Promise<void>(r => release.push(r))});
  const store = new GuideWindowStore({source});
  store.setViewport({firstRow: 0, lastRow: 11, start: NOW, end: NOW + HOUR_MS});
  store.invalidate();
  for (let n = 0; n < 10 && release.length; n++) { for (const r of release.splice(0)) r(); await settle(); }
  assert.equal(store.channelAt(0)?.id, 'c0');
  assert.equal(store.programsFor(0, NOW, NOW + HOUR_MS).complete, true);
  store.dispose();
});
