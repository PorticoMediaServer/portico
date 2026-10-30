import {playbackFence, type QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
/** Canonical synthetic queue identity for UI-only source fixtures. It is never
 * a runtime acceptance receipt or an admitted native output. */
export function dockFence(id = 'original'): QueuePlaybackFence {
  return playbackFence({kind: 'queue', playback: {scope: {serverId: 'fixture-server', authority: 'local', accountId: 'fixture-account',
    profileId: 'fixture-profile', controllerId: 'fixture-controller', controllerEpoch: 'fixture-epoch', commandLaneId: 'fixture-lane'}, run: 1},
    sessionId: `session-${id}`, itemId: id, sourceId: `source-${id}`, entryId: `entry-${id}`, generation: 1}) as QueuePlaybackFence;
}
