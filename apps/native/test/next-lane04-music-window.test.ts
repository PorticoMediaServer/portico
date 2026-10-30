import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackItemStore} from '../src-next/screens/destinations/music/playback-item.ts';
import {musicWindow, musicWindowShift} from '../src-next/screens/destinations/music/music-window.ts';
import {audiobookAuthorInitials} from '../src-next/screens/destinations/audiobooks/presentation.ts';

test('music item selector suppresses progress/buffering publications and observes item removal', () => {
  let snapshot = {playback: {itemId: 'first' as string | null, position: 0}}, calls = 0;
  const listeners = new Set<() => void>();
  const store = playbackItemStore({getSnapshot: () => snapshot, subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}});
  const off = store.subscribe(() => calls++);
  const publish = (itemId: string | null, position: number) => {snapshot = {playback: {itemId, position}}; for (const listener of listeners) listener();};
  for (let tick = 1; tick <= 600; tick++) publish('first', tick);
  assert.equal(calls, 0);
  assert.equal(store.getSnapshot(), 'first');
  publish('second', 0); assert.equal(calls, 1); assert.equal(store.getSnapshot(), 'second');
  publish(null, 0); assert.equal(calls, 2); assert.equal(store.getSnapshot(), undefined);
  off(); publish('third', 0); assert.equal(calls, 2); assert.equal(listeners.size, 0);
});

test('a missing playback port provides one stable primitive snapshot', () => {
  const store = playbackItemStore();
  for (let index = 0; index < 100; index++) assert.equal(store.getSnapshot(), undefined);
  store.subscribe(() => assert.fail('no producer'))();
});

test('600 retained songs mount only the viewport and two overscan rows on either side', () => {
  const visible = musicWindow(600, 1, 54, 100, 100 + 300 * 54, 540);
  assert.deepEqual(visible, Array.from({length: 15}, (_, index) => index + 298));
  assert.equal(new Set(visible).size, visible.length);
});

test('a distant focus pin adds just its host rather than the intervening album', () => {
  const regular = musicWindow(600, 1, 60, 300, 300 + 450 * 60, 600);
  const pinned = musicWindow(600, 1, 60, 300, 300 + 450 * 60, 600, 5);
  assert.deepEqual(new Set(pinned), new Set([...regular, 5]));
  assert.equal(pinned.length, regular.length + 1);
  assert.deepEqual(musicWindow(600, 1, 60, 300, 300 + 450 * 60, 600, 450), regular);
});

test('release grids keep complete visible rows and a bounded range for a million entries', () => {
  const indices = musicWindow(1_000_000, 6, 330, 1000, 1000 + 1500 * 330, 1080);
  assert.equal(indices[0], 1498 * 6);
  assert.equal(indices.length, 54);
  assert.equal(indices.at(-1), 1507 * 6 - 1);
  assert.deepEqual(musicWindow(0, 6, 330, 0, 0, 1080), []);
});

test('sections completely above or below the viewport mount only an actual pin', () => {
  assert.deepEqual(musicWindow(600, 1, 54, 10_000, 0, 800), []);
  assert.deepEqual(musicWindow(10, 1, 54, 0, 10_000, 800), []);
  assert.deepEqual(musicWindow(10, 1, 54, 0, 10_000, 800, 3), [3]);
  assert.deepEqual(musicWindow(10, 1, 54, 0, 10_000, 800, -1), []);
});

test('bounded pager eviction preserves the exact first visible song and its fractional offset', () => {
  const before = Array.from({length: 600}, (_, index) => String(index));
  const after = Array.from({length: 600}, (_, index) => String(index + 200));
  const local = 250 * 54 + 13, shift = musicWindowShift(before, after, 1, 54, local);
  assert.equal(shift, -200 * 54);
  const moved = local + shift;
  assert.equal(after[Math.floor(moved / 54)], before[Math.floor(local / 54)]);
  assert.equal(moved % 54, 13);
  assert.equal(musicWindowShift(before, after, 1, 54, -10), 0);
  assert.equal(musicWindowShift(before, after, 1, 54, 10), 0, 'a removed anchor cannot be invented');
});

test('author initials match people circles without splitting Unicode surrogate pairs', () => {
  assert.equal(audiobookAuthorInitials('  Andy   Weir  '), 'AW');
  assert.equal(audiobookAuthorInitials('J. R. R. Tolkien'), 'JR');
  assert.equal(audiobookAuthorInitials('Émile Zola'), 'ÉZ');
  assert.equal(audiobookAuthorInitials('𝒜 Author'), '𝒜A');
  assert.equal(audiobookAuthorInitials('   '), '');
});
