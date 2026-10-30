import test from 'node:test';
import assert from 'node:assert/strict';
import {categoryDestination, libraryTab, libraryTabForPivot, libraryTabSort, libraryTabs, readingTabModel} from '../src/presentation/library-tabs.ts';

test('Recently aired opens latest episode first and leaves the order to the viewer', () => {
  const aired = libraryTab('tv', 'aired');
  assert.deepEqual(aired.defaultSort, {field: 'latestAired', direction: 'desc'});
  // The server offers the sort: the tab opens in its own order.
  assert.deepEqual(libraryTabSort(aired, [{id: 'title'}, {id: 'latestAired'}], {field: 'title', direction: 'asc'}), {field: 'latestAired', direction: 'desc'});
  // A server that does not offer it: the pivot's default, never a sort the server would refuse.
  assert.deepEqual(libraryTabSort(aired, [{id: 'title'}], {field: 'title', direction: 'asc'}), {field: 'title', direction: 'asc'});
  // Before the vocabulary has arrived the tab's own order is assumed.
  assert.deepEqual(libraryTabSort(aired, undefined, undefined), {field: 'latestAired', direction: 'desc'});
});

test('a tab without its own order opens in the pivot default, then title', () => {
  const shows = libraryTab('tv', 'browse');
  assert.deepEqual(libraryTabSort(shows, [{id: 'title'}, {id: 'added'}], {field: 'added', direction: 'desc'}), {field: 'added', direction: 'desc'});
  assert.deepEqual(libraryTabSort(shows, [], undefined), {field: 'title', direction: 'asc'});
});

test('no tab hides the sort: none declares an order the viewer cannot change', () => {
  for (const kind of ['movie', 'tv', 'anime', 'music', 'audiobook']) {
    for (const tab of libraryTabs(kind, {owner: true, unmatched: true})) assert.equal('fixedSort' in tab, false, `${kind}/${tab.id}`);
  }
});

test('a row that names a pivot opens the plain tab for it, not Recently aired', () => {
  assert.equal(libraryTabForPivot('tv', 'shows')?.id, 'browse');
  assert.equal(libraryTabForPivot('music', 'albums')?.id, 'releases');
});

test('a category link is the tab that lists its titles with one ordinary filter', () => {
  assert.deepEqual(categoryDestination('movie', 'genre:Drama'), {tab: 'browse', predicate: {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Drama']}});
  assert.deepEqual(categoryDestination('tv', 'decade:1990'), {tab: 'browse', predicate: {variant: 'predicate', field: 'decade', operator: 'in', value: [1990]}});
  assert.deepEqual(categoryDestination('movie', 'studio:A24: Films'), {tab: 'browse', predicate: {variant: 'predicate', field: 'studio', operator: 'in', value: ['A24: Films']}});
  // Music genres open albums, as the Genres tab does; an audiobook series opens books.
  assert.deepEqual(categoryDestination('music', 'genre:Jazz'), {tab: 'releases', predicate: {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Jazz']}});
  assert.deepEqual(categoryDestination('audiobook', 'series:Dune'), {tab: 'browse', predicate: {variant: 'predicate', field: 'series', operator: 'contains-any', value: ['Dune']}});
});

test('a category this app does not know is left to the server', () => {
  assert.equal(categoryDestination('movie', 'mood:Cosy'), undefined);
  assert.equal(categoryDestination('movie', 'decade:199x'), undefined);
  assert.equal(categoryDestination('movie', 'genre:'), undefined);
  assert.equal(categoryDestination('movie', 'nothing'), undefined);
});

test('a book library offers Discover and the bounded Books list, a comic library Volumes and issues', () => {
  assert.deepEqual(libraryTabs('book').map(tab => tab.id), ['discover', 'browse']);
  assert.equal(libraryTab('book', 'browse').label, 'library.tab.books');
  assert.deepEqual(libraryTabs('comic').map(tab => tab.id), ['discover', 'browse']);
  assert.equal(libraryTab('comic', 'browse').label, 'library.tab.volumesAndIssues');
  for (const kind of ['book', 'comic']) {
    // The base grid reads the bounded Reading list, not a generic browse pivot.
    assert.equal('pivot' in libraryTab(kind, 'browse'), false);
    // Magazines, Newspapers, Series and Authors stay absent until their facts and routes exist.
    assert.ok(!libraryTabs(kind).some(tab => ['authors', 'series'].includes(tab.id)));
  }
});

const readingCapabilities = (kind: string, pivots: ReadonlyArray<{id: string; browsable?: boolean; members?: boolean; facetSource?: {endpoint: string; field: 'author' | 'series'}}>) => ({
  library: {id: 'lib-reading', name: 'Books', kind, defaultView: 'discover', pinned: false},
  pivots: pivots.map(pivot => ({id: pivot.id, labelKey: `library.tab.${pivot.id}`, entityKinds: [], defaultSort: [], supportedViews: ['list'],
    browsable: pivot.browsable === true, ...(pivot.members ? {members: true} : {}), ...(pivot.facetSource ? {facetSource: pivot.facetSource} : {})})),
  fields: [], sorts: [], quickFilters: [],
  queryLimits: {maximumDepth: 4, maximumClauses: 8, maximumBytes: 4096, maximumSorts: 2, defaultLimit: 50, maximumLimit: 200, cursorTtlMs: 60000},
}) as unknown as import('../src/browse.ts').BrowseCapabilities;

const facet = (field: 'author' | 'series') => ({endpoint: '/v1/libraries/{id}/facets', field});

test('a book library takes its published pivots in order: Books, then the Authors and Series facets', () => {
  const tabs = readingTabModel(readingCapabilities('book', [
    {id: 'books', browsable: true}, {id: 'authors', facetSource: facet('author')}, {id: 'series', facetSource: facet('series')},
  ]))!;
  assert.deepEqual(tabs.map(tab => tab.id), ['discover', 'browse', 'authors', 'series']);
  assert.equal(tabs[1]!.label, 'library.tab.books');
  assert.equal(tabs[1]!.pivot, 'books');
  assert.equal(tabs[2]!.shows, 'facet');
  assert.deepEqual(tabs[2]!.facetSource, facet('author'));
  assert.deepEqual(tabs[3]!.facetSource, facet('series'));
});

test('a comic library keeps the drawn order: Series, Volumes and issues, Authors', () => {
  const tabs = readingTabModel(readingCapabilities('comic', [
    {id: 'series', facetSource: facet('series')}, {id: 'comics', browsable: true}, {id: 'authors', facetSource: facet('author')},
  ]))!;
  assert.deepEqual(tabs.map(tab => tab.id), ['discover', 'series', 'browse', 'authors']);
  assert.equal(tabs[2]!.label, 'library.tab.volumesAndIssues');
  assert.equal(tabs[2]!.pivot, 'comics');
});

test('a server without facet sources offers no Authors or Series, and no other kind is affected', () => {
  const tabs = readingTabModel(readingCapabilities('book', [{id: 'books', browsable: true}, {id: 'authors'}, {id: 'series'}]))!;
  assert.deepEqual(tabs.map(tab => tab.id), ['discover', 'browse']);
  assert.equal(readingTabModel(readingCapabilities('movie', [{id: 'movies', browsable: true}])), undefined);
  assert.equal(readingTabModel(undefined), undefined);
});

test('a member pivot never becomes a reading tab', () => {
  const tabs = readingTabModel(readingCapabilities('book', [
    {id: 'books', browsable: true}, {id: 'authors', members: true, facetSource: facet('author')},
  ]))!;
  assert.deepEqual(tabs.map(tab => tab.id), ['discover', 'browse']);
});
