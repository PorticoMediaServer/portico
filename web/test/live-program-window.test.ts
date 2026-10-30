import test from 'node:test';
import assert from 'node:assert/strict';
import type {GuideProgram} from '../../packages/client-core/src/guide/types.ts';
import {liveListPrograms, liveProgramWindow} from '../src/screens/live/program-window.ts';
import {parseLiveScreenSearch} from '../src/screens/live/route-search.ts';
const now = Date.parse('2026-10-23T20:05:00Z');
const programme = (id: string, start: number, end: number): GuideProgram => ({id, channelId: 'actual', title: id, start, end});

test('List reads the actual present after browsing a future guide window', () => {
  const start = now + 86_400_000, end = start + 7_200_000;
  assert.deepEqual(liveProgramWindow('guide', start, end, now), {start, end});
  const list = liveProgramWindow('list', start, end, now);
  assert.ok(list.start <= now && now < list.end);
  assert.ok(list.end - list.start <= 6 * 3_600_000);
  const rows = [programme('current-long', now - 2 * 3_600_000, now + 600_000), programme('next', now + 600_000, now + 1_200_000), programme('later', now + 1_200_000, now + 1_800_000)];
  assert.deepEqual(liveListPrograms(rows, now), rows);
  assert.deepEqual(liveListPrograms(rows, now + 600_000), [rows[1], rows[2], undefined]);
});

test('A genuine schedule gap never promotes the next airing into On now; search respects the producer limit', () => {
  const next = programme('next', now + 600_000, now + 1_200_000);
  assert.deepEqual(liveListPrograms([next], now), [undefined, next, undefined]);
  assert.deepEqual(liveListPrograms([], now), [undefined, undefined, undefined]);
  assert.equal(parseLiveScreenSearch({q: 'a'.repeat(120)}).q?.length, 120);
  assert.throws(() => parseLiveScreenSearch({q: 'a'.repeat(121)}));
});
