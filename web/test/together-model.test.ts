import test from 'node:test';
import assert from 'node:assert/strict';
import type {Room} from '@core/room-protocol.ts';
import {memberAlias, memberName, memberStatusKey, togetherCanMakeHost, togetherHostNotice, togetherInviteVisible, togetherPlayLabel, togetherStateKey} from '../src/screens/together/together-model.ts';
const at = '2026-11-16T12:00:00Z';
function group(overrides: Partial<Room> = {}): Room {
  return {id: 'g1', name: 'Movie night', control: 'host', you: {host: true, memberId: 'host', mayControl: true, mayBecomeHost: false},
    clock: {entryId: 'e1', positionMs: 0, at, playing: true, ready: 2, waitingFor: 1}, host: {present: true},
    members: [{id: 'host', name: 'Demo', host: true, you: true, status: 'in_step'}, {id: 'alex', username: 'alex', name: 'Movie Alex', host: false, you: false, status: 'in_step'},
      {id: 'private', private: true, host: false, you: false, status: 'catching_up'}], queue: [], inStepMs: 750, jumpMs: 3000, reportEveryMs: 10000, ...overrides};
}
test('Room clock, wait, host and ended facts drive visible state', () => {
  const g = group();
  assert.equal(togetherStateKey(g), 'web.together.state.playing');
  assert.equal(togetherStateKey({...g, clock: {...g.clock, waitUntil: at}}), 'web.together.state.waiting');
  assert.equal(togetherStateKey({...g, clock: {...g.clock, playing: false}}), 'next.dock.paused');
  assert.equal(togetherStateKey({...g, clock: {...g.clock, entryId: undefined, playing: false}}), 'web.together.state.lobby');
  assert.equal(togetherStateKey({...g, host: {present: false}}), 'web.together.state.hostReconnecting');
  assert.equal(togetherStateKey({...g, ended: {reason: 'no_host', at}}), 'web.together.state.ended');
});
test('server deadlines and self-claim permission drive countdown', () => {
  const g = group({host: {present: false, pausesAt: at, endsAt: '2026-11-16T12:08:00Z'}});
  assert.deepEqual(togetherHostNotice(g, Date.parse(at) - 30000), {key: 'web.together.pausesIn', seconds: 30, takeOver: false, canClaim: false});
  assert.deepEqual(togetherHostNotice({...g, you: {...g.you, host: false, mayBecomeHost: true}}, Date.parse(at)), {key: 'web.together.endsIn', seconds: 480, takeOver: true, canClaim: true});
  assert.equal(togetherHostNotice(group(), Date.parse(at)), undefined);
});
test('host transfer requires another in-step member; Invite requires host', () => {
  const g = group();
  assert.equal(togetherCanMakeHost(g, g.members[1]!), true);assert.equal(togetherCanMakeHost(g, g.members[0]!), false);assert.equal(togetherCanMakeHost(g, g.members[2]!), false);
  assert.equal(togetherCanMakeHost({...g, you: {...g.you, host: false}}, g.members[1]!), false);
  assert.equal(togetherInviteVisible(g), true);assert.equal(togetherInviteVisible({...g, you: {...g.you, host: false}}), false);
});
test('waiting says Play anyway even with playing intent', () => {
  const g = group();assert.equal(togetherPlayLabel(g), 'web.together.pauseAll');
  assert.equal(togetherPlayLabel({...g, clock: {...g.clock, waitUntil: at}}), 'web.together.playAnyway');
  assert.equal(togetherPlayLabel({...g, clock: {...g.clock, playing: false}}), 'web.together.playAll');
});
test('G38: username first, group alias quieter, private viewer has no names', () => {
  const g = group();assert.equal(memberName(g.members[1]!), 'alex');assert.equal(memberAlias(g.members[1]!), 'Movie Alex');
  assert.equal(memberName({...g.members[1]!, private: true}), 'Private viewer');assert.equal(memberAlias({...g.members[1]!, private: true}), undefined);
  assert.equal(memberStatusKey(g.members[2]!), 'web.together.catchingUp');assert.equal(memberStatusKey({...g.members[1]!, status: 'away'}), 'web.together.away');
});
