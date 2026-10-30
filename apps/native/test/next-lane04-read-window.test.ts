import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeReadingWindow, type NativeReadingWindowState} from '../src-next/screens/read/reading-window';

const items = (start: number, length: number) => Array.from({length}, (_, index) => ({id: `book-${start + index}`}));
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function fixture() {
  let value: NativeReadingWindowState<{id: string}> = {items: items(0, 60), cursor: 'page-two', loading: false};
  const listeners = new Set<() => void>();
  let calls = 0, next: () => Promise<void> = async () => {};
  const collection = new NativeReadingWindow(() => value, listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    () => {calls++; return next();}, item => item.id);
  return {collection, listeners, calls: () => calls, reply: (fn: () => Promise<void>) => {next = fn;},
    publish: (next: NativeReadingWindowState<{id: string}>) => {value = next; for (const listener of [...listeners]) listener();}};
}

test('reading grid prefetches ten items before the real cursor and deduplicates that cursor', async () => {
  const f = fixture(), stop = f.collection.subscribe(() => {});
  try {
    f.collection.ensureRange(0, 49); assert.equal(f.calls(), 0);
    f.collection.ensureRange(40, 50); f.collection.ensureRange(45, 59); assert.equal(f.calls(), 1);
    f.publish({items: items(0, 60), cursor: 'page-two', loading: true});
    f.collection.ensureRange(50, 60); assert.equal(f.calls(), 1);
    f.publish({items: items(0, 120), cursor: 'page-three', loading: false});
    f.collection.ensureRange(100, 109); assert.equal(f.calls(), 1);
    f.collection.ensureRange(100, 110); assert.equal(f.calls(), 2);
    await tick();
  } finally {stop(); f.collection.dispose();}
});

test('reading grid exposes stable real items and only one unknown continuation slot', () => {
  const f = fixture();
  try {
    const before = f.collection.getSnapshot(); assert.equal(f.collection.getSnapshot(), before);
    assert.equal(before.total, 61); assert.equal(before.totalIsLowerBound, true);
    assert.deepEqual(f.collection.slotAt(59), {kind: 'item', index: 59, key: 'book-59', item: {id: 'book-59'}});
    assert.equal(f.collection.slotAt(60).kind, 'placeholder');
    f.publish({items: items(0, 120), loading: false});
    assert.equal(f.collection.getSnapshot().total, 120); assert.equal(f.collection.getSnapshot().totalIsLowerBound, false);
    assert.equal(f.collection.keyAt(59), 'book-59'); assert.equal(f.collection.keyAt(119), 'book-119');
  } finally {f.collection.dispose();}
});

test('a failed continuation stays visible and requires explicit retry instead of a request loop', async () => {
  const f = fixture(), failure = new Error('unreachable');
  f.reply(async () => {throw failure;});
  try {
    f.collection.ensureRange(50, 59); await tick();
    assert.equal(f.collection.getSnapshot().error, failure); assert.equal(f.collection.getSnapshot().status, 'error');
    assert.equal(f.collection.itemAt(0)?.id, 'book-0');
    f.collection.ensureRange(50, 60); assert.equal(f.calls(), 1);
    f.reply(async () => {}); f.collection.retry(); assert.equal(f.calls(), 2);
    assert.equal(f.collection.getSnapshot().error, undefined);
  } finally {f.collection.dispose();}
});

test('mount and unmount subscribe lazily, can reattach, and retirement prevents more IO', () => {
  const f = fixture();
  assert.equal(f.listeners.size, 0, 'render-time construction has no subscriptions');
  const stop = f.collection.subscribe(() => {}); assert.equal(f.listeners.size, 1);
  stop(); assert.equal(f.listeners.size, 0);
  const again = f.collection.subscribe(() => {}); assert.equal(f.listeners.size, 1);
  again(); f.collection.dispose(); f.collection.ensureRange(0, 60); assert.equal(f.calls(), 0);
});
