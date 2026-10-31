/**
 * The one timer helper: a monotonic clock, floor, jitter and backoff, cancellation, and an
 * injectable scheduler so tests move time by hand. A server's floor (Retry-After, a poll hint)
 * is never shortened; jitter only ever adds to it. Hand-rolled setInterval polls and private
 * Retry-After parsers are replaced by this (docs/ENGINEERING-RULES.md).
 */
import {throwIfAborted} from './abort-lifetime.ts';
import {releaseQuietly} from './cleanup.ts';

/** now() is monotonic milliseconds, never wall time. */
export interface Scheduler {
  now(): number;
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

/** Host timers cap a delay at 2^31-1 ms; longer waits are chained. */
const MAX_TIMER_MS = 2147483647;

let lastWall = 0;
const monotonicNow = (): number => {
  const performance = (globalThis as {performance?: {now?: () => number}}).performance;
  if (typeof performance?.now === 'function') return performance.now();
  // Without a monotonic source, never let the clock run backwards.
  lastWall = Math.max(lastWall, Date.now());
  return lastWall;
};

export const systemScheduler: Scheduler = Object.freeze({
  now: monotonicNow,
  setTimeout: (callback: () => void, delayMs: number) => {
    const handle = globalThis.setTimeout(callback, delayMs);
    (handle as {unref?: () => void})?.unref?.();
    return handle;
  },
  clearTimeout: (handle: unknown) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
});

/** Tests: time moves only on advance(); timers due by then run in due order. */
export interface ManualScheduler extends Scheduler {
  advance(ms: number): Promise<void>;
  pending(): number;
}

export function createManualScheduler(startMs = 0): ManualScheduler {
  type Entry = {id: number; due: number; callback: () => void};
  let now = startMs, sequence = 0;
  const entries = new Map<number, Entry>();
  // A real macrotask turn lets every promise chain the callbacks started settle.
  const settle = () => new Promise<void>(resolve => globalThis.setTimeout(resolve, 0));
  return {
    now: () => now,
    setTimeout(callback, delayMs) {
      const id = ++sequence;
      entries.set(id, {id, due: now + Math.max(0, delayMs), callback});
      return id;
    },
    clearTimeout(handle) {entries.delete(handle as number);},
    pending: () => entries.size,
    async advance(ms) {
      if (!Number.isFinite(ms) || ms < 0) throw new RangeError('Invalid advance');
      const target = now + ms;
      await settle();
      for (;;) {
        let next: Entry | undefined;
        for (const entry of entries.values()) if (entry.due <= target && (!next || entry.due < next.due || entry.due === next.due && entry.id < next.id)) next = entry;
        if (!next) break;
        entries.delete(next.id);
        now = Math.max(now, next.due);
        next.callback();
        await settle();
      }
      now = target;
      await settle();
    },
  };
}

export type Backoff = Readonly<{
  /** Delay before the first retry, before jitter. */
  baseMs: number;
  /** Ceiling of our own backoff only; it never caps a server floor. */
  maxMs: number;
  /** Default 2. */
  factor?: number;
  /** Default 'equal': a delay d becomes [d/2, d]. 'full' is [0, d]. */
  jitter?: 'equal' | 'full' | 'none';
}>;

/** What the server asked for. Both are floors. */
export type ServerFloor = Readonly<{retryAfterMs?: number; pollAfterMs?: number}>;

const delayValue = (value: number, name: string) => {
  if (!Number.isFinite(value) || value < 0) throw new RangeError(`Invalid ${name}`);
  return value;
};

/** failures counts from 1 (the first retry). */
export function backoffDelay(policy: Backoff, failures: number, random: () => number = Math.random): number {
  const base = delayValue(policy.baseMs, 'backoff base'), max = delayValue(policy.maxMs, 'backoff ceiling');
  const factor = policy.factor ?? 2;
  if (!Number.isFinite(factor) || factor < 1) throw new RangeError('Invalid backoff factor');
  const step = Math.max(1, Math.floor(Number.isFinite(failures) ? failures : 1));
  const d = Math.min(max, base * factor ** Math.min(step - 1, 64));
  const r = Math.min(1, Math.max(0, random()));
  switch (policy.jitter ?? 'equal') {
    case 'none': return Math.round(d);
    case 'full': return Math.round(d * r);
    default: return Math.round(d / 2 + d / 2 * r);
  }
}

function floorOf(floor: ServerFloor | undefined): number {
  const values = [floor?.retryAfterMs, floor?.pollAfterMs].filter((v): v is number => typeof v === 'number' && Number.isFinite(v) && v > 0);
  return values.length ? Math.max(...values) : 0;
}

/** The later of our jittered backoff and the server floor. With a floor, jitter is only added on
 * top (up to 10% of the floor, at most 5 s), so clients spread out without ever arriving early. */
export function nextDelay(policy: Backoff, failures: number, floor?: ServerFloor, random: () => number = Math.random): number {
  const ours = backoffDelay(policy, failures, random), server = floorOf(floor);
  if (!server) return ours;
  const spread = Math.min(server * 0.1, 5000) * Math.min(1, Math.max(0, random()));
  return Math.max(ours, Math.ceil(server + spread));
}

/** RFC 9110 Retry-After (delay-seconds or an HTTP-date) as milliseconds from now; undefined when
 * absent, malformed or already past. Wall time is read only here; waiting uses the monotonic clock. */
export function parseRetryAfter(value: string | null | undefined, wallNowMs: number = Date.now()): number | undefined {
  if (typeof value !== 'string') return undefined;
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  if (/^\d+$/.test(trimmed)) {
    const seconds = Number(trimmed);
    return Number.isSafeInteger(seconds) && seconds > 0 ? seconds * 1000 : undefined;
  }
  if (!/[a-z]/i.test(trimmed)) return undefined;
  const at = Date.parse(trimmed);
  if (!Number.isFinite(at)) return undefined;
  const ms = at - wallNowMs;
  return ms > 0 ? Math.ceil(ms / 1000) * 1000 : undefined;
}

/** A server floor carried by an error: retryAfterMs, or ApiError's retryAfterSeconds. */
export function serverFloorFrom(error: unknown): ServerFloor | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const e = error as {retryAfterMs?: unknown; retryAfterSeconds?: unknown};
  if (typeof e.retryAfterMs === 'number' && Number.isFinite(e.retryAfterMs) && e.retryAfterMs > 0) return {retryAfterMs: e.retryAfterMs};
  if (typeof e.retryAfterSeconds === 'number' && Number.isFinite(e.retryAfterSeconds) && e.retryAfterSeconds > 0) return {retryAfterMs: e.retryAfterSeconds * 1000};
  return undefined;
}

/** Cancellable sleep on the monotonic clock. Rejects with an AbortError-shaped error on abort. */
export function wait(delayMs: number, options: Readonly<{signal?: AbortSignal; scheduler?: Scheduler}> = {}): Promise<void> {
  const {signal, scheduler = systemScheduler} = options;
  delayValue(delayMs, 'wait');
  return new Promise<void>((resolve, reject) => {
    try {throwIfAborted(signal);} catch (error) {reject(error); return;}
    const end = scheduler.now() + delayMs;
    let handle: unknown;
    const onAbort = () => {
      scheduler.clearTimeout(handle);
      try {throwIfAborted(signal);} catch (error) {reject(error);}
    };
    const arm = () => {
      const left = end - scheduler.now();
      if (left <= 0) {signal?.removeEventListener('abort', onAbort); resolve(); return;}
      handle = scheduler.setTimeout(arm, Math.min(left, MAX_TIMER_MS));
    };
    signal?.addEventListener('abort', onAbort, {once: true});
    arm();
  });
}

export type Repeating = Readonly<{
  /** Aborts the run in flight; nothing runs again. */
  stop(): void;
  /** Runs as soon as the run in flight (if any) settles, instead of waiting out the delay. */
  runNow(): void;
}>;

/** At most one run in flight. The next run is scheduled when the previous one settles: everyMs after
 * a success (raised by any ServerFloor the task returns), backoff after a failure (raised by any floor
 * the error carries). A success resets the failure count. */
export function repeat(task: (signal: AbortSignal) => Promise<ServerFloor | void>, options: Readonly<{
  everyMs: number;
  backoff: Backoff;
  /** Default true: the first run starts at once. */
  immediate?: boolean;
  signal?: AbortSignal;
  scheduler?: Scheduler;
  random?: () => number;
  onError?: (error: unknown, failures: number) => void;
}>): Repeating {
  const {everyMs, backoff, signal, scheduler = systemScheduler, random = Math.random, onError} = options;
  delayValue(everyMs, 'repeat interval');
  const steady: Backoff = {baseMs: everyMs, maxMs: everyMs, jitter: 'none'};
  let stopped = false, running = false, again = false, failures = 0;
  let handle: unknown, run: AbortController | undefined;
  const schedule = (delay: number) => {
    handle = scheduler.setTimeout(() => {handle = undefined; void tick();}, Math.min(delay, MAX_TIMER_MS));
  };
  const tick = async () => {
    if (stopped) return;
    running = true;
    const controller = run = new AbortController();
    let delay: number;
    try {
      const floor = await task(controller.signal);
      failures = 0;
      delay = nextDelay(steady, 1, floor || undefined, random);
    } catch (error) {
      failures++;
      if (!stopped && onError) releaseQuietly('repeat.onError', () => onError(error, failures));
      delay = nextDelay(backoff, failures, serverFloorFrom(error), random);
    } finally {
      running = false;
      if (run === controller) run = undefined;
    }
    if (stopped) return;
    if (again) {again = false; schedule(0);} else schedule(delay);
  };
  const stop = () => {
    if (stopped) return;
    stopped = true;
    if (handle !== undefined) scheduler.clearTimeout(handle);
    handle = undefined;
    signal?.removeEventListener('abort', stop);
    run?.abort();
  };
  if (signal?.aborted) stopped = true;
  else {
    signal?.addEventListener('abort', stop, {once: true});
    schedule(options.immediate === false ? everyMs : 0);
  }
  return Object.freeze({
    stop,
    runNow() {
      if (stopped) return;
      if (running) {again = true; return;}
      if (handle !== undefined) scheduler.clearTimeout(handle);
      schedule(0);
    },
  });
}
