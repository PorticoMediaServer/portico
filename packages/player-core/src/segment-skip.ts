/**
 * The skip rules every player shares (web, phone, tablet, television): which segment the prompt
 * offers, when it skips without asking, where a skip lands, and how long an unattended skip can be
 * undone. The server decides which segments exist and which are safe to skip unattended; the
 * viewer's settings decide whether to ask, skip or stay quiet. Skipping unattended is opt-in.
 */
import type {SegmentMarker} from './segment-markers.ts';

export type SkipMode = 'ask' | 'auto' | 'off';

/** An intro or recap skip lands this far before the segment ends, so the cut is not abrupt and
 * nothing after it is lost to a boundary a fraction late. */
export const SKIP_LEAD_SECONDS = 1;

/** How long the "Skipped intro · Undo" note stays after an unattended skip. */
export const UNDO_SECONDS = 6;

/** The prompt stops this close to a segment's end: there is nothing left to skip. */
const TAIL_SECONDS = 0.5;

/** Closing segments: the end credits and what follows the story (an outro, a preview). */
export const closingKind = (kind: string): boolean => kind === 'credits' || kind === 'outro';

/** The segment the prompt offers at a position: the first one playing that the viewer has not
 * dismissed or turned off. */
export function activeSegment(markers: readonly SegmentMarker[] | undefined, positionSeconds: number, modes: Readonly<Record<string, SkipMode>>, dismissed: ReadonlySet<string>): SegmentMarker | undefined {
  return markers?.find(m => positionSeconds >= m.startSeconds && positionSeconds < m.endSeconds - TAIL_SECONDS && !dismissed.has(m.id) && (modes[m.kind] ?? 'ask') !== 'off');
}

/** Skip without asking only when the viewer chose to for this kind and the server says the segment
 * is safe to skip unattended (approved by the owner, or confirmed by the show's other episodes). */
export const skipsAutomatically = (marker: SegmentMarker, modes: Readonly<Record<string, SkipMode>>): boolean => (modes[marker.kind] ?? 'ask') === 'auto' && marker.automaticSafe;

/** Where skipping a segment lands. Closing credits land exactly where they end: on the mid- or
 * post-credits scene when there is one, otherwise the end of the title, where the end card takes
 * over. Anything else lands a moment before its end. */
export function skipTarget(marker: SegmentMarker, durationSeconds?: number): number {
  if (closingKind(marker.kind)) return marker.endSeconds;
  const target = Math.max(marker.startSeconds, marker.endSeconds - SKIP_LEAD_SECONDS);
  return durationSeconds !== undefined && Number.isFinite(durationSeconds) && durationSeconds > 0 ? Math.min(target, durationSeconds) : target;
}

/** Whether a closing segment is followed by more of the title (a scene after the credits). */
export const followedByScene = (marker: Pick<SegmentMarker, 'endSeconds'>, durationSeconds: number): boolean => Number.isFinite(durationSeconds) && durationSeconds > 0 && durationSeconds - marker.endSeconds > 3;
