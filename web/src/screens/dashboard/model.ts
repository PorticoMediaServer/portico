import {formatAudioSource, formatDeliveryTarget, formatSubtitleSource, formatVideoSource, formatCodec} from './format';
import type {DashboardSession as AdminSession} from './bindings';
import type {DashboardHistory} from '@core/admin/dashboard.ts';
import type {ReportInterval} from '@core/report-context.ts';
import {DASHBOARD_LOAD_SERIES} from '@core/admin/dashboard.ts';
import type {TelemetryReading, TelemetryPoint} from '@core/telemetry.ts';
import {defaultI18n} from '@i18n';

export const mediaKinds = ['movies', 'tv', 'music', 'audiobooks', 'live'] as const;
export type MediaKind = typeof mediaKinds[number];
/** The drawing's main title is the show, book or current live programme.
 * Channel and episode names remain their separate facts below it. */
export function sessionTitle(session: AdminSession): string {
  if (session.live) return session.programme?.title || '';
  return session.item?.showTitle || session.item?.bookTitle || session.title;
}
export function sessionStopTitle(session: AdminSession): string {
  return sessionTitle(session) || session.channel?.name || session.title;
}
export function orderedSessions(sessions: readonly AdminSession[]): readonly AdminSession[] {
  const converts = (s: AdminSession) => Object.values(s.decision).some(d => d?.action === 'transcode' || d?.action === 'burn');
  return [...sessions].sort((a, b) => Number(!!b.problem) - Number(!!a.problem) || Number(converts(b)) - Number(converts(a)) || Date.parse(b.startedAt ?? '') - Date.parse(a.startedAt ?? '') || a.id.localeCompare(b.id));
}
export type DashboardRoute = 'original' | 'repackaged' | 'converted';
/** VS-10 per-track display classification only. Never rewrites a raw action or establishes an overall session route. */
export function trackRoute(action?: string): DashboardRoute | undefined {
  if (action === 'original' || action === 'direct') return 'original';
  if (action === 'copy') return 'repackaged';
  if (action === 'convert' || action === 'transcode' || action === 'burn_in' || action === 'burn') return 'converted';
  return undefined;
}
export type Track = Readonly<{reportedAction?: string; route?: DashboardRoute; kind: 'video' | 'audio' | 'subtitles'; source: string; action: string; output?: string; hardware: boolean}>;
export function sessionTracks(s: AdminSession): readonly Track[] {
  const tracks: Track[] = [], v = s.media?.video, a = s.media?.audio, sub = s.media?.subtitles;
  for (const kind of ['video', 'audio', 'subtitles'] as const) {
    const source = kind === 'video' && v ? formatVideoSource(v)
      : kind === 'audio' && a ? formatAudioSource(a, s.item?.kind === 'song')
      : kind === 'subtitles' && sub ? formatSubtitleSource(sub) : '';
    const delivery = s.delivery?.streams.find(t => t.kind === kind || kind === 'subtitles' && t.kind === 'subtitle');
    const decision = s.decision[kind], reported = delivery?.action ?? decision?.action;
    const action = reported === 'original' ? 'direct' : reported === 'convert' ? 'transcode' : reported === 'burn_in' ? 'burn' : reported === 'drop' ? 'off' : reported;
    const selectedSource = source || (delivery?.inputCodec ? formatCodec(delivery.inputCodec) : '');
    if (!selectedSource && !trackRoute(reported)) continue;
    tracks.push({reportedAction: reported, route: trackRoute(reported), kind, source: selectedSource, action: action ?? 'unknown', output: formatDeliveryTarget(s, kind, delivery?.outputCodec || decision?.to), hardware: kind === 'video' && s.media?.videoHardware === true && (action === 'transcode' || action === 'burn')});
  }
  return tracks;
}
export function quietestHours(grid: readonly (readonly number[])[]): number | undefined {
  if (grid.length !== 7 || grid.some(day => day.length !== 24) || !grid.some(day => day.some(n => n > 0))) return undefined;
  let best = 0, min = Infinity;
  for (let hour = 0; hour <= 21; hour++) {
    const sum = grid.reduce((total, day) => total + day[hour] + day[hour + 1] + day[hour + 2], 0);
    if (sum < min) {min = sum; best = hour;}
  }
  return best;
}
export type PlotGeometry = Readonly<{left: number; top: number; width: number; height: number}>;
/** Monotone cubic segments copied from checked settings/dashboard.js. This draws between actual samples only. */
export function curveSegments(pts: readonly (readonly [number, number])[]): string {
  const n = pts.length; if (n < 2) return '';
  const dx: number[] = [], m: number[] = [], tg: number[] = [], f1 = (v: number) => v.toFixed(1);
  for (let i = 0; i < n - 1; i++) {dx.push(pts[i + 1][0] - pts[i][0]); m.push((pts[i + 1][1] - pts[i][1]) / dx[i]);}
  tg[0] = m[0]; tg[n - 1] = m[n - 2];
  for (let i = 1; i < n - 1; i++) tg[i] = m[i - 1] * m[i] <= 0 ? 0 : 2 / (1 / m[i - 1] + 1 / m[i]);
  let d = '';
  for (let i = 0; i < n - 1; i++) {const k = dx[i] / 3; d += `C${f1(pts[i][0] + k)},${f1(pts[i][1] + tg[i] * k)} ${f1(pts[i + 1][0] - k)},${f1(pts[i + 1][1] - tg[i + 1] * k)} ${f1(pts[i + 1][0])},${f1(pts[i + 1][1])}`;}
  return d;
}
export function plotLine(pts: readonly (readonly [number, number])[], step = false): string {
  if (!pts.length) return '';
  const f1 = (v: number) => v.toFixed(1);
  let d = `M${f1(pts[0][0])},${f1(pts[0][1])}`;
  if (!step) return d + curveSegments(pts);
  for (let i = 1; i < pts.length; i++) d += `H${f1(pts[i][0])}V${f1(pts[i][1])}`;
  return d;
}
/** No synthesized zeroes or line across an unavailable sample. Geometry matches the drawing. */
export function chartPaths(points: readonly TelemetryPoint[], from: number, until: number, max: number, step = false, geometry: PlotGeometry = {left: 64, top: 8, width: 840, height: 216}, expected?: readonly number[]): readonly string[] {
  if (!points.length || until <= from || max <= 0) return [];
  const intervals = points.slice(1).map((p, i) => p.t - points[i].t).filter(n => n > 0).sort((a, b) => a - b);
  const cadence = intervals[Math.floor(intervals.length / 2)] ?? Infinity;
  const ordinals = expected && new Map(expected.map((stamp, i) => [stamp, i]));
  const paths: string[] = []; let chunk: [number, number][] = [], last: TelemetryPoint | undefined;
  for (const p of points) {
    if (!Number.isFinite(p.v) || p.t < from || p.t > until) continue;
    const x = geometry.left + (p.t - from) / (until - from) * geometry.width, y = geometry.top + geometry.height - Math.max(0, p.v) / max * geometry.height;
    const gap = last && (ordinals ? !ordinals.has(last.t) || !ordinals.has(p.t) || ordinals.get(p.t)! - ordinals.get(last.t)! > 1 : p.t - last.t > cadence * 2.5);
    if (gap && chunk.length) {paths.push(plotLine(chunk, step)); chunk = [];}
    chunk.push([x, y]);
    last = p;
  }
  if (chunk.length) paths.push(plotLine(chunk, step)); return paths;
}
export function bucketSeries(history: DashboardHistory, kind: string): readonly TelemetryPoint[] {
  return history.buckets.map(bucket => ({t: Date.parse(bucket.start), v: bucket.kinds.find(k => k.kind === kind)?.watchedMs ?? 0}));
}
/** A tile never promotes a whole-system value to Portico, or a partial pair to a total. */
export function loadTileValue(reading: TelemetryReading, kind: keyof typeof DASHBOARD_LOAD_SERIES): number | undefined {
  const metrics = DASHBOARD_LOAD_SERIES[kind].metrics;
  const names = kind === 'processor' || kind === 'memory' ? metrics.slice(0, 1) : metrics;
  const points = names.map(name => reading.status[name]?.status === 'unavailable' ? undefined : reading.series[name]?.at(-1));
  if (points.some(point => !point) || points.some(point => point!.t !== points[0]!.t)) return undefined;
  return points.reduce((sum, point) => sum + point!.v, 0);
}
export type AxisTick = Readonly<{f: number; text: string}>;
/** The drawing's label thinning, with no changes to data or bucket identities. */
export function thinTicks(ticks: readonly AxisTick[], width: number): readonly AxisTick[] {
  const tight = (a: readonly AxisTick[]) => a.some((t, i) => i && (t.f - a[i - 1].f) * width < (t.text.length + a[i - 1].text.length) * 3.3 + 18);
  let a = ticks; while (a.length > 2 && tight(a)) a = a.filter((_, i) => i % 2 === 0); return a;
}
/** histTicks from the drawing, formatting the server's actual local bucket starts. No calendar resolver. */
export function historyAxisTicks(history: Pick<DashboardHistory, 'period' | 'timeZone' | 'buckets'>): readonly AxisTick[] {
  const format = (stamp: string, options: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(defaultI18n.locale, {...options, timeZone: history.timeZone}).format(Date.parse(stamp));
  const month = (stamp: string) => format(stamp, {month: 'numeric', year: 'numeric'});
  const ticks: AxisTick[] = [];
  history.buckets.forEach((bucket, i) => {
    const f = history.buckets.length === 1 ? .5 : i / (history.buckets.length - 1);
    if (history.period === '7d') ticks.push({f, text: defaultI18n.t('next.dashboard.format.dayDate', {day: format(bucket.start, {weekday: 'short'}), date: format(bucket.start, {day: 'numeric'})})});
    else if (history.period === '30d' || history.period === '90d') {
      if (new Intl.DateTimeFormat('en-US', {weekday: 'short', timeZone: history.timeZone}).format(Date.parse(bucket.start)) === 'Mon') ticks.push({f, text: format(bucket.start, {month: 'short', day: 'numeric'})});
    } else if (i === 0 || month(history.buckets[i - 1].start) !== month(bucket.start)) ticks.push({f, text: format(bucket.start, {month: 'short'})});
  });
  return thinTicks(ticks, 854);
}
/** loadTicks presentation on genuine returned bucket starts. The server alone resolves calendars. */
export function loadAxisTicks(interval: ReportInterval, from: number, until: number, width: number): readonly AxisTick[] {
  if (interval.period === 'live') return ['2m', '1m 40s', '1m 20s', '1m', '40s', '20s', 'Now'].map((text, k) => ({f: k / 6, text: defaultI18n.t(`next.dashboard.axis.${text}` as 'next.dashboard.axis.Now')}));
  const format = (stamp: string, options: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(defaultI18n.locale, {...options, timeZone: interval.timeZone}).format(Date.parse(stamp));
  const number = (stamp: string, unit: 'hour' | 'month') => Number(new Intl.DateTimeFormat('en-US', {timeZone: interval.timeZone, [unit]: 'numeric', hourCycle: 'h23'}).format(Date.parse(stamp)));
  const month = (stamp: string) => format(stamp, {month: 'numeric', year: 'numeric'});
  let ticks: AxisTick[] = [];
  interval.buckets.forEach((bucket, i) => {
    const f = until === from ? .5 : (Date.parse(bucket.start) - from) / (until - from);
    if (f < 0 || f > 1) return;
    if (interval.period === '12h' || interval.period === '24h') {
      if (number(bucket.start, 'hour') % (interval.period === '12h' ? 2 : 4) === 0) ticks.push({f, text: format(bucket.start, {hour: 'numeric'})});
    } else if (interval.period === '7d') ticks.push({f, text: format(bucket.start, {weekday: 'short'})});
    else if (interval.period === '30d' || interval.period === '90d') {
      if (new Intl.DateTimeFormat('en-US', {weekday: 'short', timeZone: interval.timeZone}).format(Date.parse(bucket.start)) === 'Mon') ticks.push({f, text: format(bucket.start, {month: 'short', day: 'numeric'})});
    } else if (interval.period === '1y' && (i === 0 || month(interval.buckets[i - 1].start) !== month(bucket.start))) ticks.push({f, text: format(bucket.start, {month: 'short'})});
    else if (interval.period === 'all' && (number(bucket.start, 'month') - 1) % 2 === 0) ticks.push({f, text: format(bucket.start, {month: 'short', year: 'numeric'})});
  });
  if (interval.period === '90d') ticks = ticks.filter((_, i) => (ticks.length - 1 - i) % 2 === 0);
  return thinTicks(ticks, width);
}
export const hours = (milliseconds: number) => milliseconds / 3_600_000;
export function clock(milliseconds: number): string {
  const s = Math.floor(Math.max(0, milliseconds) / 1000); return `${Math.floor(s / 3600) ? `${Math.floor(s / 3600)}:` : ''}${Math.floor(s / 3600) ? String(Math.floor(s / 60) % 60).padStart(2, '0') : Math.floor(s / 60) % 60}:${String(s % 60).padStart(2, '0')}`;
}
