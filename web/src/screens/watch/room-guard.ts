import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback';

export type RoomGuardSnapshot = Readonly<{phase: string; group?: Readonly<{id: string}>}>;

/** A rendered room handler acts only while its binding still holds that same room and the
 * playback occurrence it was drawn for. The service's own leave() targets its current room,
 * so runtime playback identity alone would let a stale handler leave a newly joined room. */
export function roomActionAllowed(runtime: Pick<WebShellPlaybackRuntime, 'groupPlaybackMatches'>, bindingCurrent: boolean, fence: PlaybackFence | undefined, room: RoomGuardSnapshot, groupId: string, entryId: string): boolean {
  if (!bindingCurrent || !fence) return false;
  if (!['live', 'reconnecting'].includes(room.phase) || room.group?.id !== groupId) return false;
  return runtime.groupPlaybackMatches(fence, groupId, entryId);
}
