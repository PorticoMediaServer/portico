import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryItemQuery} from '../src/library-browse.ts';
import {readReadingBrowseFacet} from '../src/query/browse-reads.ts';

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
