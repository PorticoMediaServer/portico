import test from 'node:test';
import assert from 'node:assert/strict';
import {createArtworkDecodeLifetime, createArtworkPaintLifetime, createArtworkVisualLifetime, type ArtworkDecodeElement, type ArtworkPaintElement} from '../src/kit/artwork/decode-lifetime.ts';
import {createPaintAdmission} from '../src/kit/artwork/visual-admission.ts';

function pending() {
  let resolve!: () => void, reject!: () => void;
  const promise = new Promise<void>((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}
const flush = async () => {await Promise.resolve(); await Promise.resolve();};

test('retiring an original lease withdraws both late decode outcomes and its callbacks', async () => {
  for (const failed of [false, true]) {
    const task = pending(), received: boolean[] = [];
    const element: ArtworkDecodeElement = {isConnected: true, decode: () => task.promise};
    const lease = createArtworkDecodeLifetime({isCurrent: () => true, receive: (_decode, failed) => received.push(failed)});
    lease.decode({src: 'original'}, element); lease.retire();
    if (failed) task.reject(); else task.resolve(); await flush();
    lease.fail({src: 'original'}, element);
    assert.deepEqual(received, []);
  }
});

test('same-key image replacement cannot borrow a connected successor element or callback', async () => {
  const first = pending(), next = pending(), oldImage = {key: 'same', src: 'old'}, image = {key: 'same', src: 'new'};
  let currentImage = oldImage, calls = 0;
  const received: unknown[] = [];
  const element: ArtworkDecodeElement = {isConnected: true, decode: () => ++calls === 1 ? first.promise : next.promise};
  const lease = createArtworkDecodeLifetime({isCurrent: decode => decode.image === currentImage,
    receive: (decode, failed) => received.push([decode.image, failed])});
  lease.decode(oldImage, element); currentImage = image; lease.decode(image, element);
  first.resolve(); await flush(); assert.deepEqual(received, []);
  next.resolve(); await flush(); assert.deepEqual(received, [[image, false]]);
});

test('an actual image error retires its pending decode result without forcing success', async () => {
  const task = pending(), received: boolean[] = [], image = {};
  const element: ArtworkDecodeElement = {isConnected: true, decode: () => task.promise};
  const lease = createArtworkDecodeLifetime({isCurrent: () => true, receive: (_decode, failed) => received.push(failed)});
  lease.decode(image, element); lease.fail(image, element); task.resolve(); await flush();
  assert.deepEqual(received, [true]);
});

test('disconnected or no-longer-current sources never publish either status outcome', async () => {
  for (const connected of [false, true]) {
    const task = pending(), received: boolean[] = []; let current = true;
    const element = {isConnected: true, decode: () => task.promise};
    const lease = createArtworkDecodeLifetime({isCurrent: () => current, receive: (_decode, failed) => received.push(failed)});
    lease.decode({}, element); element.isConnected = connected; current = !connected;
    task.reject(); await flush(); assert.deepEqual(received, []);
  }
});

test('synchronous decode refusal reports the original failure rather than succeeding', () => {
  const image = {}, received: unknown[] = [];
  const lease = createArtworkDecodeLifetime({isCurrent: () => true, receive: (decode, failed) => received.push([decode.image, failed])});
  lease.decode(image, {isConnected: true, decode: () => {throw new Error('decode refused');}});
  assert.deepEqual(received, [[image, true]]);
});

function visibleGroup() {
  const scope = {}, admissions = createPaintAdmission();
  return {
    art: (order: number) => createArtworkVisualLifetime<object, object>(() => admissions.mount({scope, group: 'actual-row', order})),
    pending: () => admissions.size(),
  };
}

test('loading to the first image preserves its mounted predecessor until genuine decode', () => {
  const row = visibleGroup(), first = row.art(0), next = row.art(1), a = {}, b = {}, seen: string[] = [];
  first.change(true); next.change(true);
  next.begin({}, b); next.decoded(b, false, () => true, () => seen.push('next'));
  assert.equal(seen.length, 0);
  first.begin({}, a); assert.equal(seen.length, 0, 'receiving bytes is not decode success or predecessor retirement');
  first.decoded(a, false, () => true, () => seen.push('first'));
  assert.deepEqual(seen, ['first', 'next']);
  assert.equal(row.pending(), 0);
});

test('a genuine cached decode bypasses both a pending predecessor and unknown visibility', () => {
  const row = visibleGroup(), pending = row.art(0), cached = row.art(1), owner = {}, seen: string[] = [];
  pending.change(true); cached.begin({}, owner);
  assert.equal(seen.length, 0, 'a cache flag alone never paints before decode');
  cached.decoded(owner, true, () => true, () => seen.push('cached'));
  assert.deepEqual(seen, ['cached']); assert.equal(row.pending(), 1);
  pending.retire(); cached.retire(); assert.equal(row.pending(), 0);
});

test('an offscreen or failed predecessor releases ready siblings without borrowing its callback', () => {
  for (const retire of ['offscreen', 'failed'] as const) {
    const row = visibleGroup(), first = row.art(0), next = row.art(1), a = {}, b = {}, seen: string[] = [];
    first.change(true); next.change(true); first.begin({}, a); next.begin({}, b);
    next.decoded(b, false, () => true, () => seen.push('next'));
    if (retire === 'offscreen') first.change(false); else first.fail();
    assert.deepEqual(seen, ['next']);
    first.decoded(a, false, () => true, () => seen.push('old'));
    assert.deepEqual(seen, ['next']);
    first.retire(); next.retire(); assert.equal(row.pending(), 0);
  }
});

test('a same-image new decode generation replaces the queued paint before retiring its old turn', () => {
  const row = visibleGroup(), anchor = row.art(0), first = row.art(1), next = row.art(2), image = {}, a = {}, old = {}, fresh = {}, b = {}, seen: string[] = [];
  anchor.change(true); first.change(true); next.change(true);
  anchor.begin({}, a); first.begin(image, old); next.begin({}, b);
  first.decoded(old, false, () => true, () => seen.push('old'));
  next.decoded(b, false, () => true, () => seen.push('next'));
  first.begin(image, fresh); anchor.decoded(a, false, () => true, () => seen.push('anchor'));
  assert.deepEqual(seen, ['anchor'], 'the later sibling cannot steal the replacement predecessor turn');
  first.decoded(old, false, () => true, () => seen.push('borrowed'));
  first.decoded(fresh, false, () => true, () => seen.push('fresh'));
  assert.deepEqual(seen, ['anchor', 'fresh', 'next']);
});

test('retirement withdraws an already-decoded paint callback and ignores late ready/visibility', () => {
  const row = visibleGroup(), first = row.art(0), next = row.art(1), a = {}, b = {}, seen: string[] = [];
  first.change(true); next.change(true); first.begin({}, a); next.begin({}, b);
  next.decoded(b, false, () => true, () => seen.push('retired'));
  next.retire(); next.change(true); next.decoded(b, true, () => true, () => seen.push('late-cache'));
  first.decoded(a, false, () => true, () => seen.push('first')); assert.deepEqual(seen, ['first']); assert.equal(row.pending(), 0);
});


function imageNode() {
  const attributes = new Map<string, string>();
  const node: ArtworkPaintElement = {isConnected: true,
    setAttribute: (name, value) => {attributes.set(name, value);},
    removeAttribute: name => {attributes.delete(name);},
  };
  return {node, admitted: () => attributes.has('data-artwork-admitted')};
}

test('admitted paint is published on the owned original node without a later React commit', () => {
  const {node, admitted} = imageNode(); let current = true;
  const original = createArtworkPaintLifetime(node, () => current);
  assert.equal(admitted(), false); original.reveal(); assert.equal(admitted(), true);
  current = false; original.retire(); assert.equal(admitted(), false);
  original.reveal(); assert.equal(admitted(), false);
});

test('a same-node replacement withdraws original paint and refuses stale reveal or cleanup', () => {
  const {node, admitted} = imageNode();
  const original = createArtworkPaintLifetime(node, () => true); original.reveal();
  const replacement = createArtworkPaintLifetime(node, () => true);
  assert.equal(admitted(), false, 'an actual replacement still needs its own decode/admission');
  original.reveal(); assert.equal(admitted(), false);
  replacement.reveal(); original.retire(); assert.equal(admitted(), true, 'old cleanup cannot erase successor paint');
  replacement.retire(); assert.equal(admitted(), false);
});

test('disconnected or no-longer-current paint nodes never publish a visual fact', () => {
  for (const connected of [false, true]) {
    const {node, admitted} = imageNode(); Object.assign(node, {isConnected: connected});
    const paint = createArtworkPaintLifetime(node, () => !connected); paint.reveal();
    assert.equal(admitted(), false); paint.retire();
  }
});
