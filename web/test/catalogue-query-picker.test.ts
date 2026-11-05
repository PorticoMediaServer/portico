import assert from 'node:assert/strict';
import test from 'node:test';
import {LibraryPickerProducer} from '../src/screens/browse/selection-picker.ts';
import {wholeViewQuery} from '../src/screens/browse/selection-query.ts';
import {testQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

const revision = `browse_${'a'.repeat(64)}`;
function target() {
  const every = {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: true, edit: true, refresh: true, delete: true};
  const query = wholeViewQuery({libraryId: 'lib', pivot: 'movies', filter: null, sort: [{field: 'title', direction: 'asc'}],
    catalogRevision: revision, selectionBasis: 'b'.repeat(64), total: 2,
    types: [{kind: 'movie', count: 2, libraries: [{libraryId: 'lib', count: 2}], capabilities: Object.fromEntries(Object.keys(every).map(key => [key, 2]))}]} as never);
  return {mode: 'query', selector: query.selector, total: 2, sourceTotal: 2,
    summary: {count: 2, types: [{kind: 'movie', count: 2}], every, library: {kind: 'single', libraryId: 'lib'}},
    refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: revision}};
}

test('query picker refuses an invalid destination fence before creating its session', async () => {
  const calls: unknown[][] = []; let ids = 0, publications = 0;
  const api = {async request<T>(...args: unknown[]): Promise<T> {calls.push(args); return {viewerFence: 'borrowed-fence'} as T;}};
  const producer = new LibraryPickerProducer(target() as never, 'playlist', {api, queries: testQueries(api, {serverId: 's', viewerId: 'v'}),
    serverId: 's', viewerId: 'v', requestId: async () => {ids++; return 'request';}, isCurrent: () => true,
    pickerIdentity: {authority: 'local', accountId: 'a', profileId: 'p', events: {on: () => () => {}}}} as never, true, () => {publications++;});
  try {
    await assert.rejects(producer.open(), /picker-authority-required/);
    assert.equal(calls.length, 1); assert.deepEqual(calls[0].slice(0, 3), ['/v1/home/destination-picker', 'POST', {kind: 'playlist', query: '', limit: 40}]);
    assert.equal(ids, 0); assert.equal(publications, 0);
  } finally {producer.close();}
});

test('a retired query picker cannot create a destination session from a late valid fence', async () => {
  let current = true, finish!: (value: unknown) => void, sent!: AbortSignal, ids = 0, publications = 0;
  const api = {async request<T>(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> {
    sent = signal!; return new Promise(resolve => {finish = value => resolve(value as T);});
  }};
  const producer = new LibraryPickerProducer(target() as never, 'playlist', {api, queries: testQueries(api, {serverId: 's', viewerId: 'v'}),
    serverId: 's', viewerId: 'v', requestId: async () => {ids++; return 'request';}, isCurrent: () => current,
    pickerIdentity: {authority: 'local', accountId: 'a', profileId: 'p', events: {on: () => () => {}}}} as never, true, () => {publications++;});
  try {
    const work = producer.open();
    await new Promise(resolve => setImmediate(resolve));
    current = false; producer.close(); assert.equal(sent.aborted, true);
    finish({serverId: 's', viewerFence: 'f'.repeat(64), kind: 'playlist', query: '', revision: {catalog: 3, viewer: 2}, rows: [], nextCursor: 'opaque/+?='});
    await assert.rejects(work, {name: 'AbortError'});
    assert.equal(ids, 0); assert.equal(publications, 0);
  } finally {producer.close();}
});
