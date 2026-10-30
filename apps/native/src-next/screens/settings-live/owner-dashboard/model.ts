import type {Alert, Job, Measurement} from '../../../../../../packages/client-core/src/console';
import type {PlayCount, PlaySummary} from '../../../../../../packages/client-core/src/play-history';
import type {AdminSession} from '../../../../../../packages/client-core/src/playback-v1/types';
import type {TelemetryPoint, TelemetryReading, TelemetryWindow} from '../../../../../../packages/client-core/src/telemetry';
import type {FactPart, TextEnvironment} from '../../../../../../packages/client-core/src/presentation/kit-model';
import type {MessageId, MessageValues} from '../../../../../../packages/i18n/src';
import {formatBytes} from '../../../../../../packages/client-core/src/presentation/content';
import {alertName, jobName, jobStateName, severityName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import {duration, jobTone, when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {healthFacts, loadValue, measuredLoadTiles, type HealthFacts, type LoadTile} from '../../../../../../packages/client-core/src/server-admin/dashboard';
import {playedFraction, positionClock, rateMbps, windowRange} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {STATE_PERMISSIONS_CODE} from '../../../../../../packages/client-core/src/administration';
import type {StatTile} from '../../../kit/shell';
import {nativeDashboardIdentity} from '../dashboard-presentation';

export type Translate = (id: MessageId, values?: MessageValues) => string;
export type Has = (id: string) => id is MessageId;
/** The full catalogue translator plus the formatting locale; the page uses it for every word. */
export type OwnerText = Readonly<{t: Translate; locale?: string}>;

/** The three periods the Health section draws (owner-dashboard frames 2–3, 7–8). */
export const OWNER_HEALTH_WINDOWS = ['10m', '1h', '24h'] as const;
export type OwnerHealthWindow = (typeof OWNER_HEALTH_WINDOWS)[number];
/** The four periods the Viewing statistics section draws (frame 3). */
export const OWNER_VIEWING_PERIODS = ['today', '7d', '30d', 'all'] as const;
export type OwnerViewingPeriod = (typeof OWNER_VIEWING_PERIODS)[number];

export const ownerHealthWindowLabel = (window: OwnerHealthWindow): MessageId => `next.dashboard.range.${window}` as MessageId;
export const ownerViewingPeriodLabel = (period: OwnerViewingPeriod): MessageId => `next.dashboard.range.${period}` as MessageId;

export type OwnerSessionTotals = Readonly<{streams: number; paused: number; converting: number; bandwidthKbps: number; people: number; atHome: number; away: number}>;

/** One already-authorized stream, as the drawn card and compact row need it. */
export type OwnerStreamView = Readonly<{
  id: string;
  title: string;
  identity: readonly FactPart[];
  viewer: Readonly<{name: string; username?: string; device: string}>;
  facts: readonly FactPart[];
  state?: FactPart;
  position?: FactPart;
  progress?: number;
  paused: boolean;
  menuLabel: string;
  /** The notable row note, or nothing: Converted, Buffering, Paused (owner-dashboard frames 4d–4f). */
  note?: OwnerStreamNote;
  /** What the compact row says beside the title: the episode, year, artist, or the channel name. */
  qualifier?: string;
}>;

/** Only a notable stream carries a compact row note; the tone draws its amber route dot, amber problem or grey pause. */
export type OwnerStreamNote = Readonly<{id: 'converted' | 'buffering' | 'paused'; text: string; tone: 'route' | 'problem' | 'paused'}>;

/** At ten streams or fewer the page keeps one full card per stream (frames 2 and 4). */
export const OWNER_STREAM_CARD_LIMIT = 10;
/** The compact row's fixed pitch, so the windowed list mounts only the rows near the viewport. */
export const OWNER_COMPACT_ROW_PX = 64;

/** The routes that mean this stream is being converted now. */
export function ownerConverting(session: AdminSession): boolean {
  return [session.decision?.video?.action, session.decision?.audio?.action, session.decision?.subtitles?.action].some(action => action === 'transcode' || action === 'burn');
}

/** A problem first, then a conversion, then paused: the only notes the frames draw on a row. */
export function ownerStreamNote(session: AdminSession, t: Translate): OwnerStreamNote | undefined {
  if (session.problem) return Object.freeze({id: 'buffering', text: t('next.dashboard.buffering'), tone: 'problem'});
  if (ownerConverting(session)) return Object.freeze({id: 'converted', text: t('next.dashboard.converted'), tone: 'route'});
  if (session.state === 'paused') return Object.freeze({id: 'paused', text: t('next.dashboard.paused'), tone: 'paused'});
  return undefined;
}

const part = (id: string, text: string, role: FactPart['role'] = 'context'): FactPart => Object.freeze({id, text, role});

/** Ruling 4: a stream's route is Original, Repackaged or Converted, never Direct play. */
export function ownerRouteFact(session: AdminSession, t: Translate): FactPart | undefined {
  const actions = [session.decision?.video?.action, session.decision?.audio?.action, session.decision?.subtitles?.action];
  if (ownerConverting(session)) {
    const height = session.quality?.maxHeight;
    return part('route', height ? t('next.dashboard.convert', {target: `${height}p`}) : t('next.dashboard.converted'));
  }
  if (actions.includes('copy')) return part('route', t('next.dashboard.copy'));
  if (actions.includes('direct')) return part('route', t('next.dashboard.original'));
  return undefined;
}

/** Only the supplied session's facts; no title, device or route is inferred. */
export function ownerStreamView(session: AdminSession, env: OwnerText): OwnerStreamView {
  const {t} = env, identity = nativeDashboardIdentity(session, {t: env.t as unknown as TextEnvironment['t'], ...(env.locale ? {locale: env.locale} : {})});
  const rate = rateMbps(session);
  const facts = [
    session.location === 'local' ? part('location', t('next.kit.session.local')) : session.location === 'remote' ? part('location', t('next.kit.session.remote')) : undefined,
    ownerRouteFact(session, t),
    rate ? part('rate', t('web.nowPlaying.rate', {rate}), 'supplement') : undefined,
  ].filter((value): value is FactPart => value !== undefined);
  const state = session.state === 'paused' ? part('state', t('next.kit.session.paused'), 'status') : session.state === 'playing' ? part('state', t('next.kit.session.playing'), 'status') : session.state === 'buffering' || session.state === 'preparing' ? part('state', t('next.kit.session.starting'), 'status') : undefined;
  const position = ownerStreamPosition(session, t), progress = playedFraction(session), note = ownerStreamNote(session, t);
  // A compact row says only the first qualifier the frames draw after the title: the episode
  // numbering, the year, or the artist. The card keeps the full identity below its title.
  const qualifier = identity.secondary[0];
  return Object.freeze({
    id: session.id,
    title: identity.title,
    identity: Object.freeze(identity.secondary.map((text, index) => part(`identity-${index}`, text, 'identity'))),
    viewer: Object.freeze({name: session.profile, ...(session.user ? {username: session.user} : {}), device: session.device}),
    facts: Object.freeze(facts),
    ...(state ? {state} : {}),
    ...(position ? {position} : {}),
    ...(progress === undefined ? {} : {progress}),
    paused: session.state === 'paused',
    menuLabel: t('next.dashboard.sessionMenu', {user: session.user || session.profile}),
    ...(note ? {note} : {}),
    ...(qualifier ? {qualifier} : {}),
  });
}

function ownerStreamPosition(session: AdminSession, t: Translate): FactPart | undefined {
  if ((session.kind === 'vod' || session.kind === 'audio') && session.positionMs > 0) {
    const position = positionClock(session.positionMs);
    return part('position', session.durationMs ? t('web.nowPlaying.positionOf', {position, duration: positionClock(session.durationMs)}) : t('web.nowPlaying.position', {position}), 'supplement');
  }
  const started = session.startedAt ? Date.parse(session.startedAt) : NaN;
  return Number.isFinite(started) ? part('started', t('web.nowPlaying.started', {time: when(started)}), 'supplement') : undefined;
}

export type OwnerJobView = Readonly<{
  id: string;
  title: string;
  subtitle?: string;
  status: string;
  progress?: number;
  tone: 'accent' | 'healthy' | 'danger' | 'warning' | 'neutral';
  icon: 'i-film' | 'i-image' | 'i-skip' | 'i-note' | 'i-refresh' | 'i-check' | 'i-server';
  scan: boolean;
}>;

const jobIcon = (kind: string, state: Job['state']): OwnerJobView['icon'] =>
  state === 'succeeded' ? 'i-check' : kind.includes('scan') ? 'i-film' : kind.includes('trickplay') ? 'i-image' : kind.includes('segment') ? 'i-skip' : kind.includes('metadata') ? 'i-refresh' : kind.includes('backup') ? 'i-check' : 'i-note';

/** A task in words, with its real progress: nothing is drawn while the server publishes no total. */
export function ownerJobView(job: Job, t: Translate, has: Has): OwnerJobView {
  const total = job.total !== undefined && job.total > 0 ? job.total : undefined;
  const done = job.processed;
  const fraction = total && done !== undefined ? Math.min(1, Math.max(0, done / total)) : undefined;
  const status = fraction !== undefined ? `${Math.round(fraction * 100)}%` : jobStateName(t, has, job.state);
  const name = jobName(t, has, job.kind);
  return Object.freeze({
    id: job.id,
    title: job.resource || name,
    subtitle: total !== undefined && done !== undefined ? t('next.shell.filesOf', {processed: done, total}) : name,
    status,
    ...(fraction === undefined ? {} : {progress: fraction}),
    tone: jobTone(job.state),
    icon: jobIcon(job.kind, job.state),
    scan: job.kind.includes('scan'),
  });
}

export type OwnerAlertView = Readonly<{
  id: string;
  title: string;
  body?: string;
  severity: string;
  tone: 'danger' | 'warning' | 'info';
  acknowledged: boolean;
  meta: string;
  permissions: boolean;
}>;

/** Alerts are named and explained only from catalogue copy the server supplied facts for. */
export function ownerAlertView(alert: Alert, t: Translate, has: Has): OwnerAlertView {
  const permissions = alert.code === STATE_PERMISSIONS_CODE;
  const meta = [alert.occurrences > 1 ? t('server.alert.seen', {count: alert.occurrences, when: when(alert.lastAt)}) : t('server.alert.firstSeen', {when: when(alert.firstAt)}), alert.status === 'acknowledged' ? t('server.alert.acknowledged') : ''].filter(Boolean).join(' · ');
  return Object.freeze({
    id: alert.id,
    title: permissions ? t('web.maintenance.permissionsTitle') : alertName(t, has, alert.code),
    ...(permissions ? {body: alert.status === 'resolved' ? t('web.maintenance.permissionsFixed') : t('web.maintenance.permissionsBody')} : {}),
    severity: severityName(t, has, alert.severity),
    tone: alert.severity === 'critical' ? 'danger' : alert.severity === 'warning' ? 'warning' : 'info',
    acknowledged: alert.status === 'acknowledged',
    meta,
    permissions,
  });
}

/** The header's Health sentence, from the two measurements the controller already read. */
export function ownerHealthLine(health: Measurement | undefined, storage: Measurement | undefined, t: Translate): Readonly<{facts: HealthFacts; status: string; line: string; uptimeSeconds?: number}> {
  const facts = healthFacts(health, storage), parts = [
    facts.uptimeSeconds !== undefined ? t('settings.server.health.upFor', {duration: duration(facts.uptimeSeconds)}) : '',
    facts.freeBytes !== undefined ? t('settings.server.health.free', {bytes: formatBytes(facts.freeBytes)}) : '',
    facts.databaseBytes !== undefined ? t('settings.server.health.database', {bytes: formatBytes(facts.databaseBytes)}) : '',
  ].filter(Boolean);
  return Object.freeze({facts, status: t(facts.degraded ? 'settings.server.health.attention' : 'settings.server.health.running'), line: parts.join(' · '), ...(facts.uptimeSeconds === undefined ? {} : {uptimeSeconds: facts.uptimeSeconds})});
}

/** A server that started moments ago keeps the note the frame draws under its tiles. */
export const ownerIsNewServer = (uptimeSeconds: number | undefined): boolean => uptimeSeconds !== undefined && uptimeSeconds < 900;

export type OwnerHealthTile = StatTile & Readonly<{metric: LoadTile['metric']; unit: LoadTile['unit']}>;

/** A metric the server cannot measure is left out; the peak every line uses is its own points. */
export function ownerHealthTiles(reading: TelemetryReading | undefined, t: Translate): readonly OwnerHealthTile[] {
  if (!reading) return [];
  return Object.freeze(measuredLoadTiles(reading.status).map(tile => {
    const series = reading.series[tile.metric] ?? [];
    const values = series.map(point => point.v).filter(value => Number.isFinite(value));
    const peak = Math.max(1, ...values) * 1.1;
    return Object.freeze({id: tile.metric, metric: tile.metric, unit: tile.unit, label: t(tile.label), value: loadValue(tile, reading.status[tile.metric]), samples: Object.freeze(series.map(point => point.v)), min: 0, max: peak});
  }));
}

export type OwnerHealthDetail = Readonly<{metric: LoadTile['metric']; label: string; value: string; unit: LoadTile['unit']; points: readonly TelemetryPoint[]; min: number; max: number; lowest: number; average: number; highest: number; highestAt: string}>;

/** The opened tile: one longer line on a true 0–100 axis for percentages, worked out from the line's own points. */
export function ownerHealthDetail(reading: TelemetryReading | undefined, metric: LoadTile['metric'], t: Translate): OwnerHealthDetail | undefined {
  const tile = reading ? measuredLoadTiles(reading.status).find(value => value.metric === metric) : undefined;
  if (!reading || !tile) return undefined;
  const points = reading.series[metric] ?? [];
  if (points.length < 2) return undefined;
  const values = points.map(point => point.v);
  const highestPoint = points.reduce((best, point) => point.v > best.v ? point : best, points[0]);
  const percent = tile.unit === 'percent';
  return Object.freeze({
    metric, label: t(tile.label), value: loadValue(tile, reading.status[metric]), unit: tile.unit,
    points: Object.freeze([...points]), min: 0, max: percent ? 100 : Math.max(1, ...values) * 1.1,
    lowest: Math.min(...values), average: values.reduce((sum, value) => sum + value, 0) / values.length,
    highest: Math.max(...values), highestAt: when(highestPoint.t),
  });
}

export type OwnerStatistic = Readonly<{id: 'plays' | 'people' | 'hours' | 'converted'; label: string; value: string; note?: string}>;

/** The four viewing figures, in the drawn order, with the Converted share under its count. */
export function ownerStatistics(summary: PlaySummary | undefined, t: Translate): readonly OwnerStatistic[] {
  if (!summary) return [];
  const share = summary.plays ? Math.round(summary.converted / summary.plays * 100) : 0;
  return Object.freeze([
    {id: 'plays', label: t('server.viewing.plays'), value: String(summary.plays)},
    {id: 'people', label: t('server.viewing.people'), value: String(summary.people)},
    {id: 'hours', label: t('server.viewing.hours'), value: (summary.watchedMs / 3_600_000).toFixed(1)},
    {id: 'converted', label: t('server.viewing.converted'), value: String(summary.converted), ...(summary.plays ? {note: t('next.dashboard.convertedShare', {percent: share})} : {})},
  ]);
}

export type OwnerBarRow = Readonly<{id: string; label: string; value: string; fraction: number; initial?: string}>;

/** Most played and Most active share one bar shape; the tallest supplied count is the full bar. */
export function ownerBarRows(rows: readonly PlayCount[], kind: 'played' | 'active'): readonly OwnerBarRow[] {
  const max = Math.max(1, ...rows.map(row => row.plays));
  return Object.freeze(rows.map((row, index) => ({id: `${kind}-${index}-${row.name}`, label: row.name, value: String(row.plays), fraction: Math.max(0, Math.min(1, row.plays / max)), ...(kind === 'active' ? {initial: (row.name.trim()[0] ?? '?').toUpperCase()} : {})})));
}

/** "Updated a few seconds ago" is only said while it is true; otherwise the real clock time. */
export function ownerUpdatedLabel(observedAt: number | undefined, now: number, t: Translate): string | undefined {
  if (!observedAt || !Number.isFinite(observedAt)) return undefined;
  const seconds = Math.max(0, Math.round((now - observedAt) / 1000));
  return seconds < 120 ? t('next.dashboard.updatedWhen', {when: t('next.dashboard.secondsAgo', {seconds})}) : t('next.dashboard.updatedWhen', {when: when(observedAt)});
}

/** Totals from the loaded page, used only when the server did not publish its own. */
export function ownerDerivedTotals(sessions: readonly AdminSession[]): OwnerSessionTotals {
  const converting = sessions.filter(session => ownerConverting(session)).length;
  return {streams: sessions.length, paused: sessions.filter(session => session.state === 'paused').length, converting, bandwidthKbps: sessions.reduce((sum, session) => sum + (session.bandwidthKbps || session.bitrateKbps || 0), 0), people: sessions.length, atHome: sessions.filter(session => session.location === 'local').length, away: sessions.filter(session => session.location === 'remote').length};
}

export const ownerPeopleLine = (totals: OwnerSessionTotals | undefined, t: Translate): string | undefined =>
  totals ? `${t('next.dashboard.peopleCount', {count: totals.people})}${totals.atHome + totals.away > 0 ? ` · ${t('next.dashboard.locations', {home: totals.atHome, away: totals.away})}` : ''}` : undefined;

/**
 * Past ten streams the cards give way to compact rows (frames 4d–4f). The count is the
 * server's own totals; a controller that published no totals keeps the cards, because a
 * page of ten cannot prove there are more.
 */
export const ownerUsesCompactRows = (totals: OwnerSessionTotals | undefined): boolean => (totals?.streams ?? 0) > OWNER_STREAM_CARD_LIMIT;

/** One drawn total: Streams with its paused count, Converting with its share, Bandwidth sent. */
export type OwnerStreamTotal = Readonly<{id: 'streams' | 'converting' | 'bandwidth'; label: string; value: string; unit?: string; note?: string; tone?: 'converting'}>;

/** The three totals count every stream, paused ones included; only the server's own totals are drawn. */
export function ownerStreamTotals(totals: OwnerSessionTotals, t: Translate): readonly OwnerStreamTotal[] {
  const share = totals.streams > 0 ? Math.round(totals.converting / totals.streams * 100) : undefined;
  const rate = totals.bandwidthKbps > 0 ? rateMbps({bandwidthKbps: totals.bandwidthKbps}) : undefined;
  const tiles: OwnerStreamTotal[] = [
    {id: 'streams', label: t('next.dashboard.streamsLabel'), value: String(totals.streams), note: t('next.dashboard.streamsPaused', {count: totals.paused})},
    {id: 'converting', label: t('next.dashboard.converting'), value: String(totals.converting), ...(share === undefined ? {} : {note: t('next.dashboard.convertingShare', {percent: share})}), tone: 'converting'},
    {id: 'bandwidth', label: t('next.dashboard.bandwidth'), value: rate ?? '0', unit: t('next.dashboard.unit.megabits'), note: t('next.dashboard.sentToViewers')},
  ];
  return Object.freeze(tiles);
}

/** The rows in view plus overscan, at the compact row's fixed pitch. */
export const ownerCompactWindow = (scrollTop: number, viewport: number, count: number): {first: number; last: number} =>
  windowRange(scrollTop, viewport, count, OWNER_COMPACT_ROW_PX);

/** Server order is canonical, so pages are appended in the order they arrive and never re-ranked here. */
export function appendOwnerSessions(current: readonly AdminSession[], page: readonly AdminSession[]): readonly AdminSession[] {
  const seen = new Set(current.map(session => session.id));
  const added = page.filter(session => !seen.has(session.id));
  return added.length ? Object.freeze([...current, ...added]) : current;
}
