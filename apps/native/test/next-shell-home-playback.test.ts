import assert from 'node:assert/strict';
import test from 'node:test';
import {PlaybackDock, playbackFence, type PlaybackFence, type QueuePlaybackFence} from '../../../packages/player-core/src';
import {nativeShellBackTransition, type NativeShellVideoSnapshot} from '../src-next/shell/playback';
import {NativeVideoSnapshotCapture} from '../src-next/shell/video-snapshot';
import type {ShellAuthority} from '../src-next/shell/model';

const authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence: QueuePlaybackFence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});

function deferred<T>() {
  let fulfill: ((value: T) => void) | undefined, fail: ((error: unknown) => void) | undefined;
  const promise = new Promise<T>((resolve, reject) => {fulfill = resolve; fail = reject;});
  return {promise, resolve: (value: T) => {assert.ok(fulfill); fulfill(value);}, reject: (error: unknown) => {assert.ok(fail); fail(error);}};
}

function image(source: PlaybackFence = fence, release?: () => Promise<void>) {
  let released = 0;
  const value: NativeShellVideoSnapshot = {fence: source, image: {uri: 'file:///snapshot-test.jpg', width: 390, height: 220}, release: async () => {released++; await release?.();}};
  return {value, released: () => released};
}

function captures() {
  const requests: {fence: PlaybackFence; signal: AbortSignal | undefined; result: ReturnType<typeof deferred<NativeShellVideoSnapshot>>}[] = [];
  const started = deferred<void>(), nextStarted = deferred<void>(), shown: NativeShellVideoSnapshot[] = [], errors: unknown[] = [];
  let visible: NativeShellVideoSnapshot | undefined;
  const owner: NativeVideoSnapshotCapture = new NativeVideoSnapshotCapture({capture: (source, options) => {
    const result = deferred<NativeShellVideoSnapshot>(); requests.push({fence: source, signal: options?.signal, result}); started.resolve(); if (requests.length === 2) nextStarted.resolve(); return result.promise;
  }, changed: (source, value) => {assert.strictEqual(source, owner); visible = value; if (value) shown.push(value);}, reportError: error => {errors.push(error);}});
  return {owner, requests, started: started.promise, nextStarted: nextStarted.promise, shown, errors, visible: () => visible};
}

test('one current capture is reused, then its actual image is released exactly once on settlement', async () => {
  const state = captures(), frame = image();
  const pending = state.owner.prepare(fence, () => true);
  await state.started;
  const request = state.requests[0]; assert.ok(request);
  assert.strictEqual(state.owner.prepare(fence, () => true), pending);
  request.result.resolve(frame.value); await pending;
  assert.strictEqual(state.visible(), frame.value);
  await state.owner.prepare(fence, () => true);
  assert.equal(state.requests.length, 1);
  await state.owner.clear(); await state.owner.clear();
  assert.equal(state.visible(), undefined);
  assert.equal(frame.released(), 1);
  assert.equal(state.owner.matches(undefined), false);
  assert.deepEqual(state.errors, []);
});

test('clearing an in-flight capture aborts it and releases a late result without presenting it', async () => {
  const state = captures(), frame = image();
  const pending = state.owner.prepare(fence, () => true); await state.started;
  const request = state.requests[0]; assert.ok(request);
  const clearing = state.owner.clear();
  assert.equal(request.signal?.aborted, true);
  request.result.resolve(frame.value); await pending; await clearing;
  assert.deepEqual(state.shown, []);
  assert.equal(frame.released(), 1);
  assert.deepEqual(state.errors, []);
});

test('retiring before the native request starts never captures the obsolete source', async () => {
  const state = captures();
  const pending = state.owner.prepare(fence, () => true), clearing = state.owner.clear();
  await pending; await clearing;
  assert.equal(state.requests.length, 0);
  assert.deepEqual(state.shown, []);
  assert.deepEqual(state.errors, []);
});

test('authority or interaction retirement rejects a result even before cleanup runs', async () => {
  const state = captures(), frame = image(); let current = true;
  const pending = state.owner.prepare(fence, () => current); await state.started;
  const request = state.requests[0]; assert.ok(request);
  current = false; request.result.resolve(frame.value); await pending;
  assert.deepEqual(state.shown, []);
  assert.equal(frame.released(), 1);
  await state.owner.clear();
});

test('a capture from another playback generation is released without presenting it', async () => {
  const state = captures(), frame = image({...fence, generation: 2});
  const pending = state.owner.prepare(fence, () => true); await state.started;
  const request = state.requests[0]; assert.ok(request);
  request.result.resolve(frame.value); await pending;
  assert.deepEqual(state.shown, []);
  assert.equal(frame.released(), 1);
  await state.owner.clear();
});

test('an idle gap waits for a cancelled source release before another source captures', async () => {
  const state = captures(), released = deferred<void>(), releaseStarted = deferred<void>();
  const old = image(fence, () => {releaseStarted.resolve(); return released.promise;}), nextFence = {...fence, generation: 2}, next = image(nextFence);
  const first = state.owner.prepare(fence, () => true); await state.started;
  const original = state.requests[0]; assert.ok(original);
  const clearing = state.owner.clear();
  const second = state.owner.prepare(nextFence, () => true);
  original.result.resolve(old.value); await releaseStarted.promise;
  assert.equal(state.requests.length, 1);
  assert.equal(state.owner.matches(fence), false);
  released.resolve(); await first; await clearing; await state.nextStarted;
  const follow = state.requests[1]; assert.ok(follow);
  assert.equal(old.released(), 1);
  follow.result.resolve(next.value); await second;
  assert.deepEqual(state.shown, [next.value]);
  await state.owner.clear();
});

test('replacing a retained frame waits for its release before starting the next native capture', async () => {
  const state = captures(), released = deferred<void>(), releaseStarted = deferred<void>();
  const old = image(fence, () => {releaseStarted.resolve(); return released.promise;}), next = image({...fence, generation: 2});
  const first = state.owner.prepare(fence, () => true); await state.started;
  const original = state.requests[0]; assert.ok(original);
  original.result.resolve(old.value); await first;
  const second = state.owner.prepare(next.value.fence, () => true); await releaseStarted.promise;
  assert.equal(state.requests.length, 1);
  assert.equal(state.visible(), undefined);
  released.resolve(); await state.nextStarted;
  const follow = state.requests[1]; assert.ok(follow);
  follow.result.resolve(next.value); await second;
  assert.equal(old.released(), 1);
  assert.strictEqual(state.visible(), next.value);
  await state.owner.clear();
});

test('capture failures report only while their source is current', async () => {
  for (const current of [true, false]) {
    const state = captures(); let active = true;
    const pending = state.owner.prepare(fence, () => active); await state.started;
    const request = state.requests[0]; assert.ok(request);
    active = current; const failure = new Error('capture-failed'); request.result.reject(failure); await pending;
    assert.deepEqual(state.errors, current ? [failure] : []);
    assert.deepEqual(state.shown, []); await state.owner.clear();
  }
});

test('a release failure is reported and does not strand the capture owner', async () => {
  const state = captures(), failure = new Error('release-failed'), frame = image(fence, async () => {throw failure;});
  const pending = state.owner.prepare(fence, () => true); await state.started;
  const request = state.requests[0]; assert.ok(request);
  request.result.resolve(frame.value); await pending; await state.owner.clear();
  assert.equal(frame.released(), 1); assert.deepEqual(state.errors, [failure]);
  assert.equal(state.visible(), undefined);
  const next = image({...fence, generation: 2}), retry = state.owner.prepare(next.value.fence, () => true);
  await state.nextStarted;
  const follow = state.requests[1]; assert.ok(follow);
  follow.result.resolve(next.value); await retry;
  assert.strictEqual(state.visible(), next.value); await state.owner.clear();
});

test('replacing a capture owner cannot publish a retired image into the new source', async () => {
  const old = captures(), replacement = captures(), oldFrame = image(), newFrame = image();
  const first = old.owner.prepare(fence, () => true), second = replacement.owner.prepare(fence, () => true);
  await old.started; await replacement.started;
  const oldRequest = old.requests[0], newRequest = replacement.requests[0]; assert.ok(oldRequest); assert.ok(newRequest);
  const clearing = old.owner.clear();
  newRequest.result.resolve(newFrame.value); await second;
  oldRequest.result.resolve(oldFrame.value); await first; await clearing;
  assert.deepEqual(old.shown, []);
  assert.equal(oldFrame.released(), 1);
  assert.strictEqual(replacement.visible(), newFrame.value);
  assert.equal(newFrame.released(), 0);
  await replacement.owner.clear(); assert.equal(newFrame.released(), 1);
});

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
