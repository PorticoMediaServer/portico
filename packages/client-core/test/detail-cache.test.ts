import test from 'node:test';
import assert from 'node:assert/strict';
import {DetailService, readItemDetail} from '../src/detail.ts';
import {queryKeys} from '../src/query/keys.ts';
import {testQueries} from './fixtures/viewer-queries.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};
const page = (revision = 1, favorite = false) => ({scope: {serverId: 'server', libraryId: 'films', itemId: 'film', viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1},
  item: {id: 'film', libraryId: 'films', kind: 'movie', title: 'Film', available: true, durationMs: 1000, progressMs: 0},
  personal: {watchlisted: false, favorite, watched: false, rating: null, revision, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: []},
  actions: [{id: 'favorite', labelKey: 'title.favorite', enabled: true}]});

function server(answer: (path: string, method?: string, body?: any) => unknown) {
  const calls: string[] = [];
  return {calls, api: {request: async <T>(path: string, method = 'GET', body?: unknown) => {calls.push(method + ' ' + path); return answer(path, method, body) as T;}}};
}

test('a title is read once for its page, its route guard and a menu, and drawn with no request when it is opened again', async () => {
  const s = server(() => page());
  const queries = testQueries(s.api, scope);
  const first = new DetailService({queries, api: s.api, scope, requestId: async () => 'id'});
  await Promise.all([first.select({libraryId: 'films', itemId: 'film'}, {related: 'none'}), readItemDetail(queries, 'film', 'none'), readItemDetail(queries, 'film', 'none')]);
  assert.deepEqual(s.calls, ['GET /v1/items/film/detail?related=none']);
  first.dispose();
  const back = new DetailService({queries, api: s.api, scope, requestId: async () => 'id'});
  void back.select({libraryId: 'films', itemId: 'film'}, {related: 'none'});
  assert.equal(back.getSnapshot().phase, 'ready', 'drawn on its first frame');
  assert.equal(s.calls.length, 1, 'and nothing is read');
  back.dispose(); queries.dispose();
});

test('a title in another library than the route names is not that route\'s page', async () => {
  const s = server(() => page());
  const queries = testQueries(s.api, scope);
  const service = new DetailService({queries, api: s.api, scope, requestId: async () => 'id'});
  await service.select({libraryId: 'shows', itemId: 'film'}, {related: 'none'});
  assert.equal(service.getSnapshot().data, null);
  assert.equal(service.getSnapshot().phase, 'error');
  service.dispose(); queries.dispose();
});

test('a write\'s receipt reaches every cached form of the title, and a slower older read never undoes it', async () => {
  let held: ((value: unknown) => void) | undefined;
  const s = server((path, method, body) => method === 'PUT'
    ? {operationId: body.operationId, serverId: 'server', libraryId: 'films', itemId: 'film', viewerFence: 'fence', current: page(2, true).personal}
    : path.endsWith('related=all') && !held ? new Promise(resolve => { held = resolve; }) : page());
  const queries = testQueries(s.api, scope);
  queries.client.setQueryData(queryKeys.itemDetail('film', 'all'), await readItemDetail(queries, 'film', 'none'));
  const service = new DetailService({queries, api: s.api, scope, requestId: async () => '12345678-1234-4123-8123-000000000001'});
  await service.select({libraryId: 'films', itemId: 'film'}, {related: 'none'});
  await service.mutate({action: 'favorite', value: true});
  for (const related of ['none', 'all'] as const) assert.equal(queries.client.getQueryData<any>(queryKeys.itemDetail('film', related)).personal.favorite, true, related);
  // A read that left before the write lands after it: the page keeps the write.
  const late = queries.client.fetchQuery({...(await import('../src/detail.ts')).itemDetailQuery(queries, 'film', 'all'), staleTime: 0});
  await new Promise(resolve => setTimeout(resolve, 5));
  held?.(page(1, false));
  assert.equal((await late).personal.favorite, true);
  service.dispose(); queries.dispose();
});
