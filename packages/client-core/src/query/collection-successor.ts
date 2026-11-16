import type {Item} from '../../../contracts/src/next/api.ts';
import type {DownloadsApi} from '../downloads-service.ts';
import {throwIfAborted} from '../abort-lifetime.ts';
import {serverPath} from './server-api.ts';

/** The title after `currentItemId` in a collection, in the order the collection's page lists it
 * (`GET /v1/items?collection=`): ten pages of sixty at most, same cursor and cancellation. A title
 * not available to play is skipped; the walk gives up rather than guessing. */
export async function readCollectionSuccessor<T = Item>(api: DownloadsApi, collectionId: string, currentItemId: string, signal: AbortSignal,
  project: (item: Item) => T = item => item as unknown as T): Promise<T | undefined> {
  let cursor = '', after = false;
  for (let page = 0; page < 10; page++) {
    throwIfAborted(signal);
    const raw = await api.request<{items?: Item[]; nextCursor?: string}>(serverPath('GET /v1/items', {query: {collection: collectionId, limit: 60, cursor}}), 'GET', undefined, signal);
    const items = Array.isArray(raw.items) ? raw.items : [];
    if (after) {
      // The current title was last on its page; the successor is the first available title of the next page.
      const next = items.find(item => item.available !== false);
      if (next) return project(next);
      if (!raw.nextCursor) return undefined;
      cursor = raw.nextCursor;
      continue;
    }
    const at = items.findIndex(item => item.id === currentItemId);
    if (at >= 0) {
      const next = items.slice(at + 1).find(item => item.available !== false);
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
