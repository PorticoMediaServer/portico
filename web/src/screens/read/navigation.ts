import {ReadingError, type ReadingClient} from '@portico/client-core';

/** Only the genuine reading resource can classify an item as Read. Audiobook
 * ContentEntry.kind='book' never participates in this decision. */
export async function readItemEligibility(client: ReadingClient, itemId: string, signal?: AbortSignal) {
  try {
    const result = await client.versions(itemId, signal);
    // The server chooses which version to open; the client never picks the first.
    return result.versions.length ? Object.freeze({itemId, versions: result.versions, chosenSourceId: result.chosenSourceId}) : null;
  } catch (error) {if (error instanceof ReadingError && error.status === 404) return null; throw error;}
}
export type ReadDestination = Readonly<{to: '/read/$itemId'; params: {itemId: string}; search: {sourceId?: string}}>;
export function readDestination(itemId: string, sourceId: string): ReadDestination {
  return {to: '/read/$itemId', params: {itemId}, search: {sourceId}};
}
/** A shelf's Read opens the version the server chooses for this reader (`chosenSourceId`),
 * exactly as the title page does, never the row's own version. */
export function readChosenDestination(itemId: string): ReadDestination {
  return {to: '/read/$itemId', params: {itemId}, search: {}};
}
export type ReadItemEligibility = NonNullable<Awaited<ReturnType<typeof readItemEligibility>>>;
