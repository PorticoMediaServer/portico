import {callServer, type ServerRequester} from '../query/server-api.ts';
import type {ReportInterval} from '../report-context.ts';
import {periodStart} from './server-play-history.ts';

/**
 * The owner's viewing statistics on the rebuilt server (12-console rules 13 and 84 to 87).
 * `GET /v1/admin/statistics` gives the server's totals per day (plays, time played, data sent, split by kind
 * and delivery) over a range, by day, week or month: Plays over time and the range's totals.
 * `GET /v1/admin/play-history/summary` counts Top people, Top played and Busiest times from Play history.
 */
export const STATISTICS_RANGES = ['7d', '30d', '90d', '1y', 'all'] as const;
export type StatisticsRange = (typeof STATISTICS_RANGES)[number];
/** The server's kinds of media in its totals ("plays.film.converted"). */
export const STATISTICS_KINDS = ['film', 'episode', 'music', 'audiobook', 'other'] as const;
export type StatisticsKind = (typeof STATISTICS_KINDS)[number];
export type StatisticsTotals = Readonly<{plays: number; playedMs: number; bytes: number; converted: number}>;
export type StatisticsPoint = Readonly<{start: string; end: string; playedMs: Readonly<Record<StatisticsKind, number>>}>;
export type ServerStatistics = ReportInterval & Readonly<{points: readonly StatisticsPoint[]; totals: StatisticsTotals}>;
export type PlayTotal = Readonly<{id?: string; name: string; detail?: string; plays: number; watchedMs: number}>;
export type PlaySummary = Readonly<{people: readonly PlayTotal[]; titles: readonly PlayTotal[]; /** Plays by weekday from Monday, and hour. */ busiest: readonly (readonly number[])[]; window: number; countedFrom?: string}>;

function invalid(): never { throw Object.assign(new Error('invalid_statistics'), {code: 'invalid_statistics'}); }
const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const whole = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const date = (v: unknown): v is string => typeof v === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(v);
const localDate = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
/** A day, week or month starting on a server date, as this device's midnight (the server and the page share a zone). */
const midnight = (day: string) => {const [y, m, d] = day.split('-').map(Number); return new Date(y!, m! - 1, d!);};
const by = (range: StatisticsRange) => range === 'all' ? 'month' : range === '1y' ? 'week' : 'day';

/** Sums a point's or the range's values: plays, time played and data sent, and the converted plays. */
function totals(values: Readonly<Record<string, unknown>>): StatisticsTotals & {playedByKind: Record<StatisticsKind, number>} {
  const out = {plays: 0, playedMs: 0, bytes: 0, converted: 0, playedByKind: Object.fromEntries(STATISTICS_KINDS.map(k => [k, 0])) as Record<StatisticsKind, number>};
  for (const [metric, value] of Object.entries(values)) {
    if (!whole(value)) invalid();
    const [name, kind, delivery] = metric.split('.');
    if (name === 'plays') {out.plays += value; if (delivery === 'converted') out.converted += value;}
    else if (name === 'played_ms') {out.playedMs += value; out.playedByKind[(STATISTICS_KINDS as readonly string[]).includes(kind!) ? kind as StatisticsKind : 'other'] += value;}
    else if (name === 'bytes_home' || name === 'bytes_away') out.bytes += value;
  }
  return out;
}

export async function readStatistics(api: ServerRequester, range: StatisticsRange, signal?: AbortSignal, now = new Date()): Promise<ServerStatistics> {
  const from = range === 'all' ? 'all' : localDate(new Date(now.getFullYear(), now.getMonth(), now.getDate() - ({'7d': 6, '30d': 29, '90d': 89, '1y': 364} as const)[range]));
  const raw: unknown = await callServer(api, 'GET /v1/admin/statistics', {query: {from, to: localDate(now), by: by(range)}, signal});
  if (!object(raw) || !date(raw.from) || !date(raw.to) || !Array.isArray(raw.points) || !object(raw.totals)) invalid();
  const sent = new Map(raw.points.map(p => object(p) && date(p.start) && object(p.values) ? [p.start, totals(p.values).playedByKind] as const : invalid()));
  // The server sends only the days, weeks (from Monday) or months that have totals; the others played nothing.
  const unit = by(range), step = (start: Date) => unit === 'month' ? new Date(start.getFullYear(), start.getMonth() + 1, 1) : new Date(start.getFullYear(), start.getMonth(), start.getDate() + (unit === 'week' ? 7 : 1));
  const first = midnight(raw.from), last = midnight(raw.to), none = Object.fromEntries(STATISTICS_KINDS.map(k => [k, 0])) as Record<StatisticsKind, number>;
  const points: StatisticsPoint[] = [];
  for (let at = unit === 'month' ? new Date(first.getFullYear(), first.getMonth(), 1) : unit === 'week' ? new Date(first.getFullYear(), first.getMonth(), first.getDate() - (first.getDay() + 6) % 7) : first; at <= last; at = step(at))
    points.push({start: at.toISOString(), end: step(at).toISOString(), playedMs: sent.get(localDate(at)) ?? none});
  const {playedByKind: _kinds, ...sum} = totals(raw.totals);
  const zone = (() => {try {return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';} catch {return 'UTC';}})();
  return {period: range, timeZone: zone, from: (points[0] ? new Date(points[0].start) : first).toISOString(), until: step(last).toISOString(),
    buckets: points.map(p => ({start: p.start, end: p.end, partial: false})), points, totals: sum};
}

/** The page's kinds of media for Top played, as the server's kinds of item. */
export const SUMMARY_KINDS = {movies: 'movie', tv: 'episode', music: 'track'} as const;
export async function readPlaySummary(api: ServerRequester, filters: Readonly<{range: StatisticsRange; kind?: keyof typeof SUMMARY_KINDS; person?: string}>, signal?: AbortSignal, now = new Date()): Promise<PlaySummary> {
  const from = (periodStart(filters.range, now) ?? new Date(0)).toISOString();
  const raw: unknown = await callServer(api, 'GET /v1/admin/play-history/summary', {query: {from, kind: filters.kind && SUMMARY_KINDS[filters.kind], person: filters.person}, signal});
  const list = (v: unknown) => Array.isArray(v) ? v.map(t => object(t) && typeof t.name === 'string' && whole(t.plays) && whole(t.watchedMs)
    ? {name: t.name, plays: t.plays, watchedMs: t.watchedMs, ...(typeof t.id === 'string' && t.id ? {id: t.id} : {}), ...(typeof t.detail === 'string' && t.detail ? {detail: t.detail} : {})} : invalid()) : invalid();
  if (!object(raw) || !whole(raw.window) || !Array.isArray(raw.busiest) || raw.busiest.length !== 7 || raw.busiest.some(day => !Array.isArray(day) || day.length !== 24 || !day.every(whole))) invalid();
  // The server counts Sunday first; the page's grid, like its quietest window, starts on Monday.
  const busiest = raw.busiest as number[][];
  return {people: list(raw.people), titles: list(raw.titles), busiest: [...busiest.slice(1), busiest[0]!], window: raw.window,
    ...(typeof raw.countedFrom === 'string' && Number.isFinite(Date.parse(raw.countedFrom)) ? {countedFrom: raw.countedFrom} : {})};
}
