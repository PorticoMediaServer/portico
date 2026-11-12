import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryBrowseCapabilities, libraryItemQuery} from '../src/library-browse.ts';
import {parseBrowseCapabilities} from '../src/browse.ts';
import {filterControlOf, filterValuesField} from '../src/presentation/browse-toolbar.ts';
import {appliedFilterParts} from '../src/presentation/kit-model/filters.ts';
import {filterValueName} from '../src/filter-value-names.ts';
import {nameFilterValues, readBrowseFacetResponse, readReadingBrowseFacet} from '../src/query/browse-reads.ts';

// A Read library's filters on the rebuilt server: choosing an author or a series narrows its list,
// and the filter menu's author choices are the library's people by the author role.
test('a chosen author or series narrows a Read library\'s list', () => {
  const books = {id: 'lib_books', views: [{kind: 'book'}]};
  assert.deepEqual(libraryItemQuery(books, 'books', [{variant: 'predicate', field: 'author', operator: 'equals', value: 'per_1'}], []),
    {library: 'lib_books', kind: 'book', author: 'per_1'});
  assert.deepEqual(libraryItemQuery(books, 'books', [{variant: 'predicate', field: 'series', operator: 'equals', value: 'itm_dune'}], []),
    {library: 'lib_books', kind: 'book', series: 'itm_dune'});
});

test('the filter menu\'s authors are the library\'s people; its other filters are the library\'s own counted choices', async () => {
  const asked: URL[] = [];
  const api = {request: async (path: string) => {
    const url = new URL(path, 'http://server.test'); asked.push(url);
    return url.pathname === '/v1/people' ? {items: [{id: 'per_1', name: 'Jane Doe', images: {}, titleCount: 2}], total: 1}
      : {facet: 'genre', values: [{value: 'tag_1', label: 'Mystery', count: 4}]};
  }};
  const lifetime = {signal: new AbortController().signal, isCurrent: () => true};
  const authors = await readReadingBrowseFacet(api as never, 'lib_books', 'author', 'books', undefined, '', 200, lifetime);
  assert.deepEqual(authors.values.map(value => [value.value, value.label, value.count]), [['per_1', 'Jane Doe', 2]]);
  const genres = await readReadingBrowseFacet(api as never, 'lib_books', 'genre', 'books', undefined, '', 200, lifetime);
  assert.equal(genres.values[0]!.label, 'Mystery');
  assert.deepEqual(asked.map(url => url.pathname), ['/v1/people', '/v1/facets']);
});

// The filter menu's own control, end to end: the server lists an author filter for a Books
// library, the menu draws it as a list of values read from the library's people, and ticking one
// or several authors narrows the list by any of them.
test('authors ticked in the filter menu narrow the list by any of them; a series link narrows it to that series', () => {
  const library = {id: 'lib_books', name: 'Books', kind: 'books', views: [{kind: 'book'}], sorts: [{id: 'title', order: 'asc'}],
    filters: [{id: 'genre', type: 'tag'}, {id: 'author', type: 'person'}]};
  const caps = parseBrowseCapabilities(libraryBrowseCapabilities(library as never, 'books'));
  const author = caps.fields.find(field => field.id === 'author');
  assert.ok(author, 'the menu offers the author filter the server lists');
  assert.equal(filterControlOf(author!), 'values');
  const control = filterValuesField(author!);
  assert.equal(control.facet, 'author');
  const ticked = (ids: string[]) => libraryItemQuery(library as never, 'books', [{variant: 'predicate', field: control.field, operator: control.operator as never, value: ids}], []);
  assert.equal(ticked(['per_1']).author, 'per_1');
  assert.equal(ticked(['per_1', 'per_2']).author, 'any:per_1,per_2');
  assert.equal(libraryItemQuery(library as never, 'books', [{variant: 'predicate', field: 'author', operator: 'contains-all', value: ['per_1', 'per_2']}], []).author, 'all:per_1,per_2');
  // A link to a series (a book page's "More in this series") writes contains-any with its one id.
  assert.equal(libraryItemQuery(library as never, 'books', [{variant: 'predicate', field: 'series', operator: 'contains-any', value: ['itm_dune']}], []).series, 'itm_dune');
  assert.throws(() => libraryItemQuery(library as never, 'books', [{variant: 'predicate', field: 'series', operator: 'contains-any', value: ['itm_a', 'itm_b']}], []));
});

test('any library\'s author choices come from its people, not the counted facets', async () => {
  const asked: string[] = [];
  const api = {request: async (path: string) => {asked.push(new URL(path, 'http://server.test').pathname); return {items: [{id: 'per_1', name: 'A Narrator', images: {}, titleCount: 1}], total: 1};}};
  const page = await readBrowseFacetResponse(api as never, 'lib_audio', 'author', 'books', undefined, '', 200);
  assert.deepEqual(asked, ['/v1/people']);
  assert.equal((page as {values: {value: string}[]}).values[0]!.value, 'per_1');
});

test('a chosen author\'s chip says the author\'s name, not the id the list is filtered by', async () => {
  const api = {request: async () => ({items: [{id: 'per_named', name: 'Jane Doe', images: {}, titleCount: 2}], total: 1})};
  await readReadingBrowseFacet(api as never, 'lib_books', 'author', 'books', undefined, '', 200, {signal: new AbortController().signal, isCurrent: () => true});
  const parts = appliedFilterParts({field: 'author', operator: 'contains-any', value: ['per_named']}, () => 'Author', {t: ((id: string) => id) as never}, value => filterValueName(value) ?? String(value));
  assert.deepEqual(parts.values.map(value => value.text), ['Jane Doe']);
  assert.equal(filterValueName('per_never_listed'), undefined);
});

test('an author or series in use that the lists have not shown is named by reading it on its own', async () => {
  const asked: string[] = [];
  const api = {request: async (path: string) => {
    const {pathname} = new URL(path, 'http://server.test'); asked.push(pathname);
    if (pathname === '/v1/people/per_far') return {id: 'per_far', name: 'Zadie Far', images: {}};
    if (pathname === '/v1/items/itm_far') return {id: 'itm_far', kind: 'series', title: 'Far Series', libraryId: 'lib_books', addedAt: '2026-11-12T00:00:00Z', images: {}};
    throw new Error('not_found');
  }};
  await nameFilterValues(api, 'author', ['per_far', 'per_gone']);
  await nameFilterValues(api, 'series', ['itm_far']);
  assert.deepEqual(asked, ['/v1/people/per_far', '/v1/people/per_gone', '/v1/items/itm_far']);
  assert.deepEqual([filterValueName('per_far'), filterValueName('per_gone'), filterValueName('itm_far')], ['Zadie Far', undefined, 'Far Series']);
});
