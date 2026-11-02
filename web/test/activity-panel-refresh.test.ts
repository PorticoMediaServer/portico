import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import type {ActivitySummarySession, OwnerActivitySummary} from '@core/admin/activity-summary/index.ts';
import type {ActivitySummarySnapshot} from '@core/admin/activity-summary/types.ts';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {defaultI18n} = await import('../../packages/i18n/src/index.ts');
const {activityPanelView} = await import('../src/shell/activity-model.ts');
const {panelRowMoves} = await import('../src/kit/shell/activity-motion.ts');
const {ActivityPanel} = await import('../src/kit/shell/Activity.tsx');

const environment = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
const actions = {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue: () => {}};

function stream(id: string, username: string, device: string, extra: Partial<ActivitySummarySession> = {}): ActivitySummarySession {
  return {id, kind: 'vod', state: 'playing', positionMs: 600000, durationMs: 2400000, startedAt: '2026-10-22T15:00:00Z', problem: false, converting: false,
    user: {id: `account-${id}`, username}, profile: {id: `profile-${id}`, name: username}, device: {id: `device-${id}`, name: device},
    decision: {}, quality: {mode: 'auto'}, item: {id: `item-${id}`, title: `Drawn ${id}`, kind: 'movie', year: 2001, art: []}, ...extra};
}
/** The drawn frame 2 with a scan and a task running, which is when the feed hints arrive fastest. */
function summary(current = 'Practical Magic 2 (2026).mkv', analyzed = 51): OwnerActivitySummary {
  return {scope: {serverId: 'server', viewerFence: 'viewer-fence'}, serverName: 'Portico demo', observedAt: `2026-10-22T15:20:0${analyzed % 10}Z`,
    health: {degraded: false}, majorIssues: [],
    streams: {count: 2, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: [stream('sam', 'sam_k', 'Living room TV'), stream('nana', 'nana_r', 'Kitchen iPad')]},
    scans: [{libraryId: 'movies', libraryName: 'Movies', kind: 'movies', phase: 'analyzing', analyzed, discovered: 80, total: 80, current}],
    tasks: {count: 1, items: [{id: 'previews', kind: 'trickplay', libraryId: 'tv', libraryName: 'TV Shows', state: 'running', phase: 'making-previews', processed: 38, total: 96, unit: 'episodes'}]},
    update: {available: false, state: 'unconfigured', current: '0.1.0'}, lastScanFinishedAt: '2026-10-22T15:12:00Z'};
}
const rows = (html: string) => (html.match(/data-panel-row=/g) ?? []).length;
const drawn = (view: ReturnType<typeof activityPanelView>) => renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view}));

test('a scan update changes the panel values in place and never replaces its rows', () => {
  const first = activityPanelView({phase: 'ready', document: summary()}, 'Portico demo', environment, actions);
  const next = activityPanelView({phase: 'ready', document: summary('Half-Light (2024).mkv', 57)}, 'Portico demo', environment, actions);
  // The same rows own the panel before and after the read: no ids changed, no loading state.
  assert.deepEqual(next.streams.map(row => row.id), first.streams.map(row => row.id));
  assert.deepEqual(next.scans.map(row => row.id), first.scans.map(row => row.id));
  assert.deepEqual(next.tasks.map(row => row.id), first.tasks.map(row => row.id));
  assert.equal(next.phase, 'ready');
  const before = drawn(first), after = drawn(next);
  assert.equal(rows(before), rows(after), 'the same number of rows still owns the panel');
  assert.match(after, /Half-Light \(2024\)\.mkv/); assert.match(after, /57 of 80 files/);
  assert.doesNotMatch(after, /role="status"/, 'the loading state never replaces the content');
});

test('the panel keeps its rows from the last ready read and only says loading when there is nothing to draw', () => {
  const document = summary();
  const view = activityPanelView({phase: 'ready', document}, 'Portico demo', environment, actions);
  assert.equal(view.phase, 'ready');
  assert.equal(view.scans[0]?.title, 'Movies');
  assert.doesNotMatch(drawn(view), /role="status"/);
  // The service no longer publishes a loading snapshot over a shown document (its own test holds
  // that), and a read that really has nothing to draw still says so.
  const cold = activityPanelView({phase: 'loading'}, 'Portico demo', environment, actions);
  assert.equal(cold.phase, 'loading');
  assert.match(drawn(cold), /role="status"/);
  assert.equal(activityPanelView({phase: 'error'}, 'Portico demo', environment, actions).phase, 'error');
});

test('only a row that really left or arrived moves the rows below it', () => {
  const placed = (...rows: [string, number][]) => new Map(rows.map(([key, top]) => [key, {key, top}]));
  const before = placed(['stream:sam', 0], ['stream:nana', 64], ['scan:movies', 140], ['task:previews', 210]);
  // A scan that found more files changes values, not places.
  assert.deepEqual(panelRowMoves(before, [...before.values()]), []);
  // The second stream ends: the scan and task rows close up, the first stream stays put.
  assert.deepEqual(panelRowMoves(before, [{key: 'stream:sam', top: 0}, {key: 'scan:movies', top: 76}, {key: 'task:previews', top: 146}]), [
    {key: 'scan:movies', from: 64}, {key: 'task:previews', from: 64},
  ]);
  // A new scan row above the task pushes it down; the new row has no earlier place to travel from.
  assert.deepEqual(panelRowMoves(placed(['task:previews', 0]), [{key: 'scan:movies', top: 0}, {key: 'task:previews', top: 70}]), [{key: 'task:previews', from: -70}]);
});
