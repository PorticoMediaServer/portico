import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeHistorySource, historyLocation} from '../src-next/screens/settings-live/server/history-source.ts';
import type {HistoryReader} from '../src-next/screens/settings-live/server/history-source.ts';
import type {PlayHistoryPage, PlayHistoryEntry, PlayHistoryFilters} from '../../../packages/client-core/src/play-history';

const report = {timeZone: 'UTC', until: '2026-10-28T12:00:00.000Z'};
const entry = (id: string) => ({id, user: 'sam_k', accountId: 'a1', kind: 'movie', title: 'Film', libraryId: 'l1', startedAt: '2026-10-28T11:00:00Z', positionSeconds: 4, durationSeconds: 90, completed: false}) as PlayHistoryEntry;
const page = (entries: readonly PlayHistoryEntry[], nextCursor = '', total?: number) => ({...report, entries, nextCursor, ...(total === undefined ? {} : {total})}) as PlayHistoryPage;
const settle = () => new Promise(resolve => setImmediate(resolve));
function deferred<T>() {let resolve!: (value: T) => void, reject!: (error: unknown) => void;const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});return {resolve, reject, promise};}

test('history cursor pages retain one report context, dedupe entries, and stop repeated cursors', async () => {
  const calls: {filters: PlayHistoryFilters; cursor: string}[] = [];
  const source = new NativeHistorySource({page: async (filters, cursor) => {calls.push({filters, cursor});return cursor ? page([entry('one'), entry('two')], 'next') : page([entry('one')], 'next', 2);}, people: async () => ({items: [], nextCursor: ''})}, () => true, () => report, error => {throw error;});
  source.start();await settle();source.more();source.more();await settle();
  assert.equal(calls.length, 2);assert.deepEqual(calls.map(call => [call.filters.until, call.filters.timeZone]), [[report.until, 'UTC'], [report.until, 'UTC']]);
  assert.deepEqual(source.getSnapshot().entries.map(entry => entry.id), ['one', 'two']);assert.equal(source.getSnapshot().total, 2);assert.equal(source.getSnapshot().nextCursor, '');source.more();assert.equal(calls.length, 2);source.dispose();
});

test('changing filters aborts the previous page and ignores its late reply', async () => {
  const requests: {filters: PlayHistoryFilters; signal: AbortSignal; result: ReturnType<typeof deferred<PlayHistoryPage>>}[] = [];
  const reader: HistoryReader = {page: (filters, _cursor, signal) => {const result = deferred<PlayHistoryPage>();requests.push({filters, signal, result});return result.promise;}, people: async () => ({items: [], nextCursor: ''})};
  const source = new NativeHistorySource(reader, () => true, () => report, error => {throw error;});source.start();
  source.change({period: '7d', media: 'movies', accountId: 'a2', search: 'film', sort: 'title', direction: 'asc'});
  assert.equal(requests[0].signal.aborted, true);assert.equal(requests[1].filters.media, 'movies');assert.equal(requests[1].filters.accountId, 'a2');assert.equal(requests[1].filters.search, 'film');
  requests[1].result.resolve(page([entry('new')]));await settle();requests[0].result.resolve(page([entry('stale')]));await settle();assert.deepEqual(source.getSnapshot().entries.map(entry => entry.id), ['new']);source.dispose();
});

test('failed reads keep an error distinct from an empty result and retry the correct cursor', async () => {
  let attempt = 0;const cursors: string[] = [];
  const source = new NativeHistorySource({page: async (_filters, cursor) => {cursors.push(cursor);if (++attempt === 2) throw new Error('offline');return attempt === 1 ? page([entry('one')], 'next', 2) : page([entry('two')]);}, people: async () => ({items: [], nextCursor: ''})}, () => true, () => report, error => {throw error;});
  source.start();await settle();source.more();await settle();assert.equal(source.getSnapshot().phase, 'error');assert.equal(source.getSnapshot().entries.length, 1);
  source.retry();await settle();assert.deepEqual(cursors, ['', 'next', 'next']);assert.equal(source.getSnapshot().phase, 'ready');assert.equal(source.getSnapshot().entries.length, 2);source.dispose();
});

test('person filters page through real account IDs and never borrow a profile as the username', async () => {
  const requests: string[] = [];
  const source = new NativeHistorySource({page: async () => page([]), people: async (_filters, cursor) => {requests.push(cursor);return cursor ? {items: [{accountId: 'a1', username: 'sam_k'}, {accountId: 'a2', username: 'nana_r'}], nextCursor: 'p2'} : {items: [{accountId: 'a1', username: 'sam_k'}], nextCursor: 'p2'};}}, () => true, () => report, error => {throw error;});
  source.start();await settle();source.morePeople();source.morePeople();await settle();assert.deepEqual(requests, ['', 'p2']);assert.deepEqual(source.getSnapshot().people, [{accountId: 'a1', username: 'sam_k'}, {accountId: 'a2', username: 'nana_r'}]);assert.equal(source.getSnapshot().peopleCursor, '');source.dispose();
});

test('retiring a History owner aborts reads and prevents late data or new requests', async () => {
  const held = deferred<PlayHistoryPage>();let signal: AbortSignal | undefined, reads = 0, active = true;
  const source = new NativeHistorySource({page: (_filters, _cursor, next) => {reads++;signal = next;return held.promise;}, people: async () => ({items: [], nextCursor: ''})}, () => active, () => report, error => {throw error;});
  source.start();active = false;source.dispose();assert.equal(signal!.aborted, true);held.resolve(page([entry('stale')]));await settle();source.refresh();assert.equal(reads, 1);assert.deepEqual(source.getSnapshot().entries, []);
});

test('unknown location text never exposes an address', () => {
  assert.equal(historyLocation('remote'), 'away');assert.equal(historyLocation('local'), 'home');assert.equal(historyLocation('192.0.2.5'), undefined);assert.equal(historyLocation(undefined), undefined);
});
