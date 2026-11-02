import test from 'node:test';
import assert from 'node:assert/strict';
import {clampGuideStart, guideDayMenu, guideNowStart, guideRange} from '../src/guide/range.ts';
import {HOUR_MS, localDayStart} from '../src/guide/time.ts';

const zone = 'Europe/London', now = Date.parse('2026-11-02T16:05:00Z'), span = 3 * HOUR_MS;
const source = (start: string, end: string, guideDays = 0) => ({availableStart: start, availableEnd: end, guideDays});

test('the range is the union of finite sources; a source with no guide yet adds nothing (LT-6)', () => {
  const range = guideRange([source('2026-11-01T00:00:00Z', '2026-11-05T00:00:00Z', 3), source('', '', 0), source('2026-10-31T00:00:00Z', '2026-11-04T00:00:00Z')]);
  assert.deepEqual(range, {start: Date.parse('2026-10-31T00:00:00Z'), end: Date.parse('2026-11-05T00:00:00Z'), days: 3});
  assert.deepEqual(guideRange([source('', '')]), {});
});

test('Now puts now at 25% of the span, the drawn #live-guide position (LT-5)', () => {
  const start = guideNowStart(now, span, {});
  assert.equal((now - start) / span, 0.25);
  // Inside a range that starts at now, the view can't open before the range, but now stays visible.
  const late = guideNowStart(now, span, {start: now, end: now + 10 * HOUR_MS});
  assert.ok(late <= now && now < late + span);
});

test('Earlier and Later stop at the published range, and Now stays reachable past a stale end (LT-6)', () => {
  const range = {start: now - 24 * HOUR_MS, end: now + 48 * HOUR_MS};
  assert.equal(clampGuideStart(now - 100 * HOUR_MS, span, now, range), range.start);
  assert.equal(clampGuideStart(now + 100 * HOUR_MS, span, now, range), range.end - span);
  const stale = {start: now - 48 * HOUR_MS, end: now - 24 * HOUR_MS};
  const here = guideNowStart(now, span, stale);
  assert.ok(here <= now && now < here + span, 'a guide that ended yesterday still opens on now');
  assert.equal(clampGuideStart(now + 5 * HOUR_MS, span, now, stale), here, 'and Later stops at now\'s span');
});

test('the day menu always has Today, reaches the range end or the published day count, and keeps the viewed day', () => {
  const today = localDayStart(now, zone);
  assert.deepEqual(guideDayMenu(now, now, zone, {}), [today], 'no horizon: Today only, never an empty menu');
  assert.equal(guideDayMenu(now, now, zone, {days: 3}).length, 4, 'the sources\' published day count when no end is finite');
  assert.equal(guideDayMenu(now, now, zone, {end: Date.parse('2026-11-05T12:00:00Z')}).length, 4);
  const yesterday = now - 20 * HOUR_MS, menu = guideDayMenu(now, yesterday, zone, {end: Date.parse('2026-11-04T00:00:00Z')});
  assert.equal(menu[0], localDayStart(yesterday, zone), 'stepping into yesterday keeps its day in the menu');
  assert.ok(menu.includes(today));
});
