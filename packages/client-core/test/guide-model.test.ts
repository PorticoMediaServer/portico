import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DAY_MS, HOUR_MS, MINUTE_MS, SLOT_MS, TIERS, blocksCovering, clampSpan, dayStarts, focusTarget, focusTimeOf, layoutRow, localDayStart, neighbor,
  nextChange, normalizePrograms, nowNext, nowX, pxPerMsFor, rulerTicks, spanAt, type GuideProgram,
} from '../src/guide/index.ts';
import {demoChannels, demoPrograms, demoSchedule} from '../src/guide/testing/fixture.ts';

const at = (iso: string) => Date.parse(iso);
const prog = (id: string, start: string, end: string, title = id): GuideProgram => ({id, channelId: 'c', title, start: at(start), end: at(end)});

test('time: blocks, now line, spans and ruler ticks', () => {
  assert.deepEqual(blocksCovering(at('2026-09-22T19:10:00Z'), at('2026-09-22T22:00:00Z')).map(t => new Date(t).toISOString()), ['2026-09-22T18:00:00.000Z', '2026-09-22T21:00:00.000Z']);
  const px = pxPerMsFor(240);
  assert.equal(px * SLOT_MS, 240);
  const view = {start: at('2026-09-22T19:00:00Z'), end: at('2026-09-22T22:00:00Z')};
  assert.equal(nowX(at('2026-09-22T19:30:00Z'), view.start, view.end, px), 240);
  assert.equal(nowX(at('2026-09-22T23:00:00Z'), view.start, view.end, px), undefined);
  assert.deepEqual(spanAt(at('2026-09-22T20:00:00Z'), 2 * HOUR_MS), {start: at('2026-09-22T19:30:00Z'), end: at('2026-09-22T21:30:00Z')});
  assert.deepEqual(clampSpan(at('2026-09-22T23:00:00Z'), 2 * HOUR_MS, view.start, view.end), {start: at('2026-09-22T20:00:00Z'), end: view.end});
  const ticks = rulerTicks(view.start, view.end, px, 'America/New_York');
  assert.equal(ticks.length, 6);
  assert.deepEqual(ticks.map(t => t.kind), ['hour', 'half', 'hour', 'half', 'hour', 'half']);
  // 04:00Z is local midnight in New York (EDT): a day tick.
  assert.equal(rulerTicks(at('2026-09-23T03:30:00Z'), at('2026-09-23T05:00:00Z'), px, 'America/New_York')[1]!.kind, 'day');
});

test('time: local day starts survive DST (23 h and 25 h days)', () => {
  const ny = 'America/New_York';
  assert.equal(localDayStart(at('2026-09-22T15:00:00Z'), ny), at('2026-09-22T04:00:00Z'));
  const spring = dayStarts(at('2026-03-07T12:00:00Z'), at('2026-03-10T00:00:00Z'), ny);
  assert.deepEqual(spring.map(t => new Date(t).toISOString()), ['2026-03-07T05:00:00.000Z', '2026-03-08T05:00:00.000Z', '2026-03-09T04:00:00.000Z']);
  assert.equal(spring[2]! - spring[1]!, 23 * HOUR_MS, 'spring-forward day is 23 hours');
  const fall = dayStarts(at('2026-10-31T12:00:00Z'), at('2026-11-03T00:00:00Z'), ny);
  assert.equal(fall[2]! - fall[1]!, 25 * HOUR_MS, 'fall-back day is 25 hours');
  // The repeated 1:30 AM: two ticks 60 real minutes apart, both half-hour ticks.
  const ticks = rulerTicks(at('2026-11-01T05:00:00Z'), at('2026-11-01T07:00:00Z'), pxPerMsFor(240), ny);
  assert.equal(ticks.length, 4);
  assert.deepEqual(ticks.map(t => t.kind), ['hour', 'half', 'hour', 'half']);
  // A zone that isn't whole-hour offset still finds midnight.
  assert.equal(localDayStart(at('2026-09-22T20:00:00Z'), 'Asia/Kolkata'), at('2026-09-22T18:30:00Z'));
});

test('layout: positions, clipping, gaps, tiers and states', () => {
  const px = pxPerMsFor(240);
  const programs = normalizePrograms([
    prog('movie', '2026-09-22T17:30:00Z', '2026-09-22T19:48:00Z'),
    prog('short', '2026-09-22T19:48:00Z', '2026-09-22T19:55:00Z'),
    prog('news', '2026-09-22T20:00:00Z', '2026-09-22T21:00:00Z'),
    prog('late', '2026-09-22T21:30:00Z', '2026-09-22T23:30:00Z'),
  ]);
  const cells = layoutRow(programs, {viewStart: at('2026-09-22T19:00:00Z'), viewEnd: at('2026-09-22T22:00:00Z'), pxPerMs: px, now: at('2026-09-22T20:15:00Z')});
  assert.deepEqual(cells.map(c => c.kind === 'gap' ? 'gap' : c.program!.id), ['movie', 'short', 'gap', 'news', 'gap', 'late']);
  const [movie, short, gap, news, , late] = cells;
  assert.equal(movie!.x, 0);
  assert.equal(movie!.clippedStart, true, 'began before the span: ‹ and a pinned title');
  assert.equal(Math.round(movie!.width), 48 * 8 - 2);
  assert.equal(movie!.state, 'past');
  assert.equal(short!.tier, 'title', '7 minutes at 240 px/30 min is 54 px');
  assert.equal(gap!.width, 5 * 8 - 2);
  assert.equal(gap!.tier, 'title');
  assert.equal(news!.state, 'now');
  assert.equal(news!.tier, 'full');
  assert.equal(late!.clippedEnd, true);
  assert.equal(late!.state, 'future');
  // TV thresholds are larger.
  assert.equal(layoutRow(programs, {viewStart: at('2026-09-22T19:00:00Z'), viewEnd: at('2026-09-22T22:00:00Z'), pxPerMs: pxPerMsFor(360), now: 0, tiers: TIERS.tv, gapPx: 4})[1]!.tier, 'title');
});

test('layout: overlaps are trimmed, duplicates dropped, a row without guide is one gap', () => {
  const programs = normalizePrograms([
    prog('a', '2026-09-22T19:00:00Z', '2026-09-22T20:00:00Z'),
    prog('b', '2026-09-22T19:45:00Z', '2026-09-22T20:30:00Z'),
    prog('a', '2026-09-22T19:00:00Z', '2026-09-22T20:00:00Z'),
    prog('c', '2026-09-22T19:50:00Z', '2026-09-22T20:10:00Z'),
  ]);
  assert.deepEqual(programs.map(p => [p.id, new Date(p.start).toISOString().slice(11, 16)]), [['a', '19:00'], ['b', '20:00']], 'b starts where a ends; c is swallowed');
  const empty = layoutRow([], {viewStart: at('2026-09-22T19:00:00Z'), viewEnd: at('2026-09-22T21:00:00Z'), pxPerMs: pxPerMsFor(240), now: 0});
  assert.equal(empty.length, 1);
  assert.equal(empty[0]!.kind, 'gap');
});

test('focus: vertical moves keep the focus time across a long movie; left/right stop at the ends', () => {
  const px = pxPerMsFor(360), view = {viewStart: at('2026-09-22T19:00:00Z'), viewEnd: at('2026-09-22T21:00:00Z'), pxPerMs: px, now: 0};
  const rowA = layoutRow(normalizePrograms([prog('a1', '2026-09-22T19:00:00Z', '2026-09-22T19:30:00Z'), prog('a2', '2026-09-22T19:30:00Z', '2026-09-22T20:00:00Z'), prog('a3', '2026-09-22T20:00:00Z', '2026-09-22T21:00:00Z')]), view);
  const rowMovie = layoutRow(normalizePrograms([prog('m', '2026-09-22T18:00:00Z', '2026-09-22T21:30:00Z')]), view);
  const rowB = layoutRow(normalizePrograms([prog('b1', '2026-09-22T19:00:00Z', '2026-09-22T19:30:00Z'), prog('b2', '2026-09-22T19:30:00Z', '2026-09-22T20:30:00Z')]), view);
  const focusTime = focusTimeOf(rowA[1]!, view.viewStart, view.viewEnd); // a2 at 19:30
  assert.equal(rowMovie[focusTarget(rowMovie, focusTime)]!.program!.id, 'm');
  // Moving on past the movie keeps 19:30 (the grid keeps the focus time, not the movie's clamped start).
  assert.equal(rowB[focusTarget(rowB, focusTime)]!.program!.id, 'b2');
  assert.equal(neighbor(rowA, 0, -1), -1);
  assert.equal(neighbor(rowA, 2, 1), -1);
  assert.equal(neighbor(rowA, 1, 1), 2);
});

test('now/next: in a program, in a gap, at the end; the next change is a boundary', () => {
  const programs = normalizePrograms([prog('a', '2026-09-22T19:00:00Z', '2026-09-22T20:00:00Z'), prog('b', '2026-09-22T20:30:00Z', '2026-09-22T21:00:00Z')]);
  const inA = nowNext(programs, at('2026-09-22T19:15:00Z'));
  assert.equal(inA.now!.id, 'a'); assert.equal(inA.next!.id, 'b'); assert.equal(inA.progress, 0.25); assert.equal(inA.remainingMs, 45 * MINUTE_MS);
  const gap = nowNext(programs, at('2026-09-22T20:10:00Z'));
  assert.equal(gap.gap, true); assert.equal(gap.now, undefined); assert.equal(gap.next!.id, 'b'); assert.equal(gap.remainingMs, 20 * MINUTE_MS);
  const after = nowNext(programs, at('2026-09-22T22:00:00Z'));
  assert.equal(after.gap, true); assert.equal(after.next, undefined);
  assert.equal(nextChange(programs, at('2026-09-22T19:15:00Z')), at('2026-09-22T20:00:00Z'));
  assert.equal(nextChange(programs, at('2026-09-22T20:10:00Z')), at('2026-09-22T20:30:00Z'));
  assert.equal(nextChange(programs, at('2026-09-22T22:00:00Z')), undefined);
});

test('demo fixture shapes: 14 channels, a no-guide row, gaps, long titles, contiguous days', () => {
  assert.equal(demoChannels.length, 14);
  assert.equal(new Set(demoChannels.map(c => c.group)).size, 9);
  const day = at('2026-09-22T00:00:00Z');
  const news = demoSchedule('north.news', day);
  assert.equal(news[0]!.start, day);
  assert.equal(news.at(-1)!.end, day + DAY_MS);
  assert.ok(news.every((p, i) => i === 0 || p.start === news[i - 1]!.end), 'a full channel has no holes');
  assert.deepEqual(demoSchedule('north.news', day), news, 'deterministic');
  assert.deepEqual(demoSchedule('quiet.noguide', day), []);
  const gaps = demoSchedule('patchy.gaps', day);
  assert.ok(gaps.some((p, i) => i > 0 && p.start > gaps[i - 1]!.end), 'the gaps channel has holes');
  const view = {viewStart: day + 18 * HOUR_MS, viewEnd: day + 24 * HOUR_MS, pxPerMs: pxPerMsFor(240), now: day + 20 * HOUR_MS};
  const gapCells = layoutRow(normalizePrograms(demoPrograms('patchy.gaps', day, day + DAY_MS)), {...view, viewStart: day, viewEnd: day + DAY_MS}).filter(c => c.kind === 'gap');
  assert.ok(gapCells.length >= 1, 'holes become "No information" cells');
  const noGuide = layoutRow([], view);
  assert.equal(noGuide.length, 1);
  // Nine days of the fixture lay out (one back, seven ahead, today).
  for (let d = -1; d <= 7; d++) {
    const start = day + d * DAY_MS;
    for (const c of demoChannels) {
      const cells = layoutRow(normalizePrograms(demoPrograms(c.id, start, start + DAY_MS)), {viewStart: start, viewEnd: start + DAY_MS, pxPerMs: pxPerMsFor(240), now: day});
      const covered = cells.reduce((sum, cell) => sum + (Math.min(cell.end, start + DAY_MS) - Math.max(cell.start, start)), 0);
      assert.equal(covered, DAY_MS, `${c.id} day ${d} covers the whole span with programs and gaps`);
    }
  }
  const long = demoSchedule('comet.comedy', day).concat(demoSchedule('comet.comedy', day + DAY_MS)).find(p => p.title.startsWith('The Inheritance'));
  assert.ok(long, 'the long title appears within two days');
  const cell = layoutRow([long!], {viewStart: long!.start, viewEnd: long!.end, pxPerMs: pxPerMsFor(180), now: 0})[0]!;
  assert.equal(cell.tier, 'full', 'a 45-minute cell on phone landscape still gets the full tier; the title truncates in the view');
});

test('layout of a 12-row × 3-hour screen is fast', () => {
  const day = at('2026-09-22T00:00:00Z');
  const rows = demoChannels.slice(0, 12).map(c => normalizePrograms(demoPrograms(c.id, day, day + 3 * DAY_MS)));
  const view = {viewStart: day + 30 * HOUR_MS, viewEnd: day + 33 * HOUR_MS, pxPerMs: pxPerMsFor(240), now: day + 31 * HOUR_MS};
  const t0 = performance.now();
  for (let i = 0; i < 100; i++) for (const r of rows) layoutRow(r, view);
  assert.ok((performance.now() - t0) / 100 < 2, 'under 2 ms per screen');
});

test('quarter-hour offset zones label local hours and midnight',()=>{
 const start=Date.parse('2026-10-12T18:00:00Z'),end=start+3*60*60*1000;
 const ticks=rulerTicks(start,end,pxPerMsFor(240),'Asia/Kathmandu');
 assert.ok(ticks.some(v=>v.kind==='day'));assert.ok(ticks.some(v=>v.kind==='hour'));
 assert.ok(ticks.every(v=>new Date(v.atMs).getUTCMinutes()===15||new Date(v.atMs).getUTCMinutes()===45));
});
