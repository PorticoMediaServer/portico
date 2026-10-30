import test from 'node:test';
import assert from 'node:assert/strict';
import {TogetherItemSummaries} from '../src/screens/together/item-summaries.ts';

const settle = async () => {for (let index = 0; index < 12; index++) await Promise.resolve();};
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}

test('a full queue is read once and retained through reorder, current-item changes and append', async () => {
  const read: string[] = [], done = deferred<void>();let values: ReadonlyMap<string, string> = new Map();
  const cache = new TogetherItemSummaries<string>({current: () => true, read: async id => {read.push(id);return id;}, reportError: error => {throw error;},
    publish: next => {values = next;if (values.size === 256) done.resolve();}});
  const ids = Array.from({length: 256}, (_, index) => String(index));
  cache.update('room', ids);await done.promise;await settle();assert.equal(read.length, 256);
  cache.update('room', [...ids].reverse());await settle();assert.equal(read.length, 256);
  cache.update('room', [ids[10]!, ...ids]);await settle();assert.equal(read.length, 256);
  cache.update('room', [...ids.slice(1), 'new']);await settle();assert.equal(read.length, 257);
  assert.equal(values.has('0'), false);assert.equal(values.get('new'), 'new');cache.dispose();
});
test('at most four real reads are in flight, and same-room updates keep them', async () => {
  const reads: {id: string; signal: AbortSignal; done: ReturnType<typeof deferred<string>>}[] = [];
  const cache = new TogetherItemSummaries<string>({current: () => true, read: (id, signal) => {const done = deferred<string>();reads.push({id, signal, done});return done.promise;}, publish: () => {}, reportError: error => {throw error;}});
  cache.update('room', ['a', 'b', 'c', 'd', 'e']);assert.equal(reads.length, 4);
  cache.update('room', ['e', 'd', 'c', 'b', 'a']);assert.equal(reads.length, 4);assert.ok(reads.every(read => !read.signal.aborted));
  reads[0]!.done.resolve('a');await settle();assert.equal(reads.length, 5);assert.equal(reads[4]!.id, 'e');cache.dispose();
});
test('removed, unavailable and old-room reads cannot republish late results', async () => {
  const reads: {id: string; signal: AbortSignal; done: ReturnType<typeof deferred<string>>}[] = [];let values: ReadonlyMap<string, string> = new Map();
  const cache = new TogetherItemSummaries<string>({current: () => true, read: (id, signal) => {const done = deferred<string>();reads.push({id, signal, done});return done.promise;}, publish: next => {values = next;}, reportError: error => {throw error;}});
  cache.update('one', ['a', 'b']);cache.update('one', ['b']);assert.equal(reads[0]!.signal.aborted, true);
  reads[0]!.done.resolve('a');await settle();assert.equal(values.has('a'), false);
  cache.update('two', ['b']);assert.equal(reads[1]!.signal.aborted, true);
  reads[1]!.done.resolve('old b');reads[2]!.done.resolve('new b');await settle();assert.equal(values.get('b'), 'new b');
  cache.update('', []);assert.equal(values.size, 0);cache.dispose();
});
test('catalogue invalidation refreshes affected summaries without clearing other libraries', async () => {
  const reads: string[] = [];let revision = 1, values: ReadonlyMap<string, {libraryId: string; revision: number}> = new Map();
  const cache = new TogetherItemSummaries<{libraryId: string; revision: number}>({current: () => true, read: async id => {reads.push(id);return {libraryId: id, revision};}, publish: next => {values = next;}, reportError: error => {throw error;}});
  cache.update('room', ['a', 'b']);await settle();revision = 2;cache.invalidate(value => value.libraryId === 'a');
  assert.equal(values.has('a'), false);assert.equal(values.get('b')?.revision, 1);
  await settle();assert.equal(values.get('a')?.revision, 2);assert.deepEqual(reads, ['a', 'b', 'a']);cache.dispose();
});
test('failures are reported once per update and retired viewer responses never publish', async () => {
  const failure = new Error('refused'), errors: unknown[] = [];let attempts = 0, current = true, values: ReadonlyMap<string, string> = new Map();const late = deferred<string>();
  const cache = new TogetherItemSummaries<string>({current: () => current, read: async id => {attempts++;if (id === 'bad') throw failure;return late.promise;}, publish: next => {values = next;}, reportError: error => {errors.push(error);}});
  cache.update('room', ['bad', 'late']);await settle();assert.deepEqual(errors, [failure]);assert.equal(attempts, 2);
  current = false;late.resolve('late');await settle();assert.equal(values.size, 0);cache.dispose();
});
