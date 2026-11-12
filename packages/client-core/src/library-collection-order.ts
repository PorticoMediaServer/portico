import {encodedRoutePath} from '../../contracts/src/route-paths.ts';

/** A request as the clients make it: the route's path, a method and a JSON body; it rejects with the
 * server's error (its `code`). */
export type CollectionRequest = <T>(path: string, method: 'GET' | 'PUT', body?: unknown) => Promise<T>;

/**
 * Moves a title of a library collection, the owner's own order: the title and the one it now
 * follows ("" the front). The move is fenced by the collection's revision, read first; when the
 * collection changed in between (another owner tool, a title added), it is read again and the
 * same move is sent once more, so a person never sees a conflict for a change that still makes
 * sense. Resolves with the collection's new revision.
 */
export async function moveCollectionTitle(request: CollectionRequest, collectionId: string, itemId: string, afterItemId: string): Promise<number> {
  const path = encodedRoutePath('reorder_library_collection', {id: encodeURIComponent(collectionId)});
  const read = async () => {
    const c = await request<{revision?: unknown}>(encodedRoutePath('get_collections_id', {id: encodeURIComponent(collectionId)}), 'GET');
    if (typeof c?.revision !== 'number' || !Number.isSafeInteger(c.revision) || c.revision < 1) throw Object.assign(new Error('The collection could not be read.'), {code: 'invalid_response'});
    return c.revision;
  };
  const send = async (revision: number) => {
    const out = await request<{revision?: unknown}>(path, 'PUT', {expectedRevision: revision, itemIds: [itemId], afterItemId});
    return typeof out?.revision === 'number' ? out.revision : revision;
  };
  try {
    return await send(await read());
  } catch (e) {
    if ((e as {code?: unknown})?.code !== 'revision_mismatch') throw e;
    return send(await read());
  }
}
