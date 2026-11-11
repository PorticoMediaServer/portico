import {callServer, type ServerRequester} from '../query/server-api.ts';
import type {PlayHistoryEntry, PlayHistoryPeriod} from '../play-history.ts';
import {dependsOn} from '../freshness-hub.ts';
import {queryKeys} from '../query/keys.ts';
import type {ViewerQueryOptions} from '../query/react.ts';

/**
 * Play history on the rebuilt server. `GET /v1/admin/play-history` lists every play, newest first, 100 a
 * page, for the owner and administrators, filtered by person, library, kind, text and a time from which.
 * It has no sort and no count: the page lists newest first and says how many it has shown.
 */
export type ServerPlayFilters = Readonly<{period: PlayHistoryPeriod; accountId?: string; libraryId?: string; media?: string; search?: string}>;
export type ServerPlayPage = Readonly<{entries: readonly PlayHistoryEntry[]; nextCursor: string}>;
export type ServerAccount = Readonly<{accountId: string; username: string}>;

export class ServerPlayHistoryError extends Error {
  readonly code = 'invalid_play_history';
  constructor() { super('invalid_play_history'); this.name = 'ServerPlayHistoryError'; }
}
const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === 'string' && v.length <= 4096;
const whole = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;

/** The page's kinds of media as the server's kinds of item. Live TV plays are not in the server's Play history. */
const mediaKinds: Readonly<Record<string, string>> = {movies: 'movie', tv: 'episode', music: 'track', audiobooks: 'audiobook'};
/** The server's kinds as the page names them (a song and an audiobook's file are drawn square). */
const pageKinds: Readonly<Record<string, string>> = {track: 'song', audiobook: 'audiobook_file'};

/** When a period starts: the start of today on this device, or so many days back. */
export function periodStart(period: PlayHistoryPeriod, now = new Date()): Date | undefined {
  if (period === 'all') return undefined;
  if (period === 'today') { const start = new Date(now); start.setHours(0, 0, 0, 0); return start; }
  const hours = period === '24h' ? 24 : Number.parseInt(period, 10) * (period.endsWith('y') ? 365 * 24 : 24);
  return new Date(now.getTime() - hours * 3_600_000);
}

/** One play in the page's shape. The server gives no id, so a row is named by what it is. */
export function playFromServer(value: unknown): PlayHistoryEntry {
  if (!object(value) || !text(value.playedAt) || !Number.isFinite(Date.parse(value.playedAt)) || !text(value.username) || !text(value.kind) ||
      !text(value.title) || !whole(value.watchedMs) || typeof value.away !== 'boolean') throw new ServerPlayHistoryError();
  const optional = (v: unknown) => text(v) && v !== '' ? v : undefined;
  const accountId = optional(value.accountId) ?? '', device = optional(value.deviceName);
  const entry: {-readonly [K in keyof PlayHistoryEntry]: PlayHistoryEntry[K]} = {
    id: [value.playedAt, accountId || value.username, device ?? '', value.title, value.watchedMs].join('\u0000'),
    user: value.username, accountId, kind: pageKinds[value.kind] ?? value.kind, title: value.title, libraryId: optional(value.libraryId) ?? '',
    startedAt: value.playedAt, positionSeconds: 0, durationSeconds: 0, completed: false, watchedMs: value.watchedMs,
    location: value.away ? 'remote' : 'local',
  };
  const profile = optional(value.profile), parentTitle = optional(value.subtitle), itemId = optional(value.itemId);
  const libraryName = optional(value.libraryId) ? optional(value.libraryName) : undefined, delivery = optional(value.delivery);
  if (profile) entry.profile = profile;
  if (parentTitle) entry.parentTitle = parentTitle;
  if (itemId) entry.itemId = itemId;
  if (device) entry.device = device;
  if (libraryName) entry.libraryName = libraryName;
  if (delivery) entry.delivery = delivery;
  return Object.freeze(entry);
}

/** One page of plays, newest first. */
export async function readServerPlayHistory(api: ServerRequester, filters: ServerPlayFilters, cursor = '', signal?: AbortSignal): Promise<ServerPlayPage> {
  const from = periodStart(filters.period);
  const raw: unknown = await callServer(api, 'GET /v1/admin/play-history', {signal, query: {
    person: filters.accountId, library: filters.libraryId, kind: filters.media ? mediaKinds[filters.media] ?? filters.media : undefined,
    text: filters.search?.trim(), from: from?.toISOString().replace(/\.\d{3}Z$/, 'Z'), cursor: cursor || undefined,
  }});
  if (!object(raw) || !Array.isArray(raw.items) || raw.items.length > 200 || raw.nextCursor !== undefined && !text(raw.nextCursor)) throw new ServerPlayHistoryError();
  return Object.freeze({entries: Object.freeze(raw.items.map(playFromServer)), nextCursor: (raw.nextCursor as string | undefined) ?? ''});
}

/** The first page for these filters in the viewer's cache; later pages are read on from it, never cached. */
export function serverPlayHistoryQuery(api: ServerRequester, filters: ServerPlayFilters): ViewerQueryOptions<ServerPlayPage> {
  return {queryKey: queryKeys.adminPlayHistory(filters, ''), queryFn: ({signal}) => readServerPlayHistory(api, filters, '', signal),
    meta: {dependencies: ['playback.history', 'session.updated'].map(type => dependsOn.event(type))}};
}

/** Every account on the server, for the person filter. */
export async function readServerAccounts(api: ServerRequester, signal?: AbortSignal): Promise<readonly ServerAccount[]> {
  const out = new Map<string, ServerAccount>(), seen = new Set<string>();
  let cursor: string | undefined;
  do {
    const raw: unknown = await callServer(api, 'GET /v1/admin/members', {signal, query: {cursor}});
    if (!object(raw) || !Array.isArray(raw.items) || raw.nextCursor !== undefined && !text(raw.nextCursor)) throw new ServerPlayHistoryError();
    for (const m of raw.items) if (object(m) && text(m.id) && m.id && text(m.username)) out.set(m.id, Object.freeze({accountId: m.id, username: m.username}));
    cursor = (raw.nextCursor as string | undefined) || undefined;
    if (cursor) { if (seen.has(cursor)) throw new ServerPlayHistoryError(); seen.add(cursor); }
  } while (cursor);
  return Object.freeze([...out.values()]);
}
