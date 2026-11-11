import type {Routes} from '../../contracts/src/next/api.ts';
import {apiRoot, routes} from '../../contracts/src/routes.ts';

/**
 * The writes the outbox carries: what a person does while the server is out of reach. Personal
 * state, list and playlist edits, and the final position of a stop. Nothing else: an owner's
 * settings save that gets no answer shows an error and the form reads the current value again.
 */
const kinds = {
  personal: ['put_items_itemId_reading_favorite', 'put_items_itemId_reading_mark', 'put_items_itemId_reading_my_list', 'put_reading_group_my_list',
    'post_libraries_libraryId_reading_mark_all', 'delete_reading_saved_group_reference', 'post_channels_preferences', 'post_library_channels_preferences'],
  list: ['post_playlists', 'patch_playlists_id', 'delete_playlists_id', 'post_playlists_id_entries', 'delete_playlists_id_entries_entryId', 'reorder_playlist_window',
    'post_saved_resources', 'patch_saved_resources_id', 'delete_saved_resources_id', 'put_saved_resources_id_entries', 'reorder_saved_collection',
    'put_saved_pins_order', 'put_saved_pins_kind_id'],
  stop: ['stop_playback_session'],
} as const;
export type OutboxKind = keyof typeof kinds;
const kindOf = new Map<string, OutboxKind>(Object.entries(kinds).flatMap(([kind, ids]) => ids.map(id => [id, kind as OutboxKind] as const)));
/** One route of the new server the outbox carries. */
const nextRoute = (route: keyof Routes) => ({kind: 'personal' as OutboxKind, method: route.slice(0, route.indexOf(' ')), parts: route.slice(route.indexOf(' ') + 1).split('/')});
/** A person's own state on a title: watched, favourite, My List, rating, hidden and position, on the one route that sets them. */
const itemState = nextRoute('PUT /v1/items/{id}/state');
/** Several of those at once, and the plays a device made out of reach (a download played with no server). */
const stateBatch = nextRoute('POST /v1/items/state:batch');
/** Asking for a download, and removing one: a wish made out of reach is sent later, like any other tap. */
const nextRoutes = [itemState, stateBatch, nextRoute('POST /v1/downloads'), nextRoute('DELETE /v1/downloads/{id}')];
const outboxRoutes = [...nextRoutes, ...routes.filter(route => kindOf.has(route.id)).map(route => ({kind: kindOf.get(route.id)!, method: route.method, parts: route.path.split('/')}))];
if (outboxRoutes.length !== kindOf.size + nextRoutes.length) throw new Error('An outbox route is missing from the generated routes.');

/** Which kind of write this is, when the outbox carries it: the words for it differ, and a stop is never reported. */
export function outboxKind(path: string, method: string): OutboxKind | undefined {
  if (!path.startsWith(apiRoot) || /[#\\]/.test(path)) return undefined;
  const segments = path.split('?')[0]!.split('/');
  return outboxRoutes.find(route => route.method === method && route.parts.length === segments.length && route.parts.every((part, i) => {
    const open = part.indexOf('{'), value = segments[i]!;
    if (open < 0) return part === value;
    const prefix = part.slice(0, open), suffix = part.slice(part.indexOf('}') + 1);
    return value.startsWith(prefix) && value.endsWith(suffix) && value.length > prefix.length + suffix.length && !/%(?:2f|5c)/i.test(value);
  }))?.kind;
}
/** Whether a write to this path goes through the outbox. */
export const isOutboxRoute = (path: string, method: string): boolean => outboxKind(path, method) !== undefined;

const matches = (route: {method: string; parts: string[]}, method: string, segments: string[]) =>
  method === route.method && segments.length === route.parts.length && route.parts.every((part, i) => part.startsWith('{') ? !!segments[i] : part === segments[i]);

/**
 * What is sent for a kept edit. A change to a title's state says how long ago the person made it
 * (`madeAgoMs`), so the server can order it against edits from other devices; the server never
 * reads this device's clock, only that length of time. In a batch every edit and every play
 * says so, adding the time it waited here to any age it already carried.
 */
export function outboxBody(entry: Readonly<{path: string; method: string; body?: unknown; createdAt: number}>, now: number): unknown {
  const segments = entry.path.split('?')[0]!.split('/');
  if (typeof entry.body !== 'object' || entry.body === null) return entry.body;
  const waited = Math.max(0, Math.round(now - entry.createdAt));
  if (matches(itemState, entry.method, segments)) return {...entry.body, madeAgoMs: waited};
  if (!matches(stateBatch, entry.method, segments)) return entry.body;
  const aged = (list: unknown) => Array.isArray(list)
    ? list.map(one => typeof one === 'object' && one !== null ? {...one, madeAgoMs: Math.max(0, Number((one as {madeAgoMs?: unknown}).madeAgoMs) || 0) + waited} : one)
    : list;
  const batch = entry.body as {items?: unknown; plays?: unknown};
  return {...batch, items: aged(batch.items ?? []), ...(batch.plays === undefined ? {} : {plays: aged(batch.plays)})};
}
