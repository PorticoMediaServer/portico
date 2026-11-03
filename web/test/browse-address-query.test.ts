import test from 'node:test';
import assert from 'node:assert/strict';
import {addressCacheKey, addressedQuery, addressedQueryKey, addressedSearch, libraryAddressKey} from '../src/screens/browse/address-query.ts';
import {parseHomeRowScreenSearch, parseLibraryScreenSearch} from '../src/screens/browse/route-search.ts';
import {BrowseController, type BrowseQuery} from '../src/screens/browse/controller.ts';
import {browseGridQuery} from '../src/screens/browse/browse-cache.ts';
import {queryKeys} from '../../packages/client-core/src/query/keys.ts';
import {createViewerQueries} from '../../packages/client-core/src/query/viewer-queries.ts';
import {FreshnessHub} from '../../packages/client-core/src/freshness-hub.ts';

const title = [{field: 'title', direction: 'asc' as const}];
const added = [{field: 'added', direction: 'desc' as const}];
const drama = {variant: 'predicate' as const, field: 'genre', operator: 'contains-any', value: ['Drama']};
const nineties = {variant: 'predicate' as const, field: 'decade', operator: 'in', value: [1990]};
const key = (search: Parameters<typeof addressedQuery>[0]) => addressedQueryKey(addressedQuery(search, title));

test('a toolbar query round-trips through the library and See all addresses, and the default order stays out of them', () => {
  const grid = {predicates: [drama, nineties], sorts: added};
  const search = addressedSearch(grid, title);
  assert.deepEqual(search.sort, added);
  // Two filters are one conjunction the route parser accepts (a bare `{all}` was refused on reload).
  for (const parse of [parseLibraryScreenSearch, parseHomeRowScreenSearch]) {
    const back = parse(JSON.parse(JSON.stringify({view: 'browse', pivot: 'movies', ...search})));
    assert.equal(key(back), addressedQueryKey(grid));
  }
  assert.deepEqual(addressedSearch({predicates: [], sorts: title}, title), {});
  assert.deepEqual(addressedSearch({predicates: [drama], sorts: title}, title), {query: drama});
  // A loose node from a link and the controller's tagged node are the same query.
  assert.equal(key({query: {field: 'genre', operator: 'contains-any', value: ['Drama']} as never}), key({query: drama}));
});

test('the library address key ignores only the toolbar state', () => {
  const at = (search: Record<string, unknown>) => libraryAddressKey({pathname: '/library/films', search});
  assert.equal(at({view: 'browse', pivot: 'movies'}), at({pivot: 'movies', view: 'browse', sort: added, query: drama}));
  assert.notEqual(at({view: 'browse', pivot: 'movies'}), at({view: 'browse', pivot: 'collections'}));
  assert.notEqual(at({view: 'browse', pivot: 'shows'}), at({view: 'browse', pivot: 'shows', tab: 'aired'}));
  assert.notEqual(at({view: 'browse'}), libraryAddressKey({pathname: '/library/shows', search: {view: 'browse'}}));
});

const pivot = {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: title, supportedViews: ['grid', 'list'], browsable: true, paging: 'offset'};
const field = (id: string, type: string) => ({id, labelKey: id, type, operators: ['contains-any', 'in'], controlHint: 'facet-multi-select', applicableKinds: ['movie']});
const caps = {library: {id: 'lib', name: 'Movies', kind: 'movie', defaultView: 'discover'}, pivots: [pivot], resolvedPivot: pivot, fields: [field('genre', 'string'), field('decade', 'number')],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']},
    {id: 'added', labelKey: 'sort.added', directions: ['asc', 'desc'], defaultDirection: 'desc', applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}};
async function settle() {for (let n = 0; n < 8; n++) await new Promise<void>(resolve => setTimeout(resolve, 0));}
/** A grid over a fake server that records each page read's query. */
function grid(opening: Partial<BrowseQuery> = {}) {
  const reads: unknown[] = [];
  const controller = new BrowseController({async request<T>(path: string, _method?: string, body?: unknown) {
    if (path.includes('capabilities')) return caps as T;
    reads.push((body as {query?: unknown; sort?: unknown}));
    return {pivot: 'movies', applied: {query: null, sort: title, seek: null}, entries: [{id: 'm1', kind: 'movie', title: 'One', libraryId: 'lib', navigation: {view: 'item'}}],
      pageInfo: {start: 0, total: 1, totalIsLowerBound: false, revision: 'r1', hasMore: false, nextCursor: ''}, positionIndex: []} as T;
  }}, {libraryId: 'lib', pivot: 'movies', ...(opening.predicates ? {predicates: opening.predicates} : {}), ...(opening.sorts ? {sorts: opening.sorts} : {})}, () => true);
  const shown = () => addressedQueryKey({predicates: controller.getSnapshot().predicates, sorts: controller.query().sort ?? []});
  return {controller, reads, shown};
}

test('Sort: Date added and a filter go to the address, and the grid shown is left as it is', async () => {
  const {controller, reads, shown} = grid();
  try {
    await controller.load(); await settle();
    const asked: BrowseQuery[] = [];
    const release = controller.routeQueries(query => asked.push(query));
    const opened = shown(), before = reads.length, revision = controller.getSnapshot().queryRevision;
    assert.equal(controller.setQuery([], added[0]), true);
    // The toolbar's change is a request to the address; nothing is read or shown yet.
    assert.equal(asked.length, 1);
    assert.equal(addressedQueryKey(asked[0]!), key({sort: added}));
    assert.equal(shown(), opened);
    assert.equal(controller.getSnapshot().queryRevision, revision);
    await settle();
    assert.equal(reads.length, before, 'the address picks the grid for the new order; this one is left as it is');
    // A filter is asked for the same way, from the query this grid shows.
    assert.equal(controller.setQuery([drama]), true);
    assert.equal(addressedQueryKey(asked[1]!), key({query: drama}));
    release();
    // Without a page owning its address (a test, a host with no route), the grid applies it itself.
    assert.equal(controller.setQuery([drama]), true);
    assert.equal(asked.length, 2);
    assert.equal(shown(), key({query: drama}));
  } finally {controller.dispose();}
});

test('the same address is one grid, another order or another tab on the pivot is another', () => {
  const at = (search: Parameters<typeof addressCacheKey>[0], opens?: typeof title) => addressCacheKey(search, opens);
  assert.equal(at({query: {field: 'genre', operator: 'contains-any', value: ['Drama']} as never}), at({query: drama}));
  assert.equal(at({query: {variant: 'group', all: [drama, nineties]} as never}), at({query: {variant: 'group', all: [drama, nineties]} as never}));
  assert.notEqual(at({sort: added}), at({}));
  assert.notEqual(at({}, added), at({}), 'Recently aired and Shows read one pivot in two orders');
  // LB-1: Movies → Sort: Date added and a filter → a title → Back (or a reload): the address the
  // toolbar wrote names the very grid the toolbar's change made.
  const written = addressedSearch({predicates: [drama], sorts: added}, title);
  const back = parseLibraryScreenSearch(JSON.parse(JSON.stringify({view: 'browse', pivot: 'movies', ...written})));
  assert.equal(at(back), at(written));
});

/** A viewer cache over a fake server; each grid read is recorded with the query it asked for. */
function cache() {
  const reads: string[] = [];
  let revision = 'r1', title = 'One';
  const api = {async request<T>(path: string, _method?: string, body?: unknown) {
    if (path.includes('capabilities')) {reads.push('capabilities'); return caps as T;}
    reads.push(JSON.stringify((body as {sort?: unknown}).sort ?? null));
    return {pivot: 'movies', applied: {query: null, sort: [{field: 'title', direction: 'asc'}], seek: null}, entries: [{id: 'm1', kind: 'movie', title, libraryId: 'lib', navigation: {view: 'item'}}],
      pageInfo: {start: 0, total: 1, totalIsLowerBound: false, revision, hasMore: false, nextCursor: ''}, positionIndex: []} as T;
  }};
  const hub = new FreshnessHub({gatherMs: 0, settleMs: 0, settleLimitMs: 0, contentEveryMs: 0});
  const queries = createViewerQueries({scope: {serverId: 'srv', viewerId: 'viewer'}, hub, api, gcTimeMs: Infinity});
  const grid = async (sort?: typeof added) => {
    const value = await queries.client.fetchQuery(browseGridQuery(queries, queryKeys.libraryBrowse('lib', 'movies', addressCacheKey(sort ? {sort} : {})),
      {libraryId: 'lib', pivot: 'movies', ...(sort ? {sorts: sort} : {})}));
    await settle();
    return value;
  };
  return {queries, hub, reads, grid, change(next: string) {revision = next; title = 'Renamed';}};
}

test('a grid comes from the viewer cache: Back to it reads nothing, another order is its own grid, and both end with the viewer', async () => {
  const {queries, reads, grid} = cache();
  try {
    const opened = await grid();
    assert.deepEqual(reads, ['capabilities', JSON.stringify(title)]);
    assert.equal(opened.collection.residentItems().length, 1);
    // Back to the same address: the same live grid, its page already held, no request.
    assert.equal(await grid(), opened);
    assert.equal(reads.length, 2);
    // Sort: Date added is another grid; the first stays cached for Back.
    const sorted = await grid(added);
    assert.notEqual(sorted, opened);
    assert.equal(reads.at(-1), JSON.stringify(added));
    assert.equal(await grid(), opened);
    assert.equal(reads.length, 4);
    queries.dispose();
    assert.equal(opened.isDisposed(), true);
    assert.equal(sorted.isDisposed(), true);
  } finally {queries.dispose();}
});

test('a cached grid is kept current: shown, a change to a title it holds is read in place; hidden, it waits until it is shown again', async () => {
  const {queries, hub, reads, grid, change} = cache();
  try {
    const opened = await grid();
    const hide = opened.show();
    change('r2');
    hub.note('personal:title:m1');
    await settle(); await settle();
    assert.equal(reads.length, 3, 'one page read again, no capabilities');
    assert.equal(opened.collection.residentItems()[0]!.entry.title, 'Renamed');
    hide();
    hub.note('personal:title:m1');
    await settle();
    assert.equal(reads.length, 3, 'nothing is read for a grid nobody sees');
    const again = opened.show();
    await settle(); await settle();
    assert.equal(reads.length, 4, 'shown again, the change it missed is read');
    again();
  } finally {queries.dispose();}
});

test('while a selection is open on a cached grid, a change is held and read once the selection ends', async () => {
  const {queries, hub, reads, grid, change} = cache();
  try {
    const opened = await grid();
    const hide = opened.show(), release = opened.hold();
    change('r2');
    hub.note('personal:title:m1');
    await settle(); await settle();
    assert.equal(reads.length, 2, 'the open selection keeps its page revision');
    release();
    await settle(); await settle();
    assert.equal(reads.length, 3);
    assert.equal(opened.collection.residentItems()[0]!.entry.title, 'Renamed');
    hide();
  } finally {queries.dispose();}
});
