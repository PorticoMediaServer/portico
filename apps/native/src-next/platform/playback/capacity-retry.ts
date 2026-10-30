import {startSignal} from './start-signal';
import {header, type V1Http, type V1Request, type V1Response} from '../../../../../packages/client-core/src/playback-v1/http.ts';
import type {Timers} from '../../../../../packages/client-core/src/playback-v1/sessions.ts';
import {routePath} from '../../../../../packages/contracts/src/routes.ts';

/** One server-directed wait on one original idempotent start, never a new Play. */
export type NativePlaybackCapacityWait = Readonly<{
  attempt: number;
  retryAt: number;
  isCurrent(): boolean;
  /** Explicit viewer retry of this same captured start; never creates another request key. */
  retryNow(): void;
  cancel(): void;
}>;

function directedWait(response: V1Response): number | undefined {
  const error = (response.body as {error?: {code?: unknown; retry?: unknown; retryAfterMs?: unknown}} | undefined)?.error;
  if (response.status !== 429 || error?.code !== 'conversion_capacity' || error.retry !== 'same_request') return;
  const seconds = header(response, 'Retry-After');
  const delay = seconds && /^\d+$/.test(seconds) ? Number(seconds) * 1000 : error.retryAfterMs;
  return typeof delay === 'number' && Number.isSafeInteger(delay) && delay > 0 ? delay : undefined;
}

/** Retry only a definite capacity refusal for an idempotent start. Transport failures,
 * other refusals and missing server timing return unchanged to the existing error path. */
export function capacityRetryHttp(input: Readonly<{
  http: V1Http; life: AbortSignal; isCurrent(): boolean; captureCurrent(request: V1Request): (() => boolean) | undefined; timers?: Timers; foreground(): boolean; onVisible(listener: () => void): () => void;
  publish(wait: NativePlaybackCapacityWait | undefined): void;
}>): V1Http {
  const now = input.timers?.now ?? Date.now;
  const setTimer = input.timers?.setTimer ?? ((fn: () => void, ms: number) => setTimeout(fn, ms));
  const clearTimer = input.timers?.clearTimer ?? ((timer: unknown) => clearTimeout(timer as ReturnType<typeof setTimeout>));
  let active: NativePlaybackCapacityWait | undefined;
  let generation = 0;
  const starts: ReadonlySet<string> = new Set([routePath('start_playback_session'), routePath('create_queue')]);
  return {async send(request) {
    if (request.method !== 'POST' || !starts.has(request.path) || request.path === routePath('create_queue') && (!request.body || typeof request.body !== 'object' || !(request.body as {startPlayback?: unknown}).startPlayback)
      || !Object.entries(request.headers ?? {}).some(([key, value]) => key.toLowerCase() === 'idempotency-key' && !!value)) return input.http.send(request);
    const originalCurrent = input.captureCurrent(request);
    if (!originalCurrent) return input.http.send(request);
    const serial = ++generation;
    if (active) {active = undefined; input.publish(undefined);}
    const controller = new AbortController();
    const joined = startSignal(controller, [input.life, request.signal]), signal = joined.signal;
    // Match the original JSON wire body and key even if a caller changes its own object later.
    const captured = {...request, headers: {...request.headers}, body: request.body === undefined ? undefined : JSON.parse(JSON.stringify(request.body)), signal};
    const started = now();
    let refused: V1Response | undefined;
    const current = () => !signal.aborted && serial === generation && input.isCurrent() && originalCurrent();
    const check = () => {if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});};
    let mine: NativePlaybackCapacityWait | undefined;
    try {
      for (let attempt = 0; ; attempt++) {
        check();
        if (!input.foreground()) await new Promise<void>((resolve, reject) => {
          let off: (() => void) | undefined;
          const abort = () => {off?.();signal.removeEventListener('abort', abort);reject(Object.assign(new Error('Playback start retired'), {name: 'AbortError'}));};
          off = input.onVisible(() => {off?.();signal.removeEventListener('abort', abort);resolve();});
          signal.addEventListener('abort', abort, {once: true});if (signal.aborted) abort();
        });
        check();
        if (refused && now() - started > 30_000) return refused;
        const response = await input.http.send(captured);
        // Return a success to its original owner even after retirement, so it can clean up
        // an actual created session. A retired refusal must never schedule another write.
        const delay = directedWait(response);
        if (!current() || delay === undefined || attempt >= 5 || now() - started + delay > 30_000) return response;
        refused = response;
        let wake: (() => void) | undefined, requested = false;
        const wait: NativePlaybackCapacityWait = Object.freeze({attempt: attempt + 1, retryAt: now() + delay,
          isCurrent: () => current() && active === wait,
          retryNow: () => {if (current() && active === wait && !requested) {requested = true; wake?.();}},
          cancel: () => {if (current() && active === wait) controller.abort();}});
        mine = active = wait;
        input.publish(wait);
        check(); // Publishing may synchronously retire or replace the playback.
        await new Promise<void>((resolve, reject) => {
          let timer: unknown;
          const abort = () => {clearTimer(timer); signal.removeEventListener('abort', abort); reject(Object.assign(new Error('Playback start retired'), {name: 'AbortError'}));};
          wake = () => {clearTimer(timer); signal.removeEventListener('abort', abort); resolve();};
          timer = setTimer(wake, delay);
          signal.addEventListener('abort', abort, {once: true});
          if (signal.aborted) abort();
          else if (requested) wake();
        });
      }
    } finally {
      joined.close();
      if (active === mine && mine) {active = undefined; input.publish(undefined);}
    }
  }};
}
