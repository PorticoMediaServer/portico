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

type Call = {path: string; method: string; body?: any};
const list = (id: string, extra: Record<string, unknown> = {}) => ({id, kind: 'playlist', name: id, owner: {id: 'me', name: 'Me'}, role: 'owner', count: 0, covers: [], revision: '2',
  createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', ...extra});
/** The rebuilt server for a whole-view picker: the profile's playlists, the view's titles, and additions. */
function server(viewTitles = 2) {
  const calls: Call[] = [];
  const api = {async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    calls.push({path, method, body});
    const url = new URL(path, 'https://x'), at = decodeURIComponent(url.pathname);
    if (method === 'GET' && at === '/v1/lists') return {items: [list('Road trip'), list('Theirs', {role: 'viewer'})]} as T;
    if (method === 'GET' && at === '/v1/lists/Road trip') return list('Road trip') as T;
    if (method === 'GET' && at === '/v1/items') return {items: Array.from({length: viewTitles}, (_, n) => ({id: `m${n}`}))} as T;
    if (method === 'POST' && at.endsWith('/entries')) return {list: list('Road trip', {count: viewTitles, revision: '3'}), added: (body as {items: string[]}).items.length, already: 0} as T;
    throw new Error(`unexpected ${method} ${path}`);
  }};
  return {api, calls};
}
const ports = (api: unknown, current = () => true) => ({api, queries: testQueries(api as never, {serverId: 's', viewerId: 'v'}), serverId: 's', viewerId: 'v',
  requestId: async () => '00000000-0000-4000-8000-000000000001', isCurrent: current, pickerIdentity: {authority: 'local', accountId: 'a', profileId: 'p', events: {on: () => () => {}}}}) as never;

test('a whole view goes into a playlist: the rows are the profile\'s playlists, and its titles are read in the app and added', async () => {
  const s = server();
  let shown: any;
  const producer = new LibraryPickerProducer(target() as never, 'playlist', ports(s.api), true, state => {shown = state;});
  try {
    await producer.open();
    assert.deepEqual(shown.rows.map((row: any) => [row.name, row.canAdd]), [['Road trip', true], ['Theirs', false]]);
    assert.equal(s.calls.some(call => call.path.includes('destination-picker')), false, 'nothing asks the old server\'s routes');
    await producer.choose('Road trip');
    const view = new URL(s.calls.find(call => call.path.startsWith('/v1/items'))!.path, 'https://x').searchParams;
    assert.deepEqual([view.get('library'), view.get('kind'), view.get('sort')], ['lib', 'movie', 'title']);
    assert.deepEqual(s.calls.filter(call => call.method === 'POST').map(call => [call.path, call.body.items]), [['/v1/lists/Road%20trip/entries', ['m0', 'm1']]]);
    assert.deepEqual([shown.phase, shown.receipt?.added], ['added', 2]);
  } finally {producer.close();}
});

test('a whole view of more than 500 titles is refused before anything is added, saying how many can be added at once', async () => {
  const s = server();
  const big = {...target(), total: 501, sourceTotal: 501, summary: {...target().summary, count: 501, types: [{kind: 'movie', count: 501}]}};
  let shown: any;
  const producer = new LibraryPickerProducer(big as never, 'playlist', ports(s.api), true, state => {shown = state;});
  try {
    await producer.open();
    await producer.choose('Road trip');
    assert.deepEqual([shown.phase, shown.error?.code, shown.error?.retryable], ['error', 'too-many-to-add', false]);
    assert.equal(s.calls.some(call => call.method === 'POST' || call.path.startsWith('/v1/items')), false);
  } finally {producer.close();}
});

test('a retired whole-view picker publishes nothing late', async () => {
  let current = true, publications = 0, finish!: (value: unknown) => void;
  const api = {async request<T>(_path: string): Promise<T> {return new Promise(resolve => {finish = value => resolve(value as T);});}};
  const producer = new LibraryPickerProducer(target() as never, 'playlist', ports(api, () => current), true, () => {publications++;});
  try {
    const work = producer.open().catch(() => {});
    await new Promise(resolve => setImmediate(resolve));
    const before = publications;
    current = false; producer.close();
    finish({items: [list('Road trip')]}); await work;
    assert.equal(publications, before);
  } finally {producer.close();}
});
