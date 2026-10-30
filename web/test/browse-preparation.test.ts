import test from 'node:test';
import assert from 'node:assert/strict';
import {routePaths} from '../../packages/contracts/src/route-paths.ts';
import {BrowseController} from '../src/screens/browse/controller.ts';
import type {LibraryContentApi} from '../../packages/client-core/src/library-content.ts';
import type {PreparationResponse} from '../../packages/client-core/src/read-preparation.ts';
test('web consumes the official generated selection preparation routes', () => {
  assert.equal(routePaths.get_library_browse_selection_query, '/v1/libraries/{id}/browse-selection-queries/{queryId}');
  assert.equal(routePaths.delete_library_browse_selection_query, routePaths.get_library_browse_selection_query);
});
const tick = async () => {for (let n = 0; n < 40; n++) await Promise.resolve();};
const pivot = {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid'], browsable: true, paging: 'offset'};
const caps = {library: {id: 'lib', name: 'Movies', kind: 'movie', defaultView: 'discover'}, pivots: [pivot], resolvedPivot: pivot, fields: [],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}};
const pending = {kind: 'browse-selection-preparing', state: 'preparing', queryId: 'selection', retryAfterMs: 0};
const capabilityCounts = {play: 1, queue: 1, playlist: 1, myList: 1, watched: 1, collection: 1, favorite: 1, download: 0, edit: 1, refresh: 1, delete: 1};
const whole = {selector: {query: {libraryId: 'lib', pivot: 'movies', filter: null, sort: pivot.defaultSort, selectionBasis: 'a'.repeat(64)}}, catalogRevision: 'r1', total: 1,
  types: [{kind: 'movie', count: 1, capabilityCounts, libraries: [{libraryId: 'lib', count: 1}]}]};
const ready = (body: unknown): PreparationResponse => ({status: 200, headers: {}, body});
const page = (body: any, selection = true) => ({pivot: 'movies', applied: {query: body.query ?? null, sort: body.sort, seek: null},
  entries: [{id: 'movie', kind: 'movie', title: 'Movie', libraryId: 'lib', navigation: {view: 'item'}}],
  pageInfo: {start: body.range.start, total: 1, revision: 'r1', hasMore: false, nextCursor: ''}, ...(selection ? {wholeSelectionPreparing: pending} : {})});

test('web Browse publishes its page immediately and notifies again when whole-selection becomes ready', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']}); let notifications = 0;
  const api: LibraryContentApi = {request: async <T>() => caps as T, requestRaw: async (_path, method, options) => method === 'POST' ? ready(page(options?.body)) : ready(whole)};
  const controller = new BrowseController(api, {libraryId: 'lib', pivot: 'movies'}, () => true); controller.subscribe(() => {notifications++;});
  try {
    await controller.load(); await tick(); assert.equal(controller.collection.itemAt(0)?.entry.id, 'movie'); assert.equal(controller.wholeSelection(), undefined);
    const before = notifications; t.mock.timers.tick(400); await tick();
    assert.equal(controller.wholeSelection()?.total, 1); assert.ok(notifications > before, 'late onWhole triggers a controller publication');
    assert.equal(controller.collection.itemAt(0)?.entry.id, 'movie');
  } finally {controller.dispose();}
});

test('web query switch cancels the captured aggregate and refuses a late response', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']}); let finish!: (value: PreparationResponse) => void, posts = 0;
  const calls: string[] = [];
  const api: LibraryContentApi = {request: async <T>() => caps as T, requestRaw: async (path, method, options) => {
    calls.push(`${method} ${path}`); if (method === 'POST') return ready(page(options?.body, posts++ === 0));
    if (method === 'DELETE') return ready(undefined); return new Promise(resolve => {finish = resolve;});
  }};
  const controller = new BrowseController(api, {libraryId: 'lib', pivot: 'movies'}, () => true);
  try {
    await controller.load(); await tick(); t.mock.timers.tick(400); await tick();
    assert.equal(controller.setQuery([], {field: 'title', direction: 'desc'}), true); await tick(); finish(ready(whole)); await tick();
    assert.equal(controller.wholeSelection(), undefined); assert.equal(controller.getSnapshot().sort?.direction, 'desc');
    assert.equal(calls.filter(call => call === 'DELETE /v1/libraries/lib/browse-selection-queries/selection').length, 1);
  } finally {controller.dispose();}
});
