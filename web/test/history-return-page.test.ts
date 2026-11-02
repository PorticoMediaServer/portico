import test from 'node:test';
import assert from 'node:assert/strict';
import {freshnessFor} from '../src/platform/freshness.ts';
import {DetailService, type DetailProjection, type DetailSnapshot} from '@core/detail.ts';
import {LibraryContentService, type LibraryContentSnapshot} from '@core/library-content.ts';
import {ShowWorkspaceService, type ShowWorkspaceRoute, type ShowWorkspaceSnapshot} from '@core/show-workspace.ts';

// J-15: a route left and opened again (browser Back and Forward, a cover, a card tapped
// twice) must draw the page it had at once and read it again in place. The viewer's page
// memory is the route's own last page; the service adopts it as its state, so every
// command still runs against what is on screen.
const scope = {serverId: 'server', viewerId: 'hosted/account/profile'};
const route: ShowWorkspaceRoute = {libraryId: 'library', showId: 'show'};

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

test('a title page reacquired from its own memory keeps its detail, and a same-title read keeps it too', async () => {
  const data = {item: {id: 'film', kind: 'movie', title: 'Film'}, scope: {serverId: 'server', libraryId: 'library', itemId: 'film', viewerFence: 'fence'},
    revision: {catalog: 1, viewer: 1}, personal: {watchlisted: false, favorite: false, watched: false, rating: null, revision: 1, progressSeconds: 0, lastPlayedAt: '', status: 'current', conflicts: []}, actions: []} as unknown as DetailProjection;
  const snapshot = {generation: 0, scope, target: {libraryId: 'library', itemId: 'film'}, phase: 'ready', data, error: null, pending: [], mutationError: null} as DetailSnapshot;
  const service = new DetailService({api: {request: async () => new Promise(() => {})}, scope, requestId: async () => 'request', initial: snapshot});
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.equal(service.getSnapshot().data, data, 'the page is already there to draw');
  const pending = service.select({libraryId: 'library', itemId: 'film'});
  assert.equal(service.getSnapshot().phase, 'loading');
  assert.equal(service.getSnapshot().data, data, 'the same title keeps the page it shows while it reads');
  await Promise.race([pending, new Promise(resolve => setTimeout(resolve, 30))]);
  service.dispose();
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

test('the title route keeps the page it reacquired while its own read runs', async () => {
  const {createDetailBindings} = await import('../src/screens/detail/bindings.ts');
  const data = {item: {id: 'film', kind: 'movie', title: 'Film'}, scope: {serverId: 'server', libraryId: 'library', itemId: 'film', viewerFence: 'fence'},
    revision: {catalog: 1, viewer: 1}, personal: {watchlisted: false, favorite: false, watched: false, rating: null, revision: 1, progressSeconds: 0, lastPlayedAt: '', status: 'current', conflicts: []}, actions: []} as unknown as DetailProjection;
  const snapshot = {generation: 0, scope, target: {libraryId: 'library', itemId: 'film'}, phase: 'ready', data, error: null, pending: [], mutationError: null} as DetailSnapshot;
  let reads = 0;
  const context = {api: {request: async () => {reads++; return new Promise(() => {});}, requestRaw: async () => ({status: 200, headers: {}, body: {}})},
    scope, isCurrent: () => true, preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry() {}, openSettings() {}, play: async () => {}, reportError() {}}} as never;
  const binding = createDetailBindings(context, {initial: snapshot});
  const pending = binding.select({libraryId: 'library', itemId: 'film'});
  assert.equal(binding.service.getSnapshot().phase, 'loading');
  assert.equal(binding.service.getSnapshot().data, data, 'the route does not blank the page it just reacquired');
  assert.equal(reads, 1);
  binding.dispose();
  await Promise.race([pending, new Promise(resolve => setTimeout(resolve, 30))]);
});

test('another viewer’s or another route’s page is never adopted', async () => {
  const show = new ShowWorkspaceService({api: {request: async () => showDto()}, scope, pageSize: 2, seasonPageSize: 2});
  await show.select(route);
  const page = show.getSnapshot(); show.dispose();
  const otherViewer = new ShowWorkspaceService({api: {request: async () => new Promise(() => {})}, scope: {...scope, viewerId: 'someone/else'}, initial: page});
  assert.equal(otherViewer.getSnapshot().phase, 'idle', 'a page belongs to the viewer who read it');
  otherViewer.dispose();
  const detail = {generation: 0, scope, target: {libraryId: 'library', itemId: 'film'}, phase: 'ready',
    data: {item: {id: 'film'}, scope: {libraryId: 'library'}} as never, error: null, pending: [], mutationError: null} as DetailSnapshot;
  const mismatch = new DetailService({api: {request: async () => new Promise(() => {})}, scope: {...scope, viewerId: 'someone/else'}, requestId: async () => 'request', initial: detail});
  assert.equal(mismatch.getSnapshot().phase, 'idle');
  mismatch.dispose();
  const wrongTitle = new DetailService({api: {request: async () => new Promise(() => {})}, scope, requestId: async () => 'request', initial: {...detail, target: {libraryId: 'library', itemId: 'other'}}});
  assert.equal(wrongTitle.getSnapshot().phase, 'idle', 'a page for one title is not another title’s first paint');
  wrongTitle.dispose();
});
