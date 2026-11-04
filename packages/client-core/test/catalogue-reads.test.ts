import test from 'node:test';
import assert from 'node:assert/strict';
import {readCatalogueItemResponse, readCatalogueItem, readPlaybackCatalogueItem, readLibraryDirectory, readLibraries} from '../src/query/catalogue-reads.ts';
import {parseMediaItem} from '../src/media-item.ts';
import {parseLibraryList} from '../src/library-list.ts';

test('catalogue reads retain the wire item, parsed durations and exact transport lifetime', async () => {
  const raw = {id: 'item', libraryId: 'lib', kind: 'movie', title: 'Movie', available: true, durationMs: 42000, progressMs: 12000, missing: false};
  const signal = new AbortController().signal, calls: unknown[][] = [];
  const api = {async request(...args: unknown[]) {calls.push(args);return raw;}};
  assert.equal(await readCatalogueItemResponse(api, 'item /?', signal), raw);
  assert.deepEqual(await readCatalogueItem(api, 'item /?', signal), parseMediaItem(raw));
  assert.deepEqual(calls, Array.from({length: 2}, () => ['/v1/items/item%20%2F%3F', 'GET', undefined, signal]));
});
test('directory reads retain the exact strict proof body and tolerant library list', async () => {
  const raw = {viewerFence: 'viewer', privacyFence: 'privacy', items: [{id: 'lib', name: 'Movies', kind: 'movie', summary: {empty: false}}, {id: 'lib', name: 'duplicate'}, {name: 'unreadable'}]};
  const signal = new AbortController().signal, calls: unknown[][] = [];
  const api = {async request(...args: unknown[]) {calls.push(args);return raw;}};
  const result = await readLibraryDirectory(api, signal);
  assert.equal(result.raw, raw, 'membership proof sees every original row');
  assert.deepEqual(result.value, parseLibraryList(raw));
  assert.equal(result.value.items.length, 1);
  assert.deepEqual(await readLibraries(api, signal), result.value);
  assert.deepEqual(calls, Array.from({length: 2}, () => ['/v1/libraries', 'GET', undefined, signal]));
});
test('catalogue operations do not add retries or mask transport and decode failures', async () => {
  const error = Object.assign(new Error('refused'), {status: 403});let calls = 0;
  const api = {async request() {calls++;throw error;}};
  await assert.rejects(readCatalogueItemResponse(api, 'id'), value => value === error);
  assert.equal(calls, 1);
  await assert.rejects(readCatalogueItem({async request() {return {variant: 'preparing'};}}, 'id'));
  await assert.rejects(readLibraries({async request() {return {items: null};}}));
});

test('playback metadata keeps three attempts, 8-second attempt timers and 1/3-second recovery', async () => {
  const delays: number[] = [], cleared: unknown[] = [];
  const timers = new Map<number, () => void>(); let timer = 0, calls = 0;
  const error = Object.assign(new Error('temporary'), {status: 503});
  const lifetime = {signal: new AbortController().signal, isCurrent: () => true,
    setTimer(callback: () => void, ms: number) {const id = ++timer;delays.push(ms);timers.set(id, callback);return id;},
    clearTimer(id: unknown) {cleared.push(id);timers.delete(id as number);}};
  const api = {async request() {calls++;throw error;}};
  const pending = readPlaybackCatalogueItem(api, 'item', lifetime);
  for (const delay of [1000, 3000]) {
    for (let n = 0; n < 8; n++) await Promise.resolve();
    assert.equal(delays.at(-1), delay);
    const id = timer, callback = timers.get(id)!; timers.delete(id);callback();
  }
  await assert.rejects(pending, value => value === error);
  assert.equal(calls, 3);
  assert.deepEqual(delays, [8000, 1000, 8000, 3000, 8000]);
  assert.equal(cleared.length, 3);
});
test('authority refusal never enters playback metadata recovery', async () => {
  let calls = 0;const error = Object.assign(new Error('denied'), {status: 403});const delays: number[] = [];
  await assert.rejects(readPlaybackCatalogueItem({async request() {calls++;throw error;}}, 'item', {
    signal: new AbortController().signal, isCurrent: () => true,
    setTimer(_callback, ms) {delays.push(ms);return 1;}, clearTimer() {},
  }), value => value === error);
  assert.equal(calls, 1);assert.deepEqual(delays, [8000]);
});
