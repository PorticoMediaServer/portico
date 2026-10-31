/**
 * Every user-initiated action ends in something the person can see: done, an honest failure, or
 * "unconfirmed" (the watchdog gave up waiting) with Try again. Nothing spins forever, a cancelled
 * action stays cancelled, and a result that arrives late never publishes. Failed and unconfirmed
 * are always shown: in the screen's drawn error slot, else the kit's inline error with Try again.
 * The slow flag is shown only where a mock-up draws a slow line. Framework-free; web and native
 * hooks subscribe through getSnapshot/subscribe (docs/ENGINEERING-RULES.md).
 */
import type {MessageId} from '../../i18n/src/index.ts';
import {ApiError} from './index.ts';
import {releaseQuietly} from './cleanup.ts';
import {randomId} from './random-id.ts';
import {serverFloorFrom, systemScheduler, type Scheduler} from './timer.ts';

export const USER_ACTION_SLOW_MS = 8000;
export const USER_ACTION_WATCHDOG_MS = 30_000;

export type UserActionFailure = Readonly<{
  /** Catalogue line to show; screens with drawn copy use their own. */
  message?: MessageId;
  retryable: boolean;
  /** The server's floor before Try again is useful (Retry-After); never shortened. */
  retryAfterMs?: number;
  cause: unknown;
}>;

export type UserActionSnapshot<T> = Readonly<
  | {phase: 'idle'}
  | {phase: 'working'; slow: boolean; attempt: number}
  | {phase: 'done'; value: T}
  | {phase: 'failed'; failure: UserActionFailure; attempt: number}
  /** The watchdog fired: the attempt was aborted and whether it took effect is unknown.
   *  Try again keeps the same intentId. */
  | {phase: 'unconfirmed'; attempt: number}
>;

export type UserActionContext = Readonly<{
  /** Aborted on cancel, the watchdog, reset, dispose or a binding change. */
  signal: AbortSignal;
  /** 1 for start(), one more for each retry(). */
  attempt: number;
  /** Stable across the retries of one intent. Send it only to endpoints that already accept an
   *  idempotency key; never as a new global header (cross-origin web would need a preflight). */
  intentId: string;
}>;

export type UserActionOptions<T, A extends readonly unknown[] = []> = Readonly<{
  run: (context: UserActionContext, ...args: A) => Promise<T>;
  describe?: (error: unknown) => UserActionFailure;
  /** Default 8 s. */
  slowAfterMs?: number;
  /** Default 30 s; must be longer than slowAfterMs. Raise it per action for known long operations. */
  watchdogMs?: number;
  scheduler?: Scheduler;
  newIntentId?: () => string;
  /** Authority the action belongs to (bindAuthority). When its signal aborts, the attempt in flight
   *  is cancelled and later starts are ignored. */
  binding?: Readonly<{signal: AbortSignal}>;
}>;

export interface UserAction<T, A extends readonly unknown[] = []> {
  getSnapshot(): UserActionSnapshot<T>;
  subscribe(listener: () => void): () => void;
  /** Starts a new intent. Ignored while working, so a double tap submits once. */
  start(...args: A): void;
  /** Runs the last arguments again with the same intentId. Only after failed or unconfirmed. */
  retry(): void;
  /** Aborts the attempt; its late result or error is dropped; back to idle. */
  cancel(): void;
  /** Back to idle; the next start gets a new intentId. */
  reset(): void;
  /** cancel() and drop every listener. Never throws. */
  dispose(): void;
}

/** ApiError carries retryable and Retry-After; a network TypeError is worth retrying; anything
 *  else is not. No message: the screen or the kit chooses the line. */
export function describeActionError(error: unknown): UserActionFailure {
  const floor = serverFloorFrom(error);
  const retryAfterMs = floor?.retryAfterMs;
  if (error instanceof ApiError) return Object.freeze({retryable: error.retryable, ...(retryAfterMs ? {retryAfterMs} : {}), cause: error});
  if (error instanceof TypeError) return Object.freeze({retryable: true, cause: error});
  return Object.freeze({retryable: false, ...(retryAfterMs ? {retryAfterMs} : {}), cause: error});
}

export function createUserAction<T, A extends readonly unknown[] = []>(options: UserActionOptions<T, A>): UserAction<T, A> {
  const {run, describe = describeActionError, scheduler = systemScheduler, newIntentId = randomId, binding} = options;
  const slowAfterMs = options.slowAfterMs ?? USER_ACTION_SLOW_MS, watchdogMs = options.watchdogMs ?? USER_ACTION_WATCHDOG_MS;
  if (!Number.isFinite(slowAfterMs) || slowAfterMs < 0 || !Number.isFinite(watchdogMs) || watchdogMs <= slowAfterMs) throw new RangeError('Invalid user action timing');

  const idle: UserActionSnapshot<T> = Object.freeze({phase: 'idle'});
  let snapshot: UserActionSnapshot<T> = idle;
  let listeners = new Set<() => void>();
  let ticket = 0, attempt = 0, intentId = '', lastArgs: A | undefined, disposed = false;
  let controller: AbortController | undefined, slowTimer: unknown, watchdogTimer: unknown;

  const publish = (next: UserActionSnapshot<T>) => {
    snapshot = next;
    for (const listener of [...listeners]) releaseQuietly('user-action listener', listener);
  };
  const onUnbound = () => {
    abandon();
    if (snapshot !== idle) publish(idle);
  };
  /** Timers and the binding listener live only while an attempt runs. */
  const clearTimers = () => {
    if (slowTimer !== undefined) scheduler.clearTimeout(slowTimer);
    if (watchdogTimer !== undefined) scheduler.clearTimeout(watchdogTimer);
    slowTimer = watchdogTimer = undefined;
    binding?.signal.removeEventListener('abort', onUnbound);
  };
  /** Ends the attempt in flight without publishing anything for it. */
  const abandon = () => {
    ticket++;
    clearTimers();
    const current = controller;
    controller = undefined;
    if (current) releaseQuietly('user-action abort', () => current.abort());
  };
  const unbound = () => disposed || !!binding?.signal.aborted;

  const launch = (args: A) => {
    const mine = ++ticket, current = controller = new AbortController(), context: UserActionContext = Object.freeze({signal: current.signal, attempt, intentId});
    publish(Object.freeze({phase: 'working', slow: false, attempt}));
    if (mine !== ticket) return; // a listener cancelled it
    binding?.signal.addEventListener('abort', onUnbound, {once: true});
    slowTimer = scheduler.setTimeout(() => {
      slowTimer = undefined;
      if (mine === ticket) publish(Object.freeze({phase: 'working', slow: true, attempt: context.attempt}));
    }, slowAfterMs);
    watchdogTimer = scheduler.setTimeout(() => {
      watchdogTimer = undefined;
      if (mine !== ticket) return;
      abandon();
      publish(Object.freeze({phase: 'unconfirmed', attempt: context.attempt}));
    }, watchdogMs);
    let pending: Promise<T>;
    try {pending = run(context, ...args);} catch (error) {pending = Promise.reject(error);}
    pending.then(value => {
      if (mine !== ticket) return;
      clearTimers();
      controller = undefined;
      publish(Object.freeze({phase: 'done', value}));
    }, error => {
      if (mine !== ticket) return;
      clearTimers();
      controller = undefined;
      // Our own aborts bump the ticket first, so an error reaching here is the action's own.
      let failure: UserActionFailure;
      try {failure = describe(error);} catch {failure = describeActionError(error);}
      publish(Object.freeze({phase: 'failed', failure, attempt: context.attempt}));
    });
  };

  return Object.freeze({
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {
      if (disposed) return () => {};
      listeners.add(listener);
      return () => {listeners.delete(listener);};
    },
    start(...args: A) {
      if (unbound() || snapshot.phase === 'working') return;
      abandon();
      intentId = newIntentId();
      attempt = 1;
      lastArgs = args;
      launch(args);
    },
    retry() {
      if (unbound() || lastArgs === undefined || snapshot.phase !== 'failed' && snapshot.phase !== 'unconfirmed') return;
      abandon();
      attempt++;
      launch(lastArgs);
    },
    cancel() {
      abandon();
      if (snapshot !== idle && !disposed) publish(idle);
    },
    reset() {
      abandon();
      lastArgs = undefined;
      intentId = '';
      attempt = 0;
      if (snapshot !== idle && !disposed) publish(idle);
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      abandon();
      listeners = new Set();
      snapshot = idle;
    },
  });
}
