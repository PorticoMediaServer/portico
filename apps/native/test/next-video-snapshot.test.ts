import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {NativeModules, Platform, emit} from './react-native-stub.mjs';
import {createVideoSnapshotCapture, VideoSnapshotError, type VideoSnapshotSource} from '../src-next/platform/video-snapshot.ts';
const modules = NativeModules as unknown as Record<string, unknown>;
const source: VideoSnapshotSource = {reactTag: 42, engine: 'native', lease: 'lease-A', binding: {intentId: 3, sessionId: 'session-A', generation: 7, itemId: 'item-A'}};
const failure = (code: VideoSnapshotError['code']) => (error: unknown) => error instanceof VideoSnapshotError && error.code === code;
const deferred = <T>() => { let resolve!: (value: T) => void, reject!: (reason: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return {promise, resolve, reject}; };
type Result = {id: string; uri: string; width: number; height: number};
const image = (id: string): Result => ({id, uri: 'file:///private/cache/frame.png', width: 1280, height: 720});
async function withBridge(run: () => Promise<void>) {
  const prior = modules.PorticoVideoSnapshot, os = Platform.OS, tv = Platform.isTV;
  Platform.OS = 'android'; Platform.isTV = false;
  try { await run(); } finally { modules.PorticoVideoSnapshot = prior; Platform.OS = os; Platform.isTV = tv; }
}

test('capture forwards the mounted tag, engine, lease and binding; returns a real immutable image handle', () => withBridge(async () => {
  const calls: {id: string; value: VideoSnapshotSource}[] = [], released: string[] = [];
  modules.PorticoVideoSnapshot = {capture: async (id: string, value: VideoSnapshotSource) => { calls.push({id, value}); return image(id); }, release: async (id: string) => { released.push(id); return true; }};
  const helper = createVideoSnapshotCapture({isCurrent: value => value.lease === source.lease});
  const result = await helper.capture({...source, privateCredential: 'must-not-cross-bridge', binding: {...source.binding, privateSource: 'must-not-cross-bridge'}} as VideoSnapshotSource);
  assert.deepEqual(calls[0].value, source);
  assert.notEqual(calls[0].value, source);
  assert.ok(Object.isFrozen(result.source.binding));
  assert.deepEqual(result.image, {uri: 'file:///private/cache/frame.png', width: 1280, height: 720});
  await assert.rejects(helper.capture(source), failure('snapshot_busy'));
  await Promise.all([result.release(), result.release()]);
  assert.deepEqual(released, [calls[0].id]);
  const mpv = await helper.capture({...source, engine: 'mpv'});
  assert.equal(calls[1].value.engine, 'mpv');
  await mpv.release(); helper.dispose();
}));

test('unsupported, absent and malformed sources decline before invoking capture', () => withBridge(async () => {
  let requests = 0;
  const helper = createVideoSnapshotCapture({isCurrent: () => true});
  modules.PorticoVideoSnapshot = {capture: async (id: string) => { requests++; return image(id); }, release: async () => true};
  for (const invalid of [{...source, reactTag: 0}, {...source, reactTag: 1.5}, {...source, lease: ''}, {...source, binding: {...source.binding, generation: NaN}}, {...source, binding: {...source.binding, sessionId: ''}}]) {
    await assert.rejects(helper.capture(invalid), failure('snapshot_unavailable'));
  }
  Platform.isTV = true; await assert.rejects(helper.capture(source), failure('snapshot_unavailable'));
  Platform.isTV = false; Platform.OS = 'windows'; await assert.rejects(helper.capture(source), failure('snapshot_unavailable'));
  Platform.OS = 'android'; delete modules.PorticoVideoSnapshot;
  await assert.rejects(helper.capture(source), failure('snapshot_unavailable'));
  assert.equal(requests, 0); helper.dispose();
}));

test('Apple phone and tablet admit the installed capture bridge; TV and absent Apple bridges decline', () => withBridge(async () => {
  Platform.OS = 'ios';
  let requests = 0, releases = 0;
  const helper = createVideoSnapshotCapture({isCurrent: () => true});
  modules.PorticoVideoSnapshot = {capture: async (id: string, value: VideoSnapshotSource) => { requests++; assert.deepEqual(value, source); return image(id); }, release: async () => { releases++; return true; }};
  const result = await helper.capture(source); await result.release();
  assert.equal(requests, 1); assert.equal(releases, 1);
  Platform.isTV = true; await assert.rejects(helper.capture(source), failure('snapshot_unavailable'));
  Platform.isTV = false; delete modules.PorticoVideoSnapshot;
  await assert.rejects(helper.capture(source), failure('snapshot_unavailable'));
  assert.equal(requests, 1); helper.dispose();
}));

test('source and authority guards reject both before and after asynchronous capture and delete late bytes', () => withBridge(async () => {
  let current = false, id = '', requests = 0;
  const released: string[] = [], native = deferred<Result>();
  modules.PorticoVideoSnapshot = {capture: (value: string) => { id = value; requests++; return native.promise; }, release: async (value: string) => { released.push(value); return true; }};
  const helper = createVideoSnapshotCapture({isCurrent: () => current});
  await assert.rejects(helper.capture(source), failure('snapshot_stale'));
  assert.equal(requests, 0); current = true;
  const pending = helper.capture(source);
  current = false; native.resolve(image(id));
  await assert.rejects(pending, failure('snapshot_stale'));
  assert.ok(released.includes(id)); helper.dispose();
}));

test('cancel rejects promptly, preserves one flight and deletes a late successful native result', () => withBridge(async () => {
  const native = deferred<Result>(), released: string[] = [];
  let id = '';
  modules.PorticoVideoSnapshot = {capture: (value: string) => { id = value; return native.promise; }, release: async (value: string) => { released.push(value); return true; }};
  const helper = createVideoSnapshotCapture({isCurrent: () => true});
  const pending = helper.capture(source);
  await assert.rejects(helper.capture(source), failure('snapshot_busy'));
  helper.cancel();
  await assert.rejects(pending, failure('snapshot_cancelled'));
  const before = released.length;
  native.resolve(image(id)); await new Promise<void>(resolve => setImmediate(resolve));
  assert.ok(released.length > before);
  helper.dispose(); await assert.rejects(helper.capture(source), failure('snapshot_stale'));
}));

test('AbortSignal cancels pending work and disposal deletes retained files without repeating release', () => withBridge(async () => {
  const released: string[] = [], native = deferred<Result>();
  let id = '';
  modules.PorticoVideoSnapshot = {capture: (value: string) => { id = value; return native.promise; }, release: async (value: string) => { released.push(value); return true; }};
  const helper = createVideoSnapshotCapture({isCurrent: () => true}), abort = new AbortController();
  const pending = helper.capture(source, {signal: abort.signal}); abort.abort();
  await assert.rejects(pending, failure('snapshot_cancelled'));
  native.resolve(image(id)); await new Promise<void>(resolve => setImmediate(resolve));
  modules.PorticoVideoSnapshot = {capture: async (value: string) => image(value), release: async (value: string) => { released.push(value); return true; }};
  const retained = await helper.capture(source), before = released.length;
  helper.dispose(); await retained.release();
  assert.equal(released.length, before + 1);
}));

test('pre-aborted capture and a throwing current guard never invoke native', () => withBridge(async () => {
  const abort = new AbortController(); abort.abort();
  const helper = createVideoSnapshotCapture({isCurrent: () => true});
  await assert.rejects(helper.capture(source, {signal: abort.signal}), failure('snapshot_cancelled'));
  helper.dispose();
  const broken = createVideoSnapshotCapture({isCurrent: () => { throw new Error('Authority gone'); }});
  await assert.rejects(broken.capture(source), failure('snapshot_stale')); broken.dispose();
}));

test('native errors and invalid pixel or URI receipts fail explicitly and release request resources', () => withBridge(async () => {
  const released: string[] = [];
  let receipt = (id: string): Result => ({...image(id), width: 4000});
  modules.PorticoVideoSnapshot = {capture: async (id: string) => receipt(id), release: async (id: string) => { released.push(id); return true; }};
  const helper = createVideoSnapshotCapture({isCurrent: () => true});
  for (const make of [(id: string) => ({...image(id), width: 4000}), (id: string) => ({...image(id), height: 0}), (id: string) => ({...image(id), uri: 'https://example.invalid/art.jpg'}), () => image('another-request')]) {
    receipt = make;
    await assert.rejects(helper.capture(source), failure('snapshot_unavailable'));
  }
  modules.PorticoVideoSnapshot = {capture: async () => { throw new Error('Secure/DRM/unready'); }, release: async (id: string) => { released.push(id); return false; }};
  await assert.rejects(helper.capture(source), failure('snapshot_unavailable'));
  assert.equal(released.length, 5); helper.dispose();
}));

test('hung native capture times out even if cleanup hangs and still queues late-image disposal', () => withBridge(async () => {
  const native = deferred<Result>(), released: string[] = [];
  let id = '';
  modules.PorticoVideoSnapshot = {capture: (value: string) => { id = value; return native.promise; }, release: (value: string) => { released.push(value); return new Promise<boolean>(() => {}); }};
  const helper = createVideoSnapshotCapture({isCurrent: () => true});
  await assert.rejects(helper.capture(source), failure('snapshot_timeout'));
  native.resolve(image(id)); await new Promise<void>(resolve => setImmediate(resolve));
  assert.ok(released.filter(value => value === id).length >= 2); helper.dispose();
}));

test('native source protocol copies only current leased surfaces with bounded, private, cancellable ownership', () => {
  const read = (name: string) => readFileSync(new URL(`../android/app/src/main/java/tv/getportico/portico/native/${name}.kt`, import.meta.url), 'utf8');
  const capture = read('PorticoVideoSnapshot');
  assert.match(capture, /PixelCopy\.request\(target\.surface, bitmap/);
  assert.match(capture, /result != PixelCopy\.SUCCESS/);
  assert.match(capture, /FLAG_SECURE/); assert.match(capture, /!TelevisionDisplay\.isTelevision/);
  assert.match(capture, /1280\.0/); assert.match(capture, /921600\.0/); assert.match(capture, /postDelayed\(work\.timeout, 1500\)/);
  assert.match(capture, /flight != null \|\| retained != null/);
  assert.match(capture, /context\.cacheDir/); assert.match(capture, /file\?\.delete\(\)/);
  assert.match(capture, /onHostPause/); assert.match(capture, /onViewDetachedFromWindow/); assert.match(capture, /surfaceDestroyed/);
  assert.match(capture, /work\.bitmap\.recycle\(\)/);
  assert.doesNotMatch(capture, /getDrawingCache|drawToBitmap|setSecure\(|addFlags|setVideoSurface|surfaceAvailable|startActivity/);
  for (const name of ['PorticoPlayerView', 'PorticoMpvPlayerView']) {
    const view = read(name); assert.match(view, /snapshotIdentity\(/); assert.match(view, /expected\["lease"\] == lease/);
    assert.match(view, /snapshotIdentity\([^\n]+\) == token/);
  }
  const media3 = read('PorticoPlaybackController'), mpv = read('PorticoMpvController');
  assert.match(media3, /drmConfiguration != null/); assert.match(media3, /drmInitData != null/);
  assert.match(media3, /firstFrameItem != itemInstanceId/);
  assert.match(mpv, /surfaceOwner !== owner \|\| surface !== target/);
  assert.match(mpv, /loadEpoch, core/);
});


// File ownership vectors only: native pixel/policy behavior requires containing-device checks.
test('only a live settled original can transfer once; source cancel cannot release claimed file pixels', () => withBridge(async () => {
  let id = '', current = true, captures = 0, claims = 0, releases = 0;
  modules.PorticoVideoSnapshot = {
    capture: async (value: string) => {id = value;captures++;return image(value);},
    release: async () => {releases++;return true;}, addListener() {}, removeListeners() {},
    retainForExit: async (value: string, offered: VideoSnapshotSource) => {claims++;assert.equal(value, id);assert.deepEqual(offered, source);return true;},
  };
  const helper = createVideoSnapshotCapture({isCurrent: () => current}), frame = await helper.capture(source);
  const retained = await frame.retainForExit!();assert.ok(retained);
  assert.equal(await frame.retainForExit!(), undefined);assert.equal(claims, 1);
  current = false;helper.cancel();await frame.release();
  assert.equal(releases, 0);assert.equal(retained.signal.aborted, false);assert.equal(captures, 1);
  emit('PorticoVideoSnapshotRetired', {id: 'successor-id'});assert.equal(retained.signal.aborted, false);
  emit('PorticoVideoSnapshotRetired', {id});assert.equal(retained.signal.aborted, true);
  await Promise.all([retained.release(), retained.release()]);assert.equal(releases, 1);
  helper.dispose();
}));

test('retirement before transfer declines without native call; retirement during transfer releases its exact file', () => withBridge(async () => {
  let current = true, claims = 0, releases = 0;
  const admission = deferred<boolean>();
  modules.PorticoVideoSnapshot = {capture: async (id: string) => image(id), release: async () => {releases++;return true;},
    addListener() {}, removeListeners() {}, retainForExit: () => {claims++;return admission.promise;}};
  const helper = createVideoSnapshotCapture({isCurrent: () => current}), frame = await helper.capture(source);
  current = false;assert.equal(await frame.retainForExit!(), undefined);assert.equal(claims, 0);
  current = true;const pending = frame.retainForExit!();current = false;admission.resolve(true);
  assert.equal(await pending, undefined);assert.equal(releases, 1);
  // Failed claim has spent/released this file; it cannot be retained afterwards.
  current = true;assert.equal(await frame.retainForExit!(), undefined);
  helper.dispose();
}));

test('native refusal, missing transfer API and disposal cannot create passive permission or a second capture', () => withBridge(async () => {
  let captures = 0, releases = 0;
  modules.PorticoVideoSnapshot = {capture: async (id: string) => {captures++;return image(id);},release: async () => {releases++;return true;}};
  const helper = createVideoSnapshotCapture({isCurrent: () => true}), unavailable = await helper.capture(source);
  assert.equal(await unavailable.retainForExit!(), undefined);await unavailable.release();
  modules.PorticoVideoSnapshot = {capture: async (id: string) => {captures++;return image(id);}, release: async () => {releases++;return true;},
    addListener() {},removeListeners() {},retainForExit: async () => true};
  const frame = await helper.capture(source), passive = await frame.retainForExit!();assert.ok(passive);
  await assert.rejects(helper.capture(source), failure('snapshot_busy'));
  helper.dispose();assert.equal(passive.signal.aborted, true);await passive.release();
  assert.equal(captures, 2);assert.equal(releases, 2);
}));


test('source cancellation promptly declines a hung transfer without delaying on native release', () => withBridge(async () => {
  let releases = 0;
  modules.PorticoVideoSnapshot = {capture: async (id: string) => image(id),
    release: () => {releases++;return new Promise<boolean>(() => {});},addListener() {},removeListeners() {},
    retainForExit: () => new Promise<boolean>(() => {})};
  const helper = createVideoSnapshotCapture({isCurrent: () => true}), frame = await helper.capture(source);
  const claim = frame.retainForExit!();helper.cancel();
  assert.equal(await claim, undefined);assert.equal(releases, 1);helper.dispose();
}));
