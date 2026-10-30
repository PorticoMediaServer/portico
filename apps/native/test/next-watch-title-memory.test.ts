import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeTitleMemory} from '../src-next/screens/watch/title-memory.ts';

test('tabs, seasons and filters restore their own physical item and relative offset', () => {
  const memory = new NativeTitleMemory(true);
  memory.select({seasonId: 'season-1'}); memory.remember({itemId: 'episode-7', offset: 19});
  memory.select({tab: 'details'}); memory.remember({itemId: 'credits', offset: 44});
  assert.deepEqual(memory.select({tab: 'episodes'}).anchor, {itemId: 'episode-7', offset: 19});
  memory.select({seasonId: 'season-2'}); memory.remember({itemId: 'episode-12', offset: 7});
  memory.select({hideWatched: true}); memory.remember({itemId: 'episode-13', offset: 22});
  assert.deepEqual(memory.select({hideWatched: false}).anchor, {itemId: 'episode-12', offset: 7});
  assert.deepEqual(memory.select({seasonId: 'season-1'}).anchor, {itemId: 'episode-7', offset: 19});
});

test('geometry restoration cannot release panel/focus before bounds and anchor settle', () => {
  const memory = new NativeTitleMemory(true);
  memory.remember({itemId: 'episode-3', offset: 13});
  const token = memory.capture(null);
  assert.equal(memory.restored(token), false);
  assert.deepEqual(memory.settled(token), {itemId: 'episode-3', offset: 13});
  assert.equal(memory.restored(token), true);
  assert.equal(memory.restored(token), false);
  const old = memory.capture(null); memory.capture(null);
  assert.equal(memory.settled(old), undefined);
  const changed = memory.capture(null); memory.select({tab: 'details'});
  assert.equal(memory.settled(changed), undefined);
});

test('anchor validation refuses nonfinite offsets and selection never invents content counts', () => {
  const memory = new NativeTitleMemory(false);
  assert.equal(memory.getSnapshot().tab, 'related');
  for (const offset of [NaN, Infinity, -1]) assert.throws(() => memory.remember({itemId: 'real', offset}));
  assert.throws(() => memory.remember({itemId: '', offset: 0}));
  memory.select({group: 'range:1'});
  assert.deepEqual(memory.getSnapshot(), {tab: 'related', seasonId: null, group: 'range:1', hideWatched: false, anchor: {itemId: null, offset: 0}});
});
