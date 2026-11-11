import test from 'node:test';
import assert from 'node:assert/strict';
import {SearchService, searchFirstPages, searchHistoryQuery} from '../src/search.ts';
import {FreshnessHub, dependsOn} from '../src/freshness-hub.ts';
import {createViewerQueries} from '../src/query/viewer-queries.ts';

// Client data step 4: a search's first page is this viewer's cached document. One request per
// settled query however many ask; a search already read is shown again with none.
const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
/** The rebuilt server's answer: every group it searches, with their first results. */
function answer(_path: string) {
  return {groups: [{kind: 'movie', total: 1, items: [{id: 'one', kind: 'movie', title: 'One', libraryId: 'library'}]}, {kind: 'show', total: 1, items: [{id: 'two', kind: 'show', title: 'Two', libraryId: 'library'}]}]};
}
function setup() {
  const paths: string[] = [];
  const api = {async request<T>(path: string): Promise<T> {
    paths.push(path);
    if (path.startsWith('/v1/search/history')) return {items: [{query: 'harbor'}]} as T;
    return answer(path) as T;
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

test('a recording read is the server\'s, fills the cache, and reads Recent searches again', async () => {
  const f = setup(), s = f.service();
  await f.queries.client.fetchQuery(searchHistoryQuery(f.queries, scope));
  const reads = () => f.paths.filter(path => path.startsWith('/v1/search/history')).length;
  await s.select('harbor', {groups, record: true});
  assert.deepEqual(f.searches().map(path => new URL(path, 'https://x').searchParams.get('record')), ['true']);
  assert.equal(f.queries.client.getQueryState(searchHistoryQuery(f.queries, scope).queryKey)?.isInvalidated, true);
  // The same query typed again is the recorded answer, with no request.
  const typed = f.service(); void typed.select('harbor', {groups});
  assert.equal(typed.getSnapshot().phase, 'ready');
  assert.equal(f.searches().length, 1);
  assert.equal(reads(), 1);
  f.queries.dispose(); f.hub.dispose();
});

test('a hint\'s re-read of the search on screen asks the server again without recording it again', async () => {
  const f = setup(), s = f.service();
  await s.select('harbor', {groups, record: true});
  await s.revalidate();
  assert.deepEqual(f.searches().map(path => new URL(path, 'https://x').searchParams.get('record')), ['true', null]);
  f.queries.dispose(); f.hub.dispose();
});
