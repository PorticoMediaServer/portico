import {parseReportInterval,verifyReportInterval,reportQuery,type ReportContext,type ReportInterval} from './report-context.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {wireDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach, keepReadable, noteSkipped} from './tolerant.ts';
import type {I18n, MessageId} from '../../i18n/src/index.ts';

/**
 * The server's play history (`GET /v1/admin/play-history`): every play on the server, newest
 * first, for its owner. The model and every word of the page are here; the web draws a table and
 * the iPhone app a list from the same columns and filters.
 *
 * It is not a viewer's own History (`personal-saved`), which they may pause and clear.
 */
export type PlayHistoryEntry = Readonly<{
  watchedMs?:number;location?:string;
  id: string;
  user: string;
  /** Named only for a profile other than the account's first. */
  profile?: string;
  accountId: string;
  kind: string;
  title: string;
  /** The show of an episode, the artist of a song, the book of an audiobook file. */
  parentTitle?: string;
  season?: number;
  episode?: number;
  /** Present while the title is still in the catalogue. */
  itemId?: string;
  libraryId: string;
  device?: string;
  platform?: string;
  startedAt: string;
  positionSeconds: number;
  durationSeconds: number;
  completed: boolean;
}>;
export type PlayHistoryPage = ReportInterval & Readonly<{entries: readonly PlayHistoryEntry[]; /** Plays matching the filters; first page only. */ total?: number; nextCursor: string}>;

export const PLAY_HISTORY_PERIODS = ['all', 'today', '24h', '7d', '30d', '90d', '1y'] as const;
export type PlayHistoryPeriod = (typeof PLAY_HISTORY_PERIODS)[number];
export type ReportPeriod=PlayHistoryPeriod|'12h';
export type PlayHistoryFilters = ReportContext & Readonly<{accountId?: string; libraryId?: string;media?:string;search?:string;sort?:string;direction?:'asc'|'desc'; period: ReportPeriod}>;
export const PLAY_HISTORY_PAGE = 100;

export interface PlayHistoryApi {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}

const object = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const text = (v: unknown, max = 1024): v is string => typeof v === 'string' && v.length <= max;
const whole = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const amount = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0;

function invalid(): never {
  throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_play_history', retryable: false});
}

export function parsePlayHistory(value: unknown): PlayHistoryPage {
  if (!object(value) || !Array.isArray(value.entries) || value.entries.length > 200 || !text(value.nextCursor, 256)) invalid();
  const total = whole(value.total) ? value.total : undefined;
  if (value.total !== undefined && total === undefined) noteSkipped('play-history', 'total');
  // A play this app cannot read is left out alone; an optional detail it cannot read is left off its play.
  const entries = firstOfEach('play-history', 'entries', keepReadable('play-history', 'entries', value.entries, raw => {
    raw=wireDurations(raw,{positionSeconds:'positionMs',durationSeconds:'durationMs'});
    if (!object(raw) || !text(raw.id, 64) || !raw.id || !text(raw.user) || !text(raw.accountId, 256) || !text(raw.kind, 64) || !text(raw.title) || !text(raw.libraryId, 256)) invalid();
    if (!text(raw.startedAt, 64) || !Number.isFinite(Date.parse(raw.startedAt)) || !amount(raw.positionSeconds) || !amount(raw.durationSeconds) || typeof raw.completed !== 'boolean') invalid();
    const entry: {-readonly [K in keyof PlayHistoryEntry]: PlayHistoryEntry[K]} = {
      ...(whole(raw.watchedMs)?{watchedMs:raw.watchedMs}:{}),...(text(raw.location,32)?{location:raw.location}:{}),
 id: raw.id, user: raw.user, accountId: raw.accountId, kind: raw.kind, title: raw.title, libraryId: raw.libraryId,
      startedAt: raw.startedAt, positionSeconds: raw.positionSeconds, durationSeconds: raw.durationSeconds, completed: raw.completed,
    };
    for (const field of ['profile', 'parentTitle', 'itemId', 'device', 'platform'] as const) {
      const v = raw[field];
      if (v === undefined || v === '') continue;
      if (!text(v)) { noteSkipped('play-history', 'entry.' + field); continue; }
      entry[field] = v;
    }
    for (const field of ['season', 'episode'] as const) {
      const v = raw[field];
      if (v === undefined) continue;
      if (!whole(v)) { noteSkipped('play-history', 'entry.' + field); continue; }
      entry[field] = v;
    }
    return Object.freeze(entry);
  }), entry => entry.id);
  return Object.freeze({...parseReportInterval(value),entries: Object.freeze(entries), ...(total === undefined ? {} : {total}), nextCursor: value.nextCursor});
}

export async function fetchPlayHistory(api: PlayHistoryApi, filters: PlayHistoryFilters, cursor = '', signal?: AbortSignal): Promise<PlayHistoryPage> {
  const query = reportQuery(filters);query.set('limit',String(PLAY_HISTORY_PAGE));
  if(filters.media)query.set('media',filters.media);if(filters.search)query.set('q',filters.search);if(filters.sort)query.set('sort',filters.sort);if(filters.direction)query.set('direction',filters.direction);
  if (filters.accountId) query.set('accountId', filters.accountId);
  if (filters.libraryId) query.set('libraryId', filters.libraryId);
  query.set('period', filters.period);
  if (cursor) query.set('cursor', cursor);
  const out=parsePlayHistory(await api.request<RouteResponse<"get_admin_play_history">>(routePath("get_admin_play_history") + "?" + (query.toString()), 'GET', undefined, signal));verifyReportInterval(out,filters,filters.period);return out;
}

// ── Viewing statistics ──────────────────────────────────────────────────────────────────────

/**
 * The Dashboard's viewing statistics (`GET /v1/admin/play-history/summary`): totals of the plays
 * in a period, read from the play history itself. They agree with the Play history page, and they
 * are kept for as long as the history is: forever, unless the owner sets a number of days.
 */
export type PlayCount = Readonly<{name: string; plays: number}>;
export type PlaySummary = ReportInterval & Readonly<{plays: number; people: number; watchedMs: number; converted: number; mostPlayed: readonly PlayCount[]; mostActive: readonly PlayCount[]}>;

/** The periods the Dashboard offers: today, the week, the month, and everything the server has kept. */
export const VIEWING_PERIODS = ['today', '7d', '30d', 'all'] as const;
export type ViewingPeriod = (typeof VIEWING_PERIODS)[number];
export const viewingPeriodLabel = (period: ViewingPeriod): MessageId => (period === 'today' ? 'server.viewing.today' : period === 'all' ? 'server.plays.allTime' : `server.viewing.period.${period}`);

export function parsePlaySummary(value: unknown): PlaySummary {

  if (!object(value) || !whole(value.plays) || !whole(value.people) || !whole(value.watchedMs) || !whole(value.converted)) invalid();
  const counts = (raw: unknown): readonly PlayCount[] => {
    if (!Array.isArray(raw) || raw.length > 50) invalid();
    return Object.freeze(keepReadable('play-summary', 'counts', raw, row => {
      if (!object(row) || !text(row.name) || !whole(row.plays)) invalid();
      return Object.freeze({name: row.name, plays: row.plays});
    }));
  };
  return Object.freeze({...parseReportInterval(value),plays: value.plays, people: value.people, watchedMs: value.watchedMs, converted: value.converted, mostPlayed: counts(value.mostPlayed), mostActive: counts(value.mostActive)});
}

export async function fetchPlaySummary(api:PlayHistoryApi,filters:PlayHistoryFilters,signal?:AbortSignal):Promise<PlaySummary>{
 const q=reportQuery(filters);q.set('period',filters.period);
 for(const key of ['accountId','libraryId','media'] as const)if(filters[key])q.set(key,filters[key]!);
 if(filters.search)q.set('q',filters.search);
 const out=parsePlaySummary(await api.request<RouteResponse<'get_admin_play_history_summary'>>(routePath('get_admin_play_history_summary')+'?'+q,'GET',undefined,signal));verifyReportInterval(out,filters,filters.period);return out;
}

/** The four figures as the panel prints them: plays, people, hours to one decimal, and the share the server converted. */
export function viewingFigures(summary: PlaySummary): Readonly<{plays: string; people: string; hours: string; converted: string}> {
  return {
    plays: String(summary.plays), people: String(summary.people), hours: (summary.watchedMs / 3_600_000).toFixed(1),
    converted: summary.plays ? `${Math.round((summary.converted / summary.plays) * 100)}%` : '—',
  };
}

// ── What the page says ──────────────────────────────────────────────────────────────────────

/** The table's columns, in order. The iPhone list uses the same labels for the same facts. */
export const PLAY_HISTORY_COLUMNS: readonly Readonly<{id: 'user' | 'type' | 'title' | 'player' | 'platform' | 'played'; label: MessageId}>[] = [
  {id: 'user', label: 'server.plays.user'},
  {id: 'type', label: 'server.plays.type'},
  {id: 'title', label: 'library.column.title'},
  {id: 'player', label: 'server.plays.player'},
  {id: 'platform', label: 'server.plays.platform'},
  {id: 'played', label: 'server.plays.played'},
];

export const playHistoryPeriodLabel = (period: PlayHistoryPeriod): MessageId => (period === 'all' ? 'server.plays.allTime' : period==='today'?'server.viewing.today':`server.plays.period.${period}`);

const KIND_LABELS: Readonly<Record<string, MessageId>> = {
  movie: 'server.plays.kind.movie', episode: 'server.plays.kind.episode', song: 'server.plays.kind.song',
  audiobook_file: 'server.plays.kind.audiobook', extra: 'server.plays.kind.extra',
};
const PLATFORM_LABELS: Readonly<Record<string, MessageId>> = {
  'google-cast': 'device.platform.googleCast',
  web: 'device.platform.web', ios: 'device.platform.ios', tvos: 'device.platform.tvos', android: 'device.platform.android', androidtv: 'device.platform.androidtv',
};

/** "justin", or "justin · Kids" for a profile other than the account's first. */
export const playHistoryUser = (entry: Pick<PlayHistoryEntry, 'user' | 'profile'>): string => (entry.profile ? `${entry.user} · ${entry.profile}` : entry.user);

export const playHistoryType = (entry: Pick<PlayHistoryEntry, 'kind'>, i18n: Pick<I18n, 't'>): string => (KIND_LABELS[entry.kind] ? i18n.t(KIND_LABELS[entry.kind]!) : entry.kind);

/** "Family Guy · S7 E13 · Stew-Roids", "Pink Floyd · Time", or the title alone. */
export function playHistoryTitle(entry: Pick<PlayHistoryEntry, 'title' | 'parentTitle' | 'season' | 'episode'>, i18n: Pick<I18n, 't'>): string {
  const code = entry.episode === undefined ? undefined
    : entry.season === undefined ? i18n.t('title.episodeOnly', {episode: entry.episode})
    : i18n.t('title.episodeCode', {season: entry.season, episode: entry.episode});
  // A one-file audiobook is named like its book: the name is said once.
  return [entry.parentTitle === entry.title ? undefined : entry.parentTitle, code, entry.title].filter(Boolean).join(' · ');
}

export const playHistoryPlatform = (entry: Pick<PlayHistoryEntry, 'platform'>, i18n: Pick<I18n, 't'>): string => {
  const id = PLATFORM_LABELS[(entry.platform ?? '').toLowerCase()];
  return id ? i18n.t(id) : entry.platform ?? '';
};

/** When it was played, and how far a play that was not finished got: "Oct 2, 2026", "9:08 AM", "Stopped at 42%". */
export function playHistoryPlayed(entry: Pick<PlayHistoryEntry, 'startedAt' | 'completed' | 'positionSeconds' | 'durationSeconds'>, i18n: Pick<I18n, 't' | 'time' | 'date'>): Readonly<{date: string; time: string; note?: string}> {
  const at = Date.parse(entry.startedAt);
  const unfinished = !entry.completed && entry.durationSeconds > 0;
  return {
    date: i18n.date(at, 'medium'), time: i18n.time(at),
    ...(unfinished ? {note: i18n.t('server.plays.stoppedAt', {percent: Math.min(99, Math.max(1, Math.round((entry.positionSeconds / entry.durationSeconds) * 100)))})} : {}),
  };
}
