import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules, Platform} from './react-native-stub.mjs';
import {coverImageMime, coverImageSupported, stageCoverImage, type CoverImageMime} from '../src-next/platform/cover-image';

type Bridge = {
  beginCoverImage?: (mime: string) => Promise<unknown>;
  appendCoverImage?: (handle: string, encoded: string) => Promise<unknown>;
  finishCoverImage?: (handle: string, size: number) => Promise<unknown>;
  deleteCoverImage?: (handle: string) => Promise<unknown>;
};
const native = NativeModules.PorticoDownloads as unknown as Bridge;
const signatures: Record<CoverImageMime, readonly number[]> = {
  'image/jpeg': [0xff, 0xd8, 0xff],
  'image/png': [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a],
  'image/webp': [0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50],
};
function image(mime: CoverImageMime, length: number) {
  const bytes = new Uint8Array(length);
  for (let index = 0; index < length; index++) bytes[index] = (index * 41) & 255;
  bytes.set(signatures[mime]); return bytes;
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {resolve = done;});
  return {promise, resolve};
}
/** A native module that stages chunks as the real ones do; `writes` are the finished files. */
function fixture(t: TestContext) {
  const saved = {...native}, tv = Platform.isTV;
  const stages = new Map<string, {mime: string; chunks: Buffer[]}>();
  const writes: {mime: string; encoded: string; chunks: number}[] = [], appends: string[] = [], deleted: string[] = [];
  Platform.isTV = false;
  native.beginCoverImage = async mime => {
    const handle = 'cover-' + (stages.size + 1); stages.set(handle, {mime, chunks: []}); return {handle};
  };
  native.appendCoverImage = async (handle, encoded) => {
    const stage = stages.get(handle)!; appends.push(encoded); stage.chunks.push(Buffer.from(encoded, 'base64'));
    return Buffer.concat(stage.chunks).length;
  };
  native.finishCoverImage = async (handle, size) => {
    const stage = stages.get(handle)!, bytes = Buffer.concat(stage.chunks);
    assert.equal(bytes.length, size);
    writes.push({mime: stage.mime, encoded: bytes.toString('base64'), chunks: stage.chunks.length});
    return {handle, uri: 'file:///private/' + handle, size: bytes.length};
  };
  native.deleteCoverImage = async handle => {deleted.push(handle);};
  t.after(() => {Object.assign(native, saved); Platform.isTV = tv;});
  const controller = new AbortController();
  return {writes, appends, deleted, stages, controller, lifetime: {signal: controller.signal, isCurrent: () => true}};
}

test('cover MIME admission uses the raster bytes, including short invalid signatures', () => {
  for (const mime of Object.keys(signatures) as CoverImageMime[]) {
    const bytes = image(mime, signatures[mime].length);
    assert.equal(coverImageMime(bytes), mime);
    assert.equal(coverImageMime(bytes.subarray(0, bytes.length - 1)), undefined);
  }
  assert.equal(coverImageMime(new Uint8Array()), undefined);
});

test('cover staging preserves every admitted byte, base64 tail and chunk boundary for all image types', {timeout: 5000}, async t => {
  const {writes, deleted, lifetime} = fixture(t);
  const sizes = [3, 4, 5, 24575, 24576, 24577, 49152, 49153];
  for (const length of sizes) {
    const bytes = image('image/jpeg', length), temp = await stageCoverImage(bytes, 'image/jpeg', lifetime);
    assert.ok(temp); assert.equal(temp.size, bytes.length); assert.ok(Object.isFrozen(temp));
    assert.equal(writes.at(-1)!.encoded, Buffer.from(bytes).toString('base64'));
    await temp.dispose();
  }
  for (const mime of ['image/png', 'image/webp'] as const) {
    const bytes = image(mime, 50001), temp = await stageCoverImage(bytes, mime, lifetime);
    assert.ok(temp); assert.equal(writes.at(-1)!.mime, mime);
    assert.equal(writes.at(-1)!.encoded, Buffer.from(bytes).toString('base64'));
    await temp.dispose();
  }
  assert.equal(deleted.length, writes.length);
});

test('large cover staging yields to queued UI work between native chunks', {timeout: 5000}, async t => {
  const {writes, lifetime} = fixture(t);
  let uiRan = false, chunks = 0;
  const ui = new Promise<void>(resolve => setTimeout(() => {uiRan = true; resolve();}, 0));
  const append = native.appendCoverImage!;
  native.appendCoverImage = async (handle, encoded) => {
    if (chunks++ > 0) assert.equal(uiRan, true, 'staging must yield an event-loop turn rather than only a promise microtask');
    return append(handle, encoded);
  };
  const temp = await stageCoverImage(image('image/jpeg', 256 * 1024), 'image/jpeg', lifetime);
  await ui; assert.ok(temp); assert.equal(writes.length, 1); assert.ok(chunks > 1); await temp.dispose();
});

test('no chunk sent to the native module is larger than its bound or the whole image', {timeout: 5000}, async t => {
  const {writes, appends, lifetime} = fixture(t), bytes = image('image/png', 100001);
  const temp = await stageCoverImage(bytes, 'image/png', lifetime);
  assert.ok(temp); assert.equal(writes[0]!.encoded, Buffer.from(bytes).toString('base64'));
  // 24 KiB per chunk: 32768 base64 characters, well inside the native 64 KiB bound.
  assert.ok(appends.every(chunk => chunk.length <= 32768)); assert.equal(appends.length, Math.ceil(100001 / (24 * 1024)));
  await temp.dispose();
});

test('the 10 MiB maximum uses bounded encoder inputs and retains byte accuracy', {timeout: 5000}, async t => {
  const {writes, lifetime} = fixture(t), original = globalThis.btoa;
  let largest = 0, calls = 0;
  t.mock.method(globalThis, 'btoa', (binary: string) => {largest = Math.max(largest, binary.length); calls++; return original(binary);});
  // This case checks the byte/encoder bounds; the preceding case checks real UI turns.
  // Drive chunk timers deterministically so host contention cannot consume the test deadline.
  t.mock.timers.enable({apis: ['setTimeout']});
  const bytes = image('image/png', 10 * 1024 * 1024);
  const staging = stageCoverImage(bytes, 'image/png', lifetime);
  let settled = false;
  staging.then(() => {settled = true;}, () => {settled = true;});
  for (let turns = 0; turns < 1000 && !settled; turns++) {t.mock.timers.tick(1); await Promise.resolve();}
  assert.equal(settled, true, 'staging completes within the bounded chunk schedule');
  const temp = await staging;
  assert.ok(temp); assert.equal(temp.size, bytes.length);
  assert.ok(calls > 1); assert.ok(largest <= 64 * 1024, 'no image-sized binary-string intermediate enters the encoder');
  assert.equal(writes[0]!.encoded, Buffer.from(bytes).toString('base64'));
  await temp.dispose();
});

test('encoding keeps its admitted byte witness when the caller mutates its array between yields', {timeout: 5000}, async t => {
  const {writes, lifetime} = fixture(t), bytes = image('image/png', 100001), expected = Buffer.from(bytes).toString('base64');
  const pending = stageCoverImage(bytes, 'image/png', lifetime);
  bytes.fill(0);
  const temp = await pending;
  assert.ok(temp); assert.equal(writes[0]!.encoded, expected); assert.equal(temp.size, 100001);
  await temp.dispose();
});

test('detaching the caller buffer during encoding cannot change the admitted image or length', {timeout: 5000}, async t => {
  const {writes, lifetime} = fixture(t), bytes = image('image/jpeg', 50001), expected = Buffer.from(bytes).toString('base64');
  const pending = stageCoverImage(bytes, 'image/jpeg', lifetime);
  structuredClone(bytes.buffer, {transfer: [bytes.buffer]});
  assert.equal(bytes.length, 0);
  const temp = await pending;
  assert.ok(temp); assert.equal(writes[0]!.encoded, expected); assert.equal(temp.size, 50001);
  await temp.dispose();
});

test('MIME mismatch and over-limit bytes are rejected before encoding or native staging', async t => {
  const {writes, lifetime} = fixture(t);
  t.mock.method(globalThis, 'btoa', () => {throw new Error('invalid input reached encoder');});
  await assert.rejects(stageCoverImage(image('image/jpeg', 20), 'image/png', lifetime), {code: 'unsupported_image'});
  const tooLarge = new Uint8Array(10 * 1024 * 1024 + 1); tooLarge.set(signatures['image/jpeg']);
  await assert.rejects(stageCoverImage(tooLarge, 'image/jpeg', lifetime), {code: 'invalid_image'});
  await assert.rejects(stageCoverImage(new Uint8Array(), 'image/png', lifetime), {code: 'unsupported_image'});
  assert.equal(writes.length, 0);
});

test('a cancelled or stale witness never starts encoding', async t => {
  const {writes, lifetime, controller} = fixture(t);
  t.mock.method(globalThis, 'btoa', () => {throw new Error('stale input reached encoder');});
  const bytes = image('image/jpeg', 20);
  assert.equal(await stageCoverImage(bytes, 'image/jpeg', {...lifetime, isCurrent: () => false}), undefined);
  controller.abort();
  assert.equal(await stageCoverImage(bytes, 'image/jpeg', lifetime), undefined);
  assert.equal(writes.length, 0);
});

test('cancellation during staging deletes the partial native stage and never finishes it', {timeout: 5000}, async t => {
  const {writes, deleted, lifetime, controller} = fixture(t);
  const pending = stageCoverImage(image('image/jpeg', 256 * 1024), 'image/jpeg', lifetime);
  controller.abort();
  assert.equal(await pending, undefined); assert.equal(writes.length, 0); assert.deepEqual(deleted, ['cover-1']);
});

test('document retirement during staging and cancellation just before finishing are fenced', {timeout: 5000}, async t => {
  const {writes, deleted, lifetime, controller} = fixture(t);
  let current = true;
  const pending = stageCoverImage(image('image/png', 50001), 'image/png', {...lifetime, isCurrent: () => current});
  current = false;
  assert.equal(await pending, undefined);
  const small = stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime);
  controller.abort();
  assert.equal(await small, undefined); assert.equal(writes.length, 0);
  assert.deepEqual(deleted, ['cover-1', 'cover-2']);
});

test('cancellation while the native finish is pending deletes only the returned owned handle', {timeout: 5000}, async t => {
  const {deleted, lifetime, controller} = fixture(t), gate = deferred<unknown>(), started = deferred<void>();
  native.finishCoverImage = () => {started.resolve(); return gate.promise;};
  const pending = stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime);
  await started.promise; controller.abort();
  gate.resolve({handle: 'cover-1', uri: 'file:///private/cover-1', size: 4});
  assert.equal(await pending, undefined); assert.deepEqual(deleted, ['cover-1']);
});

test('retirement while the native finish is pending also deletes the exact late handle', {timeout: 5000}, async t => {
  const {deleted, lifetime} = fixture(t), gate = deferred<unknown>(), started = deferred<void>();
  let current = true;
  native.finishCoverImage = () => {started.resolve(); return gate.promise;};
  const pending = stageCoverImage(image('image/jpeg', 4), 'image/jpeg', {...lifetime, isCurrent: () => current});
  await started.promise; current = false;
  gate.resolve({handle: 'cover-1', uri: 'file:///private/cover-1', size: 4});
  assert.equal(await pending, undefined); assert.deepEqual(deleted, ['cover-1']);
});

test('malformed native answers are rejected and the owned stage is cleaned up', async t => {
  const {deleted, lifetime} = fixture(t);
  for (const result of [
    null, {handle: '', uri: 'file:///private/empty', size: 4},
    {handle: 'another-cover', uri: 'file:///private/other', size: 4},
    {handle: 'HANDLE', uri: 'https://example.invalid/cover', size: 4},
    {handle: 'HANDLE', uri: 'file:///private/size', size: 5},
    {handle: 'HANDLE', uri: 'file:///private/size', size: 4.1},
  ]) {
    native.finishCoverImage = async handle => result && {...result, handle: result.handle === 'HANDLE' ? handle : result.handle};
    await assert.rejects(stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime), {code: 'invalid_image'});
  }
  assert.deepEqual(deleted, ['cover-1', 'cover-2', 'cover-3', 'cover-4', 'cover-5', 'cover-6']);
  // A stage the native module never named, or a chunk it miscounted, is refused the same way.
  native.beginCoverImage = async () => ({});
  await assert.rejects(stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime), {code: 'invalid_image'});
  native.beginCoverImage = async () => ({handle: 'miscounted'}); native.appendCoverImage = async () => 3;
  await assert.rejects(stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime), {code: 'invalid_image'});
  assert.equal(deleted.at(-1), 'miscounted');
});

test('a native staging failure deletes the stage and keeps the native error', async t => {
  const {deleted, lifetime} = fixture(t);
  native.appendCoverImage = async () => {throw Object.assign(new Error('write_failed'), {code: 'write_failed'});};
  await assert.rejects(stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime), {code: 'write_failed'});
  assert.deepEqual(deleted, ['cover-1']);
});

test('an admitted cover lease is disposed once across explicit disposal and abort', async t => {
  const {deleted, lifetime, controller} = fixture(t);
  const temp = await stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime);
  assert.ok(temp);
  await Promise.all([temp.dispose(), temp.dispose()]); controller.abort(); await temp.dispose();
  assert.deepEqual(deleted, [temp.handle]);
});

test('cover staging still refuses televisions and unavailable native bridges', async t => {
  const {lifetime} = fixture(t);
  assert.equal(coverImageSupported(), true);
  Platform.isTV = true;
  assert.equal(coverImageSupported(), false);
  await assert.rejects(stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime), {code: 'cover_image_unavailable'});
  Platform.isTV = false; native.appendCoverImage = undefined;
  assert.equal(coverImageSupported(), false);
  await assert.rejects(stageCoverImage(image('image/jpeg', 4), 'image/jpeg', lifetime), {code: 'cover_image_unavailable'});
});
