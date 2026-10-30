/**
 * Guide row layout (Spec — Channels and Guide §3.3, §4, §9.2): where each program cell starts and
 * how wide it is in the visible span, what text tier fits, and how TV focus moves.
 * Pure and O(cells in the span): a row's programs outside the span cost a binary search, not a scan.
 */
import type {GuideProgram} from './types.ts';

export type TextTier = 'full' | 'title2' | 'title' | 'sliver';
export type CellState = 'past' | 'now' | 'future';

export type GuideCell = Readonly<{
  kind: 'program' | 'gap';
  /** True times (a gap spans the hole between programs, or the whole row without a guide). */
  start: number;
  end: number;
  x: number;
  width: number;
  /** The program began before / ends after the visible span (draw ‹ / ›, pin the title left). */
  clippedStart: boolean;
  clippedEnd: boolean;
  tier: TextTier;
  state: CellState;
  program?: GuideProgram;
}>;

/** Text tiers by cell width, per platform (§3.3). */
export type TierThresholds = Readonly<{full: number; title2: number; title: number}>;
export const TIERS = {
  standard: {full: 200, title2: 96, title: 32},
  tv: {full: 300, title2: 150, title: 32},
} as const satisfies Record<string, TierThresholds>;

export type RowLayoutOptions = Readonly<{
  viewStart: number;
  viewEnd: number;
  pxPerMs: number;
  now: number;
  gapPx?: number;
  tiers?: TierThresholds;
  /**
   * Where the guide has data for this channel. Holes inside it are "No information" cells; outside
   * it nothing is drawn. Default: the whole span (every hole is a gap cell).
   */
  rangeStart?: number;
  rangeEnd?: number;
}>;

const tierFor = (width: number, t: TierThresholds): TextTier => (width >= t.full ? 'full' : width >= t.title2 ? 'title2' : width >= t.title ? 'title' : 'sliver');

/**
 * Programs sorted by start with overlaps trimmed (a program starting before the previous one ends
 * starts at that end; one left empty is dropped) and duplicates (same id) removed.
 */
export function normalizePrograms(programs: readonly GuideProgram[]): GuideProgram[] {
  const sorted = [...programs].sort((a, b) => a.start - b.start || a.end - b.end || (a.id < b.id ? -1 : 1));
  const out: GuideProgram[] = [];
  const seen = new Set<string>();
  let cursor = -Infinity;
  for (const p of sorted) {
    if (seen.has(p.id) || !(p.end > p.start)) continue;
    seen.add(p.id);
    const start = Math.max(p.start, cursor);
    if (p.end <= start) continue;
    out.push(start === p.start ? p : {...p, start});
    cursor = p.end;
  }
  return out;
}

/** The first index whose end is after `t` (programs normalized). */
function firstEndingAfter(programs: readonly GuideProgram[], t: number): number {
  let lo = 0, hi = programs.length;
  while (lo < hi) { const mid = (lo + hi) >> 1; if (programs[mid]!.end <= t) lo = mid + 1; else hi = mid; }
  return lo;
}

/** Cells for one row across the visible span. `programs` must be normalized (normalizePrograms). */
export function layoutRow(programs: readonly GuideProgram[], o: RowLayoutOptions): GuideCell[] {
  const {viewStart, viewEnd, pxPerMs, now} = o;
  const gap = o.gapPx ?? 2;
  const tiers = o.tiers ?? TIERS.standard;
  const rangeStart = Math.max(viewStart, o.rangeStart ?? viewStart);
  const rangeEnd = Math.min(viewEnd, o.rangeEnd ?? viewEnd);
  const cells: GuideCell[] = [];
  const push = (kind: 'program' | 'gap', start: number, end: number, program?: GuideProgram) => {
    const from = Math.max(start, viewStart), to = Math.min(end, viewEnd);
    if (to <= from) return;
    const width = Math.max(0, (to - from) * pxPerMs - gap);
    cells.push({kind, start, end, x: (from - viewStart) * pxPerMs, width, clippedStart: start < viewStart, clippedEnd: end > viewEnd, tier: tierFor(width, tiers), state: end <= now ? 'past' : start <= now ? 'now' : 'future', ...(program ? {program} : {})});
  };
  let cursor = rangeStart;
  for (let i = firstEndingAfter(programs, viewStart); i < programs.length; i++) {
    const p = programs[i]!;
    if (p.start >= viewEnd) break;
    if (p.start > cursor && cursor < rangeEnd) push('gap', cursor, Math.min(p.start, rangeEnd));
    push('program', p.start, p.end, p);
    cursor = Math.max(cursor, p.end);
  }
  if (cursor < rangeEnd) push('gap', cursor, rangeEnd);
  return cells;
}

/**
 * TV Up/Down: the cell containing the focus time in the target row, else the nearest by start.
 * The focus time is kept by the grid (the start of the focused cell, clamped into the span), so
 * moving through channels stays at one time even across long movies.
 */
export function focusTarget(cells: readonly GuideCell[], focusTime: number): number {
  if (!cells.length) return -1;
  let best = 0, bestDistance = Infinity;
  for (let i = 0; i < cells.length; i++) {
    const c = cells[i]!;
    if (c.start <= focusTime && focusTime < c.end) return i;
    const d = Math.abs(c.start - focusTime);
    if (d < bestDistance) { best = i; bestDistance = d; }
  }
  return best;
}

/** TV Left/Right within a row; -1 at the ends (the grid scrolls time, or stays: no traps). */
export function neighbor(cells: readonly GuideCell[], index: number, direction: -1 | 1): number {
  const next = index + direction;
  return next >= 0 && next < cells.length ? next : -1;
}

/** The focus time after focusing a cell: its start, clamped into the visible span. */
export const focusTimeOf = (cell: GuideCell, viewStart: number, viewEnd: number) => Math.min(Math.max(cell.start, viewStart), viewEnd - 1);
