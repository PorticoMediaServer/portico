import {test} from 'node:test';
import assert from 'node:assert/strict';
import {chartPaths, loadTileValue, orderedSessions, quietestHours, sessionTitle, sessionStopTitle, sessionTracks} from '../src/screens/dashboard/model.ts';
import type {DashboardSession} from '../src/screens/dashboard/bindings.ts';
const base: DashboardSession = {id: 'session', user: 'account', profile: 'Kids', device: 'TV', title: 'Actual title', kind: 'vod', state: 'playing', positionMs: 1, location: 'local', startedAt: '2026-10-23T01:00:00Z', decision: {}};
test('current programme, show and book titles retain their actual meaning in the card and Stop', () => {
  const live = {...base, live: true, title: 'Actual channel', channel: {name: 'Actual channel', number: '101'}, programme: {title: 'Actual programme', endsAt: '2026-10-23T02:00:00Z'}};
  assert.equal(sessionTitle(live), 'Actual programme'); assert.equal(sessionStopTitle(live), 'Actual programme');
  assert.equal(sessionTitle({...live, programme: undefined}), ''); assert.equal(sessionStopTitle({...live, programme: undefined}), 'Actual channel');
  assert.equal(sessionTitle({...base, title: 'Actual episode', item: {id: 'item', kind: 'episode', title: 'Actual episode', art: [], showTitle: 'Actual show'}}), 'Actual show');
  assert.equal(sessionTitle({...base, item: {id: 'item', kind: 'audiobook_file', title: 'Actual chapter', art: [], bookTitle: 'Actual book'}}), 'Actual book');
});
test('source/delivery actions use committed modes and unknown hardware remains absent', () => {
  const session = {...base, media: {video: {id: 'v', codec: 'hevc', height: 2160}}, delivery: {streams: [{kind: 'video', inputCodec: 'hevc', outputCodec: 'h264', action: 'convert'}]}};
  assert.deepEqual(sessionTracks(session), [{reportedAction: 'convert', route: 'converted', kind: 'video', source: '4K HEVC', action: 'transcode', output: undefined, hardware: false}]);
  assert.equal(sessionTracks({...session, media: {...session.media, videoHardware: true}})[0].hardware, true);
  assert.equal(sessionTracks({...session, delivery: {streams: [{kind: 'video', inputCodec: 'hevc', outputCodec: 'hevc', action: 'original'}]}})[0].action, 'direct');
});
test('problems outrank conversion and newest start, without changing input', () => {
  const old = {...base, id: 'old'}, converted = {...base, id: 'convert', decision: {audio: {action: 'transcode', reasons: []}}}, problem = {...base, id: 'problem', problem: 'buffering' as const};
  const input = [old, converted, problem]; assert.deepEqual(orderedSessions(input).map(s => s.id), ['problem', 'convert', 'old']); assert.equal(input[0].id, 'old');
});
test('telemetry gaps do not become zeroes or connected history', () => {
  const points = [{t: 0, v: 1}, {t: 2, v: 2}, {t: 4, v: 3}, {t: 30, v: 4}]; assert.equal(chartPaths(points, 0, 30, 5).length, 2); assert.match(chartPaths(points.slice(0, 2), 0, 30, 5, true)[0], /H.*V/); assert.deepEqual(chartPaths([], 0, 30, 5), []);
});
test('quiet window uses actual Monday-first grid and empty has no suggestion', () => {
  const grid = Array.from({length: 7}, () => Array(24).fill(10)); for (const day of grid) day.splice(4, 3, 0, 0, 0); assert.equal(quietestHours(grid), 4); assert.equal(quietestHours(grid.map(day => day.map(() => 0))), undefined);
});

test('reported bucket identities keep sparse and unequal calendar points as gaps', () => {
  const geom = {left: 64, top: 8, width: 840, height: 216};
  assert.equal(chartPaths([{t: 0, v: 2}, {t: 30, v: 4}], 0, 30, 5, false, geom, [0, 10, 20, 30]).length, 2);
  assert.equal(chartPaths([{t: 0, v: 2}, {t: 29, v: 4}, {t: 60, v: 3}], 0, 60, 5, false, geom, [0, 29, 60]).length, 1);
});
test('current tiles require their actual primary metric or a complete simultaneous pair', () => {
  const reading = {series: {cpu: [{t: 1, v: 40}], processCPU: [], homeMbps: [{t: 1, v: 8}], awayMbps: [{t: 2, v: 3}]}, status: {cpu: {status: 'available'}, processCPU: {status: 'unavailable'}, homeMbps: {status: 'available'}, awayMbps: {status: 'available'}}} as never;
  assert.equal(loadTileValue(reading, 'processor'), undefined);
  assert.equal(loadTileValue(reading, 'bandwidth'), undefined);
  const complete = {series: {homeMbps: [{t: 2, v: 8}], awayMbps: [{t: 2, v: 3}]}, status: {homeMbps: {status: 'available'}, awayMbps: {status: 'available'}}} as never;
  assert.equal(loadTileValue(complete, 'bandwidth'), 11);
});
