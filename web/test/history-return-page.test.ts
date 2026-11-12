import test from 'node:test';
import assert from 'node:assert/strict';
import {DetailService, type DetailProjection} from '@core/detail.ts';
import {LibraryContentService} from '@core/library-content.ts';
import {ShowWorkspaceService, type ShowWorkspaceRoute} from '@core/show-workspace.ts';
import {testQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';
import {queryKeys} from '@core/query/keys.ts';

// J-15: a route left and opened again (browser Back and Forward, a cover, a card tapped
// twice) must draw the page it had at once and read it again in place. The viewer's page
// memory is the route's own last page; the service adopts it as its state, so every
// command still runs against what is on screen.
const scope = {serverId: 'server', viewerId: 'hosted/account/profile'};
const route: ShowWorkspaceRoute = {libraryId: 'library', showId: 'show'};
const filmPage = {item: {id: 'film', kind: 'movie', title: 'Film'}, scope: {serverId: 'server', libraryId: 'library', itemId: 'film', viewerFence: 'fence'},
  revision: {catalog: 1, viewer: 1}, personal: {watchlisted: false, favorite: false, watched: false, rating: null, revision: 1, progressSeconds: 0, lastPlayedAt: '', status: 'current', conflicts: []}, actions: []} as unknown as DetailProjection;

function showDto(entries = ['ep1', 'ep2']): any {
  return {scope: {serverId: 'server', libraryId: 'library', viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1},
    show: {id: 'show', libraryId: 'library', title: 'The Harbor', year: 2024, libraryKind: 'tv', providerMatchStatus: 'unresolved'},
    seasons: [{id: 'season1', showId: 'show', number: 1, title: 'Season 1'}], seasonTotalCount: 1, nextSeasonCursor: '', groups: [],
    selected: {seasonId: 'season1'},
    episodes: {scope: {serverId: 'server', libraryId: 'library', libraryKind: 'tv', view: 'season', entityId: 'season1', viewerFence: 'fence'},
      revision: {catalog: 1, viewer: 1}, heading: {key: 'entity.title', fallback: 'The Harbor · Episodes'}, navigation: [], query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 2, searchMode: 'none'},
      sorts: [], filters: [], sections: [{id: 'episodes', type: 'list', heading: {key: 'content.episodes', fallback: 'Episodes'}, totalCount: entries.length, nextCursor: '',
        entries: entries.map(id => ({id, kind: 'episode', title: id, available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id, startMs: 0}}))}]}};
}

test('a title page read before is drawn from the viewer\'s cache at once, with no request, and keeps it while it reads again', async () => {
  let reads = 0;
  const api = {request: async () => {reads++; return new Promise(() => {});}};
  const queries = testQueries(api, scope);
  queries.client.setQueryData(queryKeys.itemDetail('film', 'none'), filmPage);
  const service = new DetailService({queries, api, scope, requestId: async () => 'request'});
  void service.select({libraryId: 'library', itemId: 'film'}, {related: 'none'});
  assert.equal(service.getSnapshot().phase, 'ready', 'drawn on the first frame');
  assert.equal(service.getSnapshot().data, filmPage);
  assert.equal(reads, 0, 'nothing moved, so nothing is read');
  void service.refresh();
  assert.equal(service.getSnapshot().phase, 'ready', 'a read in place is not a first load');
  assert.equal(service.getSnapshot().data, filmPage, 'the page stays while it is read again');
  assert.equal(reads, 1);
  service.dispose(); queries.dispose();
});

test('an album or artist page read before is drawn from the viewer\'s cache at once, with no request', async () => {
  let reads = 0;
  // The rebuilt server's answers: the album, its songs and its library. One page read is these
  // three requests, kept as one entry of the viewer's cache.
  const answer = (path: string) => {
    const u = new URL(path, 'https://server.test');
    if (u.pathname === '/v1/items') return {items: [{id: 'song', kind: 'track', title: 'Song', index: 1, parentIndex: 1}], total: 1};
    if (u.pathname.startsWith('/v1/libraries/')) return {id: 'library', name: 'Music', kind: 'music'};
    return {id: 'album', kind: 'album', title: 'Album', libraryId: 'library'};
  };
  const queries = testQueries({request: async (path: string) => {reads++; return answer(path);}}, scope);
  const first = new LibraryContentService({queries, scope});
  await first.select({libraryId: 'library', view: 'album', entityId: 'album'});
  assert.equal(first.getSnapshot().phase, 'ready', String(first.getSnapshot().error?.code));
  assert.equal(first.getSnapshot().projection?.entity?.id, 'album');
  first.dispose();
  const before = reads;
  const second = new LibraryContentService({queries, scope});
  void second.select({libraryId: 'library', view: 'album', entityId: 'album'});
  assert.equal(second.getSnapshot().phase, 'ready', 'Back draws the page on the first frame');
  assert.equal(second.getSnapshot().projection?.entity?.id, 'album');
  assert.equal(reads, before, 'Back costs no request');
  second.dispose(); queries.dispose();
});

test('the title route draws a title read before at once: Back costs no request', async () => {
  const {createDetailBindings} = await import('../src/screens/detail/bindings.ts');
  let reads = 0;
  const api = {request: async () => {reads++; return new Promise(() => {});}, requestRaw: async () => ({status: 200, headers: {}, body: {}})};
  const queries = testQueries(api, scope);
  queries.client.setQueryData(queryKeys.itemDetail('film', 'none'), filmPage);
  const context = {api, queries, scope, isCurrent: () => true, preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry() {}, openSettings() {}, play: async () => {}, reportError() {}}} as never;
  const binding = createDetailBindings(context);
  await binding.select({libraryId: 'library', itemId: 'film', kind: 'movie'});
  assert.equal(binding.service.getSnapshot().phase, 'ready');
  assert.equal(binding.service.getSnapshot().data, filmPage);
  assert.equal(reads, 0);
  binding.dispose(); queries.dispose();
});

test('another viewer\'s cache never holds this viewer\'s pages', async () => {
  const theirShows = testQueries({request: async () => showDto()}, {...scope, viewerId: 'someone/else'});
  const show = new ShowWorkspaceService({queries: theirShows, scope: {...scope, viewerId: 'someone/else'}, pageSize: 2, seasonPageSize: 2});
  await show.select(route); show.dispose(); theirShows.dispose();
  const mineShow = testQueries({request: async () => new Promise(() => {})}, scope);
  const otherViewer = new ShowWorkspaceService({queries: mineShow, scope, pageSize: 2, seasonPageSize: 2});
  void otherViewer.select(route);
  assert.equal(otherViewer.getSnapshot().phase, 'loading', 'a page belongs to the viewer who read it');
  assert.equal(otherViewer.getSnapshot().data, null);
  otherViewer.dispose(); mineShow.dispose();
  // A title another viewer read is in that viewer's cache only: this viewer's starts empty.
  const theirs = testQueries({request: async () => new Promise(() => {})}, {...scope, viewerId: 'someone/else'});
  theirs.client.setQueryData(queryKeys.itemDetail('film', 'none'), filmPage);
  const mine = testQueries({request: async () => new Promise(() => {})}, scope);
  const mismatch = new DetailService({queries: mine, api: {request: async () => new Promise(() => {})}, scope, requestId: async () => 'request'});
  void mismatch.select({libraryId: 'library', itemId: 'film'}, {related: 'none'});
  assert.equal(mismatch.getSnapshot().phase, 'loading');
  assert.equal(mismatch.getSnapshot().data, null);
  mismatch.dispose(); theirs.dispose(); mine.dispose();
});
