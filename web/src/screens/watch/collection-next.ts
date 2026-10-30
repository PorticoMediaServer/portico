import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import type {GetCollectionsIdItemsResponse} from '../../../../packages/contracts/src/types.ts';

/** The member after a title in its collection, in the collection's own order. */
export type WatchCollectionNext = Readonly<{
  collectionName: string;
  id: string;
  title: string;
  year?: number;
  posterUrl?: string;
  play: () => Promise<void>;
}>;

type Api = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

/**
 * desktop-player f4b's next-in-collection card. The members are read in the server's own
 * `order_key, item_id` order (`sort=groupOrder`), page by page and bounded, so the named
 * title is the collection's actual successor and never an unordered neighbour. A member
 * that is missing or unavailable is skipped; the walk gives up rather than guessing.
 */
export async function readCollectionNext(api: Api, collection: Readonly<{id: string; name: string}>, currentItemId: string,
  play: (itemId: string) => Promise<void>, signal: AbortSignal): Promise<WatchCollectionNext | undefined> {
  let cursor = '', after = false;
  const member = (next: GetCollectionsIdItemsResponse['items'][number]): WatchCollectionNext => Object.freeze({collectionName: collection.name, id: next.id, title: next.title,
    ...(typeof next.year === 'number' ? {year: next.year} : {}), ...(next.posterUrl ? {posterUrl: next.posterUrl} : {}), play: () => play(next.id)});
  for (let page = 0; page < 10; page++) {
    signal.throwIfAborted();
    const path = encodedRoutePath('get_collections_id_items', {id: encodeURIComponent(collection.id)})
      + `?sort=groupOrder&direction=asc&limit=60${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`;
    const raw = await api.request<GetCollectionsIdItemsResponse>(path, 'GET', undefined, signal);
    const items = Array.isArray(raw.items) ? raw.items : [];
    if (after) {
      // The original member was last on its page; the successor is the first
      // available member of the next page, never a guess.
      const next = items.find(item => item.available && !item.missing);
      if (next) return member(next);
      if (!raw.nextCursor) return undefined;
      cursor = raw.nextCursor;
      continue;
    }
    const at = items.findIndex(item => item.id === currentItemId);
    if (at >= 0) {
      const next = items.slice(at + 1).find(item => item.available && !item.missing);
      if (next) return member(next);
      if (!raw.nextCursor) return undefined;
      after = true;cursor = raw.nextCursor;
      continue;
    }
    if (!raw.nextCursor) return undefined;
    cursor = raw.nextCursor;
  }
  return undefined;
}
