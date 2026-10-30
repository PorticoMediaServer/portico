import test from 'node:test';
import assert from 'node:assert/strict';
import {createNativeStartupRecovery, RESTORE_ATTEMPT_TIMEOUT_MS, startupRetrySeconds, type NativeStartupWait} from '../src-next/shell/startup-recovery.ts';

const flush = async () => {for (let pass = 0; pass < 4; pass++) await Promise.resolve();};
function clock() {
  let now = 0, sequence = 0;
  const timers = new Map<number, {at: number; callback: () => void}>();
  return {
    now: () => now,
    schedule(delay: number, callback: () => void) {
      const id = ++sequence; timers.set(id, {at: now + delay, callback});
      return () => {timers.delete(id);};
    },
    size: () => timers.size,
    async advance(ms: number) {
      const end = now + ms;
      for (;;) {
        const next = [...timers].filter(([, value]) => value.at <= end).sort((a, b) => a[1].at - b[1].at || a[0] - b[0])[0];
        if (!next) break;
        now = next[1].at; timers.delete(next[0]); next[1].callback(); await flush();
      }
      now = end; await flush();
    },
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}
const networkFailure = Object.assign(new Error('offline test server'), {code: 'network'});
function fixture(options: {online?: boolean; foreground?: boolean; restore?: (signal: AbortSignal) => Promise<string>; attemptTimeoutMs?: number; retryable?: (error: unknown) => boolean; onAttemptTimeout?: () => void} = {}) {
  const time = clock(), signals: AbortSignal[] = [], tries: number[] = [], ready: string[] = [], discarded: string[] = [];
  const failures: {error: unknown; retrying: boolean}[] = [], changes: {at: number; wait: NativeStartupWait}[] = [];
  let live = true;
  const controller = createNativeStartupRecovery({startedAt: 0, current: () => live, online: options.online, foreground: options.foreground ?? true,
    now: time.now, schedule: time.schedule, attemptTimeoutMs: options.attemptTimeoutMs ?? 1e9, onAttemptTimeout: options.onAttemptTimeout,
    restore: signal => {signals.push(signal); tries.push(time.now()); return options.restore?.(signal) ?? Promise.reject(networkFailure);},
    discard: result => {discarded.push(result);}, retryable: options.retryable ?? (error => error === networkFailure),
    onReady: result => {ready.push(result);}, onError: (error, retrying) => {failures.push({error, retrying});},
    onWait: wait => {changes.push({at: time.now(), wait});}});
  return {controller, time, signals, tries, ready, discarded, failures, changes, retire: () => {live = false;}, wait: () => changes.at(-1)!.wait};
}

test('boot uses only its four milestones while a restore is pending, then exposes recovery at 15 seconds', async () => {
  const read = deferred<string>(), f = fixture({restore: () => read.promise});
  await f.time.advance(999); assert.equal(f.changes.length, 1); assert.equal(f.wait().spinner, false);
  await f.time.advance(1); assert.equal(f.wait().spinner, true); assert.equal(f.wait().message, undefined);
  await f.time.advance(1_999); assert.equal(f.changes.length, 2);
  await f.time.advance(1); assert.equal(f.wait().message, 'connecting');
  await f.time.advance(5_000); assert.equal(f.wait().message, 'still-connecting'); assert.equal(f.wait().stage, 'boot');
  await f.time.advance(7_000); assert.equal(f.wait().stage, 'recovery'); assert.equal(f.wait().pending, true);
  assert.deepEqual(f.changes.map(value => value.at), [0, 1_000, 3_000, 8_000, 15_000]);
  await f.time.advance(120_000); assert.equal(f.changes.length, 5); assert.equal(f.tries.length, 1);
  read.resolve('runtime'); await flush(); assert.deepEqual(f.ready, ['runtime']); f.controller.dispose();
});

test('a fast result cancels every milestone without imposing a minimum boot duration', async () => {
  const read = deferred<string>(), f = fixture({restore: () => read.promise});
  await f.time.advance(100); read.resolve('runtime'); await flush();
  assert.deepEqual(f.ready, ['runtime']); assert.equal(f.time.size(), 0);
  await f.time.advance(15_000); assert.equal(f.changes.length, 1);
  f.controller.foregroundChanged(false); f.controller.foregroundChanged(true);
  f.controller.networkChanged(false); f.controller.networkChanged(true);
  assert.equal(f.tries.length, 1, 'a ready shell never re-enters boot on wake or reconnect'); f.controller.dispose();
});

test('quiet retries follow 2/4/8/15/30/60 seconds, cap at 60, and keep the original boot clock', async () => {
  const f = fixture(); await flush();
  assert.equal(f.wait().stage, 'boot'); assert.equal(f.wait().retryAt, 2_000);
  await f.time.advance(14_999);
  assert.deepEqual(f.tries, [0, 2_000, 6_000, 14_000]); assert.equal(f.wait().stage, 'boot');
  await f.time.advance(1); assert.equal(f.wait().stage, 'recovery'); assert.equal(f.wait().retryAt, 29_000);
  await f.time.advance(164_000);
  assert.deepEqual(f.tries, [0, 2_000, 6_000, 14_000, 29_000, 59_000, 119_000, 179_000]);
  assert.equal(f.wait().retryAt, 239_000); assert.equal(f.failures.length, f.tries.length);
  assert.ok(f.failures.every(value => value.error === networkFailure && value.retrying)); f.controller.dispose();
});

test('offline still reads the stored session once, shows recovery at one second, and retries on reconnect', async () => {
  const f = fixture({online: false}); await flush();
  assert.equal(f.tries.length, 1); assert.equal(f.wait().retryAt, undefined);
  await f.time.advance(999); assert.equal(f.wait().stage, 'boot');
  await f.time.advance(1); assert.equal(f.wait().stage, 'recovery'); assert.equal(f.wait().offline, true);
  await f.time.advance(1_000); assert.equal(f.tries.length, 1);
  f.controller.networkChanged(undefined); f.controller.networkChanged(true); await flush();
  assert.deepEqual(f.tries, [0, 2_000]); assert.equal(f.wait().stage, 'recovery', 'reconnect never flashes the boot screen again');
  assert.equal(f.wait().offline, false); assert.equal(f.wait().retryAt, 4_000); f.controller.dispose();
});

test('manual, wake and reconnect attempts share one flight, then restart the backoff from two seconds', async () => {
  const read = deferred<string>(); let reads = 0;
  const f = fixture({restore: () => ++reads === 1 ? Promise.reject(networkFailure) : read.promise}); await flush();
  await f.time.advance(500); assert.equal(f.controller.retryNow(), true); assert.deepEqual(f.tries, [0, 500]);
  f.controller.foregroundChanged(false); f.controller.foregroundChanged(true);
  f.controller.networkChanged(false); f.controller.networkChanged(true); f.controller.retryNow();
  assert.deepEqual(f.tries, [0, 500]); assert.equal(f.wait().pending, true);
  read.reject(networkFailure); await flush(); assert.equal(f.wait().retryAt, 2_500); f.controller.dispose();
});

test('Try again now still makes a foreground attempt when the last native path fact is offline', async () => {
  const f = fixture({online: false}); await flush();
  f.controller.retryNow(); await flush(); assert.equal(f.tries.length, 2);
  await f.time.advance(60_000); assert.equal(f.tries.length, 2, 'the manual try does not enable polling an offline path');
  f.controller.foregroundChanged(false); f.controller.retryNow(); await flush(); assert.equal(f.tries.length, 2);
  f.controller.dispose();
});

test('background retries stop and a foreground transition retries immediately without resetting the boot clock', async () => {
  const f = fixture(); await flush(); await f.time.advance(500);
  f.controller.foregroundChanged(false); await f.time.advance(20_000);
  assert.deepEqual(f.tries, [0]); assert.equal(f.wait().stage, 'recovery');
  f.controller.foregroundChanged(true); await flush(); assert.deepEqual(f.tries, [0, 20_500]);
  assert.equal(f.wait().stage, 'recovery'); assert.equal(f.wait().retryAt, 22_500); f.controller.dispose();
});

test('terminal failures stop automatic work and retain their actual refusal', async () => {
  const error = Object.assign(new Error('session ended'), {code: 'unauthorized'});
  const f = fixture({restore: () => Promise.reject(error)}); await flush();
  assert.deepEqual(f.failures, [{error, retrying: false}]); assert.equal(f.time.size(), 0);
  assert.equal(f.controller.retryNow(), false); f.controller.foregroundChanged(false); f.controller.foregroundChanged(true);
  f.controller.networkChanged(false); f.controller.networkChanged(true); await f.time.advance(180_000);
  assert.deepEqual(f.tries, [0]); f.controller.dispose();
});

test('opening downloads or a server chooser cancels the old attempt; a late result is disposed, never admitted', async () => {
  const first = deferred<string>(), second = deferred<string>(); let reads = 0;
  const f = fixture({restore: () => ++reads === 1 ? first.promise : second.promise});
  f.controller.pause(); assert.equal(f.signals[0]!.aborted, true);
  await f.time.advance(60_000); assert.equal(f.tries.length, 1);
  f.controller.retryNow(); f.controller.foregroundChanged(false); f.controller.foregroundChanged(true);
  f.controller.networkChanged(false); f.controller.networkChanged(true); assert.equal(f.tries.length, 1);
  first.resolve('retired runtime'); await flush(); assert.deepEqual(f.discarded, ['retired runtime']); assert.deepEqual(f.ready, []);
  f.controller.resume(); assert.equal(f.tries.length, 2); second.resolve('current runtime'); await flush();
  assert.deepEqual(f.ready, ['current runtime']); f.controller.dispose();
});

test('retiring an opening cancels every timer and cannot retry or publish into its successor', async () => {
  const f = fixture(); await flush(); const count = f.changes.length;
  f.controller.dispose(); assert.equal(f.signals[0]!.aborted, true); assert.equal(f.time.size(), 0);
  f.controller.retryNow(); f.controller.networkChanged(false); f.controller.networkChanged(true);
  f.controller.foregroundChanged(false); f.controller.foregroundChanged(true); await f.time.advance(180_000);
  assert.deepEqual(f.tries, [0]); assert.equal(f.changes.length, count);
  const read = deferred<string>(), pending = fixture({restore: () => read.promise});
  pending.retire(); read.resolve('late runtime'); await flush(); assert.deepEqual(pending.ready, []);
  assert.deepEqual(pending.discarded, ['late runtime']); pending.controller.dispose();
});

test('resuming after pause still waits for a lower-level read that ignores AbortSignal', async () => {
  const first = deferred<string>(), second = deferred<string>(); let reads = 0;
  const f = fixture({restore: () => ++reads === 1 ? first.promise : second.promise});
  f.controller.pause(); f.controller.resume(); f.controller.retryNow();
  assert.equal(f.signals[0]!.aborted, true); assert.equal(f.tries.length, 1, 'no second read overlaps the aborted, unresolved promise');
  first.resolve('late runtime'); await flush();
  assert.deepEqual(f.discarded, ['late runtime']); assert.equal(f.tries.length, 2);
  second.resolve('resumed runtime'); await flush(); assert.deepEqual(f.ready, ['resumed runtime']); f.controller.dispose();
});

test('a caller-supplied start time is already partway up the ladder when JavaScript first draws', async () => {
  const changes: NativeStartupWait[] = [], time = clock(); await time.advance(9_000);
  const read = deferred<string>();
  const controller = createNativeStartupRecovery({startedAt: 0, current: () => true, foreground: true,
    now: time.now, schedule: time.schedule, restore: () => read.promise, discard() {}, retryable: () => false,
    onReady() {}, onError(error) {throw error;}, onWait: value => {changes.push(value);}});
  assert.equal(changes[0]!.message, 'still-connecting'); assert.equal(changes[0]!.spinner, true);
  assert.equal(time.size(), 2, 'the 15 s milestone and the attempt deadline');
  await time.advance(6_000); assert.equal(changes.at(-1)!.stage, 'recovery'); controller.dispose();
  read.resolve('retired'); await flush();
});

test('countdown derives from the actual retry deadline, rounds up, and never becomes negative', () => {
  assert.equal(startupRetrySeconds(15_000, 2_001), 13);
  assert.equal(startupRetrySeconds(15_000, 14_999), 1);
  assert.equal(startupRetrySeconds(15_000, 15_000), 0);
  assert.equal(startupRetrySeconds(15_000, 20_000), 0);
});

test('E-mobile-01: a restore attempt that never answers ends as a visible, retryable timeout and frees its slot', async () => {
  const stuck = deferred<string>(), answers = [stuck.promise, Promise.resolve('runtime')];
  let timeouts = 0;
  const f = fixture({restore: () => answers.shift()!, attemptTimeoutMs: RESTORE_ATTEMPT_TIMEOUT_MS, onAttemptTimeout: () => {timeouts++;},
    retryable: error => (error as {code?: string}).code === 'timeout' || error === networkFailure});
  assert.equal(RESTORE_ATTEMPT_TIMEOUT_MS, 30_000);
  await f.time.advance(29_999); assert.equal(f.failures.length, 0);
  await f.time.advance(1);
  assert.equal(timeouts, 1);
  assert.equal(f.failures.length, 1); assert.equal((f.failures[0]!.error as {code?: string}).code, 'timeout'); assert.equal(f.failures[0]!.retrying, true);
  assert.equal(f.signals[0]!.aborted, true);
  // The stuck native read still owns nothing: the scheduled retry runs and opens Home.
  await f.time.advance(2_000); assert.equal(f.tries.length, 2); assert.deepEqual(f.ready, ['runtime']);
  stuck.resolve('late'); await flush(); assert.deepEqual(f.discarded, ['late']);
  f.controller.dispose();
});

test('a restore that answers within its bound keeps no deadline timer', async () => {
  const f = fixture({restore: () => Promise.resolve('runtime'), attemptTimeoutMs: RESTORE_ATTEMPT_TIMEOUT_MS});
  await flush(); assert.deepEqual(f.ready, ['runtime']); assert.equal(f.time.size(), 0);
  f.controller.dispose();
});
