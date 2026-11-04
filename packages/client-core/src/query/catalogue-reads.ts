import {encodedRoutePath, routePath, routePattern} from '../../../contracts/src/routes.ts';
import type {RouteResponse} from '../../../contracts/src/routes.ts';
import {parseMediaItem, type ItemResponse} from '../media-item.ts';
import {parseLibraryList} from '../library-list.ts';

export interface CatalogueReadApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
/** Keep the wire item available where playback needs fields the UI projection omits.
 * The existing owner retains its validation/currentness order and retry budget. */
export function readCatalogueItemResponse(api: CatalogueReadApi, itemId: string, signal?: AbortSignal): Promise<ItemResponse> {
  return readCatalogueItemEncodedResponse(api, encodeURIComponent(itemId), signal);
}
/** Preserve the three native callers that already passed an escaped route ID. */
export function readCatalogueItemEncodedResponse(api: CatalogueReadApi, itemId: string, signal?: AbortSignal): Promise<ItemResponse> {
  return api.request(encodedRoutePath('get_items_id', {id: itemId}), 'GET', undefined, signal) as Promise<ItemResponse>;
}
/** Cast's original template encoding intentionally keeps its existing refusal
 * boundary; replacing it with a stricter route builder would change behavior. */
export function readCastCatalogueItemResponse(api: CatalogueReadApi, itemId: string, signal?: AbortSignal): Promise<ItemResponse> {
  const path = routePattern('get_items_id').replace('{id}', encodeURIComponent(itemId));
  return api.request(path, 'GET', undefined, signal) as Promise<ItemResponse>;
}
export async function readCatalogueItem(api: CatalogueReadApi, itemId: string, signal?: AbortSignal) {
  return parseMediaItem(await readCatalogueItemResponse(api, itemId, signal));
}
/** Return the same raw directory for strict membership proofs alongside tolerant labels. */
export function readLibraryDirectory(api: CatalogueReadApi, signal?: AbortSignal) {
  return api.request(routePath('get_libraries'), 'GET', undefined, signal).then(raw =>
    ({raw: raw as RouteResponse<'get_libraries'>, value: parseLibraryList(raw)}));
}
export function readLibraries(api: CatalogueReadApi, signal?: AbortSignal) {
  return api.request(routePath('get_libraries'), 'GET', undefined, signal).then(parseLibraryList);
}

/** Playback's existing three-attempt metadata read. Timer ownership stays with
 * the platform so disposal and test clocks keep the same lifetime. */
export async function readPlaybackCatalogueItem(api: CatalogueReadApi, itemId: string, lifetime: Readonly<{
  signal: AbortSignal; isCurrent: () => boolean;
  setTimer: (callback: () => void, ms: number) => unknown; clearTimer: (timer: unknown) => void;
}>): Promise<ItemResponse> {
  const {signal} = lifetime;
  const path = encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)});
  for (let attempt = 0; ; attempt++) {
    // A read that hangs is a failure too: each attempt gets 8 s.
    const attemptLife = new AbortController(), expiry = lifetime.setTimer(() => attemptLife.abort(), 8000);
    try { return await api.request(path, 'GET', undefined, AbortSignal.any([signal, attemptLife.signal])) as ItemResponse; }
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
