import type {HomeSelectionPlaybackItem} from '@core/home-selection/types.ts';
import type {SegmentInput} from '@core/playback-v1/queue.ts';
import {planSelectionPlayback} from '@core/selection-playback-plan.ts';

/** A card's resume position is display data: the server resumes on its own. Only Start
 * over or a deliberate chapter/offset supplies a position, as on native. */
export function selectionStartSeconds(items: readonly HomeSelectionPlaybackItem[]): number | undefined {
  const first = items[0];
  if (!first || !('target' in first)) return undefined;
  switch (first.startIntent ?? 'resume') {
    case 'resume': return undefined;
    case 'start-over': return 0;
    case 'explicit': {
      const seconds = first.target.startSeconds;
      if (seconds === undefined || !Number.isFinite(seconds) || seconds < 0 || seconds > 1e9) throw new Error('Invalid playback target.');
      return seconds;
    }
    default: throw new Error('Invalid playback start intent.');
  }
}

/** Keep the supplied display/pick order. Each aggregate is captured by the existing server
 * selector, while consecutive actual playable leaves share a capped items segment. Play is the
 * one queue-create request, so its 16-segment ceiling is checked before any replacement starts. */
export function selectionSegments(items: readonly HomeSelectionPlaybackItem[], playing: boolean): readonly SegmentInput[] {
  return planSelectionPlayback(items, playing ? 'replace' : 'insert').segments;
}
