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
/** A person's own state on a title: watched, favourite, My List, rating, hidden and position, on the one route that sets them. */
const itemState = {kind: 'personal' as OutboxKind, method: 'PUT', parts: '/v1/items/{id}/state'.split('/')};
const outboxRoutes = [itemState, ...routes.filter(route => kindOf.has(route.id)).map(route => ({kind: kindOf.get(route.id)!, method: route.method, parts: route.path.split('/')}))];
if (outboxRoutes.length !== kindOf.size + 1) throw new Error('An outbox route is missing from the generated routes.');

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

/**
 * What is sent for a kept edit. A change to a title's state says how long ago the person made it
 * (`madeAgoMs`), so the server can order it against edits from other devices; the server never
 * reads this device's clock, only that length of time.
 */
export function outboxBody(entry: Readonly<{path: string; method: string; body?: unknown; createdAt: number}>, now: number): unknown {
  const segments = entry.path.split('?')[0]!.split('/');
  const state = entry.method === itemState.method && segments.length === itemState.parts.length && itemState.parts.every((part, i) => part.startsWith('{') ? !!segments[i] : part === segments[i]);
  if (!state || typeof entry.body !== 'object' || entry.body === null) return entry.body;
  return {...entry.body, madeAgoMs: Math.max(0, Math.round(now - entry.createdAt))};
}
