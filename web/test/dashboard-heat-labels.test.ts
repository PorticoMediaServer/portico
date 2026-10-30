import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import {fmtHrs, formatHourOrdinal, formatQuietHours, heatMapLabels} from '../src/screens/dashboard/format.ts';
import {hours, quietestHours} from '../src/screens/dashboard/model.ts';
import {Analytics} from '../src/screens/dashboard/Analytics.tsx';

/** PERF-WEB-003 / web-render-006: the 7 × 24 heat map rebuilt one UTC weekday
 * `Intl.DateTimeFormat` per cell on every pointer-state render. The table is one
 * constructor per shape per locale now; these cases pin that cost and the exact text. */

const locales = ['en-US', 'en-GB', 'fr', 'ar', 'ja'] as const;
const hourColumns = [0, 3, 6, 9, 12, 15, 18, 21];

/** The exact per-cell expressions the fix replaced, kept as the oracle. */
const weekdayText = (locale: string, width: 'short' | 'long', day: number) => new Intl.DateTimeFormat(locale, {weekday: width, timeZone: 'UTC'}).format(Date.UTC(2026, 0, 5 + day));
const hourText = (locale: string, hour: number) => new Intl.DateTimeFormat(locale, {hour: 'numeric', timeZone: 'UTC'}).format(Date.UTC(2026, 0, 1, hour));

const signature = (options?: Intl.DateTimeFormatOptions) => options ? Object.keys(options).sort().map(name => `${name}=${String((options as Record<string, unknown>)[name])}`).join(';') : '';

/** Count every `Intl.DateTimeFormat` construction the callback performs. */
function counted<T>(run: () => T): {value: T; counts: Map<string, number>} {
  const Real = Intl.DateTimeFormat, counts = new Map<string, number>();
  function Counting(this: unknown, locales?: string | string[], options?: Intl.DateTimeFormatOptions) {
    const key = signature(options);
    counts.set(key, (counts.get(key) ?? 0) + 1);
    return new Real(locales as never, options);
  }
  Object.setPrototypeOf(Counting, Real);
  Counting.prototype = Real.prototype;
  Object.defineProperty(Intl, 'DateTimeFormat', {value: Counting, configurable: true, writable: true});
  try {return {value: run(), counts};} finally {Object.defineProperty(Intl, 'DateTimeFormat', {value: Real, configurable: true, writable: true});}
}
const countOf = (counts: Map<string, number>, options: Intl.DateTimeFormatOptions) => counts.get(signature(options)) ?? 0;

test('every weekday, hour, axis and tooltip label keeps the per-cell text in every locale', () => {
  for (const locale of locales) {
    const labels = heatMapLabels(locale);
    assert.equal(labels.weekdayShort.length, 7, `${locale} short weekdays`);
    assert.equal(labels.weekdayLong.length, 7, `${locale} long weekdays`);
    assert.equal(labels.hour.length, 24, `${locale} hour ordinals`);
    for (let day = 0; day < 7; day++) {
      assert.equal(labels.weekdayShort[day], weekdayText(locale, 'short', day), `${locale} column ${day}`);
      assert.equal(labels.weekdayLong[day], weekdayText(locale, 'long', day), `${locale} cell day ${day}`);
    }
    for (let hour = 0; hour < 24; hour++) assert.equal(labels.hour[hour], hourText(locale, hour), `${locale} hour ${hour}`);
    // The axis tick text and the floating tooltip read the same two entries.
    for (const hour of hourColumns) assert.equal(labels.hour[hour], hourText(locale, hour), `${locale} axis ${hour}`);
    for (let day = 0; day < 7; day++) for (let hour = 0; hour < 24; hour++) {
      const cell = `${weekdayText(locale, 'long', day)} ${hourText(locale, hour)}`;
      assert.equal(`${labels.weekdayLong[day]} ${labels.hour[hour]}`, cell, `${locale} tooltip ${day}/${hour}`);
      assert.equal(`${labels.weekdayLong[day]} ${labels.hour[hour]}: ${fmtHrs(hours(3_600_000))}`, `${cell}: ${fmtHrs(hours(3_600_000))}`, `${locale} aria-label ${day}/${hour}`);
    }
  }
});

test('the table costs three formatters for all 38 labels, not one per cell', () => {
  const {value: labels, counts} = counted(() => heatMapLabels());
  assert.equal([...counts.values()].reduce((total, count) => total + count, 0), 3);
  assert.equal(countOf(counts, {weekday: 'short', timeZone: 'UTC'}), 1);
  assert.equal(countOf(counts, {weekday: 'long', timeZone: 'UTC'}), 1);
  assert.equal(countOf(counts, {hour: 'numeric', timeZone: 'UTC'}), 1);
  assert.equal(labels.weekdayShort.length + labels.weekdayLong.length + labels.hour.length, 38);
});

test('the analytics heat map renders 168 cells from one formatter of each shape', () => {
  const busiestTimes = Array.from({length: 7}, (_, day) => Array.from({length: 24}, (_, hour) => (day * 24 + hour) % 5 === 0 ? (hour + 1) * 60_000 : 0));
  const quiet = quietestHours(busiestTimes);
  assert.equal(typeof quiet, 'number');
  const interval = {timeZone: 'UTC', from: '2025-10-27T00:00:00.000Z', until: '2026-10-27T00:00:00.000Z'};
  const history = {...interval, period: '1y', topKind: 'movies', top: [], quietestStartHour: quiet,
    busiestTimes, buckets: [{start: interval.from, end: interval.until, partial: false, kinds: [{kind: 'movies', plays: 4, watchedMs: 7_200_000}]}]};
  const people = {...interval, period: '7d', nextCursor: '', buckets: [], items: [{accountId: 'sam', username: 'sam_k', profiles: ['Sam'], plays: 4, watchedMs: 7_200_000, kinds: [{kind: 'movies', plays: 4, watchedMs: 7_200_000}]}]};
  const report = {
    query: {period: '1y', window: 'live', peoplePeriod: '7d', topPeriod: '30d', topKind: 'movies', peopleLimit: 100},
    change: () => {},
    state: {loading: false, failed: false, refresh: () => {}, value: {history, people, topPlayed: {...interval, period: '1y', items: []}}},
  };
  const bindings = {role: 'owner', scope: {serverId: 'server', viewerId: 'viewer'}, effects: {openHome: () => {}}, artwork: () => undefined};
  const accounts = {items: [{accountId: 'sam', username: 'sam_k'}], loading: false, failed: false, refresh: () => {}};

  const {value: html, counts} = counted(() => renderToStaticMarkup(createElement(Analytics, {bindings, report, accounts} as never)));
  const labels = heatMapLabels();
  const cells = [...html.matchAll(/<rect [^>]*class="heatCell"[^>]*aria-label="([^"]*)"/g)].map(match => match[1]);
  // The rendered aria-labels are the old per-cell expression, in day-then-hour order.
  assert.equal(cells.length, 168);
  assert.deepEqual(cells, busiestTimes.flatMap((day, d) => day.map((ms, hour) => `${weekdayText(defaultI18n.locale, 'long', d)} ${hourText(defaultI18n.locale, hour)}: ${fmtHrs(hours(ms))}`)));
  // Column headings and the 3-hour axis ticks are the same entries.
  assert.deepEqual([...html.matchAll(/<text x="32" [^>]*>([^<]*)<\/text>/g)].map(match => match[1]), [...labels.weekdayShort]);
  assert.deepEqual([...html.matchAll(/<text x="[\d.]+" y="197">([^<]*)<\/text>/g)].map(match => match[1]), hourColumns.map(hour => labels.hour[hour]));
  assert.ok(html.includes(formatQuietHours(quiet!)), 'the untouched quiet-window label keeps its text');
  assert.equal(labels.hour[13], formatHourOrdinal(13), 'the drawn ordinal formatter output is unchanged');

  // One constructor per shape for 168 cells, plus the untouched quiet label's own cost.
  assert.equal(countOf(counts, {weekday: 'short', timeZone: 'UTC'}), 1);
  assert.equal(countOf(counts, {weekday: 'long', timeZone: 'UTC'}), 1);
  const quietOnly = countOf(counted(() => formatQuietHours(quiet!)).counts, {hour: 'numeric', timeZone: 'UTC'});
  assert.equal(countOf(counts, {hour: 'numeric', timeZone: 'UTC'}), quietOnly + 1);
  assert.ok([...counts.values()].reduce((total, count) => total + count, 0) < 16, 'the whole charts render stays O(1) in cells');
});
