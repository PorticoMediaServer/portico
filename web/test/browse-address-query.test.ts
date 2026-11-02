import test from 'node:test';
import assert from 'node:assert/strict';
import {AddressContinuity, addressedQuery, addressedQueryKey, addressedSearch, libraryAddressKey} from '../src/screens/browse/address-query.ts';
import {parseHomeRowScreenSearch, parseLibraryScreenSearch} from '../src/screens/browse/route-search.ts';

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

test('Sort: Date added is published on the address, and its own replace echo settles without a rebuild', () => {
  const continuity = new AddressContinuity(), opened = key({});
  assert.equal(continuity.reconcile(opened, opened, true), 'agreed');
  const sorted = key({sort: added});
  // The toolbar moved the grid; the address still holds the opening.
  assert.equal(continuity.reconcile(opened, sorted, false), 'publish');
  // The replace lands: both sides agree, nothing is rebuilt.
  assert.equal(continuity.reconcile(sorted, sorted, false), 'agreed');
  // A filter on top of it is published the same way.
  const filtered = key({sort: added, query: drama});
  assert.equal(continuity.reconcile(sorted, filtered, false), 'publish');
  assert.equal(continuity.reconcile(filtered, filtered, false), 'agreed');
});

test('a quick second change publishes again rather than adopting the older echo', () => {
  const continuity = new AddressContinuity(), opened = key({});
  continuity.reconcile(opened, opened, true);
  const first = key({sort: added}), second = key({sort: added, query: drama});
  assert.equal(continuity.reconcile(opened, first, false), 'publish');
  // The second change lands before the first replace's echo.
  assert.equal(continuity.reconcile(opened, second, false), 'publish');
  // The first echo arrives late: it is our own write, so the newer state is republished.
  assert.equal(continuity.reconcile(first, second, false), 'publish');
  assert.equal(continuity.reconcile(second, second, false), 'agreed');
});

test('a genuine navigation to another query is adopted into a rebuilt grid, never overwritten', () => {
  const continuity = new AddressContinuity(), opened = key({sort: added});
  assert.equal(continuity.reconcile(opened, opened, true), 'agreed');
  // A link or Back/Forward moves the address while the grid still holds what was agreed.
  assert.equal(continuity.reconcile(key({query: drama}), opened, true), 'adopt');
  // Even one the page once wrote itself, when a real history action brought it back.
  const own = key({sort: title.map(value => ({...value, direction: 'desc' as const}))});
  const other = new AddressContinuity();
  other.reconcile(opened, opened, true);
  assert.equal(other.reconcile(opened, own, false), 'publish');
  assert.equal(other.reconcile(own, own, false), 'agreed');
  assert.equal(other.reconcile(opened, own, true), 'adopt');
});

test('a grid opened from a reloaded or Back address agrees with it at once (the LB-1 journey)', () => {
  // Movies → Sort: Date added → open a poster → Back: the library route mounts again on the
  // address the toolbar wrote, so the new grid is built from it and nothing is published.
  const address = parseLibraryScreenSearch(JSON.parse(JSON.stringify({view: 'browse', pivot: 'movies', ...addressedSearch({predicates: [drama], sorts: added}, title)})));
  const continuity = new AddressContinuity(), routed = key(address);
  assert.equal(continuity.reconcile(routed, addressedQueryKey({predicates: [drama], sorts: added}), true), 'agreed');
});
