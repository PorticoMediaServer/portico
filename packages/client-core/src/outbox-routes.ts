import type {Routes} from '../../contracts/src/next/api.ts';
import {apiRoot, routes} from '../../contracts/src/routes.ts';

/**
 * The writes the outbox carries: what a person does while the server is out of reach. Personal
 * state, list and playlist edits, a profile's own settings, and the final position of a stop. Nothing
 * else: an owner's server settings save that gets no answer shows an error and the form reads the
 * current value again.
 */
const kinds = {
  personal: ['put_items_itemId_reading_favorite', 'put_items_itemId_reading_mark', 'put_items_itemId_reading_my_list',
    'post_libraries_libraryId_reading_mark_all'],
  stop: ['stop_playback_session'],
} as const;
export type OutboxKind = keyof typeof kinds | 'settings' | 'download' | 'list';
const kindOf = new Map<string, OutboxKind>(Object.entries(kinds).flatMap(([kind, ids]) => ids.map(id => [id, kind as OutboxKind] as const)));
/** A person's own state on a title: watched, favourite, My List, rating, hidden and position, on the one route that sets them. */
const itemStateRoute: keyof Routes = 'PUT /v1/items/{id}/state';
const itemState = {kind: 'personal' as OutboxKind, method: 'PUT', parts: itemStateRoute.slice(itemStateRoute.indexOf(' ') + 1).split('/')};
/** A profile's own settings: the server keeps the last save. */
const settingsRoute: keyof Routes = 'PATCH /v1/preferences';
const settings = {kind: 'settings' as OutboxKind, method: 'PATCH', parts: settingsRoute.slice(settingsRoute.indexOf(' ') + 1).split('/')};
const nextRoutes = (kind: OutboxKind, names: readonly (keyof Routes)[]) => names.map(route => ({kind, method: route.slice(0, route.indexOf(' ')), parts: route.slice(route.indexOf(' ') + 1).split('/')}));
const downloadRoutes = nextRoutes('download', ['POST /v1/downloads', 'DELETE /v1/downloads/{id}', 'DELETE /v1/downloads/{id}/items/{itemId}']);
/** Playlists, collections and saved views: made, renamed, removed, and titles added, moved and removed (area 6 rule 43). */
const listRoutes = nextRoutes('list', ['POST /v1/lists', 'PATCH /v1/lists/{id}', 'DELETE /v1/lists/{id}', 'POST /v1/lists/{id}/entries',
  'PATCH /v1/lists/{id}/entries/{entryId}', 'DELETE /v1/lists/{id}/entries/{entryId}']);
const playRouteName: keyof Routes = 'POST /v1/items/state:batch';
const playRoute = {kind: 'personal' as OutboxKind, method: 'POST', parts: playRouteName.slice(playRouteName.indexOf(' ') + 1).split('/')};
const outboxRoutes = [itemState, settings, playRoute, ...downloadRoutes, ...listRoutes, ...routes.filter(route => kindOf.has(route.id)).map(route => ({kind: kindOf.get(route.id)!, method: route.method, parts: route.path.split('/')}))];
if (outboxRoutes.length !== kindOf.size + 3 + downloadRoutes.length + listRoutes.length) throw new Error('An outbox route is missing from the generated routes.');

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

/** The oldest edit the server orders (35 days); an older one counts as of then. */
export const maxEditAgeMs = 35 * 24 * 60 * 60 * 1000;
/**
 * What is sent for a kept edit. Title state, profile settings and a final playback observation say
 * how long ago the person made them (`madeAgoMs`), so the server can order it against changes from other
 * devices; the server never reads this device's clock, only that length of time.
 */
export function outboxBody(entry: Readonly<{path: string; method: string; body?: unknown; createdAt: number}>, now: number): unknown {
  const segments = entry.path.split('?')[0]!.split('/');
  if (entry.method === 'POST' && segments.join('/') === playRoute.parts.join('/') && entry.body && typeof entry.body === 'object') {
    const body = entry.body as {items?: unknown; plays?: unknown};
    const age = (rows: unknown) => Array.isArray(rows) ? rows.map(row => ({...row, madeAgoMs: Math.max(0, Number(row.madeAgoMs) || 0) + Math.max(0, now - entry.createdAt)})) : rows;
    return {...body, ...(body.items === undefined ? {} : {items: age(body.items)}), ...(body.plays === undefined ? {} : {plays: age(body.plays)})};
  }
  const stop = outboxKind(entry.path, entry.method) === 'stop' && entry.body && typeof entry.body === 'object'
    && typeof (entry.body as {progressToken?: unknown}).progressToken === 'string' && (entry.body as {progressToken: string}).progressToken !== '';
  const ordered = stop || [itemState, settings].some(route => entry.method === route.method && segments.length === route.parts.length
    && route.parts.every((part, i) => part.startsWith('{') ? !!segments[i] : part === segments[i]));
  if (!ordered || typeof entry.body !== 'object' || entry.body === null) return entry.body;
  // An edit made earlier still, while nothing could take it (a place in a book read offline), says so itself.
  const before = Math.max(0, Number((entry.body as {madeAgoMs?: unknown}).madeAgoMs) || 0);
  return {...entry.body, madeAgoMs: Math.min(maxEditAgeMs, before + Math.max(0, Math.round(now - entry.createdAt)))};
}
