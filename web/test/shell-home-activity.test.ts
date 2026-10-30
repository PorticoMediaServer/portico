import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import type {ActivitySummarySession, OwnerActivitySummary} from '@core/admin/activity-summary/index.ts';
import {activityPanelView, activitySubject, activityView} from '../src/shell/activity-model.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';

const environment = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
function stream(): ActivitySummarySession { return {id: 'session', kind: 'vod', state: 'playing', positionMs: 480000, startedAt: '2026-10-22T10:00:00Z', problem: false, converting: true,
  user: {id: 'account', username: f.account}, profile: {id: 'profile', name: f.profile}, device: {id: 'device', name: f.device},
  decision: {video: {action: 'transcode', reasons: []}}, quality: {mode: 'fixed', id: '2160p'}, videoHeight: 1080,
  item: {id: 'episode', title: f.episode, kind: 'episode', showId: 'show', showTitle: f.show, seasonNumber: 2, episodeNumber: 3, art: []}, durationMs: 1800000}; }
function summary(session = stream()): OwnerActivitySummary { return {scope: {serverId: 'server', viewerFence: 'viewer-fence'}, serverName: f.server, observedAt: '2026-10-22T12:00:00Z',
  streams: {count: 9, convertingCount: 4, overflow: {count: 6, convertingCount: 3}, items: [session, {...session, id: 'second', converting: false, decision: {}}, {...session, id: 'third', converting: false, decision: {}}]}, scans: [{libraryId: 'library', libraryName: f.libraryPrefix, kind: 'movies', phase: 'analyzing', analyzed: 12, discovered: 40}],
  tasks: {count: 4, items: [{id: 'task', kind: 'metadata', state: 'running', phase: 'fetching', processed: 5}]},
  update: {available: true, state: 'available', current: '1.0', latest: '1.1'}, health: {degraded: false}, majorIssues: [], lastScanFinishedAt: '2026-10-22T11:55:00Z'}; }
const actions = {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue: () => {}};

test('Activity maps semantic episode identity while keeping the real profile and device separate', () => {
  const view = activityView(summary(), environment, actions), value = view.streams[0];
  assert.equal(value.identity!.title, f.show);
  assert.equal(value.identity!.subtitle.find(part => part.id === 'episode-title')?.text, f.episode);
  assert.equal(value.username, f.account); assert.equal(value.name, f.profile); assert.equal(value.device, f.device);
  assert.equal(value.positionMs, 480000); assert.equal(value.durationMs, 1800000);
  assert.equal(view.streamCount, 9); assert.equal(view.taskCount, 4);
});
test('conversion names the delivered height and never substitutes the requested quality ceiling', () => {
  const view = activityView(summary(), environment, actions);
  assert.ok(view.streams[0].conversion?.text.includes('1080p'));
  assert.ok(!view.streams[0].conversion?.text.includes('2160'));
  assert.equal(activityView(summary({...stream(), videoHeight: undefined}), environment, actions).streams[0].conversion?.text, defaultI18n.t('web.nowPlaying.delivery.convertVideo'));
});
test('Activity decisions use canonical open-enum validation and conversion precedence', () => {
  const view = (decision: ActivitySummarySession['decision']) => activityView(summary({...stream(), decision, converting: decision.video?.action === 'transcode' || decision.subtitles?.action === 'burn' || decision.audio?.action === 'transcode'}), environment, actions).streams[0];
  assert.equal(view({video: {action: 'future-video-mode', reasons: []}, audio: {action: 'transcode', reasons: []}}).conversion?.text,
    defaultI18n.t('web.nowPlaying.delivery.convertAudio'));
  assert.equal(view({video: {action: 'copy', reasons: []}, audio: {action: 'transcode', reasons: []}, subtitles: {action: 'burn', reasons: []}}).conversion?.text,
    defaultI18n.t('next.home.shell.convertingTo', {quality: '1080p'}));
  for (const action of ['direct', 'copy', 'future-video-mode']) {
    assert.equal(view({video: {action, reasons: []}, audio: {action: 'direct', reasons: []}}).conversion, undefined);
  }
  assert.equal(view({}).conversion, undefined);
});
test('unknown totals stay unknown and server work counts and identifiers survive mapping', () => {
  const view = activityView(summary(), environment, actions);
  assert.equal(view.scans[0].discovered, 40); assert.equal(view.scans[0].processed, 12); assert.equal(view.scans[0].total, undefined);
  assert.equal(view.tasks[0].processed, 5); assert.equal(view.tasks[0].total, undefined);
  assert.equal(view.update.available, true); assert.equal(view.update.current, '1.0'); assert.equal(view.update.latest, '1.1');
  assert.equal(view.lastScan, defaultI18n.relativeTime('2026-10-22T11:55:00Z', Date.parse('2026-10-22T12:00:00Z')));
});
test('an absent or unrecognized media projection never becomes an invented title', () => {
  assert.equal(activitySubject({...stream(), item: undefined}), undefined);
  assert.equal(activitySubject({...stream(), item: {...stream().item!, kind: 'unrecognized'}}), undefined);
});
test('cold loading, refreshing and errors assert no health, counts or update facts, while a ready zero is real', () => {
  for (const phase of ['idle', 'loading', 'error', 'unavailable'] as const) {
    const view = activityPanelView({phase, document: summary()}, f.server, environment, actions);
    for (const field of ['health', 'streamCount', 'taskCount', 'update', 'majorIssue']) assert.equal(Object.hasOwn(view, field), false);
    assert.deepEqual(view.streams, []); assert.deepEqual(view.scans, []); assert.deepEqual(view.tasks, []);
  }
  const document = {...summary(), streams: {count: 0, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: []}, tasks: {count: 0, items: []}, scans: []};
  const ready = activityPanelView({phase: 'ready', document}, f.server, environment, actions);
  assert.equal(ready.streamCount, 0); assert.equal(ready.taskCount, 0); assert.equal(ready.health, 'normal');
});

test('Activity preserves returned priority order and authoritative full-set overflow without inferring recency', () => {
  const document = summary();
  const view = activityView(document, environment, actions);
  assert.deepEqual(view.streams.map(row => row.id), document.streams.items.map(row => row.id));
  assert.equal(view.streamOverflow, document.streams.overflow);
  assert.equal(view.streams[0].startedAt, document.streams.items[0].startedAt);
});
test('missing usernames and missing subject facts stay absent and never drop a selected session', () => {
  const document = summary({...stream(), user: {id: 'account'}, profile: {id: 'profile', name: ''}, device: {id: 'device', name: ''}, item: undefined, problem: true});
  const row = activityView(document, environment, actions).streams[0];
  assert.equal(row.username, undefined); assert.equal(row.name, ''); assert.equal(row.device, '');
  assert.equal(row.identity, undefined); assert.equal(row.title, undefined); assert.ok(row.problem);
  assert.equal(activityView(document, environment, actions).streams.length, 3);
});
test('row and overflow navigation use the existing genuine Dashboard callbacks', () => {
  const calls: unknown[] = [];
  const view = activityView(summary(), environment, {...actions, onDashboard: section => calls.push(section)});
  view.streams[0].onOpen(); view.onDashboard('streams'); view.scans[0].onOpen(); view.tasks[0].onOpen();
  assert.deepEqual(calls, ['streams', 'streams', 'activity', 'activity']);
});

test('a stored buffering problem uses its actual state while other problems remain general', () => {
  const buffering = activityView(summary({...stream(), state: 'buffering', problem: true}), environment, actions).streams[0];
  assert.equal(buffering.problem?.text, defaultI18n.t('web.together.loading'));
  const error = activityView(summary({...stream(), state: 'error', problem: true}), environment, actions).streams[0];
  assert.equal(error.problem?.text, defaultI18n.t('next.shell.activityProblem'));
  assert.equal(activityView(summary({...stream(), state: 'playing', problem: false}), environment, actions).streams[0].problem, undefined);
});

test('watchdog degraded is unqualified attention and never fabricates a major issue or destination', () => {
  const document = {...summary(), health: {degraded: true}};
  const view = activityView(document, environment, actions);
  assert.equal(view.health, 'attention');
  assert.equal(view.majorIssue, undefined);
  assert.equal(activityView(summary({...stream(), state: 'buffering', problem: true}), environment, actions).majorIssue, undefined);
  for (const phase of ['loading', 'error', 'unavailable'] as const) {
    const stale = activityPanelView({phase, document}, f.server, environment, actions);
    assert.equal(stale.majorIssue, undefined); assert.equal(stale.health, undefined);
  }
});
