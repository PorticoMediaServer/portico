import test from 'node:test';
import assert from 'node:assert/strict';
import type {Group, GroupMember, GroupReadiness} from '@core/social-playback.ts';
import {memberAlias, memberName, togetherCanMakeHost, togetherHostNotice, togetherInviteVisible, togetherPlayLabel, togetherStateKey} from '../src/screens/together/together-model.ts';

const at = '2026-10-24T12:00:00Z';
function member(id: string, name: string, role: 'host' | 'member', readiness: GroupReadiness = 'ready', present = true): GroupMember {
  return {id, displayName: name, private: false, role, state: 'joined', readiness, positionUs: '0', reportedAt: at, presence: present ? 'connected' : 'away', joinedAt: at};
}
function group(overrides: Partial<Group> = {}): Group {
  return {id: 'g1', name: 'Movie night', state: 'playing', hostAuthority: 'host-only', hostMemberId: 'm1',
    revision: '1', playbackRevision: '1', settingsRevision: '1', governanceRevision: '1', queueRevision: '1', reconnectGeneration: '0',
    lastCommand: 'play', lastCommandId: 'c1', endedReason: '', eventOrdinal: '1', createdAt: at, updatedAt: at,
    permissions: {isHost: true, canControl: true, canManageQueue: true, canClaimHost: false},
    authority: {deviceId: 'd1', playbackId: 'p1', state: 'bound'},
    host: {presence: 'connected', lastSeenAt: at, pauseAt: null, endAt: null},
    timeline: {itemId: 'i1', currentEntryId: 'e1', state: 'playing', anchorPositionUs: '0', anchorAt: at, rate: {numerator: '1', denominator: '1'}, queuePosition: 0, waitUntil: null},
    settings: {shuffleEnabled: false, repeatMode: 'none'},
    sync: {noCorrectionUnderMs: 250, rateCorrectionMinimum: '0.98', rateCorrectionMaximum: '1.02', rateCorrectionMaxMs: 2000, seekAtOrOverMs: 1000},
    readiness: {aggregate: 'ready', ready: 2, buffering: 1, lagging: 0, stale: 0, memberCount: 3},
    members: [member('m1', 'Demo', 'host'), member('m2', 'Alex', 'member'), member('m3', 'Sam', 'member', 'lagging')],
    queue: null, viewerMemberId: 'm1', ...overrides};
}

test('the room badge keeps every drawn group state instead of collapsing to Waiting to start', () => {
  const playing = group();
  assert.equal(togetherStateKey(playing), 'web.together.state.playing');
  assert.equal(togetherStateKey({...playing, state: 'waiting', timeline: {...playing.timeline, state: 'paused'}}), 'web.together.state.waiting');
  assert.equal(togetherStateKey({...playing, state: 'ready', timeline: {...playing.timeline, state: 'paused'}}), 'web.together.state.ready');
  assert.equal(togetherStateKey({...playing, state: 'paused', timeline: {...playing.timeline, state: 'paused'}}), 'next.dock.paused');
  assert.equal(togetherStateKey({...playing, state: 'preparing', timeline: {...playing.timeline, state: 'paused'}}), 'web.together.state.preparing');
  assert.equal(togetherStateKey({...playing, state: 'ending', timeline: {...playing.timeline, state: 'paused'}}), 'web.together.state.ending');
  assert.equal(togetherStateKey({...playing, state: 'ended', timeline: {...playing.timeline, state: 'stopped'}}), 'web.together.state.ended');
  assert.equal(togetherStateKey({...playing, state: 'lobby', timeline: {...playing.timeline, state: 'idle'}}), 'web.together.state.lobby');
  assert.equal(togetherStateKey({...playing, state: 'host-reconnecting-playing'}), 'web.together.state.hostReconnecting');
});

test('the host notice pauses during reconnection and ends only after the server wait', () => {
  const now = Date.parse(at);
  assert.equal(togetherHostNotice(group(), now), undefined);
  const reconnecting = group({host: {presence: 'reconnecting', lastSeenAt: at, pauseAt: at, endAt: null}, permissions: {...group().permissions, canClaimHost: true}});
  assert.deepEqual(togetherHostNotice(reconnecting, now), {key: 'web.together.pausesIn', seconds: 0, takeOver: false, canClaim: false});
  const absent = group({host: {presence: 'absent', lastSeenAt: at, pauseAt: at, endAt: '2026-10-24T12:00:30Z'}, permissions: {isHost: false, canControl: false, canManageQueue: false, canClaimHost: true}});
  assert.deepEqual(togetherHostNotice(absent, now), {key: 'web.together.endsIn', seconds: 30, takeOver: true, canClaim: true});
  const noClaim = group({host: {presence: 'absent', lastSeenAt: at, pauseAt: at, endAt: '2026-10-24T12:00:30Z'}});
  assert.deepEqual(togetherHostNotice(noClaim, now), {key: 'web.together.endsIn', seconds: 30, takeOver: false, canClaim: false});
});

test('the server owns Become host eligibility', () => {
  const absent = group({host: {presence: 'absent', lastSeenAt: at, pauseAt: at, endAt: '2026-10-24T12:00:30Z'}, permissions: {isHost: false, canControl: false, canManageQueue: false, canClaimHost: false}});
  assert.equal(togetherHostNotice(absent, Date.parse(at))?.canClaim, false);
  const host = group();
  assert.equal(togetherHostNotice({...host, host: {presence: 'absent', lastSeenAt: at, pauseAt: at, endAt: '2026-10-24T12:00:30Z'}}, Date.parse(at))?.canClaim, false);
});

test('Make host appears only for a host on someone in step', () => {
  const value = group();
  assert.equal(togetherCanMakeHost(value, value.members[1]!), true);
  assert.equal(togetherCanMakeHost(value, value.members[2]!), false);
  assert.equal(togetherCanMakeHost(value, value.members[0]!), false);
  assert.equal(togetherCanMakeHost({...value, permissions: {...value.permissions, isHost: false}}, value.members[1]!), false);
  assert.equal(togetherCanMakeHost(value, {...value.members[1]!, presence: 'away'}), false);
});

test('Play anyway is the drawn label while someone is still catching up', () => {
  const value = group();
  assert.equal(togetherPlayLabel(value), 'web.together.pauseAll');
  assert.equal(togetherPlayLabel({...value, state: 'waiting', timeline: {...value.timeline, state: 'paused'}}), 'web.together.playAnyway');
  assert.equal(togetherPlayLabel({...value, state: 'ready', timeline: {...value.timeline, state: 'paused'}, readiness: {aggregate: 'ready', ready: 3, buffering: 0, lagging: 0, stale: 0, memberCount: 3}}), 'web.together.playAll');
});

test('a member sees the room without Invite', () => {
  assert.equal(togetherInviteVisible(group()), true);
  assert.equal(togetherInviteVisible({...group(), permissions: {isHost: false, canControl: false, canManageQueue: false, canClaimHost: false}}), false);
});

test('G38: members read by username first, with a differing group name quieter; private members stay masked', () => {
  assert.equal(memberName({displayName: 'Movie Alex', username: 'alex', private: false}), 'alex');
  assert.equal(memberAlias({displayName: 'Movie Alex', username: 'alex', private: false}), 'Movie Alex');
  assert.equal(memberAlias({displayName: 'alex', username: 'alex', private: false}), undefined, 'the same name is not repeated');
  assert.equal(memberName({displayName: 'Sam', private: false}), 'Sam', 'no username published: the group name');
  assert.equal(memberAlias({displayName: 'Sam', private: false}), undefined);
  assert.equal(memberName({displayName: 'Private viewer', username: 'leaked', private: true}), 'Private viewer');
  assert.equal(memberAlias({displayName: 'Private viewer', username: 'leaked', private: true}), undefined);
});
