/**
 * Guide time math (Spec — Channels and Guide §3.2, §9.1). Layout is in real UTC milliseconds, so
 * a DST day is 23 or 25 hours wide; only labels and day starts use the viewer's time zone.
 */
export const MINUTE_MS = 60_000;
export const SLOT_MS = 30 * MINUTE_MS;
export const HOUR_MS = 60 * MINUTE_MS;
/** Guide tiles are 3-hour blocks aligned to UTC multiples of 3 h (store.ts). */
export const BLOCK_MS = 3 * HOUR_MS;
export const DAY_MS = 24 * HOUR_MS;

export const floorTo = (ms: number, step: number) => Math.floor(ms / step) * step;
export const ceilTo = (ms: number, step: number) => Math.ceil(ms / step) * step;

/** Block starts whose blocks intersect [start, end). */
export function blocksCovering(start: number, end: number, blockMs = BLOCK_MS): number[] {
  if (!(end > start)) return [];
  const out: number[] = [];
  for (let t = floorTo(start, blockMs); t < end; t += blockMs) out.push(t);
  return out;
}

/** Pixels per millisecond for a column width per 30-minute slot. */
export const pxPerMsFor = (columnPx: number, slotMs = SLOT_MS) => columnPx / slotMs;

/** The now line's x, or undefined outside the visible span. */
export function nowX(now: number, viewStart: number, viewEnd: number, pxPerMs: number): number | undefined {
  return now < viewStart || now >= viewEnd ? undefined : (now - viewStart) * pxPerMs;
}

/** The time zone's offset from UTC (ms) at an instant, e.g. -14_400_000 for EDT. */
const formats = new Map<string, Intl.DateTimeFormat>();
export function zoneOffset(ms: number, timeZone: string): number {
  let format = formats.get(timeZone);
  if (!format) { format = new Intl.DateTimeFormat('en-US', {timeZone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit'}); formats.set(timeZone, format); }
  const parts = format.formatToParts(new Date(ms));
  const n = (type: string) => Number(parts.find(p => p.type === type)?.value);
  const asUtc = Date.UTC(n('year'), n('month') - 1, n('day'), n('hour') % 24, n('minute'), n('second'));
  return asUtc - floorTo(ms, 1000);
}

/** The local calendar date (y, m, d) of an instant in a zone. */
function localDate(ms: number, timeZone: string): [number, number, number] {
  const local = new Date(ms + zoneOffset(ms, timeZone));
  return [local.getUTCFullYear(), local.getUTCMonth(), local.getUTCDate()];
}

/**
 * The instant of local midnight starting the day that contains `ms` (DST-safe: the offset is
 * re-read at the candidate, and a midnight that doesn't exist resolves to the first valid instant).
 */
export function localDayStart(ms: number, timeZone: string): number {
  const [y, m, d] = localDate(ms, timeZone);
  const wall = Date.UTC(y, m, d);
  let guess = wall - zoneOffset(wall, timeZone);
  guess = wall - zoneOffset(guess, timeZone);
  // A skipped midnight (e.g. a zone that springs forward at 00:00): step to the first local instant of that date.
  if (localDate(guess, timeZone).join() !== [y, m, d].join()) guess += HOUR_MS;
  return guess;
}

/** Local day starts from the day containing `from` up to `to` (the day menu). */
export function dayStarts(from: number, to: number, timeZone: string): number[] {
  const out: number[] = [];
  let t = localDayStart(from, timeZone);
  while (t < to && out.length < 400) {
    out.push(t);
    t = localDayStart(t + 26 * HOUR_MS, timeZone); // lands in the next local day whatever its length
  }
  return out;
}

export type RulerTick = Readonly<{atMs: number; x: number; kind: 'hour' | 'half' | 'day'}>;

/**
 * Ticks every `slotMs` across the view. A tick at a local midnight is `day` (label it with the
 * date); ticks on local whole hours are `hour`; others `half`. Labels are the platform's
 * `i18n.time`/`i18n.date`, so the model stays language-free.
 */
export function rulerTicks(viewStart: number, viewEnd: number, pxPerMs: number, timeZone: string, slotMs = SLOT_MS): RulerTick[] {
  const out: RulerTick[] = [];
  if(!Number.isFinite(slotMs)||slotMs<=0)return out;
  const offset=zoneOffset(viewStart,timeZone);
  for (let t = ceilTo(viewStart+offset,slotMs)-offset; t < viewEnd; t += slotMs) {
    const local = t + zoneOffset(t, timeZone);
    const minuteOfDay = ((local % DAY_MS) + DAY_MS) % DAY_MS;
    const kind = minuteOfDay === 0 ? 'day' : minuteOfDay % HOUR_MS === 0 ? 'hour' : 'half';
    out.push({atMs: t, x: (t - viewStart) * pxPerMs, kind});
  }
  return out;
}

/** Where "Now" puts the view: now at `lead` (25%) of the span. */
export function spanAt(anchor: number, spanMs: number, lead = 0.25): Readonly<{start: number; end: number}> {
  const start = floorTo(anchor - spanMs * lead, MINUTE_MS);
  return {start, end: start + spanMs};
}

/** Keep a span inside the published range [rangeStart, rangeEnd). */
export function clampSpan(start: number, spanMs: number, rangeStart: number, rangeEnd: number): Readonly<{start: number; end: number}> {
  const s = Math.max(rangeStart, Math.min(start, rangeEnd - spanMs));
  return {start: s, end: s + spanMs};
}
