import {encodedRoutePath} from '../../../contracts/src/routes.ts';
import type {ItemResponse} from '../media-item.ts';

/** Uncached playing-item read, preserving the original retry and timer budget. */
export async function readPlaybackItemBounded(api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}, itemId: string, signal: AbortSignal, current: () => boolean, timers: {setTimer(run: () => void, ms: number): unknown; clearTimer(timer: unknown): void}): Promise<ItemResponse> {
  const path = encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)});
  for (let attempt = 0; ; attempt++) {
    // A read that hangs is a failure too: each attempt gets 8 s.
    const attemptLife = new AbortController(), expiry = timers.setTimer(() => attemptLife.abort(), 8000);
    try { return await api.request<ItemResponse>(path, 'GET', undefined, AbortSignal.any([signal, attemptLife.signal])); }
    catch (error) {
      const failure = error as {name?: string; status?: number; retryable?: boolean} | undefined;
      const timedOut = attemptLife.signal.aborted && !signal.aborted;
      const passing = timedOut || failure?.retryable === true || (failure?.status ?? 0) >= 500;
      if (attempt >= 2 || !passing || failure?.name === 'AbortError' && !timedOut || signal.aborted || !current()) throw error;
      await new Promise<void>((resolve, reject) => {
        const timer = timers.setTimer(() => {signal.removeEventListener('abort', stop); resolve();}, attempt ? 3000 : 1000);
        const stop = () => {timers.clearTimer(timer); reject(new DOMException('Playback metadata read stopped', 'AbortError'));};
        signal.addEventListener('abort', stop, {once: true});
      });
    } finally { timers.clearTimer(expiry); }
  }
}
