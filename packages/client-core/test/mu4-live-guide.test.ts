import test from 'node:test';
import assert from 'node:assert/strict';
import {formatBehindSec, isBehindLive, behindLiveSec, programmeRangeLabel} from '../src/presentation/live-label.ts';
import {nowNextProgrammes, miniGuideNeighbors, sortChannelsByNumber} from '../src/presentation/mini-guide.ts';

test('MU4 FEAT-07: behind-live durations read as M:SS', () => {
  assert.equal(formatBehindSec(135), '2:15');
  assert.equal(formatBehindSec(5), '0:05');
  assert.equal(formatBehindSec(3661), '1:01:01');
  assert.equal(formatBehindSec(-10), '0:00');
});

test('MU4 FEAT-07: behind-live grace and edge arithmetic', () => {
  assert.equal(isBehindLive(2), false);
  assert.equal(isBehindLive(4), true);
  assert.equal(behindLiveSec(100, 90), 10);
  assert.equal(behindLiveSec(100, 110), 0);
  assert.equal(behindLiveSec(NaN, 10), 0);
});

test('MU4 FEAT-07: the live label reads "7:30 – 8:30 PM" and "2:15 behind live"', () => {
  // Wall-clock comes from the UI's locale formatter (stubbed here to the en-US shape).
  const enUS = (ms: number, meridiem: boolean) => {
    const full = ms === 19 * 3600_000 + 30 * 60_000 ? '7:30 PM' : '8:30 PM';
    return meridiem ? full : full.replace(' PM', '');
  };
  const range = programmeRangeLabel(19 * 3600_000 + 30 * 60_000, 20 * 3600_000 + 30 * 60_000, enUS);
  assert.equal(range, '7:30 – 8:30 PM');
  // 24-hour locales show both times in full.
  const fr = (ms: number) => (ms < 20 * 3600_000 ? '19:30' : '20:30');
  const start = 19 * 3600_000 + 30 * 60_000, end = 20 * 3600_000 + 30 * 60_000;
  assert.equal(programmeRangeLabel(start, end, (ms, m) => fr(ms)), '19:30 – 20:30');
  assert.equal(`${formatBehindSec(135)} behind live`, '2:15 behind live');
  assert.equal(programmeRangeLabel(NaN, 0, enUS), '');
});

test('MU4 FEAT-07: now/next programmes from the loaded programmes', () => {
  const programmes = [
    {id: 'a', title: 'News', startMs: 0, endMs: 100},
    {id: 'b', title: 'Film', startMs: 100, endMs: 200},
    {id: 'c', title: 'Late', startMs: 200, endMs: 300},
  ];
  assert.deepEqual(nowNextProgrammes(programmes, 150), {now: programmes[1], next: programmes[2]});
  assert.deepEqual(nowNextProgrammes(programmes, 500), {now: null, next: null});
  assert.deepEqual(nowNextProgrammes([], 50), {now: null, next: null});
  assert.deepEqual(nowNextProgrammes(null, 50), {now: null, next: null});
});

test('MU4 FEAT-07: mini-guide neighbours wrap at the ends of the loaded page', () => {
  const page = [{id: 'a'}, {id: 'b'}, {id: 'c'}];
  assert.deepEqual(miniGuideNeighbors(page, 'a'), {previous: page[2], current: page[0], next: page[1]});
  assert.deepEqual(miniGuideNeighbors(page, 'c'), {previous: page[1], current: page[2], next: page[0]});
  assert.deepEqual(miniGuideNeighbors(page, 'b'), {previous: page[0], current: page[1], next: page[2]});
  // Unknown id reads as the head; a single row fills all three slots.
  assert.deepEqual(miniGuideNeighbors(page, 'zzz'), {previous: page[2], current: page[0], next: page[1]});
  assert.deepEqual(miniGuideNeighbors([{id: 'a'}], 'a'), {previous: {id: 'a'}, current: {id: 'a'}, next: {id: 'a'}});
  assert.deepEqual(miniGuideNeighbors([], 'a'), {previous: null, current: null, next: null});
});

test('MU4 FEAT-07: a tune history orders by channel number', () => {
  const rows = [{id: 'c', number: '102'}, {id: 'a', number: '7'}, {id: 'b', number: '21'}];
  assert.deepEqual(sortChannelsByNumber(rows).map(r => r.id), ['a', 'b', 'c']);
});
