import test from 'node:test';
import assert from 'node:assert/strict';
import {backoffDelay, createManualScheduler, nextDelay, parseRetryAfter, repeat, serverFloorFrom, systemScheduler, wait, type ServerFloor} from '../src/timer.ts';
import {ApiError} from '../src/index.ts';

const policy = {baseMs: 1000, maxMs: 8000};

test('backoff doubles to its ceiling, with equal jitter by default', () => {
  assert.deepEqual([1, 2, 3, 4, 5].map(n => backoffDelay({...policy, jitter: 'none'}, n)), [1000, 2000, 4000, 8000, 8000]);
  assert.equal(backoffDelay(policy, 3, () => 0), 2000);
  assert.equal(backoffDelay(policy, 3, () => 1), 4000);
  assert.equal(backoffDelay({...policy, jitter: 'full'}, 3, () => 0), 0);
  assert.equal(backoffDelay(policy, 0, () => 1), 1000, 'a failure count below one is the first retry');
  assert.equal(backoffDelay({...policy, jitter: 'none'}, 10_000), 8000, 'huge counts stay at the ceiling');
  assert.throws(() => backoffDelay({baseMs: -1, maxMs: 1}, 1), RangeError);
  assert.throws(() => backoffDelay({baseMs: 1, maxMs: 1, factor: 0.5}, 1), RangeError);
});

test('a server floor is never shortened, even above our ceiling, and jitter only adds to it', () => {
  for (const r of [0, 0.3, 1]) {
    const delay = nextDelay(policy, 1, {retryAfterMs: 120_000}, () => r);
    assert.ok(delay >= 120_000 && delay <= 125_000, `${delay}`);
  }
  assert.equal(nextDelay(policy, 1, {retryAfterMs: 2000}, () => 0), 2000);
  assert.equal(nextDelay(policy, 1, {retryAfterMs: 2000}, () => 1), 2200);
  assert.equal(nextDelay(policy, 4, {pollAfterMs: 1000}, () => 1), 8000, 'our longer backoff wins over a smaller floor');
  assert.equal(nextDelay(policy, 1, {retryAfterMs: 3000, pollAfterMs: 5000}, () => 0), 5000, 'the larger floor wins');
  assert.equal(nextDelay(policy, 1, {retryAfterMs: Number.NaN, pollAfterMs: -5}, () => 1), 1000, 'invalid floors are ignored');
});

test('Retry-After reads delay-seconds and HTTP-dates, and nothing else', () => {
  const now = Date.parse('2026-10-31T12:00:00Z');
  assert.equal(parseRetryAfter('120', now), 120_000);
  assert.equal(parseRetryAfter(' 5 ', now), 5000);
  assert.equal(parseRetryAfter('Sat, 31 Oct 2026 12:00:30 GMT', now), 30_000);
  assert.equal(parseRetryAfter('Sat, 31 Oct 2026 11:59:00 GMT', now), undefined, 'a past date');
  for (const bad of [null, undefined, '', '0', '-3', '1.5', '2026-10-31', 'soon']) assert.equal(parseRetryAfter(bad, now), undefined, String(bad));
});

test('a server floor is read from errors', () => {
  assert.deepEqual(serverFloorFrom(new ApiError(503, 'unavailable', 'x', true, 7)), {retryAfterMs: 7000});
  assert.deepEqual(serverFloorFrom({retryAfterMs: 250}), {retryAfterMs: 250});
  assert.equal(serverFloorFrom(new Error('x')), undefined);
  assert.equal(serverFloorFrom(undefined), undefined);
});

test('the system scheduler is monotonic', () => {
  let last = systemScheduler.now();
  for (let i = 0; i < 1000; i++) {const now = systemScheduler.now(); assert.ok(now >= last); last = now;}
});

test('wait resolves on the scheduler clock and rejects on abort', async () => {
  const clock = createManualScheduler();
  let done = false;
  const sleeping = wait(1000, {scheduler: clock}).then(() => {done = true;});
  await clock.advance(999);
  assert.equal(done, false);
  await clock.advance(1);
  await sleeping;
  assert.equal(done, true);

  const controller = new AbortController();
  const aborted = wait(1000, {scheduler: clock, signal: controller.signal});
  controller.abort();
  await assert.rejects(aborted, {name: 'AbortError'});
  assert.equal(clock.pending(), 0, 'its timer is cleared');
  await assert.rejects(wait(10, {scheduler: clock, signal: controller.signal}), {name: 'AbortError'}, 'an already aborted signal rejects at once');
});

test('wait chains past the host timer limit', async () => {
  const clock = createManualScheduler(), days = 30 * 24 * 3600_000;
  let done = false;
  void wait(days, {scheduler: clock}).then(() => {done = true;});
  await clock.advance(2147483647);
  assert.equal(done, false);
  await clock.advance(days - 2147483647);
  assert.equal(done, true);
});

test('repeat runs one at a time, backs off on failure and resets on success', async () => {
  const clock = createManualScheduler();
  const starts: number[] = [];
  let release: (() => void) | undefined, fail = 0;
  const repeating = repeat(async () => {
    starts.push(clock.now());
    if (fail > 0) {fail--; throw new Error('down');}
    await new Promise<void>(resolve => {release = resolve;});
  }, {everyMs: 5000, backoff: {baseMs: 1000, maxMs: 4000, jitter: 'none'}, scheduler: clock});
  await clock.advance(0);
  assert.deepEqual(starts, [0]);
  await clock.advance(60_000);
  assert.deepEqual(starts, [0], 'nothing overlaps a run in flight');
  fail = 3;
  release!();
  await clock.advance(5000);
  assert.deepEqual(starts, [0, 65_000]);
  await clock.advance(1000);
  await clock.advance(2000);
  assert.deepEqual(starts, [0, 65_000, 66_000, 68_000], 'backoff 1 s, then 2 s');
  await clock.advance(4000);
  assert.deepEqual(starts.at(-1), 72_000);
  release!();
  await clock.advance(5000);
  assert.deepEqual(starts.at(-1), 77_000, 'success returns to the steady interval');
  repeating.stop();
  release!();
  await clock.advance(60_000);
  assert.equal(starts.length, 6);
  assert.equal(clock.pending(), 0);
});

test('repeat honours the floor a task or error carries', async () => {
  const clock = createManualScheduler();
  const starts: number[] = [];
  let answer: () => Promise<ServerFloor | void> = async () => ({pollAfterMs: 20_000});
  const repeating = repeat(() => {starts.push(clock.now()); return answer();}, {everyMs: 5000, backoff: {baseMs: 1000, maxMs: 2000}, scheduler: clock, random: () => 0});
  await clock.advance(0);
  answer = async () => {throw new ApiError(429, 'slow_down', 'x', true, 30);};
  await clock.advance(20_000);
  await clock.advance(30_000);
  assert.deepEqual(starts, [0, 20_000, 50_000]);
  repeating.stop();
});

test('repeat stops on its signal, aborts the run in flight, and runNow waits for it', async () => {
  const clock = createManualScheduler(), controller = new AbortController();
  const signals: AbortSignal[] = [];
  let release: (() => void) | undefined;
  const repeating = repeat(signal => {signals.push(signal); return new Promise<void>(resolve => {release = resolve;});}, {everyMs: 5000, backoff: policy, scheduler: clock, signal: controller.signal});
  await clock.advance(0);
  repeating.runNow();
  release!();
  await clock.advance(0);
  assert.equal(signals.length, 2, 'runNow during a run starts the next one when it settles');
  controller.abort();
  assert.equal(signals[1]!.aborted, true);
  release!();
  await clock.advance(60_000);
  assert.equal(signals.length, 2);
  assert.equal(clock.pending(), 0);
});

test('repeat reports failures and survives a throwing reporter', async () => {
  const clock = createManualScheduler(), seen: number[] = [];
  const warn = console.warn;
  console.warn = () => {};
  try {
    const repeating = repeat(async () => {throw new Error('x');}, {everyMs: 100, backoff: {baseMs: 100, maxMs: 100, jitter: 'none'}, scheduler: clock, onError: (_, n) => {seen.push(n); throw new Error('reporter');}});
    await clock.advance(250);
    assert.deepEqual(seen, [1, 2, 3]);
    repeating.stop();
  } finally {console.warn = warn;}
});

test('repeat with immediate false waits one interval first', async () => {
  const clock = createManualScheduler();
  let runs = 0;
  const repeating = repeat(async () => {runs++;}, {everyMs: 1000, backoff: policy, scheduler: clock, immediate: false});
  await clock.advance(999);
  assert.equal(runs, 0);
  await clock.advance(1);
  assert.equal(runs, 1);
  repeating.stop();
});
