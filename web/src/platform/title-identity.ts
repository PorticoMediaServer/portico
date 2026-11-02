import {readItemDetail, type DetailProjection} from '@core/detail.ts';
import {queryKeys} from '@core/query/keys.ts';
import type {ViewerQueries} from '@core/query/viewer-queries.ts';

/** A title's own library and kind, read from its detail route. Unlike the bare item read
 * (`GET /v1/items/{id}`, timed media only), the detail route serves every kind the viewer may
 * see, Reading included, under the same privacy fence, so a Reading title never "vanishes"
 * behind the wrong route (P2-74 E-web-23). It is the same cached page the title's screen shows:
 * a route guard, the screen and a card menu asking for one title read it once. */
export type TitleIdentity = Readonly<{id: string; libraryId: string; kind: string; title: string; posterUrl?: string; backdropUrl?: string}>;

export async function readTitleIdentity(queries: ViewerQueries, itemId: string, signal?: AbortSignal): Promise<TitleIdentity> {
  signal?.throwIfAborted();
  // Either form of the page names the title's library; the one already held costs nothing.
  const held = queries.client.getQueryData<DetailProjection>(queryKeys.itemDetail(itemId, 'all'));
  const page = held ?? await abortable(readItemDetail(queries, itemId, 'none'), signal);
  signal?.throwIfAborted();
  const item = page.item;
  if (item.id !== itemId || page.scope.itemId !== itemId) throw new Error('Invalid title identity');
  return Object.freeze({id: itemId, libraryId: page.scope.libraryId, kind: item.kind, title: item.title,
    ...(item.posterUrl ? {posterUrl: item.posterUrl} : {}), ...(item.backdropUrl ? {backdropUrl: item.backdropUrl} : {})});
}

/** The shared read goes on for its other owners; this caller stops waiting when it is cancelled. */
function abortable<T>(promise: Promise<T>, signal?: AbortSignal): Promise<T> {
  if (!signal) return promise;
  return new Promise<T>((resolve, reject) => {
    const stop = () => reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
    signal.addEventListener('abort', stop, {once: true});
    promise.then(value => {signal.removeEventListener('abort', stop); resolve(value);}, error => {signal.removeEventListener('abort', stop); reject(error);});
  });
}
