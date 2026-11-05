import {encodedRoutePath} from '../../../contracts/src/routes.ts';
import type {GetCollectionsIdItemsResponse} from '../../../contracts/src/types.ts';
import type {DownloadsApi} from '../downloads-service.ts';

/** The collection successor in server order: ten pages of sixty, same opaque cursor and cancellation. */
export async function readCollectionSuccessor<T = GetCollectionsIdItemsResponse['items'][number]>(api: DownloadsApi, collectionId: string, currentItemId: string, signal: AbortSignal, project: (item: GetCollectionsIdItemsResponse['items'][number]) => T = item => item as unknown as T): Promise<T | undefined> {
  let cursor = '', after = false;
  for (let page = 0; page < 10; page++) {
    signal.throwIfAborted();
    const path = encodedRoutePath('get_collections_id_items', {id: encodeURIComponent(collectionId)})
      + `?sort=groupOrder&direction=asc&limit=60${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`;
    const raw = await api.request<GetCollectionsIdItemsResponse>(path, 'GET', undefined, signal);
    const items = Array.isArray(raw.items) ? raw.items : [];
    if (after) {
      // The original member was last on its page; the successor is the first
      // available member of the next page, never a guess.
      const next = items.find(item => item.available && !item.missing);
      if (next) return project(next);
      if (!raw.nextCursor) return undefined;
      cursor = raw.nextCursor;
      continue;
    }
    const at = items.findIndex(item => item.id === currentItemId);
    if (at >= 0) {
      const next = items.slice(at + 1).find(item => item.available && !item.missing);
      if (next) return project(next);
      if (!raw.nextCursor) return undefined;
      after = true;cursor = raw.nextCursor;
      continue;
    }
    if (!raw.nextCursor) return undefined;
    cursor = raw.nextCursor;
  }
  return undefined;
}
