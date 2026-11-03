import test from 'node:test';
import assert from 'node:assert/strict';
import {addressedQuery, addressedQueryKey, addressedSearch, libraryAddressKey} from '../src/screens/browse/address-query.ts';
import {parseHomeRowScreenSearch, parseLibraryScreenSearch} from '../src/screens/browse/route-search.ts';
import {BrowseController, type BrowseQuery} from '../src/screens/browse/controller.ts';

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

test('Sort: Date added goes to the address, and the grid moves only when the address does', async () => {
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
    // The replace lands: the grid follows it once, and the same address again is nothing.
    assert.equal(controller.follow(addressedQuery({sort: added}, title)), true);
    await settle();
    assert.equal(shown(), key({sort: added}));
    assert.equal(reads.length, before + 1);
    assert.equal(controller.follow(addressedQuery({sort: added}, title)), false);
    await settle();
    assert.equal(reads.length, before + 1);
    // A filter on top of it is asked for the same way, with the order kept.
    assert.equal(controller.setQuery([drama]), true);
    assert.equal(addressedQueryKey(asked[1]!), key({sort: added, query: drama}));
    release();
    // Without a page owning its address (a test, a host with no route), the grid applies it itself.
    assert.equal(controller.setQuery([drama]), true);
    assert.equal(asked.length, 2);
    assert.equal(shown(), key({sort: added, query: drama}));
  } finally {controller.dispose();}
});

test('Back or a link to another query moves the grid to it; a query this pivot cannot run leaves it as it was', async () => {
  const {controller, shown} = grid({sorts: added});
  try {
    await controller.load(); await settle();
    controller.routeQueries(() => assert.fail('a navigation is not the toolbar'));
    assert.equal(shown(), key({sort: added}));
    assert.equal(controller.follow(addressedQuery({query: drama}, title)), true);
    assert.equal(shown(), key({query: drama}));
    assert.throws(() => controller.follow(addressedQuery({sort: [{field: 'nope', direction: 'asc'}]}, title)));
    assert.equal(shown(), key({query: drama}));
  } finally {controller.dispose();}
});

test('a grid opened from a reloaded or Back address shows that query at once (the LB-1 journey)', async () => {
  // Movies → Sort: Date added → open a poster → Back: the library route mounts again on the
  // address the toolbar wrote, and the new grid opens on it, with nothing to follow.
  const address = parseLibraryScreenSearch(JSON.parse(JSON.stringify({view: 'browse', pivot: 'movies', ...addressedSearch({predicates: [drama], sorts: added}, title)})));
  const opening = addressedQuery(address, title);
  const {controller, reads, shown} = grid(opening);
  try {
    await controller.load(); await settle();
    assert.equal(shown(), key(address));
    assert.equal(controller.follow(opening), false);
    assert.equal(reads.length, 1);
  } finally {controller.dispose();}
});
