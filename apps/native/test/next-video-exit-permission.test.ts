import test from 'node:test';
import assert from 'node:assert/strict';
import type {PlaybackFence} from '../../../packages/player-core/src/index.ts';
import {retainNativeVideoExitImage} from '../src-next/platform/playback/video-exit-permission.ts';

// Source fixtures only; no native image/physical handover claim.
const fence: PlaybackFence = {kind: 'queue', sessionId: 'original', generation: 1, sourceId: 'source', itemId: 'movie', entryId: 'entry',
  playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}};
function fixture() {
  const nativeLife = new AbortController(), retiring = new Set<() => Promise<void>>(), listeners = new Set<() => void>();
  let source = true, page = true, releases = 0, calls = 0;
  const image = Object.freeze({uri: 'file:///original-private-frame.jpg', width: 160, height: 90});
  const original = {sourceCurrent: () => source, paintCurrent: () => page,
    subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
    retain: async () => {calls++;return {image, signal: nativeLife.signal, release: async () => {releases++;nativeLife.abort();}};}};
  return {original, retiring, nativeLife, image, source: (value: boolean) => {source = value;},
    page: (value: boolean) => {page = value;for (const listener of [...listeners]) listener();},
    read: () => ({releases, calls, subscriptions: listeners.size})};
}

test('live original file transfer survives playback retirement, never admits a retired source or successor image', async () => {
  const d = fixture(), permit = await retainNativeVideoExitImage(fence, d.original, d.retiring);assert.ok(permit);
  assert.equal(permit.image, d.image);assert.deepEqual(permit.fence, fence);
  d.source(false);assert.equal(permit.paintCurrent(), true);assert.equal(permit.signal.aborted, false);
  assert.equal(await retainNativeVideoExitImage(fence, d.original, d.retiring), undefined);
  assert.equal(d.read().calls, 1);await Promise.all([permit.release(), permit.release()]);assert.equal(d.read().releases, 1);
  assert.equal(permit.paintCurrent(), false);assert.equal(d.retiring.size, 0);assert.equal(d.read().subscriptions, 0);
});

test('original page retirement is monotonic, independent of local playback replacement', async () => {
  const d = fixture(), permit = await retainNativeVideoExitImage(fence, d.original, d.retiring);assert.ok(permit);
  let invalidations = 0;permit.subscribe(() => {invalidations++;});
  d.page(false);assert.equal(permit.paintCurrent(), false);d.page(true);assert.equal(permit.paintCurrent(), false);
  await permit.release();assert.equal(d.read().releases, 1);assert.equal(invalidations, 1);
});

test('native expiry/background revocation and runtime retirement release exactly one original resource', async () => {
  for (const kind of ['native', 'runtime']) {
    const d = fixture(), permit = await retainNativeVideoExitImage(fence, d.original, d.retiring);assert.ok(permit);
    if (kind === 'native') d.nativeLife.abort();else for (const retire of [...d.retiring]) await retire();
    assert.equal(permit.signal.aborted, true);assert.equal(permit.paintCurrent(), false);await permit.release();
    assert.equal(d.read().releases, 1);assert.equal(d.retiring.size, 0);
  }
});

test('retirement during native transfer releases late original file and cannot manufacture a permit', async () => {
  const d = fixture();let resolve!: (value: Awaited<ReturnType<typeof d.original.retain>>) => void;
  const pending = retainNativeVideoExitImage(fence, {...d.original, retain: () => new Promise(done => {resolve = done;})}, d.retiring);
  const original = await d.original.retain();d.source(false);resolve(original);
  assert.equal(await pending, undefined);assert.equal(d.read().releases, 1);assert.equal(d.retiring.size, 0);
});

test('a synchronously retired or throwing page subscription refuses and unsubscribes without file IO', async () => {
  const d = fixture();let off = 0;
  assert.equal(await retainNativeVideoExitImage(fence, {...d.original, subscribe(listener) {d.page(false);listener();return () => {off++;};}}, d.retiring), undefined);
  assert.equal(off, 1);assert.equal(d.read().calls, 0);
  assert.equal(await retainNativeVideoExitImage(fence, {...d.original, sourceCurrent: () => {throw new Error('retired');}}, d.retiring), undefined);
});


test('late refused owner transfer resolves without waiting for a hung image release', async () => {
  for (const reason of ['source', 'page']) {
    const d = fixture();let resolve!: (value: Awaited<ReturnType<typeof d.original.retain>>) => void, cleanup = 0;
    const pending = retainNativeVideoExitImage(fence, {...d.original, retain: () => new Promise(done => {resolve = done;})}, d.retiring);
    if (reason === 'source') d.source(false);else d.page(false);
    resolve({image: d.image, signal: d.nativeLife.signal, release: () => {cleanup++;return new Promise<void>(() => {});}});
    assert.equal(await pending, undefined);assert.equal(cleanup, 1);assert.equal(d.retiring.size, 0);
  }
});
