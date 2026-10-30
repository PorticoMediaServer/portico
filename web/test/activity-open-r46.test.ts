import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import type {ActivitySummarySession, OwnerActivitySummary} from '@core/admin/activity-summary/index.ts';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {defaultI18n} = await import('../../packages/i18n/src/index.ts');
const {activityView} = await import('../src/shell/activity-model.ts');
const {ActivityPanel, TopBarActivity} = await import('../src/kit/shell/Activity.tsx');

const environment = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
const actions = {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue: () => {}};

/** The drawn frame-2 state: three streams in the server's order, one conversion, real work and an update. */
function stream(id: string, username: string, profile: string, device: string, startedAt: string, extra: Partial<ActivitySummarySession> = {}): ActivitySummarySession {
  return {id, kind: 'vod', state: 'playing', positionMs: 600000, durationMs: 2400000, startedAt, problem: false, converting: false,
    user: {id: `account-${id}`, username}, profile: {id: `profile-${id}`, name: profile}, device: {id: `device-${id}`, name: device},
    decision: {}, quality: {mode: 'auto'}, item: {id: `item-${id}`, title: 'Drawn title', kind: 'movie', year: 2001, art: []}, ...extra};
}
export function frameTwo(): OwnerActivitySummary {
  return {scope: {serverId: 'server', viewerFence: 'viewer-fence'}, serverName: 'Portico demo', observedAt: '2026-10-22T15:20:00Z',
    health: {degraded: false}, majorIssues: [],
    streams: {count: 3, convertingCount: 1, overflow: {count: 0, convertingCount: 0}, items: [
      stream('priya', 'priya.n', 'priya.n', 'iPhone', '2026-10-22T15:04:00Z', {converting: true, videoHeight: 1080, decision: {video: {action: 'transcode', reasons: []}},
        item: {id: 'spirited', title: 'Spirited Away', kind: 'movie', year: 2001, art: []}}),
      stream('sam', 'sam_k', 'Sam', 'Living room TV', '2026-10-22T15:01:00Z',
        {item: {id: 'long-long-time', title: 'Long, Long Time', kind: 'episode', showId: 'tlou', showTitle: 'The Last of Us', seasonNumber: 1, episodeNumber: 3, art: []}}),
      stream('nana', 'nana_r', 'Nana', 'Kitchen iPad', '2026-10-22T14:58:00Z',
        {item: {id: 'world-tonight', title: 'World Tonight', kind: 'movie', art: []}}),
    ]},
    scans: [{libraryId: 'movies', libraryName: 'Movies', kind: 'movies', phase: 'analyzing', analyzed: 51, discovered: 80, total: 80, current: 'Practical Magic 2 (2026).mkv'}],
    tasks: {count: 2, items: [
      {id: 'previews', kind: 'trickplay', libraryId: 'tv', libraryName: 'TV Shows', state: 'running', phase: 'making-previews', processed: 38, total: 96, unit: 'episodes'},
      {id: 'intros', kind: 'analysis', libraryId: 'anime', libraryName: 'Anime', state: 'running', phase: 'measuring', processed: 9, total: 32, unit: 'episodes'},
    ]},
    update: {available: true, state: 'available', current: '0.1.0', latest: '0.2.0'}, lastScanFinishedAt: '2026-10-22T15:12:00Z'};
}

test('frame 2 keeps the server order, naming and real work fractions without a summary line', () => {
  const view = activityView(frameTwo(), environment, actions);
  assert.equal(view.phase, 'ready');
  assert.equal(view.streamCount, 3);
  assert.deepEqual(view.streams.map(row => row.id), ['priya', 'sam', 'nana']);
  assert.deepEqual(view.streams.map(row => [row.username, row.name, row.device]), [['priya.n', 'priya.n', 'iPhone'], ['sam_k', 'Sam', 'Living room TV'], ['nana_r', 'Nana', 'Kitchen iPad']]);
  assert.equal(view.streams[0].conversion?.text, defaultI18n.t('next.home.shell.convertingTo', {quality: '1080p'}));
  assert.equal(view.streams[1].conversion, undefined);
  assert.equal(view.scans[0].processed, 51); assert.equal(view.scans[0].total, 80); assert.equal(view.scans[0].current, 'Practical Magic 2 (2026).mkv');
  assert.deepEqual(view.tasks.map(task => [task.detail, task.progressText, task.processed, task.total]), [
    ['TV Shows', defaultI18n.t('next.dashboard.taskProgressEpisodes', {done: 38, total: 96}), 38, 96],
    ['Anime', defaultI18n.t('next.dashboard.taskProgressEpisodes', {done: 9, total: 32}), 9, 32],
  ]);
  assert.deepEqual([view.update?.latest, view.update?.current], ['0.2.0', '0.1.0']);
  assert.equal(view.health, 'normal');
  assert.equal(view.majorIssue, undefined);
});

test('frame 2 renders three rows, real percentages and no invented work or issue', () => {
  const html = renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view: activityView(frameTwo(), environment, actions)}));
  assert.equal((html.match(/data-stream="true"/g) ?? []).length, 3);
  assert.ok(html.indexOf('Spirited Away') < html.indexOf('The Last of Us') && html.indexOf('The Last of Us') < html.indexOf('World Tonight'));
  assert.match(html, /Now playing/); assert.match(html, /Library scans/); assert.match(html, /Background tasks/);
  assert.match(html, /Converting to 1080p/);
  assert.match(html, /51 of 80 files/); assert.match(html, /Practical Magic 2 \(2026\)\.mkv/); assert.match(html, />64%</);
  assert.match(html, /38 of 96 episodes/); assert.match(html, />40%</);
  assert.match(html, /9 of 32 episodes/); assert.match(html, />28%</);
  assert.match(html, /TV Shows/); assert.match(html, /Anime/);
  assert.match(html, /Version 0\.2\.0/); assert.match(html, /You have 0\.1\.0/);
  assert.match(html, /3 streams/);
  // priya.n's profile has the same name as the username, so only two rows draw a profile.
  assert.equal((html.match(/class="profile"/g) ?? []).length, 2);
  assert.doesNotMatch(html, /data-streams-more/);
  assert.doesNotMatch(html, /data-major-issue/);
  assert.doesNotMatch(html, /Needs attention/);
});

test('the open frame-2 glyph carries the total, the open state and the owner-only control', () => {
  const view = activityView(frameTwo(), environment, actions);
  const trigger = renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: 'owner', view, open: true, onOpenChange() {}}));
  // The drawn f2 markup labels this state "Server activity: 3 streams"; the notes define the name as
  // saying the state, so an available update is spoken too (frame 6's Update state does the same).
  assert.match(trigger, /aria-label="Server activity: 3 streams, update available"/);
  assert.match(trigger, /aria-haspopup="dialog"/); assert.match(trigger, /aria-expanded="true"/);
  assert.match(trigger, /data-open="true"/); assert.match(trigger, /data-streams="true"/);
  assert.match(trigger, />3</);
  for (const stranger of ['member', 'administrator'] as const) {
    assert.equal(renderToStaticMarkup(createElement(TopBarActivity, {viewerRole: stranger, view, open: true, onOpenChange() {}})), '');
    assert.equal(renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: stranger, view})), '');
  }
});

test('only real totals become progress: an unfinished discovery and an unknown task total stay wordless', () => {
  const document = frameTwo();
  const thin: OwnerActivitySummary = {...document, scans: [{libraryId: 'movies', libraryName: 'Movies', kind: 'movies', phase: 'discovering', analyzed: 0, discovered: 412}],
    tasks: {count: 1, items: [{id: 'metadata', kind: 'metadata_refresh', libraryName: 'Movies', state: 'running', phase: 'fetching', processed: 5}]}};
  const view = activityView(thin, environment, actions);
  assert.equal(view.tasks[0].total, undefined); assert.equal(view.tasks[0].progressText, undefined);
  assert.equal(view.scans[0].total, undefined);
  const html = renderToStaticMarkup(createElement(ActivityPanel, {viewerRole: 'owner', view}));
  assert.match(html, /files found so far/);
  assert.doesNotMatch(html, /class="percentage"/);
  assert.doesNotMatch(html, /NaN|undefined|0%/);
});
