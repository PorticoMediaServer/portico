import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import type {ActivitySummarySession, OwnerActivitySummary} from '@core/admin/activity-summary/index.ts';
import {activityPanelView, activityView} from '../src/shell/activity-model.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';

// The owner's summary as the rebuilt server sends it (GET /v1/admin/activity): Now playing's first three streams in its
// order, how many there are and how many are converted, the Dashboard's scans, tasks and alerts, and the last scan.
const environment = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
function stream(): ActivitySummarySession { return {id: 'session', itemId: 'itm_episode', title: f.episode, kind: 'episode', state: 'playing', positionMs: 480000, durationMs: 1800000,
  startedAt: '2026-10-22T10:00:00Z', username: f.account, profile: f.profile, deviceName: f.device, away: false, route: 'converted', delivery: {video: {action: 'transcode', reasons: []}}}; }
function summary(session = stream()): OwnerActivitySummary { return {serverName: f.server, observedAt: '2026-10-22T12:00:00Z',
  streams: {count: 9, converting: 4, items: [session, {...session, id: 'second', route: 'original', delivery: {}}, {...session, id: 'third', route: 'repackaged', delivery: {}}]},
  scans: [{libraryId: 'library', libraryName: f.libraryPrefix, kind: 'movies', phase: 'analyzing', analyzed: 12, discovered: 40}],
  tasks: {count: 4, items: [{id: 'task', kind: 'metadata', state: 'running', phase: 'fetching', processed: 5}]},
  alerts: [{code: 'update_available', severity: 'warning', major: false, params: {current: '1.0', latest: '1.1'}, action: {kind: 'update', target: ''}}],
  lastScanAt: '2026-10-22T11:55:00Z'}; }
const actions = {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue: () => {}};

test('a stream keeps the server\'s title, and the person\'s username, profile and device separate', () => {
  const view = activityView(summary(), environment, actions), value = view.streams[0];
  assert.equal(value.title, f.episode); assert.equal(value.identity, undefined, 'the server names the title; nothing is made up around it');
  assert.equal(value.username, f.account); assert.equal(value.name, f.profile); assert.equal(value.device, f.device);
  assert.equal(value.positionMs, 480000); assert.equal(value.durationMs, 1800000); assert.equal(value.paused, false);
  assert.equal(view.streamCount, 9); assert.equal(view.taskCount, 4);
});
test('only a converted stream says it is converting, video or audio by what the server converts', () => {
  const view = (delivery: ActivitySummarySession['delivery'], route = 'converted') => activityView(summary({...stream(), delivery, route}), environment, actions).streams[0];
  assert.equal(view({video: {action: 'transcode', reasons: []}}).conversion?.text, defaultI18n.t('web.nowPlaying.delivery.convertVideo'));
  assert.equal(view({video: {action: 'copy', reasons: []}, subtitles: {action: 'burn', reasons: []}}).conversion?.text, defaultI18n.t('web.nowPlaying.delivery.convertVideo'));
  assert.equal(view({video: {action: 'copy', reasons: []}, audio: {action: 'transcode', reasons: []}}).conversion?.text, defaultI18n.t('web.nowPlaying.delivery.convertAudio'));
  for (const route of ['original', 'repackaged']) assert.equal(view({}, route).conversion, undefined, route);
});
test('unknown totals stay unknown; the update and the last scan come from the server', () => {
  const view = activityView(summary(), environment, actions);
  assert.equal(view.scans[0].discovered, 40); assert.equal(view.scans[0].processed, 12); assert.equal(view.scans[0].total, undefined);
  assert.equal(view.tasks[0].processed, 5); assert.equal(view.tasks[0].total, undefined); assert.equal(view.tasks[0].progressText, undefined);
  assert.deepEqual([view.update.available, view.update.current, view.update.latest], [true, '1.0', '1.1']);
  assert.equal(view.lastScan, defaultI18n.relativeTime('2026-10-22T11:55:00Z', Date.parse('2026-10-22T12:00:00Z'), {inSentence: true}));
  const none = activityView({...summary(), alerts: []}, environment, actions);
  assert.equal(none.update.available, false); assert.equal(none.health, 'normal'); assert.equal(none.majorIssue, undefined);
  const noLatest = activityView({...summary(), alerts: [{code: 'update_available', severity: 'warning', major: false, params: {current: '1.0'}, action: {kind: 'update', target: ''}}]}, environment, actions);
  assert.equal(noLatest.update.available, false, 'an update with no version to name is not offered');
  const {lastScanAt: _, ...first} = summary();
  assert.equal(activityView(first, environment, actions).lastScan, undefined);
});
test('the last scan reads mid-sentence: "finished just now", "finished yesterday", never a capital in the middle', () => {
  const now = Date.parse('2026-10-22T12:00:00Z');
  const sentence = (at: string) => defaultI18n.t('next.shell.lastScan', {when: defaultI18n.relativeTime(at, now, {inSentence: true})});
  assert.equal(sentence('2026-10-22T11:59:50Z'), 'The last library scan finished just now.');
  assert.equal(sentence('2026-10-21T12:00:00Z'), 'The last library scan finished yesterday.');
  assert.equal(sentence('2026-10-22T09:00:00Z'), 'The last library scan finished 3 hours ago.');
  // Standalone labels keep their capital.
  assert.equal(defaultI18n.relativeTime('2026-10-22T11:59:50Z', now), 'Just now');
  assert.equal(defaultI18n.relativeTime('2026-10-21T12:00:00Z', now), 'Yesterday');
});
test('cold loading, refreshing and errors assert no health, counts or update facts, while a ready zero is real', () => {
  for (const phase of ['idle', 'loading', 'error', 'unavailable'] as const) {
    const view = activityPanelView({phase, document: summary()}, f.server, environment, actions);
    for (const field of ['health', 'streamCount', 'taskCount', 'update', 'majorIssue']) assert.equal(Object.hasOwn(view, field), false);
    assert.deepEqual(view.streams, []); assert.deepEqual(view.scans, []); assert.deepEqual(view.tasks, []);
  }
  const document = {...summary(), streams: {count: 0, converting: 0, items: []}, tasks: {count: 0, items: []}, scans: []};
  const ready = activityPanelView({phase: 'ready', document}, f.server, environment, actions);
  assert.equal(ready.streamCount, 0); assert.equal(ready.taskCount, 0); assert.equal(ready.health, 'normal');
  assert.deepEqual(ready.streamOverflow, {count: 0, convertingCount: 0});
});

test('the streams keep the server\'s order, and the line for the rest is the server\'s counts less the streams shown', () => {
  const document = summary();
  const view = activityView(document, environment, actions);
  assert.deepEqual(view.streams.map(row => row.id), document.streams.items.map(row => row.id));
  // Nine streams, four converted; three shown, one of them converted: six more, three of them converted.
  assert.deepEqual(view.streamOverflow, {count: 6, convertingCount: 3});
  assert.equal(view.streams[0].startedAt, document.streams.items[0].startedAt);
});
test('a stream with no title or profile name keeps its row, named by the username alone', () => {
  const {title: _, profile: __, ...bare} = stream();
  const document = summary({...bare, deviceName: ''});
  const row = activityView(document, environment, actions).streams[0];
  assert.equal(row.title, undefined); assert.equal(row.name, f.account); assert.equal(row.username, f.account); assert.equal(row.device, '');
  assert.equal(row.problem, undefined, 'a stream that is not stuck says nothing about buffering');
  assert.equal(activityView(document, environment, actions).streams.length, 3);
});
test('row and overflow navigation use the existing genuine Dashboard callbacks', () => {
  const calls: unknown[] = [];
  const view = activityView(summary(), environment, {...actions, onDashboard: section => calls.push(section)});
  view.streams[0].onOpen(); view.onDashboard('streams'); view.scans[0].onOpen(); view.tasks[0].onOpen();
  assert.deepEqual(calls, ['streams', 'streams', 'activity', 'activity']);
});

test('a stream stuck buffering says Buffering in amber where the converting note goes; a short wait says nothing', () => {
  const stuck = activityView(summary({...stream(), state: 'buffering', problem: true, bufferingSince: '2026-10-22T11:59:40Z'}), environment, actions).streams[0];
  assert.deepEqual(stuck.problem, {id: 'problem', text: defaultI18n.t('next.dashboard.buffering'), role: 'status'});
  assert.equal(defaultI18n.t('next.dashboard.buffering'), 'Buffering');
  const waiting = activityView(summary({...stream(), state: 'buffering'}), environment, actions).streams[0];
  assert.equal(waiting.problem, undefined, 'before ten seconds the server does not call it a problem');
});
