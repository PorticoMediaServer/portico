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

// ── The siblings: the browse grid and the search results (J-15) ──────────────

const browsePivot = {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true, paging: 'offset'};
const browseCaps = {library: {id: 'lib', name: 'Movies', kind: 'movie', defaultView: 'discover'}, pivots: [browsePivot], resolvedPivot: browsePivot,
  fields: [], sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}};
const browsePage = (start: number, total = 50_000, revision = 'revision-one') => ({pivot: 'movies', applied: {query: null, sort: [{field: 'title', direction: 'asc'}], seek: null},
  entries: Array.from({length: Math.min(60, total - start)}, (_, n) => ({id: `movie-${start + n}`, kind: 'movie', title: `Movie ${start + n}`, libraryId: 'lib', navigation: {view: 'item'}})),
  pageInfo: {start, total, totalIsLowerBound: false, revision, hasMore: start + 60 < total, nextCursor: ''}, positionIndex: [{key: 'Z', index: 49_980}]});
const browseApi = (reply: (path: string, body?: unknown) => unknown) => ({request: async <T,>(path: string, _method?: string, body?: unknown) => (path.includes('capabilities') ? browseCaps : reply(path, body)) as T});
const settle = async () => {for (let n = 0; n < 10; n++) await new Promise(resolve => setTimeout(resolve, 0));};
const browseSource = {libraryId: 'lib', pivot: 'movies'};

test('Back draws the browse grid the opening showed, at its positions and total, before its fresh read answers', async () => {
  const {BrowseController} = await import('../src/screens/browse/controller.ts');
  const first = new BrowseController(browseApi((_path, body) => browsePage((body as {range: {start: number}}).range.start)) as never, browseSource, () => true);
  await first.load(); await settle();
  assert.equal(first.collection.itemAt(0)?.entry.id, 'movie-0');
  const seed = first.pageSeed()!;
  assert.ok(seed, 'a shown list is kept as a page');
  first.dispose();
  // Back: a brand-new controller restored from the same page, whose server now never answers.
  const second = new BrowseController(browseApi(() => new Promise(() => {})) as never, browseSource, () => true, seed);
  assert.equal(second.getSnapshot().phase, 'ready', 'the grid is already there to draw');
  assert.equal(second.getSnapshot().revision, 'revision-one');
  assert.equal(second.collection.getSnapshot().total, 50_000);
  assert.equal(second.collection.itemAt(0)?.entry.id, 'movie-0');
  assert.equal(second.collection.itemAt(59)?.entry.id, 'movie-59');
  assert.equal(second.contains(seed.entries[0]!.item.entry), true, 'the restored cards still act on their own list');
  // The screen's mount read runs in place; nothing the person sees is cleared.
  void second.load();
  await settle();
  assert.equal(second.collection.itemAt(0)?.entry.id, 'movie-0');
  assert.equal(second.collection.getSnapshot().status, 'ready');
  assert.equal(second.getSnapshot().phase, 'ready');
  second.dispose();
});

test('a browse re-read that fails or times out keeps the grid it showed', async () => {
  const {BrowseController} = await import('../src/screens/browse/controller.ts');
  const first = new BrowseController(browseApi((_path, body) => browsePage((body as {range: {start: number}}).range.start)) as never, browseSource, () => true);
  await first.load(); await settle();
  const seed = first.pageSeed()!; first.dispose();
  const failing: readonly (() => Promise<unknown>)[] = [async () => {throw new Error('offline');}, async () => new Promise(() => {})];
  for (const fail of failing) {
    const held = new BrowseController({request: async <T,>(path: string) => path.includes('capabilities') ? (await fail()) as T : browsePage(0) as T} as never, browseSource, () => true, seed);
    void held.load(); await settle();
    assert.equal(held.collection.itemAt(0)?.entry.id, 'movie-0', 'the list on screen is never blanked by a failed read');
    assert.equal(held.collection.getSnapshot().total, 50_000);
    held.dispose();
  }
});

test('another query’s browse page is never adopted', async () => {
  const {BrowseController} = await import('../src/screens/browse/controller.ts');
  const first = new BrowseController(browseApi((_path, body) => browsePage((body as {range: {start: number}}).range.start)) as never, browseSource, () => true);
  await first.load(); await settle();
  const seed = first.pageSeed()!; first.dispose();
  const predicate = {variant: 'predicate' as const, field: 'watched', operator: 'equals' as const, value: false};
  const other = new BrowseController(browseApi(() => new Promise(() => {})) as never, {libraryId: 'lib', pivot: 'movies', predicates: [predicate]}, () => true, seed);
  assert.equal(other.getSnapshot().phase, 'loading', 'a page for one query is not another query’s first paint');
  assert.equal(other.collection.itemAt(0), undefined);
  other.dispose();
});

test('Back draws the search results the entry last showed, then revalidates them in place', async () => {
  const {SearchService} = await import('@core/search.ts');
  const page = {q: 'harbor', capabilities: {groups: []}};
  const snapshot = {generation: 0, scope: {serverId: 'server', viewerId: 'hosted/account/profile'}, query: {q: 'harbor'}, phase: 'ready', data: page,
    groups: [], pagination: {cursor: null, canPrevious: false}, error: null} as never;
  const service = new SearchService({api: {request: async () => new Promise(() => {})} as never, scope: {serverId: 'server', viewerId: 'hosted/account/profile'}, timeoutMs: 5, initial: snapshot});
  assert.equal(service.getSnapshot().phase, 'ready', 'the results are already there to draw');
  assert.equal(service.getSnapshot().data, page);
  await service.revalidate();
  assert.equal(service.getSnapshot().data, page, 'a re-read that never answers keeps the shown results');
  service.dispose();
  const other = new SearchService({api: {request: async () => new Promise(() => {})} as never, scope: {serverId: 'server', viewerId: 'someone/else'}, initial: snapshot});
  assert.equal(other.getSnapshot().phase, 'idle', 'another viewer’s results are never adopted');
  other.dispose();
});

test('the browse and Discover pages are keyed by server, viewer and the exact route query', async () => {
  const {browsePageKey, browseDiscoverKey} = await import('../src/screens/browse/useBrowseLease.ts');
  const scope = {serverId: 'server', viewerId: 'hosted/account/profile'};
  const key = browsePageKey({scope}, '{"libraryId":"lib","pivot":"movies"}');
  assert.notEqual(key, browsePageKey({scope: {...scope, viewerId: 'someone/else'}}, '{"libraryId":"lib","pivot":"movies"}'), 'another viewer keeps its own page');
  assert.notEqual(key, browsePageKey({scope: {...scope, serverId: 'other'}}, '{"libraryId":"lib","pivot":"movies"}'), 'another server keeps its own page');
  assert.notEqual(key, browsePageKey({scope}, '{"libraryId":"lib","pivot":"shows"}'), 'another query keeps its own page');
  assert.notEqual(browseDiscoverKey({scope}, '{"libraryId":"lib","pivot":"movies"}'), key);
});

test('a kept page that fails to re-read keeps its entries and carries the failure in the state', async () => {
  const {BrowseController} = await import('../src/screens/browse/controller.ts');
  const first = new BrowseController(browseApi((_path, body) => browsePage((body as {range: {start: number}}).range.start)) as never, browseSource, () => true);
  await first.load(); await settle();
  const seed = first.pageSeed()!; first.dispose();
  const failure = new Error('offline');
  const held = new BrowseController({request: async <T,>(path: string) => path.includes('capabilities')
    ? (() => {throw failure;})() as T : browsePage(0) as T} as never, browseSource, () => true, seed);
  held.collection.ensureRange(0, 2);   // the grid is laid out before the screen's mount read
  void held.load(); await settle();
  assert.equal(held.collection.itemAt(0)?.entry.id, 'movie-0', 'the list stays exactly as it was');
  assert.equal(held.getSnapshot().error, failure, 'the failure is in the state beside the page, not hidden');
  held.dispose();
});

test('a kept page whose view moved on opens again from its first page instead of paging into nothing', async () => {
  const {BrowseController} = await import('../src/screens/browse/controller.ts');
  const first = new BrowseController(browseApi((_path, body) => browsePage((body as {range: {start: number}}).range.start)) as never, browseSource, () => true);
  await first.load(); await settle();
  const seed = first.pageSeed()!; first.dispose();
  const reads: string[] = [];
  const moved = new BrowseController({request: async <T,>(path: string, _method?: string, body?: unknown) => {
    if (path.includes('capabilities')) return browseCaps as T;
    const range = (body as {range: {start: number; revision?: string}}).range;
    reads.push(`start ${range.start}${range.revision ? ` @${range.revision}` : ' (first page)'}`);
    // The first page arrives under a newer revision; the next visible page still names the old one.
    if (range.revision) throw Object.assign(new Error('changed'), {code: 'stale_continuation'});
    return browsePage(range.start, 50_000, 'new') as T;
  }} as never, browseSource, () => true, seed);
  moved.collection.ensureRange(0, 119);   // the grid is laid out before the screen's mount read
  void moved.load(); await settle();
  assert.ok(moved.getSnapshot().queryRevision > seed.state.queryRevision, 'the view opened again from its first page');
  assert.ok(reads.filter(read => read.includes('first page')).length >= 2, 'a fresh first page was requested after the view moved');
  moved.dispose();
});

test('a seeded window only marks whole pages ready; a page with a gap loads normally', async () => {
  const {createWindowedCollection} = await import('@core/collections/windowed.ts');
  const items = ['a', 'b', 'c'];
  const collection = createWindowedCollection<string>({pageSize: 4, maxResidentPages: 4, keyOf: value => value,
    fetchPage: async (start, count) => ({items: items.slice(start, start + count), total: items.length})});
  // Index 1 is missing from the same page, so that page must not be marked ready.
  collection.seed([{index: 0, item: 'a'}, {index: 2, item: 'c'}], {total: 3});
  assert.equal(collection.itemAt(0), undefined, 'a page with a hole is not a ready page');
  assert.equal(collection.getSnapshot().total, 3);
  collection.ensureRange(0, 2);
  await settle();
  assert.equal(collection.itemAt(0), 'a');
  assert.equal(collection.itemAt(2), 'c');
  collection.dispose();
  const whole = createWindowedCollection<string>({pageSize: 2, maxResidentPages: 4, keyOf: value => value, fetchPage: async () => ({items: [], total: 0})});
  whole.seed([{index: 0, item: 'a'}, {index: 1, item: 'b'}], {total: 3});
  assert.deepEqual([whole.itemAt(0), whole.itemAt(1)], ['a', 'b'], 'a whole page is ready at once');
  whole.dispose();
});
