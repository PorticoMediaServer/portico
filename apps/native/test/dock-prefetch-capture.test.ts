import {dockFence} from './helpers/dock-fence.ts';
/** Authored UNRUN source vectors for the existing UI capture owner. No native
 * capture, rendering, file transfer, gesture timing or image proof is inferred. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeVideoSnapshotCapture} from '../src-next/shell/video-snapshot.ts';
import type {PlaybackFence} from '../../../packages/player-core/src/index.ts';
import type {NativeShellVideoSnapshot} from '../src-next/shell/playback.ts';
const fence = dockFence;
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
const frame = (value: PlaybackFence, release = async () => {}) => ({fence: value, image: {uri: 'file:///actual-source', width: 1280, height: 720}, release});

test('the original mini begin witness is checked after an earlier file finishes releasing', async () => {
  const release = deferred<void>(), calls: PlaybackFence[] = [], errors: unknown[] = [];
  let mini = true;
  const helper = new NativeVideoSnapshotCapture({capture: async value => {calls.push(value); return frame(value, calls.length === 1 ? () => release.promise : undefined);}, changed() {}, reportError: error => errors.push(error)});
  await helper.prepare(fence('old'), () => true);
  const optional = helper.prepare(fence('next'), () => true, () => mini);
  mini = false; release.resolve(); await optional;
  assert.equal(calls.length, 1, 'no capture starts after the original mini witness retires');
  assert.deepEqual(errors, []); await helper.clear();
});

test('a normal original gesture/layout request replaces pending optional prefetch and keeps its own guard', async () => {
  const release = deferred<void>(), calls: PlaybackFence[] = [], errors: unknown[] = [];
  const helper = new NativeVideoSnapshotCapture({capture: async value => {calls.push(value); return frame(value, calls.length === 1 ? () => release.promise : undefined);}, changed() {}, reportError: error => errors.push(error)});
  await helper.prepare(fence('old'), () => true);
  const next = fence('next');
  const optional = helper.prepare(next, () => true, () => false);
  const ordinary = helper.prepare(next, () => true);
  assert.equal(helper.prepare(next, () => true, () => false), ordinary, 'optional prefetch cannot replace an ordinary pending request');
  release.resolve(); await Promise.all([optional, ordinary]);
  assert.equal(calls.length, 2);
  assert.equal(calls[1], next);
  assert.deepEqual(errors, []); await helper.clear();
});

test('after a capture actually begins, a same-source result remains a normal morph image without claiming exit permission', async () => {
  const captured = deferred<ReturnType<typeof frame>>(), value = fence('original');
  let mini = true, image: NativeShellVideoSnapshot | undefined, calls = 0;
  const helper = new NativeVideoSnapshotCapture({capture: async () => {calls++; return captured.promise;},
    changed(_owner, next) {image = next;}, reportError: error => {throw error;}});
  const work = helper.prepare(value, () => true, () => mini);
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
  assert.equal(calls, 1);
  mini = false; captured.resolve(frame(value)); await work;
  assert.equal(image?.fence, value, 'post-capture ownership is still the original live source, not a latest fallback');
  assert.equal(image?.retainForExit, undefined, 'ordinary readiness is no passive exit permission');
  await helper.clear();
});
