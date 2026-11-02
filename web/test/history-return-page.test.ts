import test from 'node:test';
import assert from 'node:assert/strict';
import {freshnessFor} from '../src/platform/freshness.ts';
import {DetailService, type DetailProjection} from '@core/detail.ts';
import {LibraryContentService, type LibraryContentSnapshot} from '@core/library-content.ts';
import {ShowWorkspaceService, type ShowWorkspaceRoute, type ShowWorkspaceSnapshot} from '@core/show-workspace.ts';
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
const memory = () => freshnessFor({} as never, {serverId: 'server', viewerId: 'hosted/account/profile'}).pages;

test('Back draws the show page the route left, before its fresh read has answered', async () => {
  const pages = memory(), key = 'show-page:server:hosted/account/profile:library:show';
  // The first visit: the route's own service read the page, and the route kept it.
  const first = new ShowWorkspaceService({api: {request: async () => showDto()}, scope, pageSize: 2, seasonPageSize: 2});
  await first.select(route);
  assert.equal(first.getSnapshot().phase, 'ready');
  pages.remember(key, first.getSnapshot());
  first.dispose(); // leaving the route disposes its service
  // Back: history navigation reacquires the route's service from the same page.
  let reads = 0;
  const second = new ShowWorkspaceService({api: {request: async () => {reads++; return new Promise(() => {});}}, scope, pageSize: 2, seasonPageSize: 2, initial: pages.recall<ShowWorkspaceSnapshot>(key)});
  const reacquired = second.getSnapshot();
  assert.equal(reacquired.phase, 'ready', 'the page is already there to draw');
  assert.equal(reacquired.data?.show.id, 'show');
  assert.deepEqual(reacquired.data?.episodes.sections[0]?.entries.map(entry => entry.id), ['ep1', 'ep2']);
  // It reads again in the background, and keeps the page on screen while it does.
  const pending = second.select(route);
  assert.equal(second.getSnapshot().phase, 'loading');
  assert.equal(second.getSnapshot().data?.show.id, 'show', 'a slow read never blanks the page');
  assert.equal(reads, 1);
  await Promise.race([pending, new Promise(resolve => setTimeout(resolve, 30))]);
  second.dispose();
});

test('a re-read that times out keeps the page it showed, so the title never becomes an error page', async () => {
  const pages = memory(), key = 'show-page:server:hosted/account/profile:library:show:slow';
  const first = new ShowWorkspaceService({api: {request: async () => showDto()}, scope, pageSize: 2, seasonPageSize: 2});
  await first.select(route); pages.remember(key, first.getSnapshot()); first.dispose();
  const second = new ShowWorkspaceService({api: {request: async () => new Promise(() => {})}, scope, pageSize: 2, seasonPageSize: 2, timeoutMs: 5, initial: pages.recall<ShowWorkspaceSnapshot>(key)});
  await second.select(route);
  const failed = second.getSnapshot();
  assert.equal(failed.phase, 'error');
  assert.equal(failed.data?.show.id, 'show', 'the page is still what the viewer sees');
  second.dispose();
});

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

test('an album or artist page reacquired from its own memory draws its projection while it reads', async () => {
  const projection = {entity: {id: 'album', kind: 'album', title: 'Album', libraryId: 'library'}, scope: {libraryId: 'library', view: 'album', entityId: 'album'}, revision: {catalog: 1, viewer: 1}} as never;
  const snapshot = {generation: 0, scope, route: {libraryId: 'library', view: 'album', entityId: 'album'}, phase: 'ready', projection, sections: [], appended: [],
    pagination: {cursor: null, canPrevious: false, next: []}, error: null} as unknown as LibraryContentSnapshot;
  const service = new LibraryContentService({api: {request: async () => new Promise(() => {})}, scope, initial: snapshot});
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.equal(service.getSnapshot().projection, projection);
  const pending = service.select({libraryId: 'library', view: 'album', entityId: 'album'});
  assert.equal(service.getSnapshot().phase, 'loading');
  assert.equal(service.getSnapshot().projection, projection);
  await Promise.race([pending, new Promise(resolve => setTimeout(resolve, 30))]);
  service.dispose();
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

test('another viewer’s or another route’s page is never adopted', async () => {
  const show = new ShowWorkspaceService({api: {request: async () => showDto()}, scope, pageSize: 2, seasonPageSize: 2});
  await show.select(route);
  const page = show.getSnapshot(); show.dispose();
  const otherViewer = new ShowWorkspaceService({api: {request: async () => new Promise(() => {})}, scope: {...scope, viewerId: 'someone/else'}, initial: page});
  assert.equal(otherViewer.getSnapshot().phase, 'idle', 'a page belongs to the viewer who read it');
  otherViewer.dispose();
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
