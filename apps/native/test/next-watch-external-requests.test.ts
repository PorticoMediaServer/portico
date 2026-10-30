import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeWatchExternalRequests} from '../src-next/screens/watch/external-requests.ts';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
import {playbackFence, type PlaybackFence} from '../../../packages/player-core/src/index.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation: 1});
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
function fixture() {
  const navigation = new NativeShellNavigation(authority, () => authority);
  navigation.open({kind: 'title', entry: {id: 'item', kind: 'movie', title: 'Stored title', libraryId: 'library'}}, authority);
  let active = true, expanded = true;
  const source = new Set<() => void>(), viewer = new Set<() => void>(), pending = deferred<boolean>(), errors: unknown[] = [];
  const calls: {fence: PlaybackFence; lifetime: {signal: AbortSignal; isCurrent: () => boolean}}[] = [];
  const subscribe = (listeners: Set<() => void>) => (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};};
  const requests = new NativeWatchExternalRequests({current: () => active, subscribeCurrent: subscribe(viewer), navigation,
    playback: {getSnapshot: () => ({dock: {local: {expanded}}}), subscribe: subscribe(source), requestPictureInPicture: async (actual, lifetime) => {calls.push({fence: actual, lifetime}); return pending.promise;}}}, error => {errors.push(error);});
  return {requests, navigation, calls, errors, pending, notify: () => {for (const listener of source) listener();},
    collapse: () => {expanded = false; for (const listener of source) listener();}, retire: () => {active = false; for (const listener of viewer) listener();},
    listeners: () => source.size + viewer.size};
}

test('the real host lifetime permits its own new output and forwards the exact clicked fence', async () => {
  const d = fixture();
  try {
    const request = d.requests.request(fence); assert.equal(d.calls[0]!.fence, fence);
    d.notify(); assert.equal(d.calls[0]!.lifetime.isCurrent(), true); assert.equal(d.calls[0]!.lifetime.signal.aborted, false);
    d.pending.resolve(true); assert.equal(await request, true); assert.equal(d.listeners(), 0);
  } finally {d.requests.dispose();}
});
test('navigation, collapse, authority retirement and disposal abort the pending actual request', async () => {
  for (const change of ['navigation', 'collapse', 'authority', 'dispose'] as const) {
    const d = fixture();
    try {
      const request = d.requests.request(fence), lifetime = d.calls[0]!.lifetime;
      if (change === 'navigation') d.navigation.back(authority); else if (change === 'collapse') d.collapse(); else if (change === 'authority') d.retire(); else d.requests.dispose();
      assert.equal(lifetime.signal.aborted, true); assert.equal(lifetime.isCurrent(), false);
      assert.equal(d.listeners(), 0);
      d.pending.resolve(true); assert.equal(await request, false); assert.equal(d.errors.length, 0);
    } finally {d.requests.dispose();}
  }
});
