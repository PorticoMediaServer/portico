/**
 * Saved's History on the rebuilt server: `GET /v1/history?period=` (the profile's finished plays, newest first) and
 * `DELETE /v1/history` (Clear history). Each reader answers in the shape `personal-saved.ts` already checks, so the
 * History screens are unchanged.
 *
 * Definition area 6: History lists what the profile finished playing, newest first, for the last 24 hours, 7, 30 or 90
 * days, or all (rule 18); a title removed from the server stays in History by name and cannot be opened to play, and a
 * title the profile may no longer see is absent, which the server decides (rule 19); Clear history empties History and
 * keeps watched marks, resume points, ratings and lists (rule 21).
 */
import type {HistoryEntry, HistoryPage, Item} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {wireEntryFromItem} from './items.ts';
import type {HistoryPeriod, SavedScope} from './saved.ts';

const PAGE = 40;
/** A removed title has no item: it is named, marked missing, and offers nothing to play. */
function mediaOf(entry: HistoryEntry): Record<string, unknown> {
  const card = entry.item ? wireEntryFromItem(entry.item as Item) : undefined;
  if (card) return card;
  return {id: entry.id, kind: entry.kind, title: entry.title, ...(entry.subtitle ? {subtitle: entry.subtitle} : {}), available: false, missing: true};
}
/** One page of History, in the old answer's shape: finished plays only, so every row is completed. */
export async function readHistoryPage(api: ServerRequester, bound: SavedScope, period: HistoryPeriod | undefined, cursor: string | null, signal?: AbortSignal): Promise<unknown> {
  const page: HistoryPage = await callServer(api, 'GET /v1/history', {query: {period: period ?? 'all', limit: PAGE, cursor: cursor ?? undefined}, signal});
  return {serverId: bound.serverId, viewerFence: 'viewer', revision: 0, nextCursor: page?.nextCursor ?? '',
    entries: (Array.isArray(page?.items) ? page.items : []).map(entry => ({id: entry.id, media: mediaOf(entry), updatedAt: entry.playedAt, positionMs: entry.watchedMs ?? 0, completed: true}))};
}
/** Clear history, answered as the old receipt the service checks. The server answers nothing to keep. */
export async function clearHistory(api: ServerRequester, bound: SavedScope, operationId: string, signal?: AbortSignal, _body?: unknown): Promise<unknown> {
  await callServer(api, 'DELETE /v1/history', {signal});
  return {serverId: bound.serverId, viewerFence: 'viewer', receipt: {operationId, revision: 0}};
}
