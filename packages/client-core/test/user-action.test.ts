import test from 'node:test';
import assert from 'node:assert/strict';
import {createUserAction, describeActionError, USER_ACTION_SLOW_MS, USER_ACTION_WATCHDOG_MS, type UserActionContext, type UserActionSnapshot} from '../src/user-action.ts';
import {createManualScheduler} from '../src/timer.ts';
import {setCleanupLog} from '../src/cleanup.ts';
import {ApiError} from '../src/index.ts';

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {resolve = res; reject = rej;});
  return {promise, resolve, reject};
}

function harness<T = string>(options: {slowAfterMs?: number; watchdogMs?: number; binding?: {signal: AbortSignal}} = {}) {
  const clock = createManualScheduler(), calls: {context: UserActionContext; args: readonly unknown[]; d: ReturnType<typeof deferred<T>>}[] = [];
  let ids = 0;
  const action = createUserAction<T, [string]>({
    run: (context, ...args) => {const d = deferred<T>(); calls.push({context, args, d}); return d.promise;},
    scheduler: clock, newIntentId: () => `intent-${++ids}`, ...options,
  });
  const seen: UserActionSnapshot<T>['phase'][] = [];
  action.subscribe(() => seen.push(action.getSnapshot().phase));
  return {clock, calls, action, seen};
}

test('an action goes working, then done, and a double start submits once', async () => {
  const {calls, action, seen} = harness();
  assert.equal(action.getSnapshot().phase, 'idle');
  action.start('a');
  action.start('b');
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0]!.args, ['a']);
  assert.deepEqual(action.getSnapshot(), {phase: 'working', slow: false, attempt: 1});
  calls[0]!.d.resolve('ok');
  await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(action.getSnapshot(), {phase: 'done', value: 'ok'});
  assert.deepEqual(seen, ['working', 'done']);
});

test('slow at 8 s, unconfirmed at 30 s, with the attempt aborted and its late result dropped', async () => {
  const {clock, calls, action} = harness();
  action.start('a');
  await clock.advance(USER_ACTION_SLOW_MS - 1);
  assert.deepEqual(action.getSnapshot(), {phase: 'working', slow: false, attempt: 1});
  await clock.advance(1);
  assert.deepEqual(action.getSnapshot(), {phase: 'working', slow: true, attempt: 1});
  await clock.advance(USER_ACTION_WATCHDOG_MS - USER_ACTION_SLOW_MS);
  assert.deepEqual(action.getSnapshot(), {phase: 'unconfirmed', attempt: 1});
  assert.equal(calls[0]!.context.signal.aborted, true);
  calls[0]!.d.resolve('late');
  await clock.advance(0);
  assert.equal(action.getSnapshot().phase, 'unconfirmed', 'a late result never publishes');
  assert.equal(clock.pending(), 0);
});

test('Try again keeps the intent and the arguments, and counts the attempt', async () => {
  const {clock, calls, action} = harness();
  action.start('a');
  calls[0]!.d.reject(new ApiError(503, 'unavailable', 'x', true, 4));
  await clock.advance(0);
  const failed = action.getSnapshot();
  assert.equal(failed.phase, 'failed');
  assert.deepEqual(failed.phase === 'failed' && {retryable: failed.failure.retryable, retryAfterMs: failed.failure.retryAfterMs}, {retryable: true, retryAfterMs: 4000});
  action.retry();
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[1]!.args, ['a']);
  assert.equal(calls[1]!.context.intentId, calls[0]!.context.intentId);
  assert.equal(calls[1]!.context.attempt, 2);
  calls[1]!.d.resolve('ok');
  await clock.advance(0);
  assert.equal(action.getSnapshot().phase, 'done');
  action.retry();
  assert.equal(calls.length, 2, 'retry only after failed or unconfirmed');
  action.start('b');
  assert.notEqual(calls[2]!.context.intentId, calls[0]!.context.intentId, 'a new start is a new intent');
});

test('cancel aborts, returns to idle and drops a late error', async () => {
  const {clock, calls, action, seen} = harness();
  action.start('a');
  action.cancel();
  assert.equal(calls[0]!.context.signal.aborted, true);
  calls[0]!.d.reject(Object.assign(new Error('aborted'), {name: 'AbortError'}));
  await clock.advance(60_000);
  assert.deepEqual(seen, ['working', 'idle']);
  assert.equal(clock.pending(), 0);
});

test('a synchronous throw from run is a failure, not a crash', async () => {
  const clock = createManualScheduler();
  const action = createUserAction<void>({run: () => {throw new Error('bad');}, scheduler: clock});
  action.start();
  await clock.advance(0);
  const snapshot = action.getSnapshot();
  assert.equal(snapshot.phase, 'failed');
  assert.equal(snapshot.phase === 'failed' && snapshot.failure.retryable, false);
});

test('a throwing describe falls back to the default', async () => {
  const clock = createManualScheduler();
  const action = createUserAction<void>({run: () => Promise.reject(new TypeError('offline')), describe: () => {throw new Error('x');}, scheduler: clock});
  action.start();
  await clock.advance(0);
  const snapshot = action.getSnapshot();
  assert.equal(snapshot.phase === 'failed' && snapshot.failure.retryable, true);
});

test('a binding change cancels the attempt and refuses later starts', async () => {
  const binding = new AbortController();
  const {clock, calls, action} = harness({binding});
  action.start('a');
  binding.abort();
  assert.equal(calls[0]!.context.signal.aborted, true);
  assert.equal(action.getSnapshot().phase, 'idle');
  calls[0]!.d.resolve('late');
  await clock.advance(0);
  assert.equal(action.getSnapshot().phase, 'idle');
  action.start('b');
  assert.equal(calls.length, 1);
});

test('dispose stops everything and never throws; a throwing listener is contained', async () => {
  setCleanupLog(() => {});
  const {clock, calls, action} = harness();
  action.subscribe(() => {throw new Error('listener');});
  action.start('a');
  assert.equal(action.getSnapshot().phase, 'working');
  action.dispose();
  action.dispose();
  assert.equal(calls[0]!.context.signal.aborted, true);
  calls[0]!.d.resolve('late');
  await clock.advance(60_000);
  assert.equal(action.getSnapshot().phase, 'idle');
  action.start('b');
  assert.equal(calls.length, 1);
  assert.equal(clock.pending(), 0);
});

test('timings are per action and must be bounded', () => {
  assert.throws(() => createUserAction({run: async () => {}, slowAfterMs: 10, watchdogMs: 10}), RangeError);
  assert.throws(() => createUserAction({run: async () => {}, watchdogMs: Number.POSITIVE_INFINITY}), RangeError);
  assert.doesNotThrow(() => createUserAction({run: async () => {}, slowAfterMs: 20_000, watchdogMs: 120_000}));
});

test('the default description', () => {
  assert.deepEqual(describeActionError(new ApiError(409, 'conflict', 'x', false)).retryable, false);
  assert.equal(describeActionError(new TypeError('fetch failed')).retryable, true);
  assert.equal(describeActionError(new Error('x')).retryable, false);
  assert.equal(describeActionError(new Error('x')).message, undefined);
});
