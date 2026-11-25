import assert from 'node:assert/strict';
import test from 'node:test';
import {PlaybackDock, playbackFence, type QueuePlaybackFence} from '../../../packages/player-core/src';
import {nativeShellBackTransition} from '../src-next/shell/playback';
import type {ShellAuthority} from '../src-next/shell/model';

const authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence: QueuePlaybackFence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});

test('TV hardware Back closes both queue video and live video through genuine dock effects', () => {
  const channel = playbackFence({kind: 'channel', playback: fence.playback, sessionId: 'live-session', channelKind: 'live-source', sourceId: 'source', channelId: 'channel', channelGeneration: 'channel-generation', generation: 1});
  for (const kind of ['video', 'live'] as const) {
    const dock = new PlaybackDock(fence.playback.scope);
    dock.start(kind === 'video' ? {kind, fence, queue: null, positionSeconds: 20, playing: true, expanded: true} : {kind, fence: channel, queue: null, positionSeconds: 20, playing: true, expanded: true});
    const transition = nativeShellBackTransition(dock, {authority, fence: kind === 'video' ? fence : channel, dock: dock.getSnapshot()}, authority, true);
    assert.ok(transition);
    assert.equal(transition.state.local, null);
    assert.deepEqual(transition.effects.map(effect => effect.kind), ['stop']);
    assert.equal(dock.occupant(), null);
  }
});

test('phone video Back and TV audio Back retain their actual collapsed occupant', () => {
  for (const kind of ['video', 'music', 'audiobook'] as const) {
    const dock = new PlaybackDock(fence.playback.scope);
    dock.start({kind, fence, queue: null, positionSeconds: 20, playing: true, expanded: true});
    const transition = nativeShellBackTransition(dock, {authority, fence, dock: dock.getSnapshot()}, authority, kind !== 'video');
    assert.ok(transition);
    assert.equal(transition.state.local?.expanded, false);
    assert.equal(transition.state.local?.playing, true);
    assert.deepEqual(transition.effects, []);
    assert.equal(dock.occupant()?.mode, kind);
  }
});

test('stale authority, idle playback and an already collapsed player do not consume Back', () => {
  const dock = new PlaybackDock(fence.playback.scope);
  assert.equal(nativeShellBackTransition(dock, {authority, dock: dock.getSnapshot()}, authority, true), undefined);
  dock.start({kind: 'video', fence, queue: null, positionSeconds: 20, playing: true, expanded: true});
  const state = dock.getSnapshot();
  assert.equal(nativeShellBackTransition(dock, {authority, fence, dock: state}, {...authority, viewerFence: 'retired'}, true), undefined);
  assert.equal(nativeShellBackTransition(dock, {authority, fence: {...fence, generation: 2}, dock: state}, authority, true), undefined);
  assert.strictEqual(dock.getSnapshot(), state);
  dock.collapse(fence);
  assert.equal(nativeShellBackTransition(dock, {authority, fence, dock: dock.getSnapshot()}, authority, true), undefined);
});
