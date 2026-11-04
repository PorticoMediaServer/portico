import {encodedRoutePath, routePath} from '../../../contracts/src/routes.ts';
import type {RouteResponse} from '../../../contracts/src/routes.ts';
import {parseMediaItem, type ItemResponse} from '../media-item.ts';
import {parseLibraryList} from '../library-list.ts';

export interface CatalogueReadApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
/** Keep the wire item available where playback needs fields the UI projection omits.
 * The existing owner retains its validation/currentness order and retry budget. */
export async function readCatalogueItemResponse(api: CatalogueReadApi, itemId: string, signal?: AbortSignal): Promise<ItemResponse> {
  return await api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}), 'GET', undefined, signal) as ItemResponse;
}
export async function readCatalogueItem(api: CatalogueReadApi, itemId: string, signal?: AbortSignal) {
  return parseMediaItem(await readCatalogueItemResponse(api, itemId, signal));
}
/** Return the same raw directory for strict membership proofs alongside tolerant labels. */
export async function readLibraryDirectory(api: CatalogueReadApi, signal?: AbortSignal) {
  const raw = await api.request(routePath('get_libraries'), 'GET', undefined, signal) as RouteResponse<'get_libraries'>;
  return {raw, value: parseLibraryList(raw)};
}
export async function readLibraries(api: CatalogueReadApi, signal?: AbortSignal) {
  return (await readLibraryDirectory(api, signal)).value;
}

/** Playback's existing three-attempt metadata read. Timer ownership stays with
 * the platform so disposal and test clocks keep the same lifetime. */
export async function readPlaybackCatalogueItem(api: CatalogueReadApi, itemId: string, lifetime: Readonly<{
  signal: AbortSignal; isCurrent: () => boolean;
  setTimer: (callback: () => void, ms: number) => unknown; clearTimer: (timer: unknown) => void;
}>): Promise<ItemResponse> {
  const {signal} = lifetime;
  for (let attempt = 0; ; attempt++) {
    // A read that hangs is a failure too: each attempt gets 8 s.
    const attemptLife = new AbortController(), expiry = lifetime.setTimer(() => attemptLife.abort(), 8000);
    try { return await readCatalogueItemResponse(api, itemId, AbortSignal.any([signal, attemptLife.signal])); }
    catch (error) {
      const failure = error as {name?: string; status?: number; retryable?: boolean} | undefined;
      const timedOut = attemptLife.signal.aborted && !signal.aborted;
      const passing = timedOut || failure?.retryable === true || (failure?.status ?? 0) >= 500;
      if (attempt >= 2 || !passing || failure?.name === 'AbortError' && !timedOut || signal.aborted || !lifetime.isCurrent()) throw error;
      await new Promise<void>((resolve, reject) => {
        const timer = lifetime.setTimer(() => {signal.removeEventListener('abort', stop); resolve();}, attempt ? 3000 : 1000);
        const stop = () => {lifetime.clearTimer(timer); reject(new DOMException('Playback metadata read stopped', 'AbortError'));};
        signal.addEventListener('abort', stop, {once: true});
      });
    } finally { lifetime.clearTimer(expiry); }
  }
}
