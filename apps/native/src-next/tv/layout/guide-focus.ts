import type {GuideCell} from '../../../../../packages/client-core/src/guide';

export type TVGuideCell = GuideCell & Readonly<{pending?: boolean}>;
/** The offered next/previous block is a loading cell, never a made-up programme. */
export function tvGuideNavigationCells(cells: readonly GuideCell[], coverage: Readonly<{first: number; last: number}> | undefined,
  viewStart: number, px: number, block: number): readonly TVGuideCell[] {
  if (!coverage || !cells.length) return cells;
  const result: TVGuideCell[] = [...cells];
  const pending = (start: number, end: number): TVGuideCell => ({kind: 'gap', pending: true, start, end,
    x: (start - viewStart) * px, width: Math.max(0, (end - start) * px - 6), clippedStart: false, clippedEnd: false,
    state: 'future', tier: 'title'});
  const first = cells[0]!, last = cells[cells.length - 1]!;
  if (first.start > coverage.first) result.unshift(pending(Math.max(coverage.first, first.start - block), first.start));
  if (last.end < coverage.last) result.push(pending(last.end, Math.min(coverage.last, last.end + block)));
  return result;
}

/** Reveal the retained time position; a wide vertical destination never pans to its end. */
export function tvGuideFocusStart(anchor: number, start: number, end: number, step: number): number {
  return anchor < start ? anchor : anchor >= end ? anchor - (end - start) + Math.min(step, end - start) : start;
}
