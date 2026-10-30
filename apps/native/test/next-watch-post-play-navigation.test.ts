import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence} from '../../../packages/player-core/src/identity.ts';
import {PlaybackDock} from '../../../packages/player-core/src/dock.ts';
import {nativePostPlayNavigation} from '../src-next/screens/watch/post-play-navigation.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation: 1});
const related: any = {id: 'related', kind: 'movie', title: 'Related'};
function fixture() {
  const dock = new PlaybackDock(fence.playback.scope);
  const local: any = {kind: 'video', fence, expanded: true, playing: false, positionSeconds: 300};
  dock.start(local);
  let current = true, route = {revision: 1}, activeAuthority = authority;
  let state: any = {fence, retired: false, dock: dock.getSnapshot(), playback: {phase: 'ended'}};
  const calls: unknown[] = [], errors: unknown[] = [];
  const context: any = {authority: () => activeAuthority, current: () => current, navigation: {getSnapshot: () => route}, playback: {
    dock, getSnapshot: () => ({...state, dock: dock.getSnapshot()}), onTransition: async (transition: any) => {calls.push(transition.effects);},
  }};
  const navigation = nativePostPlayNavigation(context, fence, () => current, entry => {calls.push(entry); route = {revision: route.revision + 1};}, error => errors.push(error));
  return {context, dock, calls, errors, navigation, replace: () => {state = {...state, fence: {...fence, generation: 2}};},
    route: () => {route = {revision: 2};}, authority: () => {activeAuthority = {...authority, epoch: 'other'};}, retire: () => {current = false;}};
}

test('opening a related title closes the completed dock before navigating and cannot run twice', () => {
  const f = fixture(); assert.equal(f.navigation.current(), true);
  f.navigation.open(related);
  assert.equal(f.dock.getSnapshot().local, null);
  assert.equal((f.calls[0] as any[])[0].kind, 'stop'); assert.deepEqual((f.calls[0] as any[])[0].playback.fence, fence);
  assert.equal(f.calls[1], related); f.navigation.open(related); assert.equal(f.calls.length, 2);
});

for (const change of ['replace', 'route', 'authority', 'retire'] as const) test(`retained recommendation refuses after ${change}`, () => {
  const f = fixture(); f[change](); assert.equal(f.navigation.current(), false); f.navigation.open(related);
  assert.deepEqual(f.calls, []); assert.deepEqual(f.dock.getSnapshot().local?.fence, fence);
});

test('a synchronous transition observer that opens a newer route prevents the retained navigation', () => {
  const f = fixture(); f.context.playback.onTransition = async () => {f.route();}; f.navigation.open(related);
  assert.deepEqual(f.calls, []); assert.equal(f.dock.getSnapshot().local, null);
});
