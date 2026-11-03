import test from 'node:test';
import assert from 'node:assert/strict';
import {SearchService, searchFirstPages, searchHistoryQuery} from '../src/search.ts';
import {FreshnessHub, dependsOn} from '../src/freshness-hub.ts';
import {createViewerQueries} from '../src/query/viewer-queries.ts';

// Client data step 4: a search's first page is this viewer's cached document. One request per
// settled query however many ask; a search already read is shown again with none.
const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
function answer(path: string, recorded = false) {
  const url = new URL(path, 'https://server.test'), groups = url.searchParams.get('groups')?.split(',') ?? ['movies'];
  return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1}, heading: {key: 'search.title', fallback: 'Search'},
    query: {q: url.searchParams.get('q'), sort: 'relevance', direction: 'desc', group: '', groups, libraryIds: [], limit: Number(url.searchParams.get('limit')), searchMode: 'normalized_prefix', recorded},
    groups: groups.map(id => ({id, title: id, entityKind: 'movie', status: 'success', errorCode: '', items: [{id: 'one', libraryId: 'library', kind: 'movie', title: 'One', navigation: {view: 'item', entityId: 'one'}}], totalCount: 1, hasMore: false, nextCursor: ''})),
    capabilities: {groups: groups.map(id => ({id, title: id, entityKind: 'movie', available: true, sorts: ['relevance']})), sorts: ['relevance'], directions: ['asc', 'desc'], maxLimit: 40, groupBudgetMs: 600}};
}
function setup(recorded = true) {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    if (path.startsWith('/v1/search/history')) return {serverId: 'server', entries: [{query: 'harbor', updatedAt: '2026-11-02T00:00:00Z'}], maxEntries: 20} as T;
    return answer(path, recorded && path.includes('record=1')) as T;
  }};
  const hub = new FreshnessHub(), queries = createViewerQueries({scope, hub, api});
  const service = () => new SearchService({api, scope, firstPages: searchFirstPages(queries)});
  return {paths, hub, queries, service, searches: () => paths.filter(path => path.startsWith('/v1/search?'))};
}
const groups = ['movies', 'shows'] as const;

test('two screens asking for the same search share one read, and the search is shown again with none', async () => {
  const f = setup(), one = f.service(), two = f.service();
  await Promise.all([one.select('harbor', {groups}), two.select('harbor', {groups})]);
  assert.equal(f.searches().length, 1);
  assert.equal(two.getSnapshot().phase, 'ready');
  // Back to Search: a new screen's service shows the cached answer at once, before any await.
  const back = f.service();
  void back.select('harbor', {groups});
  assert.equal(back.getSnapshot().phase, 'ready');
  assert.equal(back.getSnapshot().data?.groups[0]?.items[0]?.id, 'one');
  assert.equal(f.searches().length, 1);
  f.queries.dispose(); f.hub.dispose();
});

test('a search whose titles moved is read again when it is next asked for', async () => {
  const f = setup(), first = f.service();
  await first.select('harbor', {groups});
  assert.equal(f.searches().length, 1);
  f.hub.note(dependsOn.content());
  for (let i = 0; i < 5; i++) await new Promise(resolve => setTimeout(resolve, 0));
  const again = f.service();
  await again.select('harbor', {groups});
  assert.equal(again.getSnapshot().phase, 'ready');
  assert.equal(f.searches().length, 2, 'the moved search is read again, not shown from the cache');
  f.queries.dispose(); f.hub.dispose();
});

test('a recording read is the server\'s, fills the cache, and reads Recent searches again only when the server recorded it', async () => {
  const f = setup(), s = f.service();
  await f.queries.client.fetchQuery(searchHistoryQuery(f.queries, scope));
  const reads = () => f.paths.filter(path => path.startsWith('/v1/search/history')).length;
  await s.select('harbor', {groups, record: true});
  assert.deepEqual(f.searches().map(path => new URL(path, 'https://x').searchParams.get('record')), ['1']);
  assert.equal(f.queries.client.getQueryState(searchHistoryQuery(f.queries, scope).queryKey)?.isInvalidated, true);
  // The same query typed again is the recorded answer, with no request.
  const typed = f.service(); void typed.select('harbor', {groups});
  assert.equal(typed.getSnapshot().phase, 'ready');
  assert.equal(f.searches().length, 1);
  assert.equal(reads(), 1);
  const off = setup(false), quiet = off.service();
  await off.queries.client.fetchQuery(searchHistoryQuery(off.queries, scope));
  await quiet.select('harbor', {groups, record: true});
  assert.equal(off.queries.client.getQueryState(searchHistoryQuery(off.queries, scope).queryKey)?.isInvalidated, false, 'history off on another device: nothing to read again');
  for (const value of [f, off]) { value.queries.dispose(); value.hub.dispose(); }
});

test('a hint\'s re-read of the search on screen asks the server again without recording it again', async () => {
  const f = setup(), s = f.service();
  await s.select('harbor', {groups, record: true});
  await s.revalidate();
  assert.deepEqual(f.searches().map(path => new URL(path, 'https://x').searchParams.get('record')), ['1', null]);
  f.queries.dispose(); f.hub.dispose();
});
