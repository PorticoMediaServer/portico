import {DAY_MS, dayStarts, localDayStart, spanAt} from './time.ts';

/** The published guide range: the union of every selected source's finite bounds (spec §3.4).
 *  A source with no published horizon (channels but no guide yet) adds nothing; it never erases
 *  the other sources' range (LT-6). Either side is open when no source bounds it. */
export type GuideRange = Readonly<{start?: number; end?: number; days?: number}>;
type Bounded = Readonly<{availableStart: string; availableEnd: string; guideDays?: number}>;

export function guideRange(sources: readonly Bounded[]): GuideRange {
  let start = Infinity, end = -Infinity, days = 0;
  for (const source of sources) {
    const from = Date.parse(source.availableStart), until = Date.parse(source.availableEnd);
    if (Number.isFinite(from) && Number.isFinite(until) && until > from) {start = Math.min(start, from); end = Math.max(end, until);}
    days = Math.max(days, source.guideDays ?? 0);
  }
  return {...(Number.isFinite(start) ? {start} : {}), ...(Number.isFinite(end) ? {end} : {}), ...(days > 0 ? {days} : {})};
}

/** The span Now shows: now at 25% of it (spec §3.4, "Now"), inside the range. */
export function guideNowStart(now: number, spanMs: number, range: GuideRange): number {
  return clampGuideStart(spanAt(now, spanMs).start, spanMs, now, range);
}

/** Keep a span inside the published range. Now's own span is always reachable, so a range that
 *  ended before now (a stale guide) never pushes the view off the present. */
export function clampGuideStart(start: number, spanMs: number, now: number, range: GuideRange): number {
  const here = spanAt(now, spanMs);
  const first = Math.min(range.start ?? -Infinity, here.start), last = Math.max(range.end ?? Infinity, here.end) - spanMs;
  return Math.max(first, Math.min(start, last));
}

/** The day menu: Today, Tomorrow, then each day the range reaches (from its end, or from the
 *  sources' published day count when no end is finite), and always the day being viewed, so the
 *  menu never shows a blank choice after stepping into a past day. */
export function guideDayMenu(now: number, viewStart: number, timeZone: string, range: GuideRange): readonly number[] {
  const until = range.end ?? (range.days ? now + range.days * DAY_MS : undefined);
  const days = until !== undefined && until > now ? dayStarts(now, until, timeZone) : [localDayStart(now, timeZone)];
  const viewed = localDayStart(viewStart, timeZone);
  return days.includes(viewed) ? days : [...days, viewed].sort((a, b) => a - b);
}
