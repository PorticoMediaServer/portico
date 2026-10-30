import {nativeStartSeconds} from './start-intent';
import type {HomeSelectionPlaybackItem} from '../../../../../packages/client-core/src/home-selection/types';
import type {SegmentInput} from '../../../../../packages/client-core/src/playback-v1/queue';
import {planSelectionPlayback} from '../../../../../packages/client-core/src/selection-playback-plan';

/** SIM-04: Home supplies display order for Play and pick order for enqueue; the shared plan
 * never expands an aggregate from preview members. Its 16-segment cap applies to Play only. */
export function selectionSegments(items: readonly HomeSelectionPlaybackItem[], playing: boolean): readonly SegmentInput[] {
  return planSelectionPlayback(items, playing ? 'replace' : 'insert').segments;
}

/** Resume lets the server decide; Start over is zero; only a chosen position is sent. */
export function selectionStartSeconds(items: readonly HomeSelectionPlaybackItem[]): number | undefined {
  const first = items[0];
  return first && 'target' in first ? nativeStartSeconds(first) : undefined;
}
