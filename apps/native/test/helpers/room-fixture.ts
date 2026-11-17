import {parseRoom, type Room} from '../../../../packages/client-core/src/room-protocol';

/** Current server-shaped Room; tests override only the facts relevant to their scenario. */
export function roomFixture(patch: Partial<Room> = {}): Room {
  return parseRoom({id: 'room', name: 'Room', control: 'host',
    you: {memberId: 'member', host: true, mayControl: true, mayBecomeHost: false},
    clock: {positionMs: 0, at: '2026-11-16T23:00:00Z', playing: false, ready: 1, waitingFor: 0},
    host: {present: true}, members: [{id: 'member', name: 'Host', host: true, you: true, status: 'in_step'}],
    queue: [], inStepMs: 1000, jumpMs: 5000, reportEveryMs: 60000, ...patch});
}
