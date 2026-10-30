// CONTROLLED VISUAL FIXTURE ONLY. No product imports this module.
// Its calendar/sample construction is synthetic test input, never a UI report resolver or producer proof.
import {captureReportContext, type ReportInterval} from '../../../packages/client-core/src/report-context';
import {METRIC_NAMES, type TelemetryReading, type TelemetryWindow} from '../../../packages/client-core/src/telemetry';
import type {DashboardTopKind} from '../../../packages/client-core/src/admin/dashboard';
import type {DashboardFilters, DashboardReport, DashboardReportQuery} from '../../src/screens/dashboard/bindings';

export const fixtureContext = captureReportContext(Intl.DateTimeFormat().resolvedOptions().timeZone, new Date(2026, 9, 22, 16, 15));
const end = Date.parse(fixtureContext.until), day = new Date(end);
const localDay = (offset: number) => new Date(day.getFullYear(), day.getMonth(), day.getDate() + offset).getTime();
const iso = (ms: number) => new Date(ms).toISOString();
const kinds = ['movies', 'tv', 'music', 'audiobooks', 'live'] as const;

export function fixtureBounds(period: string): ReportInterval {
  const buckets: {start: string; end: string; partial: boolean}[] = [];
  if (period === 'all') {
    for (let month = new Date(2025, 7, 1); month.getTime() < end; month = new Date(month.getFullYear(), month.getMonth() + 1, 1)) {
      const next = new Date(month.getFullYear(), month.getMonth() + 1, 1).getTime();
      buckets.push({start: month.toISOString(), end: iso(Math.min(end, next)), partial: next > end});
    }
  } else if (period === 'live' || period === '12h' || period === '24h') {
    const count = period === 'live' ? 1 : period === '12h' ? 12 : 24;
    const hour = end - (day.getMinutes() * 60 + day.getSeconds()) * 1000 - day.getMilliseconds();
    const start = period === 'live' ? end - 120000 : hour - (count - 1) * 3600000;
    for (let i = 0; i < count; i++) buckets.push({start: iso(start + i * 3600000), end: iso(Math.min(end, start + (i + 1) * 3600000)), partial: i === count - 1});
  } else {
    const count = period === 'today' ? 1 : period === '7d' ? 7 : period === '30d' ? 30 : period === '90d' ? 90 : 52;
    const width = period === '1y' ? 7 : 1, first = -(count * width - 1);
    for (let i = 0; i < count; i++) buckets.push({start: iso(localDay(first + i * width)), end: iso(Math.min(end, localDay(first + (i + 1) * width))), partial: i === count - 1});
  }
  return {period, ...fixtureContext, from: buckets[0]?.start ?? fixtureContext.until, buckets};
}
export function fixtureHistory(period: string, topKind: DashboardTopKind = 'movies', media?: string) {
  const bounds = fixtureBounds(period), buckets = bounds.buckets.map((b, i) => ({...b, kinds: kinds.filter(k => !media || k === media).map(kind => ({kind, plays: 10, watchedMs: (i + 1) * (kinds.indexOf(kind) + 1) * 360000}))}));
  const watched = buckets.reduce((sum, b) => sum + b.kinds.reduce((s, k) => s + k.watchedMs, 0), 0);
  const weights = Array.from({length: 168}, (_, i) => i % 24 >= 18 && i % 24 <= 22 ? 10 : i % 24 >= 3 && i % 24 < 6 ? 0 : 1), weight = weights.reduce((a, b) => a + b, 0);
  const heat = weights.map(v => Math.floor(v / weight * watched)); heat[0] += watched - heat.reduce((a, b) => a + b, 0);
  const plays = buckets.reduce((sum, b) => sum + (b.kinds.find(k => k.kind === topKind)?.plays ?? 0), 0);
  return {...bounds, buckets, busiestTimes: Array.from({length: 7}, (_, d) => heat.slice(d * 24, d * 24 + 24)), quietestStartHour: watched ? 3 : null, topKind,
    top: Array.from({length: 6}, (_, i) => {const n = Math.floor(plays * (6 - i) / 21); return {itemId: `fixture-top-${i}`, title: `Fixture ${topKind} ${i + 1}`, plays: n, people: Math.min(8, n), countUnit: topKind === 'music' ? 'track_plays' : topKind === 'tv' ? 'episode_plays' : 'movie_plays'};}).filter(t => t.plays > 0)};
}
export function fixturePeople(period: string, cursor = '') {
  const history = fixtureHistory(period), weights = Array.from({length: 137}, (_, i) => 150 - i), sum = weights.reduce((a, b) => a + b, 0);
  let previous = 0;
  const items = weights.map((w, i) => {
    const prefix = previous; previous += w;
    const split = (total: number) => Math.floor(total * previous / sum) - Math.floor(total * prefix / sum);
    const parts = kinds.map(kind => ({kind, plays: split(history.buckets.reduce((s, b) => s + (b.kinds.find(k => k.kind === kind)?.plays ?? 0), 0)), watchedMs: split(history.buckets.reduce((s, b) => s + (b.kinds.find(k => k.kind === kind)?.watchedMs ?? 0), 0))}));
    return {accountId: `fixture-account-${i}`, username: `fixture_${i}`, profiles: i === 1 ? ['Fixture child'] : [], plays: parts.reduce((s, k) => s + k.plays, 0), watchedMs: parts.reduce((s, k) => s + k.watchedMs, 0), kinds: parts};
  }).sort((a, b) => b.watchedMs - a.watchedMs || a.accountId.localeCompare(b.accountId));
  const offset = Number(cursor || 0);
  return {...fixtureBounds(period), items: items.slice(offset, offset + 100), nextCursor: offset + 100 < items.length ? String(offset + 100) : ''};
}
export function fixtureReading(window: TelemetryWindow): TelemetryReading {
  const bounds = fixtureBounds(window), points = window === 'live' ? Array.from({length: 61}, (_, i) => end - 120000 + i * 2000) : bounds.buckets.map(b => Date.parse(b.start));
  return {...bounds, window, observedAt: end, series: Object.fromEntries(METRIC_NAMES.map((metric, index) => [metric, points.map((t, i) => ({t, v: index + (i < points.length / 3 ? 4 : i < points.length * 2 / 3 ? 8 : 6)}))])) as TelemetryReading['series'], status: Object.fromEntries(METRIC_NAMES.map(metric => [metric, {status: 'available', value: 8}])) as TelemetryReading['status']};
}
export function fixturePlays(filters: DashboardFilters, cursor = '') {
  const offset = Number(cursor || 0);
  return {...fixtureBounds(filters.period), entries: Array.from({length: 100}, (_, i) => ({id: `fixture-play-${offset + i}`, user: `fixture_${i % 12}`, accountId: `fixture-account-${i % 12}`, title: `Fixture title ${offset + i}`, libraryId: 'fixture-library', kind: 'movie', startedAt: iso(end - 3600000), positionSeconds: 1800, durationSeconds: 7200, completed: i % 3 === 0, watchedMs: 1800000, location: i % 2 ? 'local' : 'remote', device: 'Fixture TV', platform: 'web'})), total: 10000, nextCursor: offset < 9900 ? String(offset + 100) : ''};
}
export function fixtureReport(query: DashboardReportQuery): DashboardReport {
  const history = fixtureHistory(query.period, query.topKind, query.media), top = fixtureHistory(query.topPeriod, query.topKind);
  const plays = history.buckets.reduce((s, b) => s + b.kinds.reduce((n, k) => n + k.plays, 0), 0), watchedMs = history.buckets.reduce((s, b) => s + b.kinds.reduce((n, k) => n + k.watchedMs, 0), 0);
  return {context: fixtureContext, history, people: fixturePeople(query.peoplePeriod, query.peopleCursor), topPlayed: {...fixtureBounds(query.topPeriod), topKind: query.topKind, items: top.top}, summary: {...fixtureBounds(query.period), plays, people: Math.min(137, plays), watchedMs, converted: 0, mostPlayed: [], mostActive: []}, table: {...fixturePlays(query, query.tableCursor), total: plays}, load: fixtureReading(query.window)};
}
