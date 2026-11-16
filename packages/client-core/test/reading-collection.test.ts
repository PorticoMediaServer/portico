import test from 'node:test';
import assert from 'node:assert/strict';
import {ReadingCollection} from '../src/reading-collection.ts';

// Books chosen in a Read library go into a collection on the rebuilt server's lists, as Home's add-to sheets do.
const books = [{id: 'b1', libraryId: 'lib', kind: 'ebook' as const, title: 'One'}, {id: 'b2', libraryId: 'lib', kind: 'comic' as const, title: 'Two'}];
const list = (id: string, name: string, count: number) => ({id, kind: 'collection', name, count, revision: '3', role: 'owner', covers: []});
function fixture() {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const api = {async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    calls.push({path, method, body});
    if (path.startsWith('/v1/lists?')) return {items: [list('lst_a', 'Favourites', 4)]} as T;
    if (path === '/v1/lists/lst_a/entries') return {list: list('lst_a', 'Favourites', 5), added: 1, already: 1} as T;
    if (path === '/v1/lists') return {...list('lst_new', 'Summer', 2)} as T;
    throw new Error('unexpected ' + path);
  }};
  const collection = new ReadingCollection({queries: {} as never, api: api as never, scope: {} as never, requestId: async () => 'key-1',
    signal: new AbortController().signal, isCurrent: () => true, canCreate: true}, books);
  return {calls, collection};
}

test('chosen books go into a chosen collection, saying how many went in and how many were there already', async () => {
  const {calls, collection} = fixture();
  await collection.load();
  const ready = collection.getSnapshot();
  assert.deepEqual(ready.rows.map(row => row.name), ['Favourites']);
  assert.match(calls[0]!.path, /^\/v1\/lists\?kind=collection/);
  await collection.choose(ready, 'lst_a');
  const done = collection.getSnapshot();
  assert.deepEqual(calls.at(-1), {path: '/v1/lists/lst_a/entries', method: 'POST', body: {items: ['b1', 'b2']}});
  assert.deepEqual([done.phase, done.destination?.name, done.destination?.entryCount, done.receipt?.added, done.receipt?.alreadyPresent], ['result', 'Favourites', 5, 1, 1]);
});

test('a new collection is made with the chosen books in it', async () => {
  const {calls, collection} = fixture();
  await collection.create(' Summer ');
  assert.deepEqual(calls.at(-1), {path: '/v1/lists', method: 'POST', body: {kind: 'collection', name: 'Summer', items: ['b1', 'b2']}});
  assert.deepEqual([collection.getSnapshot().phase, collection.getSnapshot().destination?.name, collection.getSnapshot().receipt?.added], ['result', 'Summer', 2]);
});
