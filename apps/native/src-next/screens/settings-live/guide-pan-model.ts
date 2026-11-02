import {clampSpan} from '../../../../../packages/client-core/src/guide/time';
import {guideRange} from '../../../../../packages/client-core/src/guide/range';
import type {GuideSourceSummary} from '../../../../../packages/client-core/src/guide/directory';

export type NativeGuidePanRange = Readonly<{first: number; last: number}>;

/** The selected sources' published coverage bounds this local viewport: their union, so a source
 *  with no guide yet never erases the others' range (LT-6, shared with web through guideRange). */
export function nativeGuidePanRange(sources: readonly GuideSourceSummary[], span: number): NativeGuidePanRange | undefined {
  if (!sources.length || !Number.isFinite(span) || span <= 0) return undefined;
  const range = guideRange(sources);
  if (range.start === undefined || range.end === undefined) return undefined;
  return range.end - range.start > span ? {first: range.start, last: range.end - span} : undefined;
}

export function nativeGuidePanTarget(start: number, span: number, range: NativeGuidePanRange | undefined): number | undefined {
  if (!range || !Number.isFinite(range.first) || !Number.isFinite(range.last) || range.last < range.first || !Number.isFinite(start) || !Number.isFinite(span) || span <= 0) return undefined;
  return Math.round(clampSpan(start, span, range.first, range.last + span).start);
}

/** Pin clipped programme text to the visible lane edge while its time-sized body moves. */
export function nativeGuideCellWindow(left: number, width: number, laneWidth: number, shift: number): Readonly<{cutLeft: number; width: number}> {
  'worklet';
  const visibleLeft = left + shift, cutLeft = Math.max(0, -visibleLeft);
  return {cutLeft, width: Math.max(0, Math.min(width - cutLeft, laneWidth - Math.max(0, visibleLeft)))};
}

/** Drawn gutters are outside elapsed time: the fill must end at the now line,
 * including a clipped start or an elapsed portion entirely left of the viewport. */
export function nativeGuideElapsedWidth(left: number, width: number, nowX: number): number {
  return Math.max(0, Math.min(width, nowX - left));
}

/** The visible guide rows as a half-open range [first, last): the same convention the initial
 * range ({first: 0, last: 12}), revealTVCell (`index >= last` is off screen) and the guide store
 * viewport (`lastRow: index + 1`) use. Viewability reports the rows it shows, so the end is the
 * last shown row plus one (E-tv-38: an inclusive end made the bottom row read as off screen, and
 * each focus there re-scrolled the guide). */
export function visibleGuideRows(indices: readonly number[]): Readonly<{first: number; last: number}> | undefined {
  if (!indices.length) return undefined;
  return {first: Math.min(...indices), last: Math.max(...indices) + 1};
}
