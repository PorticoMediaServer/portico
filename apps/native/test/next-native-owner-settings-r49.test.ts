import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import type {Alert, Job} from '../../../packages/client-core/src/console';
import type {AdminSession} from '../../../packages/client-core/src/playback-v1/types';
import type {TelemetryReading} from '../../../packages/client-core/src/telemetry';
import {
  ownerAlertView, ownerBarRows, ownerDerivedTotals, ownerHealthDetail, ownerHealthLine, ownerHealthTiles, ownerHealthWindowLabel, ownerIsNewServer,
  ownerJobView, ownerStatistics, ownerStreamView, ownerUpdatedLabel, ownerViewingPeriodLabel, OWNER_HEALTH_WINDOWS, OWNER_VIEWING_PERIODS,
} from '../src-next/screens/settings-live/owner-dashboard/model';

const t = defaultI18n.t;
const env = {t, locale: 'en-US'};

function session(overrides: Partial<AdminSession> = {}): AdminSession {
  return Object.freeze({
    id: 's1', user: 'priya.n', profile: 'priya.n', device: 'iPhone', title: 'Spirited Away', kind: 'vod', state: 'playing', positionMs: 3_225_000, durationMs: 7_500_000,
    decision: {video: {action: 'transcode', reasons: []}, audio: {action: 'direct', reasons: []}}, location: 'remote', quality: {mode: 'fixed', maxHeight: 1080}, bandwidthKbps: 8000,
    startedAt: '2026-10-24T15:49:00Z', ...overrides,
  }) as AdminSession;
}

const measurement = (facts: Record<string, {state: string; value: unknown}>) => ({name: 'health', observedAt: 1, freshUntil: 2, facts});

test('Ruling 4 route facts name Original, Repackaged and Converted with its height', () => {
  assert.equal(ownerStreamView(session({decision: {video: {action: 'direct', reasons: []}, audio: {action: 'direct', reasons: []}}}), env).facts.find(part => part.id === 'route')?.text, 'Original');
  assert.equal(ownerStreamView(session({decision: {video: {action: 'copy', reasons: []}, audio: {action: 'direct', reasons: []}}, quality: undefined}), env).facts.find(part => part.id === 'route')?.text, 'Repackaged');
  assert.equal(ownerStreamView(session(), env).facts.find(part => part.id === 'route')?.text, 'Converted to 1080p');
  assert.equal(ownerStreamView(session({quality: {mode: 'automatic'}}), env).facts.find(part => part.id === 'route')?.text, 'Converted');
  assert.equal(ownerStreamView(session({decision: {}}), env).facts.find(part => part.id === 'route'), undefined);
});

test('a stream card carries only the session facts the server supplied', () => {
  const view = ownerStreamView(session(), env);
  assert.equal(view.title, 'Spirited Away');
  assert.deepEqual(view.facts.map(part => part.text), ['Away from home', 'Converted to 1080p', '8.0 Mb/s']);
  assert.equal(view.position?.text, 'At 53:45 of 2:05:00');
  assert.equal(view.progress, 0.43);
  assert.equal(view.paused, false);
  assert.deepEqual(view.viewer, {name: 'priya.n', username: 'priya.n', device: 'iPhone'});
});

test('a paused episode card keeps the show title, the episode subtitle and Paused', () => {
  const view = ownerStreamView(session({state: 'paused', item: {id: 'e1', kind: 'episode', title: 'Long, Long Time', showId: 'show', showTitle: 'The Last of Us', seasonNumber: 1, episodeNumber: 3, art: []} as AdminSession['item']}), env);
  assert.equal(view.title, 'The Last of Us');
  assert.deepEqual(view.identity.map(part => part.text), ['S1 E3', 'Long, Long Time']);
  assert.equal(view.state?.text, 'Paused');
  assert.equal(view.paused, true);
});

test('a channel stream states when it started and has no position bar', () => {
  const view = ownerStreamView(session({kind: 'channel', durationMs: undefined, startedAt: '2026-10-24T15:52:00Z'}), {...env, timeZone: 'UTC', hourCycle: 'h12'} as typeof env);
  assert.equal(view.position?.id, 'started');
  assert.match(view.position?.text ?? '', /^Started /);
  assert.equal(view.progress, undefined);
});

test('Health keeps the measured sentence and degrades honestly', () => {
  const healthy = ownerHealthLine(measurement({uptime: {state: 'available', value: 518_400}}), measurement({stateVolumeAvailable: {state: 'available', value: 1_288_490_188_800}, databaseBytes: {state: 'available', value: 432_013_312}}), t);
  assert.equal(healthy.facts.degraded, false);
  assert.equal(healthy.status, 'Running normally');
  assert.match(healthy.line, /^Up for 6d 0h/);
  assert.match(healthy.line, /free ·/);
  assert.match(healthy.line, /Database /);
  const degraded = ownerHealthLine(measurement({database: {state: 'unavailable', value: null}}), undefined, t);
  assert.equal(degraded.facts.degraded, true);
  assert.equal(degraded.status, 'The database needs attention');
  assert.equal(degraded.line, '');
});

test('a server that just started says so only while its uptime is small', () => {
  assert.equal(ownerIsNewServer(120), true);
  assert.equal(ownerIsNewServer(901), false);
  assert.equal(ownerIsNewServer(undefined), false);
});

function reading(overrides: Partial<TelemetryReading> = {}): TelemetryReading {
  const points = (values: number[]) => values.map((v, index) => ({t: 1_000 + index * 1_000, v}));
  return {
    period: '1h', timeZone: 'UTC', from: '2026-10-24T15:00:00.000Z', until: '2026-10-24T16:00:00.000Z', buckets: [], window: '1h', observedAt: 1_000,
    status: {cpu: {status: 'available', value: 18}, memory: {status: 'unavailable', value: 0}, netOut: {status: 'available', value: 14 * 1024 * 1024}} as TelemetryReading['status'],
    series: {cpu: points([10, 18, 6]), netOut: points([8_000_000, 14 * 1024 * 1024])} as TelemetryReading['series'],
    ...overrides,
  };
}

test('Health tiles leave out a metric the server cannot measure and scale to their own points', () => {
  const tiles = ownerHealthTiles(reading(), t);
  assert.deepEqual(tiles.map(tile => tile.id), ['cpu', 'netOut']);
  assert.deepEqual(tiles.map(tile => tile.label), ['Processor', 'Sent to viewers']);
  assert.equal(tiles[0].value, '18%');
  assert.equal(tiles[1].value, '14 MB/s');
  assert.ok((tiles[0].max ?? 0) > 18 && (tiles[0].max ?? 0) < 20);
  assert.deepEqual(ownerHealthTiles(undefined, t), []);
});

test('the opened Health tile uses a true 0–100 axis for percentages and its own points for the figures', () => {
  const detail = ownerHealthDetail(reading(), 'cpu', t);
  assert.ok(detail);
  assert.equal(detail!.min, 0);
  assert.equal(detail!.max, 100);
  assert.equal(detail!.lowest, 6);
  assert.equal(detail!.highest, 18);
  assert.equal(detail!.average, (10 + 18 + 6) / 3);
  assert.ok(detail!.highestAt.length > 0);
  assert.equal(ownerHealthDetail(reading(), 'memory', t), undefined);
});

test('a task shows real progress only from a published total, else its state', () => {
  const running = ownerJobView({id: 'j', kind: 'library_scan', resource: 'Movies', trigger: 'manual', state: 'running', phase: 'scan', revision: 1, attempt: 1, createdAt: 1, updatedAt: 1, nextAt: 1, settingsRevision: 1, lane: 'scan', actions: ['pause'], processed: 51, total: 80}, t, defaultI18n.has);
  assert.equal(running.title, 'Movies');
  assert.equal(running.subtitle, '51 of 80 files');
  assert.equal(running.status, '64%');
  assert.equal(running.progress, 0.6375);
  assert.equal(running.tone, 'accent');
  const queued = ownerJobView({id: 'k', kind: 'trickplay', resource: 'TV Shows', trigger: 'auto', state: 'queued', phase: 'waiting', revision: 1, attempt: 1, createdAt: 1, updatedAt: 1, nextAt: 1, settingsRevision: 1, lane: 'background', actions: []}, t, defaultI18n.has);
  assert.equal(queued.title, 'TV Shows');
  assert.equal(queued.subtitle, 'Preview thumbnails');
  assert.equal(queued.status, 'Queued');
  assert.equal(queued.progress, undefined);
  assert.equal(queued.icon, 'i-image');
});

test('alerts are named from the catalogue and the permissions warning keeps its own copy', () => {
  const alert: Alert = {id: 'a', code: 'state-permissions', severity: 'warning', status: 'open', params: {}, firstAt: 1, lastAt: 2, occurrences: 1, revision: 1};
  const view = ownerAlertView(alert, t, defaultI18n.has);
  assert.equal(view.title, 'Other accounts on this computer can read Portico data');
  assert.match(view.body ?? '', /Fixing permissions keeps Portico’s files readable only by your account/);
  assert.equal(view.permissions, true);
  assert.equal(view.acknowledged, false);
  assert.equal(view.severity, 'Warning');
  const acked = ownerAlertView({...alert, code: 'low_storage', status: 'acknowledged', occurrences: 3}, t, defaultI18n.has);
  assert.equal(acked.title, 'Storage is running low');
  assert.equal(acked.acknowledged, true);
  assert.match(acked.meta, /Seen 3 times · last /);
  assert.match(acked.meta, /Acknowledged$/);
});

test('viewing statistics carry the four figures and the real Converted share', () => {
  const figures = ownerStatistics({period: '7d', timeZone: 'UTC', from: '2026-10-17T00:00:00.000Z', until: '2026-10-24T16:00:00.000Z', buckets: [], plays: 46, people: 4, watchedMs: 31 * 3_600_000, converted: 7, mostPlayed: [], mostActive: []}, t);
  assert.deepEqual(figures.map(figure => figure.value), ['46', '4', '31.0', '7']);
  assert.equal(figures[3].note, '15% of plays');
  assert.deepEqual(ownerStatistics(undefined, t), []);
});

test('ranked lists keep the server order and fill their bar from the tallest count', () => {
  const rows = ownerBarRows([{name: 'sam_k', plays: 18}, {name: 'demo', plays: 9}], 'active');
  assert.deepEqual(rows.map(row => [row.label, row.value, row.fraction, row.initial]), [['sam_k', '18', 1, 'S'], ['demo', '9', 0.5, 'D']]);
  assert.equal(ownerBarRows([], 'played').length, 0);
});

test('the updated line says seconds only while that is true', () => {
  const now = Date.UTC(2026, 9, 24, 16, 0, 0);
  assert.equal(ownerUpdatedLabel(now - 12_000, now, t), 'Updated 12 seconds ago');
  assert.match(ownerUpdatedLabel(now - 7_200_000, now, t) ?? '', /^Updated /);
  assert.equal(ownerUpdatedLabel(undefined, now, t), undefined);
});

test('derived totals count every loaded stream without inventing one', () => {
  const totals = ownerDerivedTotals([session(), session({id: 's2', state: 'paused', location: 'local', decision: {video: {action: 'direct', reasons: []}}})]);
  assert.equal(totals.streams, 2);
  assert.equal(totals.paused, 1);
  assert.equal(totals.converting, 1);
  assert.equal(totals.atHome, 1);
  assert.equal(totals.away, 1);
});

test('every drawn period label resolves in the catalogue', () => {
  for (const window of OWNER_HEALTH_WINDOWS) assert.ok(defaultI18n.has(ownerHealthWindowLabel(window)), ownerHealthWindowLabel(window));
  for (const period of OWNER_VIEWING_PERIODS) assert.ok(defaultI18n.has(ownerViewingPeriodLabel(period)), ownerViewingPeriodLabel(period));
  for (const key of ['next.dashboard.streamsLabel', 'next.dashboard.streamsPaused', 'next.dashboard.convertingShare', 'next.dashboard.sentToViewers', 'next.dashboard.delivered', 'next.dashboard.lowest', 'next.dashboard.averageValue', 'next.dashboard.highest', 'next.dashboard.nowValue', 'next.dashboard.highestAt', 'next.dashboard.updatedWhen', 'next.dashboard.updatedNow', 'next.dashboard.calmStreamsBody', 'next.dashboard.healthLater'] as const) assert.ok(defaultI18n.has(key), key);
});
