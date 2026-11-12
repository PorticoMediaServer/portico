import test from 'node:test';
import assert from 'node:assert/strict';
import {parseActivitySummary, ActivitySummaryContractError} from '../src/admin/activity-summary/parser.ts';

// The rebuilt server's GET /v1/admin/activity (server-next/internal/api/activity_summary.go).
const stamp = '2026-10-22T07:00:00.000000001Z';
const fence = 'a'.repeat(64);
const stream = (id = 'stream', route = 'converted') => ({id, itemId: 'itm_episode', title: 'Episode title', kind: 'episode', state: 'playing', positionMs: 1234, durationMs: 5000,
  username: 'sam', profile: 'Kids', deviceName: 'Living room TV', away: false, bitrateKbps: 8000, route,
  delivery: route === 'converted' ? {video: {action: 'transcode', reasons: ['quality_requested'], to: 'h264'}} : {}, startedAt: stamp});
const scan = () => ({kind: 'movie', libraryId: 'movies', libraryName: 'Movies', phase: 'analysis', discovered: 80, analyzed: 51, total: 80});
const task = (id = 'job_1') => ({id, kind: 'trickplay', libraryId: 'movies', libraryName: 'Movies', phase: 'running', state: 'running', processed: 3, total: 10});
const document = (): any => ({observedAt: stamp, serverName: 'Home server', streams: {count: 1, converting: 1, items: [stream()]}, scans: [scan()],
  tasks: {count: 1, items: [task()]}, alerts: [{code: 'library_unreachable', severity: 'major', major: true, params: {library: 'Movies'}, action: {kind: 'library-source', target: 'lib_1'}, count: 2},
    {code: 'update_available', severity: 'warning', major: false, params: {current: '1.0.0', latest: '1.1.0'}, action: {kind: 'update', target: ''}}], lastScanAt: stamp});
function rejects(change: (raw: any) => void, why: string): void { const raw = document(); change(raw); assert.throws(() => parseActivitySummary(raw), ActivitySummaryContractError, why); }

test('a busy summary keeps the server\'s streams in its order, its counts, scans, tasks, alerts and last scan', () => {
  const out = parseActivitySummary(document());
  assert.equal(out.serverName, 'Home server');
  assert.deepEqual([out.streams.count, out.streams.converting, out.streams.items[0]!.route, out.streams.items[0]!.profile], [1, 1, 'converted', 'Kids']);
  assert.deepEqual([out.scans[0]!.analyzed, out.tasks.count, out.tasks.items[0]!.total, out.lastScanAt], [51, 1, 10, stamp]);
  assert.deepEqual(out.alerts.map(alert => alert.code), ['library_unreachable', 'update_available']);
  assert.equal(out.alerts[0]!.params.subject, 'Movies', 'the library an alert is about is its subject, as on the Dashboard');
});

test('a quiet summary, before the first scan, has nothing running and no last scan', () => {
  const raw = {...document(), streams: {count: 0, converting: 0, items: []}, scans: [], tasks: {count: 0, items: []}, alerts: []};
  delete raw.lastScanAt;
  const out = parseActivitySummary(raw);
  assert.deepEqual([out.streams.count, out.scans.length, out.tasks.count, out.alerts.length, 'lastScanAt' in out], [0, 0, 0, 0, false]);
});

test('three streams of more: the rest are counts only, and the conversions among them can be worked out', () => {
  const raw = document();
  raw.streams = {count: 7, converting: 4, items: [stream('a'), stream('b'), stream('c', 'original')]};
  const out = parseActivitySummary(raw);
  assert.deepEqual([out.streams.count, out.streams.items.length, out.streams.converting - out.streams.items.filter(item => item.route === 'converted').length], [7, 3, 2]);
});

test('counts that disagree with the streams shown, a repeated stream, and a malformed stream are refused', () => {
  rejects(raw => {raw.streams.count = 2;}, 'two streams with one shown');
  rejects(raw => {raw.streams = {count: 4, converting: 0, items: [stream('a'), stream('b')]};}, 'fewer than three shown of four');
  rejects(raw => {raw.streams.converting = 2;}, 'more conversions than streams');
  rejects(raw => {raw.streams = {count: 2, converting: 0, items: [stream('a'), stream('b')]};}, 'fewer conversions than the converted streams shown');
  rejects(raw => {raw.streams = {count: 2, converting: 2, items: [stream('a'), stream('a')]};}, 'a repeated stream');
  rejects(raw => {raw.streams.items[0].route = 'teleported';}, 'an unknown route');
  rejects(raw => {delete raw.streams.items[0].username;}, 'a stream with no person');
});

test('a summary missing a part, with a malformed time, or with a malformed scan, task or alert is refused', () => {
  rejects(raw => {delete raw.alerts;}, 'no alerts');
  rejects(raw => {raw.observedAt = 'yesterday';}, 'a malformed time');
  rejects(raw => {raw.lastScanAt = 'soon';}, 'a malformed last scan');
  rejects(raw => {raw.scans[0].discovered = -1;}, 'a negative count');
  rejects(raw => {raw.tasks.items = [task('a'), task('b'), task('c'), task('d')]; raw.tasks.count = 4;}, 'more than three tasks shown');
  rejects(raw => {raw.alerts[0].major = 'yes';}, 'an alert that is not plainly major or not');
});

test('OW-2: the Dashboard reads a CD-quality music session too, and CD quality stays as strict as original', async () => {
  const {parseDashboardSessions} = await import('../src/admin/activity-summary/parser.ts');
  const song = {id: 'cd', protocol: 'v1', user: {id: 'account', name: 'account name'}, profile: {id: 'profile', name: 'profile name'}, device: {id: 'device', name: 'device name'},
    kind: 'audio', state: 'playing', positionMs: 1000, decision: {}, location: 'home', startedAt: stamp,
    item: {id: 'song', kind: 'song', title: 'Song name', artist: 'Artist name', album: 'Album name', art: []}, quality: {mode: 'cd-quality'}};
  const page = (item: unknown) => ({scope: {serverId: 'server', viewerFence: fence}, observedAt: stamp, items: [item], page: {limit: 50, total: 1, start: 0, revision: 'r'},
    totals: {streams: 1, paused: 0, converting: 0, bandwidthKbps: 0, people: 1, atHome: 1, away: 0}});
  assert.equal((parseDashboardSessions(page(song)).items[0] as {quality?: {mode?: string}}).quality?.mode, 'cd-quality');
  for (const quality of [{mode: 'cd-quality', id: '1080p'}, {mode: 'cd-quality', maxAudioBitrateKbps: 320}, {mode: 'lossless'}])
    assert.throws(() => parseDashboardSessions(page({...song, quality})), ActivitySummaryContractError, JSON.stringify(quality));
});
