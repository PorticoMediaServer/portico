import {readCollectionSuccessor} from '@core/query/collection-successor.ts';

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
  const next = await readCollectionSuccessor(api, collection.id, currentItemId, signal);
  return next ? Object.freeze({collectionName: collection.name, id: next.id, title: next.title,
    ...(typeof next.year === 'number' ? {year: next.year} : {}), ...(next.posterUrl ? {posterUrl: next.posterUrl} : {}), play: () => play(next.id)}) : undefined;
}
