import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';
import type {Recording} from '@core/dvr.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {RecordingsController, RecordingsSnapshot} from '../src/screens/live/recordings-controller.ts';

const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module' as const, source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module' as const, source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {HistoryView, RecordedView, UpcomingView, dayOf, groupUpcomingByDay} = await import('../src/screens/live/RecordingsScreen.tsx');
test.after(() => loader.deregister());

function makeRecording(id: string, start: string, end: string, state: Recording['state'] = 'scheduled'): Recording {
  return {
    id,
    revision: 1,
    occurrence: {sourceId: 's', channelId: 'c', generation: 'g', programmeId: id},
    programme: {id, channelId: 'c', title: `Show ${id}`, start, end, lineage: 'interval-only', seriesId: '', episodeId: '', newEvidence: 'unknown', description: ''},
    ruleId: '',
    options: {beforeSeconds: 0, afterSeconds: 0, priority: 0, retentionDays: 0, episodeLimit: 0},
    start,
    end,
    state,
    reason: '',
    keep: false,
    itemId: '',
    libraryId: '',
    coverageStart: start,
    coverageEnd: end,
    bytes: 0,
    conflicts: [],
  } as Recording;
}

test('groupUpcomingByDay and dayOf format dates consistently in the requested timezone and never collapse future days', () => {
  // 2026-10-22T01:30:00Z is still Wednesday Oct 21 at 9:30 PM in America/New_York, but Thursday Oct 22 in UTC.
  const now = Date.parse('2026-10-21T22:00:00Z'); // Wed Oct 21 6:00 PM EDT / 10:00 PM UTC
  const lateWedEdt = makeRecording('r1', '2026-10-22T01:30:00Z', '2026-10-22T02:30:00Z');
  const thu = makeRecording('r2', '2026-10-22T23:00:00Z', '2026-10-23T00:00:00Z');
  const sat = makeRecording('r3', '2026-10-24T20:00:00Z', '2026-10-24T21:00:00Z');
  const nextTue = makeRecording('r4', '2026-10-27T20:00:00Z', '2026-10-27T21:00:00Z');

  assert.equal(dayOf(lateWedEdt.start, now, 'America/New_York'), 'Today');
  assert.notEqual(dayOf(lateWedEdt.start, now, 'UTC'), 'Today');

  const nyGroups = groupUpcomingByDay([lateWedEdt, thu, sat, nextTue], now, 'America/New_York');
  assert.equal(nyGroups.length, 4, 'every distinct calendar day gets its own section');
  assert.equal(nyGroups[0]!.heading, 'Later today');
  assert.deepEqual(nyGroups[0]!.recordings.map(r => r.id), ['r1']);
  assert.equal(nyGroups[1]!.heading, 'Tomorrow');
  assert.deepEqual(nyGroups[1]!.recordings.map(r => r.id), ['r2']);
  assert.equal(nyGroups[2]!.heading, 'Saturday');
  assert.deepEqual(nyGroups[2]!.recordings.map(r => r.id), ['r3']);
  assert.equal(nyGroups[3]!.heading, 'Tuesday');
  assert.deepEqual(nyGroups[3]!.recordings.map(r => r.id), ['r4']);

  // In UTC, r1 and r2 are both Tomorrow (Thu Oct 22), while r3 and r4 stay separate future days.
  const utcGroups = groupUpcomingByDay([lateWedEdt, thu, sat, nextTue], now, 'UTC');
  assert.equal(utcGroups.length, 3);
  assert.equal(utcGroups[0]!.heading, 'Tomorrow');
  assert.deepEqual(utcGroups[0]!.recordings.map(r => r.id), ['r1', 'r2']);
});

test('UpcomingView, RecordedView and HistoryView never render the empty state when snapshot.error is present', () => {
  const baseSnapshot: RecordingsSnapshot = {
    query: {view: 'upcoming'},
    phase: 'ready',
    page: {recordings: [], rules: [], nextCursor: '', revision: 0, captureAvailable: true, captureUnavailableReason: '', deletionAvailable: true, usage: {bytes: 0, pendingDeleteBytes: 0, recordings: 0}},
    detail: null,
    storage: null,
    summary: null,
    groups: {groups: [], nextCursor: '', revision: 0},
    groupsSort: 'recent',
    paging: {busy: false, error: '', previous: false, next: false},
    accessDenied: false,
    error: 'Server unavailable',
    mutating: false,
    commandError: '',
  };
  const fakeContext = {isCurrent: () => true} as unknown as ScreenContext;
  const fakeController = {} as unknown as RecordingsController;

  const upcomingErr = renderToStaticMarkup(createElement(UpcomingView, {context: fakeContext, controller: fakeController, snapshot: baseSnapshot, now: Date.now(), onResolve: () => {}}));
  assert.equal(upcomingErr.includes('Nothing scheduled'), false);

  const recordedErr = renderToStaticMarkup(createElement(RecordedView, {context: fakeContext, controller: fakeController, current: () => true, snapshot: baseSnapshot}));
  assert.equal(recordedErr.includes('No recordings yet'), false);

  const historyErr = renderToStaticMarkup(createElement(HistoryView, {controller: fakeController, snapshot: baseSnapshot, now: Date.now()}));
  assert.equal(historyErr.includes('No recordings yet'), false);

  // Without error, empty state renders normally.
  const okSnapshot = {...baseSnapshot, error: ''};
  const upcomingEmpty = renderToStaticMarkup(createElement(UpcomingView, {context: fakeContext, controller: fakeController, snapshot: okSnapshot, now: Date.now(), onResolve: () => {}}));
  assert.equal(upcomingEmpty.includes('Nothing scheduled'), true);
});
