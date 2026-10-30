import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src/index';

/** An idle host has no authority to settle, capture or reuse the previous picture. */
export function currentMorphFence(current: PlaybackFence | undefined, captured: PlaybackFence | undefined): boolean {
  return captured !== undefined && sameFence(current, captured);
}
