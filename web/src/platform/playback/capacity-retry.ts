import {header, type V1Http, type V1Response} from '@core/playback-v1/http.ts';
import type {Timers} from '@core/playback-v1/sessions.ts';
import type {PageVisibility} from '@core/index.ts';
import {routePath} from '../../../../packages/contracts/src/routes.ts';

/** One server-directed wait on one original idempotent start, never a new Play. */
export type WebPlaybackCapacityWait = Readonly<{
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
  http: V1Http; life: AbortSignal; isCurrent(): boolean; captureCurrent(): (() => boolean) | undefined; timers?: Timers;
  visibility?: PageVisibility;
  publish(wait: WebPlaybackCapacityWait | undefined): void;
}>): V1Http {
  const now = input.timers?.now ?? Date.now;
  const setTimer = input.timers?.setTimer ?? ((fn: () => void, ms: number) => setTimeout(fn, ms));
  const clearTimer = input.timers?.clearTimer ?? ((timer: unknown) => clearTimeout(timer as ReturnType<typeof setTimeout>));
  let active: WebPlaybackCapacityWait | undefined;
  let generation = 0;
  const starts: ReadonlySet<string> = new Set([routePath('start_playback_session'), routePath('create_queue')]);
  return {async send(request) {
    if (request.method !== 'POST' || !starts.has(request.path)
      || !Object.entries(request.headers ?? {}).some(([key, value]) => key.toLowerCase() === 'idempotency-key' && !!value)) return input.http.send(request);
    const originalCurrent = input.captureCurrent();
    if (!originalCurrent) return input.http.send(request);
    const serial = ++generation;
    if (active) {active = undefined; input.publish(undefined);}
    const controller = new AbortController();
    const signal = AbortSignal.any([input.life, controller.signal, ...(request.signal ? [request.signal] : [])]);
    // Match the original JSON wire body and key even if a caller changes its own object later.
    const captured = {...request, headers: {...request.headers}, body: request.body === undefined ? undefined : JSON.parse(JSON.stringify(request.body)), signal};
    const started = now();
    const current = () => !signal.aborted && serial === generation && input.isCurrent() && originalCurrent();
    const check = () => {signal.throwIfAborted(); if (!current()) throw new DOMException('Playback start retired', 'AbortError');};
    const waitForForeground = async () => {
      while (input.visibility?.hidden()) {
        check();
        await new Promise<void>((resolve, reject) => {
          let off: (() => void) | undefined, settled = false;
          const finish = (aborted = false) => {
            if (settled) return;
            settled = true; off?.(); signal.removeEventListener('abort', abort);
            if (aborted) reject(new DOMException('Playback start retired', 'AbortError')); else resolve();
          };
          const abort = () => finish(true);
          off = input.visibility!.onVisible(() => {if (!input.visibility!.hidden()) finish();});
          if (settled) off();
          else {
            signal.addEventListener('abort', abort, {once: true});
            if (signal.aborted) abort(); else if (!input.visibility!.hidden()) finish();
          }
        });
      }
    };
    let mine: WebPlaybackCapacityWait | undefined;
    let refused: V1Response | undefined;
    try {
      for (let attempt = 0; ; attempt++) {
        check();
        if (refused) {
          if (now() - started > 30_000) return refused;
          await waitForForeground();
          check();
          // A timer or visibility callback can arrive long after its deadline in
          // a suspended tab. Recheck immediately before every repeated write.
          if (now() - started > 30_000) return refused;
        }
        const response = await input.http.send(captured);
        // Return a success to its original owner even after retirement, so it can clean up
        // an actual created session. A retired refusal must never schedule another write.
        const delay = directedWait(response);
        if (!current() || delay === undefined || attempt >= 5 || now() - started + delay > 30_000) return response;
        refused = response;
        let wake: (() => void) | undefined, requested = false;
        const wait: WebPlaybackCapacityWait = Object.freeze({attempt: attempt + 1, retryAt: now() + delay,
          isCurrent: () => current() && active === wait,
          retryNow: () => {if (current() && active === wait && !requested) {requested = true; wake?.();}},
          cancel: () => {if (current() && active === wait) controller.abort();}});
        mine = active = wait;
        input.publish(wait);
        check(); // Publishing may synchronously retire or replace the playback.
        await new Promise<void>((resolve, reject) => {
          let timer: unknown;
          const abort = () => {clearTimer(timer); signal.removeEventListener('abort', abort); reject(new DOMException('Playback start retired', 'AbortError'));};
          wake = () => {clearTimer(timer); signal.removeEventListener('abort', abort); resolve();};
          timer = setTimer(wake, delay);
          signal.addEventListener('abort', abort, {once: true});
          if (signal.aborted) abort();
          else if (requested) wake();
        });
      }
    } finally {
      if (active === mine && mine) {active = undefined; input.publish(undefined);}
    }
  }};
}
