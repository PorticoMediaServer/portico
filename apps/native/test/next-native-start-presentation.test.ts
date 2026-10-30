import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence} from '../../../packages/player-core/src';
import {captureNativeStartPresentation, nativeFirstVideoFrame, type NativeStartArtworkBounds} from '../src-next/platform/playback/start-presentation';

const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});
const other = playbackFence({...fence, sessionId: 'successor', playback: {...fence.playback, run: 2}});
const artwork = {uri: 'https://original.invalid/art', cacheKey: 'original:art', headers: {Authorization: 'synthetic-original'}};
const bounds = {x: 12, y: 80, width: 360, height: 240};
const tick = async () => {await Promise.resolve();await Promise.resolve();};
function fixture() {
  let original = true, art = true, source = true, off = 0, listener: (() => void) | undefined;
  let finish!: (value: NativeStartArtworkBounds | undefined) => void;
  const measured = new Promise<NativeStartArtworkBounds | undefined>(resolve => {finish = resolve;});
  const owner = captureNativeStartPresentation({artwork, bounds: measured, paintCurrent: () => art}, {
    current: () => original, sourceCurrent: () => source,
    subscribe: value => {listener = value;return () => {off++;listener = undefined;};}, changed: () => {},
  })!;
  return {owner, finish, off: () => off, retireOriginal: () => {original = false;listener?.();},
    moveArt: () => {art = false;listener?.();}, replaceSource: () => {source = false;listener?.();}};
}

// Source vectors only. No native measurement or renderer is fabricated by these fixtures.
test('paint requires the measured original art and one actual accepted fence', async () => {
  const f = fixture();
  assert.equal(f.owner.read(fence), undefined);
  f.finish(bounds);await tick();assert.equal(f.owner.read(fence), undefined);
  f.owner.bind(fence);
  const drawn = f.owner.read(fence)!;assert.ok(drawn);
  assert.deepEqual(drawn.bounds, bounds);assert.deepEqual(drawn.artwork, artwork);
  assert.ok(Object.isFrozen(drawn));assert.ok(Object.isFrozen(drawn.key));assert.ok(Object.isFrozen(drawn.artwork.headers));
  assert.equal(f.owner.read(other), undefined);
  f.owner.bind(other);assert.equal(f.owner.read(fence)?.key, drawn.key);assert.equal(f.owner.read(other), undefined);
  f.owner.retire();assert.equal(drawn.signal.aborted, true);assert.equal(drawn.paintCurrent(), false);assert.equal(f.off(), 1);
});

test('completion of the local start is not retirement of its original page pixels', async () => {
  const f = fixture();f.finish(bounds);await tick();f.owner.bind(fence);
  const drawn = f.owner.read(fence)!;
  // No command/controller is retained in this value or called on a paint read.
  assert.deepEqual(Object.keys(drawn).sort(), ['artwork', 'bounds', 'fence', 'key', 'paintCurrent', 'signal', 'subscribe']);
  assert.equal(drawn.paintCurrent(), true);assert.equal(drawn.signal.aborted, false);
  f.retireOriginal();assert.equal(drawn.paintCurrent(), false);assert.equal(drawn.signal.aborted, true);
});

for (const changed of ['retireOriginal', 'moveArt', 'replaceSource'] as const) test(`original ${changed} refuses both retained and late paint`, async () => {
  const f = fixture();f.owner.bind(fence);f[changed]();f.finish(bounds);await tick();
  assert.equal(f.owner.read(fence), undefined);assert.equal(f.off(), 1);
});

test('missing or unmeasured/invalid artwork geometry has no dock/window fallback', async () => {
  assert.equal(captureNativeStartPresentation(undefined, {current: () => true, sourceCurrent: () => true, subscribe: () => () => {}, changed: () => {}}), undefined);
  for (const value of [undefined, {...bounds, width: 0}, {...bounds, height: -1}, {...bounds, x: NaN}, {...bounds, y: Infinity}]) {
    const f = fixture();f.owner.bind(fence);f.finish(value);await tick();assert.equal(f.owner.read(fence), undefined);assert.equal(f.off(), 1);
  }
});

test('subscription-time retirement releases its original registration exactly once', () => {
  let current = true, off = 0;
  const owner = captureNativeStartPresentation({artwork, bounds: Promise.resolve(bounds), paintCurrent: () => true}, {
    current: () => current, sourceCurrent: () => true, subscribe: listener => {current = false;listener();return () => {off++;};}, changed: () => {},
  });
  assert.equal(owner, undefined);assert.equal(off, 1);
});

test('only the accepted renderer firstFrame binding can be projected as first-frame proof', () => {
  const binding = {lease: 'lease', intentId: 3, sessionId: 'session', generation: 1, itemId: 'item'};
  for (const type of ['ready', 'progress', 'playing', 'stalled']) assert.equal(nativeFirstVideoFrame({type, binding, instanceId: 'renderer'}, binding, 'native', 'renderer'), undefined);
  for (const field of ['lease', 'intentId', 'sessionId', 'generation', 'itemId'] as const)
    assert.equal(nativeFirstVideoFrame({type: 'firstFrame', binding: {...binding, [field]: 'successor'}, instanceId: 'renderer'}, binding, 'native', 'renderer'), undefined);
  assert.equal(nativeFirstVideoFrame({type: 'firstFrame', instanceId: 'renderer'}, binding, 'native', 'renderer'), undefined);
  assert.equal(nativeFirstVideoFrame({type: 'firstFrame', binding, instanceId: 'old-renderer'}, binding, 'native', 'renderer'), undefined);
  assert.equal(nativeFirstVideoFrame({type: 'firstFrame', binding, instanceId: 'renderer'}, binding, 'unknown', 'renderer'), undefined);
  const result = nativeFirstVideoFrame({type: 'firstFrame', binding, instanceId: 'renderer'}, binding, 'native', 'renderer')!;
  assert.deepEqual(result, {...binding, engine: 'native', instanceId: 'renderer'});assert.notEqual(result, binding);assert.ok(Object.isFrozen(result));
});

test('identical display values still create distinct original-start occurrences', async () => {
  const a = fixture(), b = fixture();a.finish(bounds);b.finish(bounds);await tick();a.owner.bind(fence);b.owner.bind(fence);
  assert.notEqual(a.owner.read(fence)!.key, b.owner.read(fence)!.key);a.owner.retire();b.owner.retire();
});

test('the original measured-view subscription retires paint without waiting for playback progress', async () => {
  let art = true, notify!: () => void, stopped = 0;
  const owner = captureNativeStartPresentation({artwork, bounds: Promise.resolve(bounds), paintCurrent: () => art,
    subscribe: listener => {notify = listener;return () => {stopped++;};}}, {
    current: () => true, sourceCurrent: () => true, subscribe: () => () => {stopped++;}, changed: () => {},
  })!;
  await tick();owner.bind(fence);const shown = owner.read(fence)!;
  art = false;notify();assert.equal(shown.signal.aborted, true);assert.equal(shown.paintCurrent(), false);assert.equal(stopped, 2);
});
